package adaptertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The page every [OCR] assertion recognises.
//
// It is fixed rather than configurable because the four normalisations the
// suite exists to check are all statements about a coordinate space, and a
// fixture that chose its own space could satisfy them vacuously. An adapter's
// fixtures are authored against these numbers.
//
// The geometry is US Letter at 100 DPI, so 612 × 792 points is exactly
// 850 × 1100 pixels and a fixture's pixel values convert to points without
// rounding — a normalisation that is off by a scale factor is then visible as
// an exact mismatch rather than as a rounding argument.
const (
	// OCRPageNumber is the page number the suite asks for.
	//
	// Deliberately not 1. An adapter that hardcodes the page it stamps on a
	// [ovrin.Line] or on an [ovrin.Error] passes every assertion when the
	// fixture uses the first page, and fails the moment a real document has a
	// second one.
	OCRPageNumber = 3

	// OCRPageWidth and OCRPageHeight are the page size in points, which is the
	// space every returned coordinate must be in.
	OCRPageWidth  = 612.0
	OCRPageHeight = 792.0

	// OCRPageDPI is the resolution the page was rasterised at.
	OCRPageDPI = 100

	// ocrPixelWidth and ocrPixelHeight are OCRPageWidth and OCRPageHeight at
	// OCRPageDPI, which is the space a provider reports its own boxes in.
	ocrPixelWidth  = 850
	ocrPixelHeight = 1100
)

// ocrPixelOverflow is the smallest point coordinate whose pixel value at
// [OCRPageDPI] falls off the bottom of the page.
//
// A fixture must place at least one word below it, so that a box handed back
// in pixels lands outside the page and the bounds assertion catches it. Without
// such a word every box could be left unconverted and still look plausible.
const ocrPixelOverflow = OCRPageHeight * 72.0 / OCRPageDPI

// DocumentCanary is the document a [DocumentOCR] assertion sends.
//
// It carries [ContentCanary] in its bytes, so the privacy assertion has
// something to search an error for: for OCR the document *is* the content, and
// there is no other field a provider could quote back.
var DocumentCanary = []byte("%PDF-1.7\n% " + ContentCanary + "\ntrailer\n%%EOF\n")

// OCRWant is the normalised recognition a fixture claims its response encodes.
//
// Everything here is vendor-neutral by construction: it is ovrin's own types,
// so a fixture states its expectation in the shape the confidence engine and
// the provenance model consume, rather than in the vendor's.
type OCRWant struct {
	// Words are the words in reading order, with boxes in page points and
	// confidences on 0..1.
	Words []ovrin.Word

	// Lines are the lines the words group into, in reading order.
	Lines []ovrin.Line

	// Confidence is the provider's own confidence over the page, on 0..1.
	Confidence float64

	// Language is the detected language, or empty when the fixture reports
	// none.
	Language string
}

// OCRSuite describes one [ovrin.OCR] adapter well enough to exercise the shared
// contract.
//
// The payload fields are vendor-specific by necessity. Everything asserted
// about them is not: a fixture states its expectation as [OCRWant], which is
// ovrin's own shape, so no assertion in this file knows anything about a
// vendor.
type OCRSuite struct {
	// Name identifies the adapter in test output.
	Name string

	// New builds the adapter pointed at a test server, with the credential in
	// APIKey already configured.
	//
	// It must configure the adapter for no retries, or the error assertions
	// pay a backoff each. Retry is the core's business anyway
	// (docs/rules.md §6.2).
	New func(baseURL string) ovrin.OCR

	// NewDocument builds the same adapter as an [ovrin.DocumentOCR], able to
	// read the document whose bytes are content.
	//
	// The bytes are a parameter because [ovrin.Document] carries a document's
	// kind, page count and size but not its content, so how an adapter reaches
	// the bytes is currently the adapter's own business — see the note in
	// [OCR] on the document assertions.
	//
	// Optional. Leaving it nil skips every DocumentOCR assertion, which is
	// correct for a provider that cannot rasterise server-side and wrong for
	// one that can.
	NewDocument func(baseURL string, content []byte) ovrin.DocumentOCR

	// APIKey is the credential New bakes in, so the suite can assert it never
	// reaches an error and that the adapter actually sends the one it was
	// given rather than reading the environment (docs/rules.md §6.4).
	APIKey string

	// ProviderName is what [ovrin.OCR.Name] must return. It is checked against
	// the Provider field of every error too, because a result that cannot say
	// which adapter produced it cannot be audited.
	ProviderName string

	// SuccessBody is one recognised page in the vendor's format.
	SuccessBody string

	// Want is the normalisation SuccessBody must produce.
	Want OCRWant

	// APIOrder is the order SuccessBody presents Want.Words in, as indices
	// into Want.Words.
	//
	// It must not be the identity: a fixture whose API order already is
	// reading order cannot tell an adapter that sorts from one that hands back
	// whatever arrived, and reading order is the normalisation most likely to
	// be forgotten.
	//
	// Optional. When nil the suite infers it by looking for each word's text
	// in SuccessBody, which works for a wire format that spells words out and
	// not for one that splits them into characters. A fixture whose words
	// cannot be found is rejected rather than passed vacuously.
	APIOrder []int

	// PageConfidenceBody is a successful response reporting no per-word
	// confidence.
	//
	// Optional, and the only way to check the rule that such a provider sets
	// the page confidence on each word rather than fabricating 1.0
	// (docs/rules.md §6.1). Leave it nil only when the vendor's format cannot
	// express a missing per-word confidence.
	PageConfidenceBody string

	// WantPageConfidence is the page confidence PageConfidenceBody reports,
	// which every word must then carry. It must be neither 0 nor 1: those are
	// exactly the two values a fabricating adapter would produce.
	WantPageConfidence float64

	// UsedPageConfidence reads out of [ovrin.Recognition.Raw] whether the
	// adapter recorded that it fell back to the page confidence.
	//
	// Raw is the provider's own value, so only the adapter's own test knows
	// its type. Optional; nil checks that the fallback happened but not that
	// it was recorded.
	UsedPageConfidence func(raw any) bool

	// DocumentBody is a multi-page response in the vendor's format, used with
	// NewDocument.
	DocumentBody string

	// WantDocument is one [OCRWant] per page of DocumentBody, in page order.
	//
	// At least two pages: a fixture with one cannot tell an adapter that
	// splits a document response per page from one that returns the first page
	// and drops the rest, which is exactly the silent data loss
	// docs/rules.md §6.1 forbids.
	WantDocument []OCRWant

	// UnsupportedKind is a document format the provider cannot accept.
	//
	// Optional; nil skips the assertion that such a request is refused with
	// [ovrin.ErrUnsupported] rather than served badly.
	UnsupportedKind ovrin.Kind

	// ErrorBody is an error payload in the vendor's format, used with a
	// variety of status codes.
	ErrorBody string

	// EchoErrorBody builds an error payload whose human-readable message
	// quotes the text it is given.
	//
	// This is the privacy trap. Real providers quote request fragments back in
	// validation errors, and for OCR the fragment they quote is the encoded
	// page — so an adapter that copies a provider's message into its own ships
	// the document into the caller's logs. Optional: when nil the suite echoes
	// the raw request body instead, which is a weaker version of the same
	// check because most vendor error parsers will not find a message in it.
	EchoErrorBody func(echo string) string
}

