package compare

import (
	"math"
	"testing"
	"time"
)

// equalCase is one pair of readings and the answer expected of them.
type equalCase struct {
	name string
	a, b any
	kind Kind
	want bool
	opts []Option
}

// run asserts the outcome, and asserts symmetry on every case for free: a
// comparison that answered differently depending on which reading was passed
// first would be a bug nobody would think to look for.
func run(t *testing.T, cases []equalCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Values(tc.a, tc.b, tc.kind, tc.opts...)
			if got.Equal != tc.want {
				t.Errorf("Equal = %v, want %v (applicable=%v kind=%v fallback=%v reason=%q)",
					got.Equal, tc.want, got.Applicable, got.Kind, got.Fallback, got.Reason)
			}
			if !got.Applicable {
				t.Errorf("Applicable = false, want a comparison to have been made (reason=%q)", got.Reason)
			}
			back := Values(tc.b, tc.a, tc.kind, tc.opts...)
			if back.Equal != got.Equal || back.Applicable != got.Applicable {
				t.Errorf("not symmetric: forward %+v, reverse %+v", got, back)
			}
			if Equal(tc.a, tc.b, tc.kind, tc.opts...) != got.Equal {
				t.Errorf("Equal and Values disagree")
			}
		})
	}
}

// TestNumbersAgree is the false-positive direction, and it is the one that
// decides whether the agreement signal is usable: a flag that fires on every
// formatted figure is a flag reviewers learn to dismiss.
func TestNumbersAgree(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "comma_grouping_against_bare_digits", a: "25,000", b: "25000", kind: KindNumber, want: true},
		{name: "comma_grouping_against_float", a: "25,000", b: 25000.0, kind: KindNumber, want: true},
		{name: "comma_grouping_against_int", a: "25,000", b: 25000, kind: KindNumber, want: true},
		{name: "space_grouping", a: "25 000", b: 25000.0, kind: KindNumber, want: true},
		{name: "non_breaking_space_grouping", a: "25 000", b: 25000.0, kind: KindNumber, want: true},
		{name: "narrow_no_break_space_grouping", a: "25 000", b: 25000.0, kind: KindNumber, want: true},
		{name: "thin_space_grouping", a: "25 000", b: 25000.0, kind: KindNumber, want: true},
		{name: "swiss_apostrophe_grouping", a: "25'000", b: 25000.0, kind: KindNumber, want: true},
		{name: "dot_grouping_against_comma_grouping", a: "1.234", b: "1,234", kind: KindNumber, want: true},
		{name: "european_decimal_against_english", a: "1.234,56", b: "1,234.56", kind: KindNumber, want: true},
		{name: "south_asian_grouping", a: "1,00,000", b: 100000.0, kind: KindNumber, want: true},
		{name: "accounting_parentheses_are_negative", a: "(1,234.56)", b: -1234.56, kind: KindNumber, want: true},
		{name: "trailing_sign", a: "1,234.00-", b: -1234.0, kind: KindNumber, want: true},
		{name: "unicode_minus", a: "−1234", b: -1234.0, kind: KindNumber, want: true},
		{name: "leading_plus", a: "+1234", b: 1234.0, kind: KindNumber, want: true},
		{name: "surrounding_whitespace", a: "  42  ", b: 42.0, kind: KindNumber, want: true},
		{name: "fullwidth_digits", a: "２５，０００", b: 25000.0, kind: KindNumber, want: true},
		{name: "zero_width_space_inside_digits", a: "25\u200b000", b: 25000.0, kind: KindNumber, want: true},
		{name: "currency_symbol_before", a: "$1,000.00", b: 1000.0, kind: KindNumber, want: true},
		{name: "currency_code_after", a: "1,000.00 USD", b: 1000.0, kind: KindNumber, want: true},
		{name: "unit_after", a: "1.5 kg", b: 1.5, kind: KindNumber, want: true},
		{name: "trailing_zeros_in_decimal", a: "25.50", b: 25.5, kind: KindNumber, want: true},
		{name: "json_round_trip_drift", a: 0.1 + 0.2, b: 0.3, kind: KindNumber, want: true},
		{name: "zero_is_a_value", a: "0", b: 0.0, kind: KindNumber, want: true},
		{name: "negative_zero", a: math.Copysign(0, -1), b: 0.0, kind: KindNumber, want: true},
		{name: "kind_inferred_from_the_typed_reading", a: "25,000", b: 25000.0, kind: KindUnknown, want: true},
	})
}

