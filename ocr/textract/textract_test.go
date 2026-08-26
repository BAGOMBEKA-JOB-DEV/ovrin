package textract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/adaptertest"
)

// The credential the tests configure. An access key identifier is not secret,
// but the suite asserts it never reaches an error all the same: a key
// identifier in a log line is half of an audit trail somebody did not consent
// to (rule §2.5).
const (
	testAccessKey = "AKIAOVRINADAPTERTEST"
	testSecret    = "ovrin/adapter/test/secret/key/not/a/real/one"
	testRegion    = "eu-west-1"
)

func testCredentials() Credentials {
	return Credentials{AccessKeyID: testAccessKey, SecretAccessKey: testSecret}
}

// ---------------------------------------------------------------------------
// Fixtures
//
// Built as text rather than by marshalling this package's own wire structs. A
// fixture produced by the decoder it is decoded with cannot catch a wrong json
// tag, which is one of the two things a wire fixture exists to catch.
//
// Every coordinate is written in page points and divided into the fraction
// Textract reports, so the fixture states what it means and the conversion is
// the thing under test rather than a number copied from it.
// ---------------------------------------------------------------------------

const (
	pageWidth  = adaptertest.OCRPageWidth
	pageHeight = adaptertest.OCRPageHeight
)

// fixWord is one word of a fixture, in page points.
type fixWord struct {
	text           string
	x0, y0, x1, y1 float64

	// conf is Textract's confidence on its own 0..100 scale, or nil for a
	// response that reports none for this word.
	conf *float64
}

// fixLine is one line of a fixture, with the words Textract parents to it.
type fixLine struct {
	conf  *float64
	words []fixWord
	page  int
}

func conf(v float64) *float64 { return &v }

func (l fixLine) text() string {
	parts := make([]string, 0, len(l.words))
	for _, w := range l.words {
		parts = append(parts, w.text)
	}
	return strings.Join(parts, " ")
}

// box returns the smallest rectangle containing the line's words, which is what
// Textract reports for a LINE block.
func (l fixLine) box() (x0, y0, x1, y1 float64) {
	for i, w := range l.words {
		if i == 0 {
			x0, y0, x1, y1 = w.x0, w.y0, w.x1, w.y1
			continue
		}
		if w.x0 < x0 {
			x0 = w.x0
		}
		if w.y0 < y0 {
			y0 = w.y0
		}
		if w.x1 > x1 {
			x1 = w.x1
		}
		if w.y1 > y1 {
			y1 = w.y1
		}
	}
	return x0, y0, x1, y1
}

// geometryJSON renders a rectangle the way Textract does: a fraction of the
// page in both axes, with the origin at the top left.
func geometryJSON(x0, y0, x1, y1 float64) string {
	return fmt.Sprintf(`{"BoundingBox":{"Width":%v,"Height":%v,"Left":%v,"Top":%v}}`,
		(x1-x0)/pageWidth, (y1-y0)/pageHeight, x0/pageWidth, y0/pageHeight)
}

func confidenceJSON(c *float64) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf(`,"Confidence":%v`, *c)
}

func pageJSON(page int) string {
	if page == 0 {
		return ""
	}
	return fmt.Sprintf(`,"Page":%d`, page)
}

// blocksJSON renders Textract's flat block list.
//
// The words of each line are emitted in reverse, and the child identifiers with
// them, so that a fixture cannot be satisfied by an adapter that hands back
// whatever arrived: reading order is the normalisation most likely to be
// forgotten.
func blocksJSON(lines []fixLine) string {
	var out []string
	for i, l := range lines {
		lineID := fmt.Sprintf("line-%d-%d", l.page, i)
		ids := make([]string, 0, len(l.words))
		var words []string
		for j := len(l.words) - 1; j >= 0; j-- {
			w := l.words[j]
			id := fmt.Sprintf("word-%d-%d-%d", l.page, i, j)
			ids = append(ids, fmt.Sprintf("%q", id))
			words = append(words, fmt.Sprintf(
				`{"BlockType":"WORD","Id":%q,"Text":%q,"TextType":"PRINTED"%s,"Geometry":%s%s}`,
				id, w.text, confidenceJSON(w.conf),
				geometryJSON(w.x0, w.y0, w.x1, w.y1), pageJSON(l.page)))
		}
		x0, y0, x1, y1 := l.box()
		out = append(out, fmt.Sprintf(
			`{"BlockType":"LINE","Id":%q,"Text":%q%s,"Geometry":%s,`+
				`"Relationships":[{"Type":"CHILD","Ids":[%s]}]%s}`,
			lineID, l.text(), confidenceJSON(l.conf),
			geometryJSON(x0, y0, x1, y1), strings.Join(ids, ","), pageJSON(l.page)))
		out = append(out, words...)
	}
	return strings.Join(out, ",")
}

