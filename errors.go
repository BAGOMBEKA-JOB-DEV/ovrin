package ovrin

import (
	"errors"
	"fmt"
	"strings"
)

// Op names a stage of the extraction pipeline.
//
// The same vocabulary is used by [Error] and by [Event], so an operator reading
// a trace and a developer reading an error see the same word, and both can look
// it up in docs/pipeline.md. See docs/adr/0027-twelve-sentinels-and-one-op-vocabulary.md.
type Op string

// The pipeline stages, in the order a document passes through them.
//
// OpRender and OpOCR happen within acquisition; they are named separately
// because a failure in either is worth distinguishing from a failure to choose
// a reading at all.
const (
	// OpUnknown is the zero value. An Error or Event that does not know its
	// stage says so rather than claiming one.
	OpUnknown Op = ""

	OpDetect    Op = "detect"
	OpAcquire   Op = "acquire"
	OpRender    Op = "render"
	OpOCR       Op = "ocr"
	OpNormalise Op = "normalise"
	OpSchema    Op = "schema"
	OpPrompt    Op = "prompt"
	OpGenerate  Op = "generate"
	OpValidate  Op = "validate"
	OpGround    Op = "ground"
	OpScore     Op = "score"
)

// String returns the stage name, or "unknown" for the zero value.
func (o Op) String() string {
	if o == OpUnknown {
		return "unknown"
	}
	return string(o)
}

// The conditions an extraction can fail on.
//
// These are the values to test with [errors.Is]. Nothing in ovrin, and nothing
// calling it, should ever branch on the text of an error message: a provider
// rewording a response must not change how a program behaves. See
// docs/rules.md §2.2.
var (
	// ErrUnsupportedFormat means the source is not a format ovrin can read.
	// Format is determined by content, so this is not a filename problem.
	ErrUnsupportedFormat = errors.New("ovrin: unsupported document format")

	// ErrNoContent means the document was read but yielded nothing usable —
	// a PDF whose text layer decodes to rubbish, or a blank scan.
	ErrNoContent = errors.New("ovrin: no readable content in document")

	// ErrNoProvider means no configured provider can read this document. The
	// message names the ways to fix it, because the remedy is never obvious
	// from the condition alone.
	ErrNoProvider = errors.New("ovrin: no provider configured for this document")

	// ErrSchema means the Go type cannot be turned into a schema: an unknown
	// rule, an unsupported field type, a recursive type, a malformed tag. It
	// is raised before any provider is contacted, so a typo costs nothing.
	ErrSchema = errors.New("ovrin: invalid schema")

	// ErrLimitExceeded means a resource limit was reached. The message names
	// the limit and the option that raises it. See
	// docs/adr/0020-resource-limits.md.
	ErrLimitExceeded = errors.New("ovrin: resource limit exceeded")

	// ErrAuth means a provider rejected the credential. A fallback chain never
	// advances past this: a misconfigured key should fail loudly on the first
	// provider rather than silently degrade to the third.
	ErrAuth = errors.New("ovrin: provider authentication failed")

	// ErrRateLimit means a provider is throttling. A fallback chain advances.
	ErrRateLimit = errors.New("ovrin: provider rate limited")

	// ErrUnavailable means a provider could not be reached or returned a
	// server error. A fallback chain advances.
	ErrUnavailable = errors.New("ovrin: provider unavailable")

	// ErrBadResponse means a provider replied with something unusable — most
	// often JSON that does not parse. The offending bytes are attached to the
	// [Error] so the failure can be diagnosed rather than guessed at.
	ErrBadResponse = errors.New("ovrin: provider returned an unusable response")

	// ErrUnsupported means a provider cannot do what was asked: images sent to
	// a model without vision, a URL to an adapter that requires inline data.
	// An adapter returns this rather than quietly producing a worse answer.
	ErrUnsupported = errors.New("ovrin: unsupported by this provider")

	// ErrEncrypted means the document is encrypted. The message names the
	// encryption. Password support is a later decision.
	ErrEncrypted = errors.New("ovrin: document is encrypted")

	// ErrInternal means ovrin failed, rather than the document, a provider or
	// a limit: a broken entropy source, or a pipeline stage handed input its
	// contract forbids.
	//
	// The remedy is distinct and is the reason this exists — file a bug. Do
	// not re-scan the document, switch provider or raise a limit. See
	// docs/adr/0030-an-internal-failure-sentinel.md.
	ErrInternal = errors.New("ovrin: internal failure")

	// ErrBadRequest means a provider rejected a request ovrin considers valid
	// — most often a JSON Schema dialect it does not accept.
	//
	// This is distinct from [ErrSchema], and the distinction is the remedy:
	// ErrSchema means fix the struct, ErrBadRequest means change provider or
	// simplify the schema.
	ErrBadRequest = errors.New("ovrin: provider rejected the request")
)

// Error carries the detail behind one of the sentinels above.
//
// Kind holds the sentinel, so [errors.Is] finds it. Unwrap also returns the
// underlying cause, so a single value answers both "what kind of failure was
// this?" and "was it ultimately a cancelled context?".
//
// Message never contains content read from the document. A document is
// somebody's invoice or medical record, and an error string is a log line that
// ends up in systems nobody audited. See docs/rules.md §2.5 and §7.5.
type Error struct {
	// Op is the pipeline stage that failed.
	Op Op

	// Provider names the adapter involved, if one was.
	Provider string

	// Page is 1-based, and zero when the failure is not page-specific.
	Page int

	// Field is the schema field, when the failure is field-specific. It is a
	// field name, never a field value.
	Field string

	// Kind is the sentinel this error is an instance of.
	Kind error

	// Message adds detail. It never contains document content.
	Message string

	// cause is the underlying error, reached through errors.Is and errors.As
	// rather than exposed as a field.
	cause error
}

// Error implements the error interface.
//
// The sentinel's own "ovrin: " prefix is trimmed so it is not printed twice.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("ovrin: ")
	if e.Op != OpUnknown {
		b.WriteString(e.Op.String())
		b.WriteString(": ")
	}
	if e.Provider != "" {
		b.WriteString(e.Provider)
		b.WriteString(": ")
	}
	if e.Kind != nil {
		b.WriteString(strings.TrimPrefix(e.Kind.Error(), "ovrin: "))
	} else {
		b.WriteString("extraction failed")
	}
	if e.Page > 0 {
		fmt.Fprintf(&b, " (page %d)", e.Page)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, " (field %s)", e.Field)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

// Unwrap returns both the sentinel and the underlying cause, so that
// errors.Is(err, ovrin.ErrRateLimit) and errors.Is(err, context.Canceled) can
// both succeed against the same value.
func (e *Error) Unwrap() []error {
	switch {
	case e.Kind != nil && e.cause != nil:
		return []error{e.Kind, e.cause}
	case e.Kind != nil:
		return []error{e.Kind}
	case e.cause != nil:
		return []error{e.cause}
	default:
		return nil
	}
}

// withCause attaches an underlying error, which becomes reachable through
// [errors.Is] and [errors.As] without appearing in the message.
//
// Unexported: only this package builds an Error, and ADR-0019 settles that the
// cause is reached through Unwrap rather than through an accessor. Exporting a
// getter would give callers a second way to ask the same question, and the two
// would drift.
func (e *Error) withCause(err error) *Error {
	e.cause = err
	return e
}