// OCRPage returns the page every [OCR] assertion recognises.
//
// It is exported because an adapter's fixtures state their boxes in this page's
// coordinate space, and a fixture that guessed the geometry would be asserting
// against a page the suite does not send.
//
// The image is deliberately not blank. A provider adapter that encodes the
// image at all has to produce bytes, and a uniform image compresses to so
// little that a body carrying it looks the same as a body that dropped it.
func OCRPage() ovrin.Page {
	img := image.NewGray(image.Rect(0, 0, ocrPixelWidth, ocrPixelHeight))
	// Horizontal bars, which is both cheap to encode and visibly not uniform.
	for y := 0; y < ocrPixelHeight; y++ {
		shade := uint8(255)
		if (y/50)%2 == 0 {
			shade = 32
		}
		for x := 0; x < ocrPixelWidth; x++ {
			img.SetGray(x, y, color.Gray{Y: shade})
		}
	}
	return ovrin.Page{
		Number: OCRPageNumber,
		Image:  img,
		Width:  OCRPageWidth,
		Height: OCRPageHeight,
		DPI:    OCRPageDPI,
	}
}

// ocrDocument is the document the DocumentOCR assertions send.
//
// Data carries DocumentCanary, so an adapter that reads the document reads the
// canary and an adapter that ignores Data and reaches for bytes it was given
// some other way is visible. ovrin.Document gained Data precisely because a
// DocumentOCR could not otherwise reach what it was asked to read.
func (s OCRSuite) ocrDocument() ovrin.Document {
	return ovrin.Document{
		Kind:  ovrin.KindPDF,
		Pages: len(s.WantDocument),
		Size:  int64(len(DocumentCanary)),
		Data:  DocumentCanary,
	}
}

// OCR runs the whole contract against an [ovrin.OCR] adapter.
//
// The assertions run in sequence rather than in parallel: one of them counts
// goroutines, and a goroutine count is a property of the process, so a
// concurrent sibling test would make it report another test's work as this
// adapter's leak.
//
// # A note on the document assertions
//
// [ovrin.Document] carries a document's kind, page count and size but not its
// content, so an [ovrin.DocumentOCR] cannot reach the bytes it is being asked
// to recognise from its argument alone. Until the core closes that gap, the
// suite hands the bytes to [OCRSuite.NewDocument] and lets each adapter say how
// it takes them. The assertions themselves are unaffected: they are about what
// comes back, not about how the bytes got in.
func OCR(t *testing.T, s OCRSuite) {
	t.Helper()

	if !s.valid(t) {
		return
	}

	t.Run(s.Name+"/names itself", s.testName)
	t.Run(s.Name+"/returns words in reading order", s.testReadingOrder)
	t.Run(s.Name+"/normalises coordinates to page points", s.testCoordinates)
	t.Run(s.Name+"/normalises confidence to 0..1", s.testConfidence)
	t.Run(s.Name+"/groups words into lines", s.testLines)
	t.Run(s.Name+"/reports the page confidence and language", s.testPageLevel)
	t.Run(s.Name+"/populates Raw", s.testRaw)
	t.Run(s.Name+"/uses the page confidence when the provider reports none", s.testPageConfidenceFallback)
	t.Run(s.Name+"/sends the credential it was given", s.testCredentialReachesTheWire)
	t.Run(s.Name+"/recognises a whole document", s.testDocument)
	t.Run(s.Name+"/refuses a document it cannot read", s.testUnsupportedRatherThanDegraded)
	t.Run(s.Name+"/refuses a page with no image", s.testPageWithoutImage)
	t.Run(s.Name+"/classifies failures onto ovrin sentinels", s.testErrorClassification)
	t.Run(s.Name+"/never puts the credential in an error", s.testCredentialNeverLeaks)
	t.Run(s.Name+"/never puts document content in an error", s.testContentNeverLeaks)
	t.Run(s.Name+"/aborts promptly on cancellation", s.testContextCancellation)
	t.Run(s.Name+"/is safe for concurrent use", s.testConcurrency)
	t.Run(s.Name+"/leaks no goroutine", s.testNoGoroutineLeak)
}

