package tesseract

import (
	"encoding/xml"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// Recognised is what a [Provider] puts in [ovrin.Recognition.Raw].
//
// Normalisation deliberately discards structure Tesseract reports — block and
// paragraph boundaries, baselines, x-heights, per-line confidences — and this
// is the route back to it. The hOCR is kept as the string Tesseract emitted
// rather than as decoded structs, so that this package does not export a
// parallel document model and a caller can parse it into whatever shape they
// actually want.
type Recognised struct {
	// HOCR is Tesseract's own hOCR output for the page, exactly as it arrived.
	HOCR string

	// Language is the traineddata Tesseract was asked to read with.
	//
	// It is not a detected language, which is why it is here rather than in
	// [ovrin.Recognition.Language]: Tesseract does not detect one, and
	// reporting the language it was *told* to use as the language it *found*
	// would be a fabrication of exactly the kind rule §8.5 forbids.
	Language string

	// WordConfidenceFromPage records that Tesseract reported no confidence for
	// at least one word and the page's own confidence was used for it instead.
	//
	// Without it a caller cannot tell a page-wide confidence from a per-word
	// one that happens to be uniform — and the alternative, reporting 1.0,
	// would tell the confidence engine those words were read perfectly
	// (rule §6.1, ADR-0009).
	WordConfidenceFromPage bool
}

// errNoWords reports that Tesseract produced hOCR containing no words at all.
//
// It is not an error the caller ever sees: a blank page is a legitimate
// recognition, and whether an empty result ends the extraction is the core's
// decision rather than an adapter's (rule §6.2). It exists so the parser can
// distinguish "no words" from "the document was not hOCR", which are different
// bugs.
var errNoWords = errors.New("no words")

// ---------------------------------------------------------------------------
// The document model hOCR is read into
// ---------------------------------------------------------------------------

// hocrWord is one ocrx_word element, still in Tesseract's pixel space.
type hocrWord struct {
	text string
	box  ovrin.Rect

	// conf is x_wconf on 0..100, and hasConf records whether the element
	// carried one. A word without x_wconf is not a word Tesseract was certain
	// about; it is a word Tesseract said nothing about, and the two must not
	// be collapsed (rule §6.1).
	conf    float64
	hasConf bool
}

// hocrLine is one ocr_line (or ocr_header, ocr_caption, ocr_textfloat).
type hocrLine struct {
	words []hocrWord
	box   ovrin.Rect
}

// hocrBlock is one ocr_carea, which is Tesseract's unit of layout analysis and
// therefore the unit that has to be sorted to put a two-column page in column
// order.
type hocrBlock struct {
	lines []hocrLine
	box   ovrin.Rect
}

// hocrPage is one ocr_page.
type hocrPage struct {
	blocks []hocrBlock

	// box is the ocr_page bbox, which is the pixel geometry Tesseract believes
	// the image had. It is used only as a fallback: the image this package
	// encoded is a better authority on its own size than a number parsed back
	// out of a title attribute.
	box ovrin.Rect
}

// The hOCR classes this package understands.
//
// hOCR is an open vocabulary and Tesseract emits a small, stable subset of it.
// Anything not listed here is walked through rather than rejected, so a future
// Tesseract adding a class does not turn a good page into an error.
const (
	classPage  = "ocr_page"
	classArea  = "ocr_carea"
	classWord  = "ocrx_word"
	roleIgnore = ""
)

// lineClasses are the classes that mean "a run of words sharing a baseline".
//
// Tesseract labels a heading, a caption and a floating text line differently
// from body text, and all four are lines as far as [ovrin.Line] is concerned —
// treating only ocr_line as a line would silently drop every heading on the
// page, which is rule §6.1's one intolerable behaviour.
var lineClasses = map[string]bool{
	"ocr_line":      true,
	"ocr_header":    true,
	"ocr_caption":   true,
	"ocr_textfloat": true,
}

// parseHOCR reads Tesseract's hOCR into the shape [normalise] consumes.
//
// The decoder is deliberately not strict. hOCR is XHTML, and an XHTML document
// carries a doctype, HTML entities and — depending on the build — unclosed
// void elements, none of which a strict XML decoder accepts. Failing a page
// over an &nbsp; would be a worse answer than reading it (rule §6.1).
func parseHOCR(r io.Reader) (*hocrPage, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var (
		page  hocrPage
		roles []string

		block *hocrBlock
		line  *hocrLine

		word    hocrWord
		wordBuf strings.Builder
		inWord  bool
	)

	// closeBlock and closeLine fold the open element back into its parent.
	// Empty ones are dropped rather than returned: a line with no words has no
	// text and no box, and an ovrin.Line that a word can never index is worse
	// than no line at all.
	closeLine := func() {
		if line != nil && len(line.words) > 0 && block != nil {
			block.lines = append(block.lines, *line)
		}
		line = nil
	}
	closeBlock := func() {
		closeLine()
		if block != nil && len(block.lines) > 0 {
			page.blocks = append(page.blocks, *block)
		}
		block = nil
	}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// The decoder's own message can quote the input, and the input is
			// the recognised text of somebody's document. It is dropped, and
			// only the fact of the failure survives (rule §2.5).
			return nil, errors.New("the provider's hocr could not be parsed")
		}

		switch tok := tok.(type) {
		case xml.StartElement:
			role, title := roleOf(tok)
			roles = append(roles, role)

			switch role {
			case classPage:
				closeBlock()
				page.box, _ = bboxOf(title)
			case classArea:
				closeBlock()
				block = &hocrBlock{}
				block.box, _ = bboxOf(title)
			case classWord:
				if inWord {
					// Nested ocrx_word is not something Tesseract emits; the
					// inner one wins rather than the two being concatenated.
					wordBuf.Reset()
				}
				inWord = true
				word = hocrWord{}
				word.box, _ = bboxOf(title)
				word.conf, word.hasConf = wconfOf(title)
				wordBuf.Reset()
			default:
				if !lineClasses[role] {
					break
				}
				closeLine()
				if block == nil {
					// A line outside any ocr_carea. Tesseract does not emit
					// one, but an implicit block keeps the word reachable
					// instead of dropping it.
					block = &hocrBlock{}
				}
				line = &hocrLine{}
				line.box, _ = bboxOf(title)
			}

		case xml.CharData:
			if inWord {
				wordBuf.Write(tok)
			}

		case xml.EndElement:
			var role string
			if n := len(roles); n > 0 {
				role, roles = roles[n-1], roles[:n-1]
			}
			switch {
			case role == classWord:
				inWord = false
				word.text = strings.TrimSpace(wordBuf.String())
				wordBuf.Reset()
				if word.text == "" {
					break
				}
				if line == nil {
					if block == nil {
						block = &hocrBlock{}
					}
					line = &hocrLine{}
				}
				line.words = append(line.words, word)
			case lineClasses[role]:
				closeLine()
			case role == classArea:
				closeBlock()
			case role == classPage:
				closeBlock()
			}
		}
	}
	closeBlock()

	var words int
	for _, b := range page.blocks {
		for _, l := range b.lines {
			words += len(l.words)
		}
	}
	if words == 0 {
		return &page, errNoWords
	}
	return &page, nil
}

