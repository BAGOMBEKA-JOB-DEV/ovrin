package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/adaptertest"
)

const testAPIKey = "AIza-ovrin-adapter-test-key"

// ---------------------------------------------------------------------------
// Fixtures
//
// Built as text rather than by marshalling this package's own wire structs. A
// fixture produced by the decoder it is decoded with cannot catch a wrong json
// tag, which is one of the two things a wire fixture exists to catch.
// ---------------------------------------------------------------------------

// fixWord is one word of a fixture, in the provider's own coordinate space.
type fixWord struct {
	text           string
	x0, y0, x1, y1 int

	// conf is Vision's per-word confidence, or nil for a response that reports
	// none.
	conf *float64

	// brk is the break Vision detected after the word, reported on its last
	// symbol.
	brk string
}

func conf(v float64) *float64 { return &v }

// poly renders a bounding polygon the way Vision does: four vertices,
// clockwise from the top left.
func poly(x0, y0, x1, y1 int) string {
	return fmt.Sprintf(`{"vertices":[{"x":%d,"y":%d},{"x":%d,"y":%d},`+
		`{"x":%d,"y":%d},{"x":%d,"y":%d}]}`, x0, y0, x1, y0, x1, y1, x0, y1)
}

func wordJSON(w fixWord) string {
	runes := []rune(w.text)
	syms := make([]string, 0, len(runes))
	for i, r := range runes {
		br := ""
		if i == len(runes)-1 && w.brk != "" {
			br = fmt.Sprintf(`,"property":{"detectedBreak":{"type":%q}}`, w.brk)
		}
		syms = append(syms, fmt.Sprintf(`{"boundingBox":%s,"text":%q%s}`,
			poly(w.x0, w.y0, w.x1, w.y1), string(r), br))
	}
	c := ""
	if w.conf != nil {
		c = fmt.Sprintf(`,"confidence":%g`, *w.conf)
	}
	return fmt.Sprintf(`{"boundingBox":%s,"symbols":[%s]%s}`,
		poly(w.x0, w.y0, w.x1, w.y1), strings.Join(syms, ","), c)
}

// blockJSON renders one block holding one paragraph, which is the shape Vision
// produces for an ordinary run of body text.
func blockJSON(words []fixWord) string {
	x0, y0, x1, y1 := words[0].x0, words[0].y0, words[0].x1, words[0].y1
	encoded := make([]string, 0, len(words))
	for _, w := range words {
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
		encoded = append(encoded, wordJSON(w))
	}
	return fmt.Sprintf(`{"boundingBox":%s,"blockType":"TEXT","paragraphs":`+
		`[{"boundingBox":%s,"words":[%s]}]}`,
		poly(x0, y0, x1, y1), poly(x0, y0, x1, y1), strings.Join(encoded, ","))
}

func pageJSON(width, height int, pageConf float64, lang string, blocks [][]fixWord) string {
	encoded := make([]string, 0, len(blocks))
	for _, b := range blocks {
		encoded = append(encoded, blockJSON(b))
	}
	return fmt.Sprintf(`{"property":{"detectedLanguages":[{"languageCode":%q,`+
		`"confidence":0.98}]},"width":%d,"height":%d,"confidence":%g,"blocks":[%s]}`,
		lang, width, height, pageConf, strings.Join(encoded, ","))
}

// imagesResponse wraps a page the way images:annotate does.
func imagesResponse(page string) string {
	return fmt.Sprintf(`{"responses":[{"fullTextAnnotation":`+
		`{"pages":[%s],"text":"ignored"}}]}`, page)
}

// filesResponse wraps pages the way files:annotate does, which is one level
// deeper: a response per request, each holding a response per page.
func filesResponse(pages []string) string {
	inner := make([]string, 0, len(pages))
	for i, p := range pages {
		inner = append(inner, fmt.Sprintf(
			`{"fullTextAnnotation":{"pages":[%s],"text":"ignored"},`+
				`"context":{"pageNumber":%d}}`, p, i+1))
	}
	return fmt.Sprintf(`{"responses":[{"responses":[%s],"totalPages":%d}]}`,
		strings.Join(inner, ","), len(pages))
}

