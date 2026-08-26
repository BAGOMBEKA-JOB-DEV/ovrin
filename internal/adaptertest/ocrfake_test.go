package adaptertest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"sort"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// fakeOCR is a compliant [ovrin.DocumentOCR] built from nothing but the
// standard library.
//
// It exists so this package's own tests can run the OCR contract against an
// adapter that obeys every rule the suite enforces. A suite that has never been
// run green against a correct adapter is a suite whose failures nobody can
// interpret.
//
// It is also, deliberately, the smallest complete worked example of the four
// OCR normalisations: its wire format reports pixels and confidences on 0..100
// in whatever order its blocks happened to fall — which is what Tesseract,
// Textract and Azure between them actually do — and every one of those is
// converted here rather than passed through.
//
// It is safe for concurrent use by multiple goroutines: it holds no mutable
// state.
type fakeOCR struct {
	url    string
	apiKey string

	// document is the content behind the [ovrin.Document] this provider will
	// be asked to read.
	//
	// It is held on the provider because ovrin.Document carries a document's
	// kind, page count and size but not its bytes, so there is currently no
	// other way for a DocumentOCR to reach what it is being asked to read. A
	// real adapter has the same problem and solves it the same way until the
	// core closes the gap.
	document []byte

	hc *http.Client
}

const fakeOCRName = "fakeocr"

// newFakeOCR returns a provider pointed at baseURL.
func newFakeOCR(baseURL, apiKey string, document []byte) *fakeOCR {
	return &fakeOCR{
		url:      baseURL,
		apiKey:   apiKey,
		document: document,
		// No timeout: bounding the call is the caller's context's job, and a
		// client timeout here would make the cancellation assertion pass for
		// the wrong reason.
		hc: &http.Client{},
	}
}

// Name implements [ovrin.OCR].
func (o *fakeOCR) Name() string { return fakeOCRName }

// fakeWire is the vendor's reply: pixels, 0..100 confidences, API order.
type fakeWire struct {
	Pages []fakeWirePage `json:"pages"`
}

type fakeWirePage struct {
	Width      int            `json:"width"`
	Height     int            `json:"height"`
	Language   string         `json:"language"`
	Confidence float64        `json:"confidence"`
	Words      []fakeWireWord `json:"words"`
}

type fakeWireWord struct {
	Text   string `json:"text"`
	Left   int    `json:"left"`
	Top    int    `json:"top"`
	Right  int    `json:"right"`
	Bottom int    `json:"bottom"`

	// Confidence is a pointer so an absent confidence is distinguishable from
	// a reported zero. Reading a missing value as 0 would be as much a
	// fabrication as reading it as 1.
	Confidence *float64 `json:"confidence"`
}

// fakeRaw is what the adapter puts in [ovrin.Recognition.Raw].
type fakeRaw struct {
	// Page is the provider's own page, unmodified.
	Page fakeWirePage

	// PageConfidenceUsed records that the provider reported no per-word
	// confidence and the page confidence was used instead. Without it a caller
	// cannot tell a page-wide confidence from a per-word one that happens to
	// be uniform (docs/rules.md §6.1).
	PageConfidenceUsed bool
}

// Recognise implements [ovrin.OCR].
func (o *fakeOCR) Recognise(ctx context.Context, page ovrin.Page) (*ovrin.Recognition, error) {
	if page.Image == nil {
		return nil, o.fail(ovrin.ErrUnsupported, page.Number,
			"the page carries no image, and there is nothing to recognise")
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, page.Image); err != nil {
		return nil, o.fail(ovrin.ErrInternal, page.Number,
			"the page image could not be encoded")
	}

	body, err := json.Marshal(map[string]any{
		"pageNumber": page.Number,
		"image": map[string]any{
			"content": base64.StdEncoding.EncodeToString(encoded.Bytes()),
		},
	})
	if err != nil {
		return nil, o.fail(ovrin.ErrInternal, page.Number,
			"the request could not be encoded")
	}

	wire, err := o.call(ctx, "/v1/pages:recognise", body, page.Number)
	if err != nil {
		return nil, err
	}
	if len(wire.Pages) == 0 {
		return nil, o.fail(ovrin.ErrNoContent, page.Number,
			"the provider recognised no page")
	}
	return o.normalise(wire.Pages[0], page.Number, page.Width, page.Height), nil
}

// RecogniseDocument implements [ovrin.DocumentOCR].
//
// The provider rasterises server-side, which is what lets a scanned PDF be read
// with no local renderer at all (ADR-0010).
func (o *fakeOCR) RecogniseDocument(ctx context.Context, doc ovrin.Document) ([]*ovrin.Recognition, error) {
	switch doc.Kind {
	case ovrin.KindPDF, ovrin.KindTIFF:
	default:
		return nil, o.fail(ovrin.ErrUnsupported, 0,
			"the provider reads only pdf and tiff documents, and this one is "+
				doc.Kind.String())
	}
	if len(o.document) == 0 {
		return nil, o.fail(ovrin.ErrUnsupported, 0,
			"no document content is configured, and ovrin.Document carries none")
	}

	body, err := json.Marshal(map[string]any{
		"mimeType": "application/pdf",
		"content":  base64.StdEncoding.EncodeToString(o.document),
	})
	if err != nil {
		return nil, o.fail(ovrin.ErrInternal, 0, "the request could not be encoded")
	}

	wire, err := o.call(ctx, "/v1/files:recognise", body, 0)
	if err != nil {
		return nil, err
	}

	out := make([]*ovrin.Recognition, 0, len(wire.Pages))
	for i, p := range wire.Pages {
		// The provider reports a PDF page in points already, so the source
		// space and the target space are the same one.
		out = append(out, o.normalise(p, i+1, float64(p.Width), float64(p.Height)))
	}
	return out, nil
}

