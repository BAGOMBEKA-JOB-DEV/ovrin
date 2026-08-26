package validate

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// CrossFieldResult is the outcome of one cross-field rule.
//
// It is separate from [RuleResult] because a cross-field rule is not a rule on
// a field: it names the several fields it read, and it can be inapplicable in a
// way a field rule cannot. It produces the cross_field confidence signal
// (docs/confidence.md §Signals).
type CrossFieldResult struct {
	// Name identifies the rule, for a review reason. It is a rule name chosen
	// by whoever declared the rule, never a value from the document.
	Name string

	// Fields are the field keys the rule read, so a reviewer knows where to
	// look. Keys, never values (docs/rules.md §7.5).
	Fields []string

	// Applicable reports whether the rule could run at all. A rule whose
	// inputs were not extracted has not failed — the missing field is already
	// reported by its own required rule — and counting it as a failure would
	// punish a document twice for one absence.
	Applicable bool

	// Passed reports the outcome, and is meaningful only when Applicable.
	Passed bool

	// Message says why not, and is empty when Passed. It describes the
	// inconsistency and never states the amounts: these strings reach logs.
	Message string
}

// CrossFieldRule checks one consistency property across sibling fields.
//
// The interface is deliberately tiny: a rule reads [Fields] and returns a
// verdict. It cannot fail, cannot do I/O and cannot see the document, so adding
// one is a self-contained decision and no rule can break an extraction.
type CrossFieldRule interface {
	// Name identifies the rule in results and review reasons.
	Name() string

	// Check reports the outcome. It must return a result rather than an error:
	// an inconsistency is a finding, not a failure (docs/adr/0004-partial-results.md).
	Check(f Fields) CrossFieldResult
}

// Fields is the converted value of every extracted field, keyed by the path
// used in Result.Fields: "total", "vendor.name", "items[0].unit_price".
//
// Only values that converted appear. A rule therefore never sees a fabricated
// zero, and the absence of a key means the value was not read — which is why
// [CrossFieldResult.Applicable] exists.
type Fields map[string]any

// Number returns a field's value as a float64.
//
// Any Go numeric type is accepted, because a schema may declare a total as
// float64 and a quantity as int and a rule that sums them should not care.
func (f Fields) Number(key string) (float64, bool) {
	v, ok := f[key]
	if !ok {
		return 0, false
	}
	return numeric(v)
}

// Time returns a field's value as a time.Time.
func (f Fields) Time(key string) (time.Time, bool) {
	t, ok := f[key].(time.Time)
	return t, ok
}

// Text returns a field's value as a string.
func (f Fields) Text(key string) (string, bool) {
	v, ok := f[key]
	if !ok {
		return "", false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.String {
		return "", false
	}
	return rv.String(), true
}

// Count returns how many elements of a slice field were extracted.
//
// The count comes from the keys present, so it is what was actually read rather
// than what the document claimed to contain.
func (f Fields) Count(slice string) int {
	prefix := slice + "["
	highest := -1
	for k := range f {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		end := strings.IndexByte(rest, ']')
		if end <= 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:end])
		if err == nil && n > highest {
			highest = n
		}
	}
	return highest + 1
}

// ElementKey builds the key of one leaf inside one element of a slice field:
// ElementKey("items", 0, "unit_price") is "items[0].unit_price".
func ElementKey(slice string, i int, leaf string) string {
	k := slice + "[" + strconv.Itoa(i) + "]"
	if leaf == "" {
		return k
	}
	return k + "." + leaf
}

// Tolerance bounds how far two amounts may differ and still be consistent.
//
// Both an absolute and a relative bound, because neither alone is right: an
// absolute cent covers rounding on one line and fails on a hundred, and a
// relative bound alone is meaninglessly tight near zero.
type Tolerance struct {
	// Absolute is the largest acceptable difference in the values' own units.
	Absolute float64

	// Relative is the largest acceptable difference as a fraction of the
	// larger value: 0.005 is half a percent.
	Relative float64
}

// DefaultTolerance is a cent, which is the rounding a single line item can
// introduce.
//
// A document with many rounded lines needs a wider one, and a caller summing
// fifty lines should say so rather than discover it as a review queue full of
// documents that are correct.
var DefaultTolerance = Tolerance{Absolute: 0.01}

// Within reports whether a and b agree to within the tolerance.
//
// The comparison carries a hair of slack proportional to the magnitude, because
// binary floating point cannot represent 100.01: subtracting 100 from it leaves
// a difference a few thousandths of a picocent over a cent, and a tolerance of
// exactly one cent would reject the very rounding it exists to accept. The
// slack is a thousand times the representation error and a billion times below
// any tolerance a caller would write.
func (t Tolerance) Within(a, b float64) bool {
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	slack := scale * 1e-12
	if diff <= t.Absolute+slack {
		return true
	}
	return t.Relative > 0 && diff <= t.Relative*scale+slack
}