// valid checks the fixture before the fixture checks the adapter.
//
// Each of these is a way for the suite to pass without proving anything, which
// is worse than failing: a green contract suite is the evidence an adapter is
// finished. The normalisation guards are the load-bearing ones — three of the
// four normalisations can be asserted vacuously by a carelessly chosen fixture.
func (s OCRSuite) valid(t *testing.T) bool {
	t.Helper()

	ok := true
	fail := func(format string, args ...any) {
		t.Errorf(format, args...)
		ok = false
	}

	if s.Name == "" {
		fail("OCRSuite.Name is empty; test output would not say which adapter failed")
	}
	if s.New == nil {
		fail("OCRSuite.New is nil; there is nothing to test")
	}
	if s.ProviderName == "" {
		fail("OCRSuite.ProviderName is empty; a result must carry evidence of which " +
			"adapter produced it, and the suite has nothing to check that against")
	}
	if s.SuccessBody == "" {
		fail("OCRSuite.SuccessBody is empty")
	}
	if s.ErrorBody == "" {
		fail("OCRSuite.ErrorBody is empty")
	}

	ok = s.validWant(t, "OCRSuite.Want", s.Want) && ok
	ok = s.validOrder(t) && ok
	ok = s.validFallback(t) && ok
	ok = s.validDocument(t) && ok
	return ok
}

// validWant checks one expectation is capable of failing.
func (s OCRSuite) validWant(t *testing.T, what string, want OCRWant) bool {
	t.Helper()

	ok := true
	fail := func(format string, args ...any) {
		t.Errorf(what+": "+format, args...)
		ok = false
	}

	if len(want.Words) < 3 {
		fail("has %d words; three is the minimum that can show a reordering",
			len(want.Words))
		return false
	}
	if len(want.Lines) < 2 {
		fail("has %d lines; a single line cannot show that words were grouped by one",
			len(want.Lines))
	}

	seen := make(map[string]bool, len(want.Words))
	var lowest float64
	var sawSubPage bool
	for i, w := range want.Words {
		if w.Text == "" {
			fail("word %d has no text", i)
		}
		if seen[w.Text] {
			fail("word %d repeats the text %q; the suite locates words in a fixture "+
				"by their text, and a repeat makes that ambiguous", i, w.Text)
		}
		seen[w.Text] = true

		if w.Box.MinX > w.Box.MaxX || w.Box.MinY > w.Box.MaxY {
			fail("word %d has an inverted box %+v", i, w.Box)
		}
		if w.Box.MinX < 0 || w.Box.MinY < 0 ||
			w.Box.MaxX > OCRPageWidth || w.Box.MaxY > OCRPageHeight {
			fail("word %d has a box %+v outside the %g × %g point page; the fixture "+
				"itself is not normalised", i, w.Box, OCRPageWidth, OCRPageHeight)
		}
		if w.Box.MinY > lowest {
			lowest = w.Box.MinY
		}

		if w.Confidence <= 0 || w.Confidence > 1 {
			fail("word %d has confidence %g, which is not on 0..1; the fixture itself "+
				"is not normalised", i, w.Confidence)
		}
		if w.Confidence < 1 && w.Confidence != want.Confidence {
			sawSubPage = true
		}
		if w.Line < 0 || w.Line >= len(want.Lines) {
			fail("word %d indexes line %d, which is not one of the %d lines",
				i, w.Line, len(want.Lines))
		}
	}

	if lowest <= ocrPixelOverflow {
		fail("every word sits above y=%g points, so a box handed back in pixels at "+
			"%d DPI would still land on the page and the coordinate check could not "+
			"catch it; put a word near the foot of the page",
			ocrPixelOverflow, OCRPageDPI)
	}
	if !sawSubPage {
		fail("no word has a confidence that is both below 1 and different from the " +
			"page confidence, so an adapter that fabricated one or copied the other " +
			"would still pass")
	}
	if want.Confidence <= 0 || want.Confidence >= 1 {
		fail("page confidence is %g; a value at either end of the range cannot "+
			"distinguish a normalised confidence from a fabricated one", want.Confidence)
	}
	return ok
}

// validOrder checks the fixture presents its words in some order other than
// reading order.
func (s OCRSuite) validOrder(t *testing.T) bool {
	t.Helper()

	if len(s.Want.Words) < 3 {
		return false
	}

	order := s.APIOrder
	if order == nil {
		var found bool
		order, found = apiOrder(s.SuccessBody, s.Want.Words)
		if !found {
			t.Errorf("OCRSuite.SuccessBody does not spell out every word in " +
				"OCRSuite.Want, so the suite cannot infer the order the API returned " +
				"them in; set OCRSuite.APIOrder explicitly")
			return false
		}
	}

	if len(order) != len(s.Want.Words) {
		t.Errorf("OCRSuite.APIOrder has %d entries for %d words",
			len(order), len(s.Want.Words))
		return false
	}
	if isIdentity(order) {
		t.Errorf("OCRSuite.SuccessBody returns the words already in reading order, so " +
			"the fixture cannot tell an adapter that sorts from one that hands back " +
			"whatever arrived; scramble the fixture")
		return false
	}
	return true
}

// validFallback checks the no-per-word-confidence fixture is capable of failing.
func (s OCRSuite) validFallback(t *testing.T) bool {
	t.Helper()

	if s.PageConfidenceBody == "" {
		return true
	}
	if s.WantPageConfidence <= 0 || s.WantPageConfidence >= 1 {
		t.Errorf("OCRSuite.WantPageConfidence is %g; 0 and 1 are exactly the two "+
			"values a fabricating adapter produces, so a fixture using either cannot "+
			"tell the fallback from the fabrication", s.WantPageConfidence)
		return false
	}
	return true
}

