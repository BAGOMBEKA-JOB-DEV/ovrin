// Package schema turns a Go struct into the description of what to extract.
//
// The struct is the schema: its fields, their types, and the `ovrin:"…"` tags
// on them determine what a model is asked for, what is validated afterwards,
// and the keys of the result. Reflection happens once per type and is cached,
// so a malformed schema is an error raised before any provider is contacted.
//
// The types here are the contract between this package and its consumers —
// jsonschema, validate and prompt all read a Schema and none of them reflects
// over a Go type themselves.
package schema

import "reflect"

// Schema describes one extractable type.
type Schema struct {
	// Name is the Go type name, used in errors and as the JSON Schema title.
	Name string

	// Fields are in declaration order, which is the order a model sees them.
	// Declaration order is often meaningful — an invoice's number before its
	// total — and reordering would discard that for no gain.
	Fields []Field
}

// Field is one extractable field.
type Field struct {
	// Key is the path used in Result.Fields: "total", "vendor.name",
	// "items[0].unit_price". Lowercase, dots for nesting, snake_case for
	// multi-word Go names.
	//
	// In a reflected Schema a slice element's key carries an empty index —
	// "items[]", "items[].unit_price" — because the number of elements is a
	// property of the document and not of the type. See [IndexKey].
	Key string

	// GoName is the Go field name, for reflection and for error messages that
	// need to point a developer at their own source.
	GoName string

	// Description is the prose handed to the model. It is the
	// highest-leverage part of a schema and the only untyped input to a typed
	// system: changing it can change results with no compiler signal.
	Description string

	// Kind is the extraction kind, which is coarser than the Go type.
	Kind Kind

	// Type is the Go type, retained so validate can convert into it.
	Type reflect.Type

	// Optional reports whether the Go field is a pointer. Distinct from the
	// absence of a `required` rule: Optional is about how absence is
	// represented, `required` is about whether absence is acceptable.
	Optional bool

	// Rules are the validation rules from the tag, in tag order.
	Rules []Rule

	// Elem describes the element type of a slice, and is nil otherwise.
	Elem *Field

	// Fields describes a nested struct's fields, and is nil otherwise.
	Fields []Field
}

// Kind is the extraction kind of a field.
//
// Coarser than reflect.Kind on purpose: every signed integer width extracts
// identically, and the distinction that matters to a model is number versus
// string versus date.
type Kind string

const (
	// KindUnknown is the zero value and never appears in a valid Schema.
	KindUnknown Kind = ""

	KindString Kind = "string"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindBool   Kind = "bool"
	KindTime   Kind = "time"
	KindObject Kind = "object"
	KindArray  Kind = "array"
)

// String returns the kind name, or "unknown" for the zero value.
func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

// Rule is one validation rule parsed from a tag.
//
// The vocabulary is closed: an element that is not a known rule name is an
// error naming it, never a silent skip. That is what makes a typo like
// "requird" loud rather than a rule that quietly does not apply.
type Rule struct {
	// Name is the rule: "required", "min", "max", "format", "enum".
	Name string

	// Value is the text after "=", empty for valueless rules like "required".
	Value string
}

// The closed rule vocabulary. See docs/adr/0006-tag-grammar.md.
const (
	RuleRequired = "required"
	RuleMin      = "min"
	RuleMax      = "max"
	RuleFormat   = "format"
	RuleEnum     = "enum"
)

// TagKey is the struct tag ovrin reads.
const TagKey = "ovrin"
