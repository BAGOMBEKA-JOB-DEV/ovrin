# ADR-0003: Go 1.22 floor, and `Extract[T]` is a package-level function

**Status:** Accepted · **Date:** 2026-08-26

## Context

The headline API is meant to read like this:

```go
res, err := ovrin.Extract[Invoice](ctx, client, src)
```

The obvious alternative reads better still, because it hangs the operation off
the configured client:

```go
res, err := client.Extract[Invoice](ctx, src)   // a generic method
```

That form was illegal in Go until one week ago. Methods could not declare their
own type parameters; only functions could. Go 1.27, released 19 August 2026,
lifted the restriction. Adopting it would set our minimum Go version to a
release that is days old — excluding, in practice, everyone, and certainly
every enterprise Go deployment, which is the audience for a document-processing
library.

Separately, we need a floor for the module. `skyl`, which ovrin's default model
adapter wraps, sets its core module floor at Go 1.22. Setting ovrin's floor
above skyl's would make the pair unusable together for anyone pinned between
them.

## Decision

The core module declares `go 1.22`. Adapter submodules may declare a higher
floor when a dependency forces one, and CI builds every module at both its
declared floor and the newest release (rule
[§11.2](../rules.md#11-formatting-and-tooling)).

The entry point is a package-level generic function:

```go
func Extract[T any](ctx context.Context, c *Client, src Source) (*Result[T], error)
```

The client is a parameter, not a receiver. `Result[T]` is a generic type, which
has been legal since Go 1.18 and is unaffected by the method restriction.

This is revisited when Go 1.27 is two releases old — approximately August 2027
— and not before.

## Consequences

**Good.** Ovrin compiles on every Go release still in support, and on the
version skyl targets. The call site is only marginally worse than the method
form and is a well-established Go idiom for exactly this constraint. When the
floor eventually rises, a method can be added alongside the function without
breaking anyone.

**Bad.** `Extract` cannot be discovered by typing `client.` in an editor, which
is how most people find API. Options are configured on the client but the type
parameter is on the function, so the two halves of a call are specified in two
different places. And the signature has three parameters where two would do.

## Alternatives considered

- **Require Go 1.27 and use a generic method.** Rejected: a one-week-old
  toolchain floor for a syntactic improvement. This is the correct answer in
  roughly two years.
- **Drop generics; return `any` and have the caller type-assert.** Rejected:
  the type safety *is* the product. `res.Data.Total` type-checking at compile
  time is the entire argument for doing this in Go rather than Python.
- **Generate per-type extractors with `go generate`.** Rejected: a build step,
  committed generated code and a worse error experience, to avoid a parameter.
- **Make `Client` generic — `ovrin.New[Invoice](...)`.** Rejected: forces one
  client per document type, so an application extracting five kinds of document
  configures its providers and limits five times.
