# Observability

Ovrin's core emits **hooks** and depends on nothing. The `ovrin/otel` module
turns those hooks into OpenTelemetry spans and metrics
([ADR-0021](adr/0021-observability.md)).

> **`ovrin/otel` is v0.2.** Hooks work in v0.1.

The names below are **API**. Renaming one breaks somebody's dashboard, so it is
a breaking change and goes in the changelog with a migration note.

---

## Hooks

```go
c := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithHook(func(ctx context.Context, ev ovrin.Event) {
        log.Printf("%s provider=%s page=%d %s err=%v",
            ev.Op, ev.Provider, ev.Page, ev.Duration, ev.Err)
    }),
)
```

Hooks run **synchronously on the calling goroutine**. A hook that blocks slows
the extraction, and a hook that does I/O will. That is the caller's
responsibility, and making it asynchronous is one line in your own hook.

One event fires per stage per page, plus one per stage for whole-document
stages. A thousand-page document produces a thousand `ocr` events; there is no
sampling. Validation and grounding are the exception: they run inside the
assembly of the `Result` and emit nothing, for the reason given under
[Spans](#spans).

## What an event may carry

`Event` has **no field capable of holding document content** — no
`map[string]any`, no `Raw`, no free-text note. This is structural, not a
guideline, because a guideline gets violated the first time it is convenient
(rule [§7.5](rules.md#7-untrusted-input)).

Field *counts* are carried. Field *names* and *values* are not. If you need
per-field detail, it is on the `Result`, which is yours.

## Spans

One span per stage that emits an event, nested under a root span for the
extraction.

| Span | Emitted by |
|---|---|
| `ovrin.extract` | the root span for one `Extract` call |
| `ovrin.detect` | format detection |
| `ovrin.acquire` | content acquisition, parent of render/ocr |
| `ovrin.render` | one page rasterised |
| `ovrin.ocr` | one page recognised |
| `ovrin.normalise` | text normalisation |
| `ovrin.schema` | schema reflection (absent when cached) |
| `ovrin.prompt` | instruction construction |
| `ovrin.generate` | the model call |
| `ovrin.validate` | rule checking across every field |
| `ovrin.ground` | locating each value back in the source |
| `ovrin.score` | reserved — see below |

Span names match the `Op` constants ([ADR-0027](adr/0027-twelve-sentinels-and-one-op-vocabulary.md))
and the stage names in [`pipeline.md`](pipeline.md), so one vocabulary covers
traces, errors and the documentation.

**Validation and grounding do not have a pass of their own.** They run per
field, interleaved, while the `Result` is assembled — a field is converted,
checked against its rules, then looked for in the source, before the next
field is touched. The two events are emitted once, after the walk, and their
durations therefore overlap and both cover the same span of wall time. Read
them as "this stage happened and here is roughly what it cost", not as two
disjoint slices of a timeline.

They are emitted at all because the `Op` vocabulary is one vocabulary. An `Op`
that can appear on an `Error` and never on an `Event` makes a trace and a
failure describe the same extraction in two different languages, and a reader
comparing them has to know which names are real.

**`ovrin.score` is the exception.** Scoring does emit an event, but it carries
the wall time of the whole `Extract` call along with the aggregate confidence
and the review flag; labelling that `ovrin.score` would report the entire
extraction as time spent scoring, so the module makes it the root
`ovrin.extract` span instead.

### Span attributes

| Attribute | Type | Notes |
|---|---|---|
| `ovrin.op` | string | the `Op` constant |
| `ovrin.provider` | string | adapter name, when one served the stage |
| `ovrin.page` | int | 1-based; absent for whole-document stages |
| `ovrin.attempt` | int | 1 for the first try |
| `ovrin.kind` | string | the `Kind` constant |
| `ovrin.pages` | int | page count |
| `ovrin.bytes` | int64 | bytes read or produced |
| `ovrin.fields` | int | a **count** |
| `ovrin.confidence` | double | aggregate, on the root span only |
| `ovrin.review` | bool | root span only |
| `ovrin.tokens.input` / `.output` | int | from `Usage` |
| `ovrin.page_units` | int | what OCR providers bill |

There is deliberately no attribute for a field name or a field value.

## Metrics

| Metric | Instrument | Unit | Attributes |
|---|---|---|---|
| `ovrin.extractions` | counter | `{extraction}` | `kind`, `outcome` |
| `ovrin.extraction.duration` | histogram | `s` | `kind` |
| `ovrin.stage.duration` | histogram | `s` | `op`, `provider` |
| `ovrin.pages.processed` | counter | `{page}` | `reading` |
| `ovrin.confidence` | histogram | `1` | `kind` |
| `ovrin.reviews` | counter | `{result}` | `reason` |
| `ovrin.tokens` | counter | `{token}` | `provider`, `direction` |
| `ovrin.page_units` | counter | `{page}` | `provider` |
| `ovrin.errors` | counter | `{error}` | `op`, `kind`, `provider` |

`outcome` is `ok`, `review` or `error`. There is deliberately no `invalid`: an
`Event` has no field reporting whether validation passed, so an extraction that
came back invalid is indistinguishable from one that came back clean, and
reporting a wrong outcome would be worse than reporting a coarser one.

`reason` on `ovrin.reviews` is always `unknown`. The count is real — an `Event`
carries a `Review` bool — but the breakdown is not available: the
`ReviewReason` causes live on the `Result`, and the `Result` never crosses the
hook, which is the only thing this module sees. Guessing a cause would be
fabrication, and putting the field name there instead would ship a vendor's
index exactly what [ADR-0021](adr/0021-observability.md) exists to keep out of
it. If you need the breakdown, it is on the `Result`, which is yours.

`ovrin.confidence` is the one worth an alert: a distribution that shifts
downward means the corpus changed, the provider changed, or something upstream
broke.

## Using it

```go
c := ovrin.New(
    ovrin.WithModel(model),
    ovrinotel.Option(tracerProvider, meterProvider),
)
```

`ovrinotel.Option` returns an `ovrin.Option`. It is not called `Hook` because
the core already has a type by that name.

## See also

- [ADR-0021](adr/0021-observability.md) — why hooks in the core and OTel outside
- [`data-handling.md`](data-handling.md) — what leaves the process
- [`pipeline.md`](pipeline.md) — the stages these names come from
