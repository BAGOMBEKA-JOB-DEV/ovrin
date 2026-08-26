package pdfium_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/render/pdfium"
)

// The corpus is generated at A4 by eval/corpusgen, so the expected pixel
// dimensions of every corpus page are known without asking the renderer.
const (
	a4WidthPt  = 595.0
	a4HeightPt = 842.0
)

// shared is one Renderer for the whole package.
//
// Compiling the PDFium WebAssembly module takes about a second and is the same
// work every test would otherwise repeat. Tests that need their own limits or
// their own lifecycle build their own; everything else borrows this one, which
// also means the tests exercise a Renderer used concurrently, which is the
// property the doc comment claims.
var shared = pdfium.New()

func TestMain(m *testing.M) {
	code := m.Run()
	if err := shared.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "closing shared renderer:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// corpusPDFs returns every PDF in the evaluation corpus, sorted, so a test
// name identifies which document produced a failure.
func corpusPDFs(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "eval", "corpus", "*", "*.pdf"))
	if err != nil {
		t.Fatalf("globbing the corpus: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("the evaluation corpus has no PDFs; these tests need eval/corpus")
	}
	sort.Strings(paths)
	return paths
}

// corpusDoc reads one corpus file into a Document.
func corpusDoc(t *testing.T, path string) ovrin.Document {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return ovrin.Document{Kind: ovrin.KindPDF, Size: int64(len(data)), Data: data}
}

// caseName turns a corpus path into a test name: "invoices/001".
func caseName(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	return dir + "/" + strings.TrimSuffix(filepath.Base(path), ".pdf")
}

// expectedPixels mirrors PDFium's own points-to-pixels rounding, which is a
// ceiling on each axis independently.
func expectedPixels(widthPt, heightPt float64, dpi int) (int, int) {
	scale := float64(dpi) / 72.0
	return int(math.Ceil(widthPt * scale)), int(math.Ceil(heightPt * scale))
}

func TestRenderCorpusPageDimensions(t *testing.T) {
	t.Parallel()

	dpis := []int{72, 150, 300}
	for _, path := range corpusPDFs(t) {
		path := path
		for _, dpi := range dpis {
			dpi := dpi
			t.Run(fmt.Sprintf("%s_at_%ddpi", caseName(path), dpi), func(t *testing.T) {
				t.Parallel()

				doc := corpusDoc(t, path)
				img, err := shared.Render(context.Background(), doc, 1, dpi)
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				wantW, wantH := expectedPixels(a4WidthPt, a4HeightPt, dpi)
				got := img.Bounds()
				if got.Dx() != wantW || got.Dy() != wantH {
					t.Errorf("bounds = %dx%d, want %dx%d", got.Dx(), got.Dy(), wantW, wantH)
				}
				if _, ok := img.(*image.RGBA); !ok {
					t.Errorf("image is %T, want *image.RGBA", img)
				}
			})
		}
	}
}

// TestRenderProducesMarks asserts the page is actually drawn.
//
// A renderer that returned a correctly sized blank bitmap would pass every
// dimension test in this file and be useless, and a blank page is exactly what
// a missing font or a missing filesystem mount would produce.
func TestRenderProducesMarks(t *testing.T) {
	t.Parallel()

	for _, path := range corpusPDFs(t) {
		path := path
		t.Run(caseName(path), func(t *testing.T) {
			t.Parallel()

			img, err := shared.Render(context.Background(), corpusDoc(t, path), 1, 150)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			rgba, ok := img.(*image.RGBA)
			if !ok {
				t.Fatalf("image is %T, want *image.RGBA", img)
			}

			// Count pixels that are neither white nor transparent. A page of
			// text at 150 dpi has thousands.
			dark := 0
			b := rgba.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					r, g, bl, _ := rgba.At(x, y).RGBA()
					if r < 0xC000 || g < 0xC000 || bl < 0xC000 {
						dark++
					}
				}
			}
			if dark < 1000 {
				t.Errorf("only %d non-white pixels; the page looks blank", dark)
			}
		})
	}
}

// TestRenderIndependentOfCleanup guards the use-after-free the WebAssembly
// runtime makes possible: the bitmap PDFium returns is a view into linear
// memory that is freed when the render's resources are released.
func TestRenderIndependentOfCleanup(t *testing.T) {
	t.Parallel()

	doc := corpusDoc(t, corpusPDFs(t)[0])
	first, err := shared.Render(context.Background(), doc, 1, 96)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	before := append([]byte(nil), first.(*image.RGBA).Pix...)

	// Several more renders, which reuse and overwrite the same linear memory.
	for i := 0; i < 4; i++ {
		if _, err := shared.Render(context.Background(), doc, 1, 300); err != nil {
			t.Fatalf("Render %d: %v", i, err)
		}
	}

	if !bytes.Equal(before, first.(*image.RGBA).Pix) {
		t.Error("the first image's pixels changed after later renders; it is a view into freed memory, not a copy")
	}
}

