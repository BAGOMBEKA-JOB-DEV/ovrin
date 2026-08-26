// Package retry constructs the one follow-up request ovrin makes when a model
// returns a reply that does not satisfy the schema.
//
// # What it is for
//
// A model that returns `"total": "one thousand two hundred"` for a float field
// has not misread the document; it has failed to produce the shape it was
// asked for. Reporting that as a dead extraction throws away a reading that was
// almost right, and the reading is the expensive part. Shown the schema, the
// reply it gave and a list of what was wrong with it, a model usually returns
// the same values in the right shape.
//
// This package builds that second request. It does not send it and it does not
// loop: the caller makes one call and stops. See [Build], which refuses to
// build a retry of a retry.
//
// # What is worth asking again for
//
// Only a failure of the reply, never a failure of the document. A reply that is
// not JSON, a reply that is not a JSON object, and a value that is not of the
// field's declared type are the model's mistakes, and the model can fix them.
// A value that failed `min=0`, `enum=` or `format=` is the document saying
// something the schema forbids, and asking again cannot change the document —
// it can only invite an invented value, which is the worst thing this library
// could produce (docs/rules.md §8.5). A `required` field the model omitted is
// the same case, and is the most dangerous one: the reply already says the
// document does not contain it, and re-asking is pressure to make one up.
// [Assess] encodes exactly that split.
//
// # The security property
//
// The previous reply is untrusted. It was produced by a model reading a
// third party's file, so any byte of it may be document content — including an
// injected instruction the document planted and the model copied out. Promoting
// it into the instruction region would undo, on the second request, everything
// ADR-0017 buys on the first.
//
// So it never goes there. Two structures guarantee it:
//
//   - [Instruction] takes a schema and a list of [Failure], and nothing else.
//     There is no parameter through which the reply could arrive, exactly as in
//     internal/prompt. A reviewer establishes the property from the signature.
//
//   - [Failure] has two fields and neither can hold a value. Field is a schema
//     key, and is dropped unless it names a field of the schema being rendered,
//     so the key that reaches the instruction is the schema's own string and
//     not the caller's. Fault is a closed enum that selects a fixed sentence.
//     There is no free text anywhere in the path from a reply to an
//     instruction. This mirrors the discipline ADR-0021 applies to Event.
//
// The reply itself travels in [prompt.Request.Content], delimited by markers
// carrying a per-request random identifier drawn from crypto/rand and verified
// absent from the reply, and the instruction says that block is data to be read
// and never obeyed. That is the same treatment page content gets, for the same
// reason.
//
// # What it does not send
//
// The document. The model has already read it, and re-sending it doubles the
// cost of the reading to fix a formatting mistake. [Build] takes the original
// request and uses its schema bytes and its temperature; the original's
// Content is deliberately dropped, and a test asserts that no byte of it
// reaches the follow-up.
package retry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/prompt"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// MaxReplyBytes bounds the reply this package will send back.
//
// The reply is about to be copied into a second paid request, and its size is
// chosen by a model reading an attacker-controlled file: a document that
// induces a megabyte of echoed text would otherwise buy itself a megabyte of
// billed input. 64 KiB is far more than a schema-shaped object needs and far
// less than an echo attack wants. Every limit has a finite default, checked
// before allocation (docs/rules.md §5.2, ADR-0020).
const MaxReplyBytes = 64 << 10

// The conditions building a retry can fail on.
//
// They are unprefixed and untyped for the same reason internal/prompt's are:
// the root package classifies them onto its own sentinels and attaches the
// stage, so a caller reads one error vocabulary rather than one per internal
// package (docs/rules.md §2.2).
var (
	// ErrNothingToCorrect means there is nothing a second request could fix:
	// no retryable failure, or no reply to show back. It is not a failure of
	// the extraction — the caller reports the first reply's problems and stops.
	ErrNothingToCorrect = errors.New("nothing worth asking again for")

	// ErrAlreadyRetried means the request handed in was itself built here.
	//
	// This is what makes "once" a property of the package rather than a rule
	// the caller has to remember. An unbounded loop against a model that is
	// confidently wrong spends money to receive the same answer.
	ErrAlreadyRetried = errors.New("this request is already a retry")

	// ErrReplyTooLarge means the reply exceeds [MaxReplyBytes]. It is refused
	// rather than truncated: a truncated JSON object is not a JSON object, and
	// showing a model a broken version of its own reply teaches it nothing.
	ErrReplyTooLarge = errors.New("the reply is too large to send back")

	// ErrSchema means the schema cannot be rendered: it describes no fields,
	// or the original request carried no JSON Schema bytes to reuse.
	ErrSchema = errors.New("invalid schema")

	// ErrBoundary means no delimiter could be generated for the reply block.
	//
	// It is separate because it describes a failure of this process, not of the
	// document, the schema, the model or a limit. Reporting it as one of those
	// would send an operator to inspect something that is not at fault.
	ErrBoundary = errors.New("could not generate a reply boundary")
)

