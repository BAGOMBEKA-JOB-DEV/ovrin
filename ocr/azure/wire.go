package azure

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The statuses a long-running analysis reports.
//
// Document Intelligence has no synchronous endpoint: every analysis is
// submitted and then polled, so these four values are the whole control flow of
// this package.
const (
	statusNotStarted = "notStarted"
	statusRunning    = "running"
	statusSucceeded  = "succeeded"
	statusFailed     = "failed"
)

// The units Document Intelligence reports geometry in.
//
// Which one arrives depends on what was analysed rather than on what was asked
// for: a PDF is measured in inches and a rasterised image in its own pixels. An
// adapter that assumed either would be wrong for half of its callers by a
// factor of about seventy.
const (
	unitInch  = "inch"
	unitPixel = "pixel"
)

// pointsPerInch converts Document Intelligence's inches into ovrin's points.
const pointsPerInch = 72.0

// Analysis is what a [Provider] puts in [ovrin.Recognition.Raw].
//
// Tables and key-value pairs no longer need it: they cross the seam in
// [ovrin.Recognition.Layout] whenever the configured model reports them
// (ADR-0009). Everything else Document Intelligence reports and ovrin has no
// shape for — paragraphs, selection marks, barcodes, formulas, handwriting
// styles, page rotation, a table's caption — is still discarded by
// normalisation, and this is the route back to it. The result is kept as bytes
// rather than as decoded structs so that this package does not have to export
// thirty wire types, and so that a caller can unmarshal it into whatever shape
// they actually want.
type Analysis struct {
	// JSON is the operation's own reply, exactly as it arrived. For a document
	// it is the whole reply rather than a slice of it, because the service
	// returns one result covering every page and cutting it up would produce
	// bytes it never sent.
	JSON json.RawMessage

	// Page is the page within JSON this recognition was taken from, which is
	// what makes the shared JSON usable.
	Page int

	// WordConfidenceFromPage records that the service reported no confidence
	// for at least one word and the page's own was used for it instead.
	//
	// Without it a caller cannot tell a page-wide confidence from a per-word
	// one that happens to be uniform — and the alternative, reporting 1.0,
	// would tell the confidence engine every word was read perfectly
	// (rule §6.1, ADR-0009).
	WordConfidenceFromPage bool

	// PageConfidenceDerived records that [ovrin.Recognition.Confidence] is an
	// aggregate computed here rather than a number the service reported.
	//
	// Document Intelligence publishes a confidence per word and none for the
	// page, so a page-level figure can only be a mean of them. Saying so is the
	// difference between an aggregate and an invention: a caller weighting page
	// confidence against another provider's needs to know that this one is
	// second-hand.
	PageConfidenceDerived bool

	// Unit is the unit the service measured this page in, before conversion. A
	// caller reading the polygons out of JSON needs it to make sense of them.
	Unit string
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

// analyzeRequest is the body of an analyse call.
//
// urlSource is deliberately absent. Document Intelligence will fetch a document
// from a URL, and this package never asks it to: nothing a document references
// is ever fetched (rule §7.4), and a URL an adapter passes through is a URL the
// service will retrieve on the caller's behalf without the caller seeing it.
type analyzeRequest struct {
	Base64Source string `json:"base64Source"`
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

// operation is the long-running operation's own envelope, which is what a poll
// returns.
type operation struct {
	Status        string         `json:"status"`
	Error         *apiError      `json:"error"`
	AnalyzeResult *analyzeResult `json:"analyzeResult"`
}

// apiError is the Azure error envelope.
type apiError struct {
	// Code is the machine-readable classification, which is what a program may
	// branch on.
	Code string `json:"code"`

	// Message and InnerError are decoded and never read. Azure quotes the
	// offending request back in a validation error, so copying one into an
	// ovrin error would ship the document into the caller's logs (rule §2.5).
	// They are here so the fields are accounted for rather than looking
	// forgotten.
	Message    string    `json:"message"`
	InnerError *apiError `json:"innererror"`
}

// errorEnvelope is how a failing HTTP status carries an error, which is one
// level deeper than a failed operation carries one.
type errorEnvelope struct {
	Error *apiError `json:"error"`
}

type analyzeResult struct {
	APIVersion string `json:"apiVersion"`
	ModelID    string `json:"modelId"`

	// Content is the document's own text, decoded and never read: lines are
	// rebuilt from the word geometry instead, and a whole document in a field
	// this package touches is a document one careless log line away from a
	// caller's log system (rule §7.5).
	Content string `json:"content"`

	Pages     []resultPage `json:"pages"`
	Languages []language   `json:"languages"`

	// Tables and KeyValuePairs are the structure a layout or document model
	// reports. They are stated once for the whole result, with each element
	// naming the pages it sits on, rather than nested under a page — which is
	// why [pageLayout] has to select rather than simply convert.
	//
	// The read model returns neither, and a response from it leaves both nil.
	// That is not the same as a layout model finding no tables, and the
	// difference is what [ovrin.Recognition.Layout] being a pointer exists to
	// carry (ADR-0009).
	Tables        []table        `json:"tables"`
	KeyValuePairs []keyValuePair `json:"keyValuePairs"`
}

// boundingRegion is a polygon on a named page.
//
// Structure is reported for the document rather than for a page, so every part
// of it says which page it is on. A table that crosses a page break has one
// region per page; the first is where the table starts and is the page it is
// reported on, because a table has to be reported somewhere and the page it
// begins on is the only non-arbitrary choice.
type boundingRegion struct {
	PageNumber int       `json:"pageNumber"`
	Polygon    []float64 `json:"polygon"`
}

// table is one table the service found, stated as a grid rather than as a
// picture: it is the row and column indexes that make "the 40 in the Quantity
// column" expressible, and they are what survives the seam.
type table struct {
	RowCount        int              `json:"rowCount"`
	ColumnCount     int              `json:"columnCount"`
	Cells           []tableCell      `json:"cells"`
	BoundingRegions []boundingRegion `json:"boundingRegions"`

	// Spans and Caption are decoded and never read. The caption is document
	// content with no field on [ovrin.Table] to hold it, and reporting it as a
	// cell would put it in a grid position it does not occupy; it stays in
	// [Analysis.JSON]. They are declared so the fields are accounted for
	// rather than looking forgotten.
	Spans   []span          `json:"spans"`
	Caption *keyValueRegion `json:"caption"`
}

// tableCell is one cell of a table.
//
// RowSpan and ColumnSpan are absent from the response when they are one, which
// decodes to zero — and zero and one both mean one cell on [ovrin.Cell], so
// nothing has to be repaired here.
type tableCell struct {
	Kind            string           `json:"kind"`
	RowIndex        int              `json:"rowIndex"`
	ColumnIndex     int              `json:"columnIndex"`
	RowSpan         int              `json:"rowSpan"`
	ColumnSpan      int              `json:"columnSpan"`
	Content         string           `json:"content"`
	BoundingRegions []boundingRegion `json:"boundingRegions"`
	Spans           []span           `json:"spans"`
}

// The cell kinds Document Intelligence labels a cell with.
//
// The service always sets one, so an empty kind means a response that did not
// come from a model that classifies cells rather than a cell it could not
// classify.
const (
	kindContent      = "content"
	kindRowHeader    = "rowHeader"
	kindColumnHeader = "columnHeader"
	kindStubHead     = "stubHead"
	kindDescription  = "description"
)

// keyValuePair is a label and the thing it labels, as the service reports it.
//
// Value is a pointer because a form field the service found and read nothing in
// arrives with no value at all. That is a fact about the document — the box was
// blank — and dropping the pair would turn it into "there is no such box".
type keyValuePair struct {
	Key        *keyValueRegion `json:"key"`
	Value      *keyValueRegion `json:"value"`
	Confidence float64         `json:"confidence"`
}

// keyValueRegion is one half of a pair: the text and where it was.
type keyValueRegion struct {
	Content         string           `json:"content"`
	BoundingRegions []boundingRegion `json:"boundingRegions"`
	Spans           []span           `json:"spans"`
}

type resultPage struct {
	PageNumber int     `json:"pageNumber"`
	Angle      float64 `json:"angle"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	Unit       string  `json:"unit"`
	Words      []word  `json:"words"`
	Lines      []line  `json:"lines"`
	Spans      []span  `json:"spans"`
}

type word struct {
	Content string    `json:"content"`
	Polygon []float64 `json:"polygon"`

	// Confidence is a pointer because a response that reports none omits the
	// field rather than sending a zero, and reading an absent confidence as 0
	// would be as much a fabrication as reading it as 1.
	Confidence *float64 `json:"confidence"`

	Span span `json:"span"`
}

// line is a run of words the service grouped by baseline. It carries no
// confidence of its own — only words do.
type line struct {
	Content string    `json:"content"`
	Polygon []float64 `json:"polygon"`
	Spans   []span    `json:"spans"`
}

// span is a range of the result's flat content, and is how the service says
// which words are on which line.
type span struct {
	Offset int `json:"offset"`
	Length int `json:"length"`
}

type language struct {
	Locale     string  `json:"locale"`
	Confidence float64 `json:"confidence"`
	Spans      []span  `json:"spans"`
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

// classifyCode maps an Azure error code onto an ovrin sentinel.
//
// The codes matter because a failed analysis arrives inside an HTTP 200: the
// request to submit it succeeded and the work did not, so a caller classifying
// on the status alone would treat an unreadable document as a page with no text
// on it.
func classifyCode(code string) error {
	switch code {
	case "Unauthorized", "PermissionDenied", "AccessDenied", "InvalidApiKey":
		return ovrin.ErrAuth
	case "RequestRateLimitExceeded", "Throttled", "TooManyRequests":
		return ovrin.ErrRateLimit
	case "InvalidRequest", "InvalidArgument", "InvalidContent", "InvalidImage",
		"InvalidContentDimensions", "ContentSourceNotAccessible", "NotSupportedLanguage":
		return ovrin.ErrBadRequest
	case "UnsupportedMediaType", "ModelNotFound", "InvalidContentLength":
		return ovrin.ErrUnsupported
	case "InternalServerError", "ServiceUnavailable", "Timeout", "OperationCancelled":
		return ovrin.ErrUnavailable
	default:
		// An unrecognised code is a provider failure rather than a caller
		// mistake: guessing that the caller can fix it would send a fallback
		// chain to the wrong place.
		return ovrin.ErrUnavailable
	}
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// space converts Document Intelligence's coordinates into the page's.
//
// The service measures a PDF in inches and a rasterised image in its own
// pixels, and says which on every page. Where the caller's page states its own
// size in points, that ratio is used and the unit does not matter; where it does
// not — a document, which carries a page count and no geometry — the unit is
// the only route to points there is.
type space struct {
	scaleX, scaleY float64
}

// newSpace builds the conversion from one page's reported geometry to the
// caller's.
//
// dstW and dstH are the page size in points, or zero when the caller has none
// to offer. It reports false when there is no conversion at all: a page
// measured in pixels, with no size in points to scale against, has no route to
// points, and returning the pixels unchanged is exactly the silent degradation
// rule §6.1 forbids.
func newSpace(p *resultPage, dstW, dstH float64) (space, bool) {
	if dstW > 0 && dstH > 0 && p.Width > 0 && p.Height > 0 {
		return space{scaleX: dstW / p.Width, scaleY: dstH / p.Height}, true
	}
	switch p.Unit {
	case unitInch:
		return space{scaleX: pointsPerInch, scaleY: pointsPerInch}, true
	default:
		return space{}, false
	}
}

// rect converts one polygon into a page rectangle.
//
// The polygon is four points, clockwise from the top left, flattened into eight
// numbers. A rotated page produces one that is not axis-aligned, so the extremes
// are taken rather than the corners assumed. The origin is already top left on
// the service's side, which is the one convention ovrin and Azure agree on
// (ADR-0009).
func (s space) rect(polygon []float64) (ovrin.Rect, bool) {
	if len(polygon) < 2 {
		return ovrin.Rect{}, false
	}
	var r ovrin.Rect
	for i := 0; i+1 < len(polygon); i += 2 {
		x, y := polygon[i]*s.scaleX, polygon[i+1]*s.scaleY
		if i == 0 {
			r = ovrin.Rect{MinX: x, MinY: y, MaxX: x, MaxY: y}
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

// lineSpan locates one line by the range of the result's content it covers.
type lineSpan struct {
	start, end int
	index      int
}

// lineIndex answers which line a word at a given offset belongs to.
//
// Spans rather than geometry, because spans are what the service actually
// states: a word's offset into the flat content is unambiguous, while "inside
// this rectangle" is a guess that fails on overlapping lines and on any page
// with a rotation. Geometry is the fallback for a response that omits them.
type lineIndex []lineSpan

func newLineIndex(lines []line) lineIndex {
	var idx lineIndex
	for i := range lines {
		for _, sp := range lines[i].Spans {
			if sp.Length <= 0 {
				continue
			}
			idx = append(idx, lineSpan{start: sp.Offset, end: sp.Offset + sp.Length, index: i})
		}
	}
	sort.Slice(idx, func(a, b int) bool { return idx[a].start < idx[b].start })
	return idx
}

// find returns the line covering offset, or -1.
func (l lineIndex) find(offset int) int {
	i := sort.Search(len(l), func(i int) bool { return l[i].start > offset })
	if i == 0 {
		return -1
	}
	if s := l[i-1]; offset < s.end {
		return s.index
	}
	return -1
}

// contains reports whether outer holds inner's centre, which is the fallback
// for a response that reported no spans.
func contains(outer, inner ovrin.Rect) bool {
	cx := (inner.MinX + inner.MaxX) / 2
	cy := (inner.MinY + inner.MaxY) / 2
	return cx >= outer.MinX && cx <= outer.MaxX && cy >= outer.MinY && cy <= outer.MaxY
}

// lineDraft is one line before it is flattened into ovrin's shape.
type lineDraft struct {
	text  string
	box   ovrin.Rect
	words []ovrin.Word

	// haveBox records that the box came from the service's own polygon rather
	// than from the union of the line's words.
	haveBox bool
}

// normalise turns one page of a result into ovrin's shape: page points with a
// top-left origin, confidence on 0..1, and words in reading order (ADR-0009).
//
// number is the page number to stamp on each [ovrin.Line]; it comes from the
// page that was recognised rather than from the response, because a single page
// sent on its own comes back numbered 1 whatever page of the caller's document
// it was.
//
// structure says whether the model that ran reports tables and key-value pairs.
// It decides whether [ovrin.Recognition.Layout] is populated at all, and the
// distinction is load-bearing: nil is a model that does not look, and an empty
// Layout is one that looked and found nothing.
//
// An error means the structure the service reported is not coherent — a cell
// outside its table, two cells in one position — which is a response nothing
// can be done with. It is refused rather than half-mapped, because a layout
// that quietly contradicts itself is worse than one that is not there
// (rule §6.1).
func normalise(res *analyzeResult, p *resultPage, number int, sp space, raw json.RawMessage, structure bool) (*ovrin.Recognition, error) {
	pageConf, derived := meanConfidence(p.Words)

	rec := &ovrin.Recognition{
		Confidence: pageConf,
		Language:   pageLanguage(res, p),
		// One page read is one page billed. Document Intelligence prices its
		// read model per page, and this is the only place the cost of a reading
		// can be reported from.
		Usage: ovrin.Usage{PageUnits: 1},
	}
	fromPage := false

	drafts := make([]lineDraft, 0, len(p.Lines))
	for i := range p.Lines {
		d := lineDraft{text: p.Lines[i].Content}
		if r, ok := sp.rect(p.Lines[i].Polygon); ok {
			d.box, d.haveBox = r, true
		}
		drafts = append(drafts, d)
	}

	byOffset := newLineIndex(p.Lines)
	for i := range p.Words {
		w := &p.Words[i]
		if w.Content == "" {
			continue
		}
		box, _ := sp.rect(w.Polygon)
		conf := pageConf
		if w.Confidence != nil {
			conf = *w.Confidence
		} else {
			// The page's own confidence, recorded as such on the Analysis.
			// Reporting 1.0 would tell the confidence engine the word was read
			// perfectly (rule §6.1).
			fromPage = true
		}
		out := ovrin.Word{Text: w.Content, Box: box, Confidence: conf}

		n := byOffset.find(w.Span.Offset)
		if n < 0 {
			n = geometricLine(drafts, box)
		}
		if n < 0 {
			// A word no line claims becomes a line of its own. Dropping it
			// would lose text the caller paid to have read (rule §6.1).
			drafts = append(drafts, lineDraft{
				text: w.Content, box: box, words: []ovrin.Word{out}, haveBox: true,
			})
			continue
		}
		drafts[n].words = append(drafts[n].words, out)
	}

	// A line the service gave no polygon for takes the union of its words', so
	// that it still has a position to be sorted and grounded by.
	kept := make([]lineDraft, 0, len(drafts))
	for i := range drafts {
		if len(drafts[i].words) == 0 && drafts[i].text == "" {
			continue
		}
		if !drafts[i].haveBox {
			drafts[i].box = unionOf(drafts[i].words)
		}
		kept = append(kept, drafts[i])
	}

	boxes := make([]ovrin.Rect, len(kept))
	for i, d := range kept {
		boxes[i] = d.box
	}
	for lineNumber, i := range readingOrder(boxes, p.Height*sp.scaleY) {
		d := kept[i]
		// Within a line, left to right. The service's own order follows its
		// segmentation, and a line assembled out of order reads as nonsense to
		// anything that grounds a value against it.
		words := append([]ovrin.Word(nil), d.words...)
		sort.SliceStable(words, func(a, b int) bool {
			return words[a].Box.MinX < words[b].Box.MinX
		})
		for _, w := range words {
			w.Line = lineNumber
			rec.Words = append(rec.Words, w)
		}
		rec.Lines = append(rec.Lines, ovrin.Line{Text: d.text, Box: d.box, Page: number})
	}

	rec.Raw = &Analysis{
		JSON:                   raw,
		Page:                   number,
		WordConfidenceFromPage: fromPage,
		PageConfidenceDerived:  derived,
		Unit:                   p.Unit,
	}

	if structure {
		layout := pageLayout(res, p, number, sp)
		// Checked here rather than left to a caller: this is the one place
		// that knows the layout was built by mapping and not by the caller, so
		// a failure is the response's fault and can be reported as such. The
		// error names indexes and never a cell's text (rule §2.5).
		if err := layout.Check(); err != nil {
			return nil, err
		}
		rec.Layout = layout
	}
	return rec, nil
}

// geometricLine returns the line whose box holds a word's centre, or -1.
func geometricLine(drafts []lineDraft, box ovrin.Rect) int {
	for i := range drafts {
		if drafts[i].haveBox && contains(drafts[i].box, box) {
			return i
		}
	}
	return -1
}

// meanConfidence returns the mean of the confidences the words reported, and
// whether any of them reported one.
//
// It is the page's confidence because Document Intelligence publishes none of
// its own. A page whose words all reported nothing is left at zero rather than
// given a flattering default (rule §8.5).
func meanConfidence(words []word) (float64, bool) {
	var sum float64
	var n int
	for i := range words {
		if words[i].Confidence != nil {
			sum += *words[i].Confidence
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// pageLanguage returns the language detected for one page, or empty.
//
// The service reports languages for the result as a whole, each with the spans
// of content it covers, so the page's language is the most confident of those
// that overlap the page. Ovrin has one field, so the rest are left for
// [Analysis.JSON].
func pageLanguage(res *analyzeResult, p *resultPage) string {
	best, bestConf := "", -1.0
	for _, l := range res.Languages {
		if l.Locale == "" {
			continue
		}
		if len(p.Spans) > 0 && !overlaps(l.Spans, p.Spans) {
			continue
		}
		if l.Confidence > bestConf {
			best, bestConf = l.Locale, l.Confidence
		}
	}
	return best
}

// overlaps reports whether any span in a intersects any span in b.
func overlaps(a, b []span) bool {
	for _, x := range a {
		for _, y := range b {
			if x.Offset < y.Offset+y.Length && y.Offset < x.Offset+x.Length {
				return true
			}
		}
	}
	return false
}

// unionOf returns the smallest rectangle containing every word's box.
func unionOf(words []ovrin.Word) ovrin.Rect {
	var out ovrin.Rect
	for i, w := range words {
		if i == 0 {
			out = w.Box
			continue
		}
		if w.Box.MinX < out.MinX {
			out.MinX = w.Box.MinX
		}
		if w.Box.MinY < out.MinY {
			out.MinY = w.Box.MinY
		}
		if w.Box.MaxX > out.MaxX {
			out.MaxX = w.Box.MaxX
		}
		if w.Box.MaxY > out.MaxY {
			out.MaxY = w.Box.MaxY
		}
	}
	return out
}

// readingBandFraction is how many bands a page is divided into when deciding
// what "above" means.
//
// Two lines in the same band are read left to right rather than top to bottom,
// which is what puts a two-column page's left column first even when its first
// line starts a few points lower than the right column's. A sixty-fourth of a
// page is about one text line: fine enough that consecutive lines land in
// different bands, coarse enough that a baseline wobble does not reorder them.
const readingBandFraction = 64

// readingOrder returns the indices of boxes ordered top to bottom by band and
// left to right within one.
//
// The banding keeps the comparison transitive, which a "within N points of each
// other" test would not be — and a non-transitive less function makes sort
// produce an order that depends on the input's original order, which is exactly
// what is being removed here.
func readingOrder(boxes []ovrin.Rect, height float64) []int {
	idx := make([]int, len(boxes))
	for i := range idx {
		idx[i] = i
	}
	band := height / readingBandFraction
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

// ---------------------------------------------------------------------------
// Structure
// ---------------------------------------------------------------------------

// reportsStructure says whether a model returns tables and key-value pairs.
//
// It is the question [ovrin.Recognition.Layout] being a pointer exists to
// answer, and it has to be asked of the model rather than of the response: a
// layout analysis of a page with no table in it and a read analysis of the same
// page both arrive with no tables, and the caller's next decision — read this
// page as a table or as prose — depends on telling them apart (ADR-0009).
//
// prebuilt-read is the one model documented to return text and nothing else.
// Layout, the document-shaped prebuilts and custom models all report structure,
// so anything that is not the read model is assumed to look. Assuming the other
// way would mean a caller who asked for prebuilt-layout got nil, which reads as
// "nobody looked" when somebody did.
func reportsStructure(model string) bool {
	return model != "" && model != DefaultModel
}

// pageLayout is the structure the service reported on one page, in ovrin's
// shape.
//
// It is never nil. A model that reports structure and found none on this page
// returns an empty Layout, because that is the fact: it looked. The decision to
// call this at all is [reportsStructure]'s.
//
// Structure is stated once for the whole result with each element naming its
// pages, so this selects the elements belonging to this page rather than
// converting a page-shaped part of the response. number is stamped on what it
// keeps, for the same reason the words carry it: a page sent on its own comes
// back numbered 1 whatever page of the caller's document it was.
func pageLayout(res *analyzeResult, p *resultPage, number int, sp space) *ovrin.Layout {
	key := p.PageNumber
	if key <= 0 {
		key = number
	}
	home := firstPageNumber(res, key)

	out := &ovrin.Layout{}
	for i := range res.Tables {
		t := &res.Tables[i]
		if regionPage(t.BoundingRegions, home) != key {
			continue
		}
		out.Tables = append(out.Tables, ovrin.Table{
			Page: number,
			Box:  regionBox(t.BoundingRegions, key, sp),
			// The service's own counts, not len(Cells): a table whose last row
			// is empty still has that row, and deriving the size would lose it.
			Rows:    t.RowCount,
			Columns: t.ColumnCount,
			Cells:   tableCells(t.Cells, key, sp),
			// The service publishes no confidence for a table, and zero says
			// so. A fabricated 1.0 would tell the confidence engine the table
			// was read perfectly (rule §6.1).
		})
	}

	for i := range res.KeyValuePairs {
		kv := &res.KeyValuePairs[i]
		if kv.Key == nil {
			// A pair with no key is not a label and a value; there is nothing
			// for it to say.
			continue
		}
		page := regionPage(kv.Key.BoundingRegions, home)
		if page != key {
			continue
		}
		pair := ovrin.Pair{
			Page: number,
			Key:  keyValueRegionOf(kv.Key, key, sp),
			// Confidence is the service's own for the association. It is not
			// spread onto the two regions: it is about the pairing, and the
			// service reports nothing about how well either half was read.
			Confidence: kv.Confidence,
		}
		if kv.Value != nil {
			pair.Value = keyValueRegionOf(kv.Value, key, sp)
		}
		out.Pairs = append(out.Pairs, pair)
	}

	// Reading order is ovrin's convention rather than the service's, and Order
	// is where that convention lives, so that this adapter maps and does not
	// decide (rule §6.2). It also fills the box of a table the service gave no
	// geometry for, from the union of its cells.
	out.Order()
	return out
}

// tableCells converts the cells of one table that sit on the given page.
//
// Every cell is kept, including the ones on another page of a table that
// crosses a page break: the grid is the table's and dropping part of it would
// make a row silently short. Only the geometry is page-specific, and a cell on
// another page has none here.
func tableCells(cells []tableCell, page int, sp space) []ovrin.Cell {
	if len(cells) == 0 {
		return nil
	}
	out := make([]ovrin.Cell, 0, len(cells))
	for i := range cells {
		c := &cells[i]
		out = append(out, ovrin.Cell{
			Row:    c.RowIndex,
			Column: c.ColumnIndex,
			// Passed through unrepaired: the service omits a span of one, which
			// decodes to zero, and zero already means one cell on [ovrin.Cell].
			RowSpan:    c.RowSpan,
			ColumnSpan: c.ColumnSpan,
			Kind:       cellKind(c.Kind),
			Text:       c.Content,
			Box:        regionBox(c.BoundingRegions, page, sp),
		})
	}
	return out
}

// cellKind maps the service's cell label onto ovrin's closed set.
//
// Three of the five map exactly. The other two do not, and neither loss is
// silent:
//
//   - stubHead is the corner cell sitting above a column of row headers. It
//     labels the column beneath it, so it is reported as a column header. The
//     alternative — calling it a row header — would attach it to a row it does
//     not describe, and a wrong heading is worse than a coarse one.
//   - description is a cell describing the table rather than heading anything.
//     It carries content and labels nothing, which is what CellData says; it is
//     not CellUnknown, because the service did classify it.
//
// A kind this package has not seen becomes CellUnknown, which is "the provider
// did not say" rather than "this is data" — collapsing those two would make a
// header row silently wrong instead of visibly absent.
func cellKind(k string) ovrin.CellKind {
	switch k {
	case kindColumnHeader, kindStubHead:
		return ovrin.CellColumnHeader
	case kindRowHeader:
		return ovrin.CellRowHeader
	case kindContent, kindDescription:
		return ovrin.CellData
	default:
		return ovrin.CellUnknown
	}
}

// keyValueRegionOf converts one half of a pair.
func keyValueRegionOf(r *keyValueRegion, page int, sp space) ovrin.Region {
	return ovrin.Region{
		Text: r.Content,
		Box:  regionBox(r.BoundingRegions, page, sp),
		// The service reports no confidence for either half of a pair, only for
		// the association. Zero says so; anything else would be invented.
	}
}

// regionPage returns the page a structural element is reported on.
//
// The first region that names a page wins: an element crossing a page break is
// reported on the page it starts on, which is the only choice that does not
// depend on the order the service happened to list its regions in. An element
// with no region at all falls back, because dropping it would lose structure
// the caller paid to have detected (rule §6.1).
func regionPage(regions []boundingRegion, fallback int) int {
	for _, r := range regions {
		if r.PageNumber > 0 {
			return r.PageNumber
		}
	}
	return fallback
}

// regionBox returns an element's geometry on one page, converted to points.
//
// A zero rectangle means the element has no geometry on this page — either the
// service reported none, or this is the far side of something that crosses a
// page break. Zero means unknown throughout ovrin, never "at the origin".
func regionBox(regions []boundingRegion, page int, sp space) ovrin.Rect {
	for _, r := range regions {
		if r.PageNumber != page {
			continue
		}
		if box, ok := sp.rect(r.Polygon); ok {
			return box
		}
	}
	return ovrin.Rect{}
}

// firstPageNumber is the lowest page number the result reports, and is where an
// element that names no page is attributed.
//
// It is a minimum rather than res.Pages[0] because the service does not promise
// the pages arrive in order, and a fallback that depended on arrival order
// would move a table between pages for no reason the caller could see.
func firstPageNumber(res *analyzeResult, fallback int) int {
	best := 0
	for i := range res.Pages {
		if n := res.Pages[i].PageNumber; n > 0 && (best == 0 || n < best) {
			best = n
		}
	}
	if best == 0 {
		return fallback
	}
	return best
}
