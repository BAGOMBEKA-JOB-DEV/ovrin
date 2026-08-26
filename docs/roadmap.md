# Roadmap

Ordered by what blocks adoption, not by what is interesting to build.

**Nothing below is implemented.** The repository contains the design. This
document says what gets built and in what order.

---

## Phase 0 — Prerequisites

Work outside this repository that later phases depend on.

- [ ] **Rename the GitHub repository** from `vellum` to `ovrin`. Until this is
      done the module path does not resolve
      ([ADR-0001](adr/0001-name-and-module-path.md)).
- [x] ~~**Tag skyl v0.2.0**~~ — not needed. `Request.ResponseFormat` is already
      in skyl `v0.1.0`; the claim that it was not was an error, corrected in
      [ADR-0008](adr/0008-skyl-is-an-adapter.md). `model/skyl` requires the real
      tag.
- [ ] **Seed the evaluation corpus** with five redistributable documents per
      category. Everything after this is unmeasurable without it
      ([ADR-0023](adr/0023-evaluation-corpus.md)).

---

## v0.1 — Prove the idea

The narrowest thing that is genuinely useful and genuinely honest about
confidence.

**Inputs.** PDF with a text layer. PNG, JPEG, TIFF. Scanned PDF only via cloud
OCR providers that accept a PDF directly.

**Core**
- [x] Format detection by content ([`pipeline.md`](pipeline.md) stage 1)
- [x] PDF text-layer extraction with positions, in-tree
      ([ADR-0011](adr/0011-pdf-text-extraction.md)) — the largest single piece
      of work here by a wide margin
- [x] Resource limits, enforced before allocation
      ([ADR-0020](adr/0020-resource-limits.md))
- [x] Offset-preserving normalisation ([ADR-0015](adr/0015-provenance.md))
- [x] Schema reflection and the tag grammar
      ([ADR-0005](adr/0005-schemas-are-go-structs.md),
      [ADR-0006](adr/0006-tag-grammar.md))
- [x] Prompt construction with structural separation
      ([ADR-0017](adr/0017-untrusted-document-content.md))
- [x] The three seams ([ADR-0007](adr/0007-model-seam.md),
      [ADR-0009](adr/0009-ocr-seam.md), [ADR-0010](adr/0010-no-cgo-in-core.md))
- [x] Validation: `required`, type checking, `min`, `max`, `format`, `enum`
      ([ADR-0006](adr/0006-tag-grammar.md), [ADR-0029](adr/0029-v01-scope-corrected.md))
- [x] Nested structs and slices, to full depth
- [x] Cross-field validation rules, and the `cross_field` signal
- [x] `Explain` and its `String()` rendering
      ([ADR-0016](adr/0016-explain-returns-data.md))
- [x] Grounding, and the `grounding` signal
- [x] `Result[T]`, `FieldResult`, partial results
      ([ADR-0004](adr/0004-partial-results.md))
- [x] Confidence scoring over the signals available without a second reading
- [x] Errors: sentinels and `*Error` ([ADR-0019](adr/0019-error-model.md))
- [x] Hooks ([ADR-0021](adr/0021-observability.md))

**Modules**
- [x] `model/skyl`
- [x] `ocr/google`

**Quality**
- [x] The offline sandbox ([ADR-0022](adr/0022-offline-testing.md))
- [x] The shared adapter contract suite
- [x] Evaluation harness, running against the seeded corpus

**Explicitly not in v0.1**, and version-marked wherever the guides show them
([ADR-0029](adr/0029-v01-scope-corrected.md)): two readings and
cross-validation (v0.3), provider fallback chains (v0.2), local rasterising
(v0.2), the `otel` module (v0.2), WebP input (v0.2).

---

## v0.2 — Make it usable on real documents

- [x] **Local rasterising** — `render/pdfium`, PDFium under Wazero. This is
      what makes offline and air-gapped scanned-document processing possible
      ([ADR-0010](adr/0010-no-cgo-in-core.md)).