// validDocument checks the multi-page fixture is capable of failing.
func (s OCRSuite) validDocument(t *testing.T) bool {
	t.Helper()

	if s.NewDocument == nil {
		return true
	}

	ok := true
	if s.DocumentBody == "" {
		t.Error("OCRSuite.NewDocument is set but DocumentBody is empty")
		ok = false
	}
	if len(s.WantDocument) < 2 {
		t.Errorf("OCRSuite.WantDocument has %d pages; with fewer than two the fixture "+
			"cannot tell an adapter that returns every page from one that returns the "+
			"first and drops the rest", len(s.WantDocument))
		return false
	}
	for i, want := range s.WantDocument {
		ok = s.validWant(t, fmt.Sprintf("OCRSuite.WantDocument[%d]", i), want) && ok
		for j, line := range want.Lines {
			if line.Page != i+1 {
				t.Errorf("OCRSuite.WantDocument[%d].Lines[%d].Page = %d, want %d; a "+
					"page-order fixture has to state the page numbers it expects, or "+
					"an adapter that stamps them all with 1 passes", i, j, line.Page, i+1)
				ok = false
			}
		}
	}
	return ok
}

// ---------------------------------------------------------------------------
// Servers
// ---------------------------------------------------------------------------

// ocrRecorder keeps every request a test server received.
//
// Headers as well as bodies, unlike [recorder]: an OCR credential travels in a
// header about as often as in a body, and the assertion that an adapter sends
// the credential it was given (docs/rules.md §6.4) has to be able to find it
// in either.
type ocrRecorder struct {
	mu     sync.Mutex
	bodies [][]byte
	heads  []http.Header
}

func (r *ocrRecorder) add(body []byte, h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
	r.heads = append(r.heads, h)
}

func (r *ocrRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// last returns the newest body, or nil when none arrived.
func (r *ocrRecorder) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return nil
	}
	return r.bodies[len(r.bodies)-1]
}

// lastRequest renders the newest request as one string, headers included, for
// the assertions that only need to know whether a value reached the provider at
// all rather than where it sat.
func (r *ocrRecorder) lastRequest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return ""
	}
	var b strings.Builder
	for name, values := range r.heads[len(r.heads)-1] {
		for _, v := range values {
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	b.Write(r.bodies[len(r.bodies)-1])
	return b.String()
}

// serveOCR starts a server answering every request with status and body, and
// returns the adapter pointed at it.
func (s OCRSuite) serveOCR(t *testing.T, status int, body string) (ovrin.OCR, *ocrRecorder) {
	t.Helper()
	return s.serveOCRFunc(t, func([]byte) (int, string) { return status, body })
}

// serveOCRFunc starts a server whose answer is computed from the request body,
// for the assertions that need the provider to react to what it was sent.
//
// The handler ignores the path and the query: adapters disagree about where an
// endpoint lives, and the suite has no business knowing.
func (s OCRSuite) serveOCRFunc(t *testing.T, answer func(raw []byte) (int, string)) (ovrin.OCR, *ocrRecorder) {
	t.Helper()
	rec, url := s.serveRaw(t, answer)
	return s.New(url), rec
}

// serveRaw starts the server without building an adapter, for the assertions
// that need a [ovrin.DocumentOCR] or no adapter at all.
func (s OCRSuite) serveRaw(t *testing.T, answer func(raw []byte) (int, string)) (*ocrRecorder, string) {
	t.Helper()

	rec := &ocrRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			raw = nil
		}
		head := r.Header.Clone()
		// The query carries a credential for some providers, and the assertion
		// that one was sent must be able to see it.
		head.Set("X-Adaptertest-Target", r.URL.RequestURI())
		rec.add(raw, head)

		status, body := answer(raw)
		// Real providers always declare JSON, and some clients refuse to decode
		// a body without it — so the fake must too, or it is not a fake of
		// anything real.
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		// A failed write means the adapter under test hung up, which the
		// assertion will report far more usefully than this handler could.
		_, _ = io.WriteString(w, body) //nolint:errcheck // see above
	}))
	t.Cleanup(srv.Close)

	return rec, srv.URL
}

// serveDocument starts a server and returns a DocumentOCR pointed at it,
// carrying [DocumentCanary] as the document to read.
func (s OCRSuite) serveDocument(t *testing.T, answer func(raw []byte) (int, string)) (ovrin.DocumentOCR, *ocrRecorder) {
	t.Helper()
	rec, url := s.serveRaw(t, answer)
	return s.NewDocument(url, DocumentCanary), rec
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// [ovrin.Provenance.Method] names the provider that served each value, so an
// adapter without a stable name removes a result's evidence of where its
// content went.
func (s OCRSuite) testName(t *testing.T) {
	o, _ := s.serveOCR(t, http.StatusOK, s.SuccessBody)

	got := o.Name()
	if got != s.ProviderName {
		t.Errorf("Name() = %q, want %q", got, s.ProviderName)
	}
	// Called a second time deliberately. A name that changes between calls
	// makes [ovrin.Provenance.Method] unreproducible, and two results from one
	// provider then look like results from two.
	if again := o.Name(); again != got {
		t.Errorf("Name() = %q and then %q; it must not change between calls",
			got, again)
	}
}

// Reading order is the normalisation most often skipped, because a provider's
// own order is usually close enough to look right. Anything with two columns is
// where it stops being close enough, and by then the words are in a prompt.
func (s OCRSuite) testReadingOrder(t *testing.T) {
	rec := s.recognise(t)

	got := make([]string, len(rec.Words))
	for i, w := range rec.Words {
		got[i] = w.Text
	}
	want := make([]string, len(s.Want.Words))
	for i, w := range s.Want.Words {
		want[i] = w.Text
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Recognition.Words are %v, want %v in reading order; the provider "+
			"returned them in another order and the adapter must sort", got, want)
	}
}

