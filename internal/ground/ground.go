package ground

import (
	"reflect"
	"strconv"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// The four grounding signal values.
//
// They are specified in docs/pipeline.md stage 8 and docs/confidence.md, not
// chosen here, and they are constants so that a change to them is a change to
// the documented contract rather than a number somebody tuned.
const (
	// Verbatim is the value appearing in the source exactly as extracted.
	Verbatim = 1.0

	// Normalised is the same value in a different form — 25,000 against
	// 25000, 3 March 2026 against 2026-03-03.
	Normalised = 0.8

	// Derived is a value computed or reformatted from content that is
	// present. It is declared by the caller, never detected here; see the
	// package comment.
	Derived = 0.5

	// NotFound is a value that appears nowhere in the document. The model may
	// have invented it, and it is reported as such.
	NotFound = 0.0
)

// ReasonNotFound is the review reason for an ungrounded value.
//
// It names the cause and never the value, because a reason becomes a
// ReviewReason and a ReviewReason ends up in systems nobody audited
// (docs/rules.md §7.5). It is a constant so that nothing has to match on the
// text of it (docs/rules.md §2.2).
const ReasonNotFound = "value not found in the source; it may have been inferred or invented"

// Kind is the comparison kind of a value.
//
// It is coarser than a Go type and finer than a schema kind, because
// comparison semantics are what matter here: every integer width compares
// identically, and an amount with a currency compares differently from a bare
// number even though both are float64 (docs/confidence.md, Comparison).
type Kind string

const (
	// KindUnknown is the zero value. Passing it to [Ground] means "work it
	// out from the Go type", which is what [KindOf] does.
	KindUnknown Kind = ""

	// KindString compares after Unicode normalisation, whitespace collapse
	// and case folding.
	KindString Kind = "string"

	// KindNumber compares by value after separators are stripped.
	KindNumber Kind = "number"

	// KindCurrency compares the amount and the currency, and disagrees when
	// either differs.
	KindCurrency Kind = "currency"

	// KindDate compares as an instant, so 03/04/26 and 2026-04-03 are one
	// date.
	KindDate Kind = "date"

	// KindBool compares the value. Grounding a boolean is weak: a document
	// that means yes rarely writes "true", and this kind exists so that the
	// weakness is visible rather than hidden inside a string search.
	KindBool Kind = "bool"

	// KindSlice grounds every element and reports the mean, with the
	// per-element results kept.
	KindSlice Kind = "slice"
)

// String returns the kind, or "unknown" for the zero value.
func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

// DateOrder resolves ambiguous numeric dates such as 03/04/2026.
//
// It mirrors ovrin.DateOrder rather than importing it, for the same reason
// [normalise.Span] does: the root package will import the pipeline that
// imports this one. The zero value does not guess — an ambiguous token
// matches if either reading of it equals the value, because the question here
// is whether the value appears in the document and under either reading it
// does. Deciding which reading is correct is validation's job, not this one's.
type DateOrder string

const (
	// DateOrderUnknown accepts either reading of an ambiguous numeric date.
	DateOrderUnknown DateOrder = ""

	// DayFirst reads 03/04/2026 as 3 April.
	DayFirst DateOrder = "dmy"

	// MonthFirst reads 03/04/2026 as 4 March.
	MonthFirst DateOrder = "mdy"

	// YearFirst reads 2026/03/04 as 4 March.
	YearFirst DateOrder = "ymd"
)

// Result is the outcome of grounding one value.
//
// A nil Span means the position is not known, never that the value is not on
// the page (docs/adr/0015-provenance.md).
type Result struct {
	// Grounding is one of [Verbatim], [Normalised], [Derived] or [NotFound].
	Grounding float64

	// Exact reports a verbatim match, and fills Provenance.Exact.
	Exact bool

	// Span is the range in the normalised text, or nil when nothing matched.
	Span *normalise.Span

	// Page is the 1-based page the match is on, or 0.
	Page int

	// Box is the union of the matched regions on Page, or nil when the
	// reading gave no geometry.
	Box *normalise.Rect

	// Regions is one box per line the match crosses, so a value rejoined
	// across a line break highlights as two boxes rather than one covering
	// everything between them.
	Regions []normalise.Region

	// Applicable reports whether a grounding signal was produced at all.
	//
	// A struct, a nil pointer, an empty string and a value the pipeline never
	// found have no groundable text. The scorer must treat the signal as
	// absent rather than as zero: an absent signal has its weight
	// redistributed, a zero signal is evidence against the value
	// (docs/confidence.md, Signals).
	Applicable bool

	// Reason is [ReasonNotFound] when the search failed, and empty
	// otherwise. It never contains the value.
	Reason string

	// Elements holds the per-element results for [KindSlice], in order.
	Elements []Result
}

type options struct {
	derivable bool
	order     DateOrder
}

// Option configures one grounding search.
type Option func(*options)

// WithDerivable declares that this field is legitimately computed or
// reformatted from content that is present, so that a failed search scores
// [Derived] with no review reason rather than [NotFound] with one.
//
// The caller sets it from what validation already established — a total whose
// cross-field rule passed is consistent with its line items, and that is the
// evidence. Setting it unconditionally disables the one check in this
// pipeline that catches an invented value, so it is deliberately not the
// default.
func WithDerivable() Option {
	return func(o *options) { o.derivable = true }
}

// WithDateOrder resolves ambiguous numeric dates for a corpus whose
// convention is known. Without it, either reading of an ambiguous date is
// accepted.
func WithDateOrder(d DateOrder) Option {
	return func(o *options) { o.order = d }
}

// KindOf infers the comparison kind from a Go value.
//
// It never returns [KindCurrency]: an amount and a currency are two facts and
// a float64 carries one, so currency grounding is requested explicitly by a
// caller that knows the field declares `format=currency`. It never returns
// [KindUnknown] for a value it can handle, and returns it for a struct or a
// map, which must be grounded field by field rather than as a unit.
func KindOf(v any) Kind {
	switch v.(type) {
	case nil:
		return KindUnknown
	case string:
		return KindString
	case bool:
		return KindBool
	case time.Time:
		return KindDate
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return KindUnknown
		}
		rv = rv.Elem()
		if rv.CanInterface() {
			if k := KindOf(rv.Interface()); k != KindUnknown {
				return k
			}
		}
	}
	switch rv.Kind() {
	case reflect.String:
		return KindString
	case reflect.Bool:
		return KindBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return KindNumber
	case reflect.Slice, reflect.Array:
		return KindSlice
	default:
		return KindUnknown
	}
}

