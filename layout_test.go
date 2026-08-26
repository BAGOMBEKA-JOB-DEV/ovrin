package ovrin_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

func TestLayoutAt(t *testing.T) {
	t.Parallel()

	// A 3x3 table: a header row, a cell merged across two columns, and one
	// position no cell covers.
	l := ovrin.Layout{Tables: []ovrin.Table{{
		Page: 4, Rows: 3, Columns: 3,
		Cells: []ovrin.Cell{
			{Row: 0, Column: 0, Kind: ovrin.CellColumnHeader, Text: "Item"},
			{Row: 0, Column: 1, ColumnSpan: 2, Kind: ovrin.CellColumnHeader, Text: "Amount"},
			{Row: 1, Column: 0, Text: "Consulting"},
			{Row: 1, Column: 1, Text: "1250.00"},
			// Row 1 column 2, and all of row 2, are absent on purpose.
		},
	}}}

	tests := []struct {
		name     string
		ref      ovrin.Ref
		wantOK   bool
		wantText string
	}{
		{"a cell at its own position", ovrin.Ref{Row: 1, Column: 0}, true, "Consulting"},
		{
			// The merged header covers columns 1 and 2, so asking for either
			// must return it. A caller reading a row cell by cell would
			// otherwise see the header vanish halfway across.
			name: "a merged cell is returned for every position it covers",
			ref:  ovrin.Ref{Row: 0, Column: 2}, wantOK: true, wantText: "Amount",
		},
		{
			// Not a zero Cell: an empty Cell would claim the provider read an
			// empty string, and it read nothing at all.
			name: "a position no cell covers is not found",
			ref:  ovrin.Ref{Row: 2, Column: 0}, wantOK: false,
		},
		{"a table index out of range", ovrin.Ref{Table: 7}, false, ""},
		{"a negative table index", ovrin.Ref{Table: -1}, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := l.At(tc.ref)
			if ok != tc.wantOK {
				t.Fatalf("At(%+v) ok = %v, want %v", tc.ref, ok, tc.wantOK)
			}
			if got.Text != tc.wantText {
				t.Errorf("At(%+v).Text = %q, want %q", tc.ref, got.Text, tc.wantText)
			}
		})
	}
}

// A span of zero and a span of one both mean one cell: providers differ on
// which they report, and an adapter should not have to normalise it.
func TestAZeroSpanCoversOneCell(t *testing.T) {
	t.Parallel()

	l := ovrin.Layout{Tables: []ovrin.Table{{
		Rows: 2, Columns: 2,
		Cells: []ovrin.Cell{{Row: 0, Column: 0, Text: "only"}}, // spans left at zero
	}}}

	if c, ok := l.At(ovrin.Ref{Row: 0, Column: 0}); !ok || c.Text != "only" {
		t.Errorf("At(0,0) = %q, %v; a zero span must cover its own cell", c.Text, ok)
	}
	if _, ok := l.At(ovrin.Ref{Row: 0, Column: 1}); ok {
		t.Error("a zero span covered the next column too")
	}
}

// Ref is the loggable form of a claim about a table: it must carry the
// position and nothing else, so it can go somewhere document content may not
// (docs/rules.md §7.5).
func TestRefCarriesNoDocumentContent(t *testing.T) {
	t.Parallel()

	l := ovrin.Layout{Tables: []ovrin.Table{{Page: 4, Rows: 1, Columns: 1}}}
	cell := ovrin.Cell{Row: 3, Column: 2, Text: "4111111111111111"}

	ref := l.Ref(0, cell)
	want := ovrin.Ref{Page: 4, Table: 0, Row: 3, Column: 2}
	if ref != want {
		t.Errorf("Ref = %+v, want %+v", ref, want)
	}

	// The compiler is the real guarantee — Ref has four int fields and nowhere
	// for a string to sit — but a rendered Ref is what reaches a log, so
	// assert on that too.
	if s := renderRef(ref); contains(s, "4111") {
		t.Errorf("a rendered Ref carried the cell's text: %q", s)
	}
}

func renderRef(r ovrin.Ref) string {
	return "page " + itoa(r.Page) + " table " + itoa(r.Table) +
		" row " + itoa(r.Row) + " column " + itoa(r.Column)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestCellKindHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind ovrin.CellKind
		want bool
		str  string
	}{
		{ovrin.CellColumnHeader, true, "column_header"},
		{ovrin.CellRowHeader, true, "row_header"},
		{ovrin.CellData, false, "data"},
		// Not a header, but not data either: the provider did not say.
		{ovrin.CellUnknown, false, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.str, func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.Header(); got != tc.want {
				t.Errorf("Header() = %v, want %v", got, tc.want)
			}
			if got := tc.kind.String(); got != tc.str {
				t.Errorf("String() = %q, want %q", got, tc.str)
			}
		})
	}
}