// syncBody wraps blocks the way DetectDocumentText replies.
func syncBody(lines []fixLine) string {
	return fmt.Sprintf(`{"DocumentMetadata":{"Pages":1},"Blocks":[%s],`+
		`"DetectDocumentTextModelVersion":"1.0"}`, blocksJSON(lines))
}

// jobBody wraps blocks the way GetDocumentTextDetection replies.
//
// It carries JobId as well, which StartDocumentTextDetection is what returns.
// One body serves both calls because the contract suite's server answers every
// request the same way — deliberately, since adapters disagree about where an
// endpoint lives — and Textract puts the operation in a header rather than in
// the path, so the two calls are indistinguishable to it.
func jobBody(pages int, status string, lines []fixLine) string {
	return fmt.Sprintf(`{"JobId":"7f6b1e0c-adaptertest","JobStatus":%q,`+
		`"DocumentMetadata":{"Pages":%d},"Blocks":[%s],`+
		`"DetectDocumentTextModelVersion":"1.0"}`,
		status, pages, blocksJSON(lines))
}

// The page fixture. The lines are emitted bottom to top: Textract's block order
// follows its own segmentation, and an adapter that returns what arrived
// returns nonsense for anything laid out in more than one run.
var pageLines = []fixLine{
	{conf: conf(81), words: []fixWord{
		{text: "Total", x0: 72, y0: 648, x1: 129.6, y1: 666, conf: conf(95)},
		{text: "1,234.56", x0: 432, y0: 648, x1: 576, y1: 666, conf: conf(62)},
	}},
	{conf: conf(90), words: []fixWord{
		{text: "Acme", x0: 72, y0: 144, x1: 126, y1: 162, conf: conf(87)},
		{text: "Corporation", x0: 136.8, y0: 144, x1: 288, y1: 162, conf: conf(93)},
	}},
	{conf: conf(99), words: []fixWord{
		{text: "INVOICE", x0: 72, y0: 72, x1: 180, y1: 90, conf: conf(99)},
	}},
}

// The same page with no per-word confidence, which is what Textract's format
// expresses by omitting the field. The lines keep theirs, so the page still has
// a confidence of its own to fall back to: (80 + 78 + 76) / 3 = 78.
var pageLinesNoWordConfidence = []fixLine{
	{conf: conf(76), words: []fixWord{
		{text: "Total", x0: 72, y0: 648, x1: 129.6, y1: 666},
		{text: "1,234.56", x0: 432, y0: 648, x1: 576, y1: 666},
	}},
	{conf: conf(78), words: []fixWord{
		{text: "Acme", x0: 72, y0: 144, x1: 126, y1: 162},
		{text: "Corporation", x0: 136.8, y0: 144, x1: 288, y1: 162},
	}},
	{conf: conf(80), words: []fixWord{
		{text: "INVOICE", x0: 72, y0: 72, x1: 180, y1: 90},
	}},
}

// The document fixture: two pages in one block list, which is how Textract
// returns a document, with each block naming the page it came from.
var documentLines = []fixLine{
	{page: 1, conf: conf(71), words: []fixWord{
		{text: "Bottom", x0: 100, y0: 700, x1: 200, y1: 720, conf: conf(70)},
	}},
	{page: 1, conf: conf(85), words: []fixWord{
		{text: "PAGE", x0: 100, y0: 100, x1: 200, y1: 120, conf: conf(90)},
		{text: "ONE", x0: 210, y0: 100, x1: 280, y1: 120, conf: conf(80)},
	}},
	{page: 2, conf: conf(67), words: []fixWord{
		{text: "Footer", x0: 100, y0: 710, x1: 190, y1: 730, conf: conf(66)},
	}},
	{page: 2, conf: conf(83), words: []fixWord{
		{text: "SECOND", x0: 100, y0: 100, x1: 220, y1: 120, conf: conf(88)},
		{text: "PAGE2", x0: 230, y0: 100, x1: 320, y1: 120, conf: conf(77)},
	}},
}

// wantPage is the fixture normalised: page points, 0..1, reading order.
//
// The page confidence is the mean of the lines' — (99 + 90 + 81) / 3 — because
// Textract publishes no page confidence at all.
var wantPage = adaptertest.OCRWant{
	Words: []ovrin.Word{
		{Text: "INVOICE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 180, MaxY: 90}, Confidence: 0.99, Line: 0},
		{Text: "Acme", Box: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 126, MaxY: 162}, Confidence: 0.87, Line: 1},
		{Text: "Corporation", Box: ovrin.Rect{MinX: 136.8, MinY: 144, MaxX: 288, MaxY: 162}, Confidence: 0.93, Line: 1},
		{Text: "Total", Box: ovrin.Rect{MinX: 72, MinY: 648, MaxX: 129.6, MaxY: 666}, Confidence: 0.95, Line: 2},
		{Text: "1,234.56", Box: ovrin.Rect{MinX: 432, MinY: 648, MaxX: 576, MaxY: 666}, Confidence: 0.62, Line: 2},
	},
	Lines: []ovrin.Line{
		{Text: "INVOICE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 180, MaxY: 90}, Page: adaptertest.OCRPageNumber},
		{Text: "Acme Corporation", Box: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 162}, Page: adaptertest.OCRPageNumber},
		{Text: "Total 1,234.56", Box: ovrin.Rect{MinX: 72, MinY: 648, MaxX: 576, MaxY: 666}, Page: adaptertest.OCRPageNumber},
	},
	Confidence: 0.9,
}

