package validate

import (
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// AmbiguousFormatSignal is the format signal for a value that parsed but could
// not be resolved to one reading.
//
// Half rather than zero, because an ambiguous date is not a bad value: the text
// is a perfectly good date and only its order is unknown. Scoring it as a
// format failure would rank it below a value that is genuinely malformed, which
// is backwards (docs/schema.md §format).
const AmbiguousFormatSignal = 0.5

// FormatSignal returns the format signal for a field, and whether the field
// declared a format at all.
//
// Absent rather than zero when no format is declared: a field with nothing to
// check has not failed the check, and scoring it 0.0 would penalise every
// schema that did not use the rule (docs/confidence.md §Signals).
func FormatSignal(r Result) (float64, bool) {
	var declared, passed bool
	for _, rr := range r.Rules {
		if strings.HasPrefix(rr.Rule, schema.RuleFormat+"=") {
			declared, passed = true, rr.Passed
			break
		}
	}
	if !declared {
		return 0, false
	}
	switch {
	case passed:
		return 1, true
	case r.Ambiguity != nil:
		return AmbiguousFormatSignal, true
	default:
		return 0, true
	}
}

// SchemaSignal returns the schema signal for a field: whether the value
// satisfied its declared type and rules.
//
// A value that was found but could not be converted scores zero however few
// rules it declared, because failing the type is failing the schema. A field
// with no rules and nothing wrong scores one: nothing was violated.
func SchemaSignal(r Result) float64 {
	if r.Found && !r.Converted && !composite(r) {
		return 0
	}
	if len(r.Rules) == 0 {
		return 1
	}
	passed := 0
	for _, rr := range r.Rules {
		if rr.Passed {
			passed++
		}
	}
	return float64(passed) / float64(len(r.Rules))
}

// CrossFieldSignal returns the cross_field signal, and whether any rule
// applied.
//
// Absent when no rule could run, for the same reason as [FormatSignal]: a
// document whose line items were not extracted has not contradicted its total.
func CrossFieldSignal(results []CrossFieldResult) (float64, bool) {
	applicable, passed := 0, 0
	for _, r := range results {
		if !r.Applicable {
			continue
		}
		applicable++
		if r.Passed {
			passed++
		}
	}
	if applicable == 0 {
		return 0, false
	}
	return float64(passed) / float64(applicable), true
}

// composite reports whether a result belongs to a struct or slice field, whose
// value this package does not convert: only the caller holds the results for
// the nested fields it is assembled from.
func composite(r Result) bool {
	return r.Kind == schema.KindArray || r.Kind == schema.KindObject
}