// invoiceTable is the table the layout tests work against:
//
//	| Description | Quantity | Unit price |
//	| A4 Paper    |       40 |       3.50 |
//	| Toner       |        2 |      74.00 |
//
// The cells are deliberately not in reading order, so that a test which does
// not call Order sees the provider's order and one that does sees ovrin's.
func invoiceTable() ovrin.Table {
	return ovrin.Table{
		Page: 3, Rows: 3, Columns: 3, Confidence: 0.91,
		Box: ovrin.Rect{MinX: 40, MinY: 300, MaxX: 500, MaxY: 400},
		Cells: []ovrin.Cell{
			{Row: 1, Column: 1, Kind: ovrin.CellData, Text: "40", Box: ovrin.Rect{MinX: 200, MinY: 330, MaxX: 240, MaxY: 345}, Confidence: 0.98},
			{Row: 0, Column: 1, Kind: ovrin.CellColumnHeader, Text: "Quantity", Box: ovrin.Rect{MinX: 200, MinY: 310, MaxX: 260, MaxY: 325}, Confidence: 0.99},
			{Row: 0, Column: 0, Kind: ovrin.CellColumnHeader, Text: "Description", Box: ovrin.Rect{MinX: 40, MinY: 310, MaxX: 190, MaxY: 325}, Confidence: 0.99},
			{Row: 0, Column: 2, Kind: ovrin.CellColumnHeader, Text: "Unit price", Box: ovrin.Rect{MinX: 300, MinY: 310, MaxX: 380, MaxY: 325}, Confidence: 0.99},
			{Row: 1, Column: 0, Kind: ovrin.CellData, Text: "A4 Paper", Box: ovrin.Rect{MinX: 40, MinY: 330, MaxX: 190, MaxY: 345}, Confidence: 0.97},
			{Row: 1, Column: 2, Kind: ovrin.CellData, Text: "3.50", Box: ovrin.Rect{MinX: 300, MinY: 330, MaxX: 380, MaxY: 345}, Confidence: 0.96},
			{Row: 2, Column: 0, Kind: ovrin.CellData, Text: "Toner", Box: ovrin.Rect{MinX: 40, MinY: 350, MaxX: 190, MaxY: 365}, Confidence: 0.95},
			{Row: 2, Column: 1, Kind: ovrin.CellData, Text: "2", Box: ovrin.Rect{MinX: 200, MinY: 350, MaxX: 240, MaxY: 365}, Confidence: 0.99},
			{Row: 2, Column: 2, Kind: ovrin.CellData, Text: "74.00", Box: ovrin.Rect{MinX: 300, MinY: 350, MaxX: 380, MaxY: 365}, Confidence: 0.94},
		},
	}
}

func TestTableAtResolvesSpans(t *testing.T) {
	t.Parallel()

	// | header spanning both columns |
	// | left            | right      |
	// with row 2 left empty entirely.
	table := ovrin.Table{
		Page: 1, Rows: 3, Columns: 2,
		Cells: []ovrin.Cell{
			{Row: 0, Column: 0, ColumnSpan: 2, Kind: ovrin.CellColumnHeader, Text: "Period"},
			{Row: 1, Column: 0, Text: "left"},
			{Row: 1, Column: 1, Text: "right"},
		},
	}

	cases := []struct {
		name        string
		row, column int
		wantOK      bool
		wantText    string
	}{
		{name: "the origin of a spanning cell", row: 0, column: 0, wantOK: true, wantText: "Period"},
		{name: "the far side of a spanning cell", row: 0, column: 1, wantOK: true, wantText: "Period"},
		{name: "an ordinary cell", row: 1, column: 1, wantOK: true, wantText: "right"},
		{name: "a row no cell covers", row: 2, column: 0},
		{name: "a column past the last", row: 1, column: 5},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := table.At(tc.row, tc.column)
			if ok != tc.wantOK {
				t.Fatalf("At(%d, %d) ok = %v, want %v", tc.row, tc.column, ok, tc.wantOK)
			}
			if got.Text != tc.wantText {
				t.Errorf("At(%d, %d).Text = %q, want %q", tc.row, tc.column, got.Text, tc.wantText)
			}
		})
	}
}

