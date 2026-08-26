package compare

import (
	"reflect"
	"testing"
)

// TestFieldAgreement covers the outcomes the pipeline branches on: agreement,
// disagreement, and the cases where there is no signal to report.
func TestFieldAgreement(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		kind           Kind
		readings       []Candidate
		wantAgree      bool
		wantApplicable bool
		wantReason     string
		wantBest       any
		wantCandidates int
		wantMissing    []Reading
	}{
		{
			name: "two_readings_of_one_number_agree",
			kind: KindNumber,
			readings: []Candidate{
				{Value: "25,000", Reading: ReadingOCR, Confidence: 0.7},
				{Value: 25000.0, Reading: ReadingVision, Confidence: 0.9},
			},
			wantAgree: true, wantApplicable: true, wantBest: 25000.0, wantCandidates: 2,
		},
		{
			name: "the_adr_case",
			kind: KindNumber,
			readings: []Candidate{
				{Value: "25,000", Reading: ReadingOCR, Confidence: 0.7},
				{Value: "2,500", Reading: ReadingVision, Confidence: 0.9},
			},
			wantApplicable: true, wantReason: ReasonDisagree, wantBest: "2,500", wantCandidates: 2,
		},
		{
			name: "a_single_reading_produces_no_agreement_signal",
			kind: KindNumber,
			readings: []Candidate{
				{Value: "25,000", Reading: ReadingText, Confidence: 0.8},
			},
			wantReason: ReasonSingle, wantBest: "25,000", wantCandidates: 1,
		},
		{
			name: "a_reading_that_found_nothing_is_not_a_disagreement",
			kind: KindNumber,
			readings: []Candidate{
				{Value: "25,000", Reading: ReadingOCR, Confidence: 0.7},
				{Value: "", Reading: ReadingVision, Confidence: 0.9},
			},
			wantReason: ReasonAbsent, wantBest: "25,000", wantCandidates: 1,
			wantMissing: []Reading{ReadingVision},
		},
		{
			name:       "no_reading_found_anything",
			kind:       KindNumber,
			readings:   []Candidate{{Reading: ReadingOCR}, {Reading: ReadingVision}},
			wantReason: ReasonNone, wantMissing: []Reading{ReadingOCR, ReadingVision},
		},
		{
			name:       "no_readings_at_all",
			kind:       KindNumber,
			wantReason: ReasonNone,
		},
		{
			name: "three_readings_where_one_dissents",
			kind: KindNumber,
			readings: []Candidate{
				{Value: "25,000", Reading: ReadingText, Confidence: 0.6},
				{Value: 25000.0, Reading: ReadingOCR, Confidence: 0.7},
				{Value: 2500.0, Reading: ReadingVision, Confidence: 0.9},
			},
			wantApplicable: true, wantReason: ReasonDisagree, wantBest: 2500.0, wantCandidates: 3,
		},
		{
			name: "three_readings_that_all_agree",
			kind: KindCurrency,
			readings: []Candidate{
				{Value: "$25,000.00", Reading: ReadingText, Confidence: 0.6},
				{Value: "USD 25 000", Reading: ReadingOCR, Confidence: 0.7},
				{Value: "25000 USD", Reading: ReadingVision, Confidence: 0.9},
			},
			wantAgree: true, wantApplicable: true, wantBest: "25000 USD", wantCandidates: 3,
		},
		{
			name: "values_with_no_comparison_kind",
			kind: KindUnknown,
			readings: []Candidate{
				{Value: struct{ A int }{1}, Reading: ReadingText, Confidence: 0.5},
				{Value: struct{ A int }{1}, Reading: ReadingOCR, Confidence: 0.6},
			},
			wantReason: ReasonKind, wantBest: struct{ A int }{1}, wantCandidates: 2,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Field(tc.kind, tc.readings)
			if got.Agree != tc.wantAgree {
				t.Errorf("Agree = %v, want %v", got.Agree, tc.wantAgree)
			}
			if got.Applicable != tc.wantApplicable {
				t.Errorf("Applicable = %v, want %v", got.Applicable, tc.wantApplicable)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if !reflect.DeepEqual(got.Best.Value, tc.wantBest) {
				t.Errorf("Best.Value = %#v, want %#v", got.Best.Value, tc.wantBest)
			}
			if len(got.Candidates) != tc.wantCandidates {
				t.Errorf("len(Candidates) = %d, want %d", len(got.Candidates), tc.wantCandidates)
			}
			if !reflect.DeepEqual(got.Missing, tc.wantMissing) {
				t.Errorf("Missing = %v, want %v", got.Missing, tc.wantMissing)
			}
			if v, ok := got.Signal(); ok != tc.wantApplicable {
				t.Errorf("Signal() reported ok = %v, want %v", ok, tc.wantApplicable)
			} else if ok && ((tc.wantAgree && v != Agree) || (!tc.wantAgree && v != Disagree)) {
				t.Errorf("Signal() = %v, want the %v case", v, tc.wantAgree)
			}
		})
	}
}

