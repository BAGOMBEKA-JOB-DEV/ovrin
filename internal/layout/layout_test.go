package layout

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// invoiceTable is the table every lookup test works against, and is the shape
// the package documentation describes:
//
//	| Description | Quantity | Unit price |
//	| A4 Paper    |       40 |       3.50 |
//	| Toner       |        2 |      74.00 |
//
// The rows are deliberately not in reading order in Cells, so that a test that
// does not call Order sees the provider's order and one that does sees ovrin's.
func invoiceTable() Table {
	return Table{
		Page: 3, Rows: 3, Columns: 3, Confidence: 0.91,
		Box: Rect{MinX: 40, MinY: 300, MaxX: 500, MaxY: 400},
		Cells: []Cell{
			{Row: 1, Column: 1, Text: "40", Box: Rect{MinX: 200, MinY: 330, MaxX: 240, MaxY: 345}, Confidence: 0.98},
			{Row: 0, Column: 1, Kind: CellColumnHeader, Text: "Quantity", Box: Rect{MinX: 200, MinY: 310, MaxX: 260, MaxY: 325}, Confidence: 0.99},
			{Row: 0, Column: 0, Kind: CellColumnHeader, Text: "Description", Box: Rect{MinX: 40, MinY: 310, MaxX: 190, MaxY: 325}, Confidence: 0.99},
			{Row: 0, Column: 2, Kind: CellColumnHeader, Text: "Unit price", Box: Rect{MinX: 300, MinY: 310, MaxX: 380, MaxY: 325}, Confidence: 0.99},
			{Row: 1, Column: 0, Kind: CellData, Text: "A4 Paper", Box: Rect{MinX: 40, MinY: 330, MaxX: 190, MaxY: 345}, Confidence: 0.97},
			{Row: 1, Column: 2, Kind: CellData, Text: "3.50", Box: Rect{MinX: 300, MinY: 330, MaxX: 380, MaxY: 345}, Confidence: 0.96},
			{Row: 2, Column: 0, Kind: CellData, Text: "Toner", Box: Rect{MinX: 40, MinY: 350, MaxX: 190, MaxY: 365}, Confidence: 0.95},
			{Row: 2, Column: 1, Kind: CellData, Text: "2", Box: Rect{MinX: 200, MinY: 350, MaxX: 240, MaxY: 365}, Confidence: 0.99},
			{Row: 2, Column: 2, Kind: CellData, Text: "74.00", Box: Rect{MinX: 300, MinY: 350, MaxX: 380, MaxY: 365}, Confidence: 0.94},
		},
	}
}

// The claim the package exists to make expressible: not "the 40 somewhere on
// the page" but "the 40 in the Quantity column of the A4 Paper row".
func TestACellIsAddressableByItsRowAndColumn(t *testing.T) {
	t.Parallel()

	table := invoiceTable()

	cell, ok := table.At(1, 1)
	if !ok {
		t.Fatal("At(1, 1) found nothing")
	}
	if cell.Text != "40" {
		t.Fatalf("At(1, 1).Text = %q, want %q", cell.Text, "40")
	}

	if got := table.Heading(cell).Column; got != "Quantity" {
		t.Errorf("Heading().Column = %q, want %q", got, "Quantity")
	}

	// The provider marked no row headers, so the row heading is empty rather
	// than guessed from the leftmost cell. The caller who wants that asks for
	// it themselves.
	if got := table.Heading(cell).Row; got != "" {
		t.Errorf("Heading().Row = %q, want empty: no cell is a row header", got)
	}
	label, ok := table.At(cell.Row, 0)
	if !ok || label.Text != "A4 Paper" {
		t.Errorf("At(%d, 0).Text = %q, want %q", cell.Row, label.Text, "A4 Paper")
	}

	// And the same claim in the form that can be logged: a position, with no
	// document content in it at all.
	l := Layout{Tables: []Table{table}}
	ref := l.Ref(0, cell)
	if got := ref.String(); got != "page 3, table 0, row 1, column 1" {
		t.Errorf("Ref.String() = %q", got)
	}
	for _, secret := range []string{"40", "A4 Paper", "Quantity"} {
		if strings.Contains(ref.String(), secret) {
			t.Errorf("Ref.String() carries document content: %q", ref.String())
		}
	}

	back, ok := l.At(ref)
	if !ok || back.Text != "40" {
		t.Errorf("Layout.At(%v) = %q, %v; want the cell back", ref, back.Text, ok)
	}
}