// TestNumbersDisagree is the failure ADR-0014 was written around, plus the
// near misses that must not be averaged away by a tolerance.
func TestNumbersDisagree(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "the_adr_case_25000_against_2500", a: "25,000", b: "2,500", kind: KindNumber, want: false},
		{name: "factor_of_ten", a: 25000.0, b: 2500.0, kind: KindNumber, want: false},
		{name: "one_cent_apart", a: "1,234.56", b: "1,234.57", kind: KindNumber, want: false},
		{name: "sign_flipped", a: "(1,234)", b: 1234.0, kind: KindNumber, want: false},
		{name: "decimal_read_as_grouping", a: "250,00", b: "25,000", kind: KindNumber, want: false},
		{name: "transposed_digits", a: "1,435", b: "1,345", kind: KindNumber, want: false},
		{name: "extra_trailing_digit", a: "1234", b: "12345", kind: KindNumber, want: false},
		{name: "unparseable_against_a_number", a: "2S,000", b: "25000", kind: KindNumber, want: false},
	})
}

// TestCurrencyAgrees checks the amount-and-currency row, including the
// deliberate leniency when only one reading carried a currency at all.
func TestCurrencyAgrees(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "code_after_against_code_before", a: "100 USD", b: "USD 100", kind: KindCurrency, want: true},
		{name: "symbol_against_code", a: "$100", b: "100 USD", kind: KindCurrency, want: true},
		{name: "euro_symbol_against_code", a: "€1.234,56", b: "1,234.56 EUR", kind: KindCurrency, want: true},
		{name: "lowercase_code", a: "100 usd", b: "USD 100", kind: KindCurrency, want: true},
		{name: "brazilian_real_is_not_a_dollar", a: "R$100", b: "100 BRL", kind: KindCurrency, want: true},
		{name: "currency_in_one_reading_only", a: "$1,000", b: 1000.0, kind: KindCurrency, want: true},
		{name: "no_currency_in_either", a: "1,000", b: 1000.0, kind: KindCurrency, want: true},
		{name: "grouping_and_symbol_together", a: "$25,000.00", b: "USD 25 000", kind: KindCurrency, want: true},
	})
}

// TestCurrencyDisagrees covers the row's whole point: the amount alone is not
// the value.
func TestCurrencyDisagrees(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "same_amount_different_code", a: "100 USD", b: "100 EUR", kind: KindCurrency, want: false},
		{name: "same_amount_different_symbol", a: "$100", b: "€100", kind: KindCurrency, want: false},
		{name: "symbol_against_a_different_code", a: "£100", b: "100 USD", kind: KindCurrency, want: false},
		{name: "same_code_different_amount", a: "USD 25,000", b: "USD 2,500", kind: KindCurrency, want: false},
	})
}

