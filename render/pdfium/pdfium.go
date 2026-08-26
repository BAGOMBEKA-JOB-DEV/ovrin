package pdfium

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/klippa-app/go-pdfium"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
)

// providerName is what appears in the Provider field of every error this
// package returns, so a log line names the adapter and not just the stage.
const providerName = "pdfium"

// pointsPerInch is fixed by the typographic point. PDF page geometry is in
// points, and DPI is per inch, so this is the only constant in the conversion.
const pointsPerInch = 72.0

// Renderer rasterises PDF pages with PDFium running as WebAssembly.
//
// The zero value is not usable; call [New].
//
// A Renderer is safe for concurrent use by multiple goroutines. PDFium itself
// is not thread-safe, so safety comes from never sharing one: the Renderer
// holds a fixed set of PDFium instances, and a Render borrows one for the
// length of the call and returns it afterwards. Concurrency is therefore
// bounded by the instance count ([WithInstances]) rather than by the number of
// callers — a Render that arrives when every instance is busy waits for one,
// or for its context to end, whichever comes first.
//
// Each instance gets not just its own WebAssembly module but its own Wazero
// runtime, which is stronger isolation than it first appears to need. Two
// module instances sharing a runtime also share the host functions Emscripten
// imports, and those cache a computed signature on first use — a write that
// two concurrent renders race on. Wazero's compiled-code cache is shared
// across the runtimes instead, so the isolation costs memory rather than the
// second of compilation it would otherwise cost per instance.
//
// Call [Renderer.Close] when finished. Until it is called the compiled
// WebAssembly module and every instance stay resident.
type Renderer struct {
	maxPagePixels int
	instances     int

	// initOnce guards lazy construction. Compiling four megabytes of
	// WebAssembly takes about a second, and [New] cannot report an error, so
	// the cost is paid on the first Render by whoever asks for one — and not
	// at all by a program that constructs a Renderer it never uses.
	initOnce sync.Once
	initErr  error

	// cache holds the compiled PDFium machine code, shared by every runtime so
	// that the second and later runtimes are near-free to build.
	cache wazero.CompilationCache

	// pools holds one single-worker PDFium pool per instance, and free is the
	// semaphore that hands them out. One pool per instance means one wazero
	// runtime per instance, which is what makes concurrent rendering safe; see
	// the Renderer doc comment.
	pools []pdfium.Pool
	free  chan pdfium.Pool

	// mu guards closed, and orders it against inflight so that no work can be
	// registered after Close has begun waiting.
	mu       sync.RWMutex
	closed   bool
	inflight sync.WaitGroup
}

// Option configures a [Renderer].
//
// Options rather than an exported config struct, so fields can be added
// without breaking callers, and so nothing here reads the environment: a
// library that consults os.Getenv is how a program renders with settings
// nobody in the call chain chose (docs/rules.md §6.4).
type Option func(*Renderer)

// WithMaxPagePixels bounds the size of one rasterised page.
//
// The check is made against the page's declared media box before any bitmap is
// allocated, so a page claiming an implausible size costs a page-tree lookup
// rather than an allocation larger than memory. The default is
// [ovrin.DefaultMaxPagePixels]. A value of zero or less disables the ceiling,
// which is only ever right for a corpus you produced yourself.
func WithMaxPagePixels(n int) Option {
	return func(r *Renderer) { r.maxPagePixels = n }
}

// WithInstances sets how many PDFium instances may exist at once, which is the
// maximum number of pages this Renderer will rasterise in parallel.
//
// Each instance is a separate WebAssembly module with its own linear memory,
// so the count trades memory for parallelism directly. The default is
// min(4, GOMAXPROCS), matching ovrin's own concurrency default so that a
// Renderer does not monopolise a host it shares. Values below one are treated
// as one.
func WithInstances(n int) Option {
	return func(r *Renderer) { r.instances = n }
}

