# ADR-0007: The `Model` seam takes a JSON schema, not a conversation

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin needs a language model to turn normalised document content into a
structured object. The question is what interface to define for that, given
that we deliberately do not depend on any particular provider
([ADR-0008](0008-skyl-is-an-adapter.md)).

The tempting answer is to adopt a chat abstraction: messages, roles, tools,
streaming, system prompts. Every AI library has one, skyl has a good one, and
reusing it means no new interface.

It is the wrong shape here. Ovrin makes exactly one kind of call: *given this
text, these page images, and this JSON schema, return an object matching the
schema.* It has no conversation, no turns, no tool loop and nothing to stream —
a partially-received JSON object is of no use to a validator that must see the
whole thing. Adopting a chat interface would mean every adapter author
implements message construction, role handling, tool calling and streaming, all
of it dead weight, and every one of them would build the extraction prompt
slightly differently.

Prompt construction is also the place where prompt-injection defence lives
([ADR-0017](0017-untrusted-document-content.md)). If adapters build prompts,
the security property is reimplemented once per adapter and holds only as well
as the weakest one. It has to be in the core, on the near side of the seam.

## Decision

The seam is one method.

```go
// Model produces structured JSON from document content.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type Model interface {
    Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error)
}

type ModelRequest struct {
    Instruction string      // built by ovrin; never contains document content
    Content     []Content   // the untrusted material, delimited and labelled
    Schema      []byte      // JSON Schema the reply must satisfy
    Temperature *float64
}

type ModelResponse struct {
    JSON  []byte  // raw; ovrin unmarshals and validates
    Usage Usage
    Raw   any     // provider-specific escape hatch
}
```

The core builds `Instruction`, assembles and delimits `Content`, derives
`Schema` from the user's struct, and unmarshals, validates and scores the
reply. The adapter translates the request into a vendor's wire format, asks for
constrained JSON output if the vendor supports it, and returns the bytes. It
makes no decisions (rule [§6.2](../rules.md#6-adapters)).

`ModelResponse.JSON` is returned raw rather than unmarshalled so that a model
which returns syntactically invalid JSON produces an ovrin error with the
offending bytes attached, rather than an adapter-specific one.

An adapter whose provider cannot constrain output to a schema is still valid:
it embeds the schema in the instruction and returns whatever came back. Ovrin
validates either way, and the difference shows up as a confidence signal rather
than as two code paths.

## Consequences

**Good.** An adapter is roughly a hundred lines and has one obvious correct
implementation. Prompt construction and injection defence live in one place and
are tested once. The interface has no surface for streaming, tools or
conversation state, so it cannot grow those by accident. Anyone can implement
it against a provider we have never heard of, or against a local model, without
touching ovrin.

**Bad.** It is a second abstraction stacked on whatever the user's AI library
already provides — for skyl users, an interface over an interface, which is
real conceptual overhead and one more layer to read when debugging. Multi-turn
strategies are foreclosed: a self-correcting loop that shows the model its own
validation failures cannot be expressed, and if we want that later the seam has
to change. Provider features with no representation here — caching hints,
reasoning-effort controls, batch endpoints — are reachable only through
adapter-specific options, which fragments configuration.

**Neutral.** `Raw any` is an escape hatch that will be abused. That is what
escape hatches are for; the alternative is users forking the adapter.

## Alternatives considered

- **Adopt skyl's `Provider` interface directly.** Rejected: it commits ovrin's
  core to skyl's dependency and release cadence, and imports four methods and a
  conversation model for a job that needs one method and none of it.
- **A chat-shaped seam of our own** — messages, roles, tools. Rejected: every
  adapter author implements features ovrin never calls, and prompt construction
  ends up on the far side of the security boundary.
- **`Generate(ctx, prompt string) (string, error)`.** Rejected: too thin. With
  no schema field, no adapter can use native structured-output modes, which are
  the single largest reliability win available.
- **Return an unmarshalled `map[string]any`.** Rejected: moves JSON parsing
  into every adapter, so malformed output produces a different error per
  provider and the raw bytes are lost before anyone can diagnose them.
