package jsonschema

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// ErrUnrepresentable reports a [schema.Schema] that cannot be written in the
// portable dialect this package emits.
//
// It exists so the pipeline can classify the failure rather than read its text
// (rule §2.2) and wrap it as ovrin.ErrSchema: every condition behind it is a
// defect in the Go struct or in the Schema built from it, not a provider
// problem, and it is raised at the schema stage before any provider is
// contacted. The wrapped message names the field and the rule; it never carries
// a value read from a document, because there is none — everything here comes
// from struct tags (rule §2.5).
var ErrUnrepresentable = errors.New("jsonschema: schema cannot be represented")

// maxDepth bounds nesting, mirroring ovrin.DefaultMaxDepth.
//
// internal/schema rejects a recursive type before a Schema is ever built, so
// this should be unreachable. It is here because "should be unreachable" is not
// a bound (rule §5.2, ADR-0020): this package is the last thing between a
// malformed Schema and a stack overflow, and an error naming the field is a
// better failure than a crash.
const maxDepth = 64

// Marshal renders s as JSON Schema bytes for a model's structured-output mode.
//
// The output is compact — no indentation, no trailing newline — because it is
// sent to a provider rather than read by a person, and every byte is a token
// somebody pays for. It is byte-identical for equal input; see the package
// comment for the dialect and for why every property is nullable.
//
// Marshal is safe for concurrent use by multiple goroutines.
func Marshal(s schema.Schema) ([]byte, error) {
	root := &object{}
	root.set("type", "object")
	if s.Name != "" {
		// The title is the Go type name. It costs a handful of tokens and
		// gives the model the noun the fields belong to, which is context no
		// individual field description repeats.
		root.set("title", s.Name)
	}
	if err := setObjectMembers(root, s.Fields, 1); err != nil {
		return nil, err
	}
	return root.MarshalJSON()
}

// setObjectMembers appends the properties, required and additionalProperties
// members that make one object node, in that order.
//
// Every property is listed in required and additionalProperties is always
// false, with no condition attached to either: those two are what OpenAI's
// strict mode checks, and making them unconditional means there is no path
// through this package that can emit an object it would reject.
func setObjectMembers(o *object, fields []schema.Field, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: nesting deeper than %d levels", ErrUnrepresentable, maxDepth)
	}

	props := &object{}
	// Non-nil so an object with no properties emits [] rather than null.
	required := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))

	for _, f := range fields {
		name, err := propertyName(f)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("%w: field %q: duplicate property name %q", ErrUnrepresentable, f.GoName, name)
		}
		seen[name] = true

		// Nullable regardless of f.Optional and of the required rule. See the
		// package comment: this is what lets required list every property
		// without asking a model to invent a value it could not find.
		node, err := fieldNode(f, true, depth)
		if err != nil {
			return err
		}
		props.set(name, node)
		required = append(required, name)
	}

	o.set("properties", props)
	o.set("required", required)
	o.set("additionalProperties", false)
	return nil
}

// fieldNode builds the schema node for one field.
//
// nullable is a parameter rather than being read off the field because the two
// callers want different answers: a property is always nullable, while an array
// element is nullable only for a []*T, where a nil element is a thing the Go
// type can actually hold.
func fieldNode(f schema.Field, nullable bool, depth int) (*object, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nesting deeper than %d levels", ErrUnrepresentable, maxDepth)
	}

	base, err := jsonType(f)
	if err != nil {
		return nil, err
	}
	rules := scanRules(f)

	o := &object{}
	if nullable {
		o.set("type", []string{base, "null"})
	} else {
		o.set("type", base)
	}
	if f.Description != "" {
		// The description is the highest-leverage part of a schema and the only
		// part a model reads as prose (docs/schema.md). It is carried verbatim:
		// rewriting a developer's wording here would change extraction results
		// with no compiler signal and no diff to review.
		o.set("description", f.Description)
	}

	if f.Kind == schema.KindTime {
		format, err := timeFormat(f, rules)
		if err != nil {
			return nil, err
		}
		o.set("format", format)
	}

	if err := setEnum(o, f, rules, nullable); err != nil {
		return nil, err
	}
	if err := setBounds(o, f, rules); err != nil {
		return nil, err
	}

	switch f.Kind {
	case schema.KindArray:
		if f.Elem == nil {
			return nil, fmt.Errorf("%w: field %q: array with no element type", ErrUnrepresentable, f.GoName)
		}
		items, err := fieldNode(*f.Elem, f.Elem.Optional, depth+1)
		if err != nil {
			return nil, err
		}
		o.set("items", items)
	case schema.KindObject:
		if err := setObjectMembers(o, f.Fields, depth+1); err != nil {
			return nil, err
		}
	}

	return o, nil
}