// Ground searches doc for value and returns the grounding outcome.
//
// kind selects the comparison semantics; [KindUnknown] infers it with
// [KindOf]. The search is verbatim first and type-aware second, so a value
// present in its own form is never demoted to a normalised match.
//
// It returns a value rather than an error. A value that cannot be grounded is
// not a failed extraction, and a failed field is not a failed extraction
// (docs/rules.md §2.6).
func Ground(doc *normalise.Result, value any, kind Kind, opts ...Option) Result {
	var o options
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	if kind == KindUnknown {
		kind = KindOf(value)
	}
	if doc == nil || doc.Text == "" || kind == KindUnknown || value == nil {
		return Result{}
	}
	if kind == KindSlice {
		return groundSlice(doc, value, opts...)
	}

	lit, ok := literal(value)
	if !ok || !groundable(lit) {
		return Result{}
	}

	if sp, found := findLiteral(doc, lit, kind); found {
		return hit(doc, sp, true, Verbatim)
	}
	if sp, found := findNormalisedValue(doc, value, lit, kind, o); found {
		return hit(doc, sp, false, Normalised)
	}
	if o.derivable {
		return Result{Grounding: Derived, Applicable: true}
	}
	return Result{Grounding: NotFound, Applicable: true, Reason: ReasonNotFound}
}

