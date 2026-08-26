# ADR-0030: A thirteenth sentinel, for ovrin's own failures

**Status:** Accepted · **Date:** 2026-08-26 · **Amends** [ADR-0027](0027-twelve-sentinels-and-one-op-vocabulary.md)

## Context

[ADR-0027](0027-twelve-sentinels-and-one-op-vocabulary.md) settled twelve
sentinels, and the test it applied was whether a condition has a **distinct
remedy**. Implementing the pipeline turned up two conditions that have one and
that none of the twelve covers.

`internal/prompt` can fail because `crypto/rand` returned an error — the host's
entropy source is broken — or because the pipeline handed it a page carrying
both recovered text and a page image, which cannot honestly be reduced to one
content item and must not be silently halved
(rule [§6.1](../rules.md#6-adapters)).

Neither is the document's fault. Both were reachable only by mapping onto
`ErrNoContent`, which tells an operator to go and inspect a document that is
perfectly fine, or onto `ErrBadResponse`, which blames a provider that was never
called. Every existing sentinel points at something outside ovrin: the
document, a limit, a provider, a credential. Nothing points at ovrin.

That gap will not stay confined to one package. Any stage can fail because the
stage before it produced something the contract said was impossible, and a
library that reports its own bugs as the user's is a library people learn to
distrust.

## Decision

A thirteenth sentinel:

```go
// ErrInternal means ovrin failed, rather than the document, a provider or a
// limit. Reaching it is a bug in ovrin or a failure of the host — a broken
// entropy source, a pipeline stage handed input its contract forbids.
ErrInternal = errors.New("ovrin: internal failure")
```

Its remedy is distinct and it is the reason the sentinel exists: **file a bug**.
Do not re-scan the document, do not switch provider, do not raise a limit.

Internal packages keep their own specific sentinels — `prompt.ErrBoundary`,
`prompt.ErrAmbiguousContent` — and the root classifies them onto `ErrInternal`
while attaching the `Op` and the underlying cause. A caller who wants the
detail reaches it through `errors.Is` against the wrapped cause, exactly as
with every other error; a caller who only wants to know whose problem it is
tests the sentinel.

`ErrInternal` is never returned for anything a caller can fix by changing their
input or configuration. If a condition turns out to be a caller's to fix, it
needs its own sentinel or an existing one — not this.

## Consequences

**Good.** An operator can tell "your document is unreadable" from "ovrin is
broken", which are the two most different diagnoses this library produces and
were previously indistinguishable. Internal packages can fail honestly without
either inventing a public sentinel or borrowing one that misdirects. Bug
reports arrive with an error that says it is a bug.

**Bad.** Thirteen sentinels, and ADR-0027 already conceded twelve was a lot to
keep meaningfully distinct. `ErrInternal` is also the tempting default for any
condition a contributor has not thought about, which would turn it into a
dustbin that means nothing — the same failure mode as a catch-all exception.
Review has to push back on it, and no test can. And it is an admission in the
API surface that ovrin has bugs, which is true of every library and which most
do not say out loud.

## Alternatives considered

- **Map onto `ErrNoContent`.** Rejected: it sends an operator to inspect a
  document that is not at fault, which is worse than a vague error because it
  is a confident wrong answer.
- **Panic instead.** Rejected: rule [§1.6](../rules.md#1-public-api) permits
  exactly one panic, at construction on a nil provider. A library never
  terminates its host over a runtime condition, and a broken entropy source is
  a runtime condition.
- **Return the bare underlying error with no sentinel.** Rejected: callers
  would have nothing to test with `errors.Is`, so the only way to detect it
  would be matching message text — rule
  [§2.2](../rules.md#2-errors) exactly.
- **A sentinel per internal condition** — `ErrEntropy`, `ErrAmbiguousContent`
  in the public API. Rejected: it publishes ovrin's internal structure as
  compatibility surface, and every new internal failure mode would become a
  breaking addition.