- [x] `ocr/tesseract` — and it needs **no cgo**: Tesseract compiled to
      WebAssembly under a pure-Go runtime, so the whole offline path
      cross-compiles
- [ ] `ocr/textract`, `ocr/azure`
- [x] Date-order handling for ambiguous dates
- [x] Per-field provenance with bounding boxes, for every reading that
      supplies geometry — vision supplies none, by its nature
- [x] Provider fallback chains ([ADR-0018](adr/0018-fallback-is-a-decorator.md))
- [x] `otel` module

---

## v0.3 — Harder documents

- [ ] DOCX, XLSX, CSV sources
- [ ] Multi-page documents with per-page acquisition paths
- [ ] Layout preservation — tables, columns, key-value regions
- [ ] Two readings and cross-validation
      ([ADR-0014](adr/0014-cross-validation.md))
- [ ] Extraction retry on schema-invalid output
- [ ] Suspicious-content detection
      ([ADR-0017](adr/0017-untrusted-document-content.md) mitigation 4)

---

## v1.0 — Trustworthy

v1.0 is gated on evidence, not on features. All four conditions from
[ADR-0024](adr/0024-versioning-and-stability.md) must hold.

- [ ] **Calibrated confidence** — published expected calibration error and
      accuracy within confidence bands. Until this lands, confidence is
      documented as a ranking signal and not a probability
      ([ADR-0013](adr/0013-multi-signal-confidence.md)).
- [ ] Evaluation corpus populated across every category and difficulty level,
      with committed reports across at least two provider generations
- [ ] At least one production deployment that is not the maintainer's, with its
      feedback incorporated
- [ ] Batch processing, and streaming for documents that do not fit in memory
- [ ] Circuit breaking in provider chains
- [ ] Benchmarks published, per stage
- [ ] No known API change we would want to make

---

## Beyond v1.0

Wanted, unscheduled, and each needing an ADR before code.

| Item | Note |
|---|---|
| CLI — `ovrin extract invoice.pdf --schema ./invoice.go` | Needs a schema representation outside Go source, or Go source parsing |
| An optional HTTP service | Its own module, following skyl's gateway pattern |
| Webhooks — `document.processed`, `document.review_required` | Belongs to the service, not the library |
| Runtime schemas for customer-defined forms | Returns `map[string]any`; a secondary entry point, never the primary |
| A `Validator` interface for user-defined rules | Blocked on evidence the closed vocabulary is too small |
| Three or more readings with majority voting | Blocked on two readings being measured first |
| Document classification | A different problem; possibly a different library |
| Encrypted PDF support | Blocked on demand |
| A trained confidence calibrator | Blocked on a large labelled corpus |

---

## Deferred, deliberately

Things that look like obvious features and are not being built.

**A review interface.** Ovrin produces everything one needs and builds none of
it. Every organisation's review workflow differs, and a library that ships a UI
ships an opinion about a workflow it cannot see.

**A hosted API.** No endpoint, no key from us. Ovrin is a library you run.

**A better OCR engine.** Ovrin orchestrates OCR. Writing a competitive engine
is somebody else's decade.

**Automatic resolution of disagreements.** When two readings differ, ovrin
records both and flags the field. Silently preferring one is exactly the
failure the feature exists to prevent
([ADR-0014](adr/0014-cross-validation.md)).

**Silent sanitising of suspicious content.** Detected and reported, never
stripped ([ADR-0017](adr/0017-untrusted-document-content.md)).

**A model self-confidence field.** Well-formed numbers, uncorrelated with
correctness ([ADR-0013](adr/0013-multi-signal-confidence.md)).

**Any AGPL dependency**, including the best-in-class options in two categories
([ADR-0025](adr/0025-licence-policy.md)).

---

## What this is not a roadmap for

Accuracy improvements are not scheduled here, because a schedule implies they
can be planned. They come from the evaluation corpus telling us where we are
wrong. Until the corpus exists, any promise about accuracy would be a guess
dressed as a plan.
