package ovrin

import (
	"fmt"
	"strings"
)

// Result is what an extraction produced.
//
// The error returned alongside it and the Valid field answer different
// questions. A non-nil error means nothing usable came back and Result is nil.
// Valid reports whether every validation rule passed, which is independent:
// data can come back that is real, useful and not yet good enough to act on.
//
// See docs/adr/0004-partial-results.md.
type Result[T any] struct {
	// Data holds every field that was read, whether or not Valid.
	//
	// A field that could not be read is left at its zero value here and marked
	// absent in Fields. Nothing is ever guessed to satisfy the struct.
	Data T

	// Valid reports whether every validation rule passed.
	Valid bool

	// Confidence is the aggregate over fields, weighted by whether a field is
	// required, so a missing optional field does not drag down a clean
	// document.
	//
	// Until the weights are calibrated this is a ranking signal, not a
	// probability. It orders a review queue well; it does not mean "correct
	// this often".
	Confidence float64

	// Fields holds one entry per schema field, including fields that were not
	// found. A slice field additionally contributes one entry per extracted
	// element, keyed "items[0]", so the number of keys depends on what was
	// read.
	Fields map[string]FieldResult

	// NeedsReview reports whether a person should look before this is used.
	NeedsReview bool

	// Reasons says why, one entry per triggering condition.
	Reasons []ReviewReason

	// Metadata records how the result was produced.
	Metadata Metadata
}

// FieldResult is one field, and the evidence for it.
//
// This type carries []error and so does not marshal usefully to JSON. Use
// [Result.Explain] for a value that does.
type FieldResult struct {
	// Value is what was extracted.
	Value any

	// Found reports presence.
	//
	// This is not Value != zero, and the distinction is the point: a payments
	// system must be able to tell "the total is zero" from "we could not read
	// the total".
	Found bool

	// Confidence is on 0..1.
	Confidence float64

	// Valid reports whether every rule on this field passed.
	Valid bool

	// Signals are the inputs that produced Confidence.
	Signals []Signal

	// Provenance is where the value came from.
	Provenance []Provenance

	// Candidates holds every competing value when two readings disagreed.
	// Value holds the higher-confidence one, so a caller who ignores this
	// still gets the better answer.
	Candidates []Candidate

	// Errors says why the field is not Valid, or why it was not Found.
	Errors []error
}

// Candidate is one reading's answer for a field, when readings disagreed.
//
// Disagreement is recorded rather than resolved. Two readings fail in
// uncorrelated ways, so when they differ at least one is definitely wrong and
// silently preferring either is the failure this exists to prevent.
type Candidate struct {
	// Value is this reading's answer.
	Value any

	// Reading is which reading produced it.
	Reading Reading

	// Source is where in the document it came from.
	Source Provenance
}

// Explanation is the decomposition of one field's result.
//
// A value rather than formatted text, because the consumers are review queues,
// audit stores, dashboards and JSON APIs, none of which want a string. The
// terminal rendering is [Explanation.String].
//
// It is assembled from what the pipeline already recorded, so it cannot
// disagree with the [Result] it came from.
type Explanation struct {
	Field      string
	Value      any
	Found      bool
	Confidence float64

	// Signals are every input to Confidence, with its weight and a one-line
	// note.
	Signals []Signal

	// Provenance is where the value came from.
	Provenance []Provenance

	// Candidates holds competing readings, if any.
	Candidates []Candidate

	// Validation is each rule, whether it passed, and why not.
	Validation []RuleResult

	// Reasons is why this field needs review, if it does.
	Reasons []ReviewReason
}

// Explain returns the decomposition of one field, and whether that field
// exists in the schema.
//
// The key is the field path as it appears in [Result.Fields]: "total",
// "vendor.name", "items[0].unit_price".
func (r *Result[T]) Explain(field string) (*Explanation, bool) {
	f, ok := r.Fields[field]
	if !ok {
		return nil, false
	}
	e := &Explanation{
		Field:      field,
		Value:      f.Value,
		Found:      f.Found,
		Confidence: f.Confidence,
		Signals:    f.Signals,
		Provenance: f.Provenance,
		Candidates: f.Candidates,
	}
	for _, reason := range r.Reasons {
		if reason.Field == field {
			e.Reasons = append(e.Reasons, reason)
		}
	}
	return e, true
}

// String renders an Explanation for a person reading a terminal.
//
// This format is not part of the compatibility promise. Anyone parsing it has
// taken a dependency that will break; the struct fields are the stable
// interface.
func (e *Explanation) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Field:       %s\n", e.Field)
	if e.Found {
		fmt.Fprintf(&b, "Value:       %v\n", e.Value)
	} else {
		b.WriteString("Value:       (not found)\n")
	}
	fmt.Fprintf(&b, "Confidence:  %.2f\n", e.Confidence)

	if len(e.Signals) > 0 {
		b.WriteString("\nSignals\n")
		width := 0
		for _, s := range e.Signals {
			if len(s.Name) > width {
				width = len(s.Name)
			}
		}
		for _, s := range e.Signals {
			if s.Weight == 0 {
				fmt.Fprintf(&b, "  %-*s     —          %s\n", width, s.Name, s.Note)
				continue
			}
			fmt.Fprintf(&b, "  %-*s  %.2f  ×%.2f   %s\n", width, s.Name, s.Value, s.Weight, s.Note)
		}
	}

	if len(e.Candidates) > 1 {
		b.WriteString("\nCandidates\n")
		for _, c := range e.Candidates {
			fmt.Fprintf(&b, "  %-10v %s\n", c.Value, c.Source.Method)
		}
	}

	if len(e.Provenance) > 0 {
		b.WriteString("\nProvenance\n")
		for _, p := range e.Provenance {
			fmt.Fprintf(&b, "  %-15s page %d", p.Method, p.Page)
			if p.Box != nil {
				fmt.Fprintf(&b, "   box (%.0f,%.0f)-(%.0f,%.0f)",
					p.Box.MinX, p.Box.MinY, p.Box.MaxX, p.Box.MaxY)
			}
			if p.Exact {
				b.WriteString("   exact")
			}
			b.WriteString("\n")
		}
	}

	if len(e.Validation) > 0 {
		b.WriteString("\nValidation\n")
		width := 0
		for _, v := range e.Validation {
			if len(v.Rule) > width {
				width = len(v.Rule)
			}
		}
		for _, v := range e.Validation {
			outcome := "fail"
			if v.Passed {
				outcome = "pass"
			}
			fmt.Fprintf(&b, "  %-*s  %s", width, v.Rule, outcome)
			if v.Message != "" {
				fmt.Fprintf(&b, "  %s", v.Message)
			}
			b.WriteString("\n")
		}
	}

	if len(e.Reasons) > 0 {
		b.WriteString("\nReasons\n")
		for _, r := range e.Reasons {
			fmt.Fprintf(&b, "  %s — %s\n", r.Field, r.Why)
		}
	}
	return b.String()
}
