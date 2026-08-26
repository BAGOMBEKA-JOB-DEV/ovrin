# ADR-0010: No cgo in the core; rasterising runs PDFium under Wazero

**Status:** Accepted · **Date:** 2026-08-26

## Context

This is the hardest constraint in the project, and it deserves to be stated
plainly: **there is no good pure-Go PDF renderer.**

A scanned PDF has no text layer. To read it, the page must be rasterised to an
image, and that image handed to OCR or to a vision model. Rasterising a PDF
means implementing a substantial part of the PDF imaging model — fonts, CFF and
TrueType hinting, shading, transparency groups, colour spaces, clipping. Nobody
has done it well in Go.

The options in the ecosystem:

- **`ajroetker/pdf/render`** — pure Go, unmaintained, and unequal to real-world
  PDFs.
- **cgo bindings to PDFium** (`brunsgaard/go-pdfium-render`) — fast and
  correct, requires a C toolchain and a platform-specific PDFium build.
- **MuPDF** (`lazypdf`) — fast and correct, and **AGPL**, which rule
  [§4.4](../rules.md#4-dependencies) forbids outright.
- **`klippa-app/go-pdfium` in Wazero mode** — PDFium compiled to WebAssembly,
  executed by a pure-Go WASM runtime. No cgo. Cross-compiles anywhere Go does.
  Slower than native, and embeds a large WASM binary.
- **Shell out to `pdftoppm` or Ghostscript** — an external process, a
  deployment dependency, and Ghostscript is AGPL.

Meanwhile, `CGO_ENABLED=0` static binaries and trivial cross-compilation are
substantially why people choose Go for backend services. A document library
that breaks `GOOS=linux GOARCH=arm64 go build` has broken the deployment story
of a large share of its potential users.

## Decision

**The core module never uses cgo** (rule
[§4.3](../rules.md#4-dependencies)). It is pure Go with zero dependencies. PDF
text-layer extraction, image decoding, schema reflection, prompt construction,
validation and confidence all live there and all cross-compile.

**Rasterising is an optional seam** with no default implementation in the core:

```go mirror
// Renderer rasterises a document page to an image.
type Renderer interface {
    Render(ctx context.Context, doc Document, page int, dpi int) (image.Image, error)
}
```

**The recommended implementation is `ovrin/render/pdfium`**, built on
`klippa-app/go-pdfium` in its Wazero configuration. No cgo, cross-compiles, and
correct on real PDFs. This is what the documentation points at.

**cgo is permitted in submodules whose documentation says so on the first
line** — `ovrin/render/pdfiumcgo` and `ovrin/ocr/tesseract` are expected to
exist for users who want native speed and accept the toolchain. They are never
a default and never a transitive dependency of anything that is.

**Three paths avoid rasterising entirely**, and the documentation says so
prominently, because for many users the answer is to not do this at all:

1. A PDF with a text layer needs no rasterising. This is most PDFs.
2. Cloud OCR providers — Document AI, Textract, Azure, Mistral OCR — accept a
   PDF directly and rasterise server-side. Ovrin passes the bytes through.
3. Inputs that are already images (PNG, JPEG, TIFF) need no renderer.

A scanned PDF with no renderer and no PDF-accepting OCR provider is an error
naming the three ways to fix it, not a silent empty result.

## Consequences

**Good.** `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` works on the core
and on every recommended module. Scratch and distroless containers work. The
AGPL trap is avoided by construction. The most common case — a text-layer PDF —
needs no renderer at all, so most users never meet this decision.

**Bad.** PDFium-under-Wazero is materially slower than native PDFium, and the
gap is largest on exactly the workload that matters — bulk scanned pages. The
WASM blob is tens of megabytes, which inflates build times and module cache
size for anyone importing the renderer. Users who need maximum throughput are
pushed to the cgo module and lose cross-compilation, so the constraint is
relocated rather than removed. And we are exposed to `klippa-app/go-pdfium`'s
maintenance: if it stops tracking PDFium, the recommended path decays and the
alternatives are all worse.

**Neutral.** No renderer ships in v0.1. The v0.1 story is text-layer PDFs,
images, and cloud OCR for scans; local rasterising arrives in v0.2. That is
recorded in [`docs/roadmap.md`](../roadmap.md), and it is a real limitation
rather than a soft one.

## Alternatives considered

- **cgo PDFium as the default renderer.** Rejected: the best accuracy and
  speed available, at the cost of the property most of ovrin's users chose Go
  for. Available as an opt-in module instead.
- **MuPDF via `lazypdf`.** Rejected on licence alone — AGPL, rule
  [§4.4](../rules.md#4-dependencies). Not evaluated on merit, because merit is
  irrelevant once the licence disqualifies it.
- **A pure-Go renderer of our own.** Rejected: it is a multi-year project, and
  it is a different project from this one.
- **Shell out to `pdftoppm`.** Rejected: an undeclared deployment dependency
  that fails at runtime on a machine that does not have it — the worst possible
  time to discover a missing dependency.
- **Cloud OCR only; never rasterise locally.** Rejected as a permanent policy:
  it is the correct v0.1 scope, but it makes air-gapped and
  data-residency-constrained deployments impossible, and those are precisely
  the government and healthcare users this library is aimed at.