// call posts one request and decodes the reply.
func (o *fakeOCR) call(ctx context.Context, path string, body []byte, page int) (*fakeWire, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, o.fail(ovrin.ErrBadRequest, page, "the request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, o.transportFail(ctx, page)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on read path

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, o.transportFail(ctx, page)
	}
	if resp.StatusCode >= 300 {
		// Deliberately does not read the provider's message. It quotes the
		// request back, and for OCR the request is the document
		// (docs/rules.md §2.5).
		return nil, o.fail(classifyStatus(resp.StatusCode), page,
			fmt.Sprintf("the provider returned http %d", resp.StatusCode))
	}

	var wire fakeWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, o.fail(ovrin.ErrBadResponse, page,
			"the provider's reply was not json")
	}
	return &wire, nil
}

// normalise converts one provider page into ovrin's shape.
//
// srcW and srcH are the provider's own coordinate space and dstW and dstH the
// page in points; every box is scaled between them, so a provider reporting
// pixels and one reporting points are handled by the same three lines.
func (o *fakeOCR) normalise(p fakeWirePage, page int, dstW, dstH float64) *ovrin.Recognition {
	pageConf := p.Confidence / 100
	fellBack := false

	type placed struct {
		word ovrin.Word
		midY float64
	}
	placedWords := make([]placed, 0, len(p.Words))

	scaleX, scaleY := 1.0, 1.0
	if p.Width > 0 && dstW > 0 {
		scaleX = dstW / float64(p.Width)
	}
	if p.Height > 0 && dstH > 0 {
		scaleY = dstH / float64(p.Height)
	}

	for _, w := range p.Words {
		conf := pageConf
		if w.Confidence != nil {
			conf = *w.Confidence / 100
		} else {
			// The page confidence, recorded as such. Fabricating 1.0 would
			// tell the confidence engine every word was read perfectly
			// (docs/rules.md §6.1).
			fellBack = true
		}
		box := ovrin.Rect{
			MinX: float64(w.Left) * scaleX,
			MinY: float64(w.Top) * scaleY,
			MaxX: float64(w.Right) * scaleX,
			MaxY: float64(w.Bottom) * scaleY,
		}
		placedWords = append(placedWords, placed{
			word: ovrin.Word{Text: w.Text, Box: box, Confidence: conf},
			midY: (box.MinY + box.MaxY) / 2,
		})
	}

	// Reading order: top to bottom, then left to right within a line. The
	// provider returns words in whatever order its blocks fell in, and anything
	// with two columns is where that stops being close enough.
	sort.SliceStable(placedWords, func(a, b int) bool {
		if placedWords[a].midY != placedWords[b].midY {
			return placedWords[a].midY < placedWords[b].midY
		}
		return placedWords[a].word.Box.MinX < placedWords[b].word.Box.MinX
	})

	var (
		words []ovrin.Word
		lines []ovrin.Line
	)
	for _, pw := range placedWords {
		w := pw.word
		// A word belongs to the line it vertically overlaps; anything else
		// starts one.
		if n := len(lines); n > 0 && pw.midY >= lines[n-1].Box.MinY && pw.midY <= lines[n-1].Box.MaxY {
			w.Line = n - 1
			lines[n-1].Text += " " + w.Text
			lines[n-1].Box = union(lines[n-1].Box, w.Box)
		} else {
			w.Line = n
			lines = append(lines, ovrin.Line{Text: w.Text, Box: w.Box, Page: page})
		}
		words = append(words, w)
	}

	// Within a line the sort above ordered by midpoint, which is not quite left
	// to right when baselines differ slightly.
	sortWordsWithinLines(words)

	return &ovrin.Recognition{
		Words:      words,
		Lines:      lines,
		Confidence: pageConf,
		Language:   p.Language,
		Raw:        &fakeRaw{Page: p, PageConfidenceUsed: fellBack},
	}
}

// sortWordsWithinLines puts each line's words in left-to-right order without
// disturbing the order of the lines themselves.
func sortWordsWithinLines(words []ovrin.Word) {
	sort.SliceStable(words, func(a, b int) bool {
		if words[a].Line != words[b].Line {
			return words[a].Line < words[b].Line
		}
		return words[a].Box.MinX < words[b].Box.MinX
	})
}

// union returns the smallest rect containing both.
func union(a, b ovrin.Rect) ovrin.Rect {
	if b.MinX < a.MinX {
		a.MinX = b.MinX
	}
	if b.MinY < a.MinY {
		a.MinY = b.MinY
	}
	if b.MaxX > a.MaxX {
		a.MaxX = b.MaxX
	}
	if b.MaxY > a.MaxY {
		a.MaxY = b.MaxY
	}
	return a
}

// fail builds a classified ovrin error with an adapter-authored message.
func (o *fakeOCR) fail(kind error, page int, message string) error {
	return &ovrin.Error{
		Op:       ovrin.OpOCR,
		Provider: fakeOCRName,
		Page:     page,
		Kind:     kind,
		Message:  message,
	}
}

// transportFail reports a call that never produced a usable response.
//
// When the context ended the context's own error is attached as a cause, so one
// value answers both "what kind of failure was this?" and "was it ultimately a
// cancelled context?" (ADR-0019). Only the context error is attached: it is a
// fixed string, whereas a provider's error may quote the document.
func (o *fakeOCR) transportFail(ctx context.Context, page int) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return (&ovrin.Error{
			Op:       ovrin.OpOCR,
			Provider: fakeOCRName,
			Page:     page,
			Message:  "the context ended before the provider replied",
		}).WithCause(ctxErr)
	}
	return o.fail(ovrin.ErrUnavailable, page, "the provider could not be reached")
}

var _ ovrin.DocumentOCR = (*fakeOCR)(nil)
