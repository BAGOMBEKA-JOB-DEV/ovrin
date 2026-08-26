package textract

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The X-Amz-Target values this package calls.
//
// Textract is a JSON-RPC style API: every request is a POST to "/" and the
// operation is named in a header rather than in the path. An adapter that put
// the operation in the URL would be talking to a service that does not exist.
const (
	targetDetect = "Textract.DetectDocumentText"
	targetStart  = "Textract.StartDocumentTextDetection"
	targetGet    = "Textract.GetDocumentTextDetection"
)

// The block types this package reads.
//
// Textract returns one flat list holding every level of the hierarchy at once,
// so a reader picks the levels it wants out of it rather than descending a
// tree. PAGE blocks are deliberately not read: they carry geometry that is by
// definition the whole page and no text.
const (
	blockLine = "LINE"
	blockWord = "WORD"
)

// The job statuses an asynchronous text detection reports.
const (
	jobInProgress = "IN_PROGRESS"
	jobSucceeded  = "SUCCEEDED"
	jobFailed     = "FAILED"
	jobPartial    = "PARTIAL_SUCCESS"
)

// relationshipChild is the relationship linking a LINE block to its words.
//
// Textract also reports VALUE, COMPLEX_FEATURES, MERGED_CELL and TABLE
// relationships from AnalyzeDocument; this package calls text detection only,
// so CHILD is the one it can encounter.
const relationshipChild = "CHILD"

