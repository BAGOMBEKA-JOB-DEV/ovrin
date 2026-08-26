# Documentation

Ovrin turns documents into typed Go values. This directory holds the design.

**The repository currently contains documentation only.** Implementation has
not started; see the [status section](../README.md#status).

## Guides

| Document | Read it when |
|---|---|
| [idea.md](idea.md) | You want to know what problem this solves and why it exists |
| [getting-started.md](getting-started.md) | You want to extract something |
| [schema.md](schema.md) | You are writing a struct and need the tag grammar |
| [confidence.md](confidence.md) | You are deciding what to do with a score |
| [explainability.md](explainability.md) | You are building review or audit on top |
| [architecture.md](architecture.md) | You want to know how it fits together |
| [pipeline.md](pipeline.md) | You want to know what happens to a document |
| [providers.md](providers.md) | You are writing an adapter |
| [feature-matrix.md](feature-matrix.md) | You need to know what a provider silently ignores |
| [data-handling.md](data-handling.md) | You need to know what leaves your process |
| [threat-model.md](threat-model.md) | You are processing documents from the public |
| [evaluation.md](evaluation.md) | You want to measure a change, or trust a number |
| [validating.md](validating.md) | You are assessing ovrin before adopting it |
| [rules.md](rules.md) | You are contributing code |
| [roadmap.md](roadmap.md) | You want to know what is coming |
| [project-plan.md](project-plan.md) | You want current status and what is blocked |
| [glossary.md](glossary.md) | A word here is being used in a specific way |

## Architecture decision records

Twenty-five decisions, each with the alternatives that lost and the costs that
were accepted. The [index](adr/README.md) has the full list; these are the ones
that shape the most.

| ADR | Decision |
|---|---|
| [0002](adr/0002-flat-package-layout.md) | A flat root package with the implementation in `internal/` |
| [0004](adr/0004-partial-results.md) | `Result[T]` carries partial data |
| [0005](adr/0005-schemas-are-go-structs.md) | Schemas are Go structs read by reflection |
| [0007](adr/0007-model-seam.md) | The `Model` seam takes a JSON schema, not a conversation |
| [0008](adr/0008-skyl-is-an-adapter.md) | Skyl is an adapter, not a core dependency |
| [0010](adr/0010-no-cgo-in-core.md) | No cgo in the core; rasterising runs PDFium under Wazero |
| [0012](adr/0012-text-first-ocr-on-demand.md) | Text first, OCR on demand, vision as a distinct reading |
| [0013](adr/0013-multi-signal-confidence.md) | Confidence is multi-signal; logprobs are not the source |
| [0014](adr/0014-cross-validation.md) | Two readings, and their disagreement is a result |
| [0017](adr/0017-untrusted-document-content.md) | Document content is untrusted input |

## Evaluating ovrin

| Question | Where |
|---|---|
| Is it accurate? | [evaluation.md](evaluation.md) — and no figure is claimed that the harness cannot reproduce |
| What does it send to third parties? | [data-handling.md](data-handling.md) |
| Is it safe against hostile documents? | [threat-model.md](threat-model.md) |
| Will it break my build? | Zero dependencies, no cgo — [architecture.md](architecture.md) |
| Will the API change? | Yes, before v1 — [ADR-0024](adr/0024-versioning-and-stability.md) |
| What does a provider not support? | [feature-matrix.md](feature-matrix.md) |
| Can one person maintain this? | [../MAINTAINERS.md](../MAINTAINERS.md) says the bus factor out loud |
