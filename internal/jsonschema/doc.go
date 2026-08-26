// Package jsonschema turns an internal [schema.Schema] into the JSON Schema
// bytes that cross the Model seam.
//
// ADR-0007 puts the schema on ModelRequest.Schema as []byte rather than as a Go
// type, so an adapter can hand it straight to a provider's structured-output
// mode without ovrin knowing the shape of that mode. This package produces
// those bytes and is the only place that knows which dialect they are written
// in.
//
// # The dialect is the narrowest one every provider accepts
//
// Providers disagree about JSON Schema, and that disagreement is the sharp edge
// of structured output. OpenAI's strict mode requires "additionalProperties":
// false on every object and requires every property to be named in "required",
// including the optional ones. Gemini accepts an OpenAPI 3.0 subset with no
// $ref at all. A schema that is merely valid therefore buys nothing: it comes
// back as a provider rejection (ovrin.ErrBadRequest) at the generate stage,
// after the document has been read and the reading paid for. So the output here
// is deliberately the intersection rather than anything expressive:
//
//   - Fully expanded. No "$ref", no "$defs", no "$schema", no "oneOf". A type
//     used in two places is written out twice. The duplication costs tokens and
//     buys portability, which is the better trade when the alternative is a
//     schema one provider cannot follow.
//   - "additionalProperties": false on every object, so a model cannot answer
//     with a key nobody asked for (docs/threat-model.md).
//   - Every property named in "required".
//   - Absence expressed as a nullable type union — {"type": ["string","null"]}
//     — and never by leaving a property out of "required". That is what lets a
//     property be simultaneously always-present in the reply and genuinely
//     absent in the document, which is the combination OpenAI's strict mode and
//     rule §8.5 both demand.
//   - Byte-identical output for the same Schema, every time. Tests assert exact
//     bytes, and prompt caching at every provider keys on them; an emitter that
//     shuffled its keys would silently halve a cache hit rate and nobody would
//     see it in a diff.
//
// # Every property is nullable, and that is a deliberate reading
//
// A property is emitted as a "null" union whether or not its field carries the
// `required` rule, and whether or not the Go field is a pointer. Three reasons,
// in the order they mattered:
//
// Rule §8.5 says never fabricate a value to satisfy a schema. A non-nullable
// "total" in a strict-mode reply leaves a model that cannot find the total no
// way to say so, and the cheapest thing it can do is invent one.
//
// docs/schema.md defines a missing `required` field as a validation outcome —
// Valid false, NeedsReview, a reason — and explicitly not as an error and not
// as a stopped extraction. That outcome is only reachable if the wire format
// can carry the absence in the first place.
//
// `required` is a rule, and rules are evaluated by internal/validate against
// the extracted value. Mirroring one into the schema would move its enforcement
// to a place where failure surfaces as a provider error rather than as a review
// reason, which is the opposite of ADR-0004.
//
// [schema.Field.Optional] therefore does not change the emitted schema for a
// property; it changes how internal/validate writes the value back into the Go
// struct. It does change array elements, where a []*T is emitted with nullable
// items and a []T is not, because a missing element of a slice is simply an
// element that is not there.
//
// # Which rules are emitted, and which internal/validate enforces alone
//
// A rule is emitted only where JSON Schema has an equivalent that every target
// provider accepts. The rest are not omissions and not silent drops in the
// sense of rule §6.1 — they are enforced by internal/validate against the
// extracted value, which is a strictly stronger check than a constraint a model
// is merely asked to respect:
//
//	rule                     emitted as                       enforced by
//	min / max on int, float  "minimum" / "maximum"            both
//	min / max on string      "minLength" / "maxLength"        both
//	min / max on array       "minItems" / "maxItems"          both
//	enum on string           "enum" (plus null, see above)    both
//	format=date on time      "format": "date"                 both
//	format=datetime on time  "format": "date-time"            both
//	required                 nothing                          internal/validate
//	format=email, phone,     nothing                          internal/validate
//	currency, iban, swift,
//	uuid
//	min / max elsewhere      nothing                          internal/validate
//	enum on a non-string     nothing                          internal/validate
//
// The non-date formats are the interesting omission. "email" and "uuid" are
// standard JSON Schema format values and would be legal to emit, but Gemini's
// OpenAPI subset documents support for a much shorter list of string formats,
// and a format value a provider rejects fails the whole request rather than
// relaxing one field. They are worth revisiting per-provider once the model
// adapters exist; until then internal/validate is the only enforcement, and it
// is the enforcement that decides Result.Valid anyway.
//
// # Ordering
//
// Members are emitted in a fixed canonical order — type, title, description,
// format, enum, bounds, items, properties, required, additionalProperties — and
// properties in schema declaration order, which is the order the developer
// wrote the struct in and often carries meaning that alphabetising would throw
// away (see [schema.Schema]).
//
// Nothing in this package is stateful and Marshal is safe for concurrent use.
package jsonschema
