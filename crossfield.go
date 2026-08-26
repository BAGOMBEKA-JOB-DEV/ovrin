package ovrin

import (
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// CrossFieldRule checks one consistency property across sibling fields.
//
// It is the check no single field can make: line items adding to a total, an
// issue date before a due date. Those catch the misread digit that every other
// signal accepts, because a wrong number is still a number and still passes
// its type, its format and its range.
//
// A rule reads values and returns a verdict. It cannot fail, cannot do I/O and
// cannot see the document — an inconsistency is a finding, not an error
// (docs/adr/0004-partial-results.md).
type CrossFieldRule interface {
	// Name identifies the rule in results and review reasons. It is a name
	// you choose, and it appears in a ReviewReason, so it must not contain a
	// value from the document.
	Name() string

	// Check reports the outcome.
	Check(f CrossFields) CrossFieldResult
}

// CrossFields is the converted value of every extracted field, keyed by the
// path used in [Result.Fields]: "total", "vendor.name", "items[0].unit_price".
//
// Only values that converted appear. A rule therefore never sees a fabricated
// zero, and a missing key means the value was not read — which is what
// [CrossFieldResult.Applicable] is for.
type CrossFields map[string]any

// Number returns a numeric field, and whether it was read.
func (f CrossFields) Number(key string) (float64, bool) {
	return validate.Fields(f).Number(key)
}

// Text returns a string field, and whether it was read.
func (f CrossFields) Text(key string) (string, bool) {
	return validate.Fields(f).Text(key)
}

// Time returns a date field, and whether it was read.
func (f CrossFields) Time(key string) (time.Time, bool) {
	return validate.Fields(f).Time(key)
}

// Count returns how many elements of a slice field were extracted.
func (f CrossFields) Count(slice string) int {
	return validate.Fields(f).Count(slice)
}

// CrossFieldResult is the outcome of one cross-field rule.
//
// Separate from [RuleResult] because a cross-field rule is not a rule on a
// field: it names the several fields it read, and it can be inapplicable in a
// way a field rule cannot.
type CrossFieldResult struct {
	// Name identifies the rule, for a review reason. A name you chose, never
	// a value from the document.
	Name string

	// Fields are the field keys the rule read, so a reviewer knows where to
	// look. Keys, never values (docs/rules.md §7.5).
	Fields []string

	// Applicable reports whether the rule could run at all. A rule whose
	// inputs were not extracted has not failed — the missing field is already
	// reported by its own required rule, and counting it again would punish a
	// document twice for one absence.
	Applicable bool

	// Passed reports the outcome, and is meaningful only when Applicable.
	Passed bool

	// Message says why not, and is empty when Passed. It describes the
	// inconsistency and never states the amounts: these strings reach logs.
	Message string
}

// Tolerance bounds how far two amounts may differ and still be consistent.
//
// Both an absolute and a relative bound, because neither alone is right: an
// absolute cent covers rounding on one line and fails across a hundred, and a
// relative fraction is meaningless near zero.
type Tolerance struct {
	// Absolute is the largest acceptable difference in the values' own units.
	Absolute float64

	// Relative is the largest acceptable difference as a fraction of the
	// larger value: 0.005 is half a percent.
	Relative float64
}

// Sum returns a rule requiring that named fields add up to a total.
//
// The everyday case is a subtotal and a tax adding to a total: the single most
// checkable claim on an invoice.
//
//	ovrin.Sum("total", ovrin.Tolerance{Absolute: 0.01}, "subtotal", "vat")
func Sum(total string, tol Tolerance, parts ...string) CrossFieldRule {
	return crossRule{validate.Sum(total, validate.Tolerance(tol), parts...)}
}

// SumItems returns a rule requiring that a slice's line items add up to a
// total.
//
// The leaf fields are multiplied together within each element, so a line's
// quantity and unit price make its amount:
//
//	ovrin.SumItems("total", "items", tol, "quantity", "unit_price")
func SumItems(total, slice string, tol Tolerance, leaves ...string) CrossFieldRule {
	return crossRule{validate.SumItems(total, slice, validate.Tolerance(tol), leaves...)}
}

// Before returns a rule requiring that one date is not after another.
//
// Equal dates pass: an invoice issued and due the same day is a document, not
// an inconsistency.
func Before(earlier, later string) CrossFieldRule {
	return crossRule{validate.Before(earlier, later)}
}

// CrossFieldFunc returns a rule from a function.
//
// The extension point. Consistency worth checking is specific to a document
// type, and the three rules above are the ones common enough to ship rather
// than an attempt at a complete set.
func CrossFieldFunc(name string, check func(CrossFields) CrossFieldResult) CrossFieldRule {
	return crossRule{validate.RuleFunc(name, func(f validate.Fields) validate.CrossFieldResult {
		return validate.CrossFieldResult(check(CrossFields(f)))
	})}
}

// WithCrossField declares rules checked across fields after extraction.
//
// They produce the [SignalCrossField] signal, and a failure sets NeedsReview
// with a reason naming the rule.
//
// Rules are declared here rather than in a struct tag because a rule spans
// several fields and has no natural home on any one of them — and because
// ADR-0006 fixes the tag vocabulary at five rules, none of which could express
// "these three fields must add up".
func WithCrossField(rules ...CrossFieldRule) Option {
	return optionFunc(func(c *config) {
		c.crossField = append(c.crossField, rules...)
	})
}

// crossRule adapts an internal rule to the public interface. The two are the
// same shape; the wrapper exists only because internal packages cannot be part
// of the public API.
type crossRule struct{ r validate.CrossFieldRule }

func (c crossRule) Name() string { return c.r.Name() }

func (c crossRule) Check(f CrossFields) CrossFieldResult {
	return CrossFieldResult(c.r.Check(validate.Fields(f)))
}

// internalRules converts back for the validator, which is the only consumer.
func internalRules(rules []CrossFieldRule) []validate.CrossFieldRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]validate.CrossFieldRule, 0, len(rules))
	for _, r := range rules {
		if c, ok := r.(crossRule); ok {
			out = append(out, c.r)
			continue
		}
		rule := r
		out = append(out, validate.RuleFunc(rule.Name(), func(f validate.Fields) validate.CrossFieldResult {
			return validate.CrossFieldResult(rule.Check(CrossFields(f)))
		}))
	}
	return out
}