// The blocks are deliberately emitted bottom-to-top, and the words inside two
// of them right-to-left: Vision's block order follows its own segmentation, and
// an adapter that returns what arrived returns nonsense for anything laid out
// in more than one run.
var pageBlocks = [][]fixWord{
	{
		{text: "1,234.56", x0: 600, y0: 900, x1: 800, y1: 925, conf: conf(0.62)},
		{text: "Total", x0: 100, y0: 900, x1: 180, y1: 925, conf: conf(0.95), brk: "LINE_BREAK"},
	},
	{
		{text: "Corporation", x0: 190, y0: 200, x1: 400, y1: 225, conf: conf(0.93)},
		{text: "Acme", x0: 100, y0: 200, x1: 175, y1: 225, conf: conf(0.87), brk: "LINE_BREAK"},
	},
	{
		{text: "INVOICE", x0: 100, y0: 100, x1: 250, y1: 125, conf: conf(0.99), brk: "LINE_BREAK"},
	},
}

// pageBlocksNoConfidence is the same page with no per-word confidence, which is
// what Vision returns for some scripts and some low-resolution scans.
var pageBlocksNoConfidence = [][]fixWord{
	{
		{text: "1,234.56", x0: 600, y0: 900, x1: 800, y1: 925},
		{text: "Total", x0: 100, y0: 900, x1: 180, y1: 925, brk: "LINE_BREAK"},
	},
	{
		{text: "Corporation", x0: 190, y0: 200, x1: 400, y1: 225},
		{text: "Acme", x0: 100, y0: 200, x1: 175, y1: 225, brk: "LINE_BREAK"},
	},
	{
		{text: "INVOICE", x0: 100, y0: 100, x1: 250, y1: 125, brk: "LINE_BREAK"},
	},
}

// The document pages are in the PDF's own points, which is what Vision reports
// for a file rather than for an image.
var documentPageOne = [][]fixWord{
	{
		{text: "Bottom", x0: 100, y0: 700, x1: 200, y1: 720, conf: conf(0.70), brk: "LINE_BREAK"},
	},
	{
		{text: "ONE", x0: 210, y0: 100, x1: 280, y1: 120, conf: conf(0.80)},
		{text: "PAGE", x0: 100, y0: 100, x1: 200, y1: 120, conf: conf(0.90), brk: "LINE_BREAK"},
	},
}

var documentPageTwo = [][]fixWord{
	{
		{text: "Footer", x0: 100, y0: 710, x1: 190, y1: 730, conf: conf(0.66), brk: "LINE_BREAK"},
	},
	{
		{text: "PAGE2", x0: 230, y0: 100, x1: 320, y1: 120, conf: conf(0.77)},
		{text: "SECOND", x0: 100, y0: 100, x1: 220, y1: 120, conf: conf(0.88), brk: "LINE_BREAK"},
	},
}

// wantPage is the fixture normalised: page points, 0..1, reading order.
//
// The pixel values are scaled by 612/850, which is exactly 0.72 — the ratio a
// 100 DPI raster of a US Letter page has.
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
	Confidence: 0.94,
	Language:   "en",
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
		Confidence: 0.91,
		Language:   "en",
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
		Confidence: 0.85,
		Language:   "en",
	},
}

func successBody() string {
	return imagesResponse(pageJSON(850, 1100, 0.94, "en", pageBlocks))
}

func noWordConfidenceBody() string {
	return imagesResponse(pageJSON(850, 1100, 0.78, "en", pageBlocksNoConfidence))
}

func documentBody() string {
	return filesResponse([]string{
		pageJSON(612, 792, 0.91, "en", documentPageOne),
		pageJSON(612, 792, 0.85, "en", documentPageTwo),
	})
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
		Name: "google",
		New: func(baseURL string) ovrin.OCR {
			return New(testAPIKey, WithBaseURL(baseURL))
		},
		NewDocument: func(baseURL string) ovrin.DocumentOCR {
			return New(testAPIKey, WithBaseURL(baseURL))
		},
		APIKey:       testAPIKey,
		ProviderName: providerName,

		SuccessBody: successBody(),
		Want:        wantPage,
		// Vision spells a word out one symbol at a time, so the suite cannot
		// find the words in the fixture to infer what order they arrived in.
		APIOrder: []int{4, 3, 2, 1, 0},

		PageConfidenceBody: noWordConfidenceBody(),
		WantPageConfidence: 0.78,
		UsedPageConfidence: func(raw any) bool {
			a, ok := raw.(*Annotation)
			return ok && a.WordConfidenceFromPage
		},

		DocumentBody:    documentBody(),
		WantDocument:    wantDocument,
		UnsupportedKind: ovrin.KindDOCX,

		ErrorBody: `{"error":{"code":429,"message":"Quota exceeded",` +
			`"status":"RESOURCE_EXHAUSTED"}}`,
		EchoErrorBody: func(echo string) string {
			return fmt.Sprintf(`{"error":{"code":400,"message":%q,`+
				`"status":"INVALID_ARGUMENT"}}`,
				"Invalid image content: "+echo)
		},
	})
}

