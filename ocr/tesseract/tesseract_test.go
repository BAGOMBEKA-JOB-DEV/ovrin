package tesseract

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// tessdataEnv names a directory holding <language>.traineddata, for running
// the engine-backed tests on a machine where Tesseract's language packs are
// not installed in a standard place.
//
// It is read by the test and never by the package: rule §6.4 forbids the
// adapter reading the environment, and TestPackageNeverReadsTheEnvironment
// enforces that. A test is not a library.
const tessdataEnv = "OVRIN_TESSDATA_DIR"

// corpusDir is ovrin's evaluation corpus, which is where the only real scans
// in this repository live.
const corpusDir = "../../eval/corpus"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testImage returns a page-sized image that is cheap to encode and visibly not
// uniform.
//
// Not blank, deliberately: a uniform image compresses to so little that a test
// asserting an encoded page does not appear in an error would have almost
// nothing to look for.
func testImage() image.Image {
	img := image.NewGray(image.Rect(0, 0, testPixelsW, testPixelsH))
	for y := 0; y < testPixelsH; y++ {
		shade := uint8(255)
		if (y/50)%2 == 0 {
			shade = 32
		}
		for x := 0; x < testPixelsW; x++ {
			img.SetGray(x, y, color.Gray{Y: shade})
		}
	}
	return img
}

// testOCRPage is the page the refusal assertions send.
func testOCRPage() ovrin.Page {
	return ovrin.Page{
		Number: testPage,
		Image:  testImage(),
		Width:  testPointsW,
		Height: testPointsH,
		DPI:    testDPI,
	}
}

// trainingData returns eng.traineddata, or skips the test saying how to get
// it.
//
// Skipping rather than failing is the only honest option: this module's whole
// claim is that it needs no system Tesseract at build time, and a test suite
// that could not be run without one would contradict it. Skipping *silently*
// would be the other failure — a green run that proved nothing — so the
// message says exactly what is missing.
func trainingData(t *testing.T) []byte {
	t.Helper()

	dirs := DefaultTessdataDirs
	if dir := os.Getenv(tessdataEnv); dir != "" {
		dirs = append([]string{dir}, dirs...)
	}
	var tried []string
	for _, dir := range dirs {
		path := filepath.Join(dir, DefaultLanguage+".traineddata")
		tried = append(tried, path)
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
	}

	t.Skipf("no %s.traineddata found; looked in %s. install a language pack "+
		"(debian/ubuntu: apt-get install tesseract-ocr-eng; macos: brew install "+
		"tesseract-lang) or set %s to a directory containing it. the engine "+
		"itself needs nothing installed — only the model does",
		DefaultLanguage, strings.Join(tried, ", "), tessdataEnv)
	return nil
}

// engineProvider returns a provider with a real engine behind it, closed when
// the test ends.
func engineProvider(t *testing.T, opts ...Option) *Provider {
	t.Helper()

	data := trainingData(t)
	p := New(append([]Option{WithTrainingData(data)}, opts...)...)
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return p
}

