package ovrin_test

import (
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
