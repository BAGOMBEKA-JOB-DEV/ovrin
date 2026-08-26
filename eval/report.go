package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Report is one evaluation run.
//
// It is committed to eval/report/ as JSON and as Markdown, so that a quality
// regression arrives as a reviewable diff rather than as a customer's
// complaint (ADR-0023). The JSON is what [Compare] reads to produce a delta;
// the Markdown is what a person reads.
type Report struct {
	// Generated is when the run finished, in UTC.
	Generated time.Time `json:"generated"`

	// Commit is the ovrin commit the run was made from. Without it a figure
	// cannot be reproduced, which makes it a figure rule §3.8 forbids
	// quoting.
	Commit string `json:"commit"`

	// Model, OCR and Reading identify the configuration. Different
	// configurations produce different numbers and comparing across them is
	// the most common way to draw a wrong conclusion from this file.
	Model   string `json:"model"`
	OCR     string `json:"ocr"`
	Reading string `json:"reading"`

	// Corpus is the corpus directory the run read, and Filter records any
	// -category or -difficulty restriction, because a filtered run is not
	// comparable with an unfiltered one.
	Corpus string `json:"corpus"`
	Filter string `json:"filter,omitempty"`

	// Documents is how many documents were scored.
	Documents int `json:"documents"`

	// Failures is how many extractions returned an error. Those documents are
	// still in every denominator.
	Failures int `json:"failures"`

	// Overall is the whole run, and is the figure most likely to be misquoted:
	// it is an average over whatever mix of difficulties the corpus happens to
	// hold. Read Categories and their difficulty rows instead.
	Overall Metrics `json:"overall"`

	// Categories is one entry per corpus category, in [Categories] order.
	Categories []CategoryReport `json:"categories"`

	// Calibration is the whole run's calibration.
	Calibration Calibration `json:"calibration"`

	// Cost is what the run consumed.
	Cost Cost `json:"cost"`

	// Note carries any caveat that must travel with the numbers — most
	// importantly, that no run has happened and every figure is a zero.
	Note string `json:"note,omitempty"`
}

// CategoryReport is one corpus category's results.
type CategoryReport struct {
	// Name is the category directory.
	Name string `json:"name"`

	// N is the number of documents scored in it.
	N int `json:"n"`

	// Metrics is the category aggregate.
	Metrics Metrics `json:"metrics"`

	// Difficulties breaks the category down by difficulty label. An aggregate
	// over an unbalanced corpus is meaningless, so this is the row that
	// actually says something.
	Difficulties []DifficultyReport `json:"difficulties"`

	// Fields breaks the category down by field key, sorted. This is where a
	// regression localises: an overall figure moving by a point says
	// something changed, and this says which field.
	Fields []FieldReport `json:"fields"`
}

// DifficultyReport is one difficulty label within a category.
type DifficultyReport struct {
	Difficulty string  `json:"difficulty"`
	N          int     `json:"n"`
	Metrics    Metrics `json:"metrics"`
}

// FieldReport is one field key within a category.
type FieldReport struct {
	// Field is the key as [ovrin.Result].Fields uses it, with slice indices
	// collapsed: "items[0].total" and "items[3].total" are both reported as
	// "items[].total", because per-index accuracy is noise.
	Field   string  `json:"field"`
	Metrics Metrics `json:"metrics"`
}

// Score turns a run's cases into a report.
//
// Pure: no clock, no filesystem, no provider. Everything variable — the time,
// the commit, the model names — is supplied by the caller, which is what lets
// the report renderer be golden-tested and the metric arithmetic be table
// tested.
func Score(cases []Case, prices Prices) *Report {
	r := &Report{Documents: len(cases)}

	var overall Tally
	var allJudgements []judgement

	byCat := map[string][]Case{}
	for _, c := range cases {
		if c.Observation.Failed {
			r.Failures++
		}
		byCat[c.Document.Category] = append(byCat[c.Document.Category], c)
	}

	for _, cat := range Categories {
		cs, ok := byCat[cat]
		if !ok {
			continue
		}
		cr := CategoryReport{Name: cat, N: len(cs)}

		var catTally Tally
		byDiff := map[string]*Tally{}
		byField := map[string]*Tally{}
		diffN := map[string]int{}

		for _, c := range cs {
			js := judge(c)
			t := tallyOf(js)
			catTally.Add(t)
			overall.Add(t)
			allJudgements = append(allJudgements, js...)

			d := c.Document.Meta.Difficulty
			if byDiff[d] == nil {
				byDiff[d] = &Tally{}
			}
			byDiff[d].Add(t)
			diffN[d]++

			for _, j := range js {
				if byField[j.group] == nil {
					byField[j.group] = &Tally{}
				}
				byField[j.group].Add(tallyOf([]judgement{j}))
			}
		}

		cr.Metrics = metricsOf(catTally)
		for _, d := range Difficulties {
			t, ok := byDiff[d]
			if !ok {
				continue
			}
			cr.Difficulties = append(cr.Difficulties, DifficultyReport{
				Difficulty: d, N: diffN[d], Metrics: metricsOf(*t),
			})
		}
		keys := make([]string, 0, len(byField))
		for k := range byField {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cr.Fields = append(cr.Fields, FieldReport{Field: k, Metrics: metricsOf(*byField[k])})
		}
		r.Categories = append(r.Categories, cr)
	}

	r.Overall = metricsOf(overall)
	r.Calibration = calibrationOf(allJudgements)
	r.Cost = costOf(cases, prices)
	return r
}