// TestFieldKeepsEveryReading is ADR-0014's requirement that nothing is
// discarded, asserted directly: after a disagreement, both values, both
// readings and both confidences survive.
func TestFieldKeepsEveryReading(t *testing.T) {
	t.Parallel()
	readings := []Candidate{
		{Value: "25,000", Reading: ReadingOCR, Confidence: 0.71},
		{Value: "2,500", Reading: ReadingVision, Confidence: 0.93},
	}
	got := Field(KindNumber, readings)
	if got.Agree {
		t.Fatalf("Agree = true, want the disagreement to be reported")
	}
	want := []Candidate{readings[1], readings[0]}
	if !reflect.DeepEqual(got.Candidates, want) {
		t.Errorf("Candidates = %+v, want %+v", got.Candidates, want)
	}
	if !reflect.DeepEqual(readings[0], Candidate{Value: "25,000", Reading: ReadingOCR, Confidence: 0.71}) {
		t.Errorf("Field modified the readings it was given")
	}
}

// TestRank checks the ordering the caller relies on to pick a value to show,
// and the stability that keeps a review queue from reshuffling between runs of
// the same document.
func TestRank(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    []Candidate
		want  []Reading
		empty bool
	}{
		{
			name: "highest_confidence_first",
			in: []Candidate{
				{Reading: ReadingText, Confidence: 0.4},
				{Reading: ReadingOCR, Confidence: 0.9},
				{Reading: ReadingVision, Confidence: 0.6},
			},
			want: []Reading{ReadingOCR, ReadingVision, ReadingText},
		},
		{
			name: "equal_confidence_keeps_the_callers_order",
			in: []Candidate{
				{Reading: ReadingVision, Confidence: 0.5},
				{Reading: ReadingText, Confidence: 0.5},
				{Reading: ReadingOCR, Confidence: 0.5},
			},
			want: []Reading{ReadingVision, ReadingText, ReadingOCR},
		},
		{
			name: "no_confidence_anywhere_keeps_the_callers_order",
			in: []Candidate{
				{Reading: ReadingOCR},
				{Reading: ReadingText},
			},
			want: []Reading{ReadingOCR, ReadingText},
		},
		{name: "empty", empty: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := make([]Candidate, len(tc.in))
			copy(before, tc.in)
			got := Rank(tc.in)
			if tc.empty {
				if got != nil {
					t.Fatalf("Rank(nil) = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i, r := range tc.want {
				if got[i].Reading != r {
					t.Errorf("position %d is %v, want %v", i, got[i].Reading, r)
				}
			}
			if !reflect.DeepEqual(tc.in, before) {
				t.Errorf("Rank modified its input")
			}
		})
	}
}

