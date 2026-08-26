package validate

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

func TestParseNumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want float64
	}{
		{"a plain integer", "25000", 25000},
		{"a plain decimal", "25000.50", 25000.50},
		{"comma grouping", "25,000", 25000},
		{"comma grouping with a decimal point", "1,234,567.89", 1234567.89},
		{"point grouping with a decimal comma", "1.234.567,89", 1234567.89},
		{"a decimal comma alone", "1234,56", 1234.56},
		{"space grouping", "25 000", 25000},
		{"non-breaking space grouping", "25 000", 25000},
		{"thin space grouping", "25 000", 25000},
		{"Swiss apostrophe grouping", "25'000", 25000},
		{"a leading currency symbol", "$1,234.56", 1234.56},
		{"a trailing currency symbol", "1.234,56 €", 1234.56},
		{"a shilling sign", "₹1,00,000.00", 100000},
		{"a leading ISO code", "UGX 1,200,000", 1200000},
		{"a trailing ISO code", "1 200 000 UGX", 1200000},
		{"a negative", "-1,234.56", -1234.56},
		{"a unicode minus", "−1234.56", -1234.56},
		{"a trailing sign", "1,234.56-", -1234.56},
		{"accounting parentheses", "(1,234.56)", -1234.56},
		{"a currency symbol inside parentheses", "($1,234.56)", -1234.56},
		{"an explicit plus", "+1234", 1234},
		{"a leading zero fraction is not a group", "0.500", 0.5},
		{"a fraction with no leading digit", ".5", 0.5},
		{"four decimal places", "1.2345", 1.2345},
		{"two decimal places", "12.34", 12.34},
		{"a trailing unit", "1.5 kg", 1.5},
		{"zero", "0", 0},
		{"surrounding whitespace", "  42  ", 42},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := ParseNumber(c.in)
			if !ok {
				t.Fatalf("did not parse: %s", reason)
			}
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseNumberRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"whitespace", "   "},
		{"prose", "twenty five thousand"},
		{"a percentage, whose meaning depends on a convention we do not know", "50%"},
		{"a word that looks like a code", "NIL"},
		{"groups that are not groups of three", "1,2,3"},
		{"two decimal points", "1.2.3.4,5,6"},
		{"a lone separator", ","},
		{"scientific notation, which no document prints", "1e5"},
		{"a range", "10-20"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := ParseNumber(c.in); ok {
				t.Errorf("parsed as %v, want a refusal", got)
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
		ok   bool
	}{
		{"yes", "yes", true, true},
		{"Yes capitalised", "Yes", true, true},
		{"YES shouted", "YES", true, true},
		{"y", "y", true, true},
		{"true", "true", true, true},
		{"checked", "checked", true, true},
		{"checked with whitespace", "  Checked  ", true, true},
		{"no", "no", false, true},
		{"n", "n", false, true},
		{"false", "false", false, true},
		{"unchecked", "unchecked", false, true},
		{"a digit, which may mean one rather than yes", "1", false, false},
		{"a tick nobody can attribute", "x", false, false},
		{"empty", "", false, false},
		{"prose", "probably", false, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, _ := ParseBool(c.in)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFieldConvertsIntoTheDeclaredType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   schema.Kind
		goType any
		raw    any
		want   any
	}{
		{"a string field", schema.KindString, "", "Acme Ltd", "Acme Ltd"},
		{"a float64 field", schema.KindFloat, float64(0), "1,234.56", float64(1234.56)},
		{"a float32 field", schema.KindFloat, float32(0), "1.5", float32(1.5)},
		{"an int field", schema.KindInt, int(0), "42", int(42)},
		{"an int8 field", schema.KindInt, int8(0), "12", int8(12)},
		{"a uint field", schema.KindInt, uint(0), "42", uint(42)},
		{"an int from a whole-numbered float", schema.KindInt, int(0), float64(42), int(42)},
		{"an int from grouped text", schema.KindInt, int(0), "1,000", int(1000)},
		{"a bool field", schema.KindBool, false, "yes", true},
		{"a bool field from a real bool", schema.KindBool, false, true, true},
		{"a number that arrived as JSON", schema.KindFloat, float64(0), json.Number("12.5"), float64(12.5)},
		{"an optional field converts to its element type", schema.KindFloat, (*float64)(nil), "2.5", float64(2.5)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(field(c.kind, c.goType), c.raw)
			if !r.Converted {
				t.Fatalf("did not convert: %s", r.Message)
			}
			if !reflect.DeepEqual(r.Value, c.want) {
				t.Errorf("Value = %#v (%T), want %#v (%T)", r.Value, r.Value, c.want, c.want)
			}
		})
	}
}

