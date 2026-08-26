package compare

import (
	"reflect"
	"strings"
	"time"
)

// compareSlices compares two slices: same length, and every element equal
// (docs/confidence.md §Comparison).
//
// Length first, because two readings that found a different number of line
// items disagree about the document whatever the elements say, and it is the
// cheaper question. Elements are compared with their own inferred kinds, so a
// slice of amounts compares as amounts.
func compareSlices(a, b any, o options, depth int) Result {
	x, okx := sliceOf(a)
	y, oky := sliceOf(b)
	if !okx || !oky {
		return Result{Kind: KindSlice, Reason: ReasonKind}
	}
	if x.Len() != y.Len() {
		return Result{Applicable: true, Kind: KindSlice, Reason: ReasonDisagree}
	}

	out := Result{Kind: KindSlice}
	for i := 0; i < x.Len(); i++ {
		xe, ye := x.Index(i), y.Index(i)
		if !xe.CanInterface() || !ye.CanInterface() {
			continue
		}
		r := compare(xe.Interface(), ye.Interface(), KindUnknown, o, depth+1)
		if !r.Applicable {
			// An element neither reading produced is absence, which the
			// field's own rules report; it is not this slice disagreeing
			// with itself.
			continue
		}
		out.Applicable = true
		out.Fallback = out.Fallback || r.Fallback
		if !r.Equal {
			out.Reason = ReasonDisagree
			return out
		}
	}
	if !out.Applicable {
		out.Reason = ReasonNone
		return out
	}
	out.Equal = true
	if out.Fallback {
		out.Reason = ReasonFallback
	}
	return out
}

// sliceOf returns the value as a slice or array.
func sliceOf(v any) (reflect.Value, bool) {
	rv, ok := deref(v)
	if !ok {
		return reflect.Value{}, false
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return reflect.Value{}, false
	}
	return rv, true
}

// absent reports whether a reading produced no value at all.
//
// A zero is not absent. "The total is zero" and "we could not read the total"
// are the distinction the whole library turns on (docs/rules.md §8.5), so a
// numeric zero and a false boolean are values like any other and are compared
// like any other.
//
// Blank text is absent: a model that returned "" or "   " for a field did not
// read one, and a comparison of two blanks would otherwise report agreement
// about nothing. So is the zero [time.Time] — no document states the first of
// January in the year 1 — and so is an empty slice, which is how a reading
// says it found no line items.
//
// The consequence is deliberate and worth stating: when one reading finds a
// value and the other finds nothing, this package reports no agreement signal
// rather than a disagreement. The absence is already reported by the field's
// Found and by its required rule, and docs/confidence.md gives a cross-field
// rule whose inputs are missing exactly this treatment — no signal rather than
// a zero one — because counting it twice punishes the document twice for one
// absence.
func absent(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case bool:
		return false
	case time.Time:
		return t.IsZero()
	}
	rv, ok := deref(v)
	if !ok {
		return true
	}
	if rv.CanInterface() {
		if t, ok := rv.Interface().(time.Time); ok {
			return t.IsZero()
		}
	}
	switch rv.Kind() {
	case reflect.String:
		return strings.TrimSpace(rv.String()) == ""
	case reflect.Slice, reflect.Map:
		return rv.IsNil() || rv.Len() == 0
	case reflect.Array:
		return rv.Len() == 0
	default:
		return false
	}
}
