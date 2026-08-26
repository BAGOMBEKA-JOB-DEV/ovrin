package eval

import (
	"math"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// caseFrom builds a scored case from two flat maps, so a table can state a
// scenario in one line without constructing an ovrin.Result.
func caseFrom(expected map[string]any, produced map[string]Observed, exclude ...string) Case {
	return Case{
		Document: Document{
			Category: "invoices", Name: "001",
			Meta:     Meta{Difficulty: "clean-digital", Exclude: exclude, Pages: 1},
			Expected: expected,
		},
		Observation: Observation{Fields: produced},
	}
}

// found is an extracted value with a confidence.
func found(v any, conf float64) Observed { return Observed{Value: v, Found: true, Confidence: conf} }

// missing is a field the extractor reported as absent. It carries a confidence
// because the pipeline still scores absent fields, and the harness must not
// count that number as a claim about a value.
func missing() Observed { return Observed{Found: false, Confidence: 0.2} }

// TestTally covers the confusion matrix, which every ratio is derived from.
func TestTally(t *testing.T) {
	cases := []struct {
		name string
		c    Case
		want Tally
	}{
		{
			name: "a value found and correct",
			c: caseFrom(
				map[string]any{"total": num("1000")},
				map[string]Observed{"total": found(1000.0, 0.9)},
			),
			want: Tally{Expected: 1, Produced: 1, Correct: 1},
		},
		{
			name: "a value found and wrong",
			c: caseFrom(
				map[string]any{"total": num("1000")},
				map[string]Observed{"total": found(100.0, 0.9)},
			),
			want: Tally{Expected: 1, Produced: 1, Wrong: 1},
		},
		{
			name: "a value present in the document and not found",
			c: caseFrom(
				map[string]any{"total": num("1000")},
				map[string]Observed{"total": missing()},
			),
			want: Tally{Expected: 1, Missed: 1},
		},
		{
			name: "a value present in the document and reported by no field entry",
			c: caseFrom(
				map[string]any{"total": num("1000")},
				map[string]Observed{},
			),
			want: Tally{Expected: 1, Missed: 1},
		},
		{
			name: "a field absent from the document and correctly not produced",
			c: caseFrom(
				map[string]any{},
				map[string]Observed{"due": missing()},
			),
			want: Tally{Absent: 1},
		},
		{
			name: "a field absent from the document with a value invented for it",
			c: caseFrom(
				map[string]any{},
				map[string]Observed{"due": found("2026-04-13", 0.8)},
			),
			want: Tally{Absent: 1, Produced: 1, Fabricated: 1},
		},
		{
			name: "an ambiguous field is excluded rather than scored",
			c: caseFrom(
				map[string]any{"due": "2026-04-13"},
				map[string]Observed{"due": found("2026-03-04", 0.8)},
				"due",
			),
			want: Tally{Excluded: 1},
		},
		{
			name: "a slice container is not scored alongside its members",
			c: caseFrom(
				map[string]any{"items[0].amount": num("500")},
				map[string]Observed{
					"items":           found([]any{}, 0.9),
					"items[0].amount": found(500.0, 0.9),
				},
			),
			want: Tally{Expected: 1, Produced: 1, Correct: 1},
		},
		{
			name: "a mixed document",
			c: caseFrom(
				map[string]any{
					"number": "INV-1", "total": num("1000"), "issued": "2026-03-14",
				},
				map[string]Observed{
					"number": found("INV-1", 0.95),     // correct
					"total":  found(999.0, 0.8),        // wrong
					"issued": missing(),                // missed
					"due":    found("2026-04-01", 0.7), // fabricated
					"po":     missing(),                // correctly absent
				},
			),
			want: Tally{Expected: 3, Produced: 3, Correct: 1, Wrong: 1, Missed: 1, Absent: 2, Fabricated: 1},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := tallyOf(judge(c.c))
			if got != c.want {
				t.Errorf("tally = %+v\nwant     %+v", got, c.want)
			}
		})
	}
}

