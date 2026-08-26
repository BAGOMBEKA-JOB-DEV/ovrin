package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/adaptertest"
)

const testKey = "ovrin-adapter-test-subscription-key"

// ---------------------------------------------------------------------------
// Fixtures
//
// Built as text rather than by marshalling this package's own wire structs. A
// fixture produced by the decoder it is decoded with cannot catch a wrong json
// tag, which is one of the two things a wire fixture exists to catch.
//
// The spans are computed from the words rather than written by hand, because
// spans are how the service says which words are on which line and a fixture
// whose spans were typed out would be asserting against arithmetic nobody did.
// ---------------------------------------------------------------------------

// fixWord is one word of a fixture, in the unit its page declares.
type fixWord struct {
	text           string
	x0, y0, x1, y1 float64

	// conf is the confidence the service reported, or nil for a response that
	// reports none for this word.
	conf *float64
}

type fixLine struct {
	words []fixWord
}

type fixPage struct {
	number        float64
	width, height float64
	unit          string
	lines         []fixLine
}

func conf(v float64) *float64 { return &v }

// polygonJSON renders a rectangle the way the service does: four points,
// clockwise from the top left, flattened into eight numbers.
func polygonJSON(x0, y0, x1, y1 float64) string {
	return fmt.Sprintf("[%v,%v,%v,%v,%v,%v,%v,%v]", x0, y0, x1, y0, x1, y1, x0, y1)
}

func (l fixLine) text() string {
	parts := make([]string, 0, len(l.words))
	for _, w := range l.words {
		parts = append(parts, w.text)
	}
	return strings.Join(parts, " ")
}

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

