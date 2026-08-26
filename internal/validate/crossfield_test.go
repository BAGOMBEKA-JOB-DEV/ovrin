package validate

import (
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// invoice is a small set of converted values, the shape a pipeline hands to
// cross-field rules.
func invoice() Fields {
	return Fields{
		"subtotal":            float64(100),
		"tax":                 float64(18),
		"total":               float64(118),
		"issued":              time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC),
		"due":                 time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		"currency":            "UGX",
		"items[0].quantity":   int(2),
		"items[0].unit_price": float64(25),
		"items[1].quantity":   int(1),
		"items[1].unit_price": float64(50),
	}
}

func TestSum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		fields         Fields
		wantApplicable bool
		wantPassed     bool
	}{
		{"parts that add up", invoice(), true, true},
		{
			name: "parts that do not add up",
			fields: func() Fields {
				f := invoice()
				f["total"] = float64(180)
				return f
			}(),
			wantApplicable: true,
		},
		{
			name: "a total that was not read makes the rule inapplicable",
			fields: func() Fields {
				f := invoice()
				delete(f, "total")
				return f
			}(),
		},
		{
			name: "a part that was not read makes the rule inapplicable",
			fields: func() Fields {
				f := invoice()
				delete(f, "tax")
				return f
			}(),
		},
		{
			name: "a rounding cent is within tolerance",
			fields: func() Fields {
				f := invoice()
				f["total"] = float64(118.01)
				return f
			}(),
			wantApplicable: true, wantPassed: true,
		},
		{
			name: "two cents is not",
			fields: func() Fields {
				f := invoice()
				f["total"] = float64(118.02)
				return f
			}(),
			wantApplicable: true,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Sum("total", DefaultTolerance, "subtotal", "tax").Check(c.fields)
			if r.Applicable != c.wantApplicable {
				t.Fatalf("Applicable = %v, want %v", r.Applicable, c.wantApplicable)
			}
			if r.Applicable && r.Passed != c.wantPassed {
				t.Errorf("Passed = %v, want %v", r.Passed, c.wantPassed)
			}
			if r.Applicable && !r.Passed && r.Message == "" {
				t.Error("a failed cross-field rule must say what is inconsistent")
			}
			if len(r.Fields) == 0 {
				t.Error("a cross-field result must name the fields it read")
			}
		})
	}
}

func TestSumItems(t *testing.T) {
	t.Parallel()
	rule := SumItems("total", "items", DefaultTolerance, "quantity", "unit_price")

	t.Run("line items multiplying out to the total", func(t *testing.T) {
		t.Parallel()
		f := invoice()
		f["total"] = float64(100) // 2×25 + 1×50
		r := rule.Check(f)
		if !r.Applicable || !r.Passed {
			t.Errorf("got %+v, want an applicable pass", r)
		}
	})

	t.Run("line items that contradict the total", func(t *testing.T) {
		t.Parallel()
		r := rule.Check(invoice()) // total is 118, items make 100
		if !r.Applicable || r.Passed {
			t.Errorf("got %+v, want an applicable failure", r)
		}
	})

	t.Run("no line items read at all leaves the total uncontradicted", func(t *testing.T) {
		t.Parallel()
		f := Fields{"total": float64(118)}
		if r := rule.Check(f); r.Applicable {
			t.Errorf("got %+v, want inapplicable", r)
		}
	})

	t.Run("one unreadable line makes the sum unknowable", func(t *testing.T) {
		t.Parallel()
		f := invoice()
		delete(f, "items[1].unit_price")
		if r := rule.Check(f); r.Applicable {
			t.Errorf("got %+v, want inapplicable rather than a failure blamed on the total", r)
		}
	})

	t.Run("a single leaf sums that leaf", func(t *testing.T) {
		t.Parallel()
		f := Fields{
			"count":             float64(3),
			"items[0].quantity": int(2),
			"items[1].quantity": int(1),
		}
		r := SumItems("count", "items", DefaultTolerance, "quantity").Check(f)
		if !r.Applicable || !r.Passed {
			t.Errorf("got %+v, want an applicable pass", r)
		}
	})
}