// New returns a Renderer.
//
// It takes no credential because there is no service to authenticate to —
// everything runs in this process. It performs no work and cannot fail; the
// WebAssembly module is compiled on the first [Renderer.Render].
func New(opts ...Option) *Renderer {
	r := &Renderer{
		maxPagePixels: ovrin.DefaultMaxPagePixels,
		instances:     defaultInstances(),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.instances < 1 {
		r.instances = 1
	}
	return r
}

// defaultInstances mirrors ovrin's own concurrency default, min(4, GOMAXPROCS).
func defaultInstances() int {
	n := runtime.GOMAXPROCS(0)
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Render rasterises one page of doc at the given resolution.
//
// page is 1-based, matching [ovrin.Page.Number] and [ovrin.Error.Page]. The
// returned image is an *image.RGBA that owns its pixels: PDFium hands back a
// view into WebAssembly linear memory that is freed when the call's resources
// are released, so the pixels are copied out before that happens.
//
// Rendering is not interruptible once PDFium has started on a page — no PDFium
// entry point takes a context. Cancelling ctx therefore makes Render return
// promptly with the context's error, while the rasterisation it abandoned runs
// to completion in the background and releases its own resources. Those
// resources are accounted for: [Renderer.Close] waits for abandoned work to
// finish before shutting the pool down.
func (r *Renderer) Render(ctx context.Context, doc ovrin.Document, page, dpi int) (image.Image, error) {
	if err := r.check(doc, page, dpi); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, cancelled(page, err)
	}

	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return nil, renderErr(page, ovrin.ErrInternal, "the renderer is closed")
	}
	r.inflight.Add(1)
	r.mu.RUnlock()

	// A buffered channel, so the worker can always deliver and exit even when
	// nobody is listening any more. An unbuffered one would keep the goroutine
	// — and the PDFium instance it is closing — alive until a reader that has
	// already returned came back for the result.
	done := make(chan result, 1)
	go func() {
		defer r.inflight.Done()
		done <- r.render(ctx, doc, page, dpi)
	}()

	select {
	case res := <-done:
		return res.img, res.err
	case <-ctx.Done():
		return nil, cancelled(page, ctx.Err())
	}
}

// result is what the worker goroutine reports back.
type result struct {
	img image.Image
	err error
}

// check rejects requests that need no PDFium instance to refuse.
//
// Doing it before the pool is touched means a typo costs nothing, and keeps
// the failures that are the caller's fault separate from the ones that are the
// document's.
func (r *Renderer) check(doc ovrin.Document, page, dpi int) error {
	switch {
	case doc.Kind != ovrin.KindPDF && doc.Kind != ovrin.KindUnknown:
		return renderErr(page, ovrin.ErrUnsupportedFormat,
			fmt.Sprintf("this renderer reads PDF, not %s", doc.Kind))
	case len(doc.Data) == 0:
		return renderErr(page, ovrin.ErrNoContent, "the document has no bytes")
	case page < 1:
		return renderErr(page, ovrin.ErrInternal,
			fmt.Sprintf("page %d is not a page number; pages are 1-based", page))
	case dpi < 1:
		return renderErr(page, ovrin.ErrInternal,
			fmt.Sprintf("dpi %d is not a resolution", dpi))
	}
	return nil
}

// render does the blocking work. It runs on its own goroutine and is
// responsible for releasing everything it acquires, on every path, because the
// caller may already have returned on a cancelled context.
func (r *Renderer) render(ctx context.Context, doc ovrin.Document, page, dpi int) result {
	if err := r.ensurePools(); err != nil {
		return result{err: err}
	}

	// Wait for a free instance, or for the caller to give up.
	var pool pdfium.Pool
	select {
	case pool = <-r.free:
	case <-ctx.Done():
		return result{err: cancelled(page, ctx.Err())}
	}
	defer func() { r.free <- pool }()

	inst, err := pool.GetInstance(instanceTimeout)
	if err != nil {
		return result{err: renderErr(page, ovrin.ErrInternal,
			"a PDFium instance could not be taken from its pool").WithCause(err)}
	}
	defer inst.Close()

	// A byte slice the caller owns is handed to PDFium as a reference. It is
	// read-only for the length of this call, which is what ovrin.Document.Data
	// already promises.
	data := doc.Data
	opened, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		return result{err: openErr(page, err)}
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: opened.Document})

	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
	if err != nil {
		return result{err: renderErr(page, ovrin.ErrNoContent, "the page count could not be read")}
	}
	if count.PageCount < 1 {
		return result{err: renderErr(page, ovrin.ErrNoContent, "the document has no pages")}
	}
	if page > count.PageCount {
		return result{err: renderErr(page, ovrin.ErrInternal,
			fmt.Sprintf("page %d is outside the document; it has %d page(s), so the range is 1 to %d",
				page, count.PageCount, count.PageCount))}
	}

	ref := requests.Page{ByIndex: &requests.PageByIndex{
		Document: opened.Document,
		Index:    page - 1,
	}}

	// The ceiling is checked against the declared media box, before PDFium is
	// asked for a bitmap. Discovering an eight-gigapixel page by trying to
	// allocate it is how a malformed document takes the process down
	// (docs/adr/0020-resource-limits.md).
	size, err := inst.GetPageSize(&requests.GetPageSize{Page: ref})
	if err != nil {
		return result{err: pageErr(page, err)}
	}
	if err := r.checkPixels(page, size.Width, size.Height, dpi); err != nil {
		return result{err: err}
	}
	if err := ctx.Err(); err != nil {
		return result{err: cancelled(page, err)}
	}

	rendered, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{Page: ref, DPI: dpi})
	if err != nil {
		return result{err: pageErr(page, err)}
	}
	defer rendered.Cleanup()

	if rendered.Result.Image == nil {
		return result{err: renderErr(page, ovrin.ErrNoContent, "the page produced no bitmap")}
	}
	return result{img: copyRGBA(rendered.Result.Image)}
}

