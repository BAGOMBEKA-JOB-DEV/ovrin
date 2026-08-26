package schema

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// ErrSchema is the sentinel every error from this package wraps.
//
// The root package cannot be imported from here — it imports this package — so
// this is a second sentinel rather than ovrin.ErrSchema itself. The root wraps
// it, and a caller's errors.Is(err, ovrin.ErrSchema) is the test that matters;
// nothing anywhere reads the message text (docs/rules.md §2.2).
var ErrSchema = errors.New("invalid schema")

// timeType is compared by identity, so a named type whose underlying type is
// time.Time is not silently treated as a date.
var timeType = reflect.TypeOf(time.Time{})

// Reflect turns a Go type into a [Schema], reading the `ovrin` tag on each
// field as specified in docs/schema.md.
//
// t may be a struct or a pointer to one. Every error wraps [ErrSchema] and
// names the field and the problem, never a value: these errors are about the
// developer's own source, and they are raised before any provider is contacted
// so a typo costs nothing (ADR-0005).
//
// Keys are the paths from docs/schema.md — lowercase, dots for nesting,
// snake_case for multi-word Go names. A slice element's key carries an empty
// index, "items[]" and "items[].unit_price", because how many elements a
// document yields is not known until it has been read; [IndexKey] fills it in.
//
// Reflect does no caching. Use a [Cache] to pay for a type once.
func Reflect(t reflect.Type) (*Schema, error) {
	if t == nil {
		return nil, errf("schema type is nil")
	}
	// One pointer, the same limit fields have: *Invoice is a schema, **Invoice
	// is a mistake worth naming rather than quietly unwrapping.
	base := t
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return nil, errf("schema type %s is not a struct", t)
	}
	fields, err := structFields(base, typeName(base), "", []reflect.Type{base})
	if err != nil {
		return nil, err
	}
	return &Schema{Name: typeName(base), Fields: fields}, nil
}

// Cache memoises [Reflect] per [reflect.Type] and is safe for concurrent use by
// multiple goroutines.
//
// Reflection over a type always produces the same schema, so the cost belongs
// to the first extraction of a type and not to every one after it. Errors are
// cached too: a struct that cannot produce a schema cannot start producing one
// later, and re-deriving the same failure on every call would make a hot loop
// over a broken type expensive as well as wrong.
//
// The zero value is ready to use. A Cache is owned by one *Client rather than
// by the package, so two clients in a process share nothing (docs/rules.md
// §5.5), and it must not be copied after first use.
type Cache struct {
	mu      sync.Mutex
	entries map[reflect.Type]cacheEntry
}

// cacheEntry is one memoised result. Errors are stored alongside schemas so a
// failing type is diagnosed once.
type cacheEntry struct {
	schema *Schema
	err    error
}

// Of returns the schema for t, reflecting over it on first use.
//
// The returned *Schema is shared by every caller for that type and must be
// treated as read-only; mutating it would change what every later extraction of
// that type asks for.
func (c *Cache) Of(t reflect.Type) (*Schema, error) {
	// The lock is held across the reflection, not merely around the map, so
	// that a type is reflected exactly once and every caller gets the same
	// *Schema — pointer identity is then a usable answer to "is this the same
	// schema?". The cost is a mutex on a path taken once per extraction, where
	// the next step is a network call to a model provider; a sync.Map would
	// trade that for a race two callers can observe.
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[t]; ok {
		return e.schema, e.err
	}
	s, err := Reflect(t)
	if c.entries == nil {
		c.entries = make(map[reflect.Type]cacheEntry)
	}
	c.entries[t] = cacheEntry{schema: s, err: err}
	return s, err
}

// structFields reflects the tagged fields of a struct type in declaration
// order.
//
// goPath is the Go path of the struct for error messages ("Invoice.Vendor"),
// keyPrefix is what every field key below it starts with ("vendor."), and stack
// is the chain of struct types currently being walked, which is how a recursive
// type is caught.
func structFields(t reflect.Type, goPath, keyPrefix string, stack []reflect.Type) ([]Field, error) {
	var fields []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag, tagged := sf.Tag.Lookup(TagKey)
		if !tagged {
			// Untagged fields are not part of the schema, which is what lets an
			// application's existing struct be used as one (ADR-0005).
			continue
		}
		fieldPath := goPath + "." + sf.Name
		if sf.PkgPath != "" {
			// Reflection cannot write an unexported field, so extracting into
			// one is impossible. Skipping it silently would mean the tag looks
			// honoured and never is.
			return nil, errf("field %s is unexported and cannot be extracted", fieldPath)
		}
		if tag == TagSkip {
			continue
		}
		description, rules, err := parseTag(fieldPath, tag)
		if err != nil {
			return nil, err
		}
		if description == "" {
			description = derivedDescription(sf.Name)
		}
		f, err := buildField(sf.Type, keyPrefix+fieldKey(sf.Name), fieldPath, sf.Name, description, rules, stack, false)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		// A struct where every tag was forgotten would otherwise extract
		// nothing and report success (docs/schema.md, Errors).
		return nil, errf("type %s has no ovrin-tagged fields", t)
	}
	return fields, nil
}