// TestMetricsRatios checks that each ratio uses the denominator its
// documentation names. Getting one wrong produces a plausible number that
// nobody can reproduce, which is exactly what rule §3.8 forbids publishing.
func TestMetricsRatios(t *testing.T) {
	m := metricsOf(Tally{
		Expected: 10, Produced: 12, Correct: 6, Wrong: 3, Missed: 1,
		Absent: 4, Fabricated: 3,
	})
	want := []struct {
		name string
		got  float64
		want float64
	}{
		{"exact is correct over expected", m.Exact, 0.6},
		{"precision is correct over produced", m.Precision, 0.5},
		{"recall is found over expected", m.Recall, 0.9},
		{"fabrication is fabricated over absent", m.Fabrication, 0.75},
	}
	for _, w := range want {
		if math.Abs(w.got-w.want) > 1e-9 {
			t.Errorf("%s: got %.4f, want %.4f", w.name, w.got, w.want)
		}
	}
}

// TestMetricsOverNothingAreZeroNotNaN guards the aggregate. A NaN propagates
// through every sum that touches it, so one empty bucket would turn an entire
// report into a page of NaN.
func TestMetricsOverNothingAreZeroNotNaN(t *testing.T) {
	m := metricsOf(Tally{})
	for name, v := range map[string]float64{
		"exact": m.Exact, "precision": m.Precision,
		"recall": m.Recall, "fabrication": m.Fabrication,
	} {
		if math.IsNaN(v) || v != 0 {
			t.Errorf("%s over an empty tally = %v, want 0", name, v)
		}
	}
}

// TestCalibration covers ECE, the confidence bands and the risk-coverage
// curve, on a set of judgements chosen so every number can be checked by hand.
func TestCalibration(t *testing.T) {
	// Six produced values. Four score 0.95 and three of those are right; two
	// score 0.45 and neither is right.
	js := []judgement{
		{produced: true, correct: true, confidence: 0.95},
		{produced: true, correct: true, confidence: 0.95},
		{produced: true, correct: true, confidence: 0.95},
		{produced: true, correct: false, confidence: 0.95},
		{produced: true, correct: false, confidence: 0.45},
		{produced: true, correct: false, confidence: 0.45, flagged: true},
		// An absent field. It carries no confidence claim about a value and
		// must not appear in any calibration denominator.
		{produced: false, wanted: true, flagged: true},
	}
	cal := calibrationOf(js)

	if cal.Scored != 6 {
		t.Errorf("scored = %d, want 6; an absent field leaked into calibration", cal.Scored)
	}

	// Bin [0.9,1.0): four values, accuracy 0.75, mean confidence 0.95.
	// Bin [0.4,0.5): two values, accuracy 0.00, mean confidence 0.45.
	// ECE = 4/6*|0.75-0.95| + 2/6*|0.00-0.45| = 0.13333 + 0.15 = 0.283333
	if math.Abs(cal.ECE-0.2833333333) > 1e-6 {
		t.Errorf("ECE = %.6f, want 0.283333", cal.ECE)
	}

	top := cal.Bands[0]
	if top.N != 4 || math.Abs(top.Accuracy-0.75) > 1e-9 {
		t.Errorf("band %s: n=%d accuracy=%.2f, want n=4 accuracy=0.75", top.Label, top.N, top.Accuracy)
	}
	low := cal.Bands[2]
	if low.N != 2 || low.Accuracy != 0 {
		t.Errorf("band %s: n=%d accuracy=%.2f, want n=2 accuracy=0.00", low.Label, low.N, low.Accuracy)
	}
	if mid := cal.Bands[1]; mid.N != 0 {
		t.Errorf("band %s: n=%d, want 0", mid.Label, mid.N)
	}

	// Auto-accepting at 0.90 covers four of six and carries one error in four.
	at90 := riskAt(cal.Risk, 0.90)
	if at90.N != 4 || math.Abs(at90.Coverage-4.0/6) > 1e-9 || math.Abs(at90.Error-0.25) > 1e-9 {
		t.Errorf("at 0.90: n=%d coverage=%.4f error=%.4f, want n=4 coverage=0.6667 error=0.2500",
			at90.N, at90.Coverage, at90.Error)
	}
	// At 0.00 everything is accepted and the error rate is the overall one.
	at0 := riskAt(cal.Risk, 0.0)
	if at0.N != 6 || math.Abs(at0.Coverage-1) > 1e-9 || math.Abs(at0.Error-0.5) > 1e-9 {
		t.Errorf("at 0.00: n=%d coverage=%.4f error=%.4f, want n=6 coverage=1 error=0.5",
			at0.N, at0.Coverage, at0.Error)
	}
	// The curve must be monotonically non-increasing in coverage: raising the
	// threshold cannot accept more values.
	for i := 1; i < len(cal.Risk); i++ {
		if cal.Risk[i].Coverage > cal.Risk[i-1].Coverage+1e-12 {
			t.Errorf("coverage rose from %.4f to %.4f as the threshold rose",
				cal.Risk[i-1].Coverage, cal.Risk[i].Coverage)
		}
	}

	// Two fields were flagged and both were wrong — one produced-and-wrong,
	// one absent-and-required.
	if cal.Flagged != 2 || math.Abs(cal.ReviewPrecision-1.0) > 1e-9 {
		t.Errorf("flagged=%d review precision=%.2f, want 2 and 1.00", cal.Flagged, cal.ReviewPrecision)
	}
}