// TestDatesAgree includes the example docs/confidence.md states outright:
// 03/04/26 and 2026-04-03 are equal.
func TestDatesAgree(t *testing.T) {
	t.Parallel()
	apr3 := time.Date(2026, time.April, 3, 0, 0, 0, 0, time.UTC)
	run(t, []equalCase{
		{name: "the_documented_example", a: "03/04/26", b: "2026-04-03", kind: KindDate, want: true},
		{name: "iso_against_time_value", a: "2026-04-03", b: apr3, kind: KindDate, want: true},
		{name: "prose_against_iso", a: "3 April 2026", b: "2026-04-03", kind: KindDate, want: true},
		{name: "american_prose_against_iso", a: "April 3, 2026", b: "2026-04-03", kind: KindDate, want: true},
		{name: "iso_against_rfc3339_midnight", a: "2026-04-03", b: "2026-04-03T00:00:00Z", kind: KindDate, want: true},
		{name: "clock_in_one_reading_only", a: "2026-04-03 14:30", b: "2026-04-03", kind: KindDate, want: true},
		{name: "same_instant_across_offsets", a: "2026-04-03T23:00:00Z", b: "2026-04-04T02:00:00+03:00", kind: KindDate, want: true},
		{name: "ambiguous_matches_either_reading_of_itself", a: "03/04/2026", b: "04/03/2026", kind: KindDate, want: true},
		{name: "ambiguous_against_itself", a: "03/04/26", b: "03/04/26", kind: KindDate, want: true},
		{name: "dotted_european_against_iso", a: "3.4.2026", b: "2026-04-03", kind: KindDate, want: true},
		{name: "day_first_order_resolves_to_the_same_day", a: "03/04/2026", b: "2026-04-03", kind: KindDate, want: true, opts: []Option{WithDateOrder(DayFirst)}},
		{name: "time_values_at_the_same_instant", a: apr3, b: apr3.In(time.FixedZone("EAT", 3*60*60)), kind: KindDate, want: true},
	})
}

// TestDatesDisagree covers the off-by-one, which is what a misread digit
// actually looks like.
func TestDatesDisagree(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "one_day_apart", a: "2026-04-03", b: "2026-04-04", kind: KindDate, want: false},
		{name: "one_month_apart", a: "2026-04-03", b: "2026-05-03", kind: KindDate, want: false},
		{name: "one_year_apart", a: "2026-04-03", b: "2025-04-03", kind: KindDate, want: false},
		{name: "day_month_swap_with_the_order_settled", a: "03/04/2026", b: "04/03/2026", kind: KindDate, want: false, opts: []Option{WithDateOrder(DayFirst)}},
		{name: "different_clock_on_the_same_day", a: "2026-04-03T14:30:00Z", b: "2026-04-03T15:30:00Z", kind: KindDate, want: false},
		{name: "unparseable_against_a_date", a: "2026-04-0X", b: "2026-04-03", kind: KindDate, want: false},
	})
}

// TestStringsAgree is the other half of the false-positive direction. ACME LTD
// against Acme Ltd is the case docs/confidence.md says would make the feature
// useless through noise.
func TestStringsAgree(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "case_folded", a: "ACME LTD", b: "Acme Ltd", kind: KindString, want: true},
		{name: "internal_whitespace_collapsed", a: "Acme   Ltd", b: "Acme Ltd", kind: KindString, want: true},
		{name: "trailing_whitespace", a: "Acme Ltd  ", b: "Acme Ltd", kind: KindString, want: true},
		{name: "non_breaking_space_between_words", a: "Acme Ltd", b: "Acme Ltd", kind: KindString, want: true},
		{name: "newline_as_whitespace", a: "Acme\nLtd", b: "Acme Ltd", kind: KindString, want: true},
		{name: "ligature_expanded", a: "ﬁnance dept", b: "Finance Dept", kind: KindString, want: true},
		{name: "fullwidth_letters", a: "ＡＣＭＥ", b: "Acme", kind: KindString, want: true},
		{name: "decomposed_accent", a: "café", b: "café", kind: KindString, want: true},
		{name: "sharp_s_folds_to_ss", a: "STRASSE 1", b: "Straße 1", kind: KindString, want: true},
		{name: "final_sigma_folds_to_sigma", a: "ΟΔΟΣ", b: "οδος", kind: KindString, want: true},
		{name: "zero_width_joiner_inside_a_word", a: "Ac\u200dme", b: "Acme", kind: KindString, want: true},
		{name: "roman_numeral_compatibility_form", a: "Chapter Ⅻ", b: "Chapter XII", kind: KindString, want: true},
	})
}

