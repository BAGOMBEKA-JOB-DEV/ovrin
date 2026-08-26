// Package tesseract reads pages with a local Tesseract that needs no cgo: the
// engine is Tesseract compiled to WebAssembly and run by a pure-Go runtime, so
// `CGO_ENABLED=0 go build` and cross-compilation keep working exactly as they
// do for ovrin's core (rule §4.3, [ADR-0010]).
//
//	import (
//	    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
//	    ovrintesseract "github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/tesseract"
//	)
//
//	ocr := ovrintesseract.New(ovrintesseract.WithTrainingData(engTraineddata))
//	defer ocr.Close()
//
//	c := ovrin.New(ovrin.WithOCR(ocr))
//
// With a local renderer this is the fully offline path: a scanned PDF read
// with no network call and no credential, which is the deployment the users
// this library was written for cannot do without.
//
// # Why WebAssembly rather than the cgo binding
//
// github.com/otiai10/gosseract is the usual binding and it is cgo. cgo costs
// the two things people choose Go for here: a static binary, and building for
// another platform without that platform's toolchain. A government or
// healthcare deployment that must run offline is exactly the deployment that
// ships a distroless image and cross-compiles, and telling it to install
// libtesseract-dev and libleptonica-dev in its build stage undoes most of the
// benefit of the offline path existing at all.
//
// github.com/danlock/gogosseract runs the same Tesseract — the LSTM engine,
// compiled to WebAssembly — under github.com/tetratelabs/wazero. The costs are
// real and are named here rather than in a release note:
//
//   - Six dependencies this module inherits and hands to everyone who imports
//     it, one of which is golang.org/x/exp. The core module's zero-dependency
//     rule does not reach an adapter, but the reason behind it does.
//   - The upstream module is v0.0.x, maintained by one person, and the
//     WebAssembly is built from a personal fork of another personal project.
//   - Roughly 27 MB of module download, most of it the compiled engine.
//   - Slower than native Tesseract, by a factor that depends on the page.
//   - The "classic" pre-LSTM engine is not compiled in. It is obsolete, and
//     nothing here exposes a way to ask for it, so this costs no caller
//     anything today.
//   - One traineddata file per provider, so more than one language at a time
//     is refused rather than served badly. See [WithLanguages].
//
// # Training data
//
// Tesseract cannot read anything without a traineddata file, and this package
// never reads the environment to find one (rule §6.4) — no TESSDATA_PREFIX, no
// os.Getenv anywhere. There are two ways to supply it:
//
//   - [WithTrainingData], which takes the bytes. This is the reproducible one,
//     and with go:embed it makes a single binary that OCRs with no filesystem
//     dependency at all.
//   - [WithTessdataDirs], or the documented defaults in [DefaultTessdataDirs],
//     which read <language>.traineddata off disk. A language that is not there
//     is an [ovrin.ErrUnsupported] naming the language and every directory
//     that was searched.
//
// # What this adapter silently ignores
//
// Rule §6.5 asks every adapter to document that, not only what it supports.
//
//   - Per-character geometry and confidence. Tesseract can report both;
//     [ovrin.Word] is the smallest unit ovrin models. Reachable through the
//     hOCR on [Recognised].
//   - Baselines, x-heights, ascender and descender metrics, and per-line
//     confidence. Same route.
//   - Block and paragraph structure. Blocks are sorted into reading order and
//     then flattened into lines; the boundaries themselves are dropped.
//   - Orientation and script detection. The WebAssembly build has no OSD
//     model, so a sideways page is read sideways rather than being rotated.
//   - Word-level language. Tesseract reports the model that produced a word,
//     and ovrin has one language field per page.
//
// # What it refuses rather than degrading
//
//   - A page with no image, or with no size in points. There is no way to
//     return page-point coordinates for a page that does not say how big it
//     is, and returning pixels labelled as points is exactly the silent
//     degradation rule §6.1 forbids.
//   - More than one language. Tesseract can read `eng+swa`, but the
//     WebAssembly build loads a single traineddata file per instance, and
//     reading a bilingual page with only one of its two models is a worse
//     answer than the caller asked for.
//   - A missing traineddata file, naming the language and where it looked.
//
// # What it does not report
//
// [ovrin.Recognition.Language] is always empty. Tesseract does not detect a
// language — it is told one — and reporting the model it was handed as the
// language it found would be a fabrication of the kind rule §8.5 exists to
// prevent. The language it was asked to use is on [Recognised.Language].
//
// # Retry
//
// There is none, deliberately. Retry, backoff, fallback and timeouts belong to
// ovrin's core so that they are decided once rather than once per adapter
// (rule §6.2).
//
// # Concurrency and lifetime
//
// A [Provider] is safe for concurrent use by multiple goroutines. Tesseract
// itself is not — neither the C API nor this WebAssembly build — so a Provider
// owns a pool of independent engine instances, each used by one call at a
// time, and [WithInstances] sets how many. Concurrent calls beyond that number
// queue rather than fail.
//
// The pool is built on the first [Provider.Recognise] and lives until
// [Provider.Close], which is not optional: a Provider that is dropped without
// it leaks the engine's memory and its worker goroutines for the life of the
// process.
//
// [ADR-0010]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/blob/main/docs/adr/0010-no-cgo-in-core.md
package tesseract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/danlock/gogosseract"
)