// roleOf returns the hOCR class of an element and its title attribute.
//
// The class attribute may carry several names; the first one this package
// recognises wins, which is what the hOCR profile prescribes and what keeps a
// `class='ocr_line ocrx_block'` from being read as neither.
func roleOf(el xml.StartElement) (role, title string) {
	var class string
	for _, attr := range el.Attr {
		switch strings.ToLower(attr.Name.Local) {
		case "class":
			class = attr.Value
		case "title":
			title = attr.Value
		}
	}
	for _, name := range strings.Fields(class) {
		switch {
		case name == classPage, name == classArea, name == classWord,
			lineClasses[name]:
			return name, title
		}
	}
	return roleIgnore, title
}

// bboxOf reads the `bbox x0 y0 x1 y1` property out of an hOCR title attribute.
//
// An element without one is not an error: hOCR makes bbox optional, and a word
// whose geometry is missing is still a word. It loses its provenance, which is
// half of what the OCR seam carries (ADR-0009), and that is strictly better
// than losing the word.
func bboxOf(title string) (ovrin.Rect, bool) {
	fields, ok := property(title, "bbox")
	if !ok || len(fields) < 4 {
		return ovrin.Rect{}, false
	}
	nums := make([]float64, 4)
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return ovrin.Rect{}, false
		}
		nums[i] = v
	}
	r := ovrin.Rect{MinX: nums[0], MinY: nums[1], MaxX: nums[2], MaxY: nums[3]}
	// hOCR's origin is already top left, which is the one convention ovrin and
	// Tesseract agree on. An inverted box is a malformed title rather than a
	// different origin, so it is squared up instead of being flipped.
	if r.MinX > r.MaxX {
		r.MinX, r.MaxX = r.MaxX, r.MinX
	}
	if r.MinY > r.MaxY {
		r.MinY, r.MaxY = r.MaxY, r.MinY
	}
	return r, true
}