// Coordinates are page points with the origin top left — neither PDF's origin
// nor the provider's pixels. The confidence engine and every review interface
// are written against that one convention (ADR-0009, docs/providers.md).
func (s OCRSuite) testCoordinates(t *testing.T) {
	rec := s.recognise(t)

	if len(rec.Words) != len(s.Want.Words) {
		t.Fatalf("Recognition has %d words, want %d", len(rec.Words), len(s.Want.Words))
	}
	for i, w := range rec.Words {
		want := s.Want.Words[i].Box
		if w.Box.MinX < 0 || w.Box.MinY < 0 ||
			w.Box.MaxX > OCRPageWidth || w.Box.MaxY > OCRPageHeight {
			t.Errorf("word %d (%q) has box %+v, which is off a %g × %g point page; "+
				"the box was probably handed back in the provider's own pixels",
				i, w.Text, w.Box, OCRPageWidth, OCRPageHeight)
			continue
		}
		if !rectsEqual(w.Box, want) {
			t.Errorf("word %d (%q) box = %+v, want %+v", i, w.Text, w.Box, want)
		}
	}
}

// Confidence is on 0..1. Tesseract reports 0..100 and Textract reports 0..100;
// a scale error here does not fail, it silently makes every field look certain
// (docs/confidence.md).
func (s OCRSuite) testConfidence(t *testing.T) {
	rec := s.recognise(t)

	if len(rec.Words) != len(s.Want.Words) {
		t.Fatalf("Recognition has %d words, want %d", len(rec.Words), len(s.Want.Words))
	}
	for i, w := range rec.Words {
		if w.Confidence < 0 || w.Confidence > 1 {
			t.Errorf("word %d (%q) confidence = %g, which is not on 0..1; a provider "+
				"reporting 0..100 must be divided", i, w.Text, w.Confidence)
			continue
		}
		if !floatsEqual(w.Confidence, s.Want.Words[i].Confidence) {
			t.Errorf("word %d (%q) confidence = %g, want %g",
				i, w.Text, w.Confidence, s.Want.Words[i].Confidence)
		}
	}
}

// A word that does not index a line cannot be grouped, and grounding a value
// means finding the line it sits on.
func (s OCRSuite) testLines(t *testing.T) {
	rec := s.recognise(t)

	if len(rec.Lines) != len(s.Want.Lines) {
		t.Fatalf("Recognition has %d lines, want %d", len(rec.Lines), len(s.Want.Lines))
	}
	for i, got := range rec.Lines {
		want := s.Want.Lines[i]
		if got.Text != want.Text {
			t.Errorf("line %d text = %q, want %q", i, got.Text, want.Text)
		}
		if !rectsEqual(got.Box, want.Box) {
			t.Errorf("line %d box = %+v, want %+v", i, got.Box, want.Box)
		}
		if got.Page != want.Page {
			t.Errorf("line %d page = %d, want %d; the page number must come from the "+
				"page that was recognised, not from a constant", i, got.Page, want.Page)
		}
	}
	for i, w := range rec.Words {
		if w.Line < 0 || w.Line >= len(rec.Lines) {
			t.Errorf("word %d (%q) indexes line %d, which is not one of the %d lines",
				i, w.Text, w.Line, len(rec.Lines))
			continue
		}
		if !strings.Contains(rec.Lines[w.Line].Text, w.Text) {
			t.Errorf("word %d (%q) indexes line %d, whose text is %q and does not "+
				"contain it", i, w.Text, w.Line, rec.Lines[w.Line].Text)
		}
	}
}

func (s OCRSuite) testPageLevel(t *testing.T) {
	rec := s.recognise(t)

	if rec.Confidence < 0 || rec.Confidence > 1 {
		t.Errorf("Recognition.Confidence = %g, which is not on 0..1", rec.Confidence)
	}
	if !floatsEqual(rec.Confidence, s.Want.Confidence) {
		t.Errorf("Recognition.Confidence = %g, want %g", rec.Confidence, s.Want.Confidence)
	}
	if rec.Language != s.Want.Language {
		t.Errorf("Recognition.Language = %q, want %q", rec.Language, s.Want.Language)
	}
}

// Raw is the caller's escape hatch from ovrin's abstraction. Normalisation
// deliberately discards structure a provider reports — tables, forms, key-value
// pairs — and Raw is the only route back to it (ADR-0009).
func (s OCRSuite) testRaw(t *testing.T) {
	rec := s.recognise(t)

	if rec.Raw == nil {
		t.Error("Recognition.Raw is nil; it must carry the provider's own response, " +
			"which is the only route to the structure normalisation discarded")
	}
}

