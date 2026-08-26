package ovrin

// Layout is the structure a provider recognised on one page: its tables and
// its key-value pairs.
//
// One Layout per page, matching [Recognition], so a provider that rasterises
// server-side and returns one recognition per page returns one of these per
// page too and nothing has to be re-split.
//
// An empty Layout and no Layout are different facts. An empty one is a provider
// that looked and found no structure; no Layout at all is a provider that does
// not report structure. That is why [Recognition.Layout] is a pointer, and it
// is the whole reason this type exists rather than two slices on Recognition:
// a caller deciding whether to fall back to reading the page as prose needs to
// tell "there are no tables here" from "nobody looked".
//
// Nothing in ovrin requires a provider to fill this in. A table detected and
// reported is a table the model is told about in the page content; a table
// nobody detected is prose, which still extracts, only with less to go on.
type Layout struct {
	// Tables are in reading order, top to bottom then left to right.
	Tables []Table

	// Pairs are in reading order.
	Pairs []Pair
}

// Table is one table a provider found.
type Table struct {
	// Page is 1-based, matching [Line.Page].
	Page int

	// Box is the table's region on the page. A zero Rect means the provider
	// gave no geometry for the table as a whole.
	Box Rect

	// Rows and Columns are the table's declared size, as the provider counted
	// it rather than derived from Cells: a table whose last row is empty still
	// has that row, and deriving the size would silently lose it.
	Rows    int
	Columns int

	// Cells are in reading order, by row then by column.
	//
	// A table with merged cells has fewer entries than Rows*Columns, and a
	// sparse one fewer still. A position no cell covers is a position the
	// provider read nothing at, which is a fact about the document and not a
	// gap to be filled with an empty string.
	Cells []Cell

	// Confidence is the provider's own for the table as a whole, on 0..1.
	Confidence float64
}

// Cell is one cell of a [Table].
type Cell struct {
	// Row is the 0-based index of the cell's first row.
	Row int

	// Column is the 0-based index of the cell's first column.
	Column int

	// RowSpan is how many rows the cell covers. Zero and one both mean one:
	// providers differ on whether they report a span of one, and normalising
	// it here means every adapter does not have to.
	RowSpan int

	// ColumnSpan is how many columns the cell covers, with zero meaning one,
	// as for RowSpan.
	ColumnSpan int

	// Kind is what the provider said the cell is.
	Kind CellKind

	// Text is the cell's content as the provider read it, unnormalised.
	//
	// It is document content. It belongs on a field or in front of a person,
	// never in a log line, an error, an event or a metric (rule §2.5, §7.5).
	// Use [Table.Ref] when you need to say which cell you mean.
	Text string

	// Box is the cell's region on the page. A zero Rect means the provider
	// gave no geometry.
	Box Rect

	// Confidence is the provider's own for this cell, on 0..1.
	Confidence float64
}

// CellKind is what a provider said a cell is.
//
// The set is closed and is the intersection of what the supported providers
// report, so all of them map onto it without loss. A provider that says nothing
// leaves [CellUnknown], which is not [CellData]: "this is a data cell" and
// "this provider does not label cells" are different facts, and collapsing
// them would make a header row silently wrong rather than silently absent.
type CellKind string

// The cell kinds.
const (
	// CellUnknown is a provider that does not label cells.
	CellUnknown CellKind = ""

	// CellData is a cell the provider labelled as content.
	CellData CellKind = "data"

	// CellColumnHeader labels the column it sits above.
	CellColumnHeader CellKind = "column_header"

	// CellRowHeader labels the row it sits beside.
	CellRowHeader CellKind = "row_header"
)

// Header reports whether the cell labels other cells rather than carrying
// content of its own.
func (k CellKind) Header() bool {
	return k == CellColumnHeader || k == CellRowHeader
}

// String returns the kind as written in a struct tag and in documentation.
func (k CellKind) String() string {
	if k == CellUnknown {
		return "unknown"
	}
	return string(k)
}

// Region is a run of recognised text and where it was.
type Region struct {
	// Text is what the provider read, unnormalised. It is document content;
	// see [Cell.Text].
	Text string

	// Box is the region on the page. A zero Rect means no geometry.
	Box Rect

	// Confidence is the provider's own, on 0..1.
	Confidence float64
}

// Pair is a label and the thing it labels — a form field, in effect.
type Pair struct {
	// Page is 1-based.
	Page int

	// Key is the label.
	Key Region

	// Value is what the label labels.
	//
	// Its Text may be empty: a form field the provider found and read nothing
	// in is a fact about the document, not an absent pair. Dropping it would
	// turn "this box was blank" into "there is no such box".
	Value Region

	// Confidence is the provider's own for the association, on 0..1.
	//
	// It is about the pairing and not about either region's text. A provider
	// that reports confidence per region and none for the pairing leaves this
	// zero rather than averaging two numbers that mean something else.
	Confidence float64
}

// Ref locates one cell by position alone.
//
// It is the loggable form of a claim about a table: "page 4, table 1, row 3,
// column 2" says which value is meant without repeating the value, so it can go
// in a provenance entry, an event, or a review interface under the rule that
// document content never reaches any of them (rule §2.5, §7.5).
type Ref struct {
	// Page is 1-based; Table is the index within the page's Layout; Row and
	// Column are 0-based, as on [Cell].
	Page   int
	Table  int
	Row    int
	Column int
}

// Ref returns the position of c within table i of l, for logging or provenance.
//
// It reads nothing from the cell but its coordinates, which is the point.
func (l Layout) Ref(i int, c Cell) Ref {
	r := Ref{Table: i, Row: c.Row, Column: c.Column}
	if i >= 0 && i < len(l.Tables) {
		r.Page = l.Tables[i].Page
	}
	return r
}

// At returns the cell r names, and whether there was one.
//
// A position no cell covers returns false rather than a zero Cell, because a
// sparse table's empty position is a place the provider read nothing and an
// empty Cell would claim it read an empty string. Spans are honoured: a cell
// covering four positions is returned for all four.
func (l Layout) At(r Ref) (Cell, bool) {
	if r.Table < 0 || r.Table >= len(l.Tables) {
		return Cell{}, false
	}
	for _, c := range l.Tables[r.Table].Cells {
		rs, cs := c.RowSpan, c.ColumnSpan
		if rs < 1 {
			rs = 1
		}
		if cs < 1 {
			cs = 1
		}
		if r.Row >= c.Row && r.Row < c.Row+rs &&
			r.Column >= c.Column && r.Column < c.Column+cs {
			return c, true
		}
	}
	return Cell{}, false
}