// pageJSON renders one page, and returns it with the content it contributed.
//
// The lines are emitted bottom to top and the words of each line right to left:
// the service's order follows its own segmentation, and an adapter that returns
// what arrived returns nonsense for anything laid out in more than one run.
func pageJSON(p fixPage, offset int) (encoded, content string, next int) {
	var words, lines []string
	var buf strings.Builder
	start := offset

	for _, l := range p.lines {
		lineStart := offset
		var wordJSON []string
		for _, w := range l.words {
			if offset > lineStart {
				buf.WriteString(" ")
				offset++
			}
			confidence := ""
			if w.conf != nil {
				confidence = fmt.Sprintf(`,"confidence":%v`, *w.conf)
			}
			wordJSON = append(wordJSON, fmt.Sprintf(
				`{"content":%q,"polygon":%s%s,"span":{"offset":%d,"length":%d}}`,
				w.text, polygonJSON(w.x0, w.y0, w.x1, w.y1), confidence,
				offset, len(w.text)))
			buf.WriteString(w.text)
			offset += len(w.text)
		}
		// Reversed, so the fixture cannot be satisfied by an adapter that hands
		// back whatever arrived.
		for i := len(wordJSON) - 1; i >= 0; i-- {
			words = append(words, wordJSON[i])
		}
		x0, y0, x1, y1 := l.box()
		lines = append(lines, fmt.Sprintf(
			`{"content":%q,"polygon":%s,"spans":[{"offset":%d,"length":%d}]}`,
			l.text(), polygonJSON(x0, y0, x1, y1), lineStart, offset-lineStart))
		buf.WriteString("\n")
		offset++
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	encoded = fmt.Sprintf(
		`{"pageNumber":%v,"angle":0,"width":%v,"height":%v,"unit":%q,`+
			`"spans":[{"offset":%d,"length":%d}],"words":[%s],"lines":[%s]}`,
		p.number, p.width, p.height, p.unit, start, offset-start,
		strings.Join(words, ","), strings.Join(lines, ","))
	return encoded, buf.String(), offset
}

// resultJSON renders a finished analysis.
//
// The pages are emitted last page first, so that a fixture cannot be satisfied
// by an adapter that hands back the order they arrived in: a caller reading
// page three has no way to notice it was given page four.
func resultJSON(pages []fixPage, locale string) string {
	encoded := make([]string, len(pages))
	var content strings.Builder
	offset := 0
	for i, p := range pages {
		var text string
		encoded[i], text, offset = pageJSON(p, offset)
		content.WriteString(text)
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}

	languages := ""
	if locale != "" {
		languages = fmt.Sprintf(`,"languages":[{"locale":%q,"confidence":0.98,`+
			`"spans":[{"offset":0,"length":%d}]}]`, locale, offset)
	}
	return fmt.Sprintf(`{"status":"succeeded","createdDateTime":"2026-08-26T12:00:00Z",`+
		`"lastUpdatedDateTime":"2026-08-26T12:00:03Z","analyzeResult":`+
		`{"apiVersion":%q,"modelId":%q,"stringIndexType":"textElements","content":%q,`+
		`"pages":[%s]%s,"styles":[],"paragraphs":[]}}`,
		DefaultAPIVersion, DefaultModel, content.String(),
		strings.Join(encoded, ","), languages)
}

// The page fixture, measured in the pixels of the raster the page was sent as.
// 850 × 1100 pixels onto a 612 × 792 point page is a factor of exactly 0.72.
var rasterPage = fixPage{
	number: 1, width: 850, height: 1100, unit: unitPixel,
	lines: []fixLine{
		{words: []fixWord{
			{text: "INVOICE", x0: 100, y0: 100, x1: 250, y1: 125, conf: conf(0.99)},
		}},
		{words: []fixWord{
			{text: "Acme", x0: 100, y0: 200, x1: 175, y1: 225, conf: conf(0.87)},
			{text: "Corporation", x0: 190, y0: 200, x1: 400, y1: 225, conf: conf(0.93)},
		}},
		{words: []fixWord{
			{text: "Total", x0: 100, y0: 900, x1: 180, y1: 925, conf: conf(0.95)},
			{text: "1,234.56", x0: 600, y0: 900, x1: 800, y1: 925, conf: conf(0.62)},
		}},
	},
}

// The same page with a confidence for one word and none for the rest, which is
// what the service's format expresses by omitting the field. The page's own
// confidence is the mean of the words that reported one — the service publishes
// no page confidence at all — so every word here ends up carrying 0.78, and an
// adapter that fabricated 1.0 instead is visible.
var rasterPageOneConfidence = fixPage{
	number: 1, width: 850, height: 1100, unit: unitPixel,
	lines: []fixLine{
		{words: []fixWord{
			{text: "INVOICE", x0: 100, y0: 100, x1: 250, y1: 125, conf: conf(0.78)},
		}},
		{words: []fixWord{
			{text: "Acme", x0: 100, y0: 200, x1: 175, y1: 225},
			{text: "Corporation", x0: 190, y0: 200, x1: 400, y1: 225},
		}},
		{words: []fixWord{
			{text: "Total", x0: 100, y0: 900, x1: 180, y1: 925},
			{text: "1,234.56", x0: 600, y0: 900, x1: 800, y1: 925},
		}},
	},
}

// The document fixture, measured in inches, which is what the service reports
// for a PDF. An inch is exactly 72 points, so nothing is lost to rounding.
var documentPages = []fixPage{
	{
		number: 1, width: 8.5, height: 11, unit: unitInch,
		lines: []fixLine{
			{words: []fixWord{
				{text: "PAGE", x0: 1, y0: 1, x1: 2, y1: 1.25, conf: conf(0.91)},
				{text: "ONE", x0: 2.25, y0: 1, x1: 3, y1: 1.25, conf: conf(0.83)},
			}},
			{words: []fixWord{
				{text: "Bottom", x0: 1, y0: 9.5, x1: 2, y1: 9.75, conf: conf(0.72)},
			}},
		},
	},
	{
		number: 2, width: 8.5, height: 11, unit: unitInch,
		lines: []fixLine{
			{words: []fixWord{
				{text: "SECOND", x0: 1, y0: 1, x1: 2.5, y1: 1.25, conf: conf(0.89)},
				{text: "PAGE2", x0: 2.75, y0: 1, x1: 3.5, y1: 1.25, conf: conf(0.78)},
			}},
			{words: []fixWord{
				{text: "Footer", x0: 1, y0: 9.75, x1: 2, y1: 10, conf: conf(0.64)},
			}},
		},
	},
}

// wantPage is the raster fixture normalised: page points, 0..1, reading order.
//
// The page confidence is the mean of the words' — (0.99 + 0.87 + 0.93 + 0.95 +
// 0.62) / 5 — because the service publishes no page confidence.
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
	Confidence: 0.872,
	Language:   "en",
}