// A provider that reports no per-word confidence sets the page confidence on
// each word and records that it did. Fabricating 1.0 would tell the confidence
// engine every word was read perfectly, which is the most consequential lie an
// adapter can tell (docs/rules.md §6.1, ADR-0009).
func (s OCRSuite) testPageConfidenceFallback(t *testing.T) {
	if s.PageConfidenceBody == "" {
		t.Skip("suite has no response without per-word confidence")
	}

	o, _ := s.serveOCR(t, http.StatusOK, s.PageConfidenceBody)
	rec, err := o.Recognise(context.Background(), OCRPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if len(rec.Words) == 0 {
		t.Fatal("Recognise() returned no words")
	}

	if !floatsEqual(rec.Confidence, s.WantPageConfidence) {
		t.Errorf("Recognition.Confidence = %g, want %g",
			rec.Confidence, s.WantPageConfidence)
	}
	for i, w := range rec.Words {
		if floatsEqual(w.Confidence, 1) {
			t.Errorf("word %d (%q) confidence = 1 where the provider reported none; "+
				"a fabricated certainty is worse than an honest page-level one",
				i, w.Text)
			continue
		}
		if !floatsEqual(w.Confidence, s.WantPageConfidence) {
			t.Errorf("word %d (%q) confidence = %g, want the page confidence %g",
				i, w.Text, w.Confidence, s.WantPageConfidence)
		}
	}

	if s.UsedPageConfidence == nil {
		t.Skip("suite does not expose whether the fallback was recorded")
	}
	if !s.UsedPageConfidence(rec.Raw) {
		t.Error("the adapter fell back to the page confidence without recording that " +
			"it did; a caller cannot otherwise tell a page-wide confidence from a " +
			"per-word one that happens to be uniform")
	}
}

// An adapter takes its credential explicitly and sends that one. A library
// reading the environment is how a program talks to the wrong account
// (docs/rules.md §6.4).
func (s OCRSuite) testCredentialReachesTheWire(t *testing.T) {
	if s.APIKey == "" {
		t.Skip("suite has no credential to check")
	}

	o, rec := s.serveOCR(t, http.StatusOK, s.SuccessBody)
	if _, err := o.Recognise(context.Background(), OCRPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if !strings.Contains(rec.lastRequest(), s.APIKey) {
		t.Error("the credential the adapter was given never reached the provider, in " +
			"a header, in the query or in the body; an adapter must use what it was " +
			"handed rather than reading the environment")
	}
}

// A provider that rasterises server-side is what lets a scanned PDF be read
// with no local renderer at all, which is the route ADR-0010 depends on. Every
// page must come back, in page order.
func (s OCRSuite) testDocument(t *testing.T) {
	if s.NewDocument == nil {
		t.Skip("suite has no DocumentOCR")
	}

	o, _ := s.serveDocument(t, func([]byte) (int, string) {
		return http.StatusOK, s.DocumentBody
	})

	recs, err := o.RecogniseDocument(context.Background(), s.ocrDocument())
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	if len(recs) != len(s.WantDocument) {
		t.Fatalf("RecogniseDocument() returned %d pages, want %d; a page that cannot "+
			"be returned is ErrUnsupported, never a shorter slice",
			len(recs), len(s.WantDocument))
	}

	for i, rec := range recs {
		want := s.WantDocument[i]
		if rec == nil {
			t.Errorf("page %d is nil", i+1)
			continue
		}
		got := make([]string, len(rec.Words))
		for j, w := range rec.Words {
			got[j] = w.Text
		}
		wantText := make([]string, len(want.Words))
		for j, w := range want.Words {
			wantText[j] = w.Text
		}
		if strings.Join(got, "|") != strings.Join(wantText, "|") {
			t.Errorf("page %d words = %v, want %v in reading order", i+1, got, wantText)
		}
		for j, w := range rec.Words {
			if j >= len(want.Words) {
				break
			}
			if w.Confidence < 0 || w.Confidence > 1 {
				t.Errorf("page %d word %d confidence = %g, which is not on 0..1",
					i+1, j, w.Confidence)
			}
			if !rectsEqual(w.Box, want.Words[j].Box) {
				t.Errorf("page %d word %d box = %+v, want %+v",
					i+1, j, w.Box, want.Words[j].Box)
			}
		}
		for j, line := range rec.Lines {
			if j >= len(want.Lines) {
				break
			}
			if line.Page != want.Lines[j].Page {
				t.Errorf("page %d line %d Page = %d, want %d",
					i+1, j, line.Page, want.Lines[j].Page)
			}
		}
	}
}

// An adapter that cannot serve a request says so. It never quietly produces a
// worse answer than the caller believes they asked for, which docs/rules.md
// §6.1 names as the one behaviour that is never acceptable.
func (s OCRSuite) testUnsupportedRatherThanDegraded(t *testing.T) {
	if s.NewDocument == nil || s.UnsupportedKind == "" {
		t.Skip("suite names no document format the provider must refuse")
	}

	o, rec := s.serveDocument(t, func([]byte) (int, string) {
		return http.StatusOK, s.DocumentBody
	})

	doc := s.ocrDocument()
	doc.Kind = s.UnsupportedKind

	recs, err := o.RecogniseDocument(context.Background(), doc)
	if err == nil {
		t.Fatalf("RecogniseDocument() accepted a %s document the provider cannot read; "+
			"it must return ErrUnsupported naming what it could not do", s.UnsupportedKind)
	}
	if recs != nil {
		t.Error("RecogniseDocument() returned both a result and an error")
	}
	if !errors.Is(err, ovrin.ErrUnsupported) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnsupported)
	}
	if rec.count() > 0 {
		t.Error("a request was sent for a format the provider cannot read; refusing " +
			"before the call is what stops the caller paying for a worse answer")
	}
}

// A page with no image is nothing an OCR provider can read. Sending the request
// anyway earns a reply about an empty page, which looks exactly like a blank
// scan and is the degraded answer §6.1 forbids.
func (s OCRSuite) testPageWithoutImage(t *testing.T) {
	o, rec := s.serveOCR(t, http.StatusOK, s.SuccessBody)

	page := OCRPage()
	page.Image = nil

	got, err := o.Recognise(context.Background(), page)
	if err == nil {
		t.Fatal("Recognise() accepted a page with no image; it must refuse rather " +
			"than ask a provider to read nothing")
	}
	if got != nil {
		t.Error("Recognise() returned both a recognition and an error")
	}

	var e *ovrin.Error
	if !errors.As(err, &e) {
		t.Errorf("errors.As did not recover *ovrin.Error from %v", err)
	} else if e.Op != ovrin.OpOCR {
		t.Errorf("Error.Op = %q, want %q", e.Op, ovrin.OpOCR)
	}
	if rec.count() > 0 {
		t.Error("a request was sent for a page with no image")
	}
}

