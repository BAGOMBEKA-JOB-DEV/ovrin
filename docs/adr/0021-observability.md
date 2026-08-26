# ADR-0021: Hooks in the core, OpenTelemetry in its own module

**Status:** Accepted · **Date:** 2026-08-26 · **Amended by** [ADR-0027](0027-twelve-sentinels-and-one-op-vocabulary.md)

## Context

Document extraction is slow, costly and probabilistic, which makes it exactly
the kind of workload people need to observe. Operators want per-stage latency,
which provider served each call, token and page counts for cost attribution,
confidence distributions, and review rates over time.

OpenTelemetry is the obvious answer and it is a large dependency —
`go.opentelemetry.io/otel` plus its metric and trace SDKs pull in a substantial
graph. Rule [§4.1](../rules.md#4-dependencies) says the core has zero external
dependencies, and OTel would break it for a feature many users do not want.

There is also a subtler hazard specific to this library. Traces and metrics get
shipped to third-party observability vendors. A span attribute containing an
extracted value would send somebody's medical record or national ID number to a
SaaS the document's subject never heard of. That is a privacy incident created
by an observability feature, and it is the sort of thing that happens by
accident when someone adds a helpful attribute.

## Decision

**The core emits hooks.** One function type, no dependencies:

```go
type Hook func(ctx context.Context, ev Event)

type Event struct {
    Op         Op            // the pipeline stage; see ADR-0027
    Provider   string
    Page       int
    Attempt    int
    Duration   time.Duration
    Err        error
    Bytes      int64
    Pages      int
    Fields     int
    Usage      Usage         // tokens, page-units
    Confidence float64
    Review     bool
}
```

Hooks run synchronously on the calling goroutine, which is documented, because
a hook that blocks slows extraction and the caller must know that is their
responsibility.

**OpenTelemetry lives in `ovrin/otel`**, a separate module that converts events
into spans and metrics. It depends on OTel; the core does not.

```go
c := ovrin.New(ovrinotel.Option(tracerProvider, meterProvider))
```

**No document content in events, ever** (rule
[§7.5](../rules.md#7-untrusted-input)). The `Event` struct has no field capable
of carrying a value — not a `map[string]any`, not a `Raw`, not a note. This is
structural rather than a guideline, because a guideline would be violated the
first time somebody found it convenient. Field *names* are countable and appear
as `Fields`; field *values* are not representable.

The metric and span names `ovrin/otel` emits are listed in
[`docs/observability`](../architecture.md) and treated as API: renaming one
breaks dashboards, so it is a breaking change.

## Consequences

**Good.** The core stays dependency-free and users who want no telemetry carry
none. Users on a non-OTel stack — statsd, Prometheus directly, structured logs
— write a five-line hook instead of adopting OTel to get anything. The privacy
property is enforced by the type system rather than by review. And the pattern
matches skyl's, so a user of both learns it once.

**Bad.** Hooks are lower-level than most users want; getting from a `Hook` to a
useful dashboard is work, and the OTel module is only useful to OTel users.
Synchronous execution means a badly-written hook degrades extraction, and
somebody will write one that does I/O. A closed `Event` struct means every new
observable quantity is a struct field and therefore an API addition — the
inflexibility is the point, but it is still inflexibility. And there is no
sampling or filtering: every event fires, and a thousand-page document produces
a thousand OCR events whether or not anyone wants them.

**Neutral.** `ovrin/otel` versions separately, so an OTel major version bump is
one module's problem rather than everyone's.

## Alternatives considered

- **OpenTelemetry in the core.** Rejected: breaks rule
  [§4.1](../rules.md#4-dependencies) and imposes a large graph on users who do
  not want it.
- **`log/slog` in the core.** Rejected: logging is not metrics, structured logs
  are not traces, and it invites document content into log lines — the exact
  failure this decision is shaped to prevent.
- **An `Observer` interface with a method per stage.** Rejected: every new
  stage breaks every implementation, and callers who care about one stage must
  implement seven methods.
- **Asynchronous hooks on an internal goroutine.** Rejected: hides ordering,
  can drop events, and leaks a goroutine per client. Callers who want async can
  make their own hook async in one line.
