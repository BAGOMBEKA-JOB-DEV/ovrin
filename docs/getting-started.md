# Getting started

> **Ovrin has not been implemented yet.** This document specifies the intended
> experience. Nothing below works today; see the
> [README's status section](../README.md#status).
>
> Sections opening with a **v0.2** or **v0.3** note describe features that will
> not be in the first release. Everything unmarked is v0.1 scope, and the
> documentation checks in CI enforce that — an unmarked example may not use a
> feature [`feature-matrix.md`](feature-matrix.md) marks ⛔ for the current
> version ([ADR-0029](adr/0029-v01-scope-corrected.md)).

**Contents:** [Install](#install) · [First extraction](#first-extraction) ·
[Reading the result](#reading-the-result) · [Scanned documents](#scanned-documents) ·
[Validation](#validation) · [Two readings](#two-readings) ·
[Fallback](#provider-fallback) · [Limits](#limits) · [Where next](#where-next)

---

## Install

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl
```

The core has no dependencies. The model adapter is what talks to a provider,
and you pick it ([ADR-0008](adr/0008-skyl-is-an-adapter.md)).

---

## First extraction

A PDF invoice that has a text layer — which is most PDFs that were not scanned.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/BAGOMBEKA-JOB-DEV/ovrin"
    ovrinskyl "github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl"
    "github.com/BAGOMBEKA-JOB-DEV/skyl"
    "github.com/BAGOMBEKA-JOB-DEV/skyl/provider/openai"
)

type Invoice struct {
    Number   string  `ovrin:"invoice number,required"`
    Vendor   string  `ovrin:"vendor company name"`
    Currency string  `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
    Total    float64 `ovrin:"total amount including tax,required,min=0"`
}

func main() {
    client := ovrin.New(
        ovrin.WithModel(ovrinskyl.New(
            skyl.New(openai.New(os.Getenv("OPENAI_API_KEY"))),
            ovrinskyl.WithModelID("gpt-5.2"),
        )),
    )

    f, err := os.Open("invoice.pdf")
    if err != nil {
        log.Fatal(err)
    }
    defer f.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    res, err := ovrin.Extract[Invoice](ctx, client, ovrin.Reader(f))
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("%s — %s %.2f from %s\n",
        res.Data.Number, res.Data.Currency, res.Data.Total, res.Data.Vendor)
    fmt.Printf("confidence %.2f · valid %t · review %t\n",
        res.Confidence, res.Valid, res.NeedsReview)
}
```

Always set a context deadline. Ovrin has no default wall-clock limit, because
Go already has one mechanism for that and a second would let them disagree
([ADR-0020](adr/0020-resource-limits.md)).

---

## Reading the result

`err` and `Valid` answer different questions, and conflating them is the
mistake most likely to produce a bug.

```go
res, err := ovrin.Extract[Invoice](ctx, client, src)

if err != nil {
    // Nothing usable came back: unreadable file, no provider for this
    // document, a limit was hit, the context expired. res is nil.
    return err
}

if !res.Valid {
    // Data came back, but a validation rule failed. res.Data is populated
    // with everything that was read.
    for name, f := range res.Fields {
        if !f.Valid {
            log.Printf("%s: %v", name, f.Errors)
        }
    }
}

if res.NeedsReview {
    return review.Queue(res)
}

return ledger.Post(res.Data)
```

**A field that could not be read is absent, not zero.**

```go
total := res.Fields["total"]
if !total.Found {
    // The total was not found. res.Data.Total is 0.0, and that
    // does NOT mean the invoice is for nothing.
}
```

This distinction is why `Fields` exists ([ADR-0004](adr/0004-partial-results.md)).
For fields where a downstream consumer must never confuse absent with zero, use
a pointer in the struct so the absence is visible on `Data` itself.

**Ask why:**

```go
if e, ok := res.Explain("total"); ok {
    fmt.Println(e)
}
```

---

## Scanned documents

> **Cloud OCR works in v0.1. Local OCR needs `render/pdfium`, which arrives in
> v0.2** ([ADR-0029](adr/0029-v01-scope-corrected.md)).

A scan has no text layer, so the page must be read as pixels. There are two
routes.

**Cloud OCR** — the provider accepts the PDF and rasterises server-side, so you
need no local renderer:

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/google
```

```go
client := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithOCR(google.New(credentials)),
)
```

**Local OCR** — needs a renderer to turn pages into images first
([ADR-0010](adr/0010-no-cgo-in-core.md)):

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/render/pdfium
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/tesseract
```

```go
client := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithRenderer(pdfium.New()),   // PDFium under Wazero — no cgo
    ovrin.WithOCR(tesseract.New()),
)
```

`render/pdfium` runs PDFium as WebAssembly, so it cross-compiles and needs no C
toolchain. It is slower than a native build and embeds a large binary; that
trade is discussed in [ADR-0010](adr/0010-no-cgo-in-core.md).

Nothing changes at the call site. Ovrin uses the text layer when there is one
and falls through to OCR when there is not, per page
([ADR-0012](adr/0012-text-first-ocr-on-demand.md)).

---

## Validation

Rules go in the tag, after the description
([`schema.md`](schema.md)):

```go
type Application struct {
    Name     string    `ovrin:"applicant's full name as written,required"`
    NIN      string    `ovrin:"national identification number,required"`
    Born     time.Time `ovrin:"date of birth,required,format=date"`
    Email    string    `ovrin:"email address,format=email"`
    District string    `ovrin:"district of residence,required"`
    Children int       `ovrin:"number of dependent children,min=0,max=30"`
}
```

A failed rule is never an error. It sets `Valid` false on the field and on the
result, and extraction continues — eleven good fields are not discarded because
of one bad one.

---

## Two readings

> **v0.3.** Cross-validation needs two independent readings; see
> [ADR-0029](adr/0029-v01-scope-corrected.md).

When a value being wrong has a real consequence, run two independent readings
and compare them ([ADR-0014](adr/0014-cross-validation.md)):

```go
res, err := ovrin.Extract[Invoice](ctx, client, src,
    ovrin.WithReading(ovrin.ModeBoth),
)

if f := res.Fields["total"]; len(f.Candidates) > 1 {
    for _, c := range f.Candidates {
        log.Printf("%v from %s", c.Value, c.Reading)
    }
    // 25000.00 from ocr
    //  2500.00 from vision
}
```

Disagreement is recorded, never resolved silently. `Value` holds the
higher-confidence candidate, `agreement` drops, and the field is flagged.

This roughly doubles cost and latency, which is why it is opt-in.

---

## Provider fallback

> **v0.2.** Chains are a decorator over the seam
> ([ADR-0018](adr/0018-fallback-is-a-decorator.md)).

A chain is an ordinary provider, so the pipeline cannot tell the difference
([ADR-0018](adr/0018-fallback-is-a-decorator.md)):

```go
client := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithOCR(ovrin.OCRChain(
        google.New(creds),
        textract.New(cfg),
        tesseract.New(),
    )),
)
```

It advances on rate limits, server errors and transport failures. It **never**
advances on an authentication or bad-request error, because a wrong credential
should fail loudly on the first provider rather than silently degrade to the
third. Every attempt is reported through the hook.

---

## Limits

Every limit has a finite default ([ADR-0020](adr/0020-resource-limits.md)).
Raise them deliberately for documents you trust:

```go
internal := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithMaxPages(5000),
    ovrin.WithMaxSourceBytes(512<<20),
)

public := ovrin.New(
    ovrin.WithModel(model),   // defaults: 1000 pages, 64 MiB
)
```

Run a permissive client for trusted internal documents and a strict one for
public uploads.

---

## Observability

> **Hooks work in v0.1. The `ovrin/otel` module arrives in v0.2.**

```go
client := ovrin.New(
    ovrin.WithModel(model),
    ovrin.WithHook(func(ctx context.Context, ev ovrin.Event) {
        log.Printf("%s provider=%s page=%d %s err=%v",
            ev.Op, ev.Provider, ev.Page, ev.Duration, ev.Err)
    }),
)
```

Events carry field names, pages, counts and durations — never document values
([ADR-0021](adr/0021-observability.md)). For OpenTelemetry, use the
`ovrin/otel` module.

---

## Where next

- [`schema.md`](schema.md) — the tag grammar, rules and supported types
- [`confidence.md`](confidence.md) — what the number means, and what it does not
- [`pipeline.md`](pipeline.md) — what happens between file and struct
- [`threat-model.md`](threat-model.md) — read before processing public uploads
- [`providers.md`](providers.md) — writing an adapter for something we do not support