var wantDocument = []adaptertest.OCRWant{
	{
		Words: []ovrin.Word{
			{Text: "PAGE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 144, MaxY: 90}, Confidence: 0.91, Line: 0},
			{Text: "ONE", Box: ovrin.Rect{MinX: 162, MinY: 72, MaxX: 216, MaxY: 90}, Confidence: 0.83, Line: 0},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 72, MinY: 684, MaxX: 144, MaxY: 702}, Confidence: 0.72, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "PAGE ONE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 216, MaxY: 90}, Page: 1},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 72, MinY: 684, MaxX: 144, MaxY: 702}, Page: 1},
		},
		Confidence: 0.82,
		Language:   "en",
	},
	{
		Words: []ovrin.Word{
			{Text: "SECOND", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 180, MaxY: 90}, Confidence: 0.89, Line: 0},
			{Text: "PAGE2", Box: ovrin.Rect{MinX: 198, MinY: 72, MaxX: 252, MaxY: 90}, Confidence: 0.78, Line: 0},
			{Text: "Footer", Box: ovrin.Rect{MinX: 72, MinY: 702, MaxX: 144, MaxY: 720}, Confidence: 0.64, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "SECOND PAGE2", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 252, MaxY: 90}, Page: 2},
			{Text: "Footer", Box: ovrin.Rect{MinX: 72, MinY: 702, MaxX: 144, MaxY: 720}, Page: 2},
		},
		Confidence: 0.77,
		Language:   "en",
	},
}

func successBody() string  { return resultJSON([]fixPage{rasterPage}, "en") }
func documentBody() string { return resultJSON(documentPages, "en") }

// ---------------------------------------------------------------------------
// The contract
// ---------------------------------------------------------------------------

// The suite in internal/adaptertest is the barrier: an adapter that cannot pass
// it is not finished (docs/providers.md). Everything vendor-neutral about this
// adapter is asserted there rather than here, so a rule added to the suite
// reaches this adapter without anybody editing this file.
func TestProviderContract(t *testing.T) {
	adaptertest.OCR(t, adaptertest.OCRSuite{
		Name: "azure",
		New: func(baseURL string) ovrin.OCR {
			return New(baseURL, testKey, WithPollInterval(time.Millisecond))
		},
		// The bytes are not used: this adapter reads a document from
		// [ovrin.Document.Data], which is where the core already holds it.
		NewDocument: func(baseURL string) ovrin.DocumentOCR {
			return New(baseURL, testKey, WithPollInterval(time.Millisecond))
		},
		APIKey:       testKey,
		ProviderName: providerName,

		SuccessBody: successBody(),
		Want:        wantPage,
		// Stated rather than inferred: a line repeats the content of every word
		// on it, so where a word first appears in the body is the line's
		// content and not the word's own object.
		APIOrder: []int{4, 3, 2, 1, 0},

		PageConfidenceBody: resultJSON([]fixPage{rasterPageOneConfidence}, "en"),
		WantPageConfidence: 0.78,
		UsedPageConfidence: func(raw any) bool {
			a, ok := raw.(*Analysis)
			return ok && a.WordConfidenceFromPage
		},

		DocumentBody:    documentBody(),
		WantDocument:    wantDocument,
		UnsupportedKind: ovrin.KindDOCX,

		ErrorBody: `{"error":{"code":"InvalidRequest","message":"Invalid request.",` +
			`"innererror":{"code":"InvalidContent","message":"The file is corrupted."}}}`,
		EchoErrorBody: func(echo string) string {
			return fmt.Sprintf(`{"error":{"code":"InvalidRequest","message":%q}}`,
				"The input is not valid: "+echo)
		},
	})
}

// ---------------------------------------------------------------------------
// What only this adapter's own test can check
// ---------------------------------------------------------------------------

// reply is one canned response, headers included, because this service returns
// the address of a result in a header and nothing else in that reply at all.
type reply struct {
	status int
	body   string
	header map[string]string
}

