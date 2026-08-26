package office

import (
	"encoding/xml"
	"io"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// docxBody is the part a DOCX's visible body lives in. It is a fixed name in
// the format, and it is the only part this package reads text out of.
const docxBody = "word/document.xml"

// docxAux describes a part whose text is deliberately not extracted, so that
// its presence can be reported instead of silently ignored.
//
// The match is on ovrin's own fixed strings, and what comes out is a [Part]
// constant. A container entry's real name never leaves this file.
type docxAux struct {
	prefix string
	suffix string
	exact  bool
	part   Part
}

// The auxiliary parts, in the order they are reported.
//
// Headers and footers are numbered per section, so they are matched by prefix;
// notes and comments are single parts with fixed names. commentsExtended.xml
// and the several other comment sidecar parts are not listed because they hold
// no text of their own.
var docxAuxParts = []docxAux{
	{prefix: "word/header", suffix: ".xml", part: PartHeader},
	{prefix: "word/footer", suffix: ".xml", part: PartFooter},
	{prefix: "word/footnotes.xml", exact: true, part: PartFootnote},
	{prefix: "word/endnotes.xml", exact: true, part: PartEndnote},
	{prefix: "word/comments.xml", exact: true, part: PartComment},
}

// docxSkip are the elements whose subtrees hold nothing a reader of the
// document sees.
//
// Two groups, for two different reasons. The property elements hold formatting
// and never text. The other three hold text that exists in the file and is not
// shown: w:del and w:delText are what tracked changes removed, w:moveFrom is
// where tracked content was moved from, and w:instrText is an instruction to
// the word processor rather than a word on the page. Extracting any of them
// would put text into the model's input that no human reading the document
// would ever see, which is the shape of the problem this library exists to
// avoid (docs/adr/0017-untrusted-document-content.md).
var docxSkip = map[string]bool{
	"pPr":      true,
	"rPr":      true, // reached other than through w:r; a run's own rPr is inspected
	"sectPr":   true,
	"tblPr":    true,
	"tblPrEx":  true,
	"trPr":     true,
	"tcPr":     true,
	"tblGrid":  true,
	"numPr":    true,
	"sdtPr":    true,
	"sdtEndPr": true,

	"del":       true,
	"delText":   true,
	"moveFrom":  true,
	"instrText": true,
}

// ExtractDOCX reads the body of a DOCX as a single page.
//
// One page, always. A Word document is paginated by the renderer, so the page
// a paragraph lands on is a function of fonts and paper size rather than of
// the file; an explicit page break is only some of the breaks a document has.
// See the package comment for why one page always beats a number that is right
// on some documents.
func ExtractDOCX(data []byte, lim detect.Limits, cum *detect.Counter) (doc *Document, err error) {
	defer recovered(&doc, &err)
	lim = lim.Normalised()

	c, err := openContainer(data, lim, cum)
	if err != nil {
		return nil, err
	}
	text := detect.NewCounter(detect.LimitTextBytes, lim.MaxTextBytes)

	w := &docxWalker{
		b:    newPageBuilder(1, text),
		part: PartDocument,
	}
	w.acc.text = text

	if err := w.readPart(c, docxBody, lim); err != nil {
		return nil, err
	}

	skipped, err := docxSkipped(c, lim, text)
	if err != nil {
		return nil, err
	}

	return &Document{
		Kind:       detect.KindDOCX,
		Pages:      []normalise.Page{w.b.page()},
		Skipped:    skipped,
		HiddenRuns: w.hidden,
	}, nil
}

// docxWalker turns the body part's element tree into words and lines.
//
// The descent is generic — every element is walked into unless it is on
// [docxSkip] — rather than a list of the containers text is known to appear
// in. OOXML wraps runs in content controls, hyperlinks, smart tags, tracked
// insertions, text boxes and markup-compatibility alternatives, and a walker
// that recognises only the wrappers its author thought of loses text to the
// next one. Depth is bounded by a detect.Depth budget, so descending into
// everything is safe.
type docxWalker struct {
	b      *pageBuilder
	acc    textAccumulator
	part   Part
	hidden int
	inRow  int
}

// readPart opens a part and walks it.
func (w *docxWalker) readPart(c *container, name string, lim detect.Limits) error {
	rc, err := c.open(name, w.part)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read
	return w.walk(newDecoder(rc), lim.Depth())
}

// walk reads the part's root element and everything under it.
func (w *docxWalker) walk(d *xml.Decoder, dep detect.Depth) error {
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				// A part with no elements is an empty body, not a failure.
				return nil
			}
			return err
		}
		if el, ok := t.(xml.StartElement); ok {
			if err := w.element(d, el, dep); err != nil {
				return err
			}
			return w.flush()
		}
	}
}

