# ADR-0004: `Result[T]` carries partial data

**Status:** Accepted · **Date:** 2026-08-26

## Context

A document with twelve fields where eleven were read cleanly and one was
illegible is the normal case, not the exceptional one. Scanners clip edges,
staples punch holes, handwriting overlaps printed boxes, and a phone photograph
of a receipt is out of focus in exactly one corner.

Go's `(T, error)` convention pushes toward a binary answer, and the naive
implementation returns `(Invoice{}, err)` the moment any field fails. That
discards eleven correct values to report one failure, and the caller — who
often only needed the invoice number and the total — cannot proceed even though
the data they wanted is sitting in the discarded struct.

The opposite failure is worse. If a field cannot be read and the library
returns a zero value in a `float64`, the caller sees `0.00` and has no way to
distinguish "the total is zero" from "we could not read the total". A payments
system that cannot tell those apart will eventually pay the wrong amount.

## Decision

`Extract` returns either a `*Result[T]` or an error, never both, and `Valid`
rather than `error` reports whether the data is good.

```go
type Result[T any] struct {
    Data        T
    Valid       bool
    Confidence  float64
    Fields      map[string]FieldResult
    NeedsReview bool
    Reasons     []ReviewReason
    Metadata    Metadata
}
```

- `error` is non-nil only when the extraction as a whole is meaningless: the
  source could not be read, no reading could be produced at all, a limit was
  exceeded, or the context was cancelled. In those cases `Result` is nil.
- `Valid` reports whether every validation rule in the schema passed. It is
  independent of `error`.
- `Data` holds every field that was read, whether or not `Valid` is false.
- `Fields` holds one entry per schema field, including fields that were not
  found. A slice field additionally contributes one entry per extracted element
  (`items[0]`, `items[1]`…), so the number of keys depends on what was read.

A field that could not be read is **absent and marked absent**. It is never
filled with a zero value, and never guessed (rule
[§8.5](../rules.md#8-confidence-and-provenance)). `FieldResult.Found` reports
presence; `FieldResult.Errors` says why not.

The caller's decision therefore reads:

```go
res, err := ovrin.Extract[Invoice](ctx, c, src)
if err != nil {
    return err                      // nothing usable came back
}
if !res.Valid || res.NeedsReview {
    return queue.ForHumanReview(res) // usable, but not automatically
}
```

## Consequences

**Good.** Callers who need three fields out of twelve are not blocked by the
other nine. The distinction between "absent" and "zero" is representable, which
is the difference between a correct payments integration and an incorrect one.
Human-review workflows get everything they need — the partial data, the
per-field reasons, and the confidence — in one value.

**Bad.** This is not the shape Go programmers expect. `err == nil` does not mean
"it worked", and every caller must learn that `Valid` exists; some will ignore
it and ship a bug. Two failure channels means two things to document, two
things to test and two things to get wrong. `Result` is a wide struct, and wide
structs accumulate fields. Callers who genuinely want all-or-nothing must write
the check themselves.

We accept it because the alternative is a library that either destroys good
data or lies about missing data, and both are worse than an API that has to be
read carefully once.

## Alternatives considered

- **Strict `(T, error)`; any field failure fails the call.** Rejected:
  discards correct data and makes the common case an error case.
- **Return the zero value for unreadable fields, with no marker.** Rejected:
  silently indistinguishable from a legitimately zero value. This is the single
  most dangerous option available.
- **Make every field in the user's struct a pointer.** Rejected: it does encode
  absence, but it inflicts `*string`/`*float64` on every schema the user
  writes, and still carries no reason, confidence or provenance.
- **Return `(T, []FieldError, error)`.** Rejected: three return values, and no
  natural place for confidence, provenance or review reasons as they are added.