// The spans a provider leaves unreported are the ones an adapter author
// forgets, so Rows and Columns are what everything reading a grid must use.
func TestCellSpanOfZeroMeansOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                 string
		cell                 ovrin.Cell
		wantRows, wantCols   int
		coversRow, coversCol int
		wantCovers           bool
	}{
		{name: "unreported spans", cell: ovrin.Cell{}, wantRows: 1, wantCols: 1, wantCovers: true},
		{name: "a reported span of one", cell: ovrin.Cell{RowSpan: 1, ColumnSpan: 1}, wantRows: 1, wantCols: 1, wantCovers: true},
		{
			name: "a negative span is still one cell", cell: ovrin.Cell{RowSpan: -3},
			wantRows: 1, wantCols: 1, wantCovers: true,
		},
		{
			name: "a real span covers its far corner", cell: ovrin.Cell{RowSpan: 2, ColumnSpan: 3},
			wantRows: 2, wantCols: 3, coversRow: 1, coversCol: 2, wantCovers: true,
		},
		{
			name: "and stops there", cell: ovrin.Cell{RowSpan: 2, ColumnSpan: 3},
			wantRows: 2, wantCols: 3, coversRow: 2, coversCol: 0, wantCovers: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cell.Rows(); got != tc.wantRows {
				t.Errorf("Rows() = %d, want %d", got, tc.wantRows)
			}
			if got := tc.cell.Columns(); got != tc.wantCols {
				t.Errorf("Columns() = %d, want %d", got, tc.wantCols)
			}
			if got := tc.cell.Covers(tc.coversRow, tc.coversCol); got != tc.wantCovers {
				t.Errorf("Covers(%d, %d) = %v, want %v", tc.coversRow, tc.coversCol, got, tc.wantCovers)
			}
		})
	}
}

