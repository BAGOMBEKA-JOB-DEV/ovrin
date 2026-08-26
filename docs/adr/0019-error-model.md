# ADR-0019: Sentinels plus a typed `*Error` with multi-error `Unwrap`

**Status:** Accepted · **Date:** 2026-08-26 · **Amended by** [ADR-0027](0027-twelve-sentinels-and-one-op-vocabulary.md)

## Context

Callers need to branch on what went wrong. A payment processor retries a rate
limit, alerts on an authentication failure, queues a malformed document for a
human and gives up on an unsupported format. Those are four different
responses, so four different error conditions must be distinguishable without
reading English.

Two further requirements are specific to ovrin. Errors travel through OCR
adapters, model adapters, fallback chains and the pipeline, so a caller must be
able to ask both "what kind of failure is this?" and "was it ultimately a
cancelled context?" about the same value. And errors must never contain
document content: a document is somebody's medical record, and an error string
is a log line that ends up in five systems nobody audited
(rule [§2.5](../rules.md#2-errors)).

Skyl solved the first part with a pattern worth copying directly rather than
reinventing.

## Decision

Package-level sentinels for the kinds, and one typed error carrying detail.

```go
var (
    ErrUnsupportedFormat = errors.New("ovrin: unsupported document format")
    ErrNoContent         = errors.New("ovrin: no readable content in document")
    ErrNoProvider        = errors.New("ovrin: no provider configured for this document")
    ErrSchema            = errors.New("ovrin: invalid schema")
    ErrLimitExceeded     = errors.New("ovrin: resource limit exceeded")
    ErrAuth              = errors.New("ovrin: provider authentication failed")
    ErrRateLimit         = errors.New("ovrin: provider rate limited")
    ErrUnavailable       = errors.New("ovrin: provider unavailable")
    ErrBadResponse       = errors.New("ovrin: provider returned an unusable response")
    ErrUnsupported       = errors.New("ovrin: unsupported by this provider")
    ErrEncrypted         = errors.New("ovrin: document is encrypted")
    ErrBadRequest        = errors.New("ovrin: provider rejected the request")
)

type Error struct {
    Op       Op            // the pipeline stage; see ADR-0027
    Provider string        // adapter name, if a provider was involved
    Page     int           // 1-based, 0 if not page-specific
    Field    string        // schema field, if field-specific
    Kind     error         // the sentinel
    Message  string        // never contains document content
    cause    error
}

func (e *Error) Unwrap() []error   // returns Kind and cause
```

The multi-error `Unwrap` is the load-bearing part. Returning both the sentinel
and the underlying cause means both of these work on the same value:

```go
errors.Is(err, ovrin.ErrRateLimit)
errors.Is(err, context.DeadlineExceeded)
```

Supporting rules, each of which exists because its absence causes a specific
bug:

- **Classify at the boundary, never branch on message text** (rule
  [§2.2](../rules.md#2-errors)). An adapter converts a provider's status code
  into a sentinel; nothing downstream ever looks at a string.
- **`Op`, `Page` and `Field` are populated wherever known**, because "OCR
  failed" is not actionable and "OCR failed on page 14" is.
- **`Message` never contains a value read from the document.** Enforced by a
  test in the shared adapter suite.
- **Field-level problems are not errors at all.** They are recorded on
  `FieldResult` ([ADR-0004](0004-partial-results.md)). `*Error` is for
  conditions that make the whole extraction meaningless.

## Consequences

**Good.** Callers get precise, stable branching without string matching. One
value answers both the kind question and the cause question. `Op`/`Page`/
`Field` make errors debuggable without a debugger. The pattern is already
proven in the maintainer's other library, so there is no novelty risk.

**Bad.** Twelve sentinels is a lot to document and to keep meaningfully
distinct, and the boundaries between `ErrNoContent` and `ErrNoProvider`, and
between `ErrSchema` and `ErrBadRequest`, will be argued about. Multi-error `Unwrap` requires Go 1.20 and is unfamiliar enough
that contributors will implement single-error `Unwrap` by habit. Adding a
sentinel is a compatibility event, so the initial set has to be close to right.
And having two failure channels — errors and `FieldResult.Errors` — means
contributors must think about which one applies, and will sometimes choose
wrong.

## Alternatives considered

- **Sentinels only.** Rejected: no room for `Op`, `Page`, `Provider` or
  `Field`, so every error is "something went wrong somewhere".
- **A typed error only, with a `Kind` string.** Rejected: `errors.Is` stops
  working and callers compare strings, which is rule
  [§2.2](../rules.md#2-errors) exactly.
- **One error type per condition.** Rejected: eleven types, eleven `errors.As`
  calls, and no way to ask a general question.
- **Single-error `Unwrap` returning the cause.** Rejected: `errors.Is` against
  the sentinel then fails, which is the more common query.