var wantDocument = []adaptertest.OCRWant{
	{
		Words: []ovrin.Word{
			{Text: "PAGE", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 200, MaxY: 120}, Confidence: 0.90, Line: 0},
			{Text: "ONE", Box: ovrin.Rect{MinX: 210, MinY: 100, MaxX: 280, MaxY: 120}, Confidence: 0.80, Line: 0},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 100, MinY: 700, MaxX: 200, MaxY: 720}, Confidence: 0.70, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "PAGE ONE", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 280, MaxY: 120}, Page: 1},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 100, MinY: 700, MaxX: 200, MaxY: 720}, Page: 1},
		},
		Confidence: 0.78,
	},
	{
		Words: []ovrin.Word{
			{Text: "SECOND", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 220, MaxY: 120}, Confidence: 0.88, Line: 0},
			{Text: "PAGE2", Box: ovrin.Rect{MinX: 230, MinY: 100, MaxX: 320, MaxY: 120}, Confidence: 0.77, Line: 0},
			{Text: "Footer", Box: ovrin.Rect{MinX: 100, MinY: 710, MaxX: 190, MaxY: 730}, Confidence: 0.66, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "SECOND PAGE2", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 320, MaxY: 120}, Page: 2},
			{Text: "Footer", Box: ovrin.Rect{MinX: 100, MinY: 710, MaxX: 190, MaxY: 730}, Page: 2},
		},
		Confidence: 0.75,
	},
}

// ---------------------------------------------------------------------------
// The contract
// ---------------------------------------------------------------------------

// The suite in internal/adaptertest is the barrier: an adapter that cannot pass
// it is not finished (docs/providers.md). Everything vendor-neutral about this
// adapter is asserted there rather than here, so a rule added to the suite
// reaches this adapter without anybody editing this file.
func TestProviderContract(t *testing.T) {
	adaptertest.OCR(t, adaptertest.OCRSuite{
		Name: "textract",
		New: func(baseURL string) ovrin.OCR {
			return New(testRegion, testCredentials(), WithBaseURL(baseURL))
		},
		// The bytes are not used: this adapter reads a document from
		// [ovrin.Document.Data], and a document of more than one page cannot be
		// sent inline at all — Textract's asynchronous operation, the only one
		// that reads more than a page, accepts an Amazon S3 object and has no
		// field for a document's bytes. So the two-page fixture below travels
		// the S3 route, and TestSynchronousDocument covers the inline one.
		NewDocument: func(baseURL string, _ []byte) ovrin.DocumentOCR {
			return New(testRegion, testCredentials(), WithBaseURL(baseURL),
				WithPageSize(pageWidth, pageHeight),
				WithDocumentLocation(func(context.Context, ovrin.Document) (S3Object, error) {
					return S3Object{Bucket: "ovrin-adaptertest", Name: "document.pdf"}, nil
				}))
		},
		APIKey:       testAccessKey,
		ProviderName: providerName,

		SuccessBody: syncBody(pageLines),
		Want:        wantPage,
		// Stated rather than inferred: a LINE block repeats the text of every
		// word on it, so where a word first appears in the body is the line's
		// text and not the word's block.
		APIOrder: []int{4, 3, 2, 1, 0},

		PageConfidenceBody: syncBody(pageLinesNoWordConfidence),
		WantPageConfidence: 0.78,
		UsedPageConfidence: func(raw any) bool {
			a, ok := raw.(*Analysis)
			return ok && a.WordConfidenceFromPage
		},

		DocumentBody:    jobBody(2, jobSucceeded, documentLines),
		WantDocument:    wantDocument,
		UnsupportedKind: ovrin.KindDOCX,

		ErrorBody: `{"__type":"InvalidParameterException",` +
			`"Message":"1 validation error detected"}`,
		EchoErrorBody: func(echo string) string {
			return fmt.Sprintf(`{"__type":"InvalidParameterException","Message":%q}`,
				"Request has invalid image: "+echo)
		},
	})
}

// ---------------------------------------------------------------------------
// What only this adapter's own test can check
// ---------------------------------------------------------------------------