// buildField classifies one Go type into a [Field].
//
// elem reports whether this is a slice's element rather than a struct field. An
// element carries no rules — the grammar has nowhere to write them — so the
// rule checks, including the one that requires a format on a time.Time, do not
// apply to it. Without that exemption []time.Time would be undeclarable.
func buildField(t reflect.Type, key, goPath, goName, description string, rules []Rule, stack []reflect.Type, elem bool) (Field, error) {
	f := Field{
		Key:         key,
		GoName:      goName,
		Description: description,
		Type:        t,
		Rules:       rules,
	}

	// Optional is about the pointer, not about the pointee: *float64 extracts
	// exactly as float64 does and differs only in how absence is represented.
	base := t
	if base.Kind() == reflect.Pointer {
		f.Optional = true
		base = base.Elem()
	}

	switch {
	case base == timeType:
		f.Kind = KindTime

	case base.Kind() == reflect.Slice:
		f.Kind = KindArray
		// The element key carries an empty index; see [IndexKey].
		e, err := buildField(base.Elem(), key+"[]", goPath+"[]", goName, "", nil, stack, true)
		if err != nil {
			return Field{}, err
		}
		f.Elem = &e

	case base.Kind() == reflect.Struct:
		f.Kind = KindObject
		if i := indexOfType(stack, base); i >= 0 {
			// There is no way to bound the schema sent to the model, and a
			// model given an unbounded schema produces unbounded output.
			return Field{}, errf("recursive type through field %s: %s", goPath, cycle(stack[i:], base))
		}
		// A full slice expression: sibling fields must not share a backing
		// array, or one branch's push would overwrite another's.
		nested, err := structFields(base, goPath, key+".", append(stack[:len(stack):len(stack)], base))
		if err != nil {
			return Field{}, err
		}
		f.Fields = nested

	default:
		switch base.Kind() {
		case reflect.String:
			f.Kind = KindString
		case reflect.Bool:
			f.Kind = KindBool
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			// Every width extracts identically; the width is enforced when the
			// value is converted back into the struct.
			f.Kind = KindInt
		case reflect.Float32, reflect.Float64:
			f.Kind = KindFloat
		default:
			// Maps, any, channels, functions, complex numbers, unsafe pointers
			// and fixed-size arrays all land here. docs/schema.md, Types.
			return Field{}, errf("field %s has unsupported type %s", goPath, t)
		}
	}

	if !elem {
		if err := checkRules(goPath, &f); err != nil {
			return Field{}, err
		}
	}
	return f, nil
}

// indexOfType returns the position of t in stack, or -1.
//
// A linear scan over a slice beats a map here: the stack is a nesting depth,
// which is single digits in every schema anyone writes.
func indexOfType(stack []reflect.Type, t reflect.Type) int {
	for i, s := range stack {
		if s == t {
			return i
		}
	}
	return -1
}

// cycle renders a chain of types as "ovrin.Invoice -> ovrin.Vendor ->
// ovrin.Invoice", so the error names the cycle and not merely the type it was
// noticed at. Names are package-qualified because a cycle can run through a
// type declared somewhere the reader is not looking.
func cycle(chain []reflect.Type, back reflect.Type) string {
	names := make([]string, 0, len(chain)+1)
	for _, t := range chain {
		names = append(names, t.String())
	}
	return strings.Join(append(names, back.String()), " -> ")
}

// typeName is the schema name of a type: the declared name where there is one,
// and the full description for an unnamed type. Error messages use the type's
// own String instead, because "time.Time" locates a type and "Time" does not.
func typeName(t reflect.Type) string {
	if n := t.Name(); n != "" {
		return n
	}
	return t.String()
}

// schemaError is a schema problem. Its message is the detail alone, so the root
// package can wrap it as ovrin.ErrSchema without "invalid schema" appearing
// twice in one sentence.
type schemaError struct {
	msg string
}

// Error returns the detail: the field and the problem, never a value.
func (e *schemaError) Error() string { return e.msg }

// Unwrap returns [ErrSchema], which is what errors.Is matches against.
func (e *schemaError) Unwrap() error { return ErrSchema }

// errf builds a schema error. Lowercase and unpunctuated (docs/rules.md §2.3),
// and it takes only type and field names — there is no document value in scope
// here and none may ever be added.
func errf(format string, args ...any) error {
	return &schemaError{msg: fmt.Sprintf(format, args...)}
}

// inapplicable reports a rule used on a type it has no meaning for, naming all
// three things the developer needs: the rule, the field and the type.
func inapplicable(rule, goPath string, t reflect.Type) error {
	return errf("rule %s cannot apply to field %s of type %s", rule, goPath, t)
}
