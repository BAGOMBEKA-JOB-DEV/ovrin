package ovrin

import (
	"fmt"
	"sort"
	"strconv"
)

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

// At returns the cell covering a grid position within the table, spans
// included, and whether there was one.
//
// The second result is false when nothing covers the position, which is the
// honest answer for a sparse table: an empty [Cell] would be indistinguishable
// from a cell the provider read as empty, and the two are different facts
// about the document.
//
// It is a linear scan of Cells rather than a materialised grid, because a
// table's declared size is the provider's number and building a grid from it
// would allocate for a table declaring two billion rows. [Layout.Check] is
// where that number is bounded before anything is allocated from it.
func (t Table) At(row, column int) (Cell, bool) {
	for _, c := range t.Cells {
		if c.Covers(row, column) {
			return c, true
		}
	}
	return Cell{}, false
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

// Rows returns how many rows the cell covers, treating an unreported span of
// zero as one.
//
// It exists so that the "zero and one both mean one" rule [Cell.RowSpan]
// states is applied in one place. Every caller that walks a merged table needs
// it, and a caller that reads RowSpan directly gets a table one row short
// wherever a provider left the span unreported.
func (c Cell) Rows() int { return cellSpan(c.RowSpan) }

// Columns returns how many columns the cell covers, treating an unreported
// span of zero as one, as for [Cell.Rows].
func (c Cell) Columns() int { return cellSpan(c.ColumnSpan) }

// Covers reports whether the cell occupies a grid position, spans included.
//
// It is the operation every lookup in a merged table needs: the value under
// "Quantity" may be stored at row 3 and asked for at row 4 because the row
// above it spans two, and a caller comparing Row and Column directly would
// conclude the position is empty.
func (c Cell) Covers(row, column int) bool {
	return row >= c.Row && row < c.Row+c.Rows() &&
		column >= c.Column && column < c.Column+c.Columns()
}

// cellSpan normalises a reported span, where zero and one both mean one.
func cellSpan(n int) int {
	if n < 1 {
		return 1
	}
	return n
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

// Box is the region a review interface highlights for the whole pair.
//
// A label and the value it labels are routinely on different lines — that
// separation is what makes a pair worth reporting rather than five words in
// reading order — so the pair's region is the union of the two and not either
// one of them.
func (p Pair) Box() Rect { return rectUnion(p.Key.Box, p.Value.Box) }

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
	return l.Tables[r.Table].At(r.Row, r.Column)
}

// String renders the reference for a diagnostic.
//
// It exists so that a Ref reaching a log line reads as a position rather than
// as a struct of four integers, and it is safe to call anywhere document
// content may not go: a Ref has nowhere to put any (rule §2.5, §7.5).
func (r Ref) String() string {
	return "page " + strconv.Itoa(r.Page) + ", table " + strconv.Itoa(r.Table) +
		", row " + strconv.Itoa(r.Row) + ", column " + strconv.Itoa(r.Column)
}

// Order puts a provider's output into the order the rest of ovrin assumes, and
// fills in the boxes a provider left empty.
//
// It exists so that "cells are in reading order" is something an adapter
// achieves by calling one function rather than something each adapter
// reimplements — three implementations of reading order is three orders, and
// the difference only shows up on the documents nobody tested with. It is the
// counterpart of [Layout.Check]: Check says whether the structure is coherent,
// Order says how it is arranged.
//
// The sorts are stable, so a provider that already emits a defensible order
// keeps it wherever this has no opinion. Order mutates the receiver's slices in
// place and does not copy: a Layout is built once by an adapter and handed on,
// and copying every cell to sort it would be the largest allocation on the
// path for no benefit.
func (l *Layout) Order() {
	for i := range l.Tables {
		l.Tables[i].order()
	}
	sort.SliceStable(l.Tables, func(i, j int) bool {
		return beforeOnPage(l.Tables[i].Page, l.Tables[i].Box, l.Tables[j].Page, l.Tables[j].Box)
	})
	sort.SliceStable(l.Pairs, func(i, j int) bool {
		return beforeOnPage(l.Pairs[i].Page, l.Pairs[i].Box(), l.Pairs[j].Page, l.Pairs[j].Box())
	})
}

// order sorts one table's cells and fills its box.
func (t *Table) order() {
	sort.SliceStable(t.Cells, func(i, j int) bool {
		if t.Cells[i].Row != t.Cells[j].Row {
			return t.Cells[i].Row < t.Cells[j].Row
		}
		return t.Cells[i].Column < t.Cells[j].Column
	})
	if rectIsZero(t.Box) {
		// Derived rather than assumed. A provider that reports cell geometry
		// but no table geometry — and they exist — would otherwise produce a
		// table nothing could highlight, and the union of its cells is not a
		// guess: it is exactly the region the cells occupy.
		var box Rect
		for _, c := range t.Cells {
			box = rectUnion(box, c.Box)
		}
		t.Box = box
	}
}

// beforeOnPage is reading order for two page regions: earlier page first, then
// higher on the page, then further left.
//
// It is a simple top-to-bottom, left-to-right rule and deliberately not the
// recursive cut that normalisation runs over words. A table is already a
// two-dimensional object with its own internal order, so the only question here
// is which of two tables comes first, and a page with two tables side by side
// is rare enough that the extra machinery would cost more than it settles. A
// region with no geometry sorts last, because it has nothing to compare and
// putting it first would move it ahead of tables whose position is known.
func beforeOnPage(pageA int, a Rect, pageB int, b Rect) bool {
	if pageA != pageB {
		return pageA < pageB
	}
	if rectIsZero(a) != rectIsZero(b) {
		return rectIsZero(b)
	}
	if rectIsZero(a) {
		return false
	}
	if a.MinY != b.MinY {
		return a.MinY < b.MinY
	}
	return a.MinX < b.MinX
}

// rectIsZero reports whether a rectangle carries no geometry, which is how a
// provider that reports structure without positions describes every box it
// produces. Zero means unknown, never "not on the page".
func rectIsZero(r Rect) bool {
	return r.MinX == 0 && r.MinY == 0 && r.MaxX == 0 && r.MaxY == 0
}

// rectUnion returns the smallest rectangle containing both.
//
// A zero operand is ignored rather than included, so that a cell the provider
// gave no geometry for does not drag the table's box back to the origin and
// produce a highlight covering the top-left quarter of the page.
func rectUnion(r, o Rect) Rect {
	if rectIsZero(r) {
		return o
	}
	if rectIsZero(o) {
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

// maxLayoutGrid bounds the grid [Layout.Check] will materialise.
//
// The overlap check needs to know which positions are taken, and a hostile or
// broken provider response declaring a table of two billion rows would
// otherwise allocate for it. Above the bound the overlap check is not run and
// the declared size is reported instead, so the response is refused rather
// than half-checked. Every limit has a finite default, checked before
// allocation (rule §5.2, ADR-0020).
const maxLayoutGrid = 1 << 20

// Check reports the first structural mistake in the layout, or nil.
//
// It is what an adapter's own tests run over the layouts they build from
// recorded provider responses, and it is the reason those tests can be shared:
// "the cells are inside the table, nothing overlaps, and every confidence is a
// probability" is the same requirement whichever provider produced them. An
// adapter that mapped a percentage without dividing it, or flattened a merged
// cell into two overlapping ones, fails here rather than in a confidence score
// nobody can explain six weeks later.
//
// Every error wraps [ErrBadResponse], because an incoherent layout is a
// response nothing can be done with. It is deliberately not three new
// sentinels of its own: ovrin has one error vocabulary and adding to it for
// one subsystem is how a caller ends up with two
// (ADR-0027). The message names which check failed and where.
//
// The errors name page, table and cell indexes and nothing else. A cell's text
// is document content and never appears in one (rule §2.5, §7.5).
func (l Layout) Check() error {
	for i, t := range l.Tables {
		if err := t.check(); err != nil {
			return fmt.Errorf("table %d: %w", i, err)
		}
	}
	for i, p := range l.Pairs {
		if p.Page < 1 {
			return fmt.Errorf("%w: pair %d: page %d is not 1-based", ErrBadResponse, i, p.Page)
		}
		for _, c := range []struct {
			what string
			conf float64
		}{{"key", p.Key.Confidence}, {"value", p.Value.Confidence}, {"pair", p.Confidence}} {
			if err := checkConfidence(c.conf); err != nil {
				return fmt.Errorf("pair %d %s: %w", i, c.what, err)
			}
		}
	}
	return nil
}

// check reports the first structural mistake in one table.
func (t Table) check() error {
	if t.Page < 1 {
		return fmt.Errorf("%w: page %d is not 1-based", ErrBadResponse, t.Page)
	}
	if t.Rows < 0 || t.Columns < 0 {
		return fmt.Errorf("%w: %d rows by %d columns", ErrBadResponse, t.Rows, t.Columns)
	}
	if err := checkConfidence(t.Confidence); err != nil {
		return err
	}

	for i, c := range t.Cells {
		if c.Row < 0 || c.Column < 0 || c.RowSpan < 0 || c.ColumnSpan < 0 {
			return fmt.Errorf("%w: cell %d: negative row, column or span", ErrBadResponse, i)
		}
		if c.Row+c.Rows() > t.Rows || c.Column+c.Columns() > t.Columns {
			return fmt.Errorf("%w: cell %d: rows %d..%d and columns %d..%d in a table of %d by %d",
				ErrBadResponse, i, c.Row, c.Row+c.Rows()-1, c.Column, c.Column+c.Columns()-1, t.Rows, t.Columns)
		}
		if err := checkConfidence(c.Confidence); err != nil {
			return fmt.Errorf("cell %d: %w", i, err)
		}
	}

	// Each dimension is bounded before they are multiplied: two large ints
	// multiplied overflow into a small positive one, and the bound would then
	// pass on exactly the table it exists to refuse.
	if t.Rows > maxLayoutGrid || t.Columns > maxLayoutGrid || t.Rows*t.Columns > maxLayoutGrid {
		return fmt.Errorf("%w: %d cells declared, limit %d", ErrBadResponse, t.Rows*t.Columns, maxLayoutGrid)
	}
	// Allocated only after the declared size has been checked against the
	// bound, never before (rule §5.2).
	taken := make([]int, t.Rows*t.Columns)
	for i := range taken {
		taken[i] = -1
	}
	for i, c := range t.Cells {
		for r := c.Row; r < c.Row+c.Rows(); r++ {
			for col := c.Column; col < c.Column+c.Columns(); col++ {
				at := r*t.Columns + col
				if taken[at] >= 0 {
					// Two cells at one position is the mistake a
					// span-flattening adapter makes, and it matters because
					// [Table.At] would then return whichever of them happened
					// to be stored first.
					return fmt.Errorf("%w: cells %d and %d both cover row %d, column %d",
						ErrBadResponse, taken[at], i, r, col)
				}
				taken[at] = i
			}
		}
	}
	return nil
}

// checkConfidence reports whether a value is a probability.
//
// A confidence outside 0..1 is almost always a provider reporting a percentage
// that nobody divided by a hundred, and it is checked because a confidence of
// 87 would otherwise pass into a score and make every other signal irrelevant.
//
// NaN fails, which the comparison gives for free: a NaN is neither below zero
// nor above one, so it is caught by requiring that the value be within range
// rather than by rejecting what is outside it.
func checkConfidence(v float64) error {
	if v >= 0 && v <= 1 {
		return nil
	}
	return fmt.Errorf("%w: confidence %g is outside 0..1", ErrBadResponse, v)
}