// TestStringsDisagree keeps the noise threshold honest in the other direction:
// Acme Ltd and Acme Limited are two companies as often as they are one.
func TestStringsDisagree(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "expanded_suffix", a: "Acme Ltd", b: "Acme Limited", kind: KindString, want: false},
		{name: "one_letter_apart", a: "Acme Ltd", b: "Acne Ltd", kind: KindString, want: false},
		{name: "prefix_is_not_the_whole_value", a: "Acme", b: "Acme Ltd", kind: KindString, want: false},
		{name: "transposed_words", a: "Ltd Acme", b: "Acme Ltd", kind: KindString, want: false},
		{name: "trailing_full_stop_is_a_difference", a: "Acme Ltd.", b: "Acme Ltd", kind: KindString, want: false},
		{name: "case_sensitive_when_the_caller_says_so", a: "abcDEF", b: "ABCdef", kind: KindString, want: false, opts: []Option{WithCaseSensitive()}},
	})
}

// TestBooleans checks that the spellings a form uses read the same here as
// they do in conversion.
func TestBooleans(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "yes_is_true", a: "Yes", b: true, kind: KindBool, want: true},
		{name: "checked_is_true", a: "checked", b: "true", kind: KindBool, want: true},
		{name: "no_is_false", a: "no", b: false, kind: KindBool, want: true},
		{name: "y_is_true", a: "Y", b: true, kind: KindBool, want: true},
		{name: "false_is_a_value_not_an_absence", a: false, b: false, kind: KindBool, want: true},
		{name: "true_against_false", a: true, b: false, kind: KindBool, want: false},
		{name: "yes_against_no", a: "Yes", b: "No", kind: KindBool, want: false},
		{name: "unreadable_against_true", a: "maybe", b: true, kind: KindBool, want: false},
	})
}

// TestSlices covers the length rule and element-wise comparison, including a
// slice whose elements are formatted differently in each reading.
func TestSlices(t *testing.T) {
	t.Parallel()
	run(t, []equalCase{
		{name: "elements_case_folded", a: []string{"Acme Ltd", "Beta"}, b: []string{"ACME LTD", "BETA"}, kind: KindSlice, want: true},
		{name: "elements_formatted_differently", a: []any{"25,000", "2,500"}, b: []any{25000.0, 2500.0}, kind: KindSlice, want: true},
		{name: "numeric_elements", a: []float64{1, 2, 3}, b: []float64{1, 2, 3}, kind: KindSlice, want: true},
		{name: "nested_slices", a: [][]string{{"a"}, {"b"}}, b: [][]string{{"A"}, {"B"}}, kind: KindSlice, want: true},
		{name: "one_element_differs", a: []any{"25,000", "2,500"}, b: []any{25000.0, 250.0}, kind: KindSlice, want: false},
		{name: "different_lengths", a: []string{"a", "b"}, b: []string{"a"}, kind: KindSlice, want: false},
		{name: "kind_inferred_as_slice", a: []string{"a"}, b: []string{"A"}, kind: KindUnknown, want: true},
		{name: "array_against_slice", a: [2]string{"a", "b"}, b: []string{"A", "B"}, kind: KindSlice, want: true},
	})
}

// TestNotApplicable checks the comparisons that produce no agreement signal at
// all, which the scorer must treat as absent rather than as zero.
func TestNotApplicable(t *testing.T) {
	t.Parallel()
	type nested struct{ Name string }
	cases := []struct {
		name       string
		a, b       any
		kind       Kind
		wantReason string
	}{
		{name: "neither_reading_found_it", a: nil, b: nil, kind: KindString, wantReason: ReasonNone},
		{name: "both_blank", a: "", b: "   ", kind: KindString, wantReason: ReasonNone},
		{name: "one_reading_found_nothing", a: "25,000", b: "", kind: KindNumber, wantReason: ReasonAbsent},
		{name: "one_reading_is_nil", a: nil, b: 25000.0, kind: KindNumber, wantReason: ReasonAbsent},
		{name: "nil_pointer", a: (*string)(nil), b: "Acme", kind: KindString, wantReason: ReasonAbsent},
		{name: "zero_time_is_absent", a: time.Time{}, b: time.Time{}, kind: KindDate, wantReason: ReasonNone},
		{name: "empty_slices", a: []string{}, b: []string(nil), kind: KindSlice, wantReason: ReasonNone},
		{name: "structs_are_compared_field_by_field", a: nested{"a"}, b: nested{"a"}, kind: KindUnknown, wantReason: ReasonKind},
		{name: "a_slice_against_a_scalar", a: []string{"a"}, b: "a", kind: KindSlice, wantReason: ReasonKind},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Values(tc.a, tc.b, tc.kind)
			if got.Applicable {
				t.Errorf("Applicable = true, want false")
			}
			if got.Equal {
				t.Errorf("Equal = true, want false for a comparison that was not made")
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if _, ok := got.Signal(); ok {
				t.Errorf("Signal reported a value for an inapplicable comparison")
			}
		})
	}
}

