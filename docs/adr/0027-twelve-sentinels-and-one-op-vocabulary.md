# ADR-0027: A twelfth sentinel, and one `Op` vocabulary

**Status:** Accepted · **Date:** 2026-08-26 · **Amends** [ADR-0019](0019-error-model.md), [ADR-0021](0021-observability.md)

## Context

Two defects in the error model, both found by auditing the documentation
against itself before writing the code.

**Two sentinels are used but never defined.**
[ADR-0018](0018-fallback-is-a-decorator.md) specifies when a provider chain
advances entirely in terms of sentinels, and names `ErrServer` and
`ErrBadRequest`:

> Advance on `ErrRateLimit`, `ErrServer`, `ErrUnavailable` and unclassified
> transport errors. **Never** on `ErrAuth`, `ErrBadRequest` or
> `ErrUnsupported`.

[`docs/feature-matrix.md`](../feature-matrix.md) promises `ErrBadRequest` too:
a schema a provider rejects "surfaces as `ErrBadRequest` naming the construct".
Neither appears in ADR-0019's `var` block, which declares eleven sentinels and
says so.

**Two `Op` vocabularies disagree, and neither matches the pipeline.**
ADR-0019 gives `Error.Op` four values (`parse`, `ocr`, `extract`, `validate`).
ADR-0021 gives `Event.Op` six (adding `render` and `score`).
[`docs/pipeline.md`](../pipeline.md) documents nine stages, none of which is
called "parse" or "extract". A rasterising failure or a grounding failure has
no `Error.Op` value at all, so the field cannot say where the failure happened —
which is the entire reason it exists.

## Decision

**`ErrServer` is not added.** `ErrUnavailable` already reads "provider
unavailable" and covers the 5xx case. Two sentinels for one condition would
force every caller to check both forever.

**`ErrBadRequest` is added, making twelve.** It is genuinely distinct from
`ErrSchema`: `ErrSchema` means *your schema is invalid* and is raised before any
provider is contacted; `ErrBadRequest` means *your schema is valid and this
provider will not accept it*. The distinction is load-bearing because the
remedies differ — fix the struct, versus change provider or simplify the schema.
It is also the case skyl warns about: OpenAI's strict mode and Gemini's OpenAPI
subset accept different JSON Schema dialects.

```go
ErrBadRequest = errors.New("ovrin: provider rejected the request")
```

ADR-0018's lists are restated against the real vocabulary:

- **Advance on:** `ErrRateLimit`, `ErrUnavailable`, unclassified transport errors.
- **Never advance on:** `ErrAuth`, `ErrBadRequest`, `ErrUnsupported`, `ErrSchema`.

**One `Op` type, shared by `Error` and `Event`**, named after the pipeline
stages so that a value can be found in [`pipeline.md`](../pipeline.md):

```go
type Op string

const (
    OpUnknown   Op = ""
    OpDetect    Op = "detect"
    OpAcquire   Op = "acquire"
    OpRender    Op = "render"     // within acquire
    OpOCR       Op = "ocr"        // within acquire
    OpNormalise Op = "normalise"
    OpSchema    Op = "schema"
    OpPrompt    Op = "prompt"
    OpGenerate  Op = "generate"
    OpValidate  Op = "validate"
    OpGround    Op = "ground"
    OpScore     Op = "score"
)
```

`OpUnknown` is the documented unknown member rule
[§1.9](../rules.md#1-public-api) requires, and it is the zero value, so an
`Error` or `Event` built without setting `Op` is honest rather than wrong.

`Error.Op` and `Event.Op` change from `string` to `Op`. That is a type change,
permitted before v1.0 with a changelog note
([ADR-0024](0024-versioning-and-stability.md)).

## Consequences

**Good.** Every documented behaviour now names a sentinel that exists, so
ADR-0018 is implementable as written. `ErrBadRequest` separates two failures
with different remedies, which is the test for whether a sentinel earns its
place. One `Op` vocabulary means an operator reading a trace and a developer
reading an error see the same words, and both can look them up in one document.
A typed `Op` makes a typo a compile error rather than a mystery in a dashboard.

**Bad.** Twelve sentinels is more than eleven, and ADR-0019 was already candid
that the boundary between some of them "will be argued about" — `ErrBadRequest`
against `ErrSchema` is a new argument of exactly that kind. Twelve `Op`
constants is a lot for a field most callers ignore, and `OpRender`/`OpOCR`
overlap with `OpAcquire`, so two reasonable people will label the same failure
differently. Changing `Op` from `string` to `Op` breaks anyone who wrote
`ev.Op == "ocr"` — nobody has, because there is no code yet, which is precisely
why this is being fixed now.

## Alternatives considered

- **Add both `ErrServer` and `ErrBadRequest`, making thirteen.** Rejected:
  `ErrServer` and `ErrUnavailable` would be synonyms, and callers would have to
  check both for the rest of the project's life.
- **Add neither; rewrite ADR-0018 against the existing eleven.** Rejected for
  `ErrBadRequest`: a provider rejecting a valid schema is a real, distinct,
  actionable condition, and collapsing it into `ErrUnsupported` loses the
  detail that makes it actionable.
- **Keep `Op` as a plain `string`.** Rejected: rule
  [§1.9](../rules.md#1-public-api) requires string enums to be named types with
  an unknown member, and untyped strings are how the two vocabularies drifted
  apart in the first place.
- **Separate `ErrorOp` and `EventOp` types.** Rejected: they describe the same
  nine stages, and two names for one vocabulary is the defect being fixed.
