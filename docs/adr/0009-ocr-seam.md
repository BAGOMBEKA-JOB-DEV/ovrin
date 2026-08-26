# ADR-0009: OCR providers are separate modules behind a two-method seam

**Status:** Accepted · **Date:** 2026-08-26

## Context

OCR providers differ from one another far more than language models do, and the
differences are not cosmetic.

Tesseract is a local C++ library. Google Cloud Vision and Document AI are HTTP
APIs behind a large generated SDK. AWS Textract is another. Azure Document
Intelligence is a third. Mistral OCR is a fourth. Each cloud SDK is tens of
megabytes of generated code with its own transitive dependency tree and its own
authentication model, and no two of them agree on how to express a bounding box
or a per-word confidence.

If ovrin's core imported even one of them, every user would inherit it. A user
running Tesseract locally has no business carrying the AWS SDK; a user on
Textract has no business carrying a Google credentials library. This is rule
[§4.2](../rules.md#4-dependencies), and OCR is the case that motivates it most
sharply.

The second problem is what the seam returns. OCR output is not a string. It is
words with positions, confidences, reading order and page geometry, and ovrin
needs all of that: positions and geometry for provenance
([ADR-0015](0015-provenance.md)), per-word confidence as a scoring signal
([ADR-0013](0013-multi-signal-confidence.md)), reading order for anything with
columns. A seam returning `string` would throw away the signals the confidence
engine is built on.

## Decision

Two methods in the core, every implementation in its own module.

```go
// OCR recovers text and layout from a rasterised page.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type OCR interface {
    Recognise(ctx context.Context, page Page) (*Recognition, error)
    Name() string
}

type Recognition struct {
    Words      []Word    // in reading order
    Lines      []Line
    Confidence float64   // provider's own, over the page
    Language   string
    Raw        any
}

type Word struct {
    Text       string
    Box        Rect     // page coordinates, origin top-left, points
    Confidence float64  // 0..1, normalised
    Line       int
}
```

Modules: `ovrin/ocr/tesseract`, `ovrin/ocr/google`, `ovrin/ocr/textract`,
`ovrin/ocr/azure`. Each has its own `go.mod`, its own release tag and its own
CI matrix row. The core has none of them.

Every implementation passes the shared contract suite in
`internal/adaptertest` (rule [§3.1](../rules.md#3-testing)), which fixes the
behaviours that are easy to get subtly wrong: coordinates are page points with
the origin top-left regardless of what the vendor uses; confidence is
normalised to 0–1 regardless of whether the vendor reports 0–100; words are in
reading order, not API order; cancellation aborts; errors carry no document
content.

A provider that does not report per-word confidence sets it to the page
confidence and records that it did, rather than reporting a fabricated 1.0
(rule [§6.1](../rules.md#6-adapters)).

`Page` is a rasterised image plus its dimensions, which is why
[ADR-0010](0010-no-cgo-in-core.md) has to solve rasterising first — with one
exception, noted there, for cloud providers that accept a PDF directly and
rasterise server-side.

## Consequences

**Good.** The core stays dependency-free and users pay only for the OCR they
use. A new provider is a new module by anyone, in or out of tree, with no
change to ovrin. The contract suite means adding a rule fixes every adapter at
once. Normalised coordinates and confidences mean the confidence engine and the
provenance model are written once against one shape.

**Bad.** Normalisation loses information: Textract's table and form structures,
Document AI's entity detection and Azure's key-value pairs are all richer than
`Words` and `Lines`, and reducing them to words discards work the caller paid
for. `Raw any` mitigates it for anyone willing to type-assert, which is not a
real answer. Five modules is five sets of release mechanics for one maintainer.
And the contract suite is a genuine barrier to contribution — "here is a
provider adapter" now means "here is a provider adapter that passes twenty
behavioural tests".

**Neutral.** Some providers do extraction as well as OCR — Document AI will
return an invoice object directly. Ovrin uses them for OCR only. Their
extraction is a different product with a different accuracy profile, and mixing
the two would make confidence uninterpretable.

## Alternatives considered

- **`Recognise(ctx, page) (string, error)`.** Rejected: discards position and
  confidence, which are inputs to two other subsystems. The simplest seam that
  cannot support the product.
- **All providers in the core behind build tags.** Rejected: `go.mod`
  dependencies are not tag-conditional, so every user gets every SDK regardless
  of tags. This does not work, as skyl's ADR-0001 also records.
- **One `ovrin/ocr` module holding every provider.** Rejected: better than the
  core, still wrong — a Tesseract user carries three cloud SDKs.
- **Shell out to provider CLIs.** Rejected: an exec dependency, a parsing
  surface, and no route to per-word confidence.
