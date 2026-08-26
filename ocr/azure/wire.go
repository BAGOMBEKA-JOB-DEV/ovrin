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
// Normalisation deliberately discards structure Document Intelligence reports —
// paragraphs, tables, key-value pairs, selection marks, barcodes, formulas and
// handwriting styles (ADR-0009) — and this is the route back to it. The result
// is kept as bytes rather than as decoded structs so that this package does not
// have to export thirty wire types, and so that a caller can unmarshal it into
// whatever shape they actually want.
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
func normalise(res *analyzeResult, p *resultPage, number int, sp space, raw json.RawMessage) *ovrin.Recognition {
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
	return rec
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
