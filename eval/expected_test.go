package eval

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// TestParseExpected covers flattening ground truth to field keys.
//
// The keys have to match [ovrin.Result].Fields exactly. A flattener that
// produced "items.0.unit_price" instead of "items[0].unit_price" would score
// every line item as both a miss and a fabrication, and the report would look
// like a catastrophic regression in the extractor.
func TestParseExpected(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
		err  bool
	}{
		{
			name: "the example from docs/evaluation.md",
			in: `{
			  "number": "INV-2026-0417",
			  "vendor": "Kampala Supplies Ltd",
			  "currency": "UGX",
			  "total": 2500000.00,
			  "issued": "2026-03-14",
			  "items": [
			    {"description": "A4 paper, 80gsm", "quantity": 40, "unit_price": 12500.00},
			    {"description": "Toner cartridge",  "quantity":  4, "unit_price": 185000.00}
			  ]
			}`,
			want: map[string]any{
				"number":               "INV-2026-0417",
				"vendor":               "Kampala Supplies Ltd",
				"currency":             "UGX",
				"total":                json.Number("2500000.00"),
				"issued":               "2026-03-14",
				"items[0].description": "A4 paper, 80gsm",
				"items[0].quantity":    json.Number("40"),
				"items[0].unit_price":  json.Number("12500.00"),
				"items[1].description": "Toner cartridge",
				"items[1].quantity":    json.Number("4"),
				"items[1].unit_price":  json.Number("185000.00"),
			},
		},
		{
			name: "nested objects become dotted keys",
			in:   `{"vendor": {"name": "Acme", "tax_id": "100"}}`,
			want: map[string]any{"vendor.name": "Acme", "vendor.tax_id": "100"},
		},
		{
			name: "a null is absence and is dropped",
			in:   `{"total": 1, "due": null}`,
			want: map[string]any{"total": json.Number("1")},
		},
		{
			name: "an empty list is a claim and is kept",
			in:   `{"items": []}`,
			want: map[string]any{"items": []any{}},
		},
		{
			name: "booleans survive",
			in:   `{"renewal": true, "tax_cleared": false}`,
			want: map[string]any{"renewal": true, "tax_cleared": false},
		},
		{
			name: "a top level that is not an object is refused",
			in:   `[1, 2]`,
			err:  true,
		},
		{
			name: "malformed JSON is refused",
			in:   `{"total":`,
			err:  true,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseExpected([]byte(c.in))
			if c.err {
				if err == nil {
					t.Fatal("ParseExpected accepted malformed input")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseExpected: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got  %#v\nwant %#v", got, c.want)
			}
		})
	}
}

// TestLeafKeys covers container suppression.
//
// A Result reports both "items" and "items[0].total". Scoring both would count
// one slice once as a container and again for every member, which inflates
// whichever metric the container happens to land in.
func TestLeafKeys(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "a slice container is dropped when it has members",
			in:   []string{"items", "items[0].total", "items[1].total", "total"},
			want: []string{"items[0].total", "items[1].total", "total"},
		},
		{
			name: "an object container is dropped when it has members",
			in:   []string{"vendor", "vendor.name", "vendor.tax_id"},
			want: []string{"vendor.name", "vendor.tax_id"},
		},
		{
			name: "a container with no members is itself a leaf",
			in:   []string{"items", "total"},
			want: []string{"items", "total"},
		},
		{
			name: "a key that merely shares a prefix is not a member",
			in:   []string{"total", "totals_checked"},
			want: []string{"total", "totals_checked"},
		},
		{
			name: "nesting to two levels",
			in:   []string{"a", "a.b", "a.b.c"},
			want: []string{"a.b.c"},
		},
		{
			name: "nothing",
			in:   nil,
			want: []string{},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := leafKeys(c.in)
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("leafKeys(%v) = %v, want %v", c.in, got, want)
			}
		})
	}
}

// TestCollapseIndices covers the per-field grouping key.
func TestCollapseIndices(t *testing.T) {
	cases := []struct{ in, want string }{
		{"total", "total"},
		{"items[0].unit_price", "items[].unit_price"},
		{"items[12].unit_price", "items[].unit_price"},
		{"a[0].b[3].c", "a[].b[].c"},
		{"items[]", "items[]"},
		{"broken[", "broken["},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			if got := CollapseIndices(c.in); got != c.want {
				t.Errorf("CollapseIndices(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
