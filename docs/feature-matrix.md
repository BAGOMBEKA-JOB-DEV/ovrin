# Feature matrix

What each provider supports, what it rejects, and — the column that matters —
what it **silently ignores**.

A matrix that lists only the green cells is exactly the thing rule
[§6.5](rules.md#6-adapters) rejects. Silent degradation is the failure mode
this document exists to prevent: an adapter that quietly produces a worse
answer than the caller believes they asked for.

> **Nothing here is implemented yet.** This is the specification the adapters
> will be built and tested against. Every ⚠️ cell is a behaviour the shared
> contract suite must assert.

**Legend**

| | |
|---|---|
| ✅ | Supported and mapped |
| ⛔ | Rejected with `ErrUnsupported`, naming what could not be done |
| ⚠️ | **Silently ignored or degraded** — the request succeeds, but not as asked |
| — | Not applicable |

Rule [§6.1](rules.md#6-adapters) says an adapter must never silently drop data.
Every ⚠️ below is therefore either a defect to fix or a documented, deliberate
degradation with a reason given. There should be very few, and each one should
be uncomfortable to look at.

---

## Model adapters

| Capability | `model/skyl` |
|---|---|
| Schema-constrained JSON output | ✅ via `Request.ResponseFormat` |
| Text content | ✅ |
| Page images (vision) | ✅ via `skyl.Image`, raw bytes |
| Multiple images per request | ✅ |
| PDF sent natively to the provider | ⛔ skyl's `Part` set is closed and has no PDF member ([ADR-0008](adr/0008-skyl-is-an-adapter.md)) |
| Token usage reporting | ✅ |
| Reasoning-token accounting | ⚠️ skyl does not surface reasoning tokens; on Gemini, output tokens under-report |
| Cache-read / cache-write accounting | ⚠️ available on skyl's `Usage`, not yet mapped onto ovrin's |
| Retry on transient failure | ✅ inherited from `*skyl.Client` |
| Context cancellation | ✅ |
| Streaming | — ovrin has nothing to stream; a partial JSON object is useless to a validator |

**Schema portability is not guaranteed.** Providers differ in the JSON Schema
subset they accept — `$ref`, `oneOf`, `additionalProperties` and unbounded
integers are all handled inconsistently. Ovrin emits a conservative,
fully-expanded schema targeting the narrowest common subset. A schema that a
provider rejects surfaces as `ErrBadRequest` naming the construct, never as a
silently relaxed constraint.

---

## OCR adapters

Only `google` and `tesseract` exist. `textract` and `azure` are v0.2 items not
yet started, and are listed so the shape of the seam is visible — every cell in
their columns is a plan, not a measurement.

| Capability | `google` | `tesseract` | `textract` | `azure` |
|---|---|---|---|---|
| **Built** | ✅ | ✅ | ⛔ not started | ⛔ not started |
| Word text | ✅ | ✅ | — | — |
| Word bounding boxes | ✅ | ✅ | — | — |
| Per-word confidence | ✅ | ✅ | — | — |
| Reading order | ⚠️ blocks and paragraphs are sorted; lines within a block keep the provider's order, so a two-column page is approximate | ✅ | — | — |
| Accepts a PDF whole (no local renderer) | ✅ **PDF only** — TIFF and GIF are refused with `ErrUnsupported`, because Vision reports their geometry in pixels with no resolution and it could not be converted to points | ⛔ | — | — |
| Pages per document call | ⚠️ **5 maximum.** Google's synchronous endpoint truncates silently beyond that, so a document with more is refused rather than half-read | — | — | — |
| Language hints | ✅ | ✅ | — | — |
| Handwriting | ✅ | ⚠️ poor; Tesseract is built for print | — | — |
| Table structure | ⚠️ detected by the provider, **discarded** by ovrin's `Recognition` | ⛔ | — | — |
| Key-value pairs | ⚠️ same | ⛔ | — | — |
| Per-symbol geometry, block types, per-block languages | ⚠️ **silently ignored** — reachable only through `Recognition.Raw` | ⛔ | — | — |
| Page-unit billing | ⚠️ not reported; `Recognition` gained a `Usage` field in v0.2 and the adapter does not yet fill it | ⚠️ same | — | — |
| Provider-side entity extraction | ⛔ deliberately unused — see below | ⛔ | — | — |
| Runs offline | ⛔ | ✅ | ⛔ | ⛔ |
| Requires cgo [^cgo] | no | see the module's own documentation | — | — |
| Module dependencies | **none but `ovrin`** — REST over `net/http`, no Google SDK | — | — | — |

**Authentication is where standard-library-only costs something.** `ocr/google`
takes an API key directly, and a service account through
`WithTokenSource(func(ctx) (string, error))` — so ADC, workload identity and
the rest work, but `golang.org/x/oauth2/google` lands in **your** `go.mod`
rather than the adapter's. Reimplementing service-account JWT signing inside an
OCR adapter would be security-relevant code in the wrong place.

**Google reports per-page failures inside an HTTP 200.** An adapter classifying
on status alone would report a permission failure as a blank scan. Both the
status code and the per-response gRPC code are classified.

**The table and key-value rows are the honest ones.** Document AI, Textract and
Azure all return richer structure than ovrin's `Recognition` carries, and
reducing it to words and lines discards work you paid for
([ADR-0009](adr/0009-ocr-seam.md)). It is reachable through `Recognition.Raw`
by type assertion, which is not a real answer. Layout preservation is v0.3.

**Provider-side extraction is deliberately unused.** Document AI will return an
invoice object directly. Ovrin uses these providers for OCR only, because
mixing two extraction systems with different accuracy profiles makes confidence
uninterpretable.

---

## Renderers

| Capability | `render/pdfium` | `render/pdfiumcgo` |
|---|---|---|
| Rasterise a page | ✅ | ✅ |
| Requires cgo [^cgo] | no — Wazero | yes |
| Cross-compiles | yes | no |
| Static binary | yes | no |
| Speed | materially slower than native | native |
| Binary size | embeds a large WASM blob | small |
| DPI control | ✅ | ✅ |
| Encrypted PDFs | ⛔ | ⛔ |

`render/pdfium` is the recommended default and the one the documentation points
at ([ADR-0010](adr/0010-no-cgo-in-core.md)). The cgo variant exists for
throughput-bound deployments that accept the toolchain.

---

## Sources

| Source | v0.1 | v0.2 | v0.3 | Note |
|---|---|---|---|---|
| PDF with a text layer | ✅ | ✅ | ✅ | The common case; exact and nearly free |
| PDF, scanned, cloud OCR | ✅ | ✅ | ✅ | Provider rasterises server-side |
| PDF, scanned, offline | ⛔ | ✅ | ✅ | Needs `render/pdfium` |
| PDF, encrypted | ⛔ | ⛔ | ⛔ | `ErrEncrypted`, naming the encryption |
| PNG, JPEG | ✅ | ✅ | ✅ | |
| TIFF | ✅ | ✅ | ✅ | Multi-page TIFF from v0.2 |
| WebP | ⛔ | ✅ | ✅ | |
| DOCX, XLSX, CSV | ⛔ | ⛔ | ✅ | |
| HTML, email | ⛔ | ⛔ | ⛔ | Not planned |

---

## Core capabilities by version

| Capability | v0.1 | v0.2 | v0.3 | v1.0 |
|---|---|---|---|---|
| Typed extraction | ✅ | ✅ | ✅ | ✅ |
| Nested structs, slices, to full depth | ✅ | ✅ | ✅ | ✅ |
| `required`, `min`, `max` | ✅ | ✅ | ✅ | ✅ |
| `format`, `enum` | ✅ | ✅ | ✅ | ✅ |
| Cross-field rules | ✅ | ✅ | ✅ | ✅ |
| Grounding | ✅ | ✅ | ✅ | ✅ |
| Provenance with boxes | ⚠️ page only | ✅ | ✅ | ✅ |
| `Explain` | ✅ | ✅ | ✅ | ✅ |
| Two readings, cross-validation | ⛔ | ⛔ | ✅ | ✅ |
| Provider fallback | ⛔ | ✅ | ✅ | ✅ |
| Circuit breaking | ⛔ | ⛔ | ⛔ | ✅ |
| Hooks | ✅ | ✅ | ✅ | ✅ |
| OpenTelemetry | ⛔ | ✅ | ✅ | ✅ |
| Calibrated confidence | ⛔ | ⛔ | ⛔ | ✅ |
| Batch, streaming large documents | ⛔ | ⛔ | ⛔ | ✅ |

The ⚠️ in v0.1 provenance is the one to watch: page-level provenance is enough
for grounding but not enough to highlight a region, so review interfaces built
against v0.1 will need changing at v0.2.

[^cgo]: Plain yes/no, not a ⚠️. Requiring cgo is a documented property of a
module, not a silent degradation — and every ⚠️ in this document is
contractually a behaviour the shared contract suite asserts, which is not
something that can be written for "needs a C toolchain".
