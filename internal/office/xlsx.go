package office

import (
	"encoding/xml"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// The fixed part names an XLSX package uses.
const (
	xlsxWorkbook     = "xl/workbook.xml"
	xlsxWorkbookRels = "xl/_rels/workbook.xml.rels"
	xlsxSharedString = "xl/sharedStrings.xml"
	xlsxBase         = "xl/"
	xlsxSheetPrefix  = "xl/worksheets/"
)

// maxCellsPerRow bounds how many cells one row may hold before it is emitted.
//
// A row is buffered so that its cells can be put back into column order, so
// the buffer is an allocation a document controls and therefore needs a
// ceiling. The number is Excel's own column limit: a row with more cells than
// a spreadsheet can have columns is not a row a spreadsheet wrote.
const maxCellsPerRow = 16384

// maxColumnLetters is the longest column reference a spreadsheet can have.
// Excel's last column is XFD; anything longer is not a reference and is
// treated as one that could not be read rather than converted into an enormous
// number.
const maxColumnLetters = 3

// ExtractXLSX reads a workbook as one page per worksheet, in workbook order.
//
// Page N is the Nth sheet the workbook lists, which is the only handle a
// caller gets on which sheet a value came from: sheet names are read to order
// the sheets and are never emitted and never put in an error, because a sheet
// name is a string the document's author chose and therefore document content
// (docs/rules.md §2.5).
//
// Cell values are the values as stored. Number formats are not applied — see
// the package comment for why a half-implemented format language is worse than
// none.
func ExtractXLSX(data []byte, lim detect.Limits, cum *detect.Counter) (doc *Document, err error) {
	defer recovered(&doc, &err)
	lim = lim.Normalised()

	c, err := openContainer(data, lim, cum)
	if err != nil {
		return nil, err
	}

	sheets, err := xlsxSheetNames(c, lim)
	if err != nil {
		return nil, err
	}
	// The sheet count is checked before a page is built for any of them, and
	// it also bounds spend: a workbook declaring ten thousand sheets is not a
	// crash, it is a bill (docs/threat-model.md T2).
	if err := lim.CheckPages(len(sheets)); err != nil {
		return nil, err
	}

	// The shared string table gets a budget of its own rather than sharing
	// the page text budget. It is held whole in memory and every cell that
	// references an entry emits a fresh copy, so charging both to one counter
	// would either double-count a legitimate workbook or leave the table
	// itself unbounded. Two ceilings, each finite, each the same size.
	sst, err := xlsxSharedStrings(c, lim)
	if err != nil {
		return nil, err
	}

	text := detect.NewCounter(detect.LimitTextBytes, lim.MaxTextBytes)
	pages := make([]normalise.Page, 0, len(sheets))
	for i, name := range sheets {
		w := &xlsxWalker{
			b:    newPageBuilder(i+1, text),
			sst:  sst,
			text: text,
			part: PartWorksheet,
			page: i + 1,
		}
		w.acc.text = text
		// A sheet the workbook names but the container does not hold is an
		// empty page rather than a failure. The workbook said there are five
		// sheets; the answer to "what was on sheet four" is "nothing that
		// could be read", and that is representable.
		if c.has(name) {
			if err := w.readPart(c, name, lim); err != nil {
				return nil, err
			}
		}
		pages = append(pages, w.b.page())
	}

	return &Document{Kind: detect.KindXLSX, Pages: pages}, nil
}

// xlsxSheetNames returns the container names of the worksheets, in workbook
// order.
//
// The order comes from xl/workbook.xml, which lists the sheets, resolved
// through xl/_rels/workbook.xml.rels, which says which part each one is. That
// is the only authoritative ordering: the numbers in sheet1.xml, sheet2.xml
// are part names and need not match tab order at all.
//
// When the workbook or its relationships cannot be read, the worksheet parts
// are used in numeric name order instead. That is a guess about order and it
// is a much better one than refusing a workbook every other reader opens; it
// is never a guess about content.
func xlsxSheetNames(c *container, lim detect.Limits) ([]string, error) {
	ids, err := xlsxWorkbookOrder(c, lim)
	if err != nil && isLimit(err) {
		return nil, err
	}
	if err == nil && len(ids) > 0 {
		rels, relErr := xlsxRelationships(c, lim)
		if relErr != nil && isLimit(relErr) {
			return nil, relErr
		}
		if relErr == nil {
			out := make([]string, 0, len(ids))
			for _, id := range ids {
				if target, ok := rels[id]; ok {
					out = append(out, target)
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	return xlsxSheetFallback(c), nil
}

// xlsxWorkbookOrder returns the relationship identifiers of the sheets, in the
// order the workbook lists them.
func xlsxWorkbookOrder(c *container, lim detect.Limits) ([]string, error) {
	rc, err := c.open(xlsxWorkbook, PartWorkbook)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read

	d := newDecoder(rc)
	var ids []string
	err = eachElement(d, PartWorkbook, lim, func(el xml.StartElement) error {
		if el.Name.Local != "sheet" {
			return nil
		}
		if len(ids) >= maxZipEntries {
			return unsupported("sheet", PartWorkbook, "workbook names too many sheets")
		}
		// The identifier is matched on its local name, so it is found whether
		// the relationship namespace is bound to r, rel or nothing at all.
		if id, ok := attr(el, "id"); ok {
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// xlsxRelationships maps a relationship identifier to the container name of
// the part it points at.
//
// A relationship in External mode points outside the package. It is dropped
// rather than resolved: nothing a document references is ever fetched
// (docs/rules.md §7.4), and a target this package will not follow is better
// absent than present and silently failing later.
func xlsxRelationships(c *container, lim detect.Limits) (map[string]string, error) {
	rc, err := c.open(xlsxWorkbookRels, PartRelationships)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read

	d := newDecoder(rc)
	out := make(map[string]string, 8)
	err = eachElement(d, PartRelationships, lim, func(el xml.StartElement) error {
		if el.Name.Local != "Relationship" {
			return nil
		}
		if len(out) >= maxZipEntries {
			return unsupported("sheet", PartRelationships, "too many relationships")
		}
		if mode, ok := attr(el, "TargetMode"); ok && strings.EqualFold(mode, "External") {
			return nil
		}
		id, hasID := attr(el, "Id")
		target, hasTarget := attr(el, "Target")
		if !hasID || !hasTarget {
			return nil
		}
		if name, ok := resolveTarget(target); ok {
			out[id] = name
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// resolveTarget turns a relationship target into a container name.
//
// A target is relative to the part's own directory — xl/ for the workbook's
// relationships — or absolute within the package when it begins with a slash.
// A target containing a parent reference is refused outright rather than
// cleaned: nothing legitimate needs one, resolving it can only ever name
// something outside the directory the relationship is scoped to, and the
// resulting name is used as a lookup key that an attacker would then choose.
// Nothing is ever written to disk here, so this is not a path traversal in the
// filesystem sense; it is refused because a name nobody meant is not a name to
// act on.
func resolveTarget(target string) (string, bool) {
	t := strings.TrimSpace(target)
	if t == "" || strings.Contains(t, "..") {
		return "", false
	}
	if strings.HasPrefix(t, "/") {
		return strings.TrimPrefix(t, "/"), true
	}
	t = strings.TrimPrefix(t, "./")
	return xlsxBase + t, true
}

// xlsxSheetFallback returns the worksheet parts in numeric name order.
func xlsxSheetFallback(c *container) []string {
	names := make([]string, 0, 8)
	for _, n := range c.namesWithPrefix(xlsxSheetPrefix) {
		if strings.HasSuffix(n, ".xml") {
			names = append(names, n)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		ni, oki := trailingNumber(names[i])
		nj, okj := trailingNumber(names[j])
		if oki && okj && ni != nj {
			return ni < nj
		}
		if oki != okj {
			return oki
		}
		return names[i] < names[j]
	})
	return names
}

// trailingNumber returns the run of digits ending a part's base name, so that
// sheet10.xml sorts after sheet9.xml rather than before it.
func trailingNumber(name string) (int, bool) {
	base := strings.TrimSuffix(name, ".xml")
	end := len(base)
	start := end
	for start > 0 && base[start-1] >= '0' && base[start-1] <= '9' {
		start--
	}
	if start == end || end-start > 9 {
		return 0, false
	}
	n, err := strconv.Atoi(base[start:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// xlsxSharedStrings reads the string table, or returns nil when there is none.
//
// A workbook without one is ordinary: a sheet of nothing but numbers needs no
// strings, and a producer may write every string inline instead.
func xlsxSharedStrings(c *container, lim detect.Limits) ([]string, error) {
	if !c.has(xlsxSharedString) {
		return nil, nil
	}
	rc, err := c.open(xlsxSharedString, PartSharedStrings)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read

	budget := detect.NewCounter(detect.LimitTextBytes, lim.Normalised().MaxTextBytes)
	acc := textAccumulator{text: budget}
	d := newDecoder(rc)
	dep := lim.Depth()

	var out []string
	for {
		t, err := nextToken(d, PartSharedStrings)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return out, nil
			}
			return nil, err
		}
		el, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if el.Name.Local != "si" {
			continue
		}
		// The sst element's count attribute is never used to size this slice.
		// A declared count costs nothing to write and everything to honour,
		// so the table grows against what actually arrives and is checked
		// against the object ceiling as it does.
		if err := lim.CheckObjects(len(out) + 1); err != nil {
			return nil, err
		}
		if err := readRichText(d, PartSharedStrings, &acc, dep); err != nil {
			return nil, err
		}
		out = append(out, acc.take())
	}
}

// readRichText consumes a CT_Rst — the shape both a shared string item and an
// inline string take — accumulating its text.
//
// Runs are concatenated with nothing between them, because a rich-text run
// boundary is a change of formatting in the middle of a value and not a word
// break. Phonetic guides (rPh) are the reading aid printed above Japanese
// text; they are a transliteration of the value rather than part of it, and
// concatenating them would interleave two spellings of the same word.
func readRichText(d *xml.Decoder, part Part, acc *textAccumulator, dep detect.Depth) error {
	depth := 1
	inText := 0
	skipTo := -1
	for depth > 0 {
		t, err := nextToken(d, part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", part, "part ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			depth++
			if depth > dep.Remaining() {
				return &detect.LimitError{Limit: detect.LimitDepth, Max: int64(dep.Remaining())}
			}
			if skipTo < 0 && (t.Name.Local == "rPh" || t.Name.Local == "phoneticPr") {
				skipTo = depth
			}
			if skipTo < 0 && t.Name.Local == "t" {
				inText++
			}
		case xml.EndElement:
			if skipTo == depth {
				skipTo = -1
			} else if skipTo < 0 && t.Name.Local == "t" && inText > 0 {
				inText--
			}
			depth--
		case xml.CharData:
			if skipTo < 0 && inText > 0 {
				if err := acc.add(t); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// eachElement calls fn for every start tag in a part, bounding nesting.
//
// It is the flat scan the small parts want: a workbook and a relationship part
// are attribute-only documents where nothing depends on where an element sits,
// so a walker that tracked structure would be structure nobody reads.
func eachElement(d *xml.Decoder, part Part, lim detect.Limits, fn func(xml.StartElement) error) error {
	maxDepth := lim.Normalised().MaxDepth
	depth := 0
	for {
		t, err := nextToken(d, part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return nil
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return &detect.LimitError{Limit: detect.LimitDepth, Max: int64(maxDepth)}
			}
			if err := fn(t); err != nil {
				return err
			}
		case xml.EndElement:
			depth--
		}
	}
}

// xlsxWalker turns one worksheet into a page: one line per row, one word per
// cell.
type xlsxWalker struct {
	b    *pageBuilder
	acc  textAccumulator
	sst  []string
	text *detect.Counter
	part Part
	page int
}

// cell is one buffered cell, kept with its column so a row can be put back in
// order before it is emitted.
type cell struct {
	col  int
	text string
}

// readPart opens a worksheet and walks it.
func (w *xlsxWalker) readPart(c *container, name string, lim detect.Limits) error {
	rc, err := c.open(name, w.part)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read
	return w.walk(newDecoder(rc), lim.Depth())
}

// walk reads the worksheet's root element and everything under it.
func (w *xlsxWalker) walk(d *xml.Decoder, dep detect.Depth) error {
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return nil
			}
			return err
		}
		if el, ok := t.(xml.StartElement); ok {
			return w.element(d, el, dep)
		}
	}
}

// element dispatches one start tag and consumes its element.
func (w *xlsxWalker) element(d *xml.Decoder, el xml.StartElement, dep detect.Depth) error {
	dep, err := dep.Descend()
	if err != nil {
		return err
	}
	switch el.Name.Local {
	case "row":
		return w.row(d, dep)
	case "headerFooter", "drawing", "legacyDrawing", "extLst", "dataValidations", "autoFilter":
		// Sheet furniture. A print header is not a cell, and the drawing
		// parts hold shapes whose text lives in a part of their own that the
		// worksheet only references.
		return skipElement(d, w.part)
	default:
		return w.children(d, dep)
	}
}

// children walks every child of the element currently open.
func (w *xlsxWalker) children(d *xml.Decoder, dep detect.Depth) error {
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformedPage("xml", w.part, w.page, "sheet ended inside an element")
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

// row buffers a row's cells, orders them by column and emits them on one line.
//
// Ordering by column rather than by document order is what keeps a sheet whose
// cells are written out of sequence from reading out of sequence. A cell with
// no usable reference keeps its position relative to the others that have
// none, at the end of the row, because there is nowhere honest to put it.
func (w *xlsxWalker) row(d *xml.Decoder, dep detect.Depth) error {
	cells := make([]cell, 0, 16)
	for {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformedPage("xml", w.part, w.page, "sheet ended inside a row")
			}
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			if t.Name.Local != "c" {
				if err := skipElement(d, w.part); err != nil {
					return err
				}
				continue
			}
			if len(cells) >= maxCellsPerRow {
				return &detect.LimitError{Limit: detect.LimitObjects, Max: maxCellsPerRow}
			}
			c, err := w.cell(d, t, dep)
			if err != nil {
				return err
			}
			cells = append(cells, c)
		case xml.EndElement:
			sort.SliceStable(cells, func(i, j int) bool { return cells[i].col < cells[j].col })
			for _, c := range cells {
				if err := w.b.addWord(c.text); err != nil {
					return err
				}
			}
			w.b.endLine()
			return nil
		}
	}
}

// cell reads one cell and returns its displayed value.
func (w *xlsxWalker) cell(d *xml.Decoder, el xml.StartElement, dep detect.Depth) (cell, error) {
	typ, _ := attr(el, "t")
	ref, _ := attr(el, "r")

	out := cell{col: columnOf(ref)}
	var raw, inline string
	haveInline := false

	depth := 1
	for depth > 0 {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return out, malformedPage("xml", w.part, w.page, "sheet ended inside a cell")
			}
			return out, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "v":
				if err := w.chardata(d); err != nil {
					return out, err
				}
				raw = w.acc.take()
			case "is":
				if err := readRichText(d, w.part, &w.acc, dep); err != nil {
					return out, err
				}
				inline, haveInline = w.acc.take(), true
			default:
				// A formula's own text is not what the cell shows; the cached
				// result in v is. Everything else in a cell is formatting.
				if err := skipElement(d, w.part); err != nil {
					return out, err
				}
			}
		case xml.EndElement:
			depth--
		}
	}

	switch typ {
	case "s":
		// A shared string cell's v is an index into the table. An index that
		// is not a number, or points outside the table, yields nothing rather
		// than a value from somewhere else in the sheet.
		i, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || i < 0 || i >= len(w.sst) {
			return out, nil
		}
		out.text = w.sst[i]
		if err := w.text.Add(int64(len(out.text))); err != nil {
			return out, err
		}
	case "inlineStr":
		if haveInline {
			out.text = inline
		}
	case "b":
		// A boolean is stored as 0 or 1 and shown as FALSE or TRUE. This is
		// the one place this package renders rather than reports, because the
		// stored form carries no clue that it is a boolean at all and a bare
		// 1 in the text is indistinguishable from the number one.
		switch strings.TrimSpace(raw) {
		case "1":
			out.text = "TRUE"
		case "0":
			out.text = "FALSE"
		}
		if err := w.text.Add(int64(len(out.text))); err != nil {
			return out, err
		}
	default:
		// n (number), str (a formula's string result), e (an error value such
		// as #REF!) and the absent type all show what is stored.
		out.text = raw
	}
	return out, nil
}

// chardata accumulates the text of the element currently open.
func (w *xlsxWalker) chardata(d *xml.Decoder) error {
	depth := 1
	for depth > 0 {
		t, err := nextToken(d, w.part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformedPage("xml", w.part, w.page, "sheet ended inside an element")
			}
			return err
		}
		switch t := t.(type) {
		case xml.CharData:
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

// columnOf returns the zero-based column a cell reference names, or a value
// that sorts after every real column when there is not one.
//
// The letters are bounded at Excel's own three, so a reference of two hundred
// letters is treated as unreadable instead of being converted into a number
// with no meaning.
func columnOf(ref string) int {
	n := 0
	letters := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		switch {
		case ch >= 'A' && ch <= 'Z':
			n = n*26 + int(ch-'A') + 1
		case ch >= 'a' && ch <= 'z':
			n = n*26 + int(ch-'a') + 1
		default:
			if letters == 0 {
				return unknownColumn
			}
			return n - 1
		}
		letters++
		if letters > maxColumnLetters {
			return unknownColumn
		}
	}
	if letters == 0 {
		return unknownColumn
	}
	return n - 1
}

// unknownColumn sorts after every column a spreadsheet can have.
const unknownColumn = 1 << 30
