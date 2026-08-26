# ovrin

[![CI](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/BAGOMBEKA-JOB-DEV/ovrin.svg)](https://pkg.go.dev/github.com/BAGOMBEKA-JOB-DEV/ovrin)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Turn documents into structured data.**

Ovrin is a Go library that reads PDFs, scans and images and returns a typed Go
struct — with per-field confidence, a record of where every value came from,
and an explicit signal when a human should look at it.

Define what you want:

```go
type Invoice struct {
    Number   string  `ovrin:"invoice number,required"`
    Vendor   string  `ovrin:"vendor company name"`
    Currency string  `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
    Total    float64 `ovrin:"total amount including tax,required,min=0"`
}
```

Ask for it:

```go
res, err := ovrin.Extract[Invoice](ctx, client, ovrin.File("invoice.pdf"))
if err != nil {
    return err
}

fmt.Println(res.Data.Total)        // 2500.00, a float64
fmt.Println(res.Confidence)        // 0.96
fmt.Println(res.NeedsReview)       // false
```

Ovrin handles the rest: detecting the format, reading the text layer,
rasterising and running OCR when there isn't one, normalising the content,
constraining the model to your schema, validating the result, checking that
every value actually appears in the document, and scoring what it found.

---

## Why ovrin

|  |  |
|---|---|
| **Typed, not `map[string]any`** | `res.Data.Total` is a `float64` at compile time. Rename a field and the compiler finds every use. |
| **A pipeline, not a prompt** | Text layer first, OCR on demand, vision as a distinct reading. Staged extraction measurably beats handing a model raw pages. |
| **Confidence you can decompose** | Every score breaks down into named signals. No number is produced that you cannot take apart. |
| **Every value points back** | Page, bounding box and source span for each field. Review interfaces can highlight; auditors can check. |
| **Fabrication is detected** | Values that appear nowhere in the document are flagged, not returned as fact. |
| **Provider independent** | Three small interfaces. Bring OpenAI, Anthropic, Gemini, Tesseract, Textract, Ollama, or your own. |
| **Zero dependencies in the core** | `go get` pulls nothing. No cgo. Cross-compiles and builds static. |
| **Untrusted input by default** | Documents are parsed with finite limits and prompted as data, never as instruction. |

## What it is not

Ovrin is not "send a PDF to a model and get JSON back". That takes an
afternoon, costs more, and is measurably less accurate on real documents. It
also cannot tell you how confident to be, where a value came from, or whether
the model invented it — which is the part that matters when the extracted
number is a payment.

---

## Install

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin
```

The core has **no external dependencies**. You add exactly the providers you
use, and nothing else enters your `go.sum`:

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl      # OpenAI, Anthropic, Gemini, Ollama, …
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/tesseract   # local OCR
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/google      # Cloud Vision / Document AI
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/render/pdfium   # v0.2 — rasterise scanned PDFs, no cgo
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/otel            # v0.2 — OpenTelemetry
```

Go 1.22 or newer.

## Inputs

| Input | v0.1 | How |
|---|---|---|
| PDF with a text layer | yes | read directly — exact and nearly free |
| PNG, JPEG, TIFF | yes | OCR or vision |
| Scanned PDF | yes, via cloud OCR | providers that accept a PDF rasterise server-side |
| Scanned PDF, offline | v0.2 | needs a local renderer |
| DOCX, XLSX, CSV | v0.3 | |

Document *types* are never hardcoded. Invoices, receipts, government forms,
transcripts, bank statements, medical forms and contracts are all the same
mechanism — you write a struct.

---

## Reading a result

```go mirror
type Result[T any] struct {
    Data        T                        // typed, partially populated
    Valid       bool                     // every validation rule passed
    Confidence  float64
    Fields      map[string]FieldResult   // one per schema field
    NeedsReview bool
    Reasons     []ReviewReason
    Metadata    Metadata
}
```

`err != nil` means nothing usable came back. It does **not** mean the data is
good — that is `Valid`. A field that could not be read is marked absent and is
never filled with a zero value, because a payments system must be able to tell
"the total is zero" from "we could not read the total".

```go
res, err := ovrin.Extract[Invoice](ctx, client, ovrin.File("invoice.pdf"))
if err != nil {
    return err                              // unreadable, no provider, limit hit
}
if !res.Valid || res.NeedsReview {
    return review.Queue(res)                // usable, but not automatically
}
return ledger.Post(res.Data)
```

Ask why:

```go
e, _ := res.Explain("total")
fmt.Println(e)
```

```text
Field:       total
Value:       2500.00
Confidence:  0.99

Signals
  grounding    1.00  ×0.30   found verbatim, page 1
  ocr          0.97  ×0.20   12 backing words, mean 0.97
  schema       1.00  ×0.15   float64, min=0 satisfied
  cross_field  1.00  ×0.05   line items sum to total
  format       1.00  ×0.05   parsed as currency
  agreement       —          only one reading

Provenance
  ocr:tesseract   page 1   box (412,688)-(486,702)   exact

Validation
  required  pass
  min=0     pass
```

---

## Documentation

| Document | What it covers |
|---|---|
| [Getting started](docs/getting-started.md) | First extraction, end to end |
| [The idea](docs/idea.md) | The problem, the goals, and the non-goals |
| [Architecture](docs/architecture.md) | Modules, seams, and which way the arrows point |
| [Pipeline](docs/pipeline.md) | All nine stages in detail |
| [Schemas](docs/schema.md) | The tag grammar and the rule vocabulary |
| [Confidence](docs/confidence.md) | Signals, weights, and what the number does not mean |
| [Explainability](docs/explainability.md) | Provenance, review, and audit |
| [Observability](docs/observability.md) | Hooks, spans and metric names — all of them API |
| [Threat model](docs/threat-model.md) | Prompt injection, resource limits, exfiltration |
| [Data handling](docs/data-handling.md) | What leaves the process, and to whom |
| [Providers](docs/providers.md) | Writing an adapter |
| [Feature matrix](docs/feature-matrix.md) | What each provider supports — and silently ignores |
| [Evaluation](docs/evaluation.md) | How accuracy is measured |
| [Roadmap](docs/roadmap.md) | What is next, and what is deliberately deferred |
| [Rules](docs/rules.md) | The engineering rules this codebase is held to |
| [Decisions](docs/adr/) | 25 ADRs — why it is like this |
| [Glossary](docs/glossary.md) | Terms used throughout |

Contributors and coding agents should start with [`AGENTS.md`](AGENTS.md).

---

## Status

**Pre-v1, and pre-code.** This repository currently contains the design: 25
architecture decision records, a specification of the pipeline, the schema
grammar, the confidence model and the threat model. Implementation has not
started.

What that means concretely:

- **No release exists.** The install commands above will not work yet.
- **The Go API shown here is a specification, not a description.** It will
  change as it meets real documents.
- **No accuracy figure has been published**, and none will be until the
  evaluation harness can reproduce it ([ADR-0023](docs/adr/0023-evaluation-corpus.md)).
- **Confidence weights are provisional.** Confidence is a ranking signal today,
  not a probability. See [`docs/confidence.md`](docs/confidence.md).

Ovrin will remain on v0 until the design has been used on real documents by
people who are not the maintainer. The conditions for v1.0 are written down in
[ADR-0024](docs/adr/0024-versioning-and-stability.md).

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`docs/rules.md`](docs/rules.md).
Commits are Conventional Commits and must be signed off (DCO). The most
valuable contribution right now is a document for the evaluation corpus that we
are legally allowed to redistribute.

## License

Apache-2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