// serve starts a test server whose reply is computed from the request, and
// returns a provider pointed at it along with what it received.
func serve(t *testing.T, answer func(target string, body []byte) (int, string)) (*Provider, *requests) {
	t.Helper()

	rec := &requests{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body) //nolint:errcheck // a short read is still evidence
		target := r.Header.Get("X-Amz-Target")
		rec.add(target, r.Header.Get("Authorization"), string(buf))

		status, body := answer(target, buf)
		w.Header().Set("Content-Type", contentTypeJSON)
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body) //nolint:errcheck // the assertion reports the failure
	}))
	t.Cleanup(srv.Close)

	return New(testRegion, testCredentials(), WithBaseURL(srv.URL),
		WithPageSize(pageWidth, pageHeight), WithPollInterval(time.Millisecond)), rec
}

// requests records what a test server received. It is guarded because the
// concurrency assertions call through it from several goroutines.
type requests struct {
	n       atomic.Int64
	targets []string
	auths   []string
	bodies  []string
}

func (r *requests) add(target, auth, body string) {
	r.n.Add(1)
	r.targets = append(r.targets, target)
	r.auths = append(r.auths, auth)
	r.bodies = append(r.bodies, body)
}

func (r *requests) count() int { return int(r.n.Load()) }

func ok(body string) func(string, []byte) (int, string) {
	return func(string, []byte) (int, string) { return http.StatusOK, body }
}

func testPage() ovrin.Page { return adaptertest.OCRPage() }

// The whole point of the seam: a page read costs a page, and Recognition.Usage
// is the only place that can be reported from. A provider that leaves it zero
// makes OCR the one stage of the pipeline whose spend is invisible.
func TestPageUnitsAreReported(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, ok(syncBody(pageLines)))

	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if rec.Usage.PageUnits != 1 {
		t.Errorf("Recognition.Usage.PageUnits = %d, want 1", rec.Usage.PageUnits)
	}
	if rec.Usage.InputTokens != 0 || rec.Usage.OutputTokens != 0 {
		t.Errorf("Recognition.Usage = %+v; an OCR provider bills pages, not tokens",
			rec.Usage)
	}

	doc, _ := serve(t, ok(jobBody(2, jobSucceeded, documentLines)))
	docP := New(testRegion, testCredentials(), WithBaseURL(""), WithPageSize(pageWidth, pageHeight))
	_ = docP
	recs, err := documentProvider(t, ok(jobBody(2, jobSucceeded, documentLines))).
		RecogniseDocument(context.Background(), pdf(2, nil))
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	var total int
	for _, r := range recs {
		total += r.Usage.PageUnits
	}
	if total != 2 {
		t.Errorf("the document's page units total %d, want 2; the sum over a "+
			"document's pages is what the provider bills for it", total)
	}
	_ = doc
}

// documentProvider returns a provider configured to read a document from S3.
func documentProvider(t *testing.T, answer func(string, []byte) (int, string)) *Provider {
	t.Helper()

	p, _ := serve(t, answer)
	return New(testRegion, testCredentials(), WithBaseURL(p.baseURL),
		WithPageSize(pageWidth, pageHeight), WithPollInterval(time.Millisecond),
		WithDocumentLocation(func(context.Context, ovrin.Document) (S3Object, error) {
			return S3Object{Bucket: "ovrin-adaptertest", Name: "document.pdf"}, nil
		}))
}

// pdf returns a document of n pages carrying data.
func pdf(n int, data []byte) ovrin.Document {
	return ovrin.Document{Kind: ovrin.KindPDF, Pages: n, Size: int64(len(data)), Data: data}
}

// A single-page document can be sent inline, and that route reads the bytes the
// core already holds rather than asking for them again.
func TestSynchronousDocument(t *testing.T) {
	t.Parallel()

	p, rec := serve(t, ok(syncBody(pageLines)))

	recs, err := p.RecogniseDocument(context.Background(), pdf(1, []byte("%PDF-1.7\ncontent")))
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("RecogniseDocument() returned %d pages, want 1", len(recs))
	}
	if len(recs[0].Words) != len(wantPage.Words) {
		t.Errorf("page 1 has %d words, want %d", len(recs[0].Words), len(wantPage.Words))
	}
	if recs[0].Lines[0].Page != 1 {
		t.Errorf("line 0 Page = %d, want 1", recs[0].Lines[0].Page)
	}
	if rec.count() != 1 {
		t.Fatalf("%d requests were sent for a single-page document, want 1", rec.count())
	}
	if rec.targets[0] != targetDetect {
		t.Errorf("X-Amz-Target = %q, want %q; a document that fits the synchronous "+
			"operation must not start a job", rec.targets[0], targetDetect)
	}
	if !strings.Contains(rec.bodies[0], "JVBERi0xLjcKY29udGVudA==") {
		t.Error("the document's own bytes never reached the provider")
	}
}