// serve starts a test server answering with a sequence of replies, the last of
// which repeats, and returns a provider pointed at it.
func serve(t *testing.T, replies ...reply) (*Provider, *requests) {
	t.Helper()

	rec := &requests{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body) //nolint:errcheck // a short read is still evidence
		n := rec.add(r.Method, r.URL.RequestURI(), r.Header.Get("Ocp-Apim-Subscription-Key"),
			r.Header.Get("Authorization"), string(buf))

		rep := replies[len(replies)-1]
		if n < len(replies) {
			rep = replies[n]
		}
		for k, v := range rep.header {
			// %BASE% stands for the server's own address, which is not known
			// until it is listening.
			w.Header().Set(k, strings.ReplaceAll(v, "%BASE%", rec.base))
		}
		w.Header().Set("Content-Type", "application/json")
		if rep.status != 0 && rep.status != http.StatusOK {
			w.WriteHeader(rep.status)
		}
		_, _ = io.WriteString(w, rep.body) //nolint:errcheck // the assertion reports the failure
	}))
	t.Cleanup(srv.Close)
	rec.base = srv.URL

	return New(srv.URL, testKey, WithPollInterval(time.Millisecond)), rec
}

// requests records what a test server received. It is guarded because a test
// server serves each request on its own goroutine.
type requests struct {
	mu      sync.Mutex
	base    string
	methods []string
	uris    []string
	keys    []string
	auths   []string
	bodies  []string
}

func (r *requests) add(method, uri, key, auth, body string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.methods)
	r.methods = append(r.methods, method)
	r.uris = append(r.uris, uri)
	r.keys = append(r.keys, key)
	r.auths = append(r.auths, auth)
	r.bodies = append(r.bodies, body)
	return n
}

func (r *requests) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.methods)
}

func (r *requests) at(i int) (method, uri, key, auth, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.methods[i], r.uris[i], r.keys[i], r.auths[i], r.bodies[i]
}

func testPage() ovrin.Page { return adaptertest.OCRPage() }

func pdf(n int, data []byte) ovrin.Document {
	return ovrin.Document{Kind: ovrin.KindPDF, Pages: n, Size: int64(len(data)), Data: data}
}

// The documented flow: the submission is accepted, the result appears at the
// address the service names, and it is polled until it is there. Nothing here
// bounds the loop but the caller's context (rule §6.2).
func TestSubmitAndPoll(t *testing.T) {
	t.Parallel()

	p, rec := serve(t,
		reply{status: http.StatusAccepted, header: map[string]string{
			"Operation-Location": "%BASE%/documentintelligence/documentModels/" +
				"prebuilt-read/analyzeResults/1f9a?api-version=" + DefaultAPIVersion,
		}},
		reply{body: `{"status":"running"}`},
		reply{body: `{"status":"notStarted"}`},
		reply{body: successBody()},
	)

	got, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if len(got.Words) != len(wantPage.Words) {
		t.Errorf("Recognise() returned %d words, want %d", len(got.Words), len(wantPage.Words))
	}
	if rec.count() != 4 {
		t.Fatalf("%d requests were made, want 4: one submission and three polls",
			rec.count())
	}

	method, uri, key, _, body := rec.at(0)
	if method != http.MethodPost {
		t.Errorf("the submission was a %s, want a POST", method)
	}
	if !strings.Contains(uri, ":analyze") || !strings.Contains(uri, "api-version="+DefaultAPIVersion) {
		t.Errorf("the submission went to %q, which is not the analyse route", uri)
	}
	if key != testKey {
		t.Errorf("Ocp-Apim-Subscription-Key = %q, want the key the provider was given", key)
	}
	if !strings.Contains(body, `"base64Source"`) {
		t.Errorf("the page was not sent as base64Source: %s", body)
	}

	for i := 1; i < 4; i++ {
		method, _, key, _, _ := rec.at(i)
		if method != http.MethodGet {
			t.Errorf("poll %d was a %s, want a GET", i, method)
		}
		if key != testKey {
			t.Errorf("poll %d carried no credential", i)
		}
	}
}

// The service names the address its result will appear at, and this package
// sends the caller's credential there. A location on another host would
// therefore hand that host the credential, which is the same rule that stops
// ovrin fetching anything a document references (rule §7.4).
func TestOperationLocationOnAnotherHostIsRefused(t *testing.T) {
	t.Parallel()

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the adapter followed the provider to another host, carrying %q",
			r.Header.Get("Ocp-Apim-Subscription-Key"))
		_, _ = io.WriteString(w, successBody()) //nolint:errcheck // the failure is reported above
	}))
	t.Cleanup(elsewhere.Close)

	p, _ := serve(t, reply{status: http.StatusAccepted, header: map[string]string{
		"Operation-Location": elsewhere.URL + "/documentintelligence/x",
	}})

	_, err := p.Recognise(context.Background(), testPage())
	if err == nil {
		t.Fatal("Recognise() followed a result location on another host")
	}
	if !errors.Is(err, ovrin.ErrBadResponse) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrBadResponse)
	}
}