// providerName appears in [ovrin.Provenance.Method] and on every error, so a
// result carries the evidence of which engine read it.
//
// It names the engine rather than the binding: a page read by this package and
// a page read by a cgo Tesseract adapter were read by the same OCR, and a
// provenance record that distinguished them would be recording an
// implementation detail as a fact about the document.
const providerName = "tesseract"

// DefaultLanguage is the traineddata a [Provider] looks for when none is
// configured. It matches Tesseract's own default.
const DefaultLanguage = "eng"

// DefaultInstances is how many engine instances a [Provider] builds.
//
// One, because each instance holds a full copy of the model and of the page
// being read, and a library that quietly allocated one per core would be the
// largest allocation in most callers' processes. Raise it with [WithInstances]
// when pages are actually being read concurrently.
const DefaultInstances = 1

// DefaultTessdataDirs are the directories searched for <language>.traineddata
// when [WithTessdataDirs] is not used and [WithTrainingData] is not set.
//
// These are the paths the Tesseract packages of the common distributions
// install into. The list is a constant rather than a lookup of TESSDATA_PREFIX
// because a library that reads the environment is how a program ends up using
// the wrong model (rule §6.4) — and unlike a credential, a wrong model is not
// an error, it is a page of plausible nonsense.
var DefaultTessdataDirs = []string{
	"/usr/share/tessdata",
	"/usr/share/tesseract-ocr/5/tessdata",
	"/usr/share/tesseract-ocr/4.00/tessdata",
	"/usr/local/share/tessdata",
	"/usr/local/share/tesseract-ocr/5/tessdata",
	"/opt/homebrew/share/tessdata",
}

// Provider reads pages with a local Tesseract.
//
// It is safe for concurrent use by multiple goroutines: the configuration is
// immutable after construction, and the engine instances are handed out one
// call at a time by the pool. See the package documentation on lifetime —
// [Provider.Close] is required.
type Provider struct {
	languages []string
	dirs      []string
	data      []byte
	instances int
	variables map[string]string

	// mu guards everything below it. The pool is built on first use because
	// building it means compiling a WebAssembly module and loading a model,
	// which takes long enough that doing it in New would make constructing a
	// provider a blocking call with no context to cancel it.
	mu       sync.Mutex
	pool     *gogosseract.Pool
	starting chan struct{}
	startErr error
	closed   bool
}

// Option configures a [Provider]. Options are applied in order.
type Option func(*Provider)