// TestCalibrationOfNothing checks the empty case, which is what a committed
// report looks like before anybody has run the harness.
func TestCalibrationOfNothing(t *testing.T) {
	cal := calibrationOf(nil)
	if math.IsNaN(cal.ECE) || cal.ECE != 0 {
		t.Errorf("ECE over nothing = %v, want 0", cal.ECE)
	}
	if len(cal.Bins) != 10 {
		t.Errorf("bins = %d, want 10 even when empty", len(cal.Bins))
	}
	for _, p := range cal.Risk {
		if math.IsNaN(p.Coverage) || math.IsNaN(p.Error) {
			t.Errorf("risk point at %.2f is NaN", p.Threshold)
		}
	}
}

// TestCost covers usage totals, pricing and the latency quantiles.
func TestCost(t *testing.T) {
	mk := func(in, out, pages int, d time.Duration) Case {
		return Case{
			Document:    Document{Meta: Meta{Pages: pages}},
			Observation: Observation{Usage: ovrin.Usage{InputTokens: in, OutputTokens: out, PageUnits: pages}, Duration: d},
		}
	}
	cases := []Case{
		mk(1000, 100, 1, 2*time.Second),
		mk(2000, 200, 2, 4*time.Second),
		mk(3000, 300, 1, 6*time.Second),
		mk(4000, 400, 1, 20*time.Second),
	}

	unpriced := costOf(cases, Prices{})
	if unpriced.Priced {
		t.Error("a Prices with every field zero produced a priced Cost")
	}
	if unpriced.USDPerDocument != 0 {
		t.Errorf("USDPerDocument = %v with no price table, want 0", unpriced.USDPerDocument)
	}
	if unpriced.Usage.InputTokens != 10000 || unpriced.Usage.OutputTokens != 1000 || unpriced.Usage.PageUnits != 5 {
		t.Errorf("usage = %+v", unpriced.Usage)
	}
	if unpriced.MedianLatency != 4*time.Second {
		t.Errorf("median latency = %v, want 4s", unpriced.MedianLatency)
	}
	if unpriced.P95Latency != 20*time.Second {
		t.Errorf("p95 latency = %v, want 20s", unpriced.P95Latency)
	}

	priced := costOf(cases, Prices{USDPerInputToken: 1e-6, USDPerOutputToken: 4e-6, USDPerPageUnit: 0.0015})
	// 10000*1e-6 + 1000*4e-6 + 5*0.0015 = 0.01 + 0.004 + 0.0075 = 0.0215
	if !priced.Priced {
		t.Fatal("a Prices with a value produced an unpriced Cost")
	}
	if math.Abs(priced.USDPerDocument-0.0215/4) > 1e-12 {
		t.Errorf("USDPerDocument = %.8f, want %.8f", priced.USDPerDocument, 0.0215/4)
	}
	if math.Abs(priced.USDPerPage-0.0215/5) > 1e-12 {
		t.Errorf("USDPerPage = %.8f, want %.8f", priced.USDPerPage, 0.0215/5)
	}
}

// TestQuantile covers the nearest-rank definition, which never invents a
// duration between two observations.
func TestQuantile(t *testing.T) {
	s := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}
	cases := []struct {
		name string
		q    float64
		want time.Duration
	}{
		{"the median of four", 0.5, 2 * time.Second},
		{"the 95th percentile of four", 0.95, 4 * time.Second},
		{"the minimum", 0, time.Second},
		{"the maximum", 1, 4 * time.Second},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := quantile(s, c.q); got != c.want {
				t.Errorf("quantile(%.2f) = %v, want %v", c.q, got, c.want)
			}
		})
	}
	if got := quantile(nil, 0.5); got != 0 {
		t.Errorf("quantile of nothing = %v, want 0", got)
	}
}