// ---------------------------------------------------------------------------
// What only this adapter's own test can check
// ---------------------------------------------------------------------------

// serve starts a test server answering with status and body, and returns a
// provider pointed at it along with the requests it received.
func serve(t *testing.T, status int, body string) (*Provider, *[]string) {
	t.Helper()

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body) //nolint:errcheck // a short read is still evidence
		got = append(got, r.URL.RequestURI()+"\n"+r.Header.Get("Authorization")+"\n"+string(buf))
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body)) //nolint:errcheck // the assertion reports the failure
	}))
	t.Cleanup(srv.Close)

	return New(testAPIKey, WithBaseURL(srv.URL)), &got
}

func testPage() ovrin.Page { return adaptertest.OCRPage() }

// Vision reports a failed page inside a 200, so an adapter that classified on
// the HTTP status alone would hand the caller an empty page and call it a scan
// with no text on it.
func TestPerPageErrorInsideASuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
		want error
	}{
		{"PERMISSION_DENIED is an authentication failure", 7, ovrin.ErrAuth},
		{"UNAUTHENTICATED is an authentication failure", 16, ovrin.ErrAuth},
		{"RESOURCE_EXHAUSTED is a rate limit", 8, ovrin.ErrRateLimit},
		{"INVALID_ARGUMENT is a rejected request", 3, ovrin.ErrBadRequest},
		{"UNIMPLEMENTED is unsupported", 12, ovrin.ErrUnsupported},
		{"UNAVAILABLE is an unavailable provider", 14, ovrin.ErrUnavailable},
		{"an unknown code is an unavailable provider", 99, ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := fmt.Sprintf(`{"responses":[{"error":{"code":%d,`+
				`"message":"%s"}}]}`, tc.code, adaptertest.ContentCanary)
			p, _ := serve(t, http.StatusOK, body)

			rec, err := p.Recognise(context.Background(), testPage())
			if err == nil {
				t.Fatal("Recognise() error = nil for a page Vision reported as failed")
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

// A blank page is a real thing a scanner produces. Calling it an error would
// make a document with one blank page unextractable, which is not the core's
// rule for a page that yielded nothing (rule §2.6).
func TestBlankPageIsNotAnError(t *testing.T) {
	t.Parallel()

	p, _ := serve(t, http.StatusOK, `{"responses":[{}]}`)

	rec, err := p.Recognise(context.Background(), testPage())
	if err != nil {
		t.Fatalf("Recognise() error = %v for a page with no text", err)
	}
	if len(rec.Words) != 0 || len(rec.Lines) != 0 {
		t.Errorf("Recognise() invented %d words and %d lines for a blank page",
			len(rec.Words), len(rec.Lines))
	}
	if rec.Raw == nil {
		t.Error("Recognition.Raw is nil even for a blank page")
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
			name: "a document that is not a pdf",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					ovrin.Document{Kind: ovrin.KindTIFF, Pages: 1})
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			name: "a document longer than the synchronous endpoint reads",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					ovrin.Document{Kind: ovrin.KindPDF, Pages: maxSyncPages + 1})
				return err
			},
			want: ovrin.ErrUnsupported,
		},
		{
			// Not ErrUnsupported: this provider reads documents perfectly
			// well, it was simply handed one with nothing in it. Saying
			// "unsupported" would send the caller looking for a provider that
			// does support documents, which is the wrong fix.
			name: "a document carrying no bytes",
			call: func(p *Provider) error {
				_, err := p.RecogniseDocument(context.Background(),
					ovrin.Document{Kind: ovrin.KindPDF, Pages: 1})
				return err
			},
			want: ovrin.ErrNoContent,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, requests := serve(t, http.StatusOK, successBody())
			err := tc.call(p)
			if err == nil {
				t.Fatalf("call succeeded; it must return %v naming what it could not do",
					tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to classify as %v", err, tc.want)
			}
			if len(*requests) != 0 {
				t.Error("a request was sent for something the provider cannot serve; " +
					"refusing before the call is what stops the caller paying for it")
			}
		})
	}
}

