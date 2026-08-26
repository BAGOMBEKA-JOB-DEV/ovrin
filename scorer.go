package ovrin

import (
	"fmt"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/ground"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// The weights the default scorer gives each signal.
//
// They are **provisional**. Until they are calibrated against a corpus,
// confidence is a ranking signal — good for ordering a review queue — and not
// a probability. Nothing here means "correct this often", and
// docs/confidence.md says so at greater length.
//
// defaults_doc_test.go asserts these equal the table in docs/confidence.md, so
// changing one here without changing the other turns the build red.
const (
	WeightGrounding  = 0.30
	WeightAgreement  = 0.25
	WeightOCR        = 0.20
	WeightSchema     = 0.15
	WeightFormat     = 0.05
	WeightCrossField = 0.05
)

// The ceilings applied after the weighted mean.
//
// A floor exists where averaging would be wrong: a value that satisfies four
// signals and fails its declared rule is not four-fifths correct, it is
// unusable. Averaging alone would let strong signals hide the one that matters.
const (
	// CapRuleFailed applies when a declared rule other than required failed.
	CapRuleFailed = 0.40

	// CapUngrounded applies when the value appears nowhere in the source. It
	// is lower than CapRuleFailed because a well-formed value that is not in
	// the document is the more dangerous of the two: it looks correct.
	CapUngrounded = 0.35

	// CapDisagreement applies when two readings produced different values.
	CapDisagreement = 0.50

	// CapSuspicious applies when the source page carried content that looked
	// like an injection attempt.
	CapSuspicious = 0.60
)

// defaultScorer is the scorer a Client uses when the caller supplies none.
//
// It is a weighted mean over the signals that applied, with the weight of any
// absent signal redistributed across the rest rather than counted as zero. That
// distinction is the whole design: a text-layer PDF has no OCR signal, and
// scoring it zero would penalise the most accurate reading ovrin has.
type defaultScorer struct{}

// Score implements Scorer.
func (defaultScorer) Score(f FieldEvidence) (float64, []Signal) {
	if !f.Found {
		// Nothing was extracted, so there is nothing to be confident about.
		// This is not low confidence in a value; it is the absence of one.
		return 0, nil
	}

	var signals []Signal
	add := func(name string, value, weight float64, note string) {
		signals = append(signals, Signal{Name: name, Value: value, Weight: weight, Note: note})
	}

	if f.Grounding > 0 || len(f.Provenance) > 0 {
		add(SignalGrounding, f.Grounding, WeightGrounding, groundingNote(f.Grounding))
	}
	if f.OCRConfidence != nil {
		add(SignalOCR, *f.OCRConfidence, WeightOCR, "mean confidence of the backing words")
	}

	schemaValue, ruleFailed := schemaSignal(f.Validation)
	add(SignalSchema, schemaValue, WeightSchema, schemaNote(f.Validation))

	if v, ok := formatSignal(f.Validation); ok {
		add(SignalFormat, v, WeightFormat, "the declared format parsed")
	}
	if len(f.Candidates) > 1 {
		add(SignalAgreement, 0, WeightAgreement, "readings disagree")
	}

	mean := weightedMean(signals)
	conf := mean

	// The ceilings. A value that satisfies four signals and fails its declared
	// rule is not four-fifths correct, it is unusable, and averaging alone
	// would let the strong signals hide the one that matters.
	//
	// A ceiling that binds is recorded as a zero-weight signal. docs/confidence.md
	// promises every score decomposes into its signals, and a confidence that
	// came out below the mean with nothing to explain the gap would make that
	// claim false — a reader could do the arithmetic and get a different
	// number. Zero weight keeps the mean itself untouched.
	cap := func(limit float64, name, why string) {
		if limit >= conf {
			return
		}
		conf = limit
		signals = append(signals, Signal{Name: name, Note: why})
	}
	if ruleFailed {
		cap(CapRuleFailed, "capped:rule", "a declared rule failed")
	}
	if f.Grounding == ground.NotFound && len(f.Provenance) == 0 && hasSignal(signals, SignalGrounding) {
		cap(CapUngrounded, "capped:grounding", "the value is not in the source")
	}
	if len(f.Candidates) > 1 {
		cap(CapDisagreement, "capped:disagreement", "the readings disagree")
	}
	if f.Suspicious {
		cap(CapSuspicious, "capped:suspicious", "the source page carried suspicious content")
	}
	return round2(conf), signals
}

// weightedMean divides by the weight that was actually present, so an absent
// signal is excluded from the denominator rather than scored zero.
func weightedMean(signals []Signal) float64 {
	var sum, weight float64
	for _, s := range signals {
		sum += s.Value * s.Weight
		weight += s.Weight
	}
	if weight == 0 {
		return 0
	}
	return sum / weight
}

func hasSignal(signals []Signal, name string) bool {
	for _, s := range signals {
		if s.Name == name {
			return true
		}
	}
	return false
}

func schemaSignal(rules []RuleResult) (value float64, failed bool) {
	if len(rules) == 0 {
		return 1, false
	}
	passed := 0
	for _, r := range rules {
		if r.Passed {
			passed++
		} else {
			failed = true
		}
	}
	return float64(passed) / float64(len(rules)), failed
}

func schemaNote(rules []RuleResult) string {
	if len(rules) == 0 {
		return "no rules declared"
	}
	passed := 0
	for _, r := range rules {
		if r.Passed {
			passed++
		}
	}
	return fmt.Sprintf("%d of %d rules passed", passed, len(rules))
}

func formatSignal(rules []RuleResult) (float64, bool) {
	for _, r := range rules {
		if len(r.Rule) >= len(schema.RuleFormat) && r.Rule[:len(schema.RuleFormat)] == schema.RuleFormat {
			if r.Passed {
				return 1, true
			}
			return 0, true
		}
	}
	return 0, false
}

func groundingNote(v float64) string {
	switch v {
	case ground.Verbatim:
		return "found verbatim in the source"
	case ground.Normalised:
		return "found in the source, formatted differently"
	case ground.Derived:
		return "derived from content that is present"
	default:
		return ground.ReasonNotFound
	}
}

// score is the assembler's bridge into the configured Scorer, filling in the
// signals only the assembler can compute.
func (a *assembler) score(f schema.Field, vr validate.Result, gr ground.Result, ev FieldEvidence) (float64, []Signal) {
	if gr.Applicable {
		ev.Grounding = gr.Grounding
	}
	if v, ok := validate.FormatSignal(vr); ok {
		_ = v // carried through Validation; kept explicit so the source is visible
	}
	s := a.cfg.scorer
	if s == nil {
		s = defaultScorer{}
	}
	return s.Score(ev)
}