// Nothing downstream may branch on the text of a provider's message, so every
// failure has to arrive as one of ovrin's sentinels (docs/rules.md §2.2).
func (s OCRSuite) testErrorClassification(t *testing.T) {
	// Fixed, not configurable. A rule added here is enforced for every adapter
	// at once, which is the reason this suite exists.
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"401 is an authentication failure", http.StatusUnauthorized, ovrin.ErrAuth},
		{"403 is an authentication failure", http.StatusForbidden, ovrin.ErrAuth},
		{"429 is a rate limit", http.StatusTooManyRequests, ovrin.ErrRateLimit},
		{"400 is a rejected request", http.StatusBadRequest, ovrin.ErrBadRequest},
		{"500 is an unavailable provider", http.StatusInternalServerError, ovrin.ErrUnavailable},
		{"503 is an unavailable provider", http.StatusServiceUnavailable, ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			o, _ := s.serveOCR(t, tc.status, s.ErrorBody)

			rec, err := o.Recognise(context.Background(), OCRPage())
			if err == nil {
				t.Fatalf("Recognise() error = nil, want a failure for status %d", tc.status)
			}
			if rec != nil {
				t.Error("Recognise() returned both a recognition and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}

			var e *ovrin.Error
			if !errors.As(err, &e) {
				t.Fatalf("errors.As did not recover *ovrin.Error from %v", err)
			}
			if e.Op != ovrin.OpOCR {
				t.Errorf("Error.Op = %q, want %q", e.Op, ovrin.OpOCR)
			}
			if e.Provider != s.ProviderName {
				t.Errorf("Error.Provider = %q, want %q; a failure must say which "+
					"adapter produced it", e.Provider, s.ProviderName)
			}
			if e.Page != OCRPageNumber {
				t.Errorf("Error.Page = %d, want %d; the page is the one locator "+
					"docs/rules.md §2.5 allows an error to carry, and an error "+
					"nobody can locate is most of the way to useless",
					e.Page, OCRPageNumber)
			}
		})
	}
}

// A credential in an error string ends up in the logs of everyone who prints
// the error (docs/rules.md §2.5, §7.5).
func (s OCRSuite) testCredentialNeverLeaks(t *testing.T) {
	if s.APIKey == "" {
		t.Skip("suite has no credential to check")
	}

	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		o, _ := s.serveOCR(t, status, s.ErrorBody)
		_, err := o.Recognise(context.Background(), OCRPage())
		if err == nil {
			t.Errorf("status %d: Recognise() error = nil", status)
			continue
		}
		assertAbsent(t, err, "the API key", s.APIKey)
	}
}

// For OCR the document *is* the request: the encoded page image, or the PDF
// itself. A provider that quotes the request back in a validation error — which
// real providers do — hands an adapter the whole document to copy into an error
// string, and from there into whatever logs the caller keeps
// (docs/rules.md §2.5, §7.5).
//
// What is checked is the rendered message rather than the whole unwrap chain,
// for the reason [leaks] gives: a cause attached but never printed is how an
// adapter keeps errors.Is(err, context.Canceled) working.
func (s OCRSuite) testContentNeverLeaks(t *testing.T) {
	t.Run("the page image", func(t *testing.T) {
		o, rec := s.serveOCRFunc(t, s.echo)

		_, err := o.Recognise(context.Background(), OCRPage())
		if err == nil {
			t.Fatal("Recognise() error = nil, want a failure")
		}
		for _, leaf := range longLeaves(rec.last()) {
			assertAbsent(t, err, "the encoded page", leaf)
		}
	})

	if s.NewDocument == nil {
		return
	}
	t.Run("the document", func(t *testing.T) {
		o, rec := s.serveDocument(t, s.echo)

		_, err := o.RecogniseDocument(context.Background(), s.ocrDocument())
		if err == nil {
			t.Fatal("RecogniseDocument() error = nil, want a failure")
		}
		assertAbsent(t, err, "document content", ContentCanary)
		for _, leaf := range longLeaves(rec.last()) {
			assertAbsent(t, err, "the encoded document", leaf)
		}
	})
}

// echo answers every request with an error quoting the request back.
func (s OCRSuite) echo(raw []byte) (int, string) {
	if s.EchoErrorBody != nil {
		if quoted := longestLeaf(raw); quoted != "" {
			return http.StatusBadRequest, s.EchoErrorBody(quoted)
		}
		return http.StatusBadRequest, s.EchoErrorBody(string(raw))
	}
	// Weaker, but still a leak if the adapter dumps a body into a message.
	return http.StatusBadRequest, string(raw)
}

// A hung provider must not outlive the caller's context, or it exhausts the
// caller's resources (docs/rules.md §5.4).
func (s OCRSuite) testContextCancellation(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	o := s.New(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := o.Recognise(ctx, OCRPage())
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Recognise() returned nil after the context was cancelled")
		}
		// ovrin's error model promises that one value answers both "what kind
		// of failure was this?" and "was it ultimately a cancelled context?".
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want errors.Is(err, context.Canceled) to hold", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recognise() ignored context cancellation and is still running")
	}
}

// Everything exported is safe for concurrent use by multiple goroutines
// (docs/rules.md §5.1). Run under -race, this is where a shared buffer or a
// reused encoder surfaces.
func (s OCRSuite) testConcurrency(t *testing.T) {
	o, _ := s.serveOCR(t, http.StatusOK, s.SuccessBody)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	texts := make(chan string, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rec, err := o.Recognise(context.Background(), OCRPage())
			if err != nil {
				errs <- err
				return
			}
			words := make([]string, len(rec.Words))
			for j, w := range rec.Words {
				words[j] = w.Text
			}
			texts <- strings.Join(words, "|")
		}()
	}
	wg.Wait()
	close(errs)
	close(texts)

	for err := range errs {
		t.Errorf("concurrent Recognise() error = %v", err)
	}
	want := make([]string, len(s.Want.Words))
	for i, w := range s.Want.Words {
		want[i] = w.Text
	}
	for got := range texts {
		if got != strings.Join(want, "|") {
			t.Errorf("concurrent Recognise() words = %q, want %q",
				got, strings.Join(want, "|"))
		}
	}
}

