package ovrin_test

import (
	"context"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// tableOCR reports a table, the way ocr/azure does with prebuilt-layout.
type tableOCR struct{ layout *ovrin.Layout }

func (tableOCR) Name() string { return "tables" }

func (o tableOCR) Recognise(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
	return &ovrin.Recognition{
		Words: []ovrin.Word{{
			Text: "INVOICE", Box: ovrin.Rect{MinX: 10, MinY: 10, MaxX: 80, MaxY: 24},
			Confidence: 0.95, Line: 1,
		}},
		Confidence: 0.95,
		Layout:     o.layout,
	}, nil
}

// A table a provider detected has to reach the model, or detecting it bought
// nothing.
//
// Recognition.Layout existed, was documented, was in the golden API file, and
// no adapter filled it while nothing read it. This is the end of that wire:
// provider → Recognition.Layout → prompt → the bytes a model sees.
func TestADetectedTableReachesTheModel(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	layout := &ovrin.Layout{Tables: []ovrin.Table{{
		Page: 1, Rows: 2, Columns: 2,
		Cells: []ovrin.Cell{
			{Row: 0, Column: 0, Kind: ovrin.CellColumnHeader, Text: "Description"},
			{Row: 0, Column: 1, Kind: ovrin.CellColumnHeader, Text: "Amount"},
			{Row: 1, Column: 0, Text: "Consulting"},
			{Row: 1, Column: 1, Text: "1250.00"},
		},
	}}}

	var seen []ovrin.Content
	c := ovrin.New(
		ovrin.WithModel(captureModel{reply: map[string]any{"total": 1250.0}, seen: &seen}),
		ovrin.WithOCR(tableOCR{layout: layout}),
		ovrin.WithRenderer(stubRenderer{calls: new([]int)}),
	)

	const fixture = "testdata/mixed-digital-and-scan.pdf"
	if _, err := ovrin.Extract[Doc](context.Background(), c, ovrin.File(fixture)); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var withTable string
	for _, ct := range seen {
		if contains(ct.Text, "Consulting") {
			withTable = ct.Text
		}
	}
	if withTable == "" {
		t.Fatal("no page the model received carries the table's cells")
	}
	for _, want := range []string{"Description", "Amount", "Consulting", "1250.00"} {
		if !contains(withTable, want) {
			t.Errorf("the rendered page does not carry cell %q", want)
		}
	}

	// Document content, so it must be inside the untrusted-content boundary
	// like everything else the document said (docs/threat-model.md T2).
	head := withTable[:min(len(withTable), 40)]
	if !contains(head, "BEGIN UNTRUSTED DOCUMENT CONTENT") {
		t.Errorf("table text is not inside the untrusted boundary; page begins %q", head)
	}
}

// A provider that reports no structure changes nothing about the bytes the
// model sees. nil Layout is not an empty table.
func TestNoLayoutMeansNoTableInThePrompt(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	send := func(l *ovrin.Layout) string {
		var seen []ovrin.Content
		c := ovrin.New(
			ovrin.WithModel(captureModel{reply: map[string]any{"total": 1.0}, seen: &seen}),
			ovrin.WithOCR(tableOCR{layout: l}),
			ovrin.WithRenderer(stubRenderer{calls: new([]int)}),
		)
		if _, err := ovrin.Extract[Doc](context.Background(), c,
			ovrin.File("testdata/mixed-digital-and-scan.pdf")); err != nil {
			t.Fatalf("Extract: %v", err)
		}
		var all string
		for _, ct := range seen {
			all += ct.Text
		}
		return all
	}

	// nil: the provider does not report structure at all.
	// empty: it looked and found none. Neither may add a table.
	for _, tc := range []struct {
		name string
		l    *ovrin.Layout
	}{
		{"nobody looked", nil},
		{"looked and found none", &ovrin.Layout{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := send(tc.l); contains(got, "[table") {
				t.Errorf("a table was rendered although the layout was %s", tc.name)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