// TestZeroIsNotAbsent guards the distinction the whole library turns on: a
// total of zero is a reading, not a missing one.
func TestZeroIsNotAbsent(t *testing.T) {
	t.Parallel()
	for _, v := range []any{0, 0.0, false, "0"} {
		if absent(v) {
			t.Errorf("absent(%#v) = true, want false: a zero is a value", v)
		}
	}
	got := Values(0.0, "0", KindNumber)
	if !got.Applicable || !got.Equal {
		t.Errorf("comparing two readings of zero: %+v, want an applicable agreement", got)
	}
}

// TestFallbackToText checks the conservative path: values that will not read
// as their declared kind are compared as text rather than dropped, so two
// readings of the same unparseable string still agree.
func TestFallbackToText(t *testing.T) {
	t.Parallel()
	same := Values("n/a", "N/A", KindNumber)
	if !same.Applicable || !same.Equal || !same.Fallback {
		t.Errorf("same unreadable text: %+v, want an applicable, equal, fallback comparison", same)
	}
	if same.Reason != ReasonFallback {
		t.Errorf("Reason = %q, want %q", same.Reason, ReasonFallback)
	}
	diff := Values("n/a", "none", KindNumber)
	if !diff.Applicable || diff.Equal {
		t.Errorf("different unreadable text: %+v, want an applicable disagreement", diff)
	}
	if diff.Reason != ReasonDisagree {
		t.Errorf("Reason = %q, want %q", diff.Reason, ReasonDisagree)
	}
}

// TestSignal checks the two-result shape that keeps an absent signal from
// being read as a zero one.
func TestSignal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		result    Result
		wantValue float64
		wantOK    bool
	}{
		{name: "agreement", result: Result{Equal: true, Applicable: true}, wantValue: Agree, wantOK: true},
		{name: "disagreement", result: Result{Applicable: true}, wantValue: Disagree, wantOK: true},
		{name: "no_comparison", result: Result{}, wantValue: 0, wantOK: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, ok := tc.result.Signal()
			if v != tc.wantValue || ok != tc.wantOK {
				t.Errorf("Signal() = (%v, %v), want (%v, %v)", v, ok, tc.wantValue, tc.wantOK)
			}
		})
	}
}

