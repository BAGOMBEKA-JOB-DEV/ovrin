# Architecture decision records

A record of the decisions that shape ovrin, why they were made, and what they
cost. They are the answer to "why is it like this?" — and, more often, to "why
isn't it like that?"

## How we use them

**One decision per record.** A record that settles three things cannot be
superseded cleanly when one of them turns out wrong.

**Never edit an accepted record to change its decision.** Supersede it with a
new one and mark the old one `Superseded by ADR-XXXX`. The history is the point.

**A code listing inside a record is kept current.** Editing a declaration so
it still matches the code is not editing the decision — a listing that has gone
stale misleads a reader about the type, which is the opposite of what the
listing is for. The `mirror` fences make this mechanical: change the type and
the build tells you which records to update. What is never edited is the
decision itself.

**Numbers are permanent.** Other documents, doc comments and the pull request
template cite them. A renumbered ADR breaks every citation.

**Alternatives considered is not optional.** A record listing only the chosen
option is a press release.

**Consequences include the bad ones.** An ADR that lists no downsides has not
finished thinking; every real decision costs something, so say what.

## Format

```markdown
# ADR-NNNN: Short title, stated as a fact

**Status:** Accepted · **Date:** YYYY-MM-DD

## Context
The situation forcing a choice. Facts and constraints, not preferences.

## Decision
What we are doing, in the present tense.

## Consequences

**Good.** ...

**Bad.** ...

## Alternatives considered

- **Option.** Rejected: why it was plausible, and why it lost.
```

Files are `NNNN-kebab-case-title.md`. Status is one of `Proposed`, `Accepted`,
`Rejected`, `Deprecated`, `Superseded by ADR-NNNN`.

## The records

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-name-and-module-path.md) | The project is named ovrin | Accepted |
| [0002](0002-flat-package-layout.md) | A flat root package with the implementation in `internal/` | Accepted |
| [0003](0003-go-floor-and-generics.md) | Go 1.22 floor, and `Extract[T]` is a package-level function | Accepted |
| [0004](0004-partial-results.md) | `Result[T]` carries partial data | Accepted |
| [0005](0005-schemas-are-go-structs.md) | Schemas are Go structs read by reflection | Accepted |
| [0006](0006-tag-grammar.md) | The `ovrin` struct tag grammar | Accepted |
| [0007](0007-model-seam.md) | The `Model` seam takes a JSON schema, not a conversation | Accepted |
| [0008](0008-skyl-is-an-adapter.md) | Skyl is an adapter in its own module, not a core dependency | Accepted |
| [0009](0009-ocr-seam.md) | OCR providers are separate modules behind a two-method seam | Accepted |
| [0010](0010-no-cgo-in-core.md) | No cgo in the core; rasterising runs PDFium under Wazero | Accepted |
| [0011](0011-pdf-text-extraction.md) | PDF text-layer extraction is written in-tree | Accepted |
| [0012](0012-text-first-ocr-on-demand.md) | Text first, OCR on demand, vision as a distinct reading | Accepted |
| [0013](0013-multi-signal-confidence.md) | Confidence is multi-signal; logprobs are not the source | Accepted |
| [0014](0014-cross-validation.md) | Two readings, and their disagreement is a result | Accepted |
| [0015](0015-provenance.md) | Every field carries where it came from | Accepted |
| [0016](0016-explain-returns-data.md) | `Explain` returns a data structure, not formatted text | Accepted |
| [0017](0017-untrusted-document-content.md) | Document content is untrusted input | Accepted |
| [0018](0018-fallback-is-a-decorator.md) | Provider fallback is a decorator, not core behaviour | Accepted |
| [0019](0019-error-model.md) | Sentinels plus a typed `*Error` with multi-error `Unwrap` | Accepted |
| [0020](0020-resource-limits.md) | Every limit has a finite default | Accepted |
| [0021](0021-observability.md) | Hooks in the core, OpenTelemetry in its own module | Accepted |
| [0022](0022-offline-testing.md) | No network in unit tests; an offline sandbox serves the providers | Accepted |
| [0023](0023-evaluation-corpus.md) | An evaluation corpus lives in the repository | Accepted |
| [0024](0024-versioning-and-stability.md) | Pre-v1 stability policy and per-module versioning | Accepted |
| [0025](0025-licence-policy.md) | Apache-2.0, and no AGPL dependencies | Accepted |
| [0026](0026-extract-takes-per-call-options.md) | `Extract` takes per-call options | Accepted |
| [0027](0027-twelve-sentinels-and-one-op-vocabulary.md) | A twelfth sentinel, and one `Op` vocabulary | Accepted |
| [0028](0028-reading-and-readingmode.md) | `Reading` and `ReadingMode` are different types | Accepted |
| [0029](0029-v01-scope-corrected.md) | The v0.1 scope, corrected | Accepted |
| [0030](0030-an-internal-failure-sentinel.md) | A thirteenth sentinel, for ovrin's own failures | Accepted |

## Open questions

Decisions we know we will have to make, deliberately not made yet.

| Question | Blocked on |
|---|---|
| A `Validator` interface for user-defined rules | Evidence that the closed vocabulary in [ADR-0006](0006-tag-grammar.md) is too small |
| Runtime schemas, for customer-defined forms | A user with the requirement |
| Three-or-more readings with majority voting | Two readings measured on a real corpus ([ADR-0014](0014-cross-validation.md)) |
| Circuit breaking in provider chains | v1.0 ([ADR-0018](0018-fallback-is-a-decorator.md)) |
| Encrypted PDF support | Demand ([ADR-0011](0011-pdf-text-extraction.md)) |
| A trained confidence calibrator | A labelled corpus ([ADR-0023](0023-evaluation-corpus.md)) |
| DOCX, XLSX and CSV sources | v0.3 ([`../roadmap.md`](../roadmap.md)) |
| A CLI and an HTTP service | v1.0 ([`../roadmap.md`](../roadmap.md)) |