// WithLanguages selects the traineddata Tesseract reads with.
//
// It is variadic to match Tesseract's own `eng+swa` and ovrin's other
// adapters, and this build accepts exactly one: the WebAssembly engine loads a
// single traineddata file per instance. More than one is refused by
// [Provider.Recognise] with [ovrin.ErrUnsupported] naming the languages, since
// reading a bilingual page with one of its two models is precisely the quietly
// worse answer rule §6.1 forbids. An empty call is ignored.
//
// The refusal surfaces at Recognise rather than here because [New] returns no
// error — the cost of the functional-option shape, paid once.
func WithLanguages(codes ...string) Option {
	return func(p *Provider) {
		out := make([]string, 0, len(codes))
		for _, c := range codes {
			if c = strings.TrimSpace(c); c != "" {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			p.languages = out
		}
	}
}

// WithTessdataDirs replaces the directories searched for
// <language>.traineddata, in order.
//
// Use it when the models live somewhere this package would not think to look —
// a read-only mount in a container, or a directory shipped alongside the
// binary. An empty call is ignored, so a caller reading a path out of their own
// configuration does not have to branch on it being unset.
func WithTessdataDirs(dirs ...string) Option {
	return func(p *Provider) {
		out := make([]string, 0, len(dirs))
		for _, d := range dirs {
			if d = strings.TrimSpace(d); d != "" {
				out = append(out, d)
			}
		}
		if len(out) > 0 {
			p.dirs = out
		}
	}
}

// WithTrainingData supplies the traineddata directly, skipping the filesystem
// search entirely.
//
// This is the reproducible option and the one to prefer. Combined with an
// [embed.FS] it produces a single binary that reads scans with no filesystem
// dependency, no package manager and no network — which is the whole claim
// this module exists to make.
//
// The slice is retained, not copied, and must not be modified afterwards. It
// is read once, when the engine is built.
func WithTrainingData(data []byte) Option {
	return func(p *Provider) {
		if len(data) > 0 {
			p.data = data
		}
	}
}

// WithInstances sets how many engine instances the provider runs, and
// therefore how many pages it reads at once.
//
// Each instance holds its own copy of the model and of the page being read, so
// this is a memory decision as much as a throughput one. Values below one are
// ignored rather than clamped silently to a different meaning.
func WithInstances(n int) Option {
	return func(p *Provider) {
		if n > 0 {
			p.instances = n
		}
	}
}

// WithVariables sets Tesseract configuration variables on every instance —
// tessedit_pageseg_mode for a page that is one column, or
// tessedit_char_whitelist for a field that is only ever digits.
//
// This is a deliberate hole in rule §6.2's "adapters map, they do not decide":
// the variables change what Tesseract does rather than how ovrin uses it, and
// there is no ovrin-level vocabulary that could express them. The map is
// copied, so the caller may reuse it.
func WithVariables(vars map[string]string) Option {
	return func(p *Provider) {
		if len(vars) == 0 {
			return
		}
		out := make(map[string]string, len(vars))
		for k, v := range vars {
			out[k] = v
		}
		p.variables = out
	}
}

// New returns a provider backed by a local Tesseract.
//
// It takes no credential, because there is nothing to authenticate against:
// rule §6.4's shape for an adapter that needs none. It does no work and cannot
// fail — the engine is built on the first [Provider.Recognise], where there is
// a context to cancel it with.
//
// The returned Provider is safe for concurrent use by multiple goroutines, and
// must be closed with [Provider.Close].
func New(opts ...Option) *Provider {
	p := &Provider{
		languages: []string{DefaultLanguage},
		dirs:      DefaultTessdataDirs,
		instances: DefaultInstances,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements [ovrin.OCR]. It appears in [ovrin.Provenance.Method], so a
// result records that this engine read it.
func (p *Provider) Name() string { return providerName }

// Recognise implements [ovrin.OCR], reading one rasterised page.
//
// The page's Width and Height are what the returned coordinates are expressed
// in: Tesseract reports boxes in the pixel space of the image it was given,
// and every one of them is scaled into the page's own points before it is
// returned. Its confidences are on 0..100 and are divided (ADR-0009).
//
// The first call builds the engine, which is the slow one. A cancelled context
// abandons that wait without abandoning the engine — a half-built pool is torn
// down, and a finished one is kept for the next caller rather than being
// rebuilt.
func (p *Provider) Recognise(ctx context.Context, page ovrin.Page) (*ovrin.Recognition, error) {
	if err := ctx.Err(); err != nil {
		return nil, p.fail(nil, page.Number,
			"the context ended before the page was read").WithCause(err)
	}
	if page.Image == nil {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page carries no image, and there is nothing to recognise")
	}
	if page.Width <= 0 || page.Height <= 0 {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page does not say how large it is in points, so a box could not "+
				"be converted out of tesseract's pixels")
	}
	if len(p.languages) != 1 {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			fmt.Sprintf("this build loads one traineddata file per engine and %d "+
				"languages are configured (%s); reading the page with one of them "+
				"would drop the rest",
				len(p.languages), strings.Join(p.languages, ", ")))
	}

	bounds := page.Image.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, p.fail(ovrin.ErrUnsupported, page.Number,
			"the page image has no pixels")
	}

	var encoded bytes.Buffer
	// PNG rather than JPEG: OCR reads glyph edges, and JPEG's artefacts sit
	// exactly there. Leptonica, which decodes the image inside the engine,
	// reads both.
	if err := png.Encode(&encoded, page.Image); err != nil {
		return nil, p.fail(ovrin.ErrInternal, page.Number,
			"the page image could not be encoded").WithCause(err)
	}

	pool, err := p.engine(ctx, page.Number)
	if err != nil {
		return nil, err
	}

	hocr, err := pool.ParseImage(ctx, bytes.NewReader(encoded.Bytes()),
		gogosseract.ParseImageOptions{IsHOCR: true})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// A context that ended is not an engine failure and gets no
			// sentinel: a fallback chain must not advance to the next provider
			// with a context that is already dead (ADR-0019).
			return nil, p.fail(nil, page.Number,
				"the context ended before the page was read").WithCause(ctxErr)
		}
		// The engine's own error is attached, never described. Tesseract's
		// messages are its own vocabulary today, and an adapter that copies a
		// message into its own is one release away from copying a fragment of
		// the document with it (rule §2.5).
		return nil, p.fail(ovrin.ErrUnavailable, page.Number,
			"the tesseract engine failed to read the page").WithCause(err)
	}

	parsed, err := parseHOCR(strings.NewReader(hocr))
	if err != nil && !errors.Is(err, errNoWords) {
		return nil, p.fail(ovrin.ErrBadResponse, page.Number,
			"the engine's hocr output could not be parsed").WithCause(err)
	}

	sp := newSpace(bounds.Dx(), bounds.Dy(), page.Width, page.Height)
	// A page with no words is a legitimate recognition — a blank scan, a page
	// of photographs — and whether that ends the extraction is the core's
	// decision, not an adapter's (rule §6.2, ADR-0004).
	return normalise(parsed, sp, page.Number, Recognised{
		HOCR:     hocr,
		Language: p.languages[0],
	}), nil
}