// A submission accepted with nowhere to collect the result from is a reply
// nothing can be done with, and pretending the page was blank would be worse.
func TestAcceptedWithNoLocationIsAnUnusableResponse(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, reply{status: http.StatusAccepted, body: `{}`})

	_, err := p.Recognise(context.Background(), testPage())
	if !errors.Is(err, ovrin.ErrBadResponse) {
		t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrBadResponse)
	}
}

// How long to wait between polls is the service's own suggestion where it makes
// one. Honouring it is mapping rather than deciding (rule §6.2).
func TestRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"no header keeps the configured interval", "", time.Second},
		{"a number of seconds is honoured", "3", 3 * time.Second},
		{"a date nobody can parse keeps the interval", "Wed, 21 Oct 2026 07:28:00 GMT", time.Second},
		{"a negative value keeps the interval", "-1", time.Second},
		{"a zero keeps the interval", "0", time.Second},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := http.Header{}
			if tc.value != "" {
				h.Set("Retry-After", tc.value)
			}
			if got := retryAfter(h, time.Second); got != tc.want {
				t.Errorf("retryAfter() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A caller who gives up must not leave a polling loop running: a goroutine that
// is merely blocked is not a data race, so -race would never report it.
func TestPollingStopsWithTheContext(t *testing.T) {
	t.Parallel()

	p, _ := serve(t,
		reply{status: http.StatusAccepted, header: map[string]string{
			"Operation-Location": "%BASE%/documentintelligence/x",
		}},
		reply{body: `{"status":"running"}`},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Recognise(ctx, testPage())
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
	case <-time.After(5 * time.Second):
		t.Fatal("Recognise() ignored context cancellation and is still polling")
	}
}

// A failed analysis arrives inside an HTTP 200: the request to submit it
// succeeded and the work did not. An adapter classifying on the status alone
// would report an unreadable document as a page with no text on it.
func TestFailedOperationInsideASuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want error
	}{
		{"an invalid request is a rejected request", "InvalidRequest", ovrin.ErrBadRequest},
		{"invalid content is a rejected request", "InvalidContent", ovrin.ErrBadRequest},
		{"an unsupported media type is unsupported", "UnsupportedMediaType", ovrin.ErrUnsupported},
		{"a missing model is unsupported", "ModelNotFound", ovrin.ErrUnsupported},
		{"a permission failure is an authentication failure", "PermissionDenied", ovrin.ErrAuth},
		{"a throttle is a rate limit", "RequestRateLimitExceeded", ovrin.ErrRateLimit},
		{"an internal error is an unavailable provider", "InternalServerError", ovrin.ErrUnavailable},
		{"a code nobody knows is an unavailable provider", "SomethingNew", ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := fmt.Sprintf(`{"status":"failed","error":{"code":%q,"message":%q}}`,
				tc.code, adaptertest.ContentCanary)
			p, _ := serve(t, reply{body: body})

			rec, err := p.Recognise(context.Background(), testPage())
			if err == nil {
				t.Fatal("Recognise() error = nil for an analysis the provider failed")
			}
			if rec != nil {
				t.Error("Recognise() returned both a recognition and an error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if strings.Contains(err.Error(), adaptertest.ContentCanary) {
				t.Errorf("the provider's message reached the error: %v", err)
			}
		})
	}
}

// The whole point of the seam: a page read costs a page, and Recognition.Usage
// is the only place that can be reported from. A provider that leaves it zero
// makes OCR the one stage of the pipeline whose spend is invisible.
func TestPageUnitsAreReported(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, reply{body: successBody()})
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

	d, _ := serve(t, reply{body: documentBody()})
	recs, err := d.RecogniseDocument(context.Background(), pdf(2, []byte("%PDF-1.7")))
	if err != nil {
		t.Fatalf("RecogniseDocument() error = %v", err)
	}
	total := 0
	for _, r := range recs {
		total += r.Usage.PageUnits
	}
	if total != 2 {
		t.Errorf("the document's page units total %d, want 2; the sum over a "+
			"document's pages is what the provider bills for it", total)
	}
}

