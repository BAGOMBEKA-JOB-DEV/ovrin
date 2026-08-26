package google

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// featureDocumentText asks Vision for dense document OCR rather than the sparse
// TEXT_DETECTION feature.
//
// The difference is not a quality setting: TEXT_DETECTION is tuned for a
// photograph of a sign and returns a flat list of strings, while
// DOCUMENT_TEXT_DETECTION returns the block/paragraph/word/symbol hierarchy
// this package needs to rebuild lines and per-word confidence. Ovrin reads
// documents, so there is nothing to choose between them.
const featureDocumentText = "DOCUMENT_TEXT_DETECTION"

// Annotation is what a [Provider] puts in [ovrin.Recognition.Raw].
//
// Normalisation deliberately discards structure Vision reports — per-symbol
// geometry, block types, per-word languages (ADR-0009) — and this is the route
// back to it. The annotation is kept as bytes rather than as decoded structs so
// that this package does not have to export fifteen wire types, and so that a
// caller can unmarshal it into whatever shape they actually want.
type Annotation struct {
	// JSON is Vision's own annotation for this page, exactly as it arrived.
	JSON json.RawMessage

	// WordConfidenceFromPage records that Vision reported no per-word
	// confidence and the page's own was used for every word instead.
	//
	// Without it a caller cannot tell a page-wide confidence from a per-word
	// one that happens to be uniform — and the alternative, reporting 1.0,
	// would tell the confidence engine every word was read perfectly
	// (rule §6.1, ADR-0009).
	WordConfidenceFromPage bool
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

type imagesAnnotateRequest struct {
	Requests []imageRequest `json:"requests"`
}

type imageRequest struct {
	Image        imageSource   `json:"image"`
	Features     []feature     `json:"features"`
	ImageContext *imageContext `json:"imageContext,omitempty"`
}

type imageSource struct {
	Content string `json:"content"`
}

type filesAnnotateRequest struct {
	Requests []fileRequest `json:"requests"`
}

type fileRequest struct {
	InputConfig  inputConfig   `json:"inputConfig"`
	Features     []feature     `json:"features"`
	Pages        []int         `json:"pages,omitempty"`
	ImageContext *imageContext `json:"imageContext,omitempty"`
}

type inputConfig struct {
	MimeType string `json:"mimeType"`
	Content  string `json:"content"`
}

type feature struct {
	Type string `json:"type"`
}

type imageContext struct {
	LanguageHints []string `json:"languageHints,omitempty"`
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

// imagesAnnotateResponse keeps each per-page reply as raw bytes so that
// [Annotation.JSON] can carry exactly what arrived rather than a re-marshalled
// approximation of it.
type imagesAnnotateResponse struct {
	Responses []json.RawMessage `json:"responses"`
	Error     *status           `json:"error"`
}

type filesAnnotateResponse struct {
	Responses []fileResponse `json:"responses"`
	Error     *status        `json:"error"`
}

type fileResponse struct {
	Responses  []json.RawMessage `json:"responses"`
	TotalPages int               `json:"totalPages"`
	Error      *status           `json:"error"`
}

type imageResponse struct {
	FullTextAnnotation *textAnnotation `json:"fullTextAnnotation"`
	Error              *status         `json:"error"`
}

// status is Google's error object, which arrives inside a 200 as often as with
// a failing status code.
type status struct {
	Code int `json:"code"`

	// Message is decoded but never read. Vision quotes the offending request
	// back in a validation error, so copying this into an ovrin error would
	// ship the document into the caller's logs (rule §2.5). It is here so that
	// the field is accounted for rather than looking forgotten.
	Message string `json:"message"`
}

// summary describes a provider error without quoting it.
func (s *status) summary() string {
	return fmt.Sprintf("the provider reported error code %d", s.Code)
}

type textAnnotation struct {
	Pages []textPage `json:"pages"`
	Text  string     `json:"text"`
}

type textPage struct {
	Property   *textProperty `json:"property"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	Blocks     []textBlock   `json:"blocks"`
	Confidence float64       `json:"confidence"`
}

type textBlock struct {
	BoundingBox *boundingPoly   `json:"boundingBox"`
	Paragraphs  []textParagraph `json:"paragraphs"`
	BlockType   string          `json:"blockType"`
}

type textParagraph struct {
	BoundingBox *boundingPoly `json:"boundingBox"`
	Words       []textWord    `json:"words"`
}

type textWord struct {
	Property    *textProperty `json:"property"`
	BoundingBox *boundingPoly `json:"boundingBox"`
	Symbols     []textSymbol  `json:"symbols"`

	// Confidence is a pointer because Vision omits the field rather than
	// sending a zero, and reading an absent confidence as 0 would be as much a
	// fabrication as reading it as 1.
	Confidence *float64 `json:"confidence"`
}

type textSymbol struct {
	Property    *textProperty `json:"property"`
	BoundingBox *boundingPoly `json:"boundingBox"`
	Text        string        `json:"text"`
}

type textProperty struct {
	DetectedLanguages []detectedLanguage `json:"detectedLanguages"`
	DetectedBreak     *detectedBreak     `json:"detectedBreak"`
}

type detectedLanguage struct {
	LanguageCode string  `json:"languageCode"`
	Confidence   float64 `json:"confidence"`
}

type detectedBreak struct {
	Type string `json:"type"`
}

// The break types Vision reports on the last symbol of a word.
//
// Only the two that end a line and the one that joins a word to the next
// without a space are acted on; the rest fall through to a plain space.
const (
	breakLine     = "LINE_BREAK"
	breakEOLSpace = "EOL_SURE_SPACE"
	breakHyphen   = "HYPHEN"
)

type boundingPoly struct {
	Vertices []vertex `json:"vertices"`

	// NormalizedVertices carries the same geometry as fractions of the page,
	// and is what some Vision responses use instead of Vertices. A reader that
	// knew only one of the two would silently return zero boxes for the other.
	NormalizedVertices []vertex `json:"normalizedVertices"`
}

// vertex serves both integer pixel vertices and fractional normalised ones;
// json.Number is not needed because both decode into float64 without loss at
// the magnitudes a page uses.
type vertex struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

// classifyStatus maps an HTTP status onto an ovrin sentinel.
//
// Statuses, never message text: a vendor rewording a response must not change
// how a program behaves (rule §2.2).
func classifyStatus(code int) error {
	switch {
	case code == http.StatusUnauthorized,
		code == http.StatusForbidden,
		code == http.StatusProxyAuthRequired:
		return ovrin.ErrAuth
	case code == http.StatusTooManyRequests:
		return ovrin.ErrRateLimit
	case code >= 500:
		return ovrin.ErrUnavailable
	case code >= 400:
		return ovrin.ErrBadRequest
	default:
		return ovrin.ErrUnavailable
	}
}

// classifyCode maps a Google API error code onto an ovrin sentinel.
//
// The codes are the canonical gRPC ones, which Vision reports inside a 200 as
// well as alongside a failing status — so a caller checking only the HTTP
// status would treat a permission failure as a successful empty page.
func classifyCode(code int) error {
	switch code {
	case 7, 16: // PERMISSION_DENIED, UNAUTHENTICATED
		return ovrin.ErrAuth
	case 8: // RESOURCE_EXHAUSTED
		return ovrin.ErrRateLimit
	case 3, 5, 9, 11: // INVALID_ARGUMENT, NOT_FOUND, FAILED_PRECONDITION, OUT_OF_RANGE
		return ovrin.ErrBadRequest
	case 12: // UNIMPLEMENTED
		return ovrin.ErrUnsupported
	case 4, 10, 13, 14: // DEADLINE_EXCEEDED, ABORTED, INTERNAL, UNAVAILABLE
		return ovrin.ErrUnavailable
	default:
		return ovrin.ErrUnavailable
	}
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// space converts Vision's coordinates into the page's.
//
// Vision reports a rasterised page in the pixels of the image it was sent and a
// PDF page in the PDF's own points, so one conversion covers both: scale from
// whatever the provider used into whatever the page says it is.
type space struct {
	scaleX, scaleY float64
	width, height  float64
}

// newSpace builds the conversion from the provider's page geometry to the
// caller's. A dstW or dstH of zero means the provider's space is already the
// caller's, which is the PDF case.
func newSpace(srcW, srcH, dstW, dstH float64) space {
	s := space{scaleX: 1, scaleY: 1, width: srcW, height: srcH}
	if dstW > 0 {
		s.width = dstW
		if srcW > 0 {
			s.scaleX = dstW / srcW
		}
	}
	if dstH > 0 {
		s.height = dstH
		if srcH > 0 {
			s.scaleY = dstH / srcH
		}
	}
	return s
}

// rect converts one bounding polygon into a page rectangle.
//
// Vision's polygon is four vertices in clockwise order from the top left, but
// a rotated page produces one that is not axis-aligned, so the extremes are
// taken rather than the corners assumed. The origin is already top left on
// Vision's side, which is the one convention ovrin and Vision agree on
// (ADR-0009).
func (s space) rect(poly *boundingPoly) (ovrin.Rect, bool) {
	if poly == nil {
		return ovrin.Rect{}, false
	}

	verts, normalised := poly.Vertices, false
	if len(verts) == 0 {
		verts, normalised = poly.NormalizedVertices, true
	}
	if len(verts) == 0 {
		return ovrin.Rect{}, false
	}

	first := true
	var r ovrin.Rect
	for _, v := range verts {
		x, y := v.X*s.scaleX, v.Y*s.scaleY
		if normalised {
			x, y = v.X*s.width, v.Y*s.height
		}
		if first {
			r = ovrin.Rect{MinX: x, MinY: y, MaxX: x, MaxY: y}
			first = false
			continue
		}
		if x < r.MinX {
			r.MinX = x
		}
		if y < r.MinY {
			r.MinY = y
		}
		if x > r.MaxX {
			r.MaxX = x
		}
		if y > r.MaxY {
			r.MaxY = y
		}
	}
	return r, true
}

// lineWord is one word on a line, together with what Vision said about the gap
// after it.
//
// The gap is carried per word rather than being rendered into the line's text
// as words arrive, because the words are reordered before the text is built: a
// line assembled in Vision's order and then sorted would read back in the wrong
// order.
type lineWord struct {
	word ovrin.Word

	// joined records that this word runs into the next with no space, which is
	// what Vision's HYPHEN break means.
	joined bool
}

// line is one run of words sharing a baseline, before it is flattened.
type line struct {
	words []lineWord
	box   ovrin.Rect
}

// normalise turns one Vision page into ovrin's shape: page points with a
// top-left origin, confidence on 0..1, and words in reading order (ADR-0009).
func normalise(p *textPage, number int, dstW, dstH float64, raw json.RawMessage) *ovrin.Recognition {
	sp := newSpace(float64(p.Width), float64(p.Height), dstW, dstH)

	rec := &ovrin.Recognition{
		Confidence: p.Confidence,
		Language:   pageLanguage(p),
	}
	fromPage := false

	var lines []line
	// Vision's blocks are not reliably in reading order — a caption, a stamp or
	// a second column can arrive anywhere in the list — so they are sorted
	// before their words are read. Sorting the blocks rather than the finished
	// lines is what keeps a two-column page in column order: sorting lines by
	// their position alone would interleave the columns row by row.
	for _, b := range sortedBlocks(p.Blocks, sp) {
		for _, para := range sortedParagraphs(b.Paragraphs, sp) {
			cur := line{}
			for i := range para.Words {
				w := &para.Words[i]
				text := wordText(w)
				if text == "" {
					continue
				}
				box, ok := sp.rect(w.BoundingBox)
				if !ok {
					box = symbolBox(sp, w)
				}

				conf := p.Confidence
				if w.Confidence != nil {
					conf = *w.Confidence
				} else {
					// The page's own confidence, recorded as such on the
					// Annotation. Reporting 1.0 would tell the confidence
					// engine every word was read perfectly (rule §6.1).
					fromPage = true
				}

				cur.append(ovrin.Word{Text: text, Box: box, Confidence: conf},
					joinsWithoutSpace(w))

				if endsLine(w) {
					lines = append(lines, cur)
					cur = line{}
				}
			}
			// A paragraph always ends a line, whatever Vision said about the
			// last symbol's break.
			if len(cur.words) > 0 {
				lines = append(lines, cur)
			}
		}
	}

	for i := range lines {
		// Within a line, left to right. Vision's own order follows its
		// segmentation rather than the page, and a line assembled out of order
		// reads as nonsense to anything that grounds a value against it.
		words := lines[i].words
		sort.SliceStable(words, func(a, b int) bool {
			return words[a].word.Box.MinX < words[b].word.Box.MinX
		})
		for j := range words {
			w := words[j].word
			w.Line = i
			rec.Words = append(rec.Words, w)
		}
		rec.Lines = append(rec.Lines, ovrin.Line{
			Text: lines[i].text(),
			Box:  lines[i].box,
			Page: number,
		})
	}

	rec.Raw = &Annotation{JSON: raw, WordConfidenceFromPage: fromPage}
	return rec
}

// append adds one word to a line, growing its box.
func (l *line) append(w ovrin.Word, joined bool) {
	if len(l.words) == 0 {
		l.box = w.Box
	} else {
		l.box = union(l.box, w.Box)
	}
	l.words = append(l.words, lineWord{word: w, joined: joined})
}

// text renders the line's words in the order they are now in.
func (l *line) text() string {
	var b strings.Builder
	for i, lw := range l.words {
		if i > 0 && !l.words[i-1].joined {
			b.WriteString(" ")
		}
		b.WriteString(lw.word.Text)
	}
	return b.String()
}

// wordText concatenates a word's symbols, which is how Vision spells a word.
func wordText(w *textWord) string {
	var b strings.Builder
	for _, sym := range w.Symbols {
		b.WriteString(sym.Text)
	}
	return b.String()
}

// symbolBox is the union of a word's symbol boxes, for a word Vision gave no
// box of its own. Dropping the geometry instead would cost the word its
// provenance, which is half of what the OCR seam exists to carry (ADR-0009).
func symbolBox(sp space, w *textWord) ovrin.Rect {
	var out ovrin.Rect
	first := true
	for i := range w.Symbols {
		r, ok := sp.rect(w.Symbols[i].BoundingBox)
		if !ok {
			continue
		}
		if first {
			out, first = r, false
			continue
		}
		out = union(out, r)
	}
	return out
}

// lastBreak returns the break Vision detected after a word, which it reports on
// the word's last symbol rather than on the word.
func lastBreak(w *textWord) string {
	if n := len(w.Symbols); n > 0 {
		if prop := w.Symbols[n-1].Property; prop != nil && prop.DetectedBreak != nil {
			return prop.DetectedBreak.Type
		}
	}
	if w.Property != nil && w.Property.DetectedBreak != nil {
		return w.Property.DetectedBreak.Type
	}
	return ""
}

// endsLine reports whether a word is the last on its line.
func endsLine(w *textWord) bool {
	switch lastBreak(w) {
	case breakLine, breakEOLSpace:
		return true
	default:
		return false
	}
}

// joinsWithoutSpace reports whether a word runs into the next with no space,
// which is what Vision's HYPHEN break means.
func joinsWithoutSpace(w *textWord) bool {
	return lastBreak(w) == breakHyphen
}

// pageLanguage returns the language Vision detected for the page, or empty.
//
// Vision reports its languages most-confident first, and ovrin has one field,
// so the rest are left for [Annotation.JSON].
func pageLanguage(p *textPage) string {
	if p.Property == nil {
		return ""
	}
	for _, l := range p.Property.DetectedLanguages {
		if l.LanguageCode != "" {
			return l.LanguageCode
		}
	}
	return ""
}

// readingBands is how many horizontal bands a page is divided into when
// deciding what "above" means.
//
// Two blocks in the same band are read left to right rather than top to bottom,
// which is what puts a two-column page's left column first even when its first
// block starts a few points lower than the right column's. Sixty-four bands is
// about one text line on a Letter page: fine enough that consecutive lines land
// in different bands, coarse enough that a baseline wobble does not reorder
// them.
const readingBands = 64

// sortedBlocks returns a page's blocks in reading order.
//
// A new slice, because the wire structs are the decoded response that is handed
// back through [Annotation]: reordering them in place would mean Raw no longer
// described what arrived.
func sortedBlocks(blocks []textBlock, sp space) []textBlock {
	boxes := make([]ovrin.Rect, len(blocks))
	for i := range blocks {
		boxes[i], _ = sp.rect(blocks[i].BoundingBox)
	}
	out := make([]textBlock, 0, len(blocks))
	for _, i := range readingOrder(boxes, sp) {
		out = append(out, blocks[i])
	}
	return out
}

// sortedParagraphs returns a block's paragraphs in reading order.
func sortedParagraphs(paras []textParagraph, sp space) []textParagraph {
	boxes := make([]ovrin.Rect, len(paras))
	for i := range paras {
		boxes[i], _ = sp.rect(paras[i].BoundingBox)
	}
	out := make([]textParagraph, 0, len(paras))
	for _, i := range readingOrder(boxes, sp) {
		out = append(out, paras[i])
	}
	return out
}

// readingOrder returns the indices of boxes ordered top to bottom by band and
// left to right within one.
//
// The banding keeps the comparison transitive, which a "within N points of each
// other" test would not be — and a non-transitive less function makes sort
// produce an order that depends on the input's original order, which is exactly
// what is being removed here.
func readingOrder(boxes []ovrin.Rect, sp space) []int {
	idx := make([]int, len(boxes))
	for i := range idx {
		idx[i] = i
	}
	band := sp.height / readingBands
	if band <= 0 {
		band = 1
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := boxes[idx[a]], boxes[idx[b]]
		bandA, bandB := int(ra.MinY/band), int(rb.MinY/band)
		if bandA != bandB {
			return bandA < bandB
		}
		return ra.MinX < rb.MinX
	})
	return idx
}

// union returns the smallest rectangle containing both.
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