// Rule §6.1: an adapter that cannot serve a request says so, naming what it
// could not do, rather than producing a worse answer than the caller believes
// they asked for.
func TestRefusalsRatherThanDegradedResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(p *Provider) error
		want error
	}{
		{
			name: "a page with no image",
			call: func(p *Provider) error {
				page := testPage()
				page.Image = nil
				_, err := p.Recognise(context.Background(), page)
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a page that does not say how large it is",
			call: func(p *Provider) error {
				page := testPage()
				page.Width, page.Height = 0, 0
				_, err := p.Recognise(context.Background(), page)
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a document that is not a pdf or a tiff",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					ovrin.Document{Kind: ovrin.KindDOCX, Pages: 1, Data: []byte("PK")})
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a document longer than the inline operation reads",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					pdf(maxSyncPages+1, []byte("%PDF-1.7")))
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a document with no bytes",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(), pdf(1, nil))
				return err
			},
			want: ovrin.ErrNoContent,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, rec := serve(t, ok(syncBody(pageLines)))
			err := tc.call(p)
			if err == nil {
				t.Fatalf("call succeeded; it must return %v naming what it could not do",
					tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if rec.count() != 0 {
				t.Error("a request was sent for something the provider cannot serve; " +
					"refusing before the call is what stops the caller paying for it")
			}
		})
	}
}

// Textract states every box as a fraction of a page whose size it never
// reports, so a document with no declared page size has no route to points at
// all. A US Letter default would be wrong by four per cent on every A4
// document, silently, in a value nothing downstream can sanity-check.
func TestDocumentWithoutAPageSizeIsRefused(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, syncBody(pageLines)) //nolint:errcheck // asserted below
	}))
	t.Cleanup(srv.Close)

	p := New(testRegion, testCredentials(), WithBaseURL(srv.URL))
	_, err := p.RecogniseDocument(context.Background(), pdf(1, []byte("%PDF-1.7")))
	if !errors.Is(err, ovrin.ErrUnsupported) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnsupported)
	}
	if calls.Load() != 0 {
		t.Error("a request was sent for a document whose boxes could not be converted")
	}
}

// A job that ends in PARTIAL_SUCCESS read some pages and not others. Handing
// back the ones it read is the silent truncation §6.1 exists to prevent.
func TestAsynchronousFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body func(string, []byte) (int, string)
		want error
	}{
		{
			name: "a partial success is refused rather than returned short",
			body: ok(`{"JobId":"j","JobStatus":"PARTIAL_SUCCESS","DocumentMetadata":` +
				`{"Pages":2},"Warnings":[{"ErrorCode":"PAGE_CHARACTERS_EXCEEDED",` +
				`"Pages":[2]}],"Blocks":[]}`),
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a failed job is a rejected request",
			body: ok(`{"JobId":"j","JobStatus":"FAILED","StatusMessage":"` +
				adaptertest.ContentCanary + `"}`),
			want: ovrin.ErrBadRequest,
		},
		{
			name: "a status nobody knows is an unusable response",
			body: ok(`{"JobId":"j","JobStatus":"SOMETHING_NEW"}`),
			want: ovrin.ErrBadResponse,
		},
		{
			name: "a job that is never returned is an unusable response",
			body: ok(`{"DocumentMetadata":{"Pages":1}}`),
			want: ovrin.ErrBadResponse,
		},
		{
			name: "a repeated continuation token is an unusable response",
			body: ok(`{"JobId":"j","JobStatus":"SUCCEEDED","NextToken":"same",` +
				`"DocumentMetadata":{"Pages":1},"Blocks":[]}`),
			want: ovrin.ErrBadResponse,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := documentProvider(t, tc.body)
			recs, err := p.RecogniseDocument(context.Background(), pdf(2, nil))
			if err == nil {
				t.Fatalf("RecogniseDocument() error = nil, want %v", tc.want)
			}
			if recs != nil {
				t.Error("RecogniseDocument() returned both a result and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if strings.Contains(err.Error(), adaptertest.ContentCanary) {
				t.Errorf("the provider's own message reached the error: %v", err)
			}
		})
	}
}

// A job is polled until it ends or the caller's context does, and nothing here
// invents a deadline of its own (rule §6.2).
func TestPollingContinuesUntilTheJobEnds(t *testing.T) {
	t.Parallel()

	var polls atomic.Int64
	p := documentProvider(t, func(target string, _ []byte) (int, string) {
		if target != targetGet {
			return http.StatusOK, `{"JobId":"j"}`
		}
		if polls.Add(1) < 3 {
			return http.StatusOK, `{"JobStatus":"IN_PROGRESS"}`
		}
		return http.StatusOK, jobBody(2, jobSucceeded, documentLines)
	})

	recs, err := p.RecogniseDocument(context.Background(), pdf(2, nil))
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("RecogniseDocument() returned %d pages, want 2", len(recs))
	}
	if polls.Load() != 3 {
		t.Errorf("the job was polled %d times, want 3", polls.Load())
	}
}