// jsonType maps an extraction kind onto a JSON Schema type name.
//
// The mapping is narrower than it looks: KindTime is a string, and the
// distinction between a date and a timestamp is carried by "format" rather than
// by the type, because JSON has no date.
func jsonType(f schema.Field) (string, error) {
	switch f.Kind {
	case schema.KindString, schema.KindTime:
		return "string", nil
	case schema.KindInt:
		return "integer", nil
	case schema.KindFloat:
		return "number", nil
	case schema.KindBool:
		return "boolean", nil
	case schema.KindObject:
		return "object", nil
	case schema.KindArray:
		return "array", nil
	case schema.KindUnknown:
		return "", fmt.Errorf("%w: field %q: kind is unset", ErrUnrepresentable, f.GoName)
	default:
		return "", fmt.Errorf("%w: field %q: unknown kind %q", ErrUnrepresentable, f.GoName, f.Kind)
	}
}

// ruleValues is the subset of a field's rules that JSON Schema can express.
//
// Rules are scanned into this before anything is emitted so that members come
// out in the canonical order documented on the package rather than in tag
// order: a developer writing min before max and a developer writing max before
// min must produce the same bytes, or prompt caching stops working for reasons
// invisible in review.
type ruleValues struct {
	format    string
	hasFormat bool
	enum      string
	hasEnum   bool
	min       string
	hasMin    bool
	max       string
	hasMax    bool
}

// scanRules collects the expressible rules, last occurrence winning.
//
// Unknown rule names are ignored rather than rejected. The vocabulary is closed
// and internal/schema is where a misspelled rule name becomes an ErrSchema
// naming the field; repeating that check here would give the same mistake two
// different error messages depending on which package noticed first. It also
// means a rule that happens to share a name with a JSON Schema keyword — a
// "pattern" ovrin does not have — is not smuggled into the output by accident.
func scanRules(f schema.Field) ruleValues {
	var rv ruleValues
	for _, r := range f.Rules {
		switch r.Name {
		case schema.RuleFormat:
			rv.format, rv.hasFormat = r.Value, true
		case schema.RuleEnum:
			rv.enum, rv.hasEnum = r.Value, true
		case schema.RuleMin:
			rv.min, rv.hasMin = r.Value, true
		case schema.RuleMax:
			rv.max, rv.hasMax = r.Value, true
		}
	}
	return rv
}

// timeFormat maps the format rule of a time field onto a JSON Schema format.
//
// A time.Time with no format rule is rejected by internal/schema, so the
// default here is only reached by a hand-built Schema. It defaults to date-time
// rather than erroring because date-time is the wider of the two: a value that
// is only a date still parses, whereas a timestamp given "format": "date" does
// not.
func timeFormat(f schema.Field, rv ruleValues) (string, error) {
	if !rv.hasFormat {
		return "date-time", nil
	}
	switch rv.format {
	case "date":
		return "date", nil
	case "datetime", "date-time":
		return "date-time", nil
	default:
		return "", fmt.Errorf("%w: field %q: format %q is not a date format", ErrUnrepresentable, f.GoName, rv.format)
	}
}