// checkPixels refuses a page whose declared size exceeds the ceiling.
//
// The comparison is by division rather than multiplication so that dimensions
// large enough to overflow cannot wrap around and land under the ceiling — the
// same guard internal/img uses on image headers.
func (r *Renderer) checkPixels(page int, widthPt, heightPt float64, dpi int) error {
	if r.maxPagePixels <= 0 {
		return nil
	}
	if widthPt <= 0 || heightPt <= 0 {
		return renderErr(page, ovrin.ErrNoContent, "the page declares no area")
	}

	scale := float64(dpi) / pointsPerInch
	w := math.Ceil(widthPt * scale)
	h := math.Ceil(heightPt * scale)

	// Compare in float64 first: a media box large enough that the pixel count
	// exceeds an int64 would overflow the integer comparison it is about to
	// feed. float64 is exact well past any plausible bitmap.
	if w*h > float64(r.maxPagePixels) {
		return renderErr(page, ovrin.ErrLimitExceeded,
			fmt.Sprintf("page is %.0f×%.0f pixels at %d dpi, maximum %d, raise with WithMaxPagePixels",
				w, h, dpi, r.maxPagePixels))
	}
	return nil
}

// copyRGBA copies a rendered page out of WebAssembly linear memory.
//
// go-pdfium hands back an *image.RGBA whose Pix is a view into the module's
// memory, valid only until the render's resources are released. Returning it
// would be a use-after-free that reads as intermittently corrupt pixels rather
// than as a crash, which is worse.
func copyRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Rect)
	if dst.Stride == src.Stride {
		copy(dst.Pix, src.Pix)
		return dst
	}
	rows := src.Rect.Dy()
	width := 4 * src.Rect.Dx()
	for y := 0; y < rows; y++ {
		s := y * src.Stride
		d := y * dst.Stride
		copy(dst.Pix[d:d+width], src.Pix[s:s+width])
	}
	return dst
}

// ensurePools compiles the WebAssembly module and builds one single-worker
// pool — one Wazero runtime — per instance, at most once, remembering the
// failure if there was one.
func (r *Renderer) ensurePools() error {
	r.initOnce.Do(func() {
		// One compilation, shared by every runtime. Without this each runtime
		// would compile four megabytes of WebAssembly for itself.
		r.cache = wazero.NewCompilationCache()

		r.pools = make([]pdfium.Pool, 0, r.instances)
		r.free = make(chan pdfium.Pool, r.instances)
		for i := 0; i < r.instances; i++ {
			pool, err := webassembly.Init(webassembly.Config{
				MinIdle:  1,
				MaxIdle:  1,
				MaxTotal: 1,

				RuntimeConfig: wazero.NewRuntimeConfig().WithCompilationCache(r.cache),

				// The default mounts the host's root directory into the
				// sandbox. An empty FSConfig gives the module no filesystem at
				// all, which is the point of running a large C library in a
				// sandbox: a PDFium memory-safety bug then cannot reach the
				// host's files. PDFium falls back to its built-in fonts.
				FSConfig: wazero.NewFSConfig(),

				// PDFium writes parse diagnostics to stderr, and a diagnostic
				// about a malformed object can quote the object. Document
				// content never reaches a log (docs/rules.md §7.5), so it goes
				// nowhere.
				Stdout: io.Discard,
				Stderr: io.Discard,

				// Reuse the worker rather than instantiating a module per
				// render. Instantiation is cheap next to compilation but not
				// free, and a bulk scan is thousands of renders. Every
				// document is closed before its instance is returned, so
				// nothing carries over.
				ReuseWorkers: true,
			})
			if err != nil {
				r.closePools()
				r.initErr = renderErr(0, ovrin.ErrInternal,
					"the PDFium WebAssembly module could not be started").WithCause(err)
				return
			}
			r.pools = append(r.pools, pool)
			r.free <- pool
		}
	})
	return r.initErr
}