// A caller who gives up must not leave a polling loop running: a goroutine that
// is merely blocked is not a data race, so -race would never report it.
func TestPollingStopsWithTheContext(t *testing.T) {
	t.Parallel()

	p := documentProvider(t, func(target string, _ []byte) (int, string) {
		if target != targetGet {
			return http.StatusOK, `{"JobId":"j"}`
		}
		return http.StatusOK, `{"JobStatus":"IN_PROGRESS"}`
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.RecogniseDocument(ctx, pdf(2, nil))
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RecogniseDocument() returned nil after the context was cancelled")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want errors.Is(err, context.Canceled) to hold", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RecogniseDocument() ignored context cancellation and is still polling")
	}
}

// A continuation token collects the rest of a document. An adapter that stopped
// at the first response would return a document's first pages and call it the
// whole thing.
func TestContinuationTokenCollectsEveryPage(t *testing.T) {
	t.Parallel()

	p := documentProvider(t, func(target string, body []byte) (int, string) {
		if target != targetGet {
			return http.StatusOK, `{"JobId":"j"}`
		}
		if !strings.Contains(string(body), "NextToken") {
			return http.StatusOK, fmt.Sprintf(
				`{"JobStatus":"SUCCEEDED","NextToken":"more","DocumentMetadata":`+
					`{"Pages":2},"Blocks":[%s]}`, blocksJSON(documentLines[:2]))
		}
		return http.StatusOK, fmt.Sprintf(
			`{"JobStatus":"SUCCEEDED","DocumentMetadata":{"Pages":2},"Blocks":[%s]}`,
			blocksJSON(documentLines[2:]))
	})

	recs, err := p.RecogniseDocument(context.Background(), pdf(2, nil))
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("RecogniseDocument() returned %d pages, want 2", len(recs))
	}
	if len(recs[1].Words) != 3 {
		t.Errorf("page 2 has %d words, want 3; the second response was dropped",
			len(recs[1].Words))
	}
}

// A document read short is refused. Returning the pages that did arrive is the
// silent degradation rule §6.1 forbids.
func TestTruncatedDocumentIsRefused(t *testing.T) {
	t.Parallel()

	p := documentProvider(t, ok(jobBody(1, jobSucceeded, documentLines[:2])))

	recs, err := p.RecogniseDocument(context.Background(), pdf(9, nil))
	if err == nil {
		t.Fatal("RecogniseDocument() returned 1 of 9 pages without saying so")
	}
	if recs != nil {
		t.Error("RecogniseDocument() returned both a result and an error")
	}
	if !errors.Is(err, ovrin.ErrUnsupported) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnsupported)
	}
}

// AWS returns a throttle, an expired credential and a malformed request with
// the same HTTP 400. Classifying on the status alone would tell a fallback
// chain that a rate limit is a permanent request defect and stop it advancing.
func TestClassifyException(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		exception string
		want      error
	}{
		{"a throttle inside a 400 is a rate limit", 400, "ThrottlingException", ovrin.ErrRateLimit},
		{"a namespaced throttle is classified too", 400,
			"com.amazonaws.textract#ProvisionedThroughputExceededException", ovrin.ErrRateLimit},
		{"an expired token inside a 400 is an authentication failure", 400,
			"ExpiredTokenException", ovrin.ErrAuth},
		{"an unreadable document is unsupported", 400,
			"UnsupportedDocumentException", ovrin.ErrUnsupported},
		{"a document too large for the service is unsupported", 400,
			"DocumentTooLargeException", ovrin.ErrUnsupported},
		{"an internal error inside a 400 is an unavailable provider", 400,
			"InternalServerError", ovrin.ErrUnavailable},
		{"an unknown exception keeps the status classification", 400,
			"SomethingNewException", ovrin.ErrBadRequest},
		{"a body with no exception keeps the status classification", 400, "", ovrin.ErrBadRequest},
		{"the status wins where it is unambiguous", 429, "InvalidParameterException", ovrin.ErrRateLimit},
		{"a 403 is an authentication failure", 403, "AccessDeniedException", ovrin.ErrAuth},
		{"a 500 is an unavailable provider", 500, "InternalServerError", ovrin.ErrUnavailable},
		{"a 3xx nobody expects is an unavailable provider", 302, "", ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyException(tc.status, &apiError{Type: tc.exception})
			if !errors.Is(got, tc.want) {
				t.Errorf("classifyException(%d, %q) = %v, want %v",
					tc.status, tc.exception, got, tc.want)
			}
		})
	}
}

