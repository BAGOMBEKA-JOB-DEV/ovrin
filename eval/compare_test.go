package eval

import (
	"encoding/json"
	"testing"
	"time"
)

// num builds a json.Number the way ParseExpected produces one, so the table
// below exercises the same types ground truth actually arrives as.
func num(s string) json.Number { return json.Number(s) }

// TestEqual covers the type-aware comparison.
//
// This is the most consequential pure function in the package. A comparison
// that is too strict reports failures on correct extractions and makes the
// whole corpus look worse than it is; one that is too loose reports successes
// that did not happen, which is worse, because nobody investigates good news.
func TestEqual(t *testing.T) {
	mar14 := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		want any
		got  any
		eq   bool
	}{
		// Numbers.
		{"thousands separators are not part of the value", num("25000"), "25,000", true},
		{"narrow spaces group digits in some locales", num("25000"), "25 000", true},
		{"a trailing decimal zero does not change the amount", num("1463200.00"), 1463200.0, true},
		{"a currency symbol is not part of the amount", num("78470"), "UGX 78,470", false},
		{"a different number is a different number", num("25000"), "2,500", false},
		{"float64 against an integer ground truth", num("40"), float64(40), true},
		{"int against a decimal ground truth", num("40.0"), 40, true},
		{"exact decimals do not drift through binary floating point", num("18430.55"), 18430.55, true},
		{"a number against text that is not one", num("25000"), "twenty five thousand", false},
		{"a number against nothing", num("25000"), nil, false},

		// Strings.
		{"case is not part of a name", "Acme Ltd", "ACME LTD", true},
		{"surrounding whitespace is not part of a name", "Acme Ltd", "  Acme Ltd  ", true},
		{"runs of whitespace collapse", "Acme Ltd", "Acme    Ltd", true},
		{"a non-breaking space is a space", "Acme Ltd", "Acme Ltd", true},
		{"an abbreviation is a different string", "Acme Ltd", "Acme Limited", false},
		{"two invoice numbers differing in a digit", "INV-2026-0417", "INV-2026-0418", false},
		{"a leading zero is part of a reference", "007", "7", false},

		// Money.
		{"same amount and same currency", "100 USD", "USD 100", true},
		{"same amount and same currency with grouping", "1,463,200.00 UGX", "UGX 1463200", true},
		{"same amount and a different currency", "100 USD", "100 EUR", false},

		// Dates.
		{"an ISO date against a parsed instant", "2026-03-14", mar14, true},
		{"an ISO date against an RFC 3339 string", "2026-03-14", "2026-03-14T00:00:00Z", true},
		{"a different day", "2026-03-14", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), false},
		{"a date against a string that is not one", "2026-03-14", "not a date", false},
		{"an instant against a value that is not a date", "Acme Ltd", mar14, false},

		// Booleans.
		{"true is true", true, true, true},
		{"true is not false", true, false, false},
		{"a boolean against the word", true, "yes", false},

		// Absence.
		{"absent on both sides", nil, nil, true},
		{"absent against a value", nil, "something", false},

		// Slices and objects.
		{"same elements in the same order", []any{num("1"), num("2")}, []float64{1, 2}, true},
		{"a different order is a different list", []any{num("1"), num("2")}, []float64{2, 1}, false},
		{"a dropped element is a different list", []any{num("1"), num("2")}, []float64{1}, false},
		{"an empty list is not a missing one", []any{}, []float64{}, true},
		{
			"same object",
			map[string]any{"name": "Acme"},
			map[string]any{"name": "ACME"},
			true,
		},
		{
			"an object with an extra member is a different object",
			map[string]any{"name": "Acme"},
			map[string]any{"name": "Acme", "tax_id": "1"},
			false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := Equal(c.want, c.got); got != c.eq {
				t.Errorf("Equal(%#v, %#v) = %v, want %v", c.want, c.got, got, c.eq)
			}
		})
	}
}

// TestEqualIsSymmetricForScalars checks the one property the metric arithmetic
// silently assumes. Scoring compares ground truth against an extraction in one
// direction only, so an asymmetric comparison would produce a number that
// depends on which side a value happened to be written down.
func TestEqualIsSymmetricForScalars(t *testing.T) {
	pairs := []struct{ a, b any }{
		{num("25000"), "25,000"},
		{"Acme Ltd", "ACME LTD"},
		{true, true},
		{"2026-03-14", "2026-03-14T00:00:00Z"},
		{num("1"), "one"},
	}
	for _, p := range pairs {
		if Equal(p.a, p.b) != Equal(p.b, p.a) {
			t.Errorf("Equal is asymmetric for %#v and %#v", p.a, p.b)
		}
	}
}

// TestRatFromString covers the number reader directly, including the values it
// must refuse. A reference that happens to contain digits must never be read
// as arithmetic: "INV-2026-0417" and "INV-2026-0418" would otherwise both
// reduce to the same sum of digits and compare equal.
func TestRatFromString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"plain digits", "25000", true},
		{"grouped with commas", "1,463,200", true},
		{"grouped with spaces", "25 000", true},
		{"decimal", "18430.55", true},
		{"negative", "-540.00", true},
		{"leading currency symbol", "$25.00", true},
		{"an invoice reference", "INV-2026-0417", false},
		{"a percentage is a different quantity", "18%", false},
		{"empty", "", false},
		{"only punctuation", "--", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, ok := ratFromString(c.in)
			if ok != c.ok {
				t.Errorf("ratFromString(%q) ok = %v, want %v", c.in, ok, c.ok)
			}
		})
	}
}