// A label and its value are routinely on different lines, which is what makes
// a pair worth reporting; the region to highlight is therefore both of them.
func TestPairBoxIsTheUnionOfItsRegions(t *testing.T) {
	t.Parallel()

	key := ovrin.Rect{MinX: 40, MinY: 100, MaxX: 120, MaxY: 112}
	value := ovrin.Rect{MinX: 200, MinY: 130, MaxX: 300, MaxY: 142}

	cases := []struct {
		name string
		pair ovrin.Pair
		want ovrin.Rect
	}{
		{
			name: "both regions positioned",
			pair: ovrin.Pair{Key: ovrin.Region{Box: key}, Value: ovrin.Region{Box: value}},
			want: ovrin.Rect{MinX: 40, MinY: 100, MaxX: 300, MaxY: 142},
		},
		{
			// A blank form field is a fact about the document, and its box
			// must not be dragged back to the origin by the empty half.
			name: "a value the provider gave no geometry for",
			pair: ovrin.Pair{Key: ovrin.Region{Box: key}},
			want: key,
		},
		{name: "no geometry at all", pair: ovrin.Pair{}, want: ovrin.Rect{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.pair.Box(); got != tc.want {
				t.Errorf("Box() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOrderPutsEverythingInReadingOrder(t *testing.T) {
	t.Parallel()

	l := ovrin.Layout{
		Tables: []ovrin.Table{
			{Page: 2, Rows: 1, Columns: 1, Box: ovrin.Rect{MinX: 40, MinY: 500, MaxX: 100, MaxY: 520}},
			{Page: 1, Rows: 1, Columns: 1, Box: ovrin.Rect{MinX: 300, MinY: 100, MaxX: 360, MaxY: 120}},
			{Page: 1, Rows: 1, Columns: 1, Box: ovrin.Rect{MinX: 40, MinY: 100, MaxX: 100, MaxY: 120}},
			{Page: 1, Rows: 1, Columns: 1, Box: ovrin.Rect{MinX: 40, MinY: 40, MaxX: 100, MaxY: 60}},
			{Page: 1, Rows: 1, Columns: 1},
		},
		Pairs: []ovrin.Pair{
			{Page: 1, Key: ovrin.Region{Box: ovrin.Rect{MinX: 40, MinY: 400, MaxX: 90, MaxY: 412}}},
			{Page: 1, Key: ovrin.Region{Box: ovrin.Rect{MinX: 40, MinY: 200, MaxX: 90, MaxY: 212}}},
		},
	}
	l.Order()

	want := []ovrin.Rect{
		{MinX: 40, MinY: 40, MaxX: 100, MaxY: 60},
		{MinX: 40, MinY: 100, MaxX: 100, MaxY: 120},
		{MinX: 300, MinY: 100, MaxX: 360, MaxY: 120},
		{}, // no geometry: last on its page, not first
		{MinX: 40, MinY: 500, MaxX: 100, MaxY: 520},
	}
	for i, w := range want {
		if l.Tables[i].Box != w {
			t.Errorf("Tables[%d].Box = %+v, want %+v", i, l.Tables[i].Box, w)
		}
	}
	if l.Tables[3].Page != 1 || l.Tables[4].Page != 2 {
		t.Errorf("page order broken: %d then %d", l.Tables[3].Page, l.Tables[4].Page)
	}
	if l.Pairs[0].Key.Box.MinY != 200 {
		t.Errorf("Pairs[0] is at y=%g, want the higher one first", l.Pairs[0].Key.Box.MinY)
	}
}

func TestOrderSortsCellsAndDerivesAMissingTableBox(t *testing.T) {
	t.Parallel()

	table := invoiceTable()
	table.Box = ovrin.Rect{}
	l := ovrin.Layout{Tables: []ovrin.Table{table}}
	l.Order()

	got := l.Tables[0]
	want := []string{"Description", "Quantity", "Unit price", "A4 Paper", "40", "3.50", "Toner", "2", "74.00"}
	for i, w := range want {
		if got.Cells[i].Text != w {
			t.Fatalf("Cells[%d].Text = %q, want %q", i, got.Cells[i].Text, w)
		}
	}
	// The union of the cells, not a guess: it is exactly the region they
	// occupy, and a table with no box is a table nothing can highlight.
	if wantBox := (ovrin.Rect{MinX: 40, MinY: 310, MaxX: 380, MaxY: 365}); got.Box != wantBox {
		t.Errorf("Box = %+v, want the union of the cells %+v", got.Box, wantBox)
	}
}

func TestOrderLeavesAnAlreadyReportedBoxAlone(t *testing.T) {
	t.Parallel()

	table := invoiceTable()
	l := ovrin.Layout{Tables: []ovrin.Table{table}}
	l.Order()
	if l.Tables[0].Box != table.Box {
		t.Errorf("Box = %+v, want the provider's own %+v", l.Tables[0].Box, table.Box)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		layout ovrin.Layout
		// says is a fragment the message must name, so a case asserts which
		// fault was found and not merely that some error appeared. Nothing in
		// ovrin branches on it; the sentinel is what a program tests.
		says string
	}{
		{name: "a well-formed table", layout: ovrin.Layout{Tables: []ovrin.Table{invoiceTable()}}},
		{
			name: "a sparse table, which is a fact about the document",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 2, Columns: 2,
				Cells: []ovrin.Cell{{Row: 0, Column: 0, Text: "only this one"}},
			}}},
		},
		{
			name: "a table whose last row is empty",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 3, Columns: 1,
				Cells: []ovrin.Cell{{Row: 0, Column: 0}, {Row: 1, Column: 0}},
			}}},
		},
		{
			name: "a page below 1",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 0, Rows: 1, Columns: 1, Cells: []ovrin.Cell{{Row: 0, Column: 0}},
			}}},
			says: "page 0 is not 1-based",
		},
		{
			name: "a cell past the last column",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 1, Columns: 2, Cells: []ovrin.Cell{{Row: 0, Column: 2}},
			}}},
			says: "in a table of 1 by 2",
		},
		{
			name: "a span running off the end",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 2, Columns: 2, Cells: []ovrin.Cell{{Row: 0, Column: 0, RowSpan: 3}},
			}}},
			says: "rows 0..2",
		},
		{
			name: "a negative row",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 2, Columns: 2, Cells: []ovrin.Cell{{Row: -1, Column: 0}},
			}}},
			says: "negative row, column or span",
		},
		{
			// Refused rather than half-checked: the grid is what the overlap
			// check would allocate (rule §5.2).
			name:   "a grid larger than the bound",
			layout: ovrin.Layout{Tables: []ovrin.Table{{Page: 1, Rows: 1 << 20, Columns: 2}}},
			says:   "cells declared, limit",
		},
		{
			// Each dimension is bounded before they are multiplied, or the
			// product overflows into a small positive number and passes.
			name:   "dimensions that would overflow when multiplied",
			layout: ovrin.Layout{Tables: []ovrin.Table{{Page: 1, Rows: math.MaxInt32, Columns: math.MaxInt32}}},
			says:   "cells declared, limit",
		},
		{
			name: "two cells in the same position",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 1, Columns: 2,
				Cells: []ovrin.Cell{{Row: 0, Column: 0}, {Row: 0, Column: 0}},
			}}},
			says: "both cover row 0, column 0",
		},
		{
			name: "a span colliding with the cell beside it",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 1, Columns: 3,
				Cells: []ovrin.Cell{{Row: 0, Column: 0, ColumnSpan: 2}, {Row: 0, Column: 1}},
			}}},
			says: "both cover row 0, column 1",
		},
		{
			name: "a percentage nobody divided by a hundred",
			layout: ovrin.Layout{Tables: []ovrin.Table{{
				Page: 1, Rows: 1, Columns: 1,
				Cells: []ovrin.Cell{{Row: 0, Column: 0, Confidence: 87}},
			}}},
			says: "outside 0..1",
		},
		{
			name:   "a negative table confidence",
			layout: ovrin.Layout{Tables: []ovrin.Table{{Page: 1, Rows: 1, Columns: 1, Confidence: -0.5}}},
			says:   "outside 0..1",
		},
		{
			name:   "a NaN confidence",
			layout: ovrin.Layout{Tables: []ovrin.Table{{Page: 1, Rows: 1, Columns: 1, Confidence: math.NaN()}}},
			says:   "outside 0..1",
		},
		{
			name:   "a pair on no page",
			layout: ovrin.Layout{Pairs: []ovrin.Pair{{Page: 0}}},
			says:   "page 0 is not 1-based",
		},
		{
			name:   "a pair confidence outside 0..1",
			layout: ovrin.Layout{Pairs: []ovrin.Pair{{Page: 1, Confidence: 1.5}}},
			says:   "outside 0..1",
		},
		{
			name:   "a key region confidence outside 0..1",
			layout: ovrin.Layout{Pairs: []ovrin.Pair{{Page: 1, Key: ovrin.Region{Confidence: -1}}}},
			says:   "outside 0..1",
		},
		{
			name: "a well-formed pair",
			layout: ovrin.Layout{Pairs: []ovrin.Pair{{
				Page:  1,
				Key:   ovrin.Region{Text: "Date of birth", Confidence: 0.9},
				Value: ovrin.Region{Text: "4 March 1969", Confidence: 0.8},
				// A pair whose halves are read confidently and whose
				// association the provider did not score.
				Confidence: 0,
			}}},
		},
		{
			// An empty Layout is a provider that looked and found nothing, and
			// there is nothing incoherent about that.
			name:   "a provider that looked and found nothing",
			layout: ovrin.Layout{},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.layout.Check()
			if tc.says == "" {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check() = nil, want an error naming %q", tc.says)
			}
			// An incoherent layout is a response nothing can be done with.
			if !errors.Is(err, ovrin.ErrBadResponse) {
				t.Errorf("Check() = %v, want it to be ErrBadResponse", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("Check() = %q, want it to name %q", err, tc.says)
			}
		})
	}
}

