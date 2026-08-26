package compare

import (
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// datesEqual compares two values as dates: the same instant after parsing, so
// that 03/04/26 and 2026-04-03 agree (docs/confidence.md §Comparison).
//
// A reading with no clock is compared by calendar date rather than by instant.
// Comparing a date-only value against a timestamped one as instants would put
// every such pair fourteen hours apart and report a disagreement that is a
// difference in precision, not in value. The date is taken in each value's own
// location, on the same principle validate reads dates by: an invoice issued
// on 3 April in Kampala was issued on 3 April, whatever a UTC clock said.
func datesEqual(a, b any, order DateOrder) (bool, bool) {
	xs, xClock, okx := datesOf(a, order)
	ys, yClock, oky := datesOf(b, order)
	if !okx || !oky {
		return false, false
	}
	instants := xClock && yClock
	for _, x := range xs {
		for _, y := range ys {
			if instants && x.Equal(y) {
				return true, true
			}
			if !instants && sameDay(x, y) {
				return true, true
			}
		}
	}
	return false, true
}

// sameDay reports whether two times fall on the same calendar day, each read
// in its own location.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// datesOf reads a value as a date, returning every reading of it.
//
// More than one only for an ambiguous numeric date such as 03/04/2026, which
// [validate.ParseDateTime] refuses to resolve without an order. Both readings
// are kept and a match against either counts, because the two readings of the
// document are not the thing in disagreement — the world's date notation is —
// and reporting that as a field-level conflict would flag the date on every
// document from a corpus that writes them that way. [WithDateOrder] removes
// the ambiguity for a caller who knows the convention, and with it this
// blindness to a genuine day/month swap.
func datesOf(v any, order DateOrder) ([]time.Time, bool, bool) {
	if t, ok := timeOf(v); ok {
		return []time.Time{t}, !isMidnight(t), true
	}
	s, ok := textOf(v)
	if !ok {
		return nil, false, false
	}
	p := validate.ParseDateTime(canonical(s), order)
	switch {
	case p.OK:
		return []time.Time{p.Time}, !isMidnight(p.Time), true
	case p.Ambiguity != nil:
		d, m := p.Ambiguity.DayFirst, p.Ambiguity.MonthFirst
		return []time.Time{d, m}, !isMidnight(d), true
	default:
		return nil, false, false
	}
}

// timeOf returns the value as a [time.Time], through a pointer if need be.
func timeOf(v any) (time.Time, bool) {
	if t, ok := v.(time.Time); ok {
		return t, true
	}
	rv, ok := deref(v)
	if !ok || !rv.CanInterface() {
		return time.Time{}, false
	}
	t, ok := rv.Interface().(time.Time)
	return t, ok
}

// isMidnight reports whether a time carries no clock component.
//
// A date-only value is midnight by ovrin's convention (docs/schema.md
// format=date), so this is how "no clock was read" is spelled. A document that
// really does state midnight is treated as stating a date, which costs
// nothing: a comparison by calendar day agrees wherever a comparison by
// instant would have.
func isMidnight(t time.Time) bool {
	return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0
}