func TestAtResolvesSpans(t *testing.T) {
	t.Parallel()

	// A header spanning two columns above two data cells, which is the merged
	// shape every provider reports differently and this package does not.
	table := Table{
		Page: 1, Rows: 2, Columns: 3,
		Cells: []Cell{
			{Row: 0, Column: 0, ColumnSpan: 2, Kind: CellColumnHeader, Text: "Amount"},
			{Row: 0, Column: 2, Kind: CellColumnHeader, Text: "Notes"},
			{Row: 1, Column: 0, RowSpan: 1, Text: "net"},
			{Row: 1, Column: 1, Text: "gross"},
			{Row: 1, Column: 2, Text: "-"},
		},
	}

	cases := []struct {
		name       string
		row, col   int
		wantText   string
		wantFound  bool
		wantColumn string
	}{
		{name: "the spanning header at its own position", row: 0, col: 0, wantText: "Amount", wantFound: true, wantColumn: "Amount"},
		{name: "the spanning header at its second column", row: 0, col: 1, wantText: "Amount", wantFound: true, wantColumn: "Amount"},
		{name: "a cell under the first half of the span", row: 1, col: 0, wantText: "net", wantFound: true, wantColumn: "Amount"},
		{name: "a cell under the second half of the span", row: 1, col: 1, wantText: "gross", wantFound: true, wantColumn: "Amount"},
		{name: "a cell under an unspanned header", row: 1, col: 2, wantText: "-", wantFound: true, wantColumn: "Notes"},
		{name: "a position outside the table", row: 5, col: 5, wantFound: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cell, ok := table.At(tc.row, tc.col)
			if ok != tc.wantFound {
				t.Fatalf("At(%d, %d) found = %v, want %v", tc.row, tc.col, ok, tc.wantFound)
			}
			if !ok {
				return
			}
			if cell.Text != tc.wantText {
				t.Errorf("At(%d, %d).Text = %q, want %q", tc.row, tc.col, cell.Text, tc.wantText)
			}
			if got := table.Heading(cell).Column; got != tc.wantColumn {
				t.Errorf("Heading().Column = %q, want %q", got, tc.wantColumn)
			}
		})
	}
}

func TestHeadingJoinsAStackedColumnHeader(t *testing.T) {
	t.Parallel()

	// "Unit" over "Price" is one column heading. Reporting only one of them
	// would name the column wrongly, which is worse than naming it not at all.
	table := Table{
		Page: 1, Rows: 3, Columns: 1,
		Cells: []Cell{
			{Row: 0, Column: 0, Kind: CellColumnHeader, Text: "Unit"},
			{Row: 1, Column: 0, Kind: CellColumnHeader, Text: "Price"},
			{Row: 2, Column: 0, Kind: CellData, Text: "3.50"},
		},
	}
	cell, ok := table.At(2, 0)
	if !ok {
		t.Fatal("At(2, 0) found nothing")
	}
	if got := table.Heading(cell).Column; got != "Unit Price" {
		t.Errorf("Heading().Column = %q, want %q", got, "Unit Price")
	}
}

func TestHeadingReportsAMarkedRowHeader(t *testing.T) {
	t.Parallel()

	table := Table{
		Page: 1, Rows: 1, Columns: 2,
		Cells: []Cell{
			{Row: 0, Column: 0, Kind: CellRowHeader, Text: "Date of birth"},
			{Row: 0, Column: 1, Kind: CellData, Text: "4 March 1969"},
		},
	}
	cell, _ := table.At(0, 1)
	if got := table.Heading(cell).Row; got != "Date of birth" {
		t.Errorf("Heading().Row = %q, want %q", got, "Date of birth")
	}
}

func TestRowAndColumnReturnTheCellsThatStartThere(t *testing.T) {
	t.Parallel()

	table := invoiceTable()
	l := Layout{Tables: []Table{table}}
	l.Order()
	table = l.Tables[0]

	if got := len(table.Row(1)); got != 3 {
		t.Errorf("len(Row(1)) = %d, want 3", got)
	}
	if got := table.Row(1)[0].Text; got != "A4 Paper" {
		t.Errorf("Row(1)[0].Text = %q, want the leftmost cell", got)
	}
	if got := len(table.Column(1)); got != 3 {
		t.Errorf("len(Column(1)) = %d, want 3", got)
	}
	if got := table.Column(1)[0].Text; got != "Quantity" {
		t.Errorf("Column(1)[0].Text = %q, want the topmost cell", got)
	}
}

func TestCellSpanOfZeroMeansOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		cell         Cell
		rows, cols   int
		covers       [2]int
		doesNotCover [2]int
	}{
		{name: "an unreported span", cell: Cell{Row: 2, Column: 3}, rows: 1, cols: 1, covers: [2]int{2, 3}, doesNotCover: [2]int{3, 3}},
		{name: "a span of one", cell: Cell{Row: 2, Column: 3, RowSpan: 1, ColumnSpan: 1}, rows: 1, cols: 1, covers: [2]int{2, 3}, doesNotCover: [2]int{2, 4}},
		{name: "a span of two rows", cell: Cell{Row: 2, Column: 3, RowSpan: 2}, rows: 2, cols: 1, covers: [2]int{3, 3}, doesNotCover: [2]int{4, 3}},
		{name: "a negative span", cell: Cell{Row: 2, Column: 3, RowSpan: -4}, rows: 1, cols: 1, covers: [2]int{2, 3}, doesNotCover: [2]int{1, 3}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cell.Rows(); got != tc.rows {
				t.Errorf("Rows() = %d, want %d", got, tc.rows)
			}
			if got := tc.cell.Columns(); got != tc.cols {
				t.Errorf("Columns() = %d, want %d", got, tc.cols)
			}
			if !tc.cell.Covers(tc.covers[0], tc.covers[1]) {
				t.Errorf("Covers(%d, %d) = false, want true", tc.covers[0], tc.covers[1])
			}
			if tc.cell.Covers(tc.doesNotCover[0], tc.doesNotCover[1]) {
				t.Errorf("Covers(%d, %d) = true, want false", tc.doesNotCover[0], tc.doesNotCover[1])
			}
		})
	}
}

// -- pairs -----------------------------------------------------------------