// A Check error is a log line in five systems, so it names positions and never
// what a cell said (docs/rules.md §2.5, §7.5).
func TestCheckErrorsCarryNoDocumentContent(t *testing.T) {
	t.Parallel()

	const secret = "MR-ABEBE-BIKILA-9928311"
	layouts := []ovrin.Layout{
		{Tables: []ovrin.Table{{Page: 0, Rows: 1, Columns: 1, Cells: []ovrin.Cell{{Text: secret}}}}},
		{Tables: []ovrin.Table{{Page: 1, Rows: 1, Columns: 1, Cells: []ovrin.Cell{{Row: 4, Text: secret}}}}},
		{Tables: []ovrin.Table{{Page: 1, Rows: 1, Columns: 1, Cells: []ovrin.Cell{{Text: secret}, {Text: secret}}}}},
		{Tables: []ovrin.Table{{Page: 1, Rows: 1, Columns: 1, Cells: []ovrin.Cell{{Text: secret, Confidence: 87}}}}},
		{Pairs: []ovrin.Pair{{Page: 1, Key: ovrin.Region{Text: secret, Confidence: 9}}}},
	}
	for i, l := range layouts {
		err := l.Check()
		if err == nil {
			t.Fatalf("layout %d: Check() = nil, want an error", i)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("layout %d: the error carries a cell's text: %q", i, err)
		}
	}
}

// Ref exists to be logged, so it must render as a position rather than as four
// integers in braces.
func TestRefString(t *testing.T) {
	t.Parallel()

	l := ovrin.Layout{Tables: []ovrin.Table{{Page: 4, Rows: 4, Columns: 3}}}
	got := l.Ref(0, ovrin.Cell{Row: 3, Column: 2, Text: "4111111111111111"}).String()

	const want = "page 4, table 0, row 3, column 2"
	if got != want {
		t.Errorf("Ref.String() = %q, want %q", got, want)
	}
	if strings.Contains(got, "4111") {
		t.Errorf("a rendered Ref carried the cell's text: %q", got)
	}
}
