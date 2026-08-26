// Package layout is the neutral representation of the structure an OCR
// provider recognised on a page: tables and key-value regions.
//
// # Why it exists
//
// Document AI, Textract and Azure all detect table structure and key-value
// pairs, and ovrin's Recognition carries words and lines. The difference is
// discarded — docs/feature-matrix.md marks three cells "silently ignored" and
// says the escape hatch through Recognition.Raw "is not a real answer", because
// a type assertion against a provider's own response is exactly the
// provider-specific code the seam exists to remove. This package is the one
// shape every provider normalises into, so that an adapter maps and does not
// decide (docs/rules.md §6.2), and so that structure survives the seam the way
// word boxes and per-word confidence already do.
//
// # What structure is for
//
// "The 40 in the Quantity column of the A4 Paper row" is a different claim from
// "the 40 somewhere on the page", and the difference is the whole value of a
// table. A number in a total column is a total; the same number two columns
// left is a quantity. So a [Cell] carries its row and column, not only its box,
// and [Table.Heading] returns the text that identifies them. [Ref] names a cell
// by position alone, which is the form that can go in a provenance entry, a log
// line or a review interface without repeating what the cell says.
//
// # Conventions
//
// The same conventions ovrin.Recognition already establishes, because a second
// convention for the same quantity is a defect waiting to happen:
//
//   - coordinates in page points, with the origin at the top left;
//   - confidence on 0..1, carried through from the provider and never
//     interpreted here, and never fabricated as 1.0 by a provider that reports
//     none;
//   - reading order — cells top to bottom then left to right, tables and pairs
//     in the order they appear on the page — rather than whatever order the
//     provider's API returned.
//
// [Layout.Order] puts a provider's output into that order, and [Layout.Check]
// reports the structural mistakes an adapter can make. Between them they are
// what an adapter's contract test asserts.
//
// # What is not here
//
// No text. This package holds what a provider recognised, positioned; it does
// not render a table to a string, decide which cells belong to a prompt, or
// match a value to a cell. Those are pipeline decisions and belong on the far
// side of this type, with the rest of the decisions.
//
// The types mirror the root package's conventions field for field so the root
// can convert at the seam, and are declared here rather than imported because
// the root package imports the pipeline and a package the root imports cannot
// import the root back.
package layout

import "strconv"

// Rect is a region of a page, in points, with the origin at the top left.
//
// It mirrors ovrin.Rect field for field so that ovrin.Rect(r) converts. The
// origin is neither PDF's nor an image format's; one convention had to be
// chosen and adapters normalise to it (docs/adr/0015-provenance.md).
type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Zero reports whether the rectangle carries no geometry, which is how a
// provider that reports structure without positions describes every cell it
// produces. Zero means unknown, never "not on the page".
func (r Rect) Zero() bool {
	return r.MinX == 0 && r.MinY == 0 && r.MaxX == 0 && r.MaxY == 0
}

// Union returns the smallest rectangle containing both.
//
// A zero operand is ignored rather than included, so that a cell the provider
// gave no geometry for does not drag the table's box back to the origin and
// produce a highlight covering the top-left quarter of the page. It mirrors
// internal/normalise's Rect.Union, deliberately: two rules for combining boxes
// would put two different rectangles on one review interface.
func (r Rect) Union(o Rect) Rect {
	if r.Zero() {
		return o
	}
	if o.Zero() {
		return r
	}
	if o.MinX < r.MinX {
		r.MinX = o.MinX
	}
	if o.MinY < r.MinY {
		r.MinY = o.MinY
	}
	if o.MaxX > r.MaxX {
		r.MaxX = o.MaxX
	}
	if o.MaxY > r.MaxY {
		r.MaxY = o.MaxY
	}
	return r
}

// Region is a run of recognised text and where it was.
//
// It is the same three things a Word carries — text, box, confidence — without
// the line index, because the halves of a key-value pair are regions of a page
// and not members of a line: a label and its value are routinely on different
// lines, and that is what makes the pair worth reporting at all.
type Region struct {
	// Text is what the provider read, unnormalised. It is document content.
	Text string

	// Box is the region on the page, in points with the origin top left. A
	// zero Rect means the provider gave no geometry.
	Box Rect

	// Confidence is the provider's own, on 0..1. A provider that reports none
	// for a region sets the page confidence here and records that it did,
	// rather than fabricating 1.0.
	Confidence float64
}

// CellKind is what a provider said a cell is.
//
// The set is closed and is the intersection of what the three providers report:
// Textract marks individual cells as column or row headers, Azure gives each
// cell a kind, and Document AI splits a table into header rows and body rows.
// All three map onto this without loss. A provider that reports nothing leaves
// [CellUnknown], which is not the same as [CellData]: "this is a data cell" and
// "this provider does not say" are different facts, and collapsing them would
// make [Table.Heading] silently wrong instead of silently empty.
type CellKind string

