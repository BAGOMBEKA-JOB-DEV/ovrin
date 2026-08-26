# Writing an adapter

Ovrin has three seams. Anything implementing one is a first-class provider,
whether it lives in this repository or yours.

> **Not implemented yet.** This specifies the contract adapters will be written
> and tested against.

**Contents:** [The rules](#the-rules-that-apply-to-every-adapter) ·
[A Model adapter](#a-model-adapter) · [An OCR adapter](#an-ocr-adapter) ·
[A Renderer](#a-renderer) · [The contract suite](#the-contract-suite) ·
[Contributing one](#contributing-an-adapter-to-this-repository)

---

## The rules that apply to every adapter

Six, and the contract suite enforces most of them.

**1. Adapters map; they do not decide** (rule
[§6.2](rules.md#6-adapters)). No retry loop, no fallback, no timeout policy, no
limit enforcement, no prompt construction. Those are the core's, so that they
are decided once rather than once per adapter.

**2. Never silently drop data** (rule [§6.1](rules.md#6-adapters)). If you
cannot represent something the caller asked for, return `ErrUnsupported` naming
what you could not do. Quietly producing a worse answer than the caller
believes they asked for is the one behaviour that is never acceptable.

**3. Classify errors; never let a message string carry meaning** (rule
[§2.2](rules.md#2-errors)). Map the provider's status codes onto ovrin's
sentinels at your boundary.

**4. Never put document content or credentials in an error** (rule
[§2.5](rules.md#2-errors)). Tested.

**5. Read credentials only from what you are given** (rule
[§6.4](rules.md#6-adapters)). No `os.Getenv` inside the adapter. Reading the
environment in a library is how a program talks to the wrong account.

**6. Be safe for concurrent use, and honour cancellation** (rules
[§5.1](rules.md#5-concurrency-and-resources),
[§5.4](rules.md#5-concurrency-and-resources)). Say so in the doc comment.
Tested, including for goroutine leaks.

Shape:

```go
package myocr

type Option func(*Provider)

func WithBaseURL(u string) Option
func WithHTTPClient(hc *http.Client) Option

// New returns a provider backed by …
//
// The returned Provider is safe for concurrent use by multiple goroutines.
func New(apiKey string, opts ...Option) *Provider

var _ ovrin.OCR = (*Provider)(nil)   // rule §6.3
```

Functional options, no exported config struct (rule
[§1.4](rules.md#1-public-api)). A compile-time assertion at the bottom of the
file.

---

## A Model adapter

```go mirror
type Model interface {
    Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error)
}

type ModelRequest struct {
    Instruction string      // built by ovrin. Never contains document content.
    Content     []Content   // the untrusted material, delimited and labelled
    Schema      []byte      // JSON Schema the reply must satisfy
    Temperature *float64
}

type ModelResponse struct {
    JSON  []byte
    Usage Usage
    Raw   any
}
```

**Do not build a prompt.** `Instruction` is already built, and the separation
between it and `Content` is a security boundary
([ADR-0017](adr/0017-untrusted-document-content.md)). Map `Instruction` to the
provider's system role and `Content` to the user role, or to whatever the
provider's nearest equivalent is. Never concatenate them.

**Use native structured output where it exists.** Pass `Schema` to the
provider's JSON-schema mode. If the provider has none, embed the schema in the
instruction region and return what comes back — ovrin validates either way, and
the difference surfaces as a confidence signal rather than as two code paths.

**Return raw bytes.** Do not unmarshal. A model returning invalid JSON must
produce an ovrin error with the offending bytes attached, not an
adapter-specific one ([ADR-0007](adr/0007-model-seam.md)).

**Handle images.** `Content` may contain page images. A provider without vision
returns `ErrUnsupported` naming the limitation. It never drops them and
proceeds on text alone — that would silently produce a much worse answer.

**Map usage carefully.** Vendors disagree about whether cached tokens are
included in the input count. Say what you did in a comment; the feature matrix
records the discrepancies.

---

## An OCR adapter

```go mirror
type OCR interface {
    Recognise(ctx context.Context, page Page) (*Recognition, error)
    Name() string
}

type Recognition struct {
    Words      []Word    // in reading order
    Lines      []Line
    Confidence float64
    Language   string
    Raw        any
}

type Word struct {
    Text       string
    Box        Rect     // page points, origin top-left
    Confidence float64  // 0..1
    Line       int
}
```

Four normalisations the contract suite checks, because they are easy to get
subtly wrong and the confidence engine depends on all of them:

**Coordinates are page points with a top-left origin.** Not PDF's bottom-left,
not pixels. Convert.

**Confidence is 0 to 1.** Tesseract reports 0–100. Divide.

**Words are in reading order**, not API order. A provider returning results
positionally must be sorted before returning.

**A provider without per-word confidence sets the page confidence on each word
and records that it did.** It does not fabricate 1.0 (rule
[§6.1](rules.md#6-adapters)).

**Providers that accept a PDF directly** may implement the optional
`DocumentOCR` interface, letting ovrin skip local rasterising entirely — the
route that makes scanned PDFs work in v0.1 without a renderer
([ADR-0010](adr/0010-no-cgo-in-core.md)):

```go mirror
type DocumentOCR interface {
    OCR
    RecogniseDocument(ctx context.Context, doc Document) ([]*Recognition, error)
}
```

---

## A Renderer

```go mirror
type Renderer interface {
    Render(ctx context.Context, doc Document, page, dpi int) (image.Image, error)
}
```

Simple, and constrained: rule [§4.3](rules.md#4-dependencies) means a renderer
using cgo must say so **on the first line of its package documentation** and in
its module name. Users choose Go partly for static builds and
cross-compilation, and a dependency that silently breaks both is a bad
surprise.

Respect `WithMaxPagePixels` — a page declaring an implausible media box must
not be rasterised into an allocation larger than memory
([ADR-0020](adr/0020-resource-limits.md)).

---

## The contract suite

Every adapter passes the shared suite in `internal/adaptertest`
(rule [§3.1](rules.md#3-testing)). A rule added there is enforced everywhere at
once, and no adapter can regress behind another's tests.

```go
func TestProvider(t *testing.T) {
    adaptertest.OCR(t, adaptertest.OCRSuite{
        Name: "myocr",
        New:  func(baseURL string) ovrin.OCR {
            return myocr.New("test-key", myocr.WithBaseURL(baseURL))
        },
        SuccessBody: testdata.Success,
        ErrorBody:   testdata.RateLimit,
    })
}
```

What it asserts:

| | |
|---|---|
| Normalisation | coordinates, confidence range, reading order |
| Errors | status codes map to the right sentinels |
| Secrets | no credential appears in any error |
| Privacy | no document content appears in any error |
| Cancellation | a cancelled context aborts promptly |
| Goroutines | none leak, including on early return |
| Concurrency | passes under `-race` with concurrent calls |
| Unsupported | requests that cannot be served return `ErrUnsupported`, not a degraded result |

An adapter that cannot pass the suite is not finished. This is a real barrier
to contribution and it is deliberate — an adapter is a component users will
trust with documents that matter.

---

## Contributing an adapter to this repository

Out-of-tree adapters are first class and need nothing from us. To contribute
one here:

1. **Open an issue first.** Each in-tree adapter is a module the maintainer
   must release and keep working, and the bus factor is one
   ([`../MAINTAINERS.md`](../MAINTAINERS.md)). We may ask you to keep it in
   your own repository and link to it, and that is not a rejection.
2. `ocr/<name>/` or `model/<name>/`, with its own `go.mod` and its own
   `LICENSE`.
3. Wire it into the contract suite.
4. Add sandbox support in `internal/sandbox` so it is testable offline
   ([ADR-0022](adr/0022-offline-testing.md)).
5. Add a column to [`feature-matrix.md`](feature-matrix.md) — **including the
   ⚠️ cells.** An adapter contributed without its silently-ignored row is
   incomplete.
6. Add a CI matrix row.
7. `docs/rules.md` §6, in full.

## See also

- [ADR-0007](adr/0007-model-seam.md) — why the model seam is one method
- [ADR-0009](adr/0009-ocr-seam.md) — why OCR returns words, not a string
- [ADR-0010](adr/0010-no-cgo-in-core.md) — the cgo policy
- [`feature-matrix.md`](feature-matrix.md) — what existing adapters do