// Analysis is what a [Provider] puts in [ovrin.Recognition.Raw].
//
// Normalisation deliberately discards structure Textract reports — block
// identifiers, the relationship graph, per-word TextType, and everything
// AnalyzeDocument would add on top (ADR-0009) — and this is the route back to
// it. The response is kept as bytes rather than as decoded structs so that this
// package does not have to export a dozen wire types, and so that a caller can
// unmarshal it into whatever shape they actually want.
type Analysis struct {
	// JSON is the Textract response this page was read from, exactly as it
	// arrived. For a document it is the whole response rather than a slice of
	// it, because Textract returns one block list covering every page and
	// cutting it up would produce bytes the service never sent — and where a
	// document was long enough to arrive in several responses, it is the first
	// of them, since there is no single document those bytes could be joined
	// into.
	JSON json.RawMessage

	// Page is the page within JSON this recognition was taken from, which is
	// what makes the shared JSON usable.
	Page int

	// WordConfidenceFromPage records that Textract reported no confidence for
	// at least one word and the page's own was used for it instead.
	//
	// Without it a caller cannot tell a page-wide confidence from a per-word
	// one that happens to be uniform — and the alternative, reporting 1.0,
	// would tell the confidence engine every word was read perfectly
	// (rule §6.1, ADR-0009).
	WordConfidenceFromPage bool

	// PageConfidenceDerived records that [ovrin.Recognition.Confidence] is an
	// aggregate computed here rather than a number Textract reported.
	//
	// Textract publishes a confidence for every LINE and WORD block and none
	// for the page, so a page-level figure can only be a mean of them. Saying
	// so is the difference between an aggregate and an invention: a caller
	// weighting page confidence against another provider's needs to know that
	// this one is second-hand.
	PageConfidenceDerived bool
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

// detectRequest is the synchronous DetectDocumentText request.
type detectRequest struct {
	Document requestDocument `json:"Document"`
}

// requestDocument is how Textract accepts a document: inline bytes, or an
// object it fetches from Amazon S3 itself.
type requestDocument struct {
	Bytes    string    `json:"Bytes,omitempty"`
	S3Object *s3Object `json:"S3Object,omitempty"`
}

// s3Object names an object in Amazon S3. It is the wire form of [S3Object].
type s3Object struct {
	Bucket  string `json:"Bucket"`
	Name    string `json:"Name"`
	Version string `json:"Version,omitempty"`
}

// startRequest is the asynchronous StartDocumentTextDetection request.
//
// It carries no inline bytes, and that is not an omission: the asynchronous API
// reads its input from Amazon S3 and has no field a document could be sent in.
// It is the reason [WithDocumentLocation] exists.
type startRequest struct {
	DocumentLocation documentLocation `json:"DocumentLocation"`
}

type documentLocation struct {
	S3Object s3Object `json:"S3Object"`
}

// getRequest is the GetDocumentTextDetection request. NextToken is how a
// document longer than one response page is collected.
type getRequest struct {
	JobID     string `json:"JobId"`
	NextToken string `json:"NextToken,omitempty"`
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

type startResponse struct {
	JobID string `json:"JobId"`
}

// analyzeResponse is the reply to both DetectDocumentText and
// GetDocumentTextDetection, which differ only in the job fields.
type analyzeResponse struct {
	DocumentMetadata *documentMetadata `json:"DocumentMetadata"`
	Blocks           []block           `json:"Blocks"`

	// JobStatus, NextToken and Warnings are present only on the asynchronous
	// reply.
	JobStatus string    `json:"JobStatus"`
	NextToken string    `json:"NextToken"`
	Warnings  []warning `json:"Warnings"`

	// StatusMessage is decoded and never read. Textract quotes the offending
	// request back in some of them, so copying one into an ovrin error would
	// ship the document into the caller's logs (rule §2.5). It is here so the
	// field is accounted for rather than looking forgotten.
	StatusMessage string `json:"StatusMessage"`
}

type documentMetadata struct {
	Pages int `json:"Pages"`
}

// warning is a page Textract could not read, which is what turns a job into a
// PARTIAL_SUCCESS.
type warning struct {
	ErrorCode string `json:"ErrorCode"`
	Pages     []int  `json:"Pages"`
}

// block is one element of Textract's flat result list.
type block struct {
	BlockType string `json:"BlockType"`
	ID        string `json:"Id"`
	Text      string `json:"Text"`

	// Confidence is a pointer because Textract omits the field rather than
	// sending a zero, and reading an absent confidence as 0 would be as much a
	// fabrication as reading it as 1.
	Confidence *float64 `json:"Confidence"`

	Geometry *geometry `json:"Geometry"`

	// Relationships link a LINE to the WORD blocks it is made of. Textract
	// returns the levels interleaved in one list, so this graph is the only
	// thing that says which words are on which line.
	Relationships []relationship `json:"Relationships"`

	// Page is 1-based and is present on an asynchronous reply only; a
	// synchronous one reads a single page and omits it.
	Page int `json:"Page"`

	// TextType is PRINTED or HANDWRITING. Decoded because it is the field a
	// reader will look for; ovrin's Word has nowhere to put it, so it reaches
	// a caller through [Analysis] alone.
	TextType string `json:"TextType"`
}

type relationship struct {
	Type string   `json:"Type"`
	IDs  []string `json:"Ids"`
}

// geometry is where a block sits, as a fraction of the page in both axes.
//
// Textract never states the page's size, which is why converting a document's
// geometry into points needs [WithPageSize] — see [Provider.RecogniseDocument].
type geometry struct {
	BoundingBox *boundingBox `json:"BoundingBox"`
	Polygon     []point      `json:"Polygon"`
}

type boundingBox struct {
	Width  float64 `json:"Width"`
	Height float64 `json:"Height"`
	Left   float64 `json:"Left"`
	Top    float64 `json:"Top"`
}

type point struct {
	X float64 `json:"X"`
	Y float64 `json:"Y"`
}

// apiError is the AWS JSON 1.1 error envelope.
type apiError struct {
	// Type is the machine-readable exception name, which is what a program may
	// branch on. It arrives either bare or namespaced as
	// "com.amazonaws.textract#ThrottlingException".
	Type string `json:"__type"`

	// Message and message are decoded and never read, for the reason
	// [analyzeResponse.StatusMessage] gives. AWS spells the field both ways
	// depending on the service.
	Message      string `json:"Message"`
	MessageLower string `json:"message"`
}

// exception returns the bare exception name, without the namespace AWS
// sometimes prefixes it with.
func (e *apiError) exception() string {
	if e == nil {
		return ""
	}
	if i := strings.LastIndex(e.Type, "#"); i >= 0 {
		return e.Type[i+1:]
	}
	return e.Type
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

// classifyException refines a status classification with the exception name
// AWS puts in the body.
//
// It is needed because AWS returns a throttle, an expired credential and a
// malformed request with the same HTTP 400: classifying on the status alone
// would tell a fallback chain that a rate limit is a permanent request defect
// and stop it advancing. The exception name is a machine-readable code, not
// message text, so branching on it is classification rather than the string
// matching rule §2.2 forbids.
//
// The status wins wherever it is already unambiguous, so a 429 stays a rate
// limit whatever the body says.
func classifyException(status int, e *apiError) error {
	kind := classifyStatus(status)
	if kind != ovrin.ErrBadRequest {
		return kind
	}
	switch e.exception() {
	case "ThrottlingException",
		"ProvisionedThroughputExceededException",
		"LimitExceededException",
		"RequestLimitExceeded":
		return ovrin.ErrRateLimit
	case "AccessDeniedException",
		"UnrecognizedClientException",
		"InvalidSignatureException",
		"IncompleteSignatureException",
		"MissingAuthenticationTokenException",
		"ExpiredTokenException":
		return ovrin.ErrAuth
	case "UnsupportedDocumentException",
		"BadDocumentException",
		"DocumentTooLargeException",
		"InvalidS3ObjectException",
		"InvalidKMSKeyException":
		return ovrin.ErrUnsupported
	case "InternalServerError", "ServiceUnavailableException":
		return ovrin.ErrUnavailable
	default:
		return kind
	}
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// space converts Textract's coordinates into the page's.
//
// Textract reports every box as a fraction of the page in both axes and never
// says how large the page is, so the conversion is a multiplication by a size
// this package has to be told (ADR-0009).
type space struct {
	width, height float64
}

// valid reports whether the space can express a coordinate in points at all. A
// zero size would turn every box into a point at the origin, which is the
// silent degradation rule §6.1 forbids.
func (s space) valid() bool { return s.width > 0 && s.height > 0 }

// rect converts one block's geometry into a page rectangle.
//
// The bounding box is preferred because it is what Textract computes; the
// polygon is read when there is no bounding box, and its extremes are taken
// rather than its corners assumed, because a rotated page produces a polygon
// that is not axis-aligned.
func (s space) rect(g *geometry) (ovrin.Rect, bool) {
	if g == nil {
		return ovrin.Rect{}, false
	}
	if b := g.BoundingBox; b != nil {
		return ovrin.Rect{
			MinX: b.Left * s.width,
			MinY: b.Top * s.height,
			MaxX: (b.Left + b.Width) * s.width,
			MaxY: (b.Top + b.Height) * s.height,
		}, true
	}
	if len(g.Polygon) == 0 {
		return ovrin.Rect{}, false
	}
	var r ovrin.Rect
	for i, v := range g.Polygon {
		x, y := v.X*s.width, v.Y*s.height
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

// lineDraft is one line before it is flattened into ovrin's shape.
type lineDraft struct {
	text  string
	box   ovrin.Rect
	words []ovrin.Word

	// haveBox records that the box came from Textract's own geometry rather
	// than from the union of the line's words.
	haveBox bool
}

// childIDs returns the identifiers of a block's child blocks.
func childIDs(b *block) []string {
	for _, rel := range b.Relationships {
		if rel.Type == relationshipChild {
			return rel.IDs
		}
	}
	return nil
}

// meanConfidence returns the mean of the confidences present in blocks, on
// 0..1, and reports whether any block carried one.
func meanConfidence(blocks []*block) (float64, bool) {
	var sum float64
	var n int
	for _, b := range blocks {
		if b.Confidence != nil {
			sum += *b.Confidence
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	// Textract reports 0..100. A scale error here does not fail, it silently
	// makes every field look certain (docs/confidence.md).
	return sum / float64(n) / 100, true
}

// normalise turns one page's blocks into ovrin's shape: page points with a
// top-left origin, confidence on 0..1, and words in reading order (ADR-0009).
//
// number is the page number to stamp on each [ovrin.Line]; it comes from the
// page that was recognised rather than from the response, because a synchronous
// reply numbers its only page 1 whatever page of the caller's document it was.
func normalise(blocks []block, number int, sp space, raw json.RawMessage) *ovrin.Recognition {
	byID := make(map[string]*block, len(blocks))
	var lineBlocks, wordBlocks []*block
	for i := range blocks {
		b := &blocks[i]
		if b.ID != "" {
			byID[b.ID] = b
		}
		switch b.BlockType {
		case blockLine:
			lineBlocks = append(lineBlocks, b)
		case blockWord:
			wordBlocks = append(wordBlocks, b)
		}
	}

	// The page's own confidence is a mean of the lines', falling back to the
	// words'. Textract publishes neither a page confidence nor a document one,
	// and a page that reported no confidence at all is left at zero rather than
	// given a flattering default (rule §8.5).
	pageConf, derived := meanConfidence(lineBlocks)
	if !derived {
		pageConf, derived = meanConfidence(wordBlocks)
	}

	rec := &ovrin.Recognition{
		Confidence: pageConf,
		// One page read is one page billed. Textract prices text detection per
		// page, and this is the only place the cost of a reading can be
		// reported from.
		Usage: ovrin.Usage{PageUnits: 1},
	}
	fromPage := false

	claimed := make(map[string]bool, len(wordBlocks))
	drafts := make([]lineDraft, 0, len(lineBlocks))
	for _, lb := range lineBlocks {
		d := lineDraft{text: lb.Text}
		if r, ok := sp.rect(lb.Geometry); ok {
			d.box, d.haveBox = r, true
		}
		for _, id := range childIDs(lb) {
			wb := byID[id]
			if wb == nil || wb.BlockType != blockWord || wb.Text == "" {
				continue
			}
			claimed[id] = true
			d.words = append(d.words, wordFrom(wb, sp, pageConf, &fromPage))
		}
		if len(d.words) == 0 && d.text == "" {
			continue
		}
		drafts = append(drafts, d)
	}

	// A word no line claimed becomes a line of its own. Textract parents every
	// word to a line in practice, and dropping one that it did not would lose
	// text the caller paid to have read (rule §6.1).
	for _, wb := range wordBlocks {
		if claimed[wb.ID] || wb.Text == "" {
			continue
		}
		w := wordFrom(wb, sp, pageConf, &fromPage)
		drafts = append(drafts, lineDraft{
			text: wb.Text, box: w.Box, words: []ovrin.Word{w}, haveBox: true,
		})
	}

	// A line Textract gave no geometry for takes the union of its words', so
	// that it still has a position to be sorted and grounded by.
	boxes := make([]ovrin.Rect, len(drafts))
	for i, d := range drafts {
		if !d.haveBox {
			drafts[i].box = unionOf(d.words)
		}
		boxes[i] = drafts[i].box
	}

	for lineIndex, i := range readingOrder(boxes, sp.height) {
		d := drafts[i]
		// Within a line, left to right. Textract's child order is usually
		// reading order already, but "usually" is not a contract and a line
		// assembled out of order reads as nonsense to anything that grounds a
		// value against it.
		words := append([]ovrin.Word(nil), d.words...)
		sort.SliceStable(words, func(a, b int) bool {
			return words[a].Box.MinX < words[b].Box.MinX
		})
		for _, w := range words {
			w.Line = lineIndex
			rec.Words = append(rec.Words, w)
		}
		rec.Lines = append(rec.Lines, ovrin.Line{Text: d.text, Box: d.box, Page: number})
	}

	rec.Raw = &Analysis{
		JSON:                   raw,
		Page:                   number,
		WordConfidenceFromPage: fromPage,
		PageConfidenceDerived:  derived,
	}
	return rec
}

// wordFrom converts one WORD block, falling back to the page's confidence when
// Textract reported none for it and recording that it did through fromPage.
func wordFrom(b *block, sp space, pageConf float64, fromPage *bool) ovrin.Word {
	box, _ := sp.rect(b.Geometry)
	conf := pageConf
	if b.Confidence != nil {
		conf = *b.Confidence / 100
	} else {
		// The page's own confidence, recorded as such on the Analysis.
		// Reporting 1.0 would tell the confidence engine the word was read
		// perfectly (rule §6.1).
		*fromPage = true
	}
	return ovrin.Word{Text: b.Text, Box: box, Confidence: conf}
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

// readingBands is how many horizontal bands a page is divided into when
// deciding what "above" means.
//
// Two lines in the same band are read left to right rather than top to bottom,
// which is what puts a two-column page's left column first even when its first
// line starts a few points lower than the right column's. Sixty-four bands is
// about one text line on a Letter page: fine enough that consecutive lines land
// in different bands, coarse enough that a baseline wobble does not reorder
// them.
const readingBands = 64

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
	band := height / readingBands
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
