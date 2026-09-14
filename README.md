# ovrin

[![CI](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/BAGOMBEKA-JOB-DEV/ovrin.svg)](https://pkg.go.dev/github.com/BAGOMBEKA-JOB-DEV/ovrin)
[![Go Report Card](https://goreportcard.com/badge/github.com/BAGOMBEKA-JOB-DEV/ovrin)](https://goreportcard.com/report/github.com/BAGOMBEKA-JOB-DEV/ovrin)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

```bash
go get github.com/BAGOMBEKA-JOB-DEV/ovrin@v1.0.0
```

**Package:** [`github.com/BAGOMBEKA-JOB-DEV/ovrin`](https://pkg.go.dev/github.com/BAGOMBEKA-JOB-DEV/ovrin) ·
**v1.0.0** · Go 1.22+ · zero dependencies · no cgo

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
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/render/pdfium   # rasterise scanned PDFs, no cgo
go get github.com/BAGOMBEKA-JOB-DEV/ovrin/otel            # OpenTelemetry
```

Go 1.22 or newer. (That is a *language* floor. For the toolchain to build
with, see [SECURITY.md](SECURITY.md#which-go-toolchain-you-need).)

## Development

**Every command in this project is a `make` target.** Nothing is hidden in a
script or a CI file — the [`Makefile`](Makefile) is the single definition, and
[CI calls these same targets](.github/workflows/ci.yml), so a green run on your
machine is a green run on the build.

### Setting up

```bash
git clone https://github.com/BAGOMBEKA-JOB-DEV/ovrin.git
cd ovrin

make setup      # commit sign-off hook + golangci-lint and govulncheck at CI's versions
make check      # the whole gate, across all nine modules
```

That is the entire setup. **No credentials are needed to build or test** —
the default suite runs against in-process fakes and loopback servers, offline
([ADR-0022](docs/adr/0022-offline-testing.md)).

Run `make` with no arguments at any time to list every target.

### The two you will use most

| Command | What it does |
|---|---|
| `make check` | The gate to pass before opening a pull request: `gofmt`, build, `go vet` under every build tag, tests with the race detector, tests over real sockets, `go mod tidy`, `golangci-lint`, `govulncheck`, documentation checks — **for every module**. |
| `make ci` | Everything above **plus** what only CI used to do: the coverage floor, the zero-dependency assertion, and the cgo-free cross-compile. |

### Every target

**Getting started**

| Command | What it does |
|---|---|
| `make` / `make help` | List every target |
| `make setup` | Install the sign-off hook and both tools |
| `make hooks` | Just the `Signed-off-by` commit hook |
| `make tools` | Just `golangci-lint` and `govulncheck`, at the versions CI pins |

**Build and test**

| Command | What it does |
|---|---|
| `make build` | Compile every module |
| `make test` | The offline suite, with the race detector |
| `make test-sandbox` | The same over real sockets, against an adversarial fake server |
| `make test-cover` | The suite with a coverage profile — what CI runs |
| `make cover-floor` | Assert coverage is at or above 85% |
| `make cover-html` | Open the coverage profile in a browser |
| `make bench` | Benchmarks (`render/pdfium`) |
| `make fuzz` | Every fuzz target; `FUZZTIME=5m make fuzz` to run longer |
| `make test-integration` | Against real providers. **Costs money** |
| `make eval` | Accuracy against the corpus. **Needs `OPENAI_API_KEY`, costs money** |

**Quality** — each is one step of CI

| Command | What it does |
|---|---|
| `make fmt` | Format every module |
| `make fmt-check` | Fail if anything is not `gofmt`'d |
| `make vet` | `go vet` under every build tag |
| `make lint` | `golangci-lint` |
| `make actions` | Validate the GitHub Actions workflow files |
| `make vuln` | `govulncheck` |
| `make tidy` / `make tidy-check` | `go mod tidy`; the check fails if it left a diff |
| `make deps-check` | Assert the core has zero external dependencies |
| `make cross` | Assert it builds with `CGO_ENABLED=0` for linux/arm64, darwin/arm64, windows/amd64 |

**Documentation and generated files**

| Command | What it does |
|---|---|
| `make docs` | Check links, citations, ADR hygiene and API references |
| `make api` | Regenerate [`api/ovrin.txt`](api/ovrin.txt) from the source |
| `make corpus` | Regenerate the synthetic evaluation corpus |
| `make report` | Regenerate the committed no-run evaluation report |

**Running and releasing**

| Command | What it does |
|---|---|
| `make run-example` | Extract [the example receipt](examples/receipt) with a real model. Needs `OPENAI_API_KEY` |
| `make release-check VERSION=v1.0.0` | Report whether the tree is fit to tag. Never tags, never pushes. Takes a module-prefixed tag too, e.g. `model/skyl/v1.0.0` |
| `make clean` | Remove build and coverage output |

**Docker** — the toolchain pinned, nothing to install

| Command | What it does |
|---|---|
| `make docker-build` | Build the image |
| `make docker-ci` | The whole gate, in a container |
| `make docker-shell` | A shell with the toolchain, your checkout mounted |
| `make docker-test` | Just the test suites |
| `make docker-test-offline` | The suite with `--network=none`, proving it needs no network |
| `make docker-example` | The receipt example. Pass `OPENAI_API_KEY` through |
| `make docker-eval` | The evaluation harness, with `eval/report` mounted back out |
| `make docker-clean` | Remove the images |

The container is worth knowing about for two specific reasons.

It ships Tesseract's English language data, so the six engine-backed tests in
`ocr/tesseract` that skip on a machine without a language pack actually run
there.

And it pins the Go toolchain. `make vuln` reports vulnerabilities in **the
standard library of whichever Go you are running**, not only in ovrin — so on
an older toolchain it fails with a long list that no change to this repository
can fix. If that happens, either upgrade Go or run `make docker-ci`, which uses
a pinned modern one. See
[SECURITY.md](SECURITY.md#which-go-toolchain-you-need) for why the `go 1.22` in
`go.mod` is a language floor and not a claim that 1.22.0 is safe to run this
on.

### Working on one module

The repository is nine Go modules. Every target loops over all of them; pass
`MODULES` to narrow it, which is exactly how CI's matrix invokes them:

```bash
make test MODULES=ocr/azure
make build MODULES="ocr/azure ocr/textract"
```

### Environment variables

None are needed for `make check`. These matter only for the targets that
contact a real provider:

| Variable | Used by | Notes |
|---|---|---|
| `OPENAI_API_KEY` | `make run-example`, `make eval` | Required by both; they refuse to start without it |
| `OPENAI_BASE_URL` | `make eval` | Defaults to `https://api.openai.com/v1` |
| `OVRIN_MODEL` | `make run-example` | Defaults to `gpt-5.2` |
| `OVRIN_EVAL_MODEL` | `make eval` | Defaults to `gpt-5.2` |

Adapters never read the environment themselves — every credential is a
function argument ([`rules.md` §6.4](docs/rules.md#6-adapters)). The variables
above are read by the example programme and the evaluation harness, which are
programmes rather than library code.

## Inputs

| Input | v0.1 | How |
|---|---|---|
| PDF with a text layer | yes | read directly — exact and nearly free |
| PNG, JPEG, TIFF | yes | OCR or vision |
| Scanned PDF | yes, via cloud OCR | providers that accept a PDF rasterise server-side |
| Scanned PDF, offline | yes | `render/pdfium` rasterises locally, `ocr/tesseract` reads — neither needs cgo or a network |
| DOCX, XLSX, CSV | yes | read directly; no OCR and no renderer |

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
| [Decisions](docs/adr/) | 32 ADRs — why it is like this |
| [Glossary](docs/glossary.md) | Terms used throughout |

Contributors and coding agents should start with [`AGENTS.md`](AGENTS.md).

---

## Status

**v1.0.0 — the API is stable.** Nine Go modules, the core with zero
dependencies, on top of thirty-two architecture decision records, most of them
written before the code. A breaking change now requires a v2
([ADR-0032](docs/adr/0032-v1-is-an-api-promise.md)).

**That is a promise about compatibility, not about accuracy.** The two are
separable and this project keeps them separate, because a version number is a
poor place to hide a caveat:

| | |
|---|---|
| The API will not break without a v2 | **Yes.** `api/ovrin.txt` is a contract, checked on every commit |
| Published accuracy figure | **None.** The evaluation corpus is synthetic, and no run has been committed ([ADR-0023](docs/adr/0023-evaluation-corpus.md)) |
| Confidence is a calibrated probability | **No.** It is a ranking signal. It orders a review queue well; it does not mean "correct this often" ([`docs/confidence.md`](docs/confidence.md)) |
| Used in production outside this project | **Not that we know of.** If you do, please say so |

Released: the core at `v1.0.0`, and the seven adapters and the example each at
`<path>/v1.0.0`. Modules version independently
([ADR-0024](docs/adr/0024-versioning-and-stability.md)), so they may diverge
from here.

If you are deciding whether to depend on this,
[`docs/validating.md`](docs/validating.md) is written for you and includes the
reasons not to.

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`docs/rules.md`](docs/rules.md).
Commits are Conventional Commits and must be signed off (DCO). The most
valuable contribution right now is a document for the evaluation corpus that we
are legally allowed to redistribute.

## License

Apache-2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