// Fault classifies what was wrong with a reply, from the closed set of things
// worth asking again about.
//
// It is an enum rather than a message because it is the only description of a
// failure that reaches the instruction, and an enum has no room for a value.
// Each member maps to one fixed sentence; see [Fault.Sentence].
type Fault string

// The faults. Every member is a mistake the model made about the shape of its
// answer, never a fact about the document.
const (
	// FaultUnknown is the zero value and never appears in a [Failure] this
	// package produces.
	FaultUnknown Fault = ""

	// FaultNotJSON is a reply that does not parse as JSON at all — a Markdown
	// fence, a preamble, a truncated object.
	FaultNotJSON Fault = "not_json"

	// FaultNotObject is a reply that parses but is an array, a string or a
	// number rather than the object the schema describes.
	FaultNotObject Fault = "not_object"

	// FaultType is a field whose value is not of the type the schema declares:
	// prose where a number was required, a string where a boolean was.
	FaultType Fault = "type"
)

// String returns the fault name, or "unknown" for the zero value, so a fault
// never renders as an empty string in a diagnostic.
func (f Fault) String() string {
	if f == FaultUnknown {
		return "unknown"
	}
	return string(f)
}

// Document reports whether the fault belongs to the whole reply rather than to
// one field, which is what tells a renderer not to look for a field key.
func (f Fault) Document() bool {
	return f == FaultNotJSON || f == FaultNotObject
}

// Failure is one thing wrong with a reply that is worth asking again about.
//
// It has two fields and neither can hold a value. That is deliberate and
// load-bearing: a Failure is the only thing besides the schema that reaches the
// instruction, so it is built so that there is nowhere for document content to
// sit — the same structural argument ADR-0021 makes for Event. Do not add a
// message, a raw value, a candidate or a map here.
type Failure struct {
	// Field is the schema key of the field at fault — "total",
	// "items[0].unit_price" — and is empty for a [Fault.Document] fault.
	//
	// It is the developer's own key, derived from their struct. It is checked
	// against the schema before it is rendered, and dropped if it names no
	// field, so a caller cannot smuggle text into the instruction through it.
	Field string

	// Fault is what was wrong.
	Fault Fault
}

// FieldResult pairs a validation result with the key it was produced for.
//
// A slice rather than a map, because the instruction lists the failures in this
// order and map iteration order is random: two identical extractions would
// otherwise produce two different instructions, which defeats both golden tests
// and a provider's prompt cache.
type FieldResult struct {
	// Field is the key, as [schema.IndexKey] produces it for slice elements.
	Field string

	// Result is what validation determined. Only its Kind, Found, Converted and
	// Rules are read; Raw and Value are document content and are never
	// looked at, which is why nothing here can leak into a [Failure].
	Result validate.Result
}

// Assess reports what about a reply is worth asking again for, and returns nil
// when nothing is.
//
// Nil is the common and correct answer. Assess is deliberately reluctant: it
// returns a failure only for a reply that is not JSON, a reply that is not an
// object, and a field whose value could not be converted to its declared type
// with no format rule to explain it. A value that failed min, max, enum or
// format is the document's content disagreeing with the schema, and a second
// request can only make one up.
//
// results may be in any order the caller likes, but the order is preserved into
// the instruction, so a caller should pass them in schema order for a stable
// prompt.
func Assess(reply []byte, results []FieldResult) []Failure {
	// Checked before anything is parsed or allocated: an over-large reply is
	// not going to be repaired by re-asking, and parsing it to find that out
	// would be the allocation the limit exists to prevent.
	if len(reply) == 0 || len(reply) > MaxReplyBytes {
		return nil
	}

	var decoded any
	if err := json.Unmarshal(reply, &decoded); err != nil {
		return []Failure{{Fault: FaultNotJSON}}
	}
	if _, ok := decoded.(map[string]any); !ok {
		return []Failure{{Fault: FaultNotObject}}
	}

	var out []Failure
	for _, r := range results {
		if !typeFailure(r.Result) {
			continue
		}
		out = append(out, Failure{Field: r.Field, Fault: FaultType})
	}
	return out
}