// Rule §6.1: an adapter that cannot serve a request says so, naming what it
// could not do, rather than producing a worse answer than the caller believes
// they asked for.
func TestRefusalsRatherThanDegradedResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		reply    reply
		call     func(p *Provider) error
		want     error
		requests int
	}{
		{
			name:  "a page with no image",
			reply: reply{body: successBody()},
			call: func(p *Provider) error {
				page := testPage()
				page.Image = nil
				_, err := p.Recognise(context.Background(), page)
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name:  "a page that does not say how large it is",
			reply: reply{body: successBody()},
			call: func(p *Provider) error {
				page := testPage()
				page.Width, page.Height = 0, 0
				_, err := p.Recognise(context.Background(), page)
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name:  "a document that is not a pdf",
			reply: reply{body: documentBody()},
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					ovrin.Document{Kind: ovrin.KindTIFF, Pages: 1, Data: []byte("II*")})
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name:  "a document with no bytes",
			reply: reply{body: documentBody()},
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(), pdf(1, nil))
				return err
			},
			want: ovrin.ErrNoContent,
		},
		{
			name:  "a document the provider read short",
			reply: reply{body: resultJSON(documentPages[:1], "en")},
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(), pdf(9, []byte("%PDF-1.7")))
				return err
			},
			want:     ovrin.ErrUnsupported,
			requests: 1,
		},
		{
			name: "a document measured in a unit that cannot become points",
			reply: reply{body: resultJSON([]fixPage{{
				number: 1, width: 850, height: 1100, unit: unitPixel,
				lines: documentPages[0].lines,
			}}, "en")},
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(), pdf(1, []byte("%PDF-1.7")))
				return err
			},
			want:     ovrin.ErrUnsupported,
			requests: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, rec := serve(t, tc.reply)
			err := tc.call(p)
			if err == nil {
				t.Fatalf("call succeeded; it must return %v naming what it could not do",
					tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if rec.count() != tc.requests {
				t.Errorf("%d requests were made, want %d; refusing before the call is "+
					"what stops the caller paying for an answer they cannot use",
					rec.count(), tc.requests)
			}
		})
	}
}

// Rule §6.4: the credential comes from what the adapter was given, and which of
// the two forms it was given decides how the request is authenticated. Neither
// may fall back to the other silently.
func TestAuthentication(t *testing.T) {
	t.Parallel()

	t.Run("a token source is sent instead of the key", func(t *testing.T) {
		t.Parallel()

		_, rec := serve(t, reply{body: successBody()})
		p := New(rec.base, testKey, WithPollInterval(time.Millisecond),
			WithTokenSource(func(context.Context) (string, error) {
				return "eyJ0eXAiOiJKV1Qi.token", nil
			}))

		if _, err := p.Recognise(context.Background(), testPage()); err != nil {
			t.Fatalf("Recognise() error = %v", err)
		}
		_, _, key, auth, _ := rec.at(0)
		if auth != "Bearer eyJ0eXAiOiJKV1Qi.token" {
			t.Errorf("Authorization = %q, want the bearer token", auth)
		}
		if key != "" {
			t.Error("the subscription key was sent alongside the access token; a " +
				"provider given both must use one, so a caller can tell which " +
				"identity a call was made as")
		}
	})

	t.Run("a token source that fails is an authentication failure", func(t *testing.T) {
		t.Parallel()

		_, rec := serve(t, reply{body: successBody()})
		p := New(rec.base, "", WithTokenSource(func(context.Context) (string, error) {
			return "", errors.New("no credentials found")
		}))

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrAuth) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrAuth)
		}
		if rec.count() != 0 {
			t.Error("an unauthenticated request was sent")
		}
	})

	t.Run("an empty token is an authentication failure", func(t *testing.T) {
		t.Parallel()

		p, _ := serve(t, reply{body: successBody()})
		p = New(p.endpoint, "", WithTokenSource(func(context.Context) (string, error) {
			return "", nil
		}))

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrAuth) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrAuth)
		}
	})

	t.Run("no credential at all is an authentication failure", func(t *testing.T) {
		t.Parallel()

		p, _ := serve(t, reply{body: successBody()})
		p = New(p.endpoint, "")

		_, err := p.Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrAuth) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrAuth)
		}
	})

	t.Run("no endpoint at all is a rejected request", func(t *testing.T) {
		t.Parallel()

		_, err := New("", testKey).Recognise(context.Background(), testPage())
		if !errors.Is(err, ovrin.ErrBadRequest) {
			t.Errorf("err = %v, want it to classify as %v", err, ovrin.ErrBadRequest)
		}
	})
}