// Rule §6.4: the credential comes from what the adapter was given, and a
// provider function is how temporary credentials reach it.
func TestCredentials(t *testing.T) {
	t.Parallel()

	t.Run("a credentials provider is used instead of the static key", func(t *testing.T) {
		t.Parallel()

		p, rec := serve(t, ok(syncBody(pageLines)))
		p = New(testRegion, testCredentials(), WithBaseURL(p.baseURL),
			WithCredentialsProvider(func(context.Context) (Credentials, error) {
				return Credentials{
					AccessKeyID:     "ASIATEMPORARY",
					SecretAccessKey: "temporary",
					SessionToken:    "token",
				}, nil
			}))

		if _, err := p.Recognise(context.Background(), testPage()); err != nil {
			t.Fatalf("Recognise() error = %v", err)
		}
		if !strings.Contains(rec.auths[0], "ASIATEMPORARY") {
			t.Errorf("Authorization = %q, want the provider's own key", rec.auths[0])
		}
		if strings.Contains(rec.auths[0], testAccessKey) {
			t.Error("the static key was sent alongside the provider's; a provider given " +
				"both must use one, so a caller can tell which identity a call was made as")
		}
	})

	t.Run("a credentials provider that fails is an authentication failure", func(t *testing.T) {
		t.Parallel()

		p, rec := serve(t, ok(syncBody(pageLines)))
		p = New(testRegion, Credentials{}, WithBaseURL(p.baseURL),
			WithCredentialsProvider(func(context.Context) (Credentials, error) {
				return Credentials{}, errors.New("no credentials found")
			}))

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrAuth) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrAuth)
		}
		if rec.count() != 0 {
			t.Error("an unsigned request was sent")
		}
	})

	t.Run("no credential at all is an authentication failure", func(t *testing.T) {
		t.Parallel()

		p, _ := serve(t, ok(syncBody(pageLines)))
		p = New(testRegion, Credentials{}, WithBaseURL(p.baseURL))

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrAuth) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrAuth)
		}
	})

	t.Run("no region at all is a rejected request", func(t *testing.T) {
		t.Parallel()

		p, _ := serve(t, ok(syncBody(pageLines)))
		p = New("", testCredentials(), WithBaseURL(p.baseURL))

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrBadRequest) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrBadRequest)
		}
	})
}

// ocr/google found that a *url.Error renders the URL it failed on, which for a
// provider authenticating in the query string puts the credential in every log
// line that unwraps the error. Textract signs its requests instead, so the URL
// carries nothing secret — but the secret must not reach the error by any other
// route either, and the assertion is cheap.
func TestTransportFailureCarriesNoCredential(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now, so the next call cannot connect

	p := New(testRegion, testCredentials(), WithBaseURL(url))
	_, err := p.Recognise(context.Background(), testPage())
	if err == nil {
		t.Fatal("Recognise() error = nil against a closed server")
	}
	if !errors.Is(err, ovrin.ErrUnavailable) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrUnavailable)
	}

	// The rendered error, and every error under it, since a cause is reachable
	// through errors.Unwrap and callers do print those.
	for e := err; e != nil; e = errors.Unwrap(e) {
		for _, secret := range []string{testAccessKey, testSecret} {
			if strings.Contains(e.Error(), secret) {
				t.Fatalf("a credential appears in the error chain: %v", e)
			}
		}
	}
}

// A page Textract read as blank is a real thing a scanner produces. Calling it
// an error would make a document with one blank page unextractable, which is
// not the core's rule for a page that yielded nothing (rule §2.6).
func TestBlankPageIsNotAnError(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, ok(`{"DocumentMetadata":{"Pages":1},"Blocks":[]}`))

	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v for a page with no text", err)
	}
	if len(rec.Words) != 0 || len(rec.Lines) != 0 {
		t.Errorf("Recognise() invented %d words and %d lines for a blank page",
			len(rec.Words), len(rec.Lines))
	}
	if rec.Confidence != 0 {
		t.Errorf("Recognition.Confidence = %g for a page with nothing on it; a "+
			"confidence with nothing behind it is worse than none", rec.Confidence)
	}
	if rec.Raw == nil {
		t.Error("Recognition.Raw is nil even for a blank page")
	}
}

// Textract parents every word to a line in practice. A word it did not is still
// text the caller paid to have read, and dropping it is what rule §6.1 forbids.
func TestOrphanWordsAreKept(t *testing.T) {
	t.Parallel()

	body := `{"DocumentMetadata":{"Pages":1},"Blocks":[` +
		`{"BlockType":"WORD","Id":"w1","Text":"Stray","Confidence":88,"Geometry":` +
		geometryJSON(72, 300, 140, 318) + `}]}`
	p, _ := serve(t, ok(body))

	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if len(rec.Words) != 1 || rec.Words[0].Text != "Stray" {
		t.Fatalf("Recognise() returned %+v, want the one word it was sent", rec.Words)
	}
	if len(rec.Lines) != 1 || rec.Lines[0].Text != "Stray" {
		t.Errorf("Recognise() returned %d lines, want the word to have one of its own",
			len(rec.Lines))
	}
	if rec.Words[0].Line != 0 {
		t.Errorf("word Line = %d, want 0", rec.Words[0].Line)
	}
}

