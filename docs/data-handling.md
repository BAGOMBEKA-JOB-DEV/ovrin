# Data handling

Documents are medical records, national identity papers, bank statements and
benefit applications. This document states exactly what ovrin does with their
contents, what leaves your process, and to whom.

**Contents:** [What ovrin never does](#what-ovrin-never-does) ·
[What leaves the process](#what-leaves-the-process) ·
[Configurations](#configurations) · [In memory](#in-memory) ·
[Errors, logs and traces](#errors-logs-and-traces) ·
[Regulated deployments](#regulated-deployments)

---

## What ovrin never does

These are structural properties, not policies, which means they cannot be
switched off or violated by a future patch without the violation being obvious.

**Never writes a document to disk.** No cache, no temporary file, no spool. The
document exists in memory for the duration of the call.

**Never fetches anything a document references** (rule
[§7.4](rules.md#7-untrusted-input)). No URL is followed, no remote font or
image loaded, no external entity resolved.

**Never puts document content in an error** (rule
[§2.5](rules.md#2-errors)). Errors carry the operation, page, field and
provider. Enforced by a test in the shared adapter suite.

**Never puts document content in an event, trace or metric** (rule
[§7.5](rules.md#7-untrusted-input)). The `Event` struct has **no field capable
of carrying a value** — no `map[string]any`, no `Raw`, no free-text note
([ADR-0021](adr/0021-observability.md)). This is enforced by the type, because
a guideline would be violated the first time it was convenient.

**Never phones home.** No telemetry to us, no version check, no analytics.
Ovrin makes exactly the network calls your configured providers require and no
others.

**Never persists anything between calls.** No state, no learning, no history.

---

## What leaves the process

Everything that leaves does so through a provider you configured. There are no
other egress paths.

| Component | Sends | To |
|---|---|---|
| `Model` adapter | normalised text, and page images when vision is used | the model provider you configured |
| `OCR` adapter | page images, or the PDF itself for providers that accept one | the OCR provider you configured |
| `Renderer` | nothing | — runs locally |
| Core | nothing | — |
| Hooks | field names, page numbers, counts, durations, confidences | wherever you send them |

**The model sees the whole normalised document**, not just the fields you
asked for. Extraction requires context; a model cannot find the total without
seeing the invoice. If a document contains data that must not reach a
third-party model, it must not be sent to a third-party model — redact before
processing, or use a local configuration.

---

## Configurations

Three postures, each with a different data-egress profile.

### Fully local — nothing leaves

```go
client := ovrin.New(
    ovrin.WithModel(localModel),          // Ollama, vLLM, llama.cpp
    ovrin.WithRenderer(pdfium.New()),     // Wazero, no network
    ovrin.WithOCR(tesseract.New()),       // local
)
```

No document content crosses a process boundary you do not control. Suitable for
air-gapped deployments and data-residency-constrained environments. Costs
accuracy relative to frontier models, and that is the trade.

### Text-layer only — nothing leaves except to the model

```go
client := ovrin.New(ovrin.WithModel(model))
```

No OCR provider, no renderer. PDFs with a text layer are read entirely locally;
only the extracted text goes to the model. Scanned documents fail with
`ErrNoProvider` rather than silently going somewhere.

### Cloud — content goes to the providers you chose

```go
client := ovrin.New(
    ovrin.WithModel(ovrinskyl.New(skyl.New(openai.New(key)))),
    ovrin.WithOCR(google.New(creds)),
)
```

Page images or the PDF go to Google; normalised text goes to OpenAI. Both are
third parties with their own retention and training policies, which are theirs
to state and yours to verify. Ovrin does not summarise or vouch for them.

**A fallback chain widens the egress set.** `OCRChain(google, textract,
tesseract)` means content may reach Google *or* AWS depending on availability.
The provider that served each reading is recorded in `Metadata` and in every
`Provenance`, so a result always says where its content went
([ADR-0018](adr/0018-fallback-is-a-decorator.md)).

---

## In memory

| Data | Lifetime |
|---|---|
| Source bytes | the `Extract` call |
| Page images | until the reading that needs them completes |
| Normalised text | the `Extract` call |
| Extracted values | returned to you in `Result`; ovrin keeps no copy |
| Provenance spans | offsets only, not the text they point at |

`Result` is yours. Ovrin holds no reference to it after returning.

Memory is bounded by the limits in
[ADR-0020](adr/0020-resource-limits.md) — a document cannot cause unbounded
allocation, which is a resource property and also a data-minimisation one.

Ovrin does not zero buffers after use. Go's garbage collector reclaims them on
its own schedule, so document bytes may remain in freed heap memory for a time.
A deployment where that matters needs process-level controls — a dedicated
process per document, memory encryption, disabled core dumps — and ovrin cannot
provide them.

---

## Errors, logs and traces

The design intent: **you cannot accidentally leak document content through
ovrin's observability.**

```go
type Event struct {
    Op         string
    Provider   string
    Page       int
    Attempt    int
    Duration   time.Duration
    Err        error
    Bytes      int64
    Pages      int
    Fields     int          // a count, not the names
    Usage      Usage
    Confidence float64
    Review     bool
}
```

There is no field a value could go in. That is deliberate and it is the point
([ADR-0021](adr/0021-observability.md)).

`ReviewReason` carries a **field name** and a cause, not a value:

```text
total — value not found in source; may be inferred
```

not

```text
total — extracted 2500.00 but source says 25,000
```

**What you can still leak.** You hold `Result.Data`, and logging it logs
document content. That is yours to control, and ovrin cannot prevent it. When
building review queues and audit records, decide deliberately what to store —
see [`explainability.md`](explainability.md#audit).

---

## Regulated deployments

Questions a data protection officer will ask, with the answers.

**Is data sent outside our infrastructure?** Only to providers you configure.
The fully-local configuration sends nothing.

**Is data retained by ovrin?** No. Nothing is written to disk and nothing
persists between calls.

**Is data used for training?** Not by ovrin. Whether your providers do is their
policy; check it, because some default to yes.

**Can we prove what happened to a document?** Yes — `Metadata` records the
readings taken and providers used, and every field's `Provenance` records which
provider produced it ([`explainability.md`](explainability.md)).

**Can we run it air-gapped?** Yes, from v0.2, with a local model, `render/pdfium`
and `ocr/tesseract`. v0.1 needs a cloud OCR provider for scanned documents
([`roadmap.md`](roadmap.md)).

**What happens to a document that fails processing?** Nothing persists. The
error names the operation and page, never content.

**Who can see our credentials?** Adapters read credentials only from what you
pass them; ovrin never reads the environment itself (rule
[§6.4](rules.md#6-adapters)). Credentials never appear in errors, events or
traces.

## See also

- [`threat-model.md`](threat-model.md) — T4 covers exfiltration in full
- [`explainability.md`](explainability.md) — what to store for audit
- [ADR-0021](adr/0021-observability.md) — why `Event` has no value field
