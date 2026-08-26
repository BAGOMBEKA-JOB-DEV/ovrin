package eval

import (
	"math"
	"sort"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// Observed is one field as an extraction reported it.
//
// A reduction of [ovrin.FieldResult] to the three things scoring needs, so
// that the scorer can be exercised without a provider: every metric in this
// package is a pure function of a slice of these and a ground-truth map, and
// that is what makes the arithmetic testable offline.
type Observed struct {
	// Value is what was extracted.
	Value any

	// Found reports presence. It is not Value != zero, and the distinction is
	// the whole point: a total of zero and an unreadable total are different
	// results and only one of them is a miss.
	Found bool

	// Confidence is the field's score, on 0..1.
	Confidence float64

	// Flagged reports whether the field was named in a review reason.
	Flagged bool
}

// Observation is one document's extraction, reduced to what scoring needs.
type Observation struct {
	// Fields is keyed as [ovrin.Result].Fields is.
	Fields map[string]Observed

	// Confidence is the document aggregate.
	Confidence float64

	// Valid reports whether every validation rule passed.
	Valid bool

	// Usage is what the extraction consumed, for costing.
	Usage ovrin.Usage

	// Duration is the wall time of the extraction.
	Duration time.Duration

	// Failed reports that the extraction returned an error and produced
	// nothing. Such a document still counts: an extractor that fails on the
	// poor scans and succeeds on the clean ones has not scored 100%, and
	// dropping the failures from the denominator is how that gets reported.
	Failed bool
}

// Case pairs one corpus document with what an extraction made of it.
type Case struct {
	Document    Document
	Observation Observation
}

// Tally is the confusion matrix for a set of field instances.
//
// Counts rather than ratios, because ratios do not add up. Aggregating a
// category from its documents means summing tallies and dividing once at the
// end; averaging per-document percentages would weight a two-field document
// the same as a twenty-field one.
type Tally struct {
	// Expected is the number of field instances ground truth gives a value
	// for. The denominator of Exact and Recall.
	Expected int

	// Absent is the number of field instances ground truth deliberately has
	// no value for. The denominator of Fabrication, and the reason a corpus
	// needs documents with missing fields: with no absences there is no
	// opportunity to fabricate and the rate is 0.00 for the wrong reason.
	Absent int

	// Produced is the number of field instances a value came back for. The
	// denominator of Precision.
	Produced int

	// Correct is produced, expected, and equal.
	Correct int

	// Wrong is produced and expected, but not equal.
	Wrong int

	// Missed is expected and not produced.
	Missed int

	// Fabricated is produced where ground truth has no value.
	Fabricated int

	// Excluded is the number of field instances dropped from scoring because
	// the document's metadata records them as ambiguous. Reported so that a
	// corpus quietly excluding half its fields is visible.
	Excluded int
}

// Add sums another tally into this one.
func (t *Tally) Add(o Tally) {
	t.Expected += o.Expected
	t.Absent += o.Absent
	t.Produced += o.Produced
	t.Correct += o.Correct
	t.Wrong += o.Wrong
	t.Missed += o.Missed
	t.Fabricated += o.Fabricated
	t.Excluded += o.Excluded
}

// Metrics are the four field-accuracy ratios and the counts they came from.
//
// The counts travel with the ratios because a ratio without its denominator is
// not a measurement. "Fabrication 0.00" over four absences and "fabrication
// 0.00" over four hundred are different claims, and only the second is worth
// making.
type Metrics struct {
	// Exact is Correct/Expected: of the values the document contains, the
	// fraction recovered exactly under type-aware comparison.
	Exact float64

	// Precision is Correct/Produced: of the values produced, the fraction
	// that were right. Fabrications are in the denominator, so inventing
	// values lowers it.
	Precision float64

	// Recall is (Correct+Wrong)/Expected: of the values present, the fraction
	// something was produced for. It measures coverage, not correctness —
	// Exact measures correctness — and the two differ exactly by the values
	// that were found and misread.
	Recall float64

	// Fabrication is Fabricated/Absent: of the fields the document does not
	// contain, the fraction a value was invented for.
	//
	// This is the one to watch. A missing field is visible and gets handled;
	// an invented one is well-formed, passes validation and reaches
	// production.
	Fabrication float64

	// Tally is the counts every ratio above was computed from.
	Tally Tally
}

// ratio divides, returning 0 for an empty denominator. A metric over nothing
// is reported as zero next to its n=0, never as NaN: NaN propagates through
// every aggregate that touches it and turns one empty bucket into a report of
// nothing.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// metricsOf derives the ratios from a tally.
func metricsOf(t Tally) Metrics {
	return Metrics{
		Exact:       ratio(t.Correct, t.Expected),
		Precision:   ratio(t.Correct, t.Produced),
		Recall:      ratio(t.Correct+t.Wrong, t.Expected),
		Fabrication: ratio(t.Fabricated, t.Absent),
		Tally:       t,
	}
}

// judgement is the verdict on one field instance: what ground truth said,
// what the extraction said, and whether they agree.
//
// Every figure in this package is derived from a slice of these — the tallies,
// the per-field breakdown and the calibration alike — so that the accuracy
// numbers and the calibration numbers can never be computed from two different
// ideas of what "correct" means.
type judgement struct {
	key      string
	group    string
	excluded bool

	wanted   bool
	produced bool
	correct  bool

	confidence float64
	flagged    bool
}

// judge compares one document's ground truth against one extraction, field
// instance by field instance.
//
// The universe is the union of the keys ground truth gives a value for and the
// keys the extraction produced one for, restricted to leaves. Both sides
// matter: ground truth alone would make fabrication unmeasurable, and the
// extraction alone would make omission unmeasurable.
func judge(c Case) []judgement {
	excluded := map[string]bool{}
	for _, k := range c.Document.Meta.Exclude {
		excluded[k] = true
	}

	seen := map[string]bool{}
	all := make([]string, 0, len(c.Document.Expected)+len(c.Observation.Fields))
	for k := range c.Document.Expected {
		if !seen[k] {
			seen[k] = true
			all = append(all, k)
		}
	}
	for k, f := range c.Observation.Fields {
		// A field the extraction reports as not found contributes nothing on
		// its own. It is a miss only if ground truth expected it, and the loop
		// above already has that key.
		if !f.Found {
			continue
		}
		if !seen[k] {
			seen[k] = true
			all = append(all, k)
		}
	}

	keys := leafKeys(all)
	out := make([]judgement, 0, len(keys))
	for _, k := range keys {
		j := judgement{key: k, group: CollapseIndices(k), excluded: excluded[k]}
		got, produced := c.Observation.Fields[k]
		j.flagged = produced && got.Flagged
		if j.excluded {
			out = append(out, j)
			continue
		}
		want, wanted := c.Document.Expected[k]
		j.wanted = wanted
		j.produced = produced && got.Found
		if j.produced {
			j.confidence = got.Confidence
			j.correct = wanted && Equal(want, got.Value)
		}
		out = append(out, j)
	}
	return out
}

// tallyOf counts a set of judgements.
func tallyOf(js []judgement) Tally {
	var t Tally
	for _, j := range js {
		switch {
		case j.excluded:
			t.Excluded++
		case j.wanted && j.produced:
			t.Expected++
			t.Produced++
			if j.correct {
				t.Correct++
			} else {
				t.Wrong++
			}
		case j.wanted:
			t.Expected++
			t.Missed++
		case j.produced:
			t.Absent++
			t.Produced++
			t.Fabricated++
		default:
			t.Absent++
		}
	}
	return t
}

// Bands are the confidence ranges the human-readable report breaks accuracy
// down by. Half-open below, closed at the top, so every score falls in exactly
// one.
var Bands = []Band{
	{Low: 0.9, High: 1.0, Label: "0.9–1.0"},
	{Low: 0.7, High: 0.9, Label: "0.7–0.9"},
	{Low: 0.0, High: 0.7, Label: "below 0.7"},
}

// Band is one confidence range and the accuracy observed inside it.
//
// This is the table an operator reads to find out whether a stated confidence
// means anything. A band whose accuracy is far from its range is a calibration
// failure and is meant to be visible without arithmetic.
type Band struct {
	// Low and High bound the range. A score is in the band when
	// Low <= c <= High for the top band and Low <= c < High otherwise.
	Low  float64
	High float64

	// Label is how the band prints.
	Label string

	// N is the number of produced values whose confidence fell here.
	N int

	// Accuracy is the fraction of those that were right.
	Accuracy float64

	// MeanConfidence is what the scorer claimed across them, so the gap
	// between claim and outcome is readable directly.
	MeanConfidence float64
}

// RiskPoint is one threshold on the risk-coverage curve.
//
// The curve is what an operator actually needs. "Where do I set the
// threshold" is answered by a coverage and an error rate at each candidate,
// not by an opinion about what number sounds high.
type RiskPoint struct {
	// Threshold is the auto-accept cutoff: values scoring at or above it are
	// used without review.
	Threshold float64

	// Coverage is the fraction of produced values that clears it.
	Coverage float64

	// Error is the fraction of the cleared values that are wrong — the error
	// rate the operator carries by not reviewing them.
	Error float64

	// N is how many values cleared it, because an error rate over three
	// values is not an error rate.
	N int
}

// Calibration is everything the report says about whether confidence means
// anything.
type Calibration struct {
	// ECE is the expected calibration error: the mean gap between stated
	// confidence and observed accuracy, weighted by how many values fall in
	// each bin. Zero is perfect; it is not an accuracy figure and must not be
	// quoted as one.
	ECE float64

	// Bins are the ten equal-width bins ECE was computed over, kept so the
	// number can be checked by hand.
	Bins []Band

	// Bands are the three ranges the human-readable report uses.
	Bands []Band

	// Risk is the risk-coverage curve.
	Risk []RiskPoint

	// ReviewPrecision is, of the fields flagged for review, the fraction that
	// were actually wrong. Low review precision means people are being asked
	// to check values that were fine, which is how a review queue stops being
	// read.
	ReviewPrecision float64

	// Flagged is how many fields were flagged.
	Flagged int

	// Scored is how many produced values the calibration figures cover.
	// Absent fields are excluded: a confidence attached to no value is not a
	// claim about a value.
	Scored int
}

// calibrationOf computes ECE, bands and the risk-coverage curve over produced
// values.
func calibrationOf(js []judgement) Calibration {
	cal := Calibration{Bands: append([]Band(nil), Bands...)}

	produced := make([]judgement, 0, len(js))
	for _, o := range js {
		if o.excluded {
			continue
		}
		if o.flagged {
			cal.Flagged++
		}
		if o.produced {
			produced = append(produced, o)
		}
	}
	cal.Scored = len(produced)

	// Review precision counts every kind of wrongness, including a required
	// field that came back absent: an absence correctly flagged is a review
	// that saved somebody.
	wrongFlagged := 0
	for _, o := range js {
		if o.excluded {
			continue
		}
		if o.flagged && !(o.produced && o.correct) {
			wrongFlagged++
		}
	}
	cal.ReviewPrecision = ratio(wrongFlagged, cal.Flagged)

	// Ten equal-width bins, the conventional choice. Fewer hides
	// miscalibration inside a wide bin; more leaves bins too sparse to mean
	// anything on a corpus this size.
	const nbins = 10
	bins := make([]Band, nbins)
	for i := range bins {
		bins[i].Low = float64(i) / nbins
		bins[i].High = float64(i+1) / nbins
		bins[i].Label = label2(bins[i].Low) + "–" + label2(bins[i].High)
	}
	sums := make([]float64, nbins)
	for _, o := range produced {
		i := int(o.confidence * nbins)
		if i >= nbins {
			i = nbins - 1
		}
		if i < 0 {
			i = 0
		}
		bins[i].N++
		sums[i] += o.confidence
		if o.correct {
			bins[i].Accuracy++
		}
	}
	for i := range bins {
		if bins[i].N == 0 {
			continue
		}
		correct := bins[i].Accuracy
		bins[i].Accuracy = correct / float64(bins[i].N)
		bins[i].MeanConfidence = sums[i] / float64(bins[i].N)
		cal.ECE += float64(bins[i].N) / float64(len(produced)) *
			math.Abs(bins[i].Accuracy-bins[i].MeanConfidence)
	}
	cal.Bins = bins

	for i := range cal.Bands {
		b := &cal.Bands[i]
		correct, sum := 0, 0.0
		for _, o := range produced {
			if !inBand(*b, o.confidence) {
				continue
			}
			b.N++
			sum += o.confidence
			if o.correct {
				correct++
			}
		}
		b.Accuracy = ratio(correct, b.N)
		if b.N > 0 {
			b.MeanConfidence = sum / float64(b.N)
		}
	}

	// The curve is sampled at 0.05, which is fine enough to see the knee and
	// coarse enough to read.
	for t := 0.0; t <= 1.0001; t += 0.05 {
		th := math.Round(t*100) / 100
		n, wrong := 0, 0
		for _, o := range produced {
			if o.confidence < th {
				continue
			}
			n++
			if !o.correct {
				wrong++
			}
		}
		cal.Risk = append(cal.Risk, RiskPoint{
			Threshold: th,
			Coverage:  ratio(n, len(produced)),
			Error:     ratio(wrong, n),
			N:         n,
		})
	}
	return cal
}

// inBand reports whether c falls in b. The top band is closed at 1.0 so that a
// perfect score is not silently dropped.
func inBand(b Band, c float64) bool {
	if b.High >= 1.0 {
		return c >= b.Low && c <= b.High
	}
	return c >= b.Low && c < b.High
}

// label2 formats a band bound to two decimals without importing fmt for one
// call site's worth of formatting.
func label2(f float64) string {
	switch {
	case f <= 0:
		return "0.0"
	case f >= 1:
		return "1.0"
	}
	d := int(math.Round(f * 10))
	return "0." + string(rune('0'+d))
}

// Cost is what a run consumed, per document.
//
// Prices are an input, never a constant: they change, they differ by account,
// and a figure computed from a price this repository guessed would be a number
// nobody can reproduce. When no prices are given the money figures are zero
// and Priced says so, which is the honest report.
type Cost struct {
	// Documents is how many documents the figures cover.
	Documents int

	// Usage is the total across every provider call.
	Usage ovrin.Usage

	// Priced reports whether a price table was supplied.
	Priced bool

	// USDPerDocument is the mean cost of one document, zero when not Priced.
	USDPerDocument float64

	// USDPerPage is the mean cost of one page, zero when not Priced.
	USDPerPage float64

	// MedianLatency and P95Latency are wall time per document.
	MedianLatency time.Duration
	P95Latency    time.Duration
}

// Prices converts usage into money. Zero values mean "not known", and a Prices
// with every field zero produces an unpriced [Cost] rather than a cost of
// nothing.
type Prices struct {
	// USDPerInputToken and USDPerOutputToken are model prices.
	USDPerInputToken  float64
	USDPerOutputToken float64

	// USDPerPageUnit is the OCR provider's price per page.
	USDPerPageUnit float64
}

// Zero reports whether no price at all was supplied.
func (p Prices) Zero() bool {
	return p.USDPerInputToken == 0 && p.USDPerOutputToken == 0 && p.USDPerPageUnit == 0
}

// costOf totals usage and latency across a run.
func costOf(cases []Case, prices Prices) Cost {
	c := Cost{Documents: len(cases), Priced: !prices.Zero()}
	pages := 0
	lat := make([]time.Duration, 0, len(cases))
	for _, cs := range cases {
		c.Usage.InputTokens += cs.Observation.Usage.InputTokens
		c.Usage.OutputTokens += cs.Observation.Usage.OutputTokens
		c.Usage.PageUnits += cs.Observation.Usage.PageUnits
		pages += cs.Document.Meta.Pages
		lat = append(lat, cs.Observation.Duration)
	}
	if c.Priced {
		total := float64(c.Usage.InputTokens)*prices.USDPerInputToken +
			float64(c.Usage.OutputTokens)*prices.USDPerOutputToken +
			float64(c.Usage.PageUnits)*prices.USDPerPageUnit
		if len(cases) > 0 {
			c.USDPerDocument = total / float64(len(cases))
		}
		if pages > 0 {
			c.USDPerPage = total / float64(pages)
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	c.MedianLatency = quantile(lat, 0.5)
	c.P95Latency = quantile(lat, 0.95)
	return c
}

// quantile returns the q-th quantile of a sorted slice by nearest rank, which
// is the definition that never invents a duration between two observations.
func quantile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