// element dispatches one start tag and consumes its element.
func (w *docxWalker) element(d *xml.Decoder, el xml.StartElement, dep detect.Depth) error {
	dep, err := dep.Descend()
	if err != nil {
		return err
	}
	if docxSkip[el.Name.Local] {
		return skipElement(d, w.part)
	}
	switch el.Name.Local {
	case "t":
		return w.chardata(d)
	case "tab":
		// A tab is a gap, and it is kept because a tabbed layout is how a
		// great many Word documents lay out a label and its value.
		if err := w.acc.addString("\t"); err != nil {
			return err
		}
		return skipElement(d, w.part)
	case "br", "cr":
		if err := w.flush(); err != nil {
			return err
		}
		w.b.endLine()
		return skipElement(d, w.part)
	case "p":
		return w.paragraph(d, dep)
	case "tr":
		return w.row(d, dep)
	case "r":
		return w.run(d, dep)
	case "AlternateContent":
		return w.alternate(d, dep)
	default:
		return w.children(d, dep)
	}
}

// children walks every child of the element currently open and returns when it
// closes.
func (w *docxWalker) children(d *xml.Decoder, dep detect.Depth) error {
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", w.part, "part ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			if err := w.element(d, t, dep); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// paragraph emits the paragraph's text as one word.
//
// One word per paragraph rather than one per w:t run is deliberate. A DOCX
// stores its own spaces, so the runs of a paragraph concatenate to exactly the
// right string; splitting them into separate words would make
// internal/normalise insert a space at every formatting change, turning "the
// **total** amount" into "the total  amount" and worse. Splitting happens where
// the document says there is a break — a paragraph, a line break, a cell
// boundary — and nowhere else.
func (w *docxWalker) paragraph(d *xml.Decoder, dep detect.Depth) error {
	if err := w.children(d, dep); err != nil {
		return err
	}
	if err := w.flush(); err != nil {
		return err
	}
	if w.inRow == 0 {
		w.b.endLine()
	}
	return nil
}

// row keeps a table row on one line.
//
// A table read as a flat stream of cells loses the row, and the row is what
// makes a table a table: it is what associates a label in the first column
// with the figure in the last. Every paragraph in every cell of the row
// becomes a separate word on the row's single line, so cell and paragraph
// boundaries survive as word boundaries — with no geometry,
// internal/normalise separates adjacent words unconditionally — while the row
// stays one line.
//
// A table nested inside a cell collapses into the outer row's line, because
// its rows do not close the line the outer row opened. That loses the inner
// table's row structure and keeps the outer one's, which is the right way
// round: the nested case is rare and the outer row is the one carrying the
// meaning.
func (w *docxWalker) row(d *xml.Decoder, dep detect.Depth) error {
	w.inRow++
	err := w.children(d, dep)
	w.inRow--
	if err != nil {
		return err
	}
	if err := w.flush(); err != nil {
		return err
	}
	if w.inRow == 0 {
		w.b.endLine()
	}
	return nil
}

// run walks one w:r, inspecting its own run properties for hidden text on the
// way past.
func (w *docxWalker) run(d *xml.Decoder, dep detect.Depth) error {
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", w.part, "part ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			if t.Name.Local == "rPr" {
				hidden, err := w.runProps(d)
				if err != nil {
					return err
				}
				if hidden {
					w.hidden++
				}
				continue
			}
			if err := w.element(d, t, dep); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// runProps consumes a w:rPr and reports whether the run is hidden.
//
// The scan is iterative because run properties are the one part of the tree a
// hostile document can nest arbitrarily without any of it looking odd, and
// this runs before the depth budget would notice.
func (w *docxWalker) runProps(d *xml.Decoder) (bool, error) {
	depth := 1
	hidden := false
	for depth > 0 {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return false, malformed("xml", w.part, "part ended inside an element")
			}
			return false, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			depth++
			// w:vanish and w:webHidden are the two ways a run is marked
			// invisible. Both carry an optional w:val that turns the property
			// off again, which is how a style's hidden flag is cancelled on
			// one run.
			if t.Name.Local == "vanish" || t.Name.Local == "webHidden" {
				if !propertyOff(t) {
					hidden = true
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return hidden, nil
}

// propertyOff reports whether a boolean OOXML property carries a value turning
// it off. An absent w:val means on, which is why the default is false.
func propertyOff(el xml.StartElement) bool {
	v, ok := attr(el, "val")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off":
		return true
	default:
		return false
	}
}

// alternate walks a markup-compatibility alternative, taking exactly one
// branch.
//
// mc:AlternateContent holds the same content expressed twice: an mc:Choice for
// a consumer that understands some extension, an mc:Fallback for one that does
// not. Walking both emits every word in it twice, which duplicates the
// document's text and doubles what a model is asked to read. The schema puts
// Choice first, so taking the first branch encountered takes the Choice
// wherever there is one.
func (w *docxWalker) alternate(d *xml.Decoder, dep detect.Depth) error {
	taken := false
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", w.part, "part ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			isBranch := t.Name.Local == "Choice" || t.Name.Local == "Fallback"
			if isBranch && !taken {
				taken = true
				if err := w.children(d, dep); err != nil {
					return err
				}
				continue
			}
			if err := skipElement(d, w.part); err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		}
	}
}

// chardata accumulates the text of a w:t.
func (w *docxWalker) chardata(d *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", w.part, "part ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.CharData:
			// Only the element's own text. A w:t has no child elements in any
			// legitimate document; one that has them is not given a way to
			// smuggle their content in as though it were the parent's.
			if depth == 1 {
				if err := w.acc.add(t); err != nil {
					return err
				}
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// flush emits whatever text has accumulated as one word.
func (w *docxWalker) flush() error {
	if w.acc.empty() {
		return nil
	}
	return w.b.addWord(w.acc.take())
}

// docxSkipped reports which auxiliary parts held text.
//
// Presence alone is not reported, because Word ships a footnotes part with
// every document whether or not the document has a footnote. Each candidate is
// opened and scanned for a single non-whitespace w:t, under the same ceilings
// and charging the same cumulative budget as the body — so a bomb hidden in a
// header is refused here exactly as it would be refused there.
//
// A part that cannot be read is reported as skipped rather than dropped. The
// question this answers is "was text left out", and an unreadable part is a
// part that may have held some.
func docxSkipped(c *container, lim detect.Limits, text *detect.Counter) ([]Part, error) {
	var out []Part
	for _, aux := range docxAuxParts {
		var names []string
		if aux.exact {
			if c.has(aux.prefix) {
				names = []string{aux.prefix}
			}
		} else {
			for _, n := range c.namesWithPrefix(aux.prefix) {
				if strings.HasSuffix(n, aux.suffix) {
					names = append(names, n)
				}
			}
		}
		for _, n := range names {
			has, err := partHasText(c, n, aux.part, lim, text)
			if err != nil {
				// A ceiling reached while scanning an auxiliary part is the
				// document being too big, which is the caller's decision to
				// take, not something to swallow because the body happened to
				// fit.
				if isLimit(err) {
					return nil, err
				}
				has = true
			}
			if has {
				out = append(out, aux.part)
				break
			}
		}
	}
	return out, nil
}

// partHasText reports whether a part contains at least one w:t holding
// something other than whitespace.
func partHasText(c *container, name string, part Part, lim detect.Limits, text *detect.Counter) (bool, error) {
	rc, err := c.open(name, part)
	if err != nil {
		return false, err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read

	d := newDecoder(rc)
	// The scan is a flat loop rather than a recursive descent, so nesting is
	// counted rather than spent from a detect.Depth: the budget is a level
	// ceiling, and a part with a hundred sibling elements is not a part a
	// hundred levels deep.
	maxDepth := lim.Normalised().MaxDepth
	depth, inText := 0, 0
	for {
		t, err := nextToken(d, part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return false, nil
			}
			return false, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return false, &detect.LimitError{Limit: detect.LimitDepth, Max: int64(maxDepth)}
			}
			if t.Name.Local == "t" {
				inText++
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "t" && inText > 0 {
				inText--
			}
		case xml.CharData:
			if inText > 0 {
				if err := text.Add(int64(len(t))); err != nil {
					return false, err
				}
				if hasGraphic(string(t)) {
					return true, nil
				}
			}
		}
	}
}
