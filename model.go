package ovrin

import "context"

// Model produces structured JSON from document content.
//
// One method, deliberately. Ovrin makes exactly one kind of call — given this
// content and this JSON Schema, return an object matching the schema — so a
// chat abstraction would make every adapter author implement messages, roles,
// tool calling and streaming that ovrin never uses.
//
// Prompt construction stays on this side of the seam. An injected instruction
// in a document must not be able to reach a position where a model reads it as
// a directive, and that property holds identically across every provider only
// because the core builds the request. See
// docs/adr/0007-model-seam.md and docs/adr/0017-untrusted-document-content.md.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type Model interface {
	Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error)
}

// ModelRequest is one extraction call.
//
// Instruction and Content never mix. An adapter maps Instruction to the
// provider's system role and Content to the user role, or to the nearest
// equivalent; it never concatenates them.
type ModelRequest struct {
	// Instruction is built by ovrin from the schema. It never contains
	// document content.
	Instruction string

	// Content is the untrusted material, already delimited and labelled.
	Content []Content

	// Schema is the JSON Schema the reply must satisfy, as bytes so an adapter
	// can pass it to a provider verbatim.
	//
	// It is emitted fully expanded, with no $ref, additionalProperties false,
	// and every property listed in required — the narrowest dialect the major
	// providers agree on. A provider that still rejects it must surface
	// [ErrBadRequest] naming the construct, never silently relax it.
	Schema []byte

	// Temperature is nil for the provider's default. Extraction wants
	// determinism, so ovrin sets it low rather than leaving it unset.
	Temperature *float64
}

// ModelResponse is what a provider returned.
type ModelResponse struct {
	// JSON is the raw reply. It is not unmarshalled by the adapter: a model
	// returning invalid JSON must produce one ovrin error with the offending
	// bytes attached, rather than a different error per provider.
	JSON []byte

	// Usage is what the call consumed.
	Usage Usage

	// Raw is the provider's own response, for callers willing to type-assert.
	Raw any
}

// Content is one piece of material handed to a [Model]. It is always untrusted.
type Content struct {
	// Reading is which reading produced this content.
	Reading Reading

	// Page is 1-based.
	Page int

	// Text is set when Reading is text or OCR.
	Text string

	// Image is set when Reading is vision. Raw bytes, never base64 — encoding
	// is the adapter's job, and doing it twice corrupts the image.
	Image []byte

	// MediaType is the IANA media type, required when Image is set.
	MediaType string
}
