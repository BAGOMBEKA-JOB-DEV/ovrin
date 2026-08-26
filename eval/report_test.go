package eval

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// update rewrites the committed golden report. Run it as
//
//	go test ./eval/ -run TestNoRunReport -update
//
// after a deliberate change to the report format.
var update = flag.Bool("update", false, "rewrite the committed no-run report")

// noRunNote is the caveat that travels with a report nobody has run.
//
// rules §3.8 forbids claiming an accuracy figure the harness cannot reproduce,
// and a page of 0.00 with no explanation is exactly such a claim read the
// wrong way round. The note is what makes the zeros honest.
const noRunNote = "No run has happened. Every figure below is a zero produced by the report " +
	"renderer over an empty set of results, not a measurement. Nothing in this " +
	"repository may cite it as an accuracy figure."

// noRunReport is the report a run over nothing produces.
func noRunReport() *Report {
	r := Score(nil, Prices{})
	r.Generated = time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	r.Corpus = "eval/corpus"
	r.Note = noRunNote
	return r
}

// TestNoRunReport keeps the committed zero report in step with the renderer.
//
// The report is committed even though it is empty, for the reason ADR-0023
// gives: reports are committed even when they are bad, because a public record
// of how wrong we are is the only kind that stays honest. Generating it here
// rather than writing it by hand also means the committed file is provably the
// renderer's own output — nobody can quietly improve the numbers in it without
// this test failing.
func TestNoRunReport(t *testing.T) {
	r := noRunReport()

	gotJSON, err := r.JSON()
	if err != nil {
		t.Fatalf("rendering JSON: %v", err)
	}
	gotMD := r.Markdown()

	jsonPath := filepath.Join("report", "no-run.json")
	mdPath := filepath.Join("report", "no-run.md")

	if *update {
		if err := os.MkdirAll("report", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jsonPath, gotJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mdPath, []byte(gotMD), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("committed no-run report rewritten")
		return
	}

	for _, c := range []struct {
		path string
		got  string
	}{{jsonPath, string(gotJSON)}, {mdPath, gotMD}} {
		want, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to create it)", c.path, err)
		}
		if string(want) != c.got {
			t.Errorf("%s is out of date; run: go test ./eval/ -run TestNoRunReport -update", c.path)
		}
	}
}

// TestNoRunReportIsAllZeros guards the property that makes the committed file
// safe to leave in the repository: it contains no number anybody could mistake
// for a result.
func TestNoRunReportIsAllZeros(t *testing.T) {
	r := noRunReport()
	if r.Documents != 0 || r.Overall.Tally != (Tally{}) {
		t.Errorf("a report over no cases is not empty: %+v", r.Overall.Tally)
	}
	if r.Note == "" {
		t.Error("the no-run report carries no note saying it is not a measurement")
	}
	text := r.Text()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "NaN") {
			t.Errorf("the report contains NaN: %q", line)
		}
	}
}

// TestScoreAggregates checks that a report's categories, difficulties and
// per-field rows are derived from the same judgements as its overall figure.
// An overall number that does not equal the sum of its parts is a number
// nobody can act on.
func TestScoreAggregates(t *testing.T) {
	mk := func(name, difficulty string, expected map[string]any, produced map[string]Observed) Case {
		return Case{
			Document: Document{
				Category: "invoices", Name: name,
				Meta:     Meta{Difficulty: difficulty, Pages: 1},
				Expected: expected,
			},
			Observation: Observation{Fields: produced},
		}
	}

	cases := []Case{
		mk("001", "clean-digital",
			map[string]any{"total": num("1000"), "number": "A-1"},
			map[string]Observed{
				"total":  found(1000.0, 0.95),
				"number": found("A-1", 0.95),
				"due":    missing(),
			}),
		mk("002", "poor-scan",
			map[string]any{"total": num("2000"), "number": "A-2"},
			map[string]Observed{
				"total":  found(200.0, 0.5),
				"number": missing(),
				"due":    found("2026-01-01", 0.6),
			}),
	}

	r := Score(cases, Prices{})

	if r.Documents != 2 {
		t.Fatalf("documents = %d, want 2", r.Documents)
	}
	if len(r.Categories) != 1 || r.Categories[0].Name != "invoices" {
		t.Fatalf("categories = %+v", r.Categories)
	}
	cat := r.Categories[0]

	// Four expected values, two produced correctly, one produced wrongly, one
	// missed; two absences, one of them fabricated.
	want := Tally{Expected: 4, Produced: 4, Correct: 2, Wrong: 1, Missed: 1, Absent: 2, Fabricated: 1}
	if cat.Metrics.Tally != want {
		t.Errorf("category tally = %+v\nwant             %+v", cat.Metrics.Tally, want)
	}
	if r.Overall.Tally != want {
		t.Errorf("overall tally does not equal the only category's: %+v", r.Overall.Tally)
	}

	// The difficulty rows must sum to the category.
	var summed Tally
	for _, d := range cat.Difficulties {
		summed.Add(d.Metrics.Tally)
	}
	if summed != cat.Metrics.Tally {
		t.Errorf("difficulty rows sum to %+v, category is %+v", summed, cat.Metrics.Tally)
	}
	if len(cat.Difficulties) != 2 {
		t.Errorf("difficulties = %d, want 2", len(cat.Difficulties))
	}
	// Difficulties report in increasing order of difficulty, not in the order
	// documents happened to be read.
	if cat.Difficulties[0].Difficulty != "clean-digital" || cat.Difficulties[1].Difficulty != "poor-scan" {
		t.Errorf("difficulties out of order: %+v", cat.Difficulties)
	}

	// The field rows must sum to the category too.
	summed = Tally{}
	fields := map[string]bool{}
	for _, f := range cat.Fields {
		summed.Add(f.Metrics.Tally)
		fields[f.Field] = true
	}
	if summed != cat.Metrics.Tally {
		t.Errorf("field rows sum to %+v, category is %+v", summed, cat.Metrics.Tally)
	}
	for _, k := range []string{"total", "number", "due"} {
		if !fields[k] {
			t.Errorf("no row for field %q", k)
		}
	}
}

