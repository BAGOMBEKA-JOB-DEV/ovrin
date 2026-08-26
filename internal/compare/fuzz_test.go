package compare

import "testing"

// fuzzKinds is every kind a fuzzed comparison can be asked for, indexed by a
// byte of the input so the fuzzer explores all of them.
var fuzzKinds = []Kind{KindUnknown, KindString, KindNumber, KindCurrency, KindDate, KindBool, KindSlice}

// FuzzCompare asserts the three properties a comparison must hold whatever it
// is given, because each of them breaks quietly.
//
//   - It never panics. The inputs are document text; a panic here is a denial
//     of service reachable from a PDF (docs/threat-model.md).
//   - It is symmetric. An asymmetric comparison would send a field to review
//     depending on which reading the pipeline happened to list first, and no
//     test of specific values would catch it.
//   - It is reflexive wherever it applies at all. A value that disagreed with
//     itself would flag every field of every document, which is the noise
//     failure ADR-0014 names as the cost of this feature.
//
// It also asserts that no reason ever carries a value, which is the one
// property here that is a security boundary rather than a correctness one
// (docs/rules.md §7.5).
func FuzzCompare(f *testing.F) {
	seeds := [][2]string{
		{"25,000", "2,500"},
		{"25,000", "25000"},
		{"25 000", "25000"},
		{"$1,234.56", "1234.56 USD"},
		{"100 USD", "100 EUR"},
		{"03/04/26", "2026-04-03"},
		{"2026-04-03T14:30:00+03:00", "2026-04-03"},
		{"ACME LTD", "Acme Ltd"},
		{"Acme Ltd", "Acme Limited"},
		{"Straße", "STRASSE"},
		{"café", "café"},
		{"Yes", "true"},
		{"(1,234.56)", "-1234.56"},
		{"１２３", "123"},
		{"", ""},
		{"\x00\xff", "\xff\x00"},
		{"1,234,567.89", "1.234.567,89"},
	}
	for _, s := range seeds {
		for k := range fuzzKinds {
			f.Add(s[0], s[1], uint8(k), uint8(0))
		}
	}

	f.Fuzz(func(t *testing.T, a, b string, kindIndex, flags uint8) {
		kind := fuzzKinds[int(kindIndex)%len(fuzzKinds)]
		var opts []Option
		if flags&1 != 0 {
			opts = append(opts, WithCaseSensitive())
		}
		if flags&2 != 0 {
			opts = append(opts, WithDateOrder(DayFirst))
		}
		if flags&4 != 0 {
			opts = append(opts, WithDateOrder(MonthFirst))
		}

		forward := Values(a, b, kind, opts...)
		reverse := Values(b, a, kind, opts...)
		if forward.Equal != reverse.Equal || forward.Applicable != reverse.Applicable {
			t.Fatalf("not symmetric: forward %+v, reverse %+v", forward, reverse)
		}
		if forward.Equal && !forward.Applicable {
			t.Fatalf("equal without a comparison having been made: %+v", forward)
		}
		if !knownReason(forward.Reason) {
			t.Fatalf("reason is not one of the constants: %q", forward.Reason)
		}
		if v, ok := forward.Signal(); ok != forward.Applicable || (ok && v != Agree && v != Disagree) {
			t.Fatalf("Signal() = (%v, %v) for %+v", v, ok, forward)
		}

		for _, s := range []string{a, b} {
			if self := Values(s, s, kind, opts...); self.Applicable && !self.Equal {
				t.Fatalf("not reflexive for kind %v: %+v", kind, self)
			}
		}

		// The candidate path runs the same comparison and must reach the same
		// answer, since the pipeline uses it and nothing else.
		field := Field(kind, []Candidate{
			{Value: a, Reading: ReadingOCR, Confidence: 0.5},
			{Value: b, Reading: ReadingVision, Confidence: 0.6},
		}, opts...)
		if field.Applicable != forward.Applicable || field.Agree != forward.Equal {
			t.Fatalf("Field %+v disagrees with Values %+v", field, forward)
		}
		if !knownReason(field.Reason) {
			t.Fatalf("field reason is not one of the constants: %q", field.Reason)
		}
	})
}