// ocr/google found that a *url.Error renders the URL it failed on, which for a
// provider authenticating in the query string puts the credential in every log
// line that unwraps the error. This service authenticates with a header — but
// the key must not reach the error by any other route either, and the assertion
// is cheap.
func TestTransportFailureCarriesNoCredential(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close() // nothing is listening now, so the next call cannot connect

	p := New(endpoint, testKey)
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
		if strings.Contains(e.Error(), testKey) {
			t.Fatalf("the subscription key appears in the error chain: %v", e)
		}
	}
}

// A blank page is a real thing a scanner produces. Calling it an error would
// make a document with one blank page unextractable, which is not the core's
// rule for a page that yielded nothing (rule §2.6).
func TestBlankPageIsNotAnError(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, reply{body: `{"status":"succeeded","analyzeResult":` +
		`{"apiVersion":"2024-11-30","modelId":"prebuilt-read","content":"","pages":[]}}`})

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
	if rec.Usage.PageUnits != 1 {
		t.Errorf("Usage.PageUnits = %d; a page the provider read and found empty "+
			"still cost a page", rec.Usage.PageUnits)
	}
}

// Spans are how the service says which words are on which line: a word's offset
// into the flat content is unambiguous, while "inside this rectangle" fails on
// overlapping lines and on any page with a rotation. Geometry is the fallback
// for a response that omits them.
func TestWordsAreGroupedBySpanAndThenByGeometry(t *testing.T) {
	t.Parallel()

	// Two lines whose boxes overlap, with the words assigned across them in a
	// way no geometric test could recover.
	body := `{"status":"succeeded","analyzeResult":{"content":"alpha beta","pages":[` +
		`{"pageNumber":1,"width":850,"height":1100,"unit":"pixel",` +
		`"spans":[{"offset":0,"length":10}],` +
		`"words":[` +
		`{"content":"alpha","polygon":[100,100,200,100,200,125,100,125],` +
		`"confidence":0.9,"span":{"offset":0,"length":5}},` +
		`{"content":"beta","polygon":[110,105,210,105,210,130,110,130],` +
		`"confidence":0.8,"span":{"offset":6,"length":4}}],` +
		`"lines":[` +
		`{"content":"alpha","polygon":[100,100,200,100,200,125,100,125],` +
		`"spans":[{"offset":0,"length":5}]},` +
		`{"content":"beta","polygon":[100,100,210,100,210,130,100,130],` +
		`"spans":[{"offset":6,"length":4}]}]}]}}`

	p, _ := serve(t, reply{body: body})
	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if len(rec.Lines) != 2 {
		t.Fatalf("Recognise() returned %d lines, want 2", len(rec.Lines))
	}
	for _, w := range rec.Words {
		if rec.Lines[w.Line].Text != w.Text {
			t.Errorf("word %q was put on line %q; the spans say otherwise",
				w.Text, rec.Lines[w.Line].Text)
		}
	}

	t.Run("a word no line claims keeps a line of its own", func(t *testing.T) {
		t.Parallel()

		orphan := `{"status":"succeeded","analyzeResult":{"content":"stray","pages":[` +
			`{"pageNumber":1,"width":850,"height":1100,"unit":"pixel",` +
			`"words":[{"content":"stray","polygon":[100,400,200,400,200,425,100,425],` +
			`"confidence":0.9,"span":{"offset":0,"length":5}}],"lines":[]}]}}`

		p, _ := serve(t, reply{body: orphan})
		rec, err := p.Recognise(context.Background(), testPage())
		if err != nil {
			t.Fatalf("Recognise() error = %v", err)
		}
		if len(rec.Words) != 1 || len(rec.Lines) != 1 {
			t.Fatalf("Recognise() returned %d words and %d lines, want one of each; "+
				"a word no line claims is still text the caller paid to have read",
				len(rec.Words), len(rec.Lines))
		}
		if rec.Lines[0].Text != "stray" {
			t.Errorf("line 0 text = %q, want %q", rec.Lines[0].Text, "stray")
		}
	})
}