// typeFailure reports whether a result describes a value the model returned in
// the wrong shape, as opposed to a value the document simply does not support.
//
// A composite is excluded because validate never converts one — only the
// caller holds the results of the fields a struct or slice is assembled from,
// so Converted is false for every object and array and means nothing here.
//
// A field carrying a format rule is excluded because "2026-13-45 is not a
// date" is a fact about the document. The model reported what it read; asking
// again either gets the same text back or gets a repaired date that the
// document does not contain.
func typeFailure(r validate.Result) bool {
	if !r.Found || r.Converted {
		return false
	}
	if r.Kind == schema.KindArray || r.Kind == schema.KindObject {
		return false
	}
	for _, rr := range r.Rules {
		if strings.HasPrefix(rr.Rule, schema.RuleFormat+"=") {
			return false
		}
	}
	return true
}

// Build assembles the follow-up request from the original request, the schema,
// the reply that failed and what was wrong with it.
//
// orig contributes its JSON Schema bytes and its temperature, so the second
// request asks for exactly the same shape at exactly the same determinism. Its
// Content — the document — is deliberately not carried over: the model has
// already read the document, and re-sending it doubles the cost of the reading
// to fix a formatting mistake.
//
// Build refuses when orig is itself a retry, which is how "once" is enforced
// here rather than in the caller's loop.
//
// Errors never carry document content, the reply, or any part of either — not
// the bytes, not a substring, not a length (docs/rules.md §2.5).
func Build(orig prompt.Request, s schema.Schema, reply []byte, failures []Failure) (prompt.Request, error) {
	return build(nil, orig, s, reply, failures)
}

// build is Build with the entropy source injected, so tests can pin the
// boundary identifier and assert on exact bytes. A nil entropy means
// crypto/rand. It is a parameter and never a package variable, because a
// package variable is global state two clients could observe
// (docs/rules.md §5.5).
func build(entropy io.Reader, orig prompt.Request, s schema.Schema, reply []byte, failures []Failure) (prompt.Request, error) {
	if Retried(orig) {
		return prompt.Request{}, fmt.Errorf("%w: a reply may be corrected once", ErrAlreadyRetried)
	}
	if len(s.Fields) == 0 {
		return prompt.Request{}, fmt.Errorf("%w: schema describes no fields", ErrSchema)
	}
	if len(orig.Schema) == 0 {
		return prompt.Request{}, fmt.Errorf("%w: the original request carried no json schema", ErrSchema)
	}
	if len(reply) == 0 {
		return prompt.Request{}, fmt.Errorf("%w: the reply is empty, so there is nothing to correct", ErrNothingToCorrect)
	}
	if len(reply) > MaxReplyBytes {
		return prompt.Request{}, fmt.Errorf("%w: %d bytes, limit %d", ErrReplyTooLarge, len(reply), MaxReplyBytes)
	}
	// Resolved against the schema before anything is copied, so that a request
	// whose failures all name fields this schema does not have costs nothing to
	// reject rather than producing an instruction with an empty problem list.
	listed, _ := resolve(s, failures)
	if len(listed) == 0 {
		return prompt.Request{}, fmt.Errorf("%w: no reported failure is worth a second request", ErrNothingToCorrect)
	}

	id, err := boundary(entropy, reply)
	if err != nil {
		return prompt.Request{}, err
	}

	return prompt.Request{
		// The only assignment to Instruction in this package, and its right
		// hand side cannot see reply or orig.Content. docs/rules.md §7.2.
		Instruction: Instruction(s, failures),
		Content: []prompt.Content{{
			// The reply belongs to no page and to no reading. Page 0 and
			// ReadingUnknown say so; claiming page 1 would attribute a value to
			// a page it did not come from.
			Reading: prompt.ReadingUnknown,
			Page:    0,
			Text:    delimit(id, reply),
		}},
		Schema:      orig.Schema,
		Temperature: orig.Temperature,
	}, nil
}

// Retried reports whether req was built by this package.
//
// It looks for the correction heading, which cannot occur in a first request:
// an instruction built by internal/prompt is the fixed prose plus the schema's
// own field list, and every string taken from the schema passes through a
// collapse that removes newlines — so a developer who literally names a field
// "## Correction" still cannot produce the heading, because the heading has a
// newline on each side of it.
//
// The alternative would be a counter on the request, which means changing the
// model seam for a value only the core reads. See docs/adr/0007-model-seam.md.
func Retried(req prompt.Request) bool {
	return strings.Contains(req.Instruction, correctionHeading)
}