// Vision reads at most five pages synchronously and reports what it skipped.
// Handing back the pages that did arrive is the silent truncation §6.1 forbids.
func TestTruncatedDocumentIsRefused(t *testing.T) {
	t.Parallel()

	body := `{"responses":[{"responses":[{"fullTextAnnotation":{"pages":[` +
		pageJSON(612, 792, 0.9, "en", documentPageOne) +
		`]},"context":{"pageNumber":1}}],"totalPages":9}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body)) //nolint:errcheck // the assertion reports the failure
	}))
	t.Cleanup(srv.Close)

	p := New(testAPIKey, WithBaseURL(srv.URL),
		WithDocumentContent(func(context.Context, ovrin.Document) ([]byte, error) {
			return []byte("%PDF-1.7"), nil
		}))

	recs, err := p.RecogniseDocument(context.Background(),
		ovrin.Document{Kind: ovrin.KindPDF, Pages: 0})
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

// Rule §6.4: the credential comes from what the adapter was given. Which of the
// two forms it was given decides how the request is authenticated, and neither
// may fall back to the other silently.
func TestAuthentication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		provider   func(baseURL string) *Provider
		wantInURI  string
		wantHeader string
		wantErr    error
	}{
		{
			name: "an api key travels in the query",
			provider: func(u string) *Provider {
				return New(testAPIKey, WithBaseURL(u))
			},
			wantInURI: testAPIKey,
		},
		{
			name: "a token source travels in the header instead",
			provider: func(u string) *Provider {
				return New(testAPIKey, WithBaseURL(u),
					WithTokenSource(func(context.Context) (string, error) {
						return "ya29.token", nil
					}))
			},
			wantHeader: "Bearer ya29.token",
		},
		{
			name: "a token source that fails is an authentication failure",
			provider: func(u string) *Provider {
				return New("", WithBaseURL(u),
					WithTokenSource(func(context.Context) (string, error) {
						return "", errors.New("no credentials found")
					}))
			},
			wantErr: ovrin.ErrAuth,
		},
		{
			name: "an empty token is an authentication failure",
			provider: func(u string) *Provider {
				return New("", WithBaseURL(u),
					WithTokenSource(func(context.Context) (string, error) {
						return "", nil
					}))
			},
			wantErr: ovrin.ErrAuth,
		},
		{
			name: "no credential at all is an authentication failure",
			provider: func(u string) *Provider {
				return New("", WithBaseURL(u))
			},
			wantErr: ovrin.ErrAuth,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var uri, auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uri, auth = r.URL.RequestURI(), r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(successBody())) //nolint:errcheck // asserted below
			}))
			t.Cleanup(srv.Close)

			_, err := tc.provider(srv.URL).Recognise(context.Background(), testPage())

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want it to classify as %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Recognise() error = %v", err)
			}
			if tc.wantInURI != "" && !strings.Contains(uri, tc.wantInURI) {
				t.Errorf("request URI = %q, want it to carry the api key", uri)
			}
			if tc.wantHeader != "" && auth != tc.wantHeader {
				t.Errorf("Authorization = %q, want %q", auth, tc.wantHeader)
			}
			if tc.wantHeader != "" && strings.Contains(uri, testAPIKey) {
				t.Error("the api key was sent alongside the access token; a provider " +
					"given both must use one, so that a caller can tell which " +
					"identity a call was made as")
			}
		})
	}
}

// A *url.Error renders the URL it failed on, and this package puts the API key
// in the query. Attaching one unchanged as a cause would put the credential in
// every log line that unwraps the error.
func TestTransportFailureNeverCarriesTheCredential(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now, so the next call cannot connect

	p := New(testAPIKey, WithBaseURL(url))
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
		if strings.Contains(e.Error(), testAPIKey) {
			t.Fatalf("the api key appears in the error chain: %v", e)
		}
	}
}

// Language hints have no field on ovrin's request to come from, so they can only
// arrive as an option — and an option that never reaches the wire is worse than
// no option, because the caller believes the hint was applied.
func TestLanguageHintsReachTheWire(t *testing.T) {
	t.Parallel()

	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body) //nolint:errcheck // a short read is still evidence
		body = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody())) //nolint:errcheck // asserted below
	}))
	t.Cleanup(srv.Close)

	p := New(testAPIKey, WithBaseURL(srv.URL), WithLanguageHints("ja", "en"))
	if _, err := p.Recognise(context.Background(), testPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if !strings.Contains(body, `"languageHints":["ja","en"]`) {
		t.Errorf("the language hints never reached the provider\nbody: %s", body)
	}

	// And a provider given none sends no imageContext at all, rather than an
	// empty one Vision would have to interpret.
	body = ""
	if _, err := New(testAPIKey, WithBaseURL(srv.URL)).
		Recognise(context.Background(), testPage()); err != nil {
		t.Fatalf("Recognise() error = %v", err)
	}
	if strings.Contains(body, "imageContext") {
		t.Errorf("an empty imageContext was sent\nbody: %s", body)
	}
}

// Vision reports geometry in two shapes and a reader that knew only one would
// silently return zero boxes for the other.
func TestRectConversion(t *testing.T) {
	t.Parallel()

	// 850 × 1100 pixels onto a 612 × 792 point page: a factor of 0.72.
	sp := newSpace(850, 1100, 612, 792)

	tests := []struct {
		name string
		poly *boundingPoly
		want ovrin.Rect
		ok   bool
	}{
		{
			name: "pixel vertices are scaled into points",
			poly: &boundingPoly{Vertices: []vertex{
				{X: 100, Y: 200}, {X: 400, Y: 200}, {X: 400, Y: 225}, {X: 100, Y: 225},
			}},
			want: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 162},
			ok:   true,
		},
		{
			name: "a rotated polygon becomes its extremes",
			poly: &boundingPoly{Vertices: []vertex{
				{X: 110, Y: 200}, {X: 400, Y: 210}, {X: 390, Y: 235}, {X: 100, Y: 225},
			}},
			want: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 169.2},
			ok:   true,
		},
		{
			name: "normalised vertices are fractions of the page",
			poly: &boundingPoly{NormalizedVertices: []vertex{
				{X: 0, Y: 0}, {X: 0.5, Y: 0}, {X: 0.5, Y: 0.25}, {X: 0, Y: 0.25},
			}},
			want: ovrin.Rect{MinX: 0, MinY: 0, MaxX: 306, MaxY: 198},
			ok:   true,
		},
		{name: "a nil polygon has no rectangle"},
		{name: "a polygon with no vertices has no rectangle", poly: &boundingPoly{}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := sp.rect(tc.poly)
			if ok != tc.ok {
				t.Fatalf("rect() ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
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

	sp := newSpace(612, 792, 0, 0)

	tests := []struct {
		name  string
		boxes []ovrin.Rect
		want  []int
	}{
		{
			name: "blocks are read top to bottom",
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
				{MinX: 320, MinY: 102, MaxX: 540, MaxY: 400},
				{MinX: 72, MinY: 100, MaxX: 290, MaxY: 400},
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
		{
			name:  "an empty page has no order",
			boxes: nil,
			want:  nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := readingOrder(tc.boxes, sp)
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

// Vision reports the gap after a word on the word's last symbol, so a reader
// looking anywhere else runs every line of a page together.
func TestBreaks(t *testing.T) {
	t.Parallel()

	word := func(brk string) *textWord {
		w := &textWord{Symbols: []textSymbol{{Text: "a"}, {Text: "b"}}}
		if brk != "" {
			w.Symbols[1].Property = &textProperty{DetectedBreak: &detectedBreak{Type: brk}}
		}
		return w
	}

	tests := []struct {
		name       string
		brk        string
		wantEnds   bool
		wantJoined bool
	}{
		{name: "no break continues the line", brk: ""},
		{name: "a space continues the line", brk: "SPACE"},
		{name: "a sure space continues the line", brk: "SURE_SPACE"},
		{name: "a line break ends the line", brk: "LINE_BREAK", wantEnds: true},
		{name: "an end-of-line space ends the line", brk: "EOL_SURE_SPACE", wantEnds: true},
		{name: "a hyphen joins the next word", brk: "HYPHEN", wantJoined: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := word(tc.brk)
			if got := endsLine(w); got != tc.wantEnds {
				t.Errorf("endsLine() = %v, want %v", got, tc.wantEnds)
			}
			if got := joinsWithoutSpace(w); got != tc.wantJoined {
				t.Errorf("joinsWithoutSpace() = %v, want %v", got, tc.wantJoined)
			}
		})
	}
}

func TestClassifyStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"401 is an authentication failure", http.StatusUnauthorized, ovrin.ErrAuth},
		{"403 is an authentication failure", http.StatusForbidden, ovrin.ErrAuth},
		{"429 is a rate limit", http.StatusTooManyRequests, ovrin.ErrRateLimit},
		{"400 is a rejected request", http.StatusBadRequest, ovrin.ErrBadRequest},
		{"404 is a rejected request", http.StatusNotFound, ovrin.ErrBadRequest},
		{"500 is an unavailable provider", http.StatusInternalServerError, ovrin.ErrUnavailable},
		{"503 is an unavailable provider", http.StatusServiceUnavailable, ovrin.ErrUnavailable},
		{"a 3xx nobody expects is an unavailable provider", http.StatusFound, ovrin.ErrUnavailable},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyStatus(tc.status); !errors.Is(got, tc.want) {
				t.Errorf("classifyStatus(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