func TestBefore(t *testing.T) {
	t.Parallel()
	rule := Before("issued", "due")

	t.Run("an issue date before a due date", func(t *testing.T) {
		t.Parallel()
		if r := rule.Check(invoice()); !r.Applicable || !r.Passed {
			t.Errorf("got %+v, want an applicable pass", r)
		}
	})

	t.Run("dates the wrong way round", func(t *testing.T) {
		t.Parallel()
		f := invoice()
		f["due"] = time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
		r := rule.Check(f)
		if !r.Applicable || r.Passed {
			t.Errorf("got %+v, want an applicable failure", r)
		}
	})

	t.Run("issued and due the same day is a document, not an inconsistency", func(t *testing.T) {
		t.Parallel()
		f := invoice()
		f["due"] = f["issued"]
		if r := rule.Check(f); !r.Passed {
			t.Errorf("got %+v, want a pass", r)
		}
	})

	t.Run("a date that was not read makes the rule inapplicable", func(t *testing.T) {
		t.Parallel()
		f := invoice()
		delete(f, "due")
		if r := rule.Check(f); r.Applicable {
			t.Errorf("got %+v, want inapplicable", r)
		}
	})
}

func TestRuleFuncIsTheExtensionPoint(t *testing.T) {
	t.Parallel()
	// A rule nobody in this package anticipated: an invoice in UGX may not
	// carry a euro total. Three lines, no interface to implement.
	rule := RuleFunc("currency_matches", func(f Fields) CrossFieldResult {
		out := CrossFieldResult{Name: "currency_matches", Fields: []string{"currency"}}
		c, ok := f.Text("currency")
		if !ok {
			return out
		}
		out.Applicable = true
		out.Passed = c == "UGX"
		if !out.Passed {
			out.Message = "the currency is not the one this account posts in"
		}
		return out
	})
	if r := rule.Check(invoice()); !r.Applicable || !r.Passed {
		t.Errorf("got %+v, want an applicable pass", r)
	}
	if rule.Name() != "currency_matches" {
		t.Errorf("Name = %q", rule.Name())
	}
}

func TestValidatorRunsItsCrossFieldRulesInOrder(t *testing.T) {
	t.Parallel()
	v := New(
		WithCrossFieldRules(Sum("total", DefaultTolerance, "subtotal", "tax")),
		WithCrossFieldRules(Before("issued", "due"), nil),
	)
	got := v.CrossField(invoice())
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Name != "sum:total" || got[1].Name != "before:issued,due" {
		t.Errorf("order = %q, %q", got[0].Name, got[1].Name)
	}
	if none := New().CrossField(invoice()); none != nil {
		t.Errorf("a validator with no rules returned %+v, want nothing", none)
	}
}

func TestFieldsAccessors(t *testing.T) {
	t.Parallel()
	f := invoice()

	t.Run("Number reads any numeric type", func(t *testing.T) {
		t.Parallel()
		if n, ok := f.Number("items[0].quantity"); !ok || n != 2 {
			t.Errorf("got %v %v, want 2 true", n, ok)
		}
		if _, ok := f.Number("currency"); ok {
			t.Error("text is not a number")
		}
		if _, ok := f.Number("nothing"); ok {
			t.Error("an absent field is not a number")
		}
	})

	t.Run("Time reads a date", func(t *testing.T) {
		t.Parallel()
		if _, ok := f.Time("issued"); !ok {
			t.Error("issued is a date")
		}
		if _, ok := f.Time("total"); ok {
			t.Error("a number is not a date")
		}
	})

	t.Run("Text reads a string", func(t *testing.T) {
		t.Parallel()
		if s, ok := f.Text("currency"); !ok || s != "UGX" {
			t.Errorf("got %q %v", s, ok)
		}
		if _, ok := f.Text("total"); ok {
			t.Error("a number is not text")
		}
	})

	t.Run("Count counts the elements that were read", func(t *testing.T) {
		t.Parallel()
		if n := f.Count("items"); n != 2 {
			t.Errorf("Count = %d, want 2", n)
		}
		if n := f.Count("dependants"); n != 0 {
			t.Errorf("Count = %d, want 0", n)
		}
	})

	t.Run("ElementKey builds the documented key shape", func(t *testing.T) {
		t.Parallel()
		if got := ElementKey("items", 0, "unit_price"); got != "items[0].unit_price" {
			t.Errorf("got %q", got)
		}
		if got := ElementKey("items", 3, ""); got != "items[3]" {
			t.Errorf("got %q", got)
		}
	})
}

