# Feature matrix

What each provider supports, what it rejects, and — the column that matters —
what it **silently ignores**.

A matrix that lists only the green cells is exactly the thing rule
[§6.5](rules.md#6-adapters) rejects. Silent degradation is the failure mode
this document exists to prevent: an adapter that quietly produces a worse
answer than the caller believes they asked for.

> **The provider rows are measurements, not intentions.** Every adapter in
> them exists and passes the shared contract suite, and every ⚠️ cell is a
> behaviour that suite asserts. The `v1.0` column of the last table is the
> exception: that one is a plan.

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

All four exist and all four pass the shared contract suite with no assertion
skipped. Every cell below is a measurement.

| Capability | `google` | `tesseract` | `textract` | `azure` |
|---|---|---|---|---|
| **Built** | ✅ | ✅ | ✅ | ✅ |
| Word text | ✅ | ✅ | ✅ | ✅ |
| Word bounding boxes | ✅ | ✅ | ✅ converted from the 0..1 fractions Textract reports | ✅ converted from polygons, in inches or pixels per the `unit` the response declares |
| Per-word confidence | ✅ | ✅ | ✅ reported 0–100, divided | ✅ already 0..1 |
| Reading order | ⚠️ blocks and paragraphs are sorted; lines within a block keep the provider's order, so a two-column page is approximate | ✅ | ⚠️ lines sorted top-to-bottom in bands, left-to-right within a band; **a two-column page whose columns share a band interleaves**, because text detection reports no block or column structure to sort instead | ⚠️ same; the service's `paragraphs` would be the route to fixing it and are not read |
| Accepts a PDF whole (no local renderer) | ✅ **PDF only** — TIFF and GIF are refused with `ErrUnsupported`, because Vision reports their geometry in pixels with no resolution and it could not be converted to points | ⛔ | ✅ **PDF and TIFF**, but see the two rows below | ✅ **PDF only** — TIFF, BMP, HEIF and Office/HTML refused with `ErrUnsupported`, for the same geometry reason as `google` |
| Pages per document call | ⚠️ **5 maximum.** Google's synchronous endpoint truncates silently beyond that, so a document with more is refused rather than half-read | — | ⚠️ **1 inline.** More needs the S3-backed async operation via `WithDocumentLocation`; refused with `ErrUnsupported` naming both limits rather than half-read | ✅ every page returned; a short read is refused |
| Page size for a document | — | — | ⛔ **must be declared** via `WithPageSize` — Textract states geometry as a fraction of a page whose size it never reports, and a Letter default would be silently 4% wrong on every A4 document | — the service states each page's size and unit |
| Language hints | ✅ | ✅ | ⛔ the API accepts none | ✅ `WithLocale` |
| Detected language | ✅ | ✅ | ⛔ Textract reports none, so `Recognition.Language` is always empty | ✅ most confident language whose spans overlap the page |
| Page confidence | ✅ | ✅ | ⚠️ **derived** — Textract publishes none, so it is the mean of the LINE confidences (falling back to the words'), recorded in `Analysis.PageConfidenceDerived` | ⚠️ **derived** — same, the mean of the per-word confidences |
| Handwriting | ✅ | ⚠️ poor; Tesseract is built for print | ✅ read; ⚠️ the PRINTED/HANDWRITING label itself is dropped | ✅ read; ⚠️ the handwriting style is dropped |
| Table structure | ⚠️ detected by the provider, **discarded** by ovrin's `Recognition` | ⛔ | ⛔ not requested — `AnalyzeDocument` would return them and is deliberately unused | ⛔ not requested with `prebuilt-read`; ⚠️ with `WithModel("prebuilt-layout")` they are detected and discarded |
| Key-value pairs | ⚠️ same | ⛔ | ⛔ same as tables | ⚠️ same as tables |
| Per-symbol geometry, block types, per-block languages | ⚠️ **silently ignored** — reachable only through `Recognition.Raw` | ⛔ | ⚠️ block ids, the relationship graph and per-word `TextType` — same | ⚠️ paragraphs, selection marks, barcodes, formulas, styles, page rotation — same |
| Page-unit billing | ⚠️ not reported; `Recognition` gained a `Usage` field in v0.2 and the adapter does not yet fill it | ⚠️ same | ✅ **one page unit per page** | ✅ **one page unit per page** |
| Provider-side entity extraction | ⛔ deliberately unused — see below | ⛔ | ⛔ | ⛔ |
| Runs offline | ⛔ | ✅ | ⛔ | ⛔ |
| Requires cgo [^cgo] | no | see the module's own documentation | no | no |
| Module dependencies | **none but `ovrin`** — REST over `net/http`, no Google SDK | — | **none but `ovrin`** — REST over `net/http`, SigV4 over `crypto/hmac`, no AWS SDK | **none but `ovrin`** — REST over `net/http`, header auth, no Azure SDK |

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

One renderer exists.

| Capability | `render/pdfium` |
|---|---|
| Rasterise a page | ✅ |
| Requires cgo [^cgo] | no — PDFium compiled to WebAssembly, run under Wazero |
| Cross-compiles | yes |
| Static binary | yes |
| Speed | materially slower than a native PDFium build |
| Binary size | embeds a large WASM blob |
| DPI control | ✅ per `Render` call |
| Page-pixel ceiling | ✅ `WithMaxPagePixels`, checked against the declared media box **before** any bitmap is allocated ([ADR-0020](adr/0020-resource-limits.md)) |
| Parallel rasterising | ✅ `WithInstances` — one WebAssembly module with its own linear memory per instance, defaulting to min(4, `GOMAXPROCS`) |
| Encrypted PDFs | ⛔ `ErrEncrypted`; there is no password to give it |

`render/pdfium` is the recommended default and the one the documentation points
at ([ADR-0010](adr/0010-no-cgo-in-core.md)).

A cgo-linked `render/pdfiumcgo`, trading cross-compilation for native speed,
has been discussed and **does not exist**. It is not on the roadmap, has no
ADR, and nothing in this repository imports it; if it is ever built it gets a
column here, and until then it does not.

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
| Circuit breaking | ⛔ | ⛔ | ✅ | ✅ |
| Hooks | ✅ | ✅ | ✅ | ✅ |
| OpenTelemetry | ⛔ | ✅ | ✅ | ✅ |
| Calibrated confidence | ⛔ | ⛔ | ⛔ | ✅ |
| Batch processing | ⛔ | ⛔ | ✅ | ✅ |
| Streaming documents larger than memory | ⛔ | ⛔ | ⛔ | ⛔ deferred, [ADR-0031](adr/0031-documents-are-read-whole.md) |

The ⚠️ in v0.1 provenance is the one to watch: page-level provenance is enough
for grounding but not enough to highlight a region, so review interfaces built
against v0.1 will need changing at v0.2.

[^cgo]: Plain yes/no, not a ⚠️. Requiring cgo is a documented property of a
module, not a silent degradation — and every ⚠️ in this document is
contractually a behaviour the shared contract suite asserts, which is not
something that can be written for "needs a C toolchain".