// TestKindOf checks that this package's inference is ground's, since a second
// one would be the drift the alias exists to prevent.
func TestKindOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    any
		want Kind
	}{
		{name: "string", v: "a", want: KindString},
		{name: "float", v: 1.0, want: KindNumber},
		{name: "int", v: 1, want: KindNumber},
		{name: "bool", v: true, want: KindBool},
		{name: "time", v: time.Now(), want: KindDate},
		{name: "slice", v: []string{"a"}, want: KindSlice},
		{name: "struct", v: struct{}{}, want: KindUnknown},
		{name: "nil", v: nil, want: KindUnknown},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := KindOf(tc.v); got != tc.want {
				t.Errorf("KindOf = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInferredKindPrefersTheTypedReading records why inference is not simply
// "whatever the first value is": a float64 from one reading and formatted text
// from the other must compare as numbers, or every formatted figure in every
// document reports a disagreement.
func TestInferredKindPrefersTheTypedReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b any
		want Kind
	}{
		{name: "text_and_number", a: "25,000", b: 25000.0, want: KindNumber},
		{name: "number_and_text", a: 25000.0, b: "25,000", want: KindNumber},
		{name: "text_and_date", a: "2026-04-03", b: time.Now(), want: KindDate},
		{name: "two_strings", a: "a", b: "b", want: KindString},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := inferKind(tc.a, tc.b); got != tc.want {
				t.Errorf("inferKind = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMaxDepth checks that the descent into nested slices is bounded before it
// recurses, not after (docs/rules.md §5.2).
func TestMaxDepth(t *testing.T) {
	t.Parallel()
	var a, b any = "x", "x"
	for i := 0; i <= MaxDepth+2; i++ {
		a, b = []any{a}, []any{b}
	}
	got := Values(a, b, KindSlice)
	if got.Applicable {
		t.Errorf("Applicable = true at depth %d, want the limit to have bound: %+v", MaxDepth+3, got)
	}
}

// TestReasonsCarryNoValue is the rule that matters most here: a reason becomes
// a ReviewReason and a ReviewReason is logged (docs/rules.md §7.5). Every
// reason this package can produce is one of the constants.
func TestReasonsCarryNoValue(t *testing.T) {
	t.Parallel()
	pairs := [][2]any{
		{"25,000", "2,500"},
		{"25,000", "25000"},
		{"Acme Ltd", "Acme Limited"},
		{"", "x"},
		{nil, nil},
		{struct{ A int }{1}, struct{ A int }{2}},
		{[]string{"a"}, []string{"a", "b"}},
		{"n/a", "n/a"},
	}
	kinds := []Kind{KindUnknown, KindString, KindNumber, KindCurrency, KindDate, KindBool, KindSlice}
	for _, p := range pairs {
		for _, k := range kinds {
			r := Values(p[0], p[1], k)
			if !knownReason(r.Reason) {
				t.Errorf("Values(kind=%v) produced an unknown reason %q", k, r.Reason)
			}
		}
	}
}

// knownReason reports whether a reason is one of the declared constants.
func knownReason(s string) bool {
	switch s {
	case "", ReasonAbsent, ReasonNone, ReasonSingle, ReasonKind, ReasonDepth, ReasonFallback, ReasonDisagree:
		return true
	}
	return false
}

// TestThroughPointersAndNamedTypes checks the shapes a reflected schema
// actually produces: an optional field is a pointer, and a field declared as
// `type Amount float64` is not a float64 to a type switch.
func TestThroughPointersAndNamedTypes(t *testing.T) {
	t.Parallel()
	type amount float64
	type name string
	type flag bool

	s := "Acme Ltd"
	f := 25000.0
	b := true
	when := time.Date(2026, time.April, 3, 0, 0, 0, 0, time.UTC)
	a := amount(25000)
	n := name("ACME LTD")
	fl := flag(true)

	run(t, []equalCase{
		{name: "pointer_to_string", a: &s, b: "acme ltd", kind: KindString, want: true},
		{name: "pointer_to_float", a: &f, b: "25,000", kind: KindNumber, want: true},
		{name: "pointer_to_bool", a: &b, b: "Yes", kind: KindBool, want: true},
		{name: "pointer_to_time", a: &when, b: "03/04/26", kind: KindDate, want: true},
		{name: "named_float", a: a, b: "25,000", kind: KindNumber, want: true},
		{name: "named_float_inferred", a: a, b: "25,000", kind: KindUnknown, want: true},
		{name: "named_string", a: n, b: "Acme Ltd", kind: KindString, want: true},
		{name: "named_string_as_currency", a: name("$100"), b: "100 USD", kind: KindCurrency, want: true},
		{name: "named_string_as_date", a: name("2026-04-03"), b: "3 April 2026", kind: KindDate, want: true},
		{name: "named_bool", a: fl, b: "true", kind: KindBool, want: true},
		{name: "pointer_to_named_float", a: &a, b: 25000.0, kind: KindNumber, want: true},
		{name: "unsigned_integer", a: uint(42), b: "42", kind: KindNumber, want: true},
		{name: "narrow_integer", a: int32(42), b: 42.0, kind: KindNumber, want: true},
		{name: "narrow_float", a: float32(1.5), b: "1.5", kind: KindNumber, want: true},
		{name: "named_string_slice", a: []name{"Acme"}, b: []string{"ACME"}, kind: KindSlice, want: true},
	})
}

// TestNonFiniteNumbersFallBackToText records what happens to a value no
// document could contain. A NaN is not equal to itself, so comparing two of
// them as numbers would make comparison non-reflexive; they are compared as
// text instead, where two readings that produced the same nonsense agree that
// they did.
func TestNonFiniteNumbersFallBackToText(t *testing.T) {
	t.Parallel()
	nan := math.NaN()
	cases := []struct {
		name         string
		a, b         any
		wantEqual    bool
		wantFallback bool
	}{
		{name: "nan_against_itself", a: nan, b: nan, wantEqual: true, wantFallback: true},
		{name: "nan_against_a_number", a: nan, b: 1.0, wantEqual: false, wantFallback: true},
		{name: "infinity_against_itself", a: math.Inf(1), b: math.Inf(1), wantEqual: true, wantFallback: true},
		{name: "infinity_against_negative_infinity", a: math.Inf(1), b: math.Inf(-1), wantEqual: false, wantFallback: true},
		{name: "very_large_but_finite", a: 1e308, b: 1e308, wantEqual: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Values(tc.a, tc.b, KindNumber)
			if !got.Applicable {
				t.Fatalf("Applicable = false: %+v", got)
			}
			if got.Equal != tc.wantEqual || got.Fallback != tc.wantFallback {
				t.Errorf("Equal = %v, Fallback = %v, want %v and %v", got.Equal, got.Fallback, tc.wantEqual, tc.wantFallback)
			}
		})
	}
}

// TestAbsent covers the shapes that mean "this reading found nothing", since
// getting one of them wrong turns a missing field into a false agreement.
func TestAbsent(t *testing.T) {
	t.Parallel()
	type named string
	var nilMap map[string]int
	var nilSlice []string
	var nilPtr *float64
	empty := ""

	cases := []struct {
		name string
		v    any
		want bool
	}{
		{name: "nil", v: nil, want: true},
		{name: "empty_string", v: "", want: true},
		{name: "blank_string", v: " \t\n ", want: true},
		{name: "named_empty_string", v: named(""), want: true},
		{name: "nil_pointer", v: nilPtr, want: true},
		{name: "pointer_to_empty_string", v: &empty, want: true},
		{name: "nil_map", v: nilMap, want: true},
		{name: "empty_map", v: map[string]int{}, want: true},
		{name: "nil_slice", v: nilSlice, want: true},
		{name: "empty_array", v: [0]string{}, want: true},
		{name: "zero_time", v: time.Time{}, want: true},
		{name: "text", v: "x", want: false},
		{name: "named_text", v: named("x"), want: false},
		{name: "populated_map", v: map[string]int{"a": 1}, want: false},
		{name: "populated_slice", v: []string{"a"}, want: false},
		{name: "a_real_date", v: time.Date(2026, time.April, 3, 0, 0, 0, 0, time.UTC), want: false},
		{name: "struct", v: struct{ A int }{}, want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := absent(tc.v); got != tc.want {
				t.Errorf("absent = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFoldFullCaseFolding pins the foldings that [strings.ToLower] gets wrong,
// because they are the ones a German or Greek document depends on and the ones
// nobody would notice were missing.
func TestFoldFullCaseFolding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b string
	}{
		{name: "sharp_s", a: "ß", b: "ss"},
		{name: "sharp_s_is_not_folded_when_case_matters", a: "ß", b: "ß"},
		{name: "capital_sharp_s", a: "ẞ", b: "SS"},
		{name: "final_sigma", a: "ς", b: "Σ"},
		{name: "ypogegrammeni", a: "ͅ", b: "ι"},
		{name: "armenian_ligature", a: "ﬓ", b: "մն"},
		{name: "apostrophe_n", a: "ŉ", b: "ʼn"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if fold(tc.a, false) != fold(tc.b, false) {
				t.Errorf("fold(%q) = %q, fold(%q) = %q, want them equal", tc.a, fold(tc.a, false), tc.b, fold(tc.b, false))
			}
		})
	}
}
