package compare

import (
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/ground"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// Kind is the comparison kind of a value.
//
// It is an alias of [ground.Kind] rather than a second enumeration. The two
// packages ask closely related questions — is this value in the document, are
// these two readings of it the same — and a codebase carrying two spellings of
// "currency" would eventually answer them differently for no reason anybody
// could name.
type Kind = ground.Kind

// The comparison kinds, one per row of the table in docs/confidence.md
// §Comparison. They are [ground]'s constants under this package's name, so
// that a caller comparing values need not import both.
const (
	// KindUnknown means "work the kind out from the Go values", which is what
	// [KindOf] does. A value it cannot place — a struct, a map — is not
	// compared as a unit; its fields are compared one by one.
	KindUnknown = ground.KindUnknown

	// KindString compares after Unicode normalisation, whitespace collapse
	// and case folding.
	KindString = ground.KindString

	// KindNumber compares by value, so 25,000 and 25000 and 25 000 agree.
	KindNumber = ground.KindNumber

	// KindCurrency compares the amount and the currency, so 100 USD and
	// 100 EUR disagree.
	KindCurrency = ground.KindCurrency

	// KindDate compares as an instant, so 03/04/26 and 2026-04-03 agree.
	KindDate = ground.KindDate

	// KindBool compares the value, reading the spellings a form uses.
	KindBool = ground.KindBool

	// KindSlice compares length and then every element.
	KindSlice = ground.KindSlice
)

// DateOrder resolves ambiguous numeric dates such as 03/04/2026.
//
// It is an alias of [validate.DateOrder] because this package parses dates
// with validate's parser: a separate order type would have to be converted at
// every call, and a conversion that can be got wrong eventually is.
type DateOrder = validate.DateOrder

// The date orders, matching [validate]'s and the root package's. The zero
// value does not guess: an ambiguous date agrees with either reading of
// itself, which is a false agreement chosen over the 50% error rate of
// picking one (docs/rules.md §8.5).
const (
	// DateOrderUnknown accepts either reading of an ambiguous numeric date.
	DateOrderUnknown = validate.DateOrderUnknown

	// DayFirst reads 03/04/2026 as 3 April.
	DayFirst = validate.DayFirst

	// MonthFirst reads 03/04/2026 as 4 March.
	MonthFirst = validate.MonthFirst

	// YearFirst reads 2026/03/04 as 4 March.
	YearFirst = validate.YearFirst
)

// The agreement signal values.
//
// Binary, because two readings either agree about a field or they do not, and
// docs/confidence.md specifies no value in between. Majority voting over three
// or more readings is the extension ADR-0014 defers until two have been
// measured on real documents; it would go here.
const (
	// Agree is the signal when every reading produced the same value.
	Agree = 1.0

	// Disagree is the signal when they did not. It is a real zero, not an
	// absent signal: the readings were taken and they conflict.
	Disagree = 0.0
)

// The reasons a comparison reports, as constants so that nothing has to match
// on their text (docs/rules.md §2.2).
//
// None of them contains a value, and none of them ever will: a reason becomes
// a ReviewReason and a ReviewReason is logged (docs/rules.md §7.5).
const (
	// ReasonAbsent is one reading having produced no value at all. There is
	// nothing to compare, so no agreement signal is produced rather than a
	// zero one — the same treatment docs/confidence.md gives a cross-field
	// rule whose inputs were not extracted. The absence is already reported
	// by the field's own Found and required rule; counting it again would
	// punish the document twice for one absence.
	ReasonAbsent = "only one reading produced a value; there is nothing to compare"

	// ReasonNone is neither reading having produced a value.
	ReasonNone = "no reading produced a value"

	// ReasonSingle is a single reading. Agreement needs two, and its weight
	// is redistributed rather than counted against the field
	// (docs/confidence.md §Signals).
	ReasonSingle = "only one reading was taken; agreement needs two"

	// ReasonKind is a value with no comparison kind — a struct or a map,
	// which is compared field by field rather than as a unit.
	ReasonKind = "the values have no comparison kind; compare their fields instead"

	// ReasonDepth is a nesting depth beyond [MaxDepth].
	ReasonDepth = "the values nest more deeply than comparison goes"

	// ReasonFallback is a value that could not be read as its declared kind,
	// so the two were compared as text. Reported because a number that would
	// not parse is worth knowing about even when the two readings agree
	// about it.
	ReasonFallback = "the values could not be read as the declared kind and were compared as text"

	// ReasonDisagree is the plain fact of a disagreement. The values are on
	// the candidates, not in here.
	ReasonDisagree = "the readings produced different values for this field"
)

// MaxDepth is how far comparison descends into nested slices.
//
// A finite default, checked before recursing rather than after, because every
// limit in ovrin has one (docs/rules.md §5.2, ADR-0020). Eight is far beyond
// any schema a person would write and far below anything that could exhaust a
// stack.
const MaxDepth = 8

// Result is the outcome of comparing two readings of one value.
//
// It carries no error and no value. A disagreement is not a failure — it is
// the most useful thing this library can tell a caller about a field
// (docs/rules.md §8.4) — and the values are already in the caller's hands.
type Result struct {
	// Equal reports that the two readings are the same value. It is false
	// when Applicable is false, because "not compared" is not "agreed".
	Equal bool

	// Applicable reports whether a comparison was made at all.
	//
	// The scorer must treat an inapplicable comparison as an absent signal
	// rather than a zero one: an absent signal has its weight redistributed,
	// a zero signal is evidence against the value (docs/confidence.md
	// §Signals).
	Applicable bool

	// Kind is the kind the values were compared under, which is the declared
	// kind when one was given and the inferred kind otherwise.
	Kind Kind

	// Fallback reports that the values could not be read as Kind and were
	// compared as text instead.
	Fallback bool

	// Reason says why, in words that never contain a value. It is empty when
	// the readings agree with nothing else to report.
	Reason string
}

// Signal returns the agreement signal for a comparison, and whether there is
// one.
//
// The two-result shape is [validate.FormatSignal]'s, and it exists so that an
// absent signal cannot be mistaken for a zero one by a caller who forgot to
// check.
func (r Result) Signal() (float64, bool) {
	if !r.Applicable {
		return 0, false
	}
	if r.Equal {
		return Agree, true
	}
	return Disagree, true
}

type options struct {
	order         DateOrder
	caseSensitive bool
}

// Option configures a comparison.
type Option func(*options)

// WithDateOrder settles the day/month convention for a corpus that has one.
//
// Without it an ambiguous numeric date agrees with either reading of itself,
// so two readings that genuinely swapped day and month are not flagged. With
// it they are. It is not the default because guessing the convention wrong
// manufactures disagreements on every date in the corpus, which is worse.
func WithDateOrder(d DateOrder) Option {
	return func(o *options) { o.order = d }
}

// WithCaseSensitive compares strings without case folding.
//
// docs/confidence.md folds case because ACME LTD and Acme Ltd are one company
// and flagging them would make the feature useless through noise. ADR-0014
// leaves the exception open — "unless the field's format says otherwise" — and
// this is it: a base64 payload, a case-sensitive reference or a password-like
// token differs by case and a caller who knows that should be able to say so.
// Unicode normalisation and whitespace collapse still apply.
func WithCaseSensitive() Option {
	return func(o *options) { o.caseSensitive = true }
}

func newOptions(opts []Option) options {
	var o options
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}

// KindOf infers the comparison kind of a value.
//
// It delegates to [ground.KindOf] rather than reimplementing the inference,
// for the reason [Kind] is an alias. Like ground's, it never returns
// [KindCurrency]: an amount and a currency are two facts and a float64 carries
// one, so a currency comparison is requested by a caller who knows the field
// declares format=currency.
func KindOf(v any) Kind { return ground.KindOf(v) }

// Equal reports whether two independent readings of a value are the same
// value.
//
// kind selects the comparison semantics; [KindUnknown] infers it from the
// values. It is the boolean shorthand for [Values], and it answers false for a
// comparison that could not be made, so a caller who needs to tell "different"
// from "not compared" wants [Values].
func Equal(a, b any, kind Kind, opts ...Option) bool {
	return Values(a, b, kind, opts...).Equal
}

// Values compares two independent readings of one value and reports whether
// they are the same value, without resolving the difference when they are not.
//
// It is symmetric — Values(a, b) and Values(b, a) agree — and reflexive for
// any value it can compare at all, and the fuzz target asserts both, because
// a comparison that quietly stopped being either would silently change which
// documents reach a reviewer.
func Values(a, b any, kind Kind, opts ...Option) Result {
	return compare(a, b, kind, newOptions(opts), 0)
}

// compare is the recursive body of [Values]. depth bounds the descent into
// nested slices.
func compare(a, b any, kind Kind, o options, depth int) Result {
	if depth > MaxDepth {
		return Result{Kind: kind, Reason: ReasonDepth}
	}
	aAbsent, bAbsent := absent(a), absent(b)
	switch {
	case aAbsent && bAbsent:
		return Result{Kind: kind, Reason: ReasonNone}
	case aAbsent || bAbsent:
		return Result{Kind: kind, Reason: ReasonAbsent}
	}
	if kind == KindUnknown {
		kind = inferKind(a, b)
	}

	switch kind {
	case KindSlice:
		return compareSlices(a, b, o, depth)
	case KindUnknown:
		return Result{Kind: kind, Reason: ReasonKind}
	}

	if eq, ok := compareAs(a, b, kind, o); ok {
		r := Result{Equal: eq, Applicable: true, Kind: kind}
		if !eq {
			r.Reason = ReasonDisagree
		}
		return r
	}

	// The values did not read as their kind. Comparing them as text keeps the
	// answer conservative: two readings that produced the same unparseable
	// text have not disagreed, and two that produced different text have.
	eq, ok := textEqual(a, b, o.caseSensitive)
	if !ok {
		return Result{Kind: kind, Reason: ReasonKind}
	}
	r := Result{Equal: eq, Applicable: true, Kind: kind, Fallback: true, Reason: ReasonFallback}
	if !eq {
		r.Reason = ReasonDisagree
	}
	return r
}

// compareAs applies one row of the comparison table. The second result
// reports whether the values could be read as that kind at all.
func compareAs(a, b any, kind Kind, o options) (bool, bool) {
	switch kind {
	case KindNumber:
		return numbersEqual(a, b)
	case KindCurrency:
		return amountsEqual(a, b)
	case KindDate:
		return datesEqual(a, b, o.order)
	case KindBool:
		return boolsEqual(a, b)
	case KindString:
		return textEqual(a, b, o.caseSensitive)
	default:
		return false, false
	}
}

// inferKind works out what two values should be compared as.
//
// The stronger of the two inferences wins, and a typed value beats a string:
// a pipeline that carries 25000 as a float64 from one reading and "25,000" as
// text from the other has not found a disagreement, and comparing the pair as
// text would report one on nearly every formatted figure in every document.
func inferKind(a, b any) Kind {
	ka, kb := KindOf(a), KindOf(b)
	if rank(ka) >= rank(kb) {
		return ka
	}
	return kb
}

// rank orders the kinds by how much they claim about a value, so that the
// more specific of two inferences is the one used.
func rank(k Kind) int {
	switch k {
	case KindCurrency:
		return 5
	case KindDate:
		return 4
	case KindNumber:
		return 3
	case KindBool:
		return 2
	case KindSlice:
		return 1
	case KindString:
		return 0
	default: // KindUnknown claims nothing at all.
		return -1
	}
}
