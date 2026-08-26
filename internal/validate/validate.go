package validate

import (
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// RuleResult is one validation rule and its outcome.
//
// It duplicates ovrin.RuleResult field for field, and deliberately: the root
// package imports this one (docs/architecture.md), so importing the root back
// would be an import cycle. The two types have identical underlying types, so
// the root converts with ovrin.RuleResult(r) and needs no mapping code. If the
// root's definition ever changes, the compiler catches it at that conversion.
//
// Message is a string rather than an error because a failed rule is not an
// error (docs/adr/0004-partial-results.md), and because an explanation carrying
// one has to marshal to JSON.
type RuleResult struct {
	// Rule is the rule as written in the tag: "required", "min=0",
	// "format=date". It comes from the developer's struct tag, never from the
	// document.
	Rule string

	// Passed reports the outcome.
	Passed bool

	// Message says why not, and is empty when Passed. It never contains the
	// value (docs/rules.md §2.5).
	Message string
}

// DateOrder resolves ambiguous numeric dates such as 03/04/2026.
//
// The zero value does not guess. Its values match ovrin.DateOrder so the root
// package can convert between the two, for the reason given on [RuleResult].
type DateOrder string

// The date orders. An unknown order flags an ambiguous date rather than
// resolving it, because silently reading 3 April as 4 March is exactly the kind
// of confidently wrong answer this library exists to catch.
const (
	// DateOrderUnknown flags ambiguous dates rather than resolving them.
	DateOrderUnknown DateOrder = ""

	// DayFirst reads 03/04/2026 as 3 April.
	DayFirst DateOrder = "dmy"

	// MonthFirst reads 03/04/2026 as 4 March.
	MonthFirst DateOrder = "mdy"

	// YearFirst reads 2026/03/04 as 4 March.
	YearFirst DateOrder = "ymd"
)

// DateAmbiguity holds both readings of a numeric date that could not be
// resolved.
//
// Both are offered because a reviewer deciding between them should not have to
// re-parse the raw text, and because showing the two candidates is the whole
// point of refusing to pick one.
type DateAmbiguity struct {
	// DayFirst is the value read as day/month/year.
	DayFirst time.Time

	// MonthFirst is the value read as month/day/year.
	MonthFirst time.Time
}

// Result is everything validation determined about one field.
//
// It carries no error. Every way a value can be wrong is reported here as data,
// so a caller assembling a Result[T] never has to decide whether a bad field
// should fail the extraction: it never should (docs/rules.md §2.6).
type Result struct {
	// Kind is the field's extraction kind, carried through so a caller reading
	// a Result does not have to hold the Field beside it.
	Kind schema.Kind

	// Found reports whether a value was present at all. A field the model did
	// not return, returned as null, or returned as an empty string is not
	// found, and is never filled with a zero value (docs/rules.md §8.5).
	Found bool

	// Value is the converted value, of the field's Go type with any pointer
	// indirection removed. It is meaningful only when Converted, and only for
	// scalar kinds: a struct or slice field is assembled by the caller, which
	// is the only place the nested field results exist.
	Value any

	// Converted reports whether Value holds a usable value. False means the
	// value could not be turned into the field's type — never that it is zero.
	Converted bool

	// Raw is the value as extracted, for a reviewer to see what was actually
	// there (docs/schema.md §format). It is document content: put it on the
	// field, never in a log line or an error.
	Raw string

	// Message says why the value could not be converted, and is empty when
	// Converted or when the failure is already reported by a rule in Rules. It
	// never contains the value.
	Message string

	// Rules is one entry per rule declared on the field, in tag order.
	Rules []RuleResult

	// Ambiguity is set when a date parsed but could not be resolved to one
	// reading. The field is not converted: choosing between the two would be a
	// guess. The caller lowers the format signal and adds a review reason.
	Ambiguity *DateAmbiguity
}

// Valid reports whether every rule on the field passed.
//
// It is derived rather than stored so it cannot drift from Rules.
func (r Result) Valid() bool {
	for _, rr := range r.Rules {
		if !rr.Passed {
			return false
		}
	}
	return true
}

// Validator applies a schema's rules to extracted values.
//
// Build one and share it: it is immutable after [New] and therefore safe for
// concurrent use by multiple goroutines (docs/rules.md §5.1).
type Validator struct {
	dateOrder  DateOrder
	crossField []CrossFieldRule
}

// Option configures a [Validator].
type Option func(*Validator)

// New returns a Validator configured by opts.
//
// The zero configuration flags ambiguous dates rather than resolving them and
// declares no cross-field rules, which is the safe default: a validator that
// guesses is worse than one that asks.
func New(opts ...Option) *Validator {
	v := &Validator{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(v)
	}
	return v
}

// WithDateOrder resolves ambiguous numeric dates for a corpus whose convention
// the caller knows. Without it they are flagged rather than guessed.
func WithDateOrder(d DateOrder) Option {
	return func(v *Validator) { v.dateOrder = d }
}

// WithCrossFieldRules adds rules that check consistency between sibling fields.
//
// They are additive rather than replacing, so a caller can combine the rules
// shipped here with their own without knowing what the others are.
func WithCrossFieldRules(rules ...CrossFieldRule) Option {
	return func(v *Validator) {
		for _, r := range rules {
			if r != nil {
				v.crossField = append(v.crossField, r)
			}
		}
	}
}

// Field converts one extracted value into f's Go type and evaluates f's rules
// against it.
//
// raw is the value as the model returned it: a string, a number, a bool, a
// slice or map for composite kinds, or nil for a field that was not returned.
// Every outcome is reported on the [Result]; nothing here is an error.
//
// Rules other than "required" are evaluated only when a value was found.
// Reporting "format=date failed" for a field the document simply does not
// contain would be noise, and absence is exactly what "required" is for.
func (v *Validator) Field(f schema.Field, raw any) Result {
	res := Result{Kind: f.Kind, Raw: rawText(raw)}
	res.Found = present(f.Kind, raw)

	format, hasFormat := formatRule(f.Rules)

	var formatOK bool
	var formatMsg string
	if res.Found {
		c := v.convert(f, raw, format)
		res.Value, res.Converted, res.Ambiguity = c.value, c.ok, c.ambiguity
		formatOK, formatMsg = c.ok, c.message
		if !c.ok && !hasFormat {
			res.Message = c.message
		}
	}

	for _, rule := range f.Rules {
		res.Rules = append(res.Rules, v.rule(f, rule, raw, res, formatOK, formatMsg))
	}
	return res
}

// rule evaluates one declared rule against an already-converted value.
func (v *Validator) rule(f schema.Field, rule schema.Rule, raw any, res Result, formatOK bool, formatMsg string) RuleResult {
	out := RuleResult{Rule: ruleText(rule), Passed: true}
	switch rule.Name {
	case schema.RuleRequired:
		if !res.Found {
			out.Passed, out.Message = false, "no value was found for this required field"
		}
		return out

	case schema.RuleFormat:
		if !res.Found {
			return out
		}
		if !formatOK {
			out.Passed, out.Message = false, formatMsg
		}
		return out

	case schema.RuleMin, schema.RuleMax:
		if !res.Found {
			return out
		}
		return v.bound(f, rule, raw, res, out)

	case schema.RuleEnum:
		if !res.Found {
			return out
		}
		return enumRule(rule, res, out)
	}

	// The schema package rejects an unknown rule with ErrSchema before any of
	// this runs (docs/schema.md §Rules). One reaching here is a bug in that
	// package, and a silent pass would hide it.
	out.Passed, out.Message = false, "unknown rule"
	return out
}

// bound evaluates min or max: a numeric bound for numbers, a length bound for
// strings and slices (docs/schema.md §Rules).
func (v *Validator) bound(f schema.Field, rule schema.Rule, raw any, res Result, out RuleResult) RuleResult {
	isMin := rule.Name == schema.RuleMin

	switch f.Kind {
	case schema.KindInt, schema.KindFloat:
		limit, err := strconv.ParseFloat(strings.TrimSpace(rule.Value), 64)
		if err != nil {
			out.Passed, out.Message = false, "the rule's bound is not a number"
			return out
		}
		if !res.Converted {
			out.Passed, out.Message = false, "not evaluated: the value is not a number"
			return out
		}
		n, ok := numeric(res.Value)
		if !ok {
			out.Passed, out.Message = false, "not evaluated: the value is not a number"
			return out
		}
		if isMin && n < limit {
			out.Passed, out.Message = false, "below the minimum"
		} else if !isMin && n > limit {
			out.Passed, out.Message = false, "above the maximum"
		}
		return out

	case schema.KindString, schema.KindArray:
		limit, err := strconv.Atoi(strings.TrimSpace(rule.Value))
		if err != nil {
			out.Passed, out.Message = false, "the rule's bound is not a whole number"
			return out
		}
		n, ok := length(f.Kind, raw, res)
		if !ok {
			out.Passed, out.Message = false, "not evaluated: the value has no length"
			return out
		}
		if isMin && n < limit {
			out.Passed, out.Message = false, "shorter than the minimum length"
		} else if !isMin && n > limit {
			out.Passed, out.Message = false, "longer than the maximum length"
		}
		return out
	}

	// Inapplicable rules are ErrSchema before extraction starts. Reaching here
	// means the schema package let one through.
	out.Passed, out.Message = false, "rule does not apply to a field of this type"
	return out
}

// enumRule reports whether the value is one of the declared alternatives.
//
// The comparison is exact, against the normalised value, so a field carrying
// format=currency is compared after uppercasing. Case-insensitive matching is
// deliberately not done: a failed enum costs a review, and quietly accepting a
// value the schema did not list costs a wrong answer nobody sees.
func enumRule(rule schema.Rule, res Result, out RuleResult) RuleResult {
	if !res.Converted {
		out.Passed, out.Message = false, "not evaluated: the value is not text"
		return out
	}
	s, ok := res.Value.(string)
	if !ok {
		v := reflect.ValueOf(res.Value)
		if !v.IsValid() || v.Kind() != reflect.String {
			out.Passed, out.Message = false, "not evaluated: the value is not text"
			return out
		}
		s = v.String()
	}
	for _, alt := range strings.Split(rule.Value, "|") {
		if alt == s {
			return out
		}
	}
	out.Passed, out.Message = false, "not one of the permitted values"
	return out
}

// ruleText renders a rule the way it was written in the tag, which is what
// ovrin.RuleResult.Rule promises and what a developer will grep for.
func ruleText(r schema.Rule) string {
	if r.Value == "" {
		return r.Name
	}
	return r.Name + "=" + r.Value
}

// formatRule returns the declared format, if any.
func formatRule(rules []schema.Rule) (string, bool) {
	for _, r := range rules {
		if r.Name == schema.RuleFormat {
			return strings.ToLower(strings.TrimSpace(r.Value)), true
		}
	}
	return "", false
}

// present reports whether raw holds a value at all.
//
// A whitespace-only string is absence, not an empty value: a model with nothing
// to report returns "" far more often than a document genuinely contains an
// empty string, and treating it as a value would defeat Found (§8.5). A composite
// is present when it is non-nil, so an explicitly empty list is a finding — the
// document said there were none — and its length is min's business.
func present(kind schema.Kind, raw any) bool {
	if raw == nil {
		return false
	}
	switch kind {
	case schema.KindArray, schema.KindObject:
		v := reflect.ValueOf(raw)
		switch v.Kind() {
		case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface:
			return !v.IsNil()
		}
		return true
	}
	if s, ok := raw.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	v := reflect.ValueOf(raw)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return !v.IsNil()
	}
	return true
}

// length returns the length min and max compare against: runes for a string,
// elements for a slice.
//
// Runes rather than bytes because a document is not ASCII, and a name rejected
// for being "too long" because it is written in Amharic would be a defect.
func length(kind schema.Kind, raw any, res Result) (int, bool) {
	if kind == schema.KindString {
		if !res.Converted {
			// The value did not convert, but its text is still text and its
			// length is still meaningful, so the bound is worth checking.
			return utf8.RuneCountInString(res.Raw), true
		}
		v := reflect.ValueOf(res.Value)
		if v.IsValid() && v.Kind() == reflect.String {
			return utf8.RuneCountInString(v.String()), true
		}
		return utf8.RuneCountInString(res.Raw), true
	}
	v := reflect.ValueOf(raw)
	if !v.IsValid() {
		return 0, false
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return v.Len(), true
	}
	return 0, false
}