// TestRankIsStableAcrossRuns guards the property directly, because a ranking
// that depended on map iteration order would pass a single run of the test
// above and fail in production a fortnight later.
func TestRankIsStableAcrossRuns(t *testing.T) {
	t.Parallel()
	in := []Candidate{
		{Value: "a", Reading: ReadingText, Confidence: 0.5},
		{Value: "b", Reading: ReadingOCR, Confidence: 0.5},
		{Value: "c", Reading: ReadingVision, Confidence: 0.5},
	}
	first := Rank(in)
	for i := 0; i < 64; i++ {
		if !reflect.DeepEqual(Rank(in), first) {
			t.Fatalf("Rank is not deterministic")
		}
	}
}

// TestFieldPairwise records why agreement is every pair rather than every
// reading against the first: an ambiguous date matches either reading of
// itself, generosity is not transitive, and comparing against the first alone
// would make the answer depend on the order the readings arrived in.
func TestFieldPairwise(t *testing.T) {
	t.Parallel()
	ambiguous := Candidate{Value: "03/04/2026", Reading: ReadingText, Confidence: 0.5}
	dayFirst := Candidate{Value: "2026-04-03", Reading: ReadingOCR, Confidence: 0.6}
	monthFirst := Candidate{Value: "2026-03-04", Reading: ReadingVision, Confidence: 0.7}

	if !Field(KindDate, []Candidate{ambiguous, dayFirst}).Agree {
		t.Errorf("an ambiguous date should agree with its day-first reading")
	}
	if !Field(KindDate, []Candidate{ambiguous, monthFirst}).Agree {
		t.Errorf("an ambiguous date should agree with its month-first reading")
	}
	got := Field(KindDate, []Candidate{ambiguous, dayFirst, monthFirst})
	if got.Agree {
		t.Errorf("Agree = true, want the two unambiguous readings to be compared with each other")
	}
	if got.Reason != ReasonDisagree {
		t.Errorf("Reason = %q, want %q", got.Reason, ReasonDisagree)
	}
}

// TestFieldFallback checks that a value which would not read as its declared
// kind is reported as such even when the readings agree about it.
func TestFieldFallback(t *testing.T) {
	t.Parallel()
	got := Field(KindNumber, []Candidate{
		{Value: "n/a", Reading: ReadingOCR, Confidence: 0.4},
		{Value: "N/A", Reading: ReadingVision, Confidence: 0.5},
	})
	if !got.Agree || !got.Applicable {
		t.Fatalf("%+v, want an applicable agreement", got)
	}
	if !got.Fallback || got.Reason != ReasonFallback {
		t.Errorf("Fallback = %v, Reason = %q, want the fallback reported", got.Fallback, got.Reason)
	}
}

// TestReadingString keeps a message from ever reading "reading=".
func TestReadingString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		r    Reading
		want string
	}{
		{name: "unknown", r: ReadingUnknown, want: "unknown"},
		{name: "text", r: ReadingText, want: "text"},
		{name: "ocr", r: ReadingOCR, want: "ocr"},
		{name: "vision", r: ReadingVision, want: "vision"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.r.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFieldReasonsCarryNoValue holds [FieldResult] to the same rule [Result]
// is held to: a reason is logged, and document content is not.
func TestFieldReasonsCarryNoValue(t *testing.T) {
	t.Parallel()
	sets := [][]Candidate{
		{{Value: "25,000"}, {Value: "2,500"}},
		{{Value: "25,000"}, {Value: "25000"}},
		{{Value: "Acme Ltd"}, {Value: ""}},
		{{Value: nil}},
		{{Value: []string{"a"}}, {Value: []string{"a", "b"}}},
		{{Value: struct{ A int }{1}}, {Value: struct{ A int }{2}}},
	}
	kinds := []Kind{KindUnknown, KindString, KindNumber, KindCurrency, KindDate, KindBool, KindSlice}
	for _, set := range sets {
		for _, k := range kinds {
			if r := Field(k, set); !knownReason(r.Reason) {
				t.Errorf("Field(kind=%v) produced an unknown reason %q", k, r.Reason)
			}
		}
	}
}