// The cell kinds.
const (
	// CellUnknown is the zero value: the provider did not classify the cell.
	CellUnknown CellKind = ""

	// CellData is a cell the provider said holds a value.
	CellData CellKind = "data"

	// CellColumnHeader is a cell that names its column — "Quantity".
	CellColumnHeader CellKind = "column_header"

	// CellRowHeader is a cell that names its row. Providers mark these far
	// less often than column headers, which is why [Table.Heading] returns an
	// empty row heading rather than guessing one.
	CellRowHeader CellKind = "row_header"
)

// String returns the kind, or "unknown" for the zero value, so a kind never
// renders as an empty string in a diagnostic.
func (k CellKind) String() string {
	if k == CellUnknown {
		return "unknown"
	}
	return string(k)
}

// Header reports whether the kind is one of the header kinds, which is the
// question every caller of CellKind actually asks.
func (k CellKind) Header() bool {
	return k == CellColumnHeader || k == CellRowHeader
}

// Cell is one cell of a table.
//
// Row and Column are 0-based indexes into the table's grid, not positions on
// the page: they are what makes "the Quantity column of the A4 Paper row"
// expressible, and they are what a [Ref] carries.
type Cell struct {
	// Row is the 0-based index of the cell's first row.
	Row int

	// Column is the 0-based index of the cell's first column.
	Column int

	// RowSpan is how many rows the cell covers. Zero and one both mean one
	// row: providers differ on whether they report a span of one, and
	// normalising it here means every adapter does not have to.
	RowSpan int

	// ColumnSpan is how many columns the cell covers, with zero meaning one,
	// as for RowSpan.
	ColumnSpan int

	// Kind is what the provider said the cell is.
	Kind CellKind

	// Text is the cell's content as the provider read it, unnormalised. It is
	// document content: it belongs on a field, never in a log line or an error
	// (docs/rules.md §2.5, §7.5).
	Text string

	// Box is the cell's region on the page, in points with the origin top
	// left. A zero Rect means the provider gave no geometry.
	Box Rect

	// Confidence is the provider's own for this cell, on 0..1. It is carried
	// through unchanged so a scorer can use it; nothing here interprets it.
	Confidence float64
}

// Rows returns how many rows the cell covers, treating an unreported span of
// zero as one.
func (c Cell) Rows() int { return span(c.RowSpan) }

// Columns returns how many columns the cell covers, treating an unreported
// span of zero as one.
func (c Cell) Columns() int { return span(c.ColumnSpan) }

// Covers reports whether the cell occupies the given grid position, spans
// included.
//
// It is the operation every lookup in a merged table needs: the "40" under
// Quantity may be stored at row 3 and asked for at row 4 because the row above
// it spans two.
func (c Cell) Covers(row, column int) bool {
	return row >= c.Row && row < c.Row+c.Rows() &&
		column >= c.Column && column < c.Column+c.Columns()
}