// corpusPage loads one scan from the evaluation corpus as a page.
//
// The corpus images carry no page size of their own, so they are treated as US
// Letter — which is what they were drawn as. Nothing in the accuracy
// assertions depends on the geometry; the normalisation assertions that do use
// the synthetic fixtures, where the numbers are exact.
func corpusPage(t *testing.T, rel string) ovrin.Page {
	t.Helper()

	f, err := os.Open(filepath.Join(corpusDir, rel)) //nolint:gosec // a path this test wrote
	if err != nil {
		t.Fatalf("opening %s: %v", rel, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	var img image.Image
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".png":
		img, err = png.Decode(f)
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
	default:
		t.Fatalf("%s is not an image this test can read", rel)
	}
	if err != nil {
		t.Fatalf("decoding %s: %v", rel, err)
	}

	return ovrin.Page{
		Number: 1,
		Image:  img,
		Width:  testPointsW,
		Height: testPointsH,
		DPI:    testDPI,
	}
}

// corpusImages returns every scan in the corpus, as paths relative to it.
func corpusImages(t *testing.T) []string {
	t.Helper()

	var out []string
	err := filepath.Walk(corpusDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg":
			rel, relErr := filepath.Rel(corpusDir, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the corpus: %v", err)
	}
	return out
}

// checkNoGoroutineLeaks returns a function that fails the test if goroutines
// outlive it.
//
// A leaked goroutine never shows up in a test's wall clock; it surfaces as
// unbounded memory growth in production. -race does not catch it either — a
// goroutine that is merely blocked is not a data race (docs/rules.md §3.6).
func checkNoGoroutineLeaks(t *testing.T) func() {
	t.Helper()
	before := runtime.NumGoroutine()
	return func() {
		t.Helper()
		// Retried, because a goroutine that has been told to stop takes a
		// moment to actually stop, and a single sample turns that into a
		// flake.
		for i := 0; i < 200; i++ {
			if runtime.NumGoroutine() <= before {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("goroutines grew from %d to %d and stayed there; the engine's "+
			"workers must not outlive Close", before, runtime.NumGoroutine())
	}
}

func errorOf(t *testing.T, err error) *ovrin.Error {
	t.Helper()
	var e *ovrin.Error
	if !errors.As(err, &e) {
		t.Fatalf("errors.As did not recover *ovrin.Error from %v", err)
	}
	return e
}

// ---------------------------------------------------------------------------
// The adapter contract, minus the parts the shared suite cannot express
// ---------------------------------------------------------------------------

// The shared contract suite in internal/adaptertest is written for a network
// adapter, and this one has no network.
//
// [adaptertest.OCRSuite] requires a New that takes a base URL, serves fixtures
// over httptest and asserts that http 401, 403, 429, 400, 500 and 503 map onto
// ovrin's sentinels. A local engine contacts no server, so those six
// assertions have nothing to provoke and the privacy assertions have no
// request to echo back. Running it would not fail honestly, it would fail for
// the wrong reason.
//
// Everything in the suite that is a statement about an *adapter* rather than
// about HTTP is asserted in this package instead: reading order, coordinates,
// confidence, line grouping, Raw, the page-confidence fallback, the refusal of
// a page with no image, classification onto sentinels, cancellation,
// concurrency and goroutine leaks. This test exists so the gap is recorded in
// the place someone will look for it rather than only in a commit message.
func TestSharedContractSuiteIsNotApplicable(t *testing.T) {
	t.Skip("internal/adaptertest.OCRSuite drives an adapter through an httptest " +
		"server and classifies http status codes; this adapter has no transport. " +
		"the assertions that are about the adapter rather than about http are " +
		"reimplemented in this package — see hocr_test.go for the four " +
		"normalisations and the tests below for errors, cancellation and leaks")
}

// [ovrin.Provenance.Method] names the provider that served each value, so an
// adapter without a stable name removes a result's evidence of where its
// content went.
func TestName(t *testing.T) {
	t.Parallel()

	p := New()
	if got := p.Name(); got != providerName {
		t.Errorf("Name() = %q, want %q", got, providerName)
	}
	// Called a second time deliberately. A name that changes between calls
	// makes [ovrin.Provenance.Method] unreproducible.
	if again := p.Name(); again != p.Name() {
		t.Errorf("Name() = %q and then %q; it must not change between calls",
			again, p.Name())
	}
}

// An adapter that cannot serve a request says so, naming what it could not do.
// It never quietly produces a worse answer than the caller believes they asked
// for, which docs/rules.md §6.1 names as the one behaviour that is never
// acceptable.
//
// Every case here is refused before the engine is reached, which is why they
// run on a machine with no Tesseract at all.
func TestRecogniseRefusesRatherThanDegrading(t *testing.T) {
	t.Parallel()

	missing := t.TempDir()

	tests := []struct {
		name     string
		provider func() *Provider
		page     func() ovrin.Page
		want     error
		// mentions are substrings the message must contain, so that a refusal
		// says what to do about it rather than only that it happened.
		mentions []string
	}{
		{
			name:     "a page with no image",
			provider: func() *Provider { return New() },
			page: func() ovrin.Page {
				page := testOCRPage()
				page.Image = nil
				return page
			},
			want:     ovrin.ErrUnsupported,
			mentions: []string{"no image"},
		},
		{
			name:     "a page that does not say how big it is",
			provider: func() *Provider { return New() },
			page: func() ovrin.Page {
				page := testOCRPage()
				page.Width, page.Height = 0, 0
				return page
			},
			want:     ovrin.ErrUnsupported,
			mentions: []string{"points"},
		},
		{
			name:     "a page whose image has no pixels",
			provider: func() *Provider { return New() },
			page: func() ovrin.Page {
				page := testOCRPage()
				page.Image = image.NewGray(image.Rect(0, 0, 0, 0))
				return page
			},
			want:     ovrin.ErrUnsupported,
			mentions: []string{"no pixels"},
		},
		{
			name: "more than one language",
			provider: func() *Provider {
				return New(WithLanguages("eng", "swa"))
			},
			page:     testOCRPage,
			want:     ovrin.ErrUnsupported,
			mentions: []string{"eng, swa", "traineddata"},
		},
		{
			name: "a language whose traineddata is not installed",
			provider: func() *Provider {
				return New(WithLanguages("swa"), WithTessdataDirs(missing))
			},
			page: testOCRPage,
			want: ovrin.ErrUnsupported,
			mentions: []string{
				`"swa"`,
				filepath.Join(missing, "swa.traineddata"),
				"WithTrainingData",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := tc.provider()
			t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck // teardown

			rec, err := p.Recognise(context.Background(), tc.page())
			if err == nil {
				t.Fatal("Recognise() error = nil, want a refusal")
			}
			if rec != nil {
				t.Error("Recognise() returned both a recognition and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}

			e := errorOf(t, err)
			if e.Op != ovrin.OpOCR {
				t.Errorf("Error.Op = %q, want %q", e.Op, ovrin.OpOCR)
			}
			if e.Provider != providerName {
				t.Errorf("Error.Provider = %q, want %q; a failure must say which "+
					"adapter produced it", e.Provider, providerName)
			}
			for _, want := range tc.mentions {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// The page number is the one locator docs/rules.md §2.5 allows an error to
// carry, and an error nobody can locate is most of the way to useless. It has
// to come from the page rather than from a constant, which is why the fixtures
// use page three.
func TestRecogniseErrorsCarryThePageNumber(t *testing.T) {
	t.Parallel()

	p := New()
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck // teardown

	page := testOCRPage()
	page.Image = nil

	_, err := p.Recognise(context.Background(), page)
	if err == nil {
		t.Fatal("Recognise() error = nil")
	}
	if e := errorOf(t, err); e.Page != testPage {
		t.Errorf("Error.Page = %d, want %d", e.Page, testPage)
	}
}

// A context that ended is not an engine failure and gets no sentinel: a
// fallback chain must not advance to the next provider with a context that is
// already dead. One value has to answer both "what kind of failure was this?"
// and "was it ultimately a cancelled context?" (ADR-0019).
func TestRecogniseHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	p := New()
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck // teardown

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec, err := p.Recognise(ctx, testOCRPage())
	if err == nil {
		t.Fatal("Recognise() error = nil after the context was cancelled")
	}
	if rec != nil {
		t.Error("Recognise() returned both a recognition and an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled) to hold", err)
	}
	for _, sentinel := range []error{
		ovrin.ErrUnavailable, ovrin.ErrUnsupported, ovrin.ErrBadResponse,
	} {
		if errors.Is(err, sentinel) {
			t.Errorf("a cancelled context classified as %v; a fallback chain would "+
				"advance to the next provider with a dead context", sentinel)
		}
	}
}

// A provider that has been closed refuses further work rather than silently
// rebuilding, since a rebuild would hide the bug that closed it too early.
func TestRecogniseAfterCloseRefuses(t *testing.T) {
	t.Parallel()

	p := New()
	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// Twice, because Close is documented as safe to repeat and a double free
	// is exactly what that documentation is protecting against.
	if err := p.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	_, err := p.Recognise(context.Background(), testOCRPage())
	if err == nil {
		t.Fatal("Recognise() succeeded on a closed provider")
	}
	if !errors.Is(err, ovrin.ErrUnavailable) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnavailable)
	}
}

// For OCR the document *is* the request: the page image. An adapter that
// copies an engine's message into its own, or dumps the encoded page into a
// message to help debugging, ships the document into whatever logs the caller
// keeps (docs/rules.md §2.5, §7.5).
func TestErrorsCarryNoDocumentContent(t *testing.T) {
	t.Parallel()

	// The rendered message is what is checked rather than the whole unwrap
	// chain: a cause attached but never printed is how this adapter keeps
	// errors.Is(err, context.Canceled) working.
	page := testOCRPage()

	var encoded strings.Builder
	if err := png.Encode(&encoded, page.Image); err != nil {
		t.Fatalf("encoding the page: %v", err)
	}
	blob := encoded.String()
	if len(blob) < 512 {
		t.Fatalf("the fixture page encodes to %d bytes, which is too little for "+
			"this assertion to mean anything", len(blob))
	}

	failures := map[string]func() error{
		"no traineddata": func() error {
			p := New(WithTessdataDirs(t.TempDir()))
			defer p.Close() //nolint:errcheck // teardown
			_, err := p.Recognise(context.Background(), page)
			return err
		},
		"no page size": func() error {
			p := New()
			defer p.Close() //nolint:errcheck // teardown
			sized := page
			sized.Width, sized.Height = 0, 0
			_, err := p.Recognise(context.Background(), sized)
			return err
		},
	}

	for name, run := range failures {
		name, run := name, run
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := run()
			if err == nil {
				t.Fatal("wanted a failure")
			}
			msg := err.Error()
			// Any long run of the encoded page appearing in the message is a
			// leak; sampling a few windows is enough to catch a dump.
			for offset := 0; offset+64 <= len(blob); offset += len(blob) / 8 {
				if strings.Contains(msg, blob[offset:offset+64]) {
					t.Fatalf("the error quotes the encoded page back at offset %d", offset)
				}
			}
			if len(msg) > 1024 {
				t.Errorf("the error message is %d bytes; nothing this package "+
					"writes is that long, so something was copied into it", len(msg))
			}
		})
	}
}

// Rule §6.4: no adapter reads the environment itself, because reading
// os.Getenv inside a library is how a program ends up talking to the wrong
// account — or here, reading a page with the wrong model, which is not an
// error but a page of plausible nonsense.
func TestPackageNeverReadsTheEnvironment(t *testing.T) {
	t.Parallel()

	// The source is parsed rather than searched for a string, because the
	// package documentation says the words "os.Getenv" while promising not to
	// call it, and a test that could not tell the promise from the breach
	// would have to be deleted the first time someone wrote the promise down.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}

	banned := map[string]map[string]bool{
		"os":      {"Getenv": true, "LookupEnv": true, "Environ": true},
		"syscall": {"Getenv": true, "Environ": true},
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			name, file := name, file
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if banned[ident.Name][sel.Sel.Name] {
					t.Errorf("%s calls %s.%s; an adapter takes its configuration from "+
						"what it is given (rule §6.4), and a wrong model is not an "+
						"error but a page of plausible nonsense",
						name, ident.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

func TestOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		opts  []Option
		check func(t *testing.T, p *Provider)
	}{
		{
			name: "the defaults are one language, one instance and the standard dirs",
			check: func(t *testing.T, p *Provider) {
				if len(p.languages) != 1 || p.languages[0] != DefaultLanguage {
					t.Errorf("languages = %v, want [%s]", p.languages, DefaultLanguage)
				}
				if p.instances != DefaultInstances {
					t.Errorf("instances = %d, want %d", p.instances, DefaultInstances)
				}
				if len(p.dirs) != len(DefaultTessdataDirs) {
					t.Errorf("dirs = %v, want the defaults", p.dirs)
				}
			},
		},
		{
			name: "an empty option call is ignored rather than clearing the default",
			opts: []Option{
				WithLanguages(), WithTessdataDirs(), WithTrainingData(nil),
				WithInstances(0), WithVariables(nil),
			},
			check: func(t *testing.T, p *Provider) {
				if len(p.languages) != 1 || p.languages[0] != DefaultLanguage {
					t.Errorf("languages = %v, want the default kept", p.languages)
				}
				if p.instances != DefaultInstances {
					t.Errorf("instances = %d, want the default kept", p.instances)
				}
				if len(p.dirs) == 0 {
					t.Error("dirs was cleared by an empty WithTessdataDirs")
				}
			},
		},
		{
			name: "blank languages and directories are dropped",
			opts: []Option{WithLanguages("  ", "swa", ""), WithTessdataDirs("", " /models ")},
			check: func(t *testing.T, p *Provider) {
				if len(p.languages) != 1 || p.languages[0] != "swa" {
					t.Errorf("languages = %v, want [swa]", p.languages)
				}
				if len(p.dirs) != 1 || p.dirs[0] != "/models" {
					t.Errorf("dirs = %v, want [/models]", p.dirs)
				}
			},
		},
		{
			name: "the variables map is copied",
			opts: nil,
			check: func(t *testing.T, p *Provider) {
				vars := map[string]string{"tessedit_pageseg_mode": "6"}
				WithVariables(vars)(p)
				vars["tessedit_pageseg_mode"] = "3"
				if p.variables["tessedit_pageseg_mode"] != "6" {
					t.Error("mutating the caller's map changed the provider's " +
						"configuration")
				}
			},
		},
		{
			name: "later options win",
			opts: []Option{WithInstances(2), WithInstances(5)},
			check: func(t *testing.T, p *Provider) {
				if p.instances != 5 {
					t.Errorf("instances = %d, want 5", p.instances)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, New(tc.opts...))
		})
	}
}

// ---------------------------------------------------------------------------
// The engine
// ---------------------------------------------------------------------------

// The end-to-end normalisation check: a real page, read by a real Tesseract,
// producing coordinates on the page and confidences on 0..1.
//
// The synthetic fixtures pin the arithmetic exactly; this one proves the
// arithmetic is applied to what the engine actually returns.
func TestRecogniseNormalisesARealPage(t *testing.T) {
	p := engineProvider(t)

	page := corpusPage(t, "invoices/003.png")
	rec, err := p.Recognise(context.Background(), page)
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if len(rec.Words) == 0 {
		t.Fatal("Recognise() found no words on a page of an invoice")
	}

	if rec.Confidence <= 0 || rec.Confidence > 1 {
		t.Errorf("Recognition.Confidence = %g, which is not on 0..1", rec.Confidence)
	}
	if rec.Language != "" {
		t.Errorf("Recognition.Language = %q, want empty", rec.Language)
	}
	if _, ok := rec.Raw.(*Recognised); !ok {
		t.Errorf("Recognition.Raw is %T, want *Recognised", rec.Raw)
	}

	bounds := page.Image.Bounds()
	for i, w := range rec.Words {
		if w.Confidence < 0 || w.Confidence > 1 {
			t.Fatalf("word %d confidence = %g, which is not on 0..1; tesseract "+
				"reports 0..100 and it must be divided", i, w.Confidence)
		}
		if w.Box.MinX < 0 || w.Box.MinY < 0 ||
			w.Box.MaxX > page.Width || w.Box.MaxY > page.Height {
			t.Fatalf("word %d box = %+v, off a %g × %g point page",
				i, w.Box, page.Width, page.Height)
		}
		if w.Line < 0 || w.Line >= len(rec.Lines) {
			t.Fatalf("word %d indexes line %d, which is not one of the %d lines",
				i, w.Line, len(rec.Lines))
		}
		if !strings.Contains(rec.Lines[w.Line].Text, w.Text) {
			t.Fatalf("word %d (%q) indexes line %d, whose text is %q",
				i, w.Text, w.Line, rec.Lines[w.Line].Text)
		}
		if rec.Lines[w.Line].Page != page.Number {
			t.Fatalf("line %d is stamped page %d, want %d",
				w.Line, rec.Lines[w.Line].Page, page.Number)
		}
	}

	// The scan is wider than it is tall in points only if the conversion was
	// skipped, which is the failure this catches on a real page: an
	// unconverted box would run to the image's pixel count.
	var widest float64
	for _, w := range rec.Words {
		if w.Box.MaxX > widest {
			widest = w.Box.MaxX
		}
	}
	if widest > float64(bounds.Dx()) {
		t.Errorf("the rightmost word reaches x=%g, which is past the page and "+
			"about where a pixel value would land", widest)
	}
}

// A hung engine must not outlive the caller's context (docs/rules.md §5.4).
func TestRecogniseAbortsOnCancellationMidCall(t *testing.T) {
	p := engineProvider(t)

	// Warm the engine first, so what is being timed is the read rather than
	// the one-off WebAssembly compile.
	if _, err := p.Recognise(context.Background(), corpusPage(t, "forms/003.png")); err != nil {
		t.Fatalf("warm-up Recognise() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Recognise(ctx, corpusPage(t, "invoices/003.png"))
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Recognise() returned nil after the context was cancelled")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want errors.Is(err, context.Canceled) to hold", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Recognise() ignored context cancellation and is still running")
	}
}

// Everything exported is safe for concurrent use by multiple goroutines
// (docs/rules.md §5.1). Tesseract itself is not, which is the whole reason the
// provider owns a pool — run under -race, this is where a shared instance
// would surface.
func TestRecogniseIsSafeForConcurrentUse(t *testing.T) {
	p := engineProvider(t, WithInstances(2))

	page := corpusPage(t, "receipts/002.png")

	const n = 4
	var wg sync.WaitGroup
	results := make(chan string, n)
	errs := make(chan error, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec, err := p.Recognise(context.Background(), page)
			if err != nil {
				errs <- err
				return
			}
			words := make([]string, len(rec.Words))
			for j, w := range rec.Words {
				words[j] = w.Text
			}
			results <- strings.Join(words, "|")
		}()
	}
	wg.Wait()
	close(errs)
	close(results)

	for err := range errs {
		t.Errorf("concurrent Recognise() error = %v", err)
	}
	var first string
	for got := range results {
		if first == "" {
			first = got
			continue
		}
		if got != first {
			t.Error("two concurrent reads of the same page disagreed; the engine " +
				"instances are sharing state")
		}
	}
}

// The engine holds the model and the page buffers, and nothing reclaims them
// when the Provider becomes unreachable. Close is the whole contract, and this
// is the test that says so.
func TestCloseReleasesTheEnginesGoroutines(t *testing.T) {
	data := trainingData(t)

	// One warm-up outside the measurement, so the runtime's own lazily
	// started goroutines are in the baseline rather than being reported as
	// this provider's leak.
	warm := New(WithTrainingData(data))
	if _, err := warm.Recognise(context.Background(), corpusPage(t, "forms/003.png")); err != nil {
		t.Fatalf("warm-up Recognise() error = %v", err)
	}
	if err := warm.Close(); err != nil {
		t.Fatalf("warm-up Close() error = %v", err)
	}

	defer checkNoGoroutineLeaks(t)()

	p := New(WithTrainingData(data), WithInstances(2))
	if _, err := p.Recognise(context.Background(), corpusPage(t, "forms/003.png")); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// And the abandoned path: a provider closed while its engine is still
	// starting, which is the one nobody exercises by hand.
	abandoned := New(WithTrainingData(data))
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = abandoned.Recognise(ctx, corpusPage(t, "forms/003.png")) //nolint:errcheck // abandonment is the point
	}()
	if err := abandoned.Close(); err != nil {
		t.Fatalf("Close() on an abandoned provider error = %v", err)
	}
}

// What the engine actually reads off the corpus, measured rather than claimed.
//
// It is a smoke test with a floor, not an accuracy figure: rule §3.8 forbids
// claiming a number `go test -tags=eval` cannot reproduce, and the real
// measurement belongs to the eval harness. The floor is deliberately low —
// what it catches is a page read as nothing at all, or read upside down.
func TestCorpusIsReadable(t *testing.T) {
	p := engineProvider(t)

	for _, rel := range corpusImages(t) {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			rec, err := p.Recognise(context.Background(), corpusPage(t, rel))
			if err != nil {
				t.Fatalf("Recognise() error = %v", err)
			}
			if len(rec.Words) < 10 {
				t.Errorf("read %d words off %s; a page of a document has more than "+
					"that, so this one was read as noise", len(rec.Words), rel)
			}
			if rec.Confidence <= 0 || rec.Confidence > 1 {
				t.Errorf("page confidence = %g, which is not on 0..1", rec.Confidence)
			}
			t.Logf("%s: %d words, %d lines, page confidence %.3f",
				rel, len(rec.Words), len(rec.Lines), rec.Confidence)
		})
	}
}