func TestFieldRefusesToForceAValueIntoAType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   schema.Kind
		goType any
		raw    any
	}{
		{"a fraction in an integer field", schema.KindInt, int(0), "2.5"},
		{"a fraction that arrived as a JSON number", schema.KindInt, int(0), float64(2.5)},
		{"a negative in an unsigned field", schema.KindInt, uint(0), "-1"},
		{"a value too large for an int8", schema.KindInt, int8(0), "200"},
		{"a value too large for a uint8", schema.KindInt, uint8(0), "256"},
		{"a value too large for a float32", schema.KindFloat, float32(0), "1e40"},
		{"prose in a number", schema.KindFloat, float64(0), "about twenty"},
		{"prose in a bool", schema.KindBool, false, "possibly"},
		{"a number in a bool", schema.KindBool, false, float64(1)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New().Field(field(c.kind, c.goType), c.raw)
			if r.Converted {
				t.Fatalf("converted to %#v; a value that does not fit must not be forced", r.Value)
			}
			if r.Value != nil {
				t.Errorf("Value = %#v, want nothing at all", r.Value)
			}
			if r.Message == "" {
				t.Error("a refusal must say why")
			}
			if !r.Found {
				t.Error("Found = false; the value was there, it just did not fit")
			}
		})
	}
}

func TestRawTextIsWhatAReviewerWouldRecognise(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"text is itself", "Acme Ltd", "Acme Ltd"},
		{"text is trimmed", "  Acme Ltd  ", "Acme Ltd"},
		{"a large number is not written in scientific notation", float64(1234567), "1234567"},
		{"a decimal keeps its places", float64(12.5), "12.5"},
		{"an integer", 42, "42"},
		{"a bool", true, "true"},
		{"nothing", nil, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := rawText(c.in); got != c.want {
				t.Errorf("rawText(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestDefensivePathsHoldWhenTheSchemaPackageDoesNot covers the branches that
// exist because this package is not the only thing that can be wrong. The
// schema package rejects every one of these before extraction starts; if one
// ever reaches here it must be visible rather than silently passed.
func TestDefensivePathsHoldWhenTheSchemaPackageDoesNot(t *testing.T) {
	t.Parallel()

	t.Run("a field with no reflect.Type still converts by kind", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name string
			kind schema.Kind
			raw  any
			want any
		}{
			{"string", schema.KindString, "x", "x"},
			{"int", schema.KindInt, "3", int(3)},
			{"float", schema.KindFloat, "3.5", float64(3.5)},
			{"bool", schema.KindBool, "yes", true},
		} {
			c := c
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				r := New().Field(field(c.kind, nil), c.raw)
				if !r.Converted || !reflect.DeepEqual(r.Value, c.want) {
					t.Errorf("Value = %#v (converted %v), want %#v", r.Value, r.Converted, c.want)
				}
			})
		}
	})

	t.Run("a kind that is not a kind converts to nothing", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindUnknown, nil), "x")
		if r.Converted || r.Message == "" {
			t.Errorf("got %+v, want a refusal with a reason", r)
		}
	})

	t.Run("a text format on a field that cannot hold text", func(t *testing.T) {
		t.Parallel()
		f := field(schema.KindFloat, float64(0), rule(schema.RuleFormat, schema.FormatCurrency))
		r := New().Field(f, "UGX")
		if r.Converted {
			t.Errorf("Value = %#v, want a refusal", r.Value)
		}
	})

	t.Run("a format nobody implements", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindString, "", rule(schema.RuleFormat, "postcode")), "SW1A 1AA")
		if r.Converted || ruleByName(t, r, "format=postcode").Passed {
			t.Errorf("got %+v, want a refusal", r)
		}
	})

	t.Run("a time field with no format is parsed rather than abandoned", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindTime, time.Time{}), "2026-04-03")
		if !r.Converted {
			t.Fatalf("did not convert: %s", r.Message)
		}
		if got := r.Value.(time.Time); !got.Equal(time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("a value that is already a time is taken as it is", func(t *testing.T) {
		t.Parallel()
		in := time.Date(2026, 4, 3, 14, 30, 0, 0, time.UTC)
		f := field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate))
		r := New().Field(f, in)
		if !r.Converted {
			t.Fatalf("did not convert: %s", r.Message)
		}
		if got := r.Value.(time.Time); !got.Equal(time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("got %v, want it truncated to midnight UTC", got)
		}
	})

	t.Run("an enum on a field that is not text", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindFloat, float64(0), rule(schema.RuleEnum, "1|2")), "1")
		if ruleByName(t, r, "enum=1|2").Passed {
			t.Error("an enum on a number must not pass by accident")
		}
	})

	t.Run("a length bound on a kind that has no length", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindObject, struct{}{}, rule(schema.RuleMax, "2")), map[string]any{"a": 1})
		if ruleByName(t, r, "max=2").Passed {
			t.Error("a bound that cannot be evaluated must not pass")
		}
	})

	t.Run("a nil option is ignored rather than dereferenced", func(t *testing.T) {
		t.Parallel()
		if v := New(nil, WithDateOrder(DayFirst)); v.dateOrder != DayFirst {
			t.Errorf("dateOrder = %q", v.dateOrder)
		}
	})
}

func TestRawTextOfTheOddCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"a JSON number keeps its printed form", json.Number("1234.50"), "1234.50"},
		{"an unsigned integer", uint16(65535), "65535"},
		{"a float32", float32(1.5), "1.5"},
		{"a time", time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC), "2026-04-03T00:00:00Z"},
		{"a slice has no text of its own", []any{1, 2}, ""},
		{"a map has no text of its own", map[string]any{"a": 1}, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := rawText(c.in); got != c.want {
				t.Errorf("rawText(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