// wconfOf reads the `x_wconf` property, which Tesseract reports on 0..100.
//
// The second return is whether it was there at all. It is the whole reason
// this returns two values: a word Tesseract said nothing about must not be
// reported as a word it was certain about (rule §6.1).
func wconfOf(title string) (float64, bool) {
	fields, ok := property(title, "x_wconf")
	if !ok || len(fields) < 1 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// property returns the arguments of one hOCR title property.
//
// An hOCR title is a semicolon-separated list of `name arg arg …`, and this is
// the whole of the grammar that matters here.
func property(title, name string) ([]string, bool) {
	for _, part := range strings.Split(title, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == name {
			return fields[1:], true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// space converts Tesseract's pixel geometry into the page's own points.
//
// The scale comes from the image this package encoded rather than from
// [ovrin.Page.DPI], and the two agree by construction: a page rasterised at
// DPI has Width*DPI/72 pixels across, so Width/pixels is 72/DPI exactly.
// Dividing by the pixel count is the more robust of the two — it stays correct
// when a renderer rounds a pixel count up, and when DPI is left at zero it is
// the only one that works at all.
type space struct {
	scaleX, scaleY float64
	width, height  float64
}

func newSpace(pixelsW, pixelsH int, pointsW, pointsH float64) space {
	s := space{scaleX: 1, scaleY: 1, width: pointsW, height: pointsH}
	if pixelsW > 0 {
		s.scaleX = pointsW / float64(pixelsW)
	}
	if pixelsH > 0 {
		s.scaleY = pointsH / float64(pixelsH)
	}
	return s
}

// rect converts one pixel rectangle into page points, clamped to the page.
//
// The clamp is not cosmetic. Everything downstream — grounding, a review
// interface drawing a highlight — assumes a box is on the page it names, and a
// box a fraction of a point over the edge because a renderer rounded is not
// worth propagating as an anomaly.
func (s space) rect(r ovrin.Rect) ovrin.Rect {
	return ovrin.Rect{
		MinX: clamp(r.MinX*s.scaleX, 0, s.width),
		MinY: clamp(r.MinY*s.scaleY, 0, s.height),
		MaxX: clamp(r.MaxX*s.scaleX, 0, s.width),
		MaxY: clamp(r.MaxY*s.scaleY, 0, s.height),
	}
}

func clamp(v, lo, hi float64) float64 {
	switch {
	case v < lo:
		return lo
	case hi > lo && v > hi:
		return hi
	default:
		return v
	}
}

// normalise turns one parsed hOCR page into ovrin's shape: page points with a
// top-left origin, confidence on 0..1, and words in reading order (ADR-0009).
//
// number is the page number being recognised, which is stamped on every
// [ovrin.Line]. It comes from the page rather than from hOCR's own ppageno,
// which counts from zero within one recognition and is always 0 here.
func normalise(page *hocrPage, sp space, number int, raw Recognised) *ovrin.Recognition {
	// Two passes over the words, and it has to be two. The page confidence is
	// the mean of the per-word confidences — which is exactly what Tesseract's
	// own MeanTextConf computes — and a word Tesseract reported no confidence
	// for takes that page confidence, so the page's value must be known before
	// any word's is filled in.
	var sum float64
	var counted int
	for _, b := range page.blocks {
		for _, l := range b.lines {
			for _, w := range l.words {
				if w.hasConf {
					sum += w.conf / 100
					counted++
				}
			}
		}
	}
	var pageConf float64
	if counted > 0 {
		pageConf = sum / float64(counted)
	}

	rec := &ovrin.Recognition{Confidence: pageConf}
	fromPage := false

	// Tesseract emits its content areas in the order layout analysis found
	// them, which is close to reading order and is not it: a header, a stamp
	// or a second column can arrive anywhere in the list. Sorting the blocks
	// rather than the finished lines is what keeps a two-column page in column
	// order — sorting lines by position alone would interleave the columns row
	// by row.
	for _, bi := range readingOrder(blockBoxes(page.blocks, sp), sp) {
		b := page.blocks[bi]
		for _, l := range b.lines {
			box := sp.rect(l.box)
			words := make([]ovrin.Word, 0, len(l.words))
			var textBox ovrin.Rect
			for _, w := range l.words {
				conf := w.conf / 100
				if !w.hasConf {
					// The page's own confidence, recorded as such on the
					// Recognised value. Reporting 1.0 would tell the
					// confidence engine this word was read perfectly, which is
					// the most consequential lie an adapter can tell
					// (rule §6.1).
					conf = pageConf
					fromPage = true
				}
				words = append(words, ovrin.Word{
					Text:       w.text,
					Box:        sp.rect(w.box),
					Confidence: conf,
					Line:       len(rec.Lines),
				})
			}

			// Within a line, left to right. Tesseract's order follows its own
			// segmentation rather than the page, and a line assembled out of
			// order reads as nonsense to anything grounding a value against it.
			sort.SliceStable(words, func(a, b int) bool {
				return words[a].Box.MinX < words[b].Box.MinX
			})

			var text strings.Builder
			for i, w := range words {
				if i > 0 {
					text.WriteString(" ")
				}
				text.WriteString(w.Text)
				if i == 0 {
					textBox = w.Box
				} else {
					textBox = union(textBox, w.Box)
				}
			}
			if !l.hasBox() {
				box = textBox
			}

			rec.Words = append(rec.Words, words...)
			rec.Lines = append(rec.Lines, ovrin.Line{
				Text: text.String(),
				Box:  box,
				Page: number,
			})
		}
	}

	raw.WordConfidenceFromPage = fromPage
	rec.Raw = &raw
	// Language stays empty. Tesseract does not detect a language, it is told
	// one, and reporting the model it was given as a detection would be a
	// fabrication (rule §8.5). What it was told is on Recognised.Language.
	return rec
}

// hasBox reports whether hOCR gave the line a bbox of its own, in which case
// it is preferred to the union of the words: Tesseract's line box includes
// descenders and inter-word gaps the word boxes do not.
func (l hocrLine) hasBox() bool {
	return l.box.MaxX > l.box.MinX || l.box.MaxY > l.box.MinY
}

func blockBoxes(blocks []hocrBlock, sp space) []ovrin.Rect {
	out := make([]ovrin.Rect, len(blocks))
	for i := range blocks {
		out[i] = sp.rect(blocks[i].box)
	}
	return out
}

// readingBands is how many horizontal bands a page is divided into when
// deciding what "above" means.
//
// Two blocks in the same band are read left to right rather than top to
// bottom, which is what puts a two-column page's left column first even when
// its first block starts a few points lower than the right column's.
// Sixty-four bands is about one text line on a Letter page: fine enough that
// consecutive lines land in different bands, coarse enough that a baseline
// wobble does not reorder them.
//
// It matches ocr/google deliberately. Two adapters that disagree about reading
// order would produce two different provenance orders for the same scan.
const readingBands = 64

// readingOrder returns the indices of boxes ordered top to bottom by band and
// left to right within one.
//
// The banding keeps the comparison transitive, which a "within N points of
// each other" test would not be — and a non-transitive less function makes
// sort produce an order that depends on the input's original order, which is
// exactly what is being removed here.
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

// describeDirs renders a search path for an error message.
//
// It exists so that "no traineddata" says where this package looked, which is
// the difference between a one-line fix and an afternoon. Paths are
// configuration, never document content (rule §2.5).
func describeDirs(dirs []string) string {
	if len(dirs) == 0 {
		return "nowhere: no tessdata directory is configured"
	}
	return strings.Join(dirs, ", ")
}