func TestRenderRejects(t *testing.T) {
	t.Parallel()

	valid := corpusDoc(t, corpusPDFs(t)[0])

	tests := []struct {
		name     string
		doc      ovrin.Document
		page     int
		dpi      int
		want     error
		inMsg    string
		notInMsg string
	}{
		{
			name: "a_non_pdf_kind_is_unsupported",
			doc:  ovrin.Document{Kind: ovrin.KindPNG, Data: []byte("\x89PNG\r\n\x1a\n")},
			page: 1, dpi: 150,
			want:  ovrin.ErrUnsupportedFormat,
			inMsg: "png",
		},
		{
			name: "an_empty_document_has_no_content",
			doc:  ovrin.Document{Kind: ovrin.KindPDF},
			page: 1, dpi: 150,
			want: ovrin.ErrNoContent,
		},
		{
			name: "bytes_that_are_not_a_pdf_are_refused",
			doc:  ovrin.Document{Kind: ovrin.KindPDF, Data: []byte("this is not a pdf at all, not even close")},
			page: 1, dpi: 150,
			want: ovrin.ErrUnsupportedFormat,
			// The refusal must not quote the document back.
			notInMsg: "not even close",
		},
		{
			name: "page_zero_is_not_a_page_number",
			doc:  valid,
			page: 0, dpi: 150,
			want:  ovrin.ErrInternal,
			inMsg: "1-based",
		},
		{
			name: "a_negative_page_is_not_a_page_number",
			doc:  valid,
			page: -3, dpi: 150,
			want: ovrin.ErrInternal,
		},
		{
			name: "page_beyond_the_end_names_the_range",
			doc:  valid,
			page: 9, dpi: 150,
			want:  ovrin.ErrInternal,
			inMsg: "range is 1 to 1",
		},
		{
			name: "zero_dpi_is_not_a_resolution",
			doc:  valid,
			page: 1, dpi: 0,
			want:  ovrin.ErrInternal,
			inMsg: "dpi 0",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			img, err := shared.Render(context.Background(), tt.doc, tt.page, tt.dpi)
			if img != nil {
				t.Error("an image was returned alongside an error")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is(err, %v)", err, tt.want)
			}
			var oe *ovrin.Error
			if !errors.As(err, &oe) {
				t.Fatalf("err is %T, want *ovrin.Error", err)
			}
			if oe.Op != ovrin.OpRender {
				t.Errorf("Op = %q, want %q", oe.Op, ovrin.OpRender)
			}
			if oe.Provider != "pdfium" {
				t.Errorf("Provider = %q, want %q", oe.Provider, "pdfium")
			}
			if tt.inMsg != "" && !strings.Contains(err.Error(), tt.inMsg) {
				t.Errorf("message %q does not mention %q", err.Error(), tt.inMsg)
			}
			if tt.notInMsg != "" && strings.Contains(err.Error(), tt.notInMsg) {
				t.Errorf("message %q quotes the document", err.Error())
			}
		})
	}
}

// TestOversizedPageIsRefusedBeforeAllocation is the resource-limit test.
//
// The PDF declares a media box of one million points square, which is 1.9e12
// pixels at 100 dpi — roughly eight terabytes of RGBA. If the check happened
// after PDFium was asked for the bitmap this test would exhaust memory rather
// than fail, and if it happened by trying and recovering it would be slow. It
// is timed, so a check that moves later fails here rather than in production.
func TestOversizedPageIsRefusedBeforeAllocation(t *testing.T) {
	t.Parallel()

	doc := ovrin.Document{Kind: ovrin.KindPDF, Data: pageOfSize(1_000_000, 1_000_000)}

	// Warm the pool outside the timed section: the first render in the process
	// compiles four megabytes of WebAssembly, which is not what is being timed.
	if _, err := shared.Render(context.Background(), corpusDoc(t, corpusPDFs(t)[0]), 1, 72); err != nil {
		t.Fatalf("warming: %v", err)
	}

	start := time.Now()
	img, err := shared.Render(context.Background(), doc, 1, 100)
	elapsed := time.Since(start)

	if img != nil {
		t.Error("an image was returned for an oversized page")
	}
	if !errors.Is(err, ovrin.ErrLimitExceeded) {
		t.Fatalf("err = %v, want errors.Is(err, ovrin.ErrLimitExceeded)", err)
	}
	if !strings.Contains(err.Error(), "WithMaxPagePixels") {
		t.Errorf("message %q does not name the option that raises the limit", err.Error())
	}
	if elapsed > 2*time.Second {
		t.Errorf("refusal took %v; the ceiling is being checked after the work, not before", elapsed)
	}
}

