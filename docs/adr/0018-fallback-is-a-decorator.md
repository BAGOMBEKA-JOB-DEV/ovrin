# ADR-0018: Provider fallback is a decorator, not core behaviour

**Status:** Accepted · **Date:** 2026-08-26

## Context

Production systems should not fail because one provider is unavailable. The
original design asked for chains:

```text
Google OCR → failed → AWS Textract → failed → Tesseract → success
```

The question is where that logic lives. The obvious place is the core, with a
`WithOCRFallback(a, b, c)` option and a loop inside the pipeline. That is how
most libraries do it, and it means the core grows a policy engine: which errors
are retryable, how long to wait, whether to fall back on a slow response or
only a failed one, whether to remember that a provider is down.

Skyl faced the same question for model providers and recorded the answer in its
own project plan: a router *"composes as a `Provider` that wraps others, so it
needs no core change."* The seam is already the right shape. A type that
implements `OCR` and delegates to a list of `OCR` values is a fallback chain,
and the core does not need to know it exists.

## Decision

Fallback is a decorator over the seam, shipped in the core because it needs no
dependencies, but architecturally invisible to the pipeline.

```go
c := ovrin.New(
    ovrin.WithOCR(ovrin.OCRChain(googleOCR, textract, tesseract)),
    ovrin.WithModel(ovrin.ModelChain(primary, secondary)),
)
```

`OCRChain` returns an `OCR`. `ModelChain` returns a `Model`. The pipeline sees
one provider and cannot tell the difference.

Behaviour, which is the part worth pinning down:

- Advance on `ErrRateLimit`, `ErrServer`, `ErrUnavailable` and unclassified
  transport errors. **Never** on `ErrAuth`, `ErrBadRequest` or
  `ErrUnsupported` — a misconfigured credential should fail loudly on the first
  provider, not silently degrade to the third.
- Every attempt is reported through the hook
  ([ADR-0021](0021-observability.md)) with the provider name and the error.
  Silent degradation is the failure mode that makes fallback dangerous: a
  system running on its worst provider for three weeks, with nobody aware.
- The provider that succeeded is recorded in `Metadata` and in every
  `Provenance` for that reading, so a result carries the evidence of which
  chain member produced it.
- Exhausting the chain returns an error wrapping **every** attempt's error, so
  the caller sees all three failures rather than only the last.
- No circuit breaking, no health memory, no cost-based selection in v0.1. Each
  extraction starts at the head of the chain.

Because a chain is an ordinary implementation of the seam, a user who wants
circuit breaking, weighted routing or cost-aware selection writes their own and
passes it to the same option.

## Consequences

**Good.** The pipeline has no fallback code in it and no policy to configure.
Chains nest and compose — a chain of chains is a chain. Users can replace the
strategy entirely without forking. Testing fallback needs no pipeline: it is a
unit test over a decorator with fake providers.

**Bad.** Restarting at the head of the chain on every extraction means a
sustained outage at the primary provider costs one failed call per document,
which at scale is a meaningful waste of latency and rate-limit budget; circuit
breaking is the fix and it is not in v0.1. A chain hides cost differences —
falling back from a cheap provider to an expensive one is invisible until the
bill arrives, and the hook is the only warning. And error aggregation produces
long error strings that are unpleasant to read in a log.

## Alternatives considered

- **Fallback inside the pipeline with a `WithOCRFallback` option.** Rejected:
  puts policy in the core and gives users no way to change it.
- **Fallback inside each adapter.** Rejected: violates rule
  [§6.2](../rules.md#6-adapters) — adapters map, they do not decide — and would
  be reimplemented differently in every adapter.
- **No fallback; the caller retries.** Rejected: the caller would have to
  re-run the whole pipeline, repeating parsing and OCR that already succeeded.
- **Circuit breaking in v0.1.** Rejected as scope: it needs shared mutable
  state and a policy for half-open probing, which is a design of its own.
  Deferred to v1.0, tracked in [`docs/roadmap.md`](../roadmap.md).
