# ADR-0008: Skyl is an adapter in its own module, not a core dependency

**Status:** Accepted · **Date:** 2026-08-26

## Context

`github.com/BAGOMBEKA-JOB-DEV/skyl` is the maintainer's other library: one Go
interface over OpenAI, Anthropic, Gemini and seventeen OpenAI-compatible hosts.
Ovrin needs a language model. The obvious move is for ovrin to depend on skyl
and take a `*skyl.Client` in its options — the division of labour is clean and
the story tells itself: skyl handles AI providers, ovrin handles document
intelligence.

Three facts complicate it.

Skyl's core module has **zero external dependencies**, and its ADR-0001 argues
at length that a module's dependencies are inherited by everyone who imports
it, so a dependency nobody calls is a permanent tax on every downstream user.
Ovrin depending on skyl would not violate that rule, but it would apply the
same tax to ovrin's users — including the ones who want to run a local model,
or who already have an LLM client and do not want a second one in `go.sum`.

Skyl's structured-output support — `Request.ResponseFormat`, the mechanism
ovrin needs — **is on `main` but is not in the `v0.1.0` tag**. A hard dependency
would mean ovrin v0.1 either pins a pseudo-version off an untagged commit, or
does not ship until skyl cuts v0.2.0.

Skyl's `Part` interface is **closed** — it has an unexported method and exactly
four implementations, with its ADR-0008 stating explicitly that widening it
breaks every exhaustive type switch outside the repository. There is no PDF
part. Ovrin cannot add one. Whatever ovrin sends must be text or images, which
is an architectural constraint ovrin has to satisfy regardless of how it
couples.

## Decision

The core module defines its own `Model` seam
([ADR-0007](0007-model-seam.md)) and depends on nothing.

`github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl` is a **separate module** with
its own `go.mod`, providing the skyl-backed implementation:

```go
import (
    "github.com/BAGOMBEKA-JOB-DEV/skyl"
    "github.com/BAGOMBEKA-JOB-DEV/skyl/provider/openai"
    ovrinskyl "github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl"
)

c := ovrin.New(
    ovrin.WithModel(ovrinskyl.New(
        skyl.New(openai.New(key)),
        ovrinskyl.WithModelID("gpt-5.2"),
    )),
)
```

This is the documented default and the path the getting-started guide takes. It
is not the only path, and nothing in the core knows it exists.

The adapter wraps `*skyl.Client` rather than `skyl.Provider`, so it inherits
skyl's retry policy and hooks rather than reimplementing them.

Because skyl's `Part` set is closed and has no PDF member, the adapter sends
text and `skyl.Image` parts only. Rasterising is therefore ovrin's problem, not
the adapter's — see [ADR-0010](0010-no-cgo-in-core.md).

A prerequisite is recorded in [`docs/roadmap.md`](../roadmap.md): **tag skyl
v0.2.0** so this adapter can require a real version rather than a pseudo-version
off `main`. Ovrin's own v0.1 does not block on it, because the core does not
depend on skyl at all.

## Consequences

**Good.** `go get` on ovrin's core pulls nothing. Ovrin v0.1 is not blocked on a
release in another repository. Users with an existing LLM client implement one
method instead of adopting a second library. Ollama, llama.cpp, a company's
internal gateway and a test fake are all first-class, none of them privileged
over skyl. The two projects version independently, so a breaking change in
skyl's API is one module's problem.

**Bad.** It is an interface over an interface, and for the common case — a skyl
user — that is a genuine layer of indirection with no benefit to them; when an
extraction misbehaves there are two abstractions to read through instead of
one. It is a second module to build, test, release and version, with its own
tag and its own row in the CI matrix. The marketing story is weaker: "ovrin is
built on skyl" is a better sentence than "ovrin has an optional skyl adapter".
And features skyl adds — prompt caching, batch endpoints — do not reach ovrin
users until the adapter surfaces them.

## Alternatives considered

- **Depend on skyl in the core and take a `*skyl.Client` option.** Rejected on
  three counts: it imposes skyl on every ovrin user, it blocks v0.1 on an
  unreleased skyl tag, and it contradicts the dependency-quarantine argument
  skyl itself makes in its ADR-0001. This remains a reasonable decision to
  reverse if ovrin and skyl end up always released together.
- **Depend on skyl's `Provider` interface only, not the client.** Rejected:
  still a core dependency, and it discards the retry and hook machinery that is
  the reason to use skyl rather than a raw provider.
- **Vendor the parts of skyl we need.** Rejected: a fork by another name, and
  it goes stale immediately.
- **No abstraction — call OpenAI directly, add providers later.** Rejected:
  provider independence is a stated design principle, and retrofitting a seam
  after users depend on the concrete path is far more expensive than defining
  it now.