func TestPairBoxIsTheUnionOfItsRegions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pair Pair
		want Rect
	}{
		{
			name: "two positioned regions",
			pair: Pair{
				Key:   Region{Text: "Date of birth", Box: Rect{MinX: 40, MinY: 100, MaxX: 140, MaxY: 112}},
				Value: Region{Text: "4 March 1969", Box: Rect{MinX: 160, MinY: 98, MaxX: 260, MaxY: 114}},
			},
			want: Rect{MinX: 40, MinY: 98, MaxX: 260, MaxY: 114},
		},
		{
			name: "a value the provider gave no geometry for",
			pair: Pair{
				Key:   Region{Text: "Date of birth", Box: Rect{MinX: 40, MinY: 100, MaxX: 140, MaxY: 112}},
				Value: Region{Text: "4 March 1969"},
			},
			want: Rect{MinX: 40, MinY: 100, MaxX: 140, MaxY: 112},
		},
		{
			name: "no geometry at all",
			pair: Pair{Key: Region{Text: "Date of birth"}, Value: Region{Text: "4 March 1969"}},
			want: Rect{},
		},
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

// -- order -----------------------------------------------------------------

func TestOrderPutsEverythingInReadingOrder(t *testing.T) {
	t.Parallel()

	l := Layout{
		Tables: []Table{
			{Page: 2, Rows: 1, Columns: 1, Box: Rect{MinX: 40, MinY: 500, MaxX: 100, MaxY: 520}},
			{Page: 1, Rows: 1, Columns: 1, Box: Rect{MinX: 300, MinY: 100, MaxX: 360, MaxY: 120}},
			{Page: 1, Rows: 1, Columns: 1, Box: Rect{MinX: 40, MinY: 100, MaxX: 100, MaxY: 120}},
			{Page: 1, Rows: 1, Columns: 1, Box: Rect{MinX: 40, MinY: 40, MaxX: 100, MaxY: 60}},
			{Page: 1, Rows: 1, Columns: 1},
		},
		Pairs: []Pair{
			{Page: 1, Key: Region{Box: Rect{MinX: 40, MinY: 400, MaxX: 90, MaxY: 412}}},
			{Page: 1, Key: Region{Box: Rect{MinX: 40, MinY: 200, MaxX: 90, MaxY: 212}}},
		},
	}
	l.Order()

	wantTables := []Rect{
		{MinX: 40, MinY: 40, MaxX: 100, MaxY: 60},
		{MinX: 40, MinY: 100, MaxX: 100, MaxY: 120},
		{MinX: 300, MinY: 100, MaxX: 360, MaxY: 120},
		{}, // no geometry: last, not first
		{MinX: 40, MinY: 500, MaxX: 100, MaxY: 520},
	}
	for i, want := range wantTables {
		if l.Tables[i].Box != want {
			t.Errorf("Tables[%d].Box = %+v, want %+v", i, l.Tables[i].Box, want)
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
	table.Box = Rect{}
	l := Layout{Tables: []Table{table}}
	l.Order()

	got := l.Tables[0]
	want := []string{"Description", "Quantity", "Unit price", "A4 Paper", "40", "3.50", "Toner", "2", "74.00"}
	for i, w := range want {
		if got.Cells[i].Text != w {
			t.Fatalf("Cells[%d].Text = %q, want %q", i, got.Cells[i].Text, w)
		}
	}
	if wantBox := (Rect{MinX: 40, MinY: 310, MaxX: 380, MaxY: 365}); got.Box != wantBox {
		t.Errorf("Box = %+v, want the union of the cells %+v", got.Box, wantBox)
	}
}

func TestOrderLeavesAnAlreadyReportedBoxAlone(t *testing.T) {
	t.Parallel()

	table := invoiceTable()
	l := Layout{Tables: []Table{table}}
	l.Order()
	if l.Tables[0].Box != table.Box {
		t.Errorf("Box = %+v, want the provider's %+v", l.Tables[0].Box, table.Box)
	}
}

// -- check -----------------------------------------------------------------

func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		layout Layout
		want   error
	}{
		{
			name:   "a well-formed table",
			layout: Layout{Tables: []Table{invoiceTable()}},
		},
		{
			name: "a sparse table, which is a fact about the document",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 2, Columns: 2,
				Cells: []Cell{{Row: 0, Column: 0, Text: "only this one"}},
			}}},
		},
		{
			name: "a table with an empty last row",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 3, Columns: 1,
				Cells: []Cell{{Row: 0, Column: 0}, {Row: 1, Column: 0}},
			}}},
		},
		{
			name: "a page below 1",
			layout: Layout{Tables: []Table{{
				Page: 0, Rows: 1, Columns: 1, Cells: []Cell{{Row: 0, Column: 0}},
			}}},
			want: ErrRange,
		},
		{
			name: "a cell past the last column",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 2, Cells: []Cell{{Row: 0, Column: 2}},
			}}},
			want: ErrRange,
		},
		{
			name: "a span running off the end",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 2, Columns: 2, Cells: []Cell{{Row: 0, Column: 0, RowSpan: 3}},
			}}},
			want: ErrRange,
		},
		{
			name: "a negative row",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 2, Columns: 2, Cells: []Cell{{Row: -1, Column: 0}},
			}}},
			want: ErrRange,
		},
		{
			name: "a grid larger than the bound",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: maxGrid, Columns: 2,
			}}},
			want: ErrRange,
		},
		{
			name: "a grid whose dimensions would overflow when multiplied",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: math.MaxInt32, Columns: math.MaxInt32,
			}}},
			want: ErrRange,
		},
		{
			name: "two cells in the same position",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 2,
				Cells: []Cell{{Row: 0, Column: 0}, {Row: 0, Column: 0}},
			}}},
			want: ErrOverlap,
		},
		{
			name: "a span colliding with the cell beside it",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 3,
				Cells: []Cell{{Row: 0, Column: 0, ColumnSpan: 2}, {Row: 0, Column: 1}},
			}}},
			want: ErrOverlap,
		},
		{
			name: "a percentage nobody divided by a hundred",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 1,
				Cells: []Cell{{Row: 0, Column: 0, Confidence: 87}},
			}}},
			want: ErrConfidence,
		},
		{
			name: "a negative table confidence",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 1, Confidence: -0.5,
			}}},
			want: ErrConfidence,
		},
		{
			name: "a NaN confidence",
			layout: Layout{Tables: []Table{{
				Page: 1, Rows: 1, Columns: 1, Confidence: math.NaN(),
			}}},
			want: ErrConfidence,
		},
		{
			name:   "a pair on no page",
			layout: Layout{Pairs: []Pair{{Page: 0}}},
			want:   ErrRange,
		},
		{
			name:   "a pair confidence outside 0..1",
			layout: Layout{Pairs: []Pair{{Page: 1, Confidence: 1.5}}},
			want:   ErrConfidence,
		},
		{
			name:   "a key region confidence outside 0..1",
			layout: Layout{Pairs: []Pair{{Page: 1, Key: Region{Confidence: -1}}}},
			want:   ErrConfidence,
		},
		{
			name:   "a well-formed pair",
			layout: Layout{Pairs: []Pair{{Page: 1, Key: Region{Text: "k", Confidence: 0.9}, Value: Region{Text: "v", Confidence: 0.8}, Confidence: 0.7}}},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.layout.Check()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckErrorsCarryNoDocumentContent(t *testing.T) {
	t.Parallel()

	const secret = "MR-ABEBE-BIKILA-9928311"
	layouts := []Layout{
		{Tables: []Table{{Page: 0, Rows: 1, Columns: 1, Cells: []Cell{{Text: secret}}}}},
		{Tables: []Table{{Page: 1, Rows: 1, Columns: 1, Cells: []Cell{{Row: 4, Text: secret}}}}},
		{Tables: []Table{{Page: 1, Rows: 1, Columns: 1, Cells: []Cell{{Text: secret}, {Text: secret}}}}},
		{Tables: []Table{{Page: 1, Rows: 1, Columns: 1, Cells: []Cell{{Text: secret, Confidence: 87}}}}},
		{Pairs: []Pair{{Page: 1, Key: Region{Text: secret, Confidence: 9}}}},
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

// -- geometry --------------------------------------------------------------

func TestRectUnionIgnoresAZeroOperand(t *testing.T) {
	t.Parallel()

	a := Rect{MinX: 10, MinY: 20, MaxX: 30, MaxY: 40}

	cases := []struct {
		name string
		l, r Rect
		want Rect
	}{
		{name: "a zero left operand", l: Rect{}, r: a, want: a},
		{name: "a zero right operand", l: a, r: Rect{}, want: a},
		{name: "both zero", l: Rect{}, r: Rect{}, want: Rect{}},
		{name: "disjoint boxes", l: a, r: Rect{MinX: 100, MinY: 5, MaxX: 120, MaxY: 15},
			want: Rect{MinX: 10, MinY: 5, MaxX: 120, MaxY: 40}},
		{name: "a contained box", l: a, r: Rect{MinX: 15, MinY: 25, MaxX: 20, MaxY: 30}, want: a},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.l.Union(tc.r); got != tc.want {
				t.Errorf("Union() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCellKindString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		kind       CellKind
		want       string
		wantHeader bool
	}{
		{name: "the zero value", kind: CellUnknown, want: "unknown"},
		{name: "data", kind: CellData, want: "data"},
		{name: "a column header", kind: CellColumnHeader, want: "column_header", wantHeader: true},
		{name: "a row header", kind: CellRowHeader, want: "row_header", wantHeader: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			if got := tc.kind.Header(); got != tc.wantHeader {
				t.Errorf("Header() = %v, want %v", got, tc.wantHeader)
			}
		})
	}
}

func TestLayoutAtRejectsAReferenceThatNamesNothing(t *testing.T) {
	t.Parallel()

	l := Layout{Tables: []Table{invoiceTable()}}

	cases := []struct {
		name string
		ref  Ref
	}{
		{name: "a table index past the end", ref: Ref{Page: 3, Table: 4, Row: 0, Column: 0}},
		{name: "a negative table index", ref: Ref{Page: 3, Table: -1}},
		{name: "the wrong page", ref: Ref{Page: 1, Table: 0, Row: 1, Column: 1}},
		{name: "a position nothing covers", ref: Ref{Page: 3, Table: 0, Row: 9, Column: 9}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := l.At(tc.ref); ok {
				t.Errorf("At(%v) resolved, want it not to", tc.ref)
			}
		})
	}
}

func TestRefOfATableThatIsNotThere(t *testing.T) {
	t.Parallel()

	l := Layout{}
	// Page 0 rather than a panic or a borrowed page number: a reference to a
	// table that is not there names no page, and says so.
	if got := l.Ref(7, Cell{Row: 1, Column: 2}); got.Page != 0 || got.Table != 7 {
		t.Errorf("Ref() = %v, want page 0 and table 7", got)
	}
}