func span(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// Heading is the text identifying a cell's column and row.
//
// Both are document content. Either may be empty, and an empty one means the
// provider marked no header, never that the table has none.
type Heading struct {
	// Column is the text of the column header covering the cell, with the
	// headers joined by a space when a column has more than one.
	Column string

	// Row is the text of the row header covering the cell, and is empty far
	// more often than Column: see [CellRowHeader].
	Row string
}

// Table is one table a provider recognised on one page.
type Table struct {
	// Page is 1-based, matching ovrin.Line.Page.
	Page int

	// Box is the table's region on the page. [Layout.Order] fills it from the
	// union of the cell boxes when a provider reports none.
	Box Rect

	// Rows and Columns are the table's declared size. They are the provider's
	// own count rather than derived from Cells, because a table whose last row
	// is empty still has that row, and deriving the size would silently lose
	// it. [Layout.Check] reports a cell that falls outside them.
	Rows    int
	Columns int

	// Cells are in reading order: by row, then by column. A table with merged
	// cells has fewer entries than Rows*Columns, and a sparse table has fewer
	// still — a position no cell covers is a position the provider read
	// nothing at, which is a fact about the document.
	Cells []Cell

	// Confidence is the provider's own for the table as a whole, on 0..1.
	Confidence float64
}

// At returns the cell covering a grid position, spans included.
//
// The second result is false when nothing covers it, which is the honest answer
// for a sparse table: an empty [Cell] would be indistinguishable from a cell the
// provider read as empty, and the two mean different things.
func (t Table) At(row, column int) (Cell, bool) {
	for _, c := range t.Cells {
		if c.Covers(row, column) {
			return c, true
		}
	}
	return Cell{}, false
}

// Row returns the cells whose first row is i, in column order.
//
// First row rather than every covering cell, so that a cell spanning three rows
// appears in one row's result and not three. A caller reconstructing a merged
// grid wants [Table.At].
func (t Table) Row(i int) []Cell {
	var out []Cell
	for _, c := range t.Cells {
		if c.Row == i {
			out = append(out, c)
		}
	}
	return out
}

// Column returns the cells whose first column is i, in row order.
func (t Table) Column(i int) []Cell {
	var out []Cell
	for _, c := range t.Cells {
		if c.Column == i {
			out = append(out, c)
		}
	}
	return out
}

// Heading returns the text identifying the cell's column and row.
//
// A column heading is every [CellColumnHeader] whose columns overlap the
// cell's, in row order, joined by a space — a two-row header reading "Unit" and
// "Price" is one heading and splitting it would name the column wrongly.
//
// A row heading is only ever a cell the provider marked [CellRowHeader]. When
// none is marked the row heading is empty. Falling back to the leftmost cell is
// a convention that is right for an invoice's line items and wrong for a
// balance sheet, and a wrong heading is worse than no heading: it attaches a
// value to a claim the document did not make. A caller who knows their corpus
// can take t.At(row, 0) themselves, having decided that on purpose.
func (t Table) Heading(c Cell) Heading {
	var h Heading
	h.Column = join(t.headers(CellColumnHeader, func(o Cell) bool {
		return overlap(o.Column, o.Columns(), c.Column, c.Columns())
	}))
	h.Row = join(t.headers(CellRowHeader, func(o Cell) bool {
		return overlap(o.Row, o.Rows(), c.Row, c.Rows())
	}))
	return h
}

// headers returns the text of every header cell of the given kind whose extent
// matches, in the order the cells are stored — which [Layout.Order] has made
// reading order.
func (t Table) headers(kind CellKind, match func(Cell) bool) []string {
	var out []string
	for _, o := range t.Cells {
		if o.Kind != kind || !match(o) {
			continue
		}
		if o.Text != "" {
			out = append(out, o.Text)
		}
	}
	return out
}

// overlap reports whether two half-open index ranges intersect.
func overlap(aAt, aLen, bAt, bLen int) bool {
	return aAt < bAt+bLen && bAt < aAt+aLen
}

// join concatenates header texts with a single space. It is not strings.Join
// only so that a nil slice yields an empty string without allocating.
func join(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

// Pair is one key-value region: a label and the value it labels.
//
// A form's "Date of birth: 4 March 1969" is two regions with a relationship the
// provider found, and reducing it to five words in reading order loses the
// relationship — which is the only part of it a schema cares about.
type Pair struct {
	// Page is 1-based.
	Page int

	// Key is the label.
	Key Region

	// Value is what the label labels. Its Text may be empty: a form field the
	// provider found and read nothing in is a fact about the document, not an
	// absent pair.
	Value Region

	// Confidence is the provider's own for the association, on 0..1. It is
	// about the pairing, not about either region's text, and a provider that
	// reports confidence per region and none for the pairing leaves this zero
	// rather than averaging two numbers that mean something else.
	Confidence float64
}

// Box is the region a review interface highlights for the whole pair.
func (p Pair) Box() Rect { return p.Key.Box.Union(p.Value.Box) }

// Layout is the structure a provider recognised on one page.
//
// One Layout per page, matching ovrin.Recognition, so that a provider which
// rasterises server-side and returns one recognition per page returns one of
// these per page too and nothing has to be re-split.
//
// An empty Layout and no Layout are different facts: an empty one is a provider
// that looked and found no structure, and no Layout at all is a provider that
// does not report structure. That is why the field this belongs on is a pointer;
// see the package documentation.
type Layout struct {
	// Tables are in reading order, top to bottom then left to right.
	Tables []Table

	// Pairs are in reading order.
	Pairs []Pair
}

// Ref locates one cell by position alone.
//
// It is the loggable form of a claim about a table. "Page 4, table 1, row 3,
// column 2" says which value is meant without repeating the value, so it can go
// in a provenance entry, an event or a review interface under the rule that
// document content never reaches any of them (docs/rules.md §2.5, §7.5).
//
// Page is 1-based, matching ovrin.Line.Page. Table, Row and Column are 0-based
// indexes, matching [Cell.Row] and [Cell.Column] and the position of a table in
// [Layout.Tables] — they index into data structures, and a second convention
// inside one value is how off-by-one errors are made.
type Ref struct {
	Page   int
	Table  int
	Row    int
	Column int
}

// String renders the reference for a diagnostic. It contains no document
// content, by construction: a Ref has nowhere to put any.
func (r Ref) String() string {
	return "page " + strconv.Itoa(r.Page) + ", table " + strconv.Itoa(r.Table) +
		", row " + strconv.Itoa(r.Row) + ", column " + strconv.Itoa(r.Column)
}

// Ref returns the reference naming a cell of the table at index i.
//
// It takes the index rather than searching for the table, because a Layout can
// hold two tables with identical geometry and searching would silently pick the
// first.
func (l Layout) Ref(i int, c Cell) Ref {
	page := 0
	if i >= 0 && i < len(l.Tables) {
		page = l.Tables[i].Page
	}
	return Ref{Page: page, Table: i, Row: c.Row, Column: c.Column}
}

// At returns the cell a reference names.
//
// The second result is false when the reference names no cell — a table index
// out of range, or a position nothing covers — which is what makes a Ref safe
// to store and resolve later against a Layout that may have been rebuilt.
func (l Layout) At(r Ref) (Cell, bool) {
	if r.Table < 0 || r.Table >= len(l.Tables) {
		return Cell{}, false
	}
	t := l.Tables[r.Table]
	if t.Page != r.Page {
		return Cell{}, false
	}
	return t.At(r.Row, r.Column)
}
