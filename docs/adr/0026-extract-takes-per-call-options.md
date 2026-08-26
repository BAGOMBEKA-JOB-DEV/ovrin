# ADR-0026: `Extract` takes per-call options

**Status:** Accepted · **Date:** 2026-08-26 · **Amends** [ADR-0003](0003-go-floor-and-generics.md)

## Context

[ADR-0003](0003-go-floor-and-generics.md) declared the entry point with exactly
three parameters and defended the shape in prose: *"the signature has three
parameters where two would do."*

```go
func Extract[T any](ctx context.Context, c *Client, src Source) (*Result[T], error)
```

[`docs/getting-started.md`](../getting-started.md) then showed a call with four,
because the feature it was demonstrating needs one:

```go
res, err := ovrin.Extract[Invoice](ctx, client, src,
    ovrin.WithReading(ovrin.ReadingBoth),
)
```

Both cannot be right, and the contradiction was found while planning the
implementation rather than by any check — the first concrete evidence for the
drift problem the anti-drift harness exists to solve.

The question it forces is not cosmetic. If every option is set on the `Client`,
then configuration is per-client, and a caller who wants two readings for one
high-value document out of ten thousand must construct a second `Client` to get
them. That is a real cost paid by exactly the users the feature is for: the ones
processing a document where being wrong matters.

The counter-argument is that two places to configure one thing is two places to
look, and that a per-call option silently overriding a client default is a
surprise.

## Decision

`Extract` accepts variadic options:

```go
func Extract[T any](ctx context.Context, c *Client, src Source, opts ...Option) (*Result[T], error)
```

There is **one** `Option` type, usable at both levels. `New` applies options to
the client's configuration; `Extract` copies that configuration and applies its
own options over the top, for that call only. The client is never mutated, so
concurrent extractions with different options cannot interfere
(rule [§5.1](../rules.md#5-concurrency-and-resources)).

Not every option is meaningful per call. Options that configure a provider or a
resource that is built once — `WithModel`, `WithOCR`, `WithRenderer`,
`WithHook` — return an error from `Extract` if passed there, rather than being
silently ignored (rule [§6.1](../rules.md#6-adapters)). Options that select
behaviour for one document — `WithReading`, `WithReviewThreshold`,
`WithScorer`, `WithDateOrder` and the limits — work at both levels.

ADR-0003's other two decisions are unaffected and still stand: the Go floor is
1.22, and `Extract` is a package-level generic function rather than a method,
because generic methods require Go 1.27.

## Consequences

**Good.** The common call is unchanged — three arguments, exactly as ADR-0003
wanted. Per-document policy costs one line instead of a second `Client`, which
is what makes cross-validation usable in a real pipeline where most documents
are routine and a few are not. One `Option` type means one thing to learn.
Copy-then-overlay keeps the client immutable, so nothing about this is racy.

**Bad.** Two places to configure the same thing, and a reader of a call site
cannot see the client's defaults. Options split into two classes — per-client
only, and both — which is a distinction that has to be documented and that
users will get wrong until they hit the error. `Extract`'s signature is now
four parameters where ADR-0003 was already uncomfortable with three. And the
copy-per-call is a small allocation on every extraction, which is irrelevant
next to a model call but is not nothing.

## Alternatives considered

- **Keep the three-parameter signature; fix the guide.** Rejected: it makes the
  per-document case require a second client, which is the wrong tax to levy on
  the users who care most about correctness.
- **A separate `ExtractWith[T](ctx, c, src, cfg Config)`.** Rejected: an
  exported config struct, which rule [§1.4](../rules.md#1-public-api) forbids,
  and a second entry point doing the same job.
- **A distinct `CallOption` type.** Rejected: it makes the two classes explicit
  at compile time, which is genuinely better, at the cost of two option
  vocabularies, two sets of `With*` functions, and a user having to know which
  is which before they can type anything. The runtime error is a worse
  diagnostic but a much smaller API.
- **Client methods that return a modified client** — `c.WithReading(...)`.
  Rejected: reads well, allocates a client per call, and invites the belief that
  the original was mutated.