// CollapseIndices rewrites "items[3].unit_price" as "items[].unit_price".
//
// Per-index accuracy is noise: nothing is learned from the third line item
// being harder than the second, and reporting them separately turns one field
// into as many rows as the longest document has rows.
func CollapseIndices(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		if key[i] != '[' {
			b.WriteByte(key[i])
			continue
		}
		j := strings.IndexByte(key[i:], ']')
		if j < 0 {
			b.WriteString(key[i:])
			break
		}
		b.WriteString("[]")
		i += j
	}
	return b.String()
}

// JSON renders the report as the committed machine-readable file.
func (r *Report) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Markdown renders the report as the committed human-readable file, in the
// layout docs/evaluation.md shows.
//
// The figures sit inside a fenced block on purpose: fixed columns diff
// cleanly, and a regression should be legible as a changed column rather than
// as a reflowed table.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ovrin eval · %s\n\n", r.Generated.UTC().Format("2006-01-02"))
	if r.Note != "" {
		fmt.Fprintf(&b, "> %s\n\n", r.Note)
	}
	b.WriteString("```text\n")
	b.WriteString(r.Text())
	b.WriteString("```\n")
	return b.String()
}

// Text renders the fixed-column body shared by the Markdown file and the test
// log, so that what a developer sees while iterating is the same thing that
// gets committed.
func (r *Report) Text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "ovrin eval · %s · commit %s\n",
		r.Generated.UTC().Format("2006-01-02"), or(r.Commit, "unknown"))
	fmt.Fprintf(&b, "model %s · ocr %s · reading %s\n",
		or(r.Model, "none"), or(r.OCR, "none"), or(r.Reading, "auto"))
	if r.Filter != "" {
		fmt.Fprintf(&b, "filter %s\n", r.Filter)
	}
	fmt.Fprintf(&b, "\noverall           n=%d\n", r.Documents)
	writeMetrics(&b, "  ", r.Overall)
	if r.Failures > 0 {
		fmt.Fprintf(&b, "  extraction errors         %d\n", r.Failures)
	}

	for _, c := range r.Categories {
		fmt.Fprintf(&b, "\n%-17s n=%d\n", c.Name, c.N)
		writeMetrics(&b, "  ", c.Metrics)

		if len(c.Difficulties) > 0 {
			b.WriteString("\n  by difficulty\n")
			for _, d := range c.Difficulties {
				fmt.Fprintf(&b, "    %-14s n=%-4d %.2f\n", d.Difficulty, d.N, d.Metrics.Exact)
			}
		}
		if len(c.Fields) > 0 {
			b.WriteString("\n  by field                exact  prec  recall  fabr\n")
			for _, f := range c.Fields {
				fmt.Fprintf(&b, "    %-20s  %.2f  %.2f    %.2f  %.2f\n",
					f.Field, f.Metrics.Exact, f.Metrics.Precision,
					f.Metrics.Recall, f.Metrics.Fabrication)
			}
		}
	}

	b.WriteString("\nconfidence calibration\n")
	fmt.Fprintf(&b, "  ECE                       %.2f\n", r.Calibration.ECE)
	for _, band := range r.Calibration.Bands {
		fmt.Fprintf(&b, "  %-14s n=%-8d accuracy %.2f\n", band.Label, band.N, band.Accuracy)
	}
	b.WriteString("\n")
	for _, t := range []float64{0.90, 0.70} {
		p := riskAt(r.Calibration.Risk, t)
		fmt.Fprintf(&b, "  auto-accept at %.2f       coverage %.2f  error %.2f\n",
			t, p.Coverage, p.Error)
	}
	if len(r.Calibration.Risk) > 0 {
		b.WriteString("\n  risk-coverage\n")
		b.WriteString("    threshold  coverage  error  n\n")
		for _, p := range r.Calibration.Risk {
			fmt.Fprintf(&b, "    %-9.2f  %-8.2f  %-5.2f  %d\n", p.Threshold, p.Coverage, p.Error, p.N)
		}
	}
	if r.Calibration.Flagged > 0 || r.Calibration.Scored > 0 {
		fmt.Fprintf(&b, "\n  review precision          %.2f  (%d flagged, %d values scored)\n",
			r.Calibration.ReviewPrecision, r.Calibration.Flagged, r.Calibration.Scored)
	}

	b.WriteString("\n")
	if r.Cost.Priced {
		fmt.Fprintf(&b, "cost      $%.4f per document · $%.4f per page\n",
			r.Cost.USDPerDocument, r.Cost.USDPerPage)
	} else {
		fmt.Fprintf(&b, "cost      no price table supplied; tokens in=%d out=%d pages=%d\n",
			r.Cost.Usage.InputTokens, r.Cost.Usage.OutputTokens, r.Cost.Usage.PageUnits)
	}
	fmt.Fprintf(&b, "latency   %s median · %s p95\n",
		secs(r.Cost.MedianLatency), secs(r.Cost.P95Latency))
	return b.String()
}