// closePools shuts every pool down and releases the compiled code. It is only
// safe to call when no render holds an instance.
func (r *Renderer) closePools() error {
	var firstErr error
	for _, pool := range r.pools {
		if err := pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.pools = nil
	if r.cache != nil {
		if err := r.cache.Close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
		r.cache = nil
	}
	return firstErr
}

// instanceTimeout bounds taking the single worker out of its own pool.
//
// This is not a rendering timeout — an adapter does not invent one
// (docs/rules.md §6.2). Waiting for an instance is handled by the Renderer's
// own semaphore, which honours the caller's context; by the time this is
// called the pool is known to be free, so any wait at all means the pool is
// broken rather than busy, and a finite bound turns that into an error instead
// of a hang.
const instanceTimeout = 30 * time.Second

// openErr classifies a failure to open the document.
//
// PDFium's conditions are sentinel values, so they are matched with errors.Is
// rather than by reading a message that a future release may reword
// (docs/rules.md §2.2).
func openErr(page int, err error) error {
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword), errors.Is(err, pdfiumerrors.ErrSecurity):
		return renderErr(page, ovrin.ErrEncrypted,
			"the PDF is encrypted; this renderer has no password support").WithCause(err)
	case errors.Is(err, pdfiumerrors.ErrFormat):
		return renderErr(page, ovrin.ErrUnsupportedFormat,
			"PDFium does not recognise these bytes as a PDF").WithCause(err)
	case errors.Is(err, pdfiumerrors.ErrFile):
		return renderErr(page, ovrin.ErrNoContent,
			"the PDF could not be read").WithCause(err)
	case errors.Is(err, pdfiumerrors.ErrPage):
		return renderErr(page, ovrin.ErrNoContent,
			"the PDF's page tree could not be read").WithCause(err)
	default:
		return renderErr(page, ovrin.ErrNoContent,
			"PDFium could not open the document").WithCause(err)
	}
}

// pageErr classifies a failure on a page that opened.
func pageErr(page int, err error) error {
	if errors.Is(err, pdfiumerrors.ErrPage) {
		return renderErr(page, ovrin.ErrNoContent,
			"the page could not be loaded").WithCause(err)
	}
	return renderErr(page, ovrin.ErrNoContent,
		"the page could not be rasterised").WithCause(err)
}

// renderErr builds an ovrin.Error for this adapter.
//
// The message says what happened and never what the document said. PDFium's
// own error text is attached as a cause rather than printed, so a diagnostic
// that quotes a malformed object cannot reach a log line (docs/rules.md §2.5).
func renderErr(page int, kind error, message string) *ovrin.Error {
	return &ovrin.Error{
		Op:       ovrin.OpRender,
		Provider: providerName,
		Page:     page,
		Kind:     kind,
		Message:  message,
	}
}

// cancelled reports a context that ended, matching how the core classifies the
// same condition: ErrUnavailable, with the context error reachable through
// errors.Is.
func cancelled(page int, err error) error {
	return renderErr(page, ovrin.ErrUnavailable,
		"the context ended before the page was rendered").WithCause(err)
}

// Close releases the WebAssembly runtime and every pooled instance.
//
// It waits for renders still in flight, including ones whose caller has
// already returned on a cancelled context — those still hold a PDFium instance
// until PDFium finishes with the page, and tearing the runtime out from under
// them would turn an abandoned render into a crash.
//
// Close is idempotent in the sense that matters: calling it twice is safe, and
// a Render after it fails rather than reviving the pool.
func (r *Renderer) Close() error {
	r.mu.Lock()
	already := r.closed
	r.closed = true
	r.mu.Unlock()
	if already {
		return nil
	}

	r.inflight.Wait()

	if err := r.closePools(); err != nil {
		return renderErr(0, ovrin.ErrInternal,
			"the PDFium runtimes did not shut down cleanly").WithCause(err)
	}
	return nil
}

var _ ovrin.Renderer = (*Renderer)(nil)
