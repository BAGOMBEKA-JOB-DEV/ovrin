package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// object is a JSON object that remembers the order its members were added in.
//
// The obvious carrier for a JSON Schema node is map[string]any, and it is the
// wrong one here for two separate reasons. encoding/json sorts map keys, so
// "properties" would reach the model alphabetised and the declaration order
// that [schema.Schema] goes out of its way to preserve would be discarded at
// the last possible moment. And sorting is only a coincidental kind of
// determinism: it is a property of encoding/json's implementation rather than
// of this package, so nothing would stop a future refactor — or a switch to a
// different encoder — from changing bytes that golden tests and provider prompt
// caches both depend on.
//
// A slice of key/value pairs makes the order explicit and local. Values are
// still encoded by encoding/json, so string escaping and number formatting stay
// standard rather than hand-rolled.
type object struct {
	members []member
}

// member is one key/value pair of an [object].
type member struct {
	key string
	val any
}

// set appends a member. Callers emit members in the canonical order documented
// on the package, so set never needs to replace or reorder; a duplicate key
// would be a bug in this package rather than in a caller's schema, and is
// caught by MarshalJSON.
func (o *object) set(key string, val any) {
	o.members = append(o.members, member{key: key, val: val})
}

// MarshalJSON writes the object in insertion order.
//
// It is written by hand rather than delegating to encoding/json because
// insertion order is the entire point of the type. The values inside it are
// still encoded by encoding/json, so string escaping and number formatting are
// the standard library's and not this package's.
func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	seen := make(map[string]bool, len(o.members))
	for i, m := range o.members {
		if seen[m.key] {
			return nil, fmt.Errorf("%w: duplicate member %q", ErrUnrepresentable, m.key)
		}
		seen[m.key] = true
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := encode(m.key)
		if err != nil {
			return nil, fmt.Errorf("%w: encoding member name %q: %w", ErrUnrepresentable, m.key, err)
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := encode(m.val)
		if err != nil {
			return nil, fmt.Errorf("%w: encoding member %q: %w", ErrUnrepresentable, m.key, err)
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// encode marshals one value with HTML escaping turned off.
//
// [json.Marshal] would escape < > and & inside a description as \u003c and
// friends. A model decodes the JSON before reading the string, so the escaping
// changes nothing it sees, but it triples the token cost of every "&" somebody
// writes in a description and makes a dumped schema harder to read while
// debugging. json.Encoder is the only way to turn it off, and it appends a
// newline that has to come back off.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