// findNormalisedValue runs the type-aware pass.
func findNormalisedValue(doc *normalise.Result, value any, lit string, kind Kind, o options) (normalise.Span, bool) {
	switch kind {
	case KindString, KindBool:
		return findFolded(doc, lit, kind)
	case KindNumber:
		n, ok := asFloat(value)
		if !ok {
			return normalise.Span{}, false
		}
		return findNumber(doc, n)
	case KindCurrency:
		return findCurrency(doc, lit)
	case KindDate:
		want, ok := asDates(value, o.order)
		if !ok {
			return normalise.Span{}, false
		}
		return findDate(doc, want, o.order)
	default:
		return normalise.Span{}, false
	}
}

// groundSlice grounds every element and reports the mean.
//
// The mean rather than the minimum: a slice of twelve line items with one
// element the model reformatted is not as suspect as a slice with nothing in
// the document at all, and a minimum cannot tell those apart. Per-element
// results are kept so the pipeline, which keys a slice's elements separately
// in Result.Fields, can use them directly.
func groundSlice(doc *normalise.Result, value any, opts ...Option) Result {
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return Result{}
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return Result{}
	}
	if rv.Len() == 0 {
		return Result{}
	}

	out := Result{Applicable: true, Exact: true}
	sum, n := 0.0, 0
	for i := 0; i < rv.Len(); i++ {
		e := rv.Index(i)
		if !e.CanInterface() {
			continue
		}
		r := Ground(doc, e.Interface(), KindUnknown, opts...)
		out.Elements = append(out.Elements, r)
		if !r.Applicable {
			continue
		}
		sum += r.Grounding
		n++
		if !r.Exact {
			out.Exact = false
		}
		if r.Span != nil && out.Span == nil {
			out.Span = r.Span
			out.Page = r.Page
			out.Box = r.Box
			out.Regions = r.Regions
		}
	}
	if n == 0 {
		return Result{Elements: out.Elements}
	}
	out.Grounding = sum / float64(n)
	if out.Grounding == NotFound {
		out.Exact = false
		out.Reason = ReasonNotFound
	}
	return out
}

// hit builds the Result for a match, filling the page and the geometry from
// the mapping the normaliser kept.
func hit(doc *normalise.Result, sp normalise.Span, exact bool, signal float64) Result {
	r := Result{
		Grounding:  signal,
		Exact:      exact,
		Span:       &sp,
		Applicable: true,
		Page:       doc.PageAt(sp.Start),
		Regions:    doc.Regions(sp),
	}
	var box normalise.Rect
	for _, rg := range r.Regions {
		if rg.Page == r.Page {
			box = box.Union(rg.Box)
		}
	}
	if !box.Zero() {
		r.Box = &box
	}
	return r
}

// literal returns the value's own text — what a verbatim match looks for —
// and whether the value has one.
func literal(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02"), true
		}
		return t.Format(time.RFC3339), true
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
		if rv.CanInterface() {
			if s, ok := literal(rv.Interface()); ok {
				return s, true
			}
		}
	}
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), true
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), true
	default:
		return "", false
	}
}

// asFloat returns the value as a float64, accepting a numeric string so that
// a schema that carries "25,000" as text still grounds as a number.
func asFloat(v any) (float64, bool) {
	if s, ok := v.(string); ok {
		return parseNumber(s)
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.String:
		return parseNumber(rv.String())
	default:
		return 0, false
	}
}

// asDates returns the readings of a value as a date.
//
// More than one only when the value is itself an ambiguous numeric string,
// which happens when a model returns "03/04/2026" as text. Both readings are
// carried, and a match against either grounds the value: the question here is
// whether the date appears in the document, and under either reading it does.
// Which reading is correct is validation's question, not this one's.
func asDates(v any, order DateOrder) ([]date, bool) {
	if t, ok := v.(time.Time); ok {
		return []date{dateOf(t)}, true
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
		if rv.CanInterface() {
			if t, ok := rv.Interface().(time.Time); ok {
				return []date{dateOf(t)}, true
			}
		}
	}
	if rv.Kind() == reflect.String {
		canon, _ := normalise.Canonical(rv.String())
		if hits := scanDates(canon, order); len(hits) == 1 {
			return hits[0].dates, true
		}
	}
	return nil, false
}