// TestScoreCountsFailedExtractions checks that a document whose extraction
// errored still counts. Dropping failures from the denominator is how a
// harness reports that everything it managed to read, it read perfectly.
func TestScoreCountsFailedExtractions(t *testing.T) {
	cases := []Case{
		{
			Document: Document{
				Category: "receipts", Name: "001",
				Meta:     Meta{Difficulty: "poor-scan", Pages: 1},
				Expected: map[string]any{"total": num("100")},
			},
			Observation: Observation{Failed: true},
		},
	}
	r := Score(cases, Prices{})
	if r.Failures != 1 {
		t.Errorf("failures = %d, want 1", r.Failures)
	}
	if r.Documents != 1 {
		t.Errorf("documents = %d, want 1", r.Documents)
	}
	if r.Overall.Tally.Missed != 1 || r.Overall.Exact != 0 {
		t.Errorf("a failed extraction did not count as a miss: %+v", r.Overall.Tally)
	}
}

// TestCompareDeltas covers the baseline comparison, including the two metrics
// where a fall is an improvement.
func TestCompareDeltas(t *testing.T) {
	base := &Report{
		Overall:     Metrics{Exact: 0.80, Precision: 0.90, Recall: 0.85, Fabrication: 0.10},
		Calibration: Calibration{ECE: 0.20},
		Categories: []CategoryReport{
			{Name: "invoices", Metrics: Metrics{Exact: 0.70}},
			{Name: "receipts", Metrics: Metrics{Exact: 0.60}},
		},
	}
	cur := &Report{
		Overall:     Metrics{Exact: 0.85, Precision: 0.88, Recall: 0.85, Fabrication: 0.05},
		Calibration: Calibration{ECE: 0.12},
		Categories: []CategoryReport{
			{Name: "invoices", Metrics: Metrics{Exact: 0.75}},
			// receipts is gone and forms is new; neither has a comparison.
			{Name: "forms", Metrics: Metrics{Exact: 0.99}},
		},
	}

	ds := Compare(base, cur)
	by := map[string]Delta{}
	for _, d := range ds {
		by[d.Scope+"/"+d.Metric] = d
	}

	check := func(key string, change float64, better bool) {
		t.Helper()
		d, ok := by[key]
		if !ok {
			t.Fatalf("no delta for %s", key)
		}
		if math.Abs(d.Change-change) > 1e-9 {
			t.Errorf("%s change = %.4f, want %.4f", key, d.Change, change)
		}
		if d.Better() != better {
			t.Errorf("%s Better() = %v, want %v", key, d.Better(), better)
		}
	}
	check("overall/exact", 0.05, true)
	check("overall/precision", -0.02, false)
	check("overall/fabrication", -0.05, true) // less fabrication is better
	check("overall/ece", -0.08, true)         // a smaller calibration gap is better
	check("invoices/exact", 0.05, true)

	if _, ok := by["forms/exact"]; ok {
		t.Error("a category with no baseline was compared against an invented zero")
	}
	if _, ok := by["receipts/exact"]; ok {
		t.Error("a category absent from the current run was reported")
	}
}

// TestRenderDeltasHidesNoise checks that movements below the epsilon are not
// printed. On a corpus of twenty-five documents one field changing its mind
// moves a ratio by thousandths, and printing that invites somebody to chase it.
func TestRenderDeltasHidesNoise(t *testing.T) {
	ds := []Delta{
		{Scope: "overall", Metric: "exact", Baseline: 0.800, Current: 0.8005, Change: 0.0005},
		{Scope: "overall", Metric: "recall", Baseline: 0.800, Current: 0.900, Change: 0.100},
	}
	out := RenderDeltas(ds, "report/baseline.json", 0.01)
	if strings.Contains(out, "exact") {
		t.Errorf("a movement of 0.0005 was printed:\n%s", out)
	}
	if !strings.Contains(out, "recall") {
		t.Errorf("a movement of 0.100 was hidden:\n%s", out)
	}

	quiet := RenderDeltas(ds[:1], "report/baseline.json", 0.01)
	if !strings.Contains(quiet, "no movement above") {
		t.Errorf("a comparison with nothing to say did not say so:\n%s", quiet)
	}
}

// TestReportJSONRoundTrips checks that a committed report can be read back as
// a baseline. A report that cannot be reloaded is a report that cannot be
// compared against, which is most of the reason for committing it.
func TestReportJSONRoundTrips(t *testing.T) {
	r := noRunReport()
	r.Overall = Metrics{Exact: 0.5, Tally: Tally{Expected: 2, Correct: 1}}
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("a report does not round-trip: %v", err)
	}
	if back.Overall.Exact != 0.5 || back.Overall.Tally.Expected != 2 {
		t.Errorf("round-tripped report lost figures: %+v", back.Overall)
	}
	if !back.Generated.Equal(r.Generated) {
		t.Errorf("round-tripped report lost its timestamp")
	}
}