func TestTolerance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tol  Tolerance
		a, b float64
		want bool
	}{
		{"exactly equal", Tolerance{}, 100, 100, true},
		{"a cent apart within a cent", Tolerance{Absolute: 0.01}, 100, 100.01, true},
		{"two cents apart", Tolerance{Absolute: 0.01}, 100, 100.02, false},
		{"within half a percent", Tolerance{Relative: 0.005}, 1000, 1004, true},
		{"outside half a percent", Tolerance{Relative: 0.005}, 1000, 1010, false},
		{"a relative bound is meaningless near zero", Tolerance{Relative: 0.005}, 0, 1, false},
		{"the absolute bound catches what the relative one cannot", Tolerance{Absolute: 1, Relative: 0.005}, 0, 1, true},
		{"negative amounts", Tolerance{Absolute: 0.01}, -100, -100.005, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.tol.Within(c.a, c.b); got != c.want {
				t.Errorf("Within(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestSignals(t *testing.T) {
	t.Parallel()

	t.Run("format is absent when no format is declared", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindString, "", rule(schema.RuleRequired, "")), "x")
		if v, ok := FormatSignal(r); ok {
			t.Errorf("got %v, want absent", v)
		}
	})

	t.Run("format is one when the value parsed", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatCurrency)), "ugx")
		if v, ok := FormatSignal(r); !ok || v != 1 {
			t.Errorf("got %v %v, want 1 true", v, ok)
		}
	})

	t.Run("format is zero when the value did not parse", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindString, "", rule(schema.RuleFormat, schema.FormatCurrency)), "dollars")
		if v, ok := FormatSignal(r); !ok || v != 0 {
			t.Errorf("got %v %v, want 0 true", v, ok)
		}
	})

	t.Run("an ambiguous date is lowered rather than failed", func(t *testing.T) {
		t.Parallel()
		f := field(schema.KindTime, time.Time{}, rule(schema.RuleFormat, schema.FormatDate))
		r := New().Field(f, "03/04/2026")
		v, ok := FormatSignal(r)
		if !ok || v != AmbiguousFormatSignal {
			t.Errorf("got %v %v, want %v", v, ok, AmbiguousFormatSignal)
		}
		if v <= 0 {
			t.Error("an ambiguous date must rank above a malformed one")
		}
	})

	t.Run("schema is one for a clean field", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindFloat, float64(0), rule(schema.RuleMin, "0")), "12.50")
		if v := SchemaSignal(r); v != 1 {
			t.Errorf("got %v, want 1", v)
		}
	})

	t.Run("schema is zero for a value that did not convert", func(t *testing.T) {
		t.Parallel()
		r := New().Field(field(schema.KindFloat, float64(0)), "about twenty")
		if v := SchemaSignal(r); v != 0 {
			t.Errorf("got %v, want 0", v)
		}
	})

	t.Run("schema is the fraction of rules that passed", func(t *testing.T) {
		t.Parallel()
		f := field(schema.KindFloat, float64(0), rule(schema.RuleRequired, ""), rule(schema.RuleMin, "100"))
		r := New().Field(f, "12.50")
		if v := SchemaSignal(r); v != 0.5 {
			t.Errorf("got %v, want 0.5", v)
		}
	})

	t.Run("cross_field is absent when no rule could run", func(t *testing.T) {
		t.Parallel()
		results := []CrossFieldResult{{Name: "sum:total"}}
		if v, ok := CrossFieldSignal(results); ok {
			t.Errorf("got %v, want absent", v)
		}
		if v, ok := CrossFieldSignal(nil); ok {
			t.Errorf("got %v, want absent", v)
		}
	})

	t.Run("cross_field is the fraction of applicable rules that passed", func(t *testing.T) {
		t.Parallel()
		results := []CrossFieldResult{
			{Applicable: true, Passed: true},
			{Applicable: true, Passed: false},
			{Applicable: false},
		}
		v, ok := CrossFieldSignal(results)
		if !ok || v != 0.5 {
			t.Errorf("got %v %v, want 0.5 true", v, ok)
		}
	})
}