// The service measures a PDF in inches and a rasterised image in its own
// pixels, and an adapter that assumed either would be wrong for half of its
// callers by a factor of about seventy.
func TestSpaceConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		page        resultPage
		dstW, dstH  float64
		want        ovrin.Rect
		polygon     []float64
		unsupported bool
	}{
		{
			name:    "a page with a size in points is scaled to it",
			page:    resultPage{Width: 850, Height: 1100, Unit: unitPixel},
			dstW:    612,
			dstH:    792,
			polygon: []float64{100, 200, 400, 200, 400, 225, 100, 225},
			want:    ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 162},
		},
		{
			name:    "a page measured in inches converts at 72 to the inch",
			page:    resultPage{Width: 8.5, Height: 11, Unit: unitInch},
			polygon: []float64{1, 2, 3, 2, 3, 2.25, 1, 2.25},
			want:    ovrin.Rect{MinX: 72, MinY: 144, MaxX: 216, MaxY: 162},
		},
		{
			name:    "a rotated polygon becomes its extremes",
			page:    resultPage{Width: 8.5, Height: 11, Unit: unitInch},
			polygon: []float64{1.05, 2, 3, 2.1, 2.95, 2.35, 1, 2.25},
			want:    ovrin.Rect{MinX: 72, MinY: 144, MaxX: 216, MaxY: 169.2},
		},
		{
			name:        "a page measured in pixels with nothing to scale against",
			page:        resultPage{Width: 850, Height: 1100, Unit: unitPixel},
			unsupported: true,
		},
		{
			name:        "a page in a unit nobody has heard of",
			page:        resultPage{Width: 10, Height: 10, Unit: "furlong"},
			unsupported: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sp, ok := newSpace(&tc.page, tc.dstW, tc.dstH)
			if ok == tc.unsupported {
				t.Fatalf("newSpace() ok = %v, want %v", ok, !tc.unsupported)
			}
			if tc.unsupported {
				return
			}
			got, ok := sp.rect(tc.polygon)
			if !ok {
				t.Fatal("rect() returned no rectangle for a polygon")
			}
			if !closeRect(got, tc.want) {
				t.Errorf("rect() = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("a polygon with no points has no rectangle", func(t *testing.T) {
		t.Parallel()

		sp := space{scaleX: 1, scaleY: 1}
		if _, ok := sp.rect(nil); ok {
			t.Error("rect(nil) returned a rectangle")
		}
	})
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

// The service publishes a confidence per word and none for the page, so a
// page-level figure can only be an aggregate — and a caller weighing it against
// another provider's needs to be told that.
func TestPageConfidenceIsDerivedAndSaysSo(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, reply{body: successBody()})
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
	if a.Unit != unitPixel {
		t.Errorf("Analysis.Unit = %q, want %q; a caller reading the polygons out of "+
			"JSON needs to know what they are measured in", a.Unit, unitPixel)
	}
	if a.Page != adaptertest.OCRPageNumber {
		t.Errorf("Analysis.Page = %d, want %d", a.Page, adaptertest.OCRPageNumber)
	}
	if len(a.JSON) == 0 {
		t.Error("Analysis.JSON is empty; it is the only route to what was discarded")
	}
}

// A locale has no field on ovrin's request to come from, so it can only arrive
// as an option — and an option that never reaches the wire is worse than no
// option, because the caller believes the hint was applied.
func TestLocaleReachesTheWire(t *testing.T) {
	t.Parallel()

	_, rec := serve(t, reply{body: successBody()})
	p := New(rec.base, testKey, WithLocale("ja"), WithModel("prebuilt-layout"),
		WithAPIVersion("2023-07-31"))

	if _, err := p.Recognise(context.Background(), testPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	_, uri, _, _, _ := rec.at(0)
	for _, want := range []string{"locale=ja", "prebuilt-layout", "api-version=2023-07-31"} {
		if !strings.Contains(uri, want) {
			t.Errorf("request uri = %q, want it to carry %q", uri, want)
		}
	}

	// And a provider given no locale sends none, rather than an empty one the
	// service would have to interpret.
	q, rec2 := serve(t, reply{body: successBody()})
	if _, err := q.Recognise(context.Background(), testPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if _, uri, _, _, _ := rec2.at(0); strings.Contains(uri, "locale") {
		t.Errorf("request uri = %q, want no locale at all", uri)
	}
}