// Close releases the engine instances and their goroutines.
//
// It is safe to call more than once and from any goroutine, and it is
// required: the WebAssembly runtime holds the model and the page buffers, and
// nothing reclaims them when the Provider becomes unreachable. A Provider that
// has been closed refuses further work rather than silently rebuilding, since
// a rebuild would hide the bug that closed it too early.
//
// It returns an error only for the shape of the interface; the pool's teardown
// cannot fail.
func (p *Provider) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	starting := p.starting
	p.mu.Unlock()

	// Wait for a build in flight before tearing down, or its pool would be
	// stored after this returns and never released.
	if starting != nil {
		<-starting
	}

	p.mu.Lock()
	pool := p.pool
	p.pool = nil
	p.mu.Unlock()

	if pool != nil {
		pool.Close()
	}
	return nil
}

// engine returns the pool, building it on first use.
//
// The build runs on its own goroutine with a context this provider owns, not
// the caller's. Handing the caller's context to the pool would tie the
// engine's lifetime to whichever request happened to be first: that request
// finishing would shut down the engine underneath everyone else. The caller
// still gets cancellation — it stops waiting — and the build carries on for
// whoever asks next, which is the same trade every connection pool makes.
func (p *Provider) engine(ctx context.Context, page int) (*gogosseract.Pool, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, p.fail(ovrin.ErrUnavailable, page,
			"the provider has been closed")
	}
	if p.pool != nil {
		pool := p.pool
		p.mu.Unlock()
		return pool, nil
	}
	if p.starting == nil {
		p.starting = make(chan struct{})
		go p.start(p.starting)
	}
	starting := p.starting
	p.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, p.fail(nil, page,
			"the context ended while the tesseract engine was starting").
			WithCause(ctx.Err())
	case <-starting:
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.startErr != nil {
		return nil, p.startErr
	}
	if p.pool == nil {
		return nil, p.fail(ovrin.ErrUnavailable, page,
			"the provider was closed while the tesseract engine was starting")
	}
	return p.pool, nil
}