// A leaked goroutine never shows up in a test's wall clock; it surfaces as
// unbounded memory growth in production. -race does not catch it either — a
// goroutine that is merely blocked is not a data race (docs/rules.md §3.6).
//
// Early return is included deliberately: the abandoned path is the one nobody
// exercises by hand.
func (s OCRSuite) testNoGoroutineLeak(t *testing.T) {
	o, _ := s.serveOCR(t, http.StatusOK, s.SuccessBody)

	blocked := make(chan struct{})
	// entered is signalled as each request reaches the handler, so the warm-up
	// below can wait for a connection to genuinely exist before abandoning it.
	// Without it the baseline misses net/http's per-connection goroutines and
	// this test reports them as the adapter's leak.
	entered := make(chan struct{}, 16)
	hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-blocked:
		}
	}))
	t.Cleanup(func() { close(blocked); hung.Close() })
	hungOCR := s.New(hung.URL)

	// Warm up before the baseline. A test server's accept loop and the HTTP
	// client's persistent connections are created on first use and torn down by
	// t.Cleanup, which runs after the deferred check below — counting them in
	// the baseline is what keeps this measuring the adapter rather than
	// net/http.
	if _, err := o.Recognise(context.Background(), OCRPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	warmCtx, warmCancel := context.WithCancel(context.Background())
	warmDone := make(chan struct{})
	go func() {
		defer close(warmDone)
		_, _ = hungOCR.Recognise(warmCtx, OCRPage()) //nolint:errcheck // warm-up only
	}()
	select {
	case <-entered:
		// The connection exists; abandoning it now exercises the same path the
		// measured loop does, and its goroutines are in the baseline.
	case <-time.After(5 * time.Second):
		t.Fatal("the hung server was never reached during warm-up")
	}
	warmCancel()
	<-warmDone

	defer checkNoGoroutineLeaks(t)()

	for i := 0; i < 3; i++ {
		if _, err := o.Recognise(context.Background(), OCRPage()); err != nil {
			t.Fatalf("Recognise() error = %v", err)
		}
	}
	// And the early-return path: a caller that gives up part way through.
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go cancel()
		_, _ = hungOCR.Recognise(ctx, OCRPage()) //nolint:errcheck // abandonment is the point
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// recognise runs the happy path, failing the test rather than returning an
// error: every assertion built on it is about the shape of a success.
func (s OCRSuite) recognise(t *testing.T) *ovrin.Recognition {
	t.Helper()

	o, _ := s.serveOCR(t, http.StatusOK, s.SuccessBody)
	rec, err := o.Recognise(context.Background(), OCRPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if rec == nil {
		t.Fatal("Recognise() returned a nil Recognition and a nil error")
	}
	return rec
}

// ocrEpsilon is the tolerance for a normalised coordinate or confidence.
//
// Loose enough for the float arithmetic a scale conversion does, far tighter
// than any error the assertions are hunting: a box left in pixels is out by a
// factor, not by a rounding.
const ocrEpsilon = 1e-6

func floatsEqual(a, b float64) bool { return math.Abs(a-b) <= ocrEpsilon }

func rectsEqual(a, b ovrin.Rect) bool {
	return floatsEqual(a.MinX, b.MinX) && floatsEqual(a.MinY, b.MinY) &&
		floatsEqual(a.MaxX, b.MaxX) && floatsEqual(a.MaxY, b.MaxY)
}

// apiOrder returns the indices of words ordered by where each word's text first
// appears in body, and reports whether every word was found.
//
// It is how the suite learns what order a fixture's API returned its words in
// without being told, so that the reading-order guard costs a fixture nothing
// in the common case where a wire format spells words out.
func apiOrder(body string, words []ovrin.Word) ([]int, bool) {
	pos := make([]int, len(words))
	idx := make([]int, len(words))
	for i, w := range words {
		p := strings.Index(body, w.Text)
		if p < 0 {
			return nil, false
		}
		pos[i], idx[i] = p, i
	}
	sort.SliceStable(idx, func(a, b int) bool { return pos[idx[a]] < pos[idx[b]] })
	return idx, true
}

// isIdentity reports whether order is 0, 1, 2, … — that is, whether the API
// already returned the words in reading order.
func isIdentity(order []int) bool {
	for i, v := range order {
		if v != i {
			return false
		}
	}
	return true
}

// ocrLeafFloor is the shortest request string the privacy assertions treat as
// document content.
//
// Short leaves in a request body are a vendor's own vocabulary — "pdf",
// "DOCUMENT_TEXT_DETECTION", a media type — and an adapter naming one in an
// error is not a leak. An encoded page is thousands of characters, so the floor
// separates the two without needing to know either.
const ocrLeafFloor = 40

// longLeaves returns the string values in an encoded request that are long
// enough to be document content.
func longLeaves(body []byte) []string {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		// Not JSON. The whole body is then the only thing to check, and only
		// when it is long enough to be content rather than a form field.
		if len(body) >= ocrLeafFloor {
			return []string{string(body)}
		}
		return nil
	}
	var out []string
	for _, leaf := range stringLeaves(decoded) {
		if len(leaf) >= ocrLeafFloor {
			out = append(out, leaf)
		}
	}
	return out
}

// longestLeaf returns the longest string value in an encoded request, which for
// an OCR request is the encoded page or document.
//
// It is what the suite hands a fixture's EchoErrorBody, so that the provider's
// error quotes back the one part of the request that is document content.
func longestLeaf(body []byte) string {
	var longest string
	for _, leaf := range longLeaves(body) {
		if len(leaf) > len(longest) {
			longest = leaf
		}
	}
	return longest
}