// writeMetrics prints the four field-accuracy ratios with their denominators.
func writeMetrics(b *strings.Builder, indent string, m Metrics) {
	fmt.Fprintf(b, "%sexact                     %.2f  (%d/%d)\n", indent, m.Exact, m.Tally.Correct, m.Tally.Expected)
	fmt.Fprintf(b, "%sprecision                 %.2f  (%d/%d)\n", indent, m.Precision, m.Tally.Correct, m.Tally.Produced)
	fmt.Fprintf(b, "%srecall                    %.2f  (%d/%d)\n", indent, m.Recall, m.Tally.Correct+m.Tally.Wrong, m.Tally.Expected)
	fmt.Fprintf(b, "%sfabrication               %.2f  (%d/%d)\n", indent, m.Fabrication, m.Tally.Fabricated, m.Tally.Absent)
	if m.Tally.Excluded > 0 {
		fmt.Fprintf(b, "%sexcluded as ambiguous     %d\n", indent, m.Tally.Excluded)
	}
}

// riskAt returns the curve point at or just below t, so that the two named
// thresholds print even when the curve was sampled coarsely.
func riskAt(curve []RiskPoint, t float64) RiskPoint {
	best := RiskPoint{Threshold: t}
	for _, p := range curve {
		if p.Threshold <= t+1e-9 {
			best = p
		}
	}
	return best
}

// or returns s, or alt when s is empty.
func or(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// secs formats a duration the way the report shows latency.
func secs(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

// Delta is one metric's movement between a baseline report and a new one.
type Delta struct {
	// Scope is "overall" or a category name.
	Scope string `json:"scope"`

	// Metric names the figure: "exact", "precision", "recall",
	// "fabrication", "ece".
	Metric string `json:"metric"`

	// Baseline and Current are the two values.
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`

	// Change is Current-Baseline. For fabrication and ECE a negative change
	// is an improvement, which is why [Delta.Better] exists rather than a
	// bare sign test.
	Change float64 `json:"change"`
}

// Better reports whether the movement is an improvement, accounting for the
// two metrics where lower is better.
func (d Delta) Better() bool {
	if d.Metric == "fabrication" || d.Metric == "ece" {
		return d.Change < 0
	}
	return d.Change > 0
}

// Compare reports how a run moved against a committed baseline.
//
// This is the form worth running during development: an absolute number says
// little without a corpus you know by heart, and a delta says whether the
// change you just made helped.
//
// Only scopes present in both reports are compared. A category added since the
// baseline has nothing to move against, and inventing a zero baseline for it
// would report a spectacular improvement that did not happen.
func Compare(baseline, current *Report) []Delta {
	var out []Delta
	add := func(scope, metric string, b, c float64) {
		out = append(out, Delta{Scope: scope, Metric: metric, Baseline: b, Current: c, Change: c - b})
	}
	addMetrics := func(scope string, b, c Metrics) {
		add(scope, "exact", b.Exact, c.Exact)
		add(scope, "precision", b.Precision, c.Precision)
		add(scope, "recall", b.Recall, c.Recall)
		add(scope, "fabrication", b.Fabrication, c.Fabrication)
	}
	addMetrics("overall", baseline.Overall, current.Overall)
	add("overall", "ece", baseline.Calibration.ECE, current.Calibration.ECE)

	base := map[string]Metrics{}
	for _, c := range baseline.Categories {
		base[c.Name] = c.Metrics
	}
	for _, c := range current.Categories {
		b, ok := base[c.Name]
		if !ok {
			continue
		}
		addMetrics(c.Name, b, c.Metrics)
	}
	return out
}

// RenderDeltas formats a comparison for a terminal, hiding movements too small
// to mean anything on a corpus of this size.
//
// The threshold is not cosmetic. A corpus of twenty-five documents has a few
// hundred field instances, so one field changing its mind moves a ratio by
// several thousandths; printing that as a result invites somebody to chase it.
func RenderDeltas(ds []Delta, baselinePath string, epsilon float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "delta against %s\n", baselinePath)
	shown := 0
	for _, d := range ds {
		if math.Abs(d.Change) < epsilon {
			continue
		}
		mark := "worse"
		if d.Better() {
			mark = "better"
		}
		fmt.Fprintf(&b, "  %-14s %-12s %.3f → %.3f  %+.3f  %s\n",
			d.Scope, d.Metric, d.Baseline, d.Current, d.Change, mark)
		shown++
	}
	if shown == 0 {
		fmt.Fprintf(&b, "  no movement above %.3f\n", epsilon)
	}
	return b.String()
}