// Textract reports geometry two ways and a reader that knew only one would
// silently return zero boxes for the other.
func TestRectConversion(t *testing.T) {
	t.Parallel()

	sp := space{width: pageWidth, height: pageHeight}

	tests := []struct {
		name string
		geo  *geometry
		want ovrin.Rect
		ok   bool
	}{
		{
			name: "a bounding box is a fraction of the page in both axes",
			geo: &geometry{BoundingBox: &boundingBox{
				Left: 0.5, Top: 0.25, Width: 0.25, Height: 0.5,
			}},
			want: ovrin.Rect{MinX: 306, MinY: 198, MaxX: 459, MaxY: 594},
			ok:   true,
		},
		{
			name: "a polygon becomes its extremes",
			geo: &geometry{Polygon: []point{
				{X: 0.1, Y: 0.1}, {X: 0.5, Y: 0.11}, {X: 0.49, Y: 0.2}, {X: 0.1, Y: 0.19},
			}},
			want: ovrin.Rect{MinX: 61.2, MinY: 79.2, MaxX: 306, MaxY: 158.4},
			ok:   true,
		},
		{name: "no geometry is no rectangle"},
		{name: "an empty geometry is no rectangle", geo: &geometry{}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, gotOK := sp.rect(tc.geo)
			if gotOK != tc.ok {
				t.Fatalf("rect() ok = %v, want %v", gotOK, tc.ok)
			}
			if !gotOK {
				return
			}
			if !closeRect(got, tc.want) {
				t.Errorf("rect() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func closeRect(a, b ovrin.Rect) bool {
	const eps = 1e-9
	d := func(x, y float64) bool {
		if x-y < 0 {
			return y-x <= eps
		}
		return x-y <= eps
	}
	return d(a.MinX, b.MinX) && d(a.MinY, b.MinY) && d(a.MaxX, b.MaxX) && d(a.MaxY, b.MaxY)
}

// Reading order is what makes a two-column page readable, and the banding is
// what stops a baseline wobble reordering two lines that are really one.
func TestReadingOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		boxes []ovrin.Rect
		want  []int
	}{
		{
			name: "lines are read top to bottom",
			boxes: []ovrin.Rect{
				{MinX: 72, MinY: 600, MaxX: 200, MaxY: 620},
				{MinX: 72, MinY: 100, MaxX: 200, MaxY: 120},
				{MinX: 72, MinY: 300, MaxX: 200, MaxY: 320},
			},
			want: []int{1, 2, 0},
		},
		{
			name: "two columns starting together are read left to right",
			boxes: []ovrin.Rect{
				{MinX: 320, MinY: 102, MaxX: 540, MaxY: 120},
				{MinX: 72, MinY: 100, MaxX: 290, MaxY: 120},
			},
			want: []int{1, 0},
		},
		{
			name: "a box far enough below is read after, whatever its column",
			boxes: []ovrin.Rect{
				{MinX: 320, MinY: 100, MaxX: 540, MaxY: 120},
				{MinX: 72, MinY: 400, MaxX: 290, MaxY: 420},
			},
			want: []int{0, 1},
		},
		{name: "an empty page has no order"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := readingOrder(tc.boxes, pageHeight)
			if len(got) != len(tc.want) {
				t.Fatalf("readingOrder() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("readingOrder() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Textract publishes a confidence for every line and word and none for the
// page, so a page-level figure can only be an aggregate — and a caller weighing
// it against another provider's needs to be told that.
func TestPageConfidenceIsDerivedAndSaysSo(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, ok(syncBody(pageLines)))
	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}

	a, ok := rec.Raw.(*Analysis)
	if !ok {
		t.Fatalf("Recognition.Raw is %T, want *Analysis", rec.Raw)
	}
	if !a.PageConfidenceDerived {
		t.Error("the page confidence was computed here and not recorded as derived")
	}
	if a.WordConfidenceFromPage {
		t.Error("no word fell back to the page confidence, but the fallback was recorded")
	}
	if a.Page != adaptertest.OCRPageNumber {
		t.Errorf("Analysis.Page = %d, want %d", a.Page, adaptertest.OCRPageNumber)
	}
	if len(a.JSON) == 0 {
		t.Error("Analysis.JSON is empty; it is the only route to what was discarded")
	}
}