// TestPixelCeilingIsPerRenderer checks the option actually moves the ceiling,
// in both directions, so the default is a default rather than a constant.
func TestPixelCeilingIsPerRenderer(t *testing.T) {
	t.Parallel()

	doc := corpusDoc(t, corpusPDFs(t)[0])

	tests := []struct {
		name      string
		maxPixels int
		dpi       int
		wantErr   bool
	}{
		{name: "a_ceiling_below_the_page_refuses_it", maxPixels: 1000, dpi: 72, wantErr: true},
		{name: "a_ceiling_above_the_page_allows_it", maxPixels: 10_000_000, dpi: 72, wantErr: false},
		{name: "a_ceiling_of_zero_disables_the_check", maxPixels: 0, dpi: 72, wantErr: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := pdfium.New(pdfium.WithMaxPagePixels(tt.maxPixels), pdfium.WithInstances(1))
			defer func() {
				if err := r.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}()

			_, err := r.Render(context.Background(), doc, 1, tt.dpi)
			if tt.wantErr {
				if !errors.Is(err, ovrin.ErrLimitExceeded) {
					t.Fatalf("err = %v, want errors.Is(err, ovrin.ErrLimitExceeded)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	t.Parallel()

	doc := corpusDoc(t, corpusPDFs(t)[0])

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want error
	}{
		{
			name: "a_context_cancelled_before_the_call",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
		{
			name: "a_deadline_already_past",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "a_context_cancelled_while_rendering",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
				t.Cleanup(cancel)
				return ctx
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			img, err := shared.Render(tt.ctx(t), doc, 1, 300)
			elapsed := time.Since(start)

			if img != nil {
				t.Error("an image was returned for a cancelled context")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is(err, %v)", err, tt.want)
			}
			if !errors.Is(err, ovrin.ErrUnavailable) {
				t.Errorf("err = %v, want it to classify as ovrin.ErrUnavailable", err)
			}
			if elapsed > 5*time.Second {
				t.Errorf("returning took %v; cancellation was recorded rather than acted on", elapsed)
			}
		})
	}
}

// TestCancellationDuringRender is the one that matters: a context cancelled
// after PDFium has started work on a page.
//
// Render cannot interrupt PDFium — no PDFium entry point takes a context — so
// what is asserted is that the caller is released promptly rather than made to
// wait for a page it no longer wants. The page is chosen to take far longer to
// render than the deadline allowed for returning.
func TestCancellationDuringRender(t *testing.T) {
	t.Parallel()

	doc := corpusDoc(t, corpusPDFs(t)[0])
	r := pdfium.New(pdfium.WithInstances(1), pdfium.WithMaxPagePixels(0))
	defer func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// Warm the runtime, and find out how long an uncancelled render of this
	// page takes, so the assertion below is relative to this machine rather
	// than to a number guessed on another one.
	start := time.Now()
	if _, err := r.Render(context.Background(), doc, 1, 900); err != nil {
		t.Fatalf("warming: %v", err)
	}
	full := time.Since(start)
	if full < 100*time.Millisecond {
		t.Skipf("a 900 dpi page renders in %v here, too fast to cancel reliably", full)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(full / 10)
		cancel()
	}()

	start = time.Now()
	img, err := r.Render(ctx, doc, 1, 900)
	elapsed := time.Since(start)

	if img != nil {
		t.Error("an image was returned for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
	if elapsed > full/2 {
		t.Errorf("Render took %v to return after %v, with a full render taking %v; "+
			"cancellation is being recorded rather than acted on", elapsed, full/10, full)
	}
}

// TestNoLeaksAcrossManyRenders asserts the two things a WebAssembly runtime
// makes easy to leak: goroutines, and PDFium instances.
//
// Instances are counted indirectly — the pool is bounded at two, so a leaked
// instance would exhaust it and the twentieth render would block until the
// test timed out rather than fail.
func TestNoLeaksAcrossManyRenders(t *testing.T) {
	doc := corpusDoc(t, corpusPDFs(t)[0])

	before := stableGoroutines()

	r := pdfium.New(pdfium.WithInstances(2))
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		img, err := r.Render(ctx, doc, 1, 72)
		cancel()
		if err != nil {
			t.Fatalf("Render %d: %v", i, err)
		}
		if img == nil {
			t.Fatalf("Render %d returned no image", i)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	after := stableGoroutines()
	if after > before+2 {
		t.Errorf("goroutines went from %d to %d across 20 renders and a Close", before, after)
	}
}

// TestCloseWaitsForAbandonedRenders asserts that a render whose caller gave up
// on a cancelled context still releases its instance, and that Close waits for
// it rather than pulling the runtime out from under it.
func TestCloseWaitsForAbandonedRenders(t *testing.T) {
	doc := corpusDoc(t, corpusPDFs(t)[0])

	before := stableGoroutines()

	r := pdfium.New(pdfium.WithInstances(1))
	// Warm the pool so the cancellation lands during rendering rather than
	// during a one-second WebAssembly compile.
	if _, err := r.Render(context.Background(), doc, 1, 72); err != nil {
		t.Fatalf("warming: %v", err)
	}

	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		if _, err := r.Render(ctx, doc, 1, 400); err == nil {
			t.Log("render completed within the deadline; that is fine, the leak check still applies")
		}
		cancel()
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A render after Close fails rather than reviving the pool.
	if _, err := r.Render(context.Background(), doc, 1, 72); !errors.Is(err, ovrin.ErrInternal) {
		t.Errorf("Render after Close: err = %v, want errors.Is(err, ovrin.ErrInternal)", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	after := stableGoroutines()
	if after > before+2 {
		t.Errorf("goroutines went from %d to %d; abandoned renders leaked", before, after)
	}
}

func TestConcurrentRendersAreSafe(t *testing.T) {
	t.Parallel()

	paths := corpusPDFs(t)
	docs := make([]ovrin.Document, len(paths))
	for i, p := range paths {
		docs[i] = corpusDoc(t, p)
	}

	r := pdfium.New(pdfium.WithInstances(4))
	defer func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	const goroutines = 12
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc := docs[i%len(docs)]
			img, err := r.Render(context.Background(), doc, 1, 100)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			wantW, wantH := expectedPixels(a4WidthPt, a4HeightPt, 100)
			if img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
				errs <- fmt.Errorf("goroutine %d: bounds %v, want %dx%d", i, img.Bounds(), wantW, wantH)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestEncryptedPDF asserts an encrypted document is refused as encrypted
// rather than as corrupt, because the two have different remedies.
func TestEncryptedPDF(t *testing.T) {
	t.Parallel()

	_, err := shared.Render(context.Background(),
		ovrin.Document{Kind: ovrin.KindPDF, Data: encryptedPDF()}, 1, 72)
	if !errors.Is(err, ovrin.ErrEncrypted) {
		t.Fatalf("err = %v, want errors.Is(err, ovrin.ErrEncrypted)", err)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("message %q does not say the document is encrypted", err.Error())
	}
}

// stableGoroutines waits for the goroutine count to settle, so a background
// goroutine that is on its way out is not mistaken for a leak.
func stableGoroutines() int {
	last := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
	}
	return last
}

// buildPDF assembles a one-page PDF from object bodies, with a correct
// cross-reference table.
//
// Written out rather than fixture files because the interesting documents here
// are ones no generator produces — a page claiming to be a kilometre across —
// and because a test that shows the bytes it is testing is easier to trust.
func buildPDF(objects []string, trailerExtra string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R %s>>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, trailerExtra, xref)
	return buf.Bytes()
}

// pageOfSize returns a one-page PDF whose media box is the given size in
// points. Nothing is drawn: the point is the declaration, not the content.
func pageOfSize(widthPt, heightPt int) []byte {
	return buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << >> >>",
			widthPt, heightPt),
	}, "")
}

// encryptedPDF returns a PDF carrying a standard security handler that this
// process has no key for.
func encryptedPDF() []byte {
	return buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << >> >>",
		"<< /Filter /Standard /V 1 /R 2 /P -1 " +
			"/O <2222222222222222222222222222222222222222222222222222222222222222> " +
			"/U <1111111111111111111111111111111111111111111111111111111111111111> >>",
	}, "/Encrypt 4 0 R /ID [<0102030405060708090A0B0C0D0E0F10> <0102030405060708090A0B0C0D0E0F10>] ")
}