// start builds the pool and publishes it, then closes done.
//
// A failed build clears p.starting so the next caller retries: a missing
// traineddata file is a condition an operator fixes without restarting the
// process, and caching the failure forever would make that impossible.
func (p *Provider) start(done chan struct{}) {
	pool, err := p.build(context.Background())

	p.mu.Lock()
	switch {
	case err != nil:
		p.startErr = err
		p.starting = nil
	case p.closed:
		// Closed while we were building. The pool is torn down below rather
		// than stored, so the goroutines it started do not outlive Close.
		p.startErr = nil
	default:
		p.pool = pool
		p.startErr = nil
	}
	closed := p.closed
	p.mu.Unlock()

	close(done)

	if err == nil && closed {
		pool.Close()
	}
}

// build loads the traineddata and starts the engine instances.
func (p *Provider) build(ctx context.Context) (*gogosseract.Pool, error) {
	if len(p.languages) != 1 {
		return nil, p.fail(ovrin.ErrUnsupported, 0,
			fmt.Sprintf("this build loads one traineddata file per engine and %d "+
				"languages are configured (%s)",
				len(p.languages), strings.Join(p.languages, ", ")))
	}
	language := p.languages[0]

	data, err := p.trainingData(language)
	if err != nil {
		return nil, err
	}

	pool, err := gogosseract.NewPool(ctx, uint(p.instances), gogosseract.PoolConfig{
		Config: gogosseract.Config{
			Language:  language,
			Variables: p.variables,
		},
		TrainingDataBytes: data,
	})
	if err != nil {
		return nil, p.fail(ovrin.ErrUnavailable, 0,
			"the tesseract engine could not be started").WithCause(err)
	}
	return pool, nil
}

// trainingData returns the model bytes, from the option or from disk.
//
// The error names the language and every directory that was searched, which is
// the difference between a one-line fix and an afternoon. A path is
// configuration, never document content, so naming one is not a §2.5 leak.
func (p *Provider) trainingData(language string) ([]byte, error) {
	if len(p.data) > 0 {
		return p.data, nil
	}

	name := language + ".traineddata"
	var tried []string
	for _, dir := range p.dirs {
		path := filepath.Join(dir, name)
		tried = append(tried, path)
		data, err := os.ReadFile(path) //nolint:gosec // a path the caller configured
		switch {
		case err == nil && len(data) > 0:
			return data, nil
		case err == nil:
			// An empty file is not a model. Naming it beats "no traineddata
			// found" listing a path that plainly exists.
			return nil, p.fail(ovrin.ErrUnsupported, 0, fmt.Sprintf(
				"the traineddata for %q at %s is empty", language, path))
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return nil, p.fail(ovrin.ErrUnsupported, 0, fmt.Sprintf(
				"the traineddata for %q at %s could not be read", language, path)).
				WithCause(err)
		}
	}

	return nil, p.fail(ovrin.ErrUnsupported, 0, fmt.Sprintf(
		"no traineddata for %q; looked in %s. install the language pack, pass "+
			"WithTessdataDirs, or embed the model with WithTrainingData",
		language, describeDirs(tried)))
}

// fail builds a classified ovrin error naming this adapter and this stage.
//
// Every message here is written in this package and every one of them is a
// fixed string or a value this package chose — a language code, a filesystem
// path, a page number. An error carries the operation, the page and the
// provider, and nothing a document could occupy (rule §2.5, §7.5).
func (p *Provider) fail(kind error, page int, message string) *ovrin.Error {
	return &ovrin.Error{
		Op:       ovrin.OpOCR,
		Provider: providerName,
		Page:     page,
		Kind:     kind,
		Message:  message,
	}
}

var _ ovrin.OCR = (*Provider)(nil)