// ruleFunc adapts a function to [CrossFieldRule].
type ruleFunc struct {
	name  string
	check func(Fields) CrossFieldResult
}

// Name implements [CrossFieldRule].
func (r ruleFunc) Name() string { return r.name }

// Check implements [CrossFieldRule].
func (r ruleFunc) Check(f Fields) CrossFieldResult { return r.check(f) }

// RuleFunc returns a cross-field rule from a function.
//
// The extension point. Consistency worth checking is specific to a document
// type — a payslip's deductions, a bank statement's running balance — and this
// package cannot enumerate them, so it makes writing one a three-line job
// instead of an interface implementation.
func RuleFunc(name string, check func(Fields) CrossFieldResult) CrossFieldRule {
	return ruleFunc{name: name, check: check}
}

// Sum returns a rule requiring that named fields add up to a total.
//
// The everyday case is a subtotal and a tax adding to a total: the single most
// checkable claim on an invoice, and the one that catches a misread digit that
// every other signal accepts.
func Sum(total string, tol Tolerance, parts ...string) CrossFieldRule {
	keys := append([]string{total}, parts...)
	return RuleFunc("sum:"+total, func(f Fields) CrossFieldResult {
		out := CrossFieldResult{Name: "sum:" + total, Fields: keys}
		want, ok := f.Number(total)
		if !ok || len(parts) == 0 {
			return out
		}
		got := 0.0
		for _, p := range parts {
			n, ok := f.Number(p)
			if !ok {
				return out
			}
			got += n
		}
		out.Applicable = true
		out.Passed = tol.Within(want, got)
		if !out.Passed {
			out.Message = "the parts do not add up to the total"
		}
		return out
	})
}

// SumItems returns a rule requiring that a slice's line items add up to a
// total.
//
// leaves are multiplied together within each element, so a line's quantity and
// unit price make its amount: SumItems("total", "items", tol, "quantity",
// "unit_price"). One leaf sums that leaf directly.
//
// A slice with no extracted elements makes the rule inapplicable rather than
// failed: a document whose line items were not read has not contradicted its
// total.
func SumItems(total, slice string, tol Tolerance, leaves ...string) CrossFieldRule {
	name := "sum_items:" + total
	return RuleFunc(name, func(f Fields) CrossFieldResult {
		out := CrossFieldResult{Name: name, Fields: []string{total, slice}}
		want, ok := f.Number(total)
		if !ok || len(leaves) == 0 {
			return out
		}
		n := f.Count(slice)
		if n == 0 {
			return out
		}
		got := 0.0
		for i := 0; i < n; i++ {
			line := 1.0
			for _, leaf := range leaves {
				v, ok := f.Number(ElementKey(slice, i, leaf))
				if !ok {
					// One unreadable line makes the sum unknowable. Reporting
					// a mismatch here would blame the total for a line item.
					return out
				}
				line *= v
			}
			got += line
		}
		out.Applicable = true
		out.Passed = tol.Within(want, got)
		if !out.Passed {
			out.Message = "the line items do not add up to the total"
		}
		return out
	})
}

// Before returns a rule requiring that one date is not after another.
//
// Equal dates pass: an invoice issued and due the same day is a document, not
// an inconsistency.
func Before(earlier, later string) CrossFieldRule {
	name := "before:" + earlier + "," + later
	return RuleFunc(name, func(f Fields) CrossFieldResult {
		out := CrossFieldResult{Name: name, Fields: []string{earlier, later}}
		a, okA := f.Time(earlier)
		b, okB := f.Time(later)
		if !okA || !okB {
			return out
		}
		out.Applicable = true
		out.Passed = !a.After(b)
		if !out.Passed {
			out.Message = "the dates are the wrong way round"
		}
		return out
	})
}

// CrossField runs the configured cross-field rules over an extraction's
// converted values.
//
// The results are returned in the order the rules were declared, so a caller
// rendering them shows the same order every time. A validator with no
// cross-field rules returns nothing, and the cross_field signal is then absent
// rather than zero (docs/confidence.md §Signals).
func (v *Validator) CrossField(f Fields) []CrossFieldResult {
	if len(v.crossField) == 0 {
		return nil
	}
	out := make([]CrossFieldResult, 0, len(v.crossField))
	for _, r := range v.crossField {
		out = append(out, r.Check(f))
	}
	return out
}