// setEnum emits the enum member for a string field.
//
// null is appended for a nullable field because the two constraints are read
// together: {"type": ["string","null"]} with an enum that omits null describes a
// value that cannot exist, and a strict provider is entitled to reject the
// whole request for it.
//
// enum on a non-string kind is not emitted. docs/schema.md scopes the rule to
// strings, and internal/validate enforces it against the extracted value either
// way.
func setEnum(o *object, f schema.Field, rv ruleValues, nullable bool) error {
	if !rv.hasEnum || f.Kind != schema.KindString {
		return nil
	}
	alternatives := strings.Split(rv.enum, "|")
	values := make([]any, 0, len(alternatives)+1)
	for _, alt := range alternatives {
		if alt == "" {
			return fmt.Errorf("%w: field %q: enum has an empty alternative", ErrUnrepresentable, f.GoName)
		}
		values = append(values, alt)
	}
	if nullable {
		values = append(values, nil)
	}
	o.set("enum", values)
	return nil
}

// setBounds emits the min and max rules under whichever JSON Schema keywords
// suit the field's kind.
//
// A kind with no equivalent — a bool, an object, a time — emits nothing and
// does not parse the value, because the value may not be a number at all and
// internal/validate is the thing that knows what a bound means for that kind.
func setBounds(o *object, f schema.Field, rv ruleValues) error {
	switch f.Kind {
	case schema.KindInt, schema.KindFloat:
		return setNumericBounds(o, f, rv)
	case schema.KindString:
		return setCountBounds(o, f, rv, "minLength", "maxLength")
	case schema.KindArray:
		return setCountBounds(o, f, rv, "minItems", "maxItems")
	default:
		return nil
	}
}

// setNumericBounds emits minimum and maximum.
//
// Both are parsed as float64 even for an integer field: JSON Schema does not
// require an integer bound on an integer type, and refusing "min=0.5" here
// would reject a schema that every provider accepts.
func setNumericBounds(o *object, f schema.Field, rv ruleValues) error {
	if rv.hasMin {
		n, err := numberValue(f, schema.RuleMin, rv.min)
		if err != nil {
			return err
		}
		o.set("minimum", n)
	}
	if rv.hasMax {
		n, err := numberValue(f, schema.RuleMax, rv.max)
		if err != nil {
			return err
		}
		o.set("maximum", n)
	}
	return nil
}

// setCountBounds emits a pair of length or item-count keywords.
func setCountBounds(o *object, f schema.Field, rv ruleValues, minKey, maxKey string) error {
	if rv.hasMin {
		n, err := countValue(f, schema.RuleMin, rv.min)
		if err != nil {
			return err
		}
		o.set(minKey, n)
	}
	if rv.hasMax {
		n, err := countValue(f, schema.RuleMax, rv.max)
		if err != nil {
			return err
		}
		o.set(maxKey, n)
	}
	return nil
}

// numberValue parses a numeric bound.
//
// NaN and the infinities are rejected explicitly rather than left to
// encoding/json, so the error names the field and the rule instead of arriving
// later as an opaque UnsupportedValueError from the middle of a marshal.
func numberValue(f schema.Field, rule, value string) (float64, error) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: field %q: rule %q: %q is not a number", ErrUnrepresentable, f.GoName, rule, value)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%w: field %q: rule %q: %q has no JSON representation", ErrUnrepresentable, f.GoName, rule, value)
	}
	return n, nil
}

// countValue parses a length or item-count bound, which JSON Schema requires to
// be a non-negative integer.
func countValue(f schema.Field, rule, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: field %q: rule %q: %q is not a whole number", ErrUnrepresentable, f.GoName, rule, value)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: field %q: rule %q: %q is negative", ErrUnrepresentable, f.GoName, rule, value)
	}
	return n, nil
}

// propertyName is the JSON object key for a field.
//
// [schema.Field.Key] is documented as the path used in Result.Fields —
// "vendor.name", "items[0].unit_price" — which is a flat key over a nested
// object. JSON Schema needs the leaf segment, since the nesting is already
// carried by the structure. Taking the last segment is correct whether a nested
// field's Key holds the full path or only its own name, and keys are lowercase
// snake_case, so a dot in one can only ever be a separator.
func propertyName(f schema.Field) (string, error) {
	name := f.Key
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", fmt.Errorf("%w: field %q: empty key", ErrUnrepresentable, f.GoName)
	}
	return name, nil
}
