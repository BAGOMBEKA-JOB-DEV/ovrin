# Changelog

All notable changes to this project are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Until v1.0.0, breaking changes may land in minor releases. They will always be
listed here with a migration note.

Modules in this repository version independently and are tagged with their path
prefix. Entries below say which module they affect where it is not the core.

## [Unreleased]

### Added

- **The design, written before the code.** The architecture, the public API,
  the pipeline, the schema grammar, the confidence model and the threat model
  for a Go library that turns documents into typed, validated data — all
  settled before the first line was implemented.

  Twenty-five architecture decision records in [`docs/adr/`](docs/adr/) settled
  the load-bearing questions, each with the alternatives that were rejected and
  the costs that were accepted. The ones that shape the most: the core has zero
  dependencies and no cgo, schemas are Go structs read by reflection,
  confidence is multi-signal because logprobs are unavailable or saturated, and
  document content is treated as untrusted input at every stage.

  Written before the code deliberately. Discovering during implementation that
  confidence cannot come from logprobs, or that prompt construction has to sit
  on the core's side of the seam for the security property to hold, would have
  produced a worse design and a rewrite.

- **The name.** The project was renamed from *vellum* to **ovrin** before any
  code existed. `vellum` collided with `github.com/blevesearch/vellum` — a
  widely-imported Go package of the same name — with vellum.ai, an active
  platform in the same problem space, and with a registered US word mark in the
  software services class. `ovrin` has no Go-namespace collision.
  [ADR-0001](docs/adr/0001-name-and-module-path.md) records the sweep,
  including the candidates that were rejected and the collisions `ovrin` does
  have.

- **Documentation.** Eighteen documents covering the idea, getting started, the
  architecture, all nine pipeline stages, the tag grammar, the confidence
  model, explainability, the threat model, data handling, provider authoring,
  the feature matrix, evaluation, the engineering rules, the roadmap, the
  project plan, a glossary, and an honest guide to
  [validating ovrin before adopting it](docs/validating.md).

- **`AGENTS.md`.** An operating contract for coding agents and new
  contributors, whose rules cite `docs/rules.md` section numbers so that a
  review comment can point at a line rather than an opinion.

- **The documentation was reconciled against itself.** An audit before writing
  any code found **27 contradictions** between documents, several of which made
  the design unimplementable as written: `Extract`'s declared signature
  disagreed with its own call sites, two error sentinels were used but never
  defined, `ADR-0004` contradicted itself about whether `Result` is nil on
  error, and fourteen types were referred to across the corpus without ever
  being declared.

  All twenty-seven are resolved. Four new decision records cover the ones that
  changed the API: [ADR-0026](docs/adr/0026-extract-takes-per-call-options.md)
  (`Extract` takes per-call options),
  [ADR-0027](docs/adr/0027-twelve-sentinels-and-one-op-vocabulary.md) (a twelfth
  sentinel, and one `Op` vocabulary shared by errors and events),
  [ADR-0028](docs/adr/0028-reading-and-readingmode.md) (`Reading` and
  `ReadingMode` are different types, so a `Provenance` cannot claim two
  readings at once) and
  [ADR-0029](docs/adr/0029-v01-scope-corrected.md) (the v0.1 scope, corrected —
  nested structs, slices, `format`, `enum` and `Explain` move into the first
  release, because the README's headline example uses them).

- **`api/ovrin.txt`.** The public API surface as a sorted, machine-readable
  contract in the format Go uses for its own `api/go1.N.txt`. Hand-authored for
  now, which makes it the specification: once there is code, the generator is
  red until the source matches it.

- **`scripts/check-docs.py`.** Documentation integrity, run in CI and locally.
  Resolves links **and their anchors**, checks that every `rules.md §N` and
  `ADR-NNNN` citation points at something real, enforces ADR hygiene, verifies
  that every ovrin symbol named in the docs exists in `api/ovrin.txt`, and compares the
  two hand-maintained repository-layout trees against each other. It found five
  problems on its first run, including two introduced minutes earlier.

- **`docs/observability.md`.** The span and metric names `ovrin/otel` will
  emit, treated as API. ADR-0021 promised this document existed; it did not.

- **The pipeline, and the first extraction.** `Extract` no longer panics. A
  document now runs the nine stages end to end — detect, acquire, normalise,
  schema, prompt, generate, validate, ground, score — and returns a typed
  struct with per-field confidence, provenance and review reasons.

  The orchestration lives in the root package rather than under `internal/`.
  It touches nearly the whole public type set, and an internal package cannot
  import the root, so it would have needed a local twin of `Model`, `OCR`,
  `Page`, `Content`, `FieldResult`, `Signal`, `Provenance`, `Metadata` and
  `Event`, with a conversion at every stage boundary — a great deal of
  mechanical code to buy a boundary that unexported identifiers already give.

- **`internal/img`.** PNG and JPEG decoding, with the pixel ceiling enforced
  from the header before any allocation: a file declaring 20,000 × 20,000 costs
  the bytes already read and nothing more.

- **The default scorer.** A weighted mean over the signals that applied, with
  the weight of an absent signal redistributed rather than counted as zero, and
  then the ceilings. A ceiling that binds is recorded as a zero-weight
  `capped:…` signal, because `docs/confidence.md` promises every score
  decomposes into its signals and a confidence below the mean with nothing to
  explain the gap would make that claim false.

- **`OCRChain` and `ModelChain`.** They advance on throttling, outages and
  transport failures, and never on a bad credential, a rejected request or a
  schema ovrin itself refused — those fail identically everywhere, and
  degrading quietly to the third provider hides a misconfiguration that should
  be loud. Exhausting a chain reports every attempt, not only the last.

- **`FieldResult.Validation`.** Each declared rule and whether it passed.
  Distinct from `Errors`, which records only failures: the rules that *passed*
  are what make a confidence score checkable by hand.

- **`examples/receipt`.** A synthetic receipt and a programme that extracts it
  with a real model. Its own module, for the same reason every adapter is one —
  it imports `model/skyl`, and keeping it in the root module would put skyl in
  the `go.sum` of every ovrin user to run a programme none of them run.

- **Two readings and cross-validation (`ModeBoth`).** OCR and vision now read
  the document independently and their answers are compared field by field.
  Where they disagree, both values are kept on `FieldResult.Candidates`, the
  `agreement` signal scores zero, confidence is capped at `CapDisagreement`,
  and the field is flagged for review. Nothing is resolved silently — a
  quiet pick between two different readings of an amount is the failure this
  exists to prevent ([ADR-0014](docs/adr/0014-cross-validation.md)).

  The comparison is type-aware, so `25,000` and `25000` are the same answer
  and only a real disagreement is reported. Before this, the `agreement`
  signal was the second-heaviest in the confidence model at 0.25 and could
  never fire, because only one reading was ever taken.

- **DOCX, XLSX and CSV** (`internal/office`). Read with `archive/zip`,
  `encoding/xml` and `encoding/csv` — no new dependency. These carry their own
  text, so they take the same path a text-layer PDF does: no OCR, no renderer,
  no rasterisation.

  They report **no geometry**, deliberately. A DOCX has no fixed layout until
  something renders it, and `internal/normalise` abstains from its
  position-dependent checks rather than run them against an invented page size.
  The cost is real and stated in the package: a value extracted from one of
  these can be located in the text but not highlighted on a page. Hidden runs
  (`w:vanish`) have their text extracted and their count reported, because text
  a reviewer cannot see and a model can is the shape of an injection.

- **`ocr/textract` and `ocr/azure`.** Both standard-library-only — AWS SigV4 is
  ~90 lines over `crypto/hmac`, checked against AWS's own published test
  vectors, and Azure needs one header. No AWS or Azure SDK enters your
  `go.mod`. Both pass the shared contract suite with no assertion skipped, and
  both report page-unit billing on `Recognition.Usage`.

- **One retry when a reply is malformed.** A model that returns a string where
  a number belongs made a formatting mistake, and is asked once more, shown its
  own validation failures. The document is **not** re-sent — the model has
  already read it — so the second request is short.

  The retry is deliberately reluctant. A value that broke a `min`, `max`,
  `enum` or `format` rule is the document disagreeing with the schema, and
  asking again could only invite the model to invent something that satisfies
  the rule, which is rule §8.5's cardinal sin. A second reply that is no better
  than the first is discarded. `Metadata.Retried` reports whether it happened.

- **`Recognition.Layout`.** Tables and key-value pairs now cross the OCR seam
  in a normalised form — `Layout`, `Table`, `Cell`, `Pair`, `Region`, `Ref` and
  `CellKind` — instead of being discarded into `Raw`. The field is a pointer
  because an empty layout and no layout are different facts: a provider that
  looked and found no tables is not a provider that does not look.

  `Ref` is the loggable form of a claim about a table — "page 4, table 1, row
  3, column 2" — so a provenance entry or a review interface can say which
  value it means without repeating the value.
  [ADR-0009](docs/adr/0009-ocr-seam.md) carries a note, since this reverses a
  cost that ADR accepted.

- **A `Makefile`, and CI that calls it.** Every command this repository runs is
  now a target — `make` on its own lists them — and
  [`ci.yml`](.github/workflows/ci.yml) invokes those targets rather than
  restating the commands. `make check` is the contributor gate; `make ci` adds
  the coverage floor, the zero-dependency assertion and the cgo-free
  cross-compile.

  The command set previously existed in four places: `CONTRIBUTING.md`,
  `AGENTS.md`, the pull-request template and the workflow. They are now one
  definition and three references to it, and `scripts/check-docs.py` fails the
  build if the README and the Makefile disagree about which targets exist.

- **Docker.** `make docker-ci` runs the whole gate in a container pinning Go,
  Python, `golangci-lint` and `govulncheck`. `make docker-shell` gives a shell
  with the toolchain and your checkout mounted.

  The image installs Tesseract's English language data, which makes the six
  engine-backed tests in `ocr/tesseract` run instead of skipping — they had
  never run anywhere, including in CI. `make docker-test-offline` runs the
  suite with `--network=none`, turning `docs/validating.md`'s claim that the
  default suite needs no network into something that either passes or does not.

- **`make release-check VERSION=vX.Y.Z`.** `RELEASING.md` documented
  `scripts/release.sh` in detail; the file never existed. The target does what
  that section described — checks the tree is clean, the tag is free, the
  changelog has a section, no module carries a `replace` directive and no
  dependency sits at a bare `v0.0.0` — and, as documented, never tags and never
  pushes.

- **Circuit breaking**, as a decorator. `BreakOCR` and `BreakModel` stop asking
  a provider that has failed N times in a row, cool off, then admit exactly one
  trial call — a provider that is still down should cost one request to
  discover, not a thundering herd. They refuse with `ErrUnavailable`
  specifically, because that is a condition a chain advances past, which is the
  point of putting a breaker inside one. Failures a cooldown cannot fix — a bad
  credential, a request no provider will accept — do not open it, or "your key
  is wrong" turns into "the circuit breaker is open".

- **`ExtractBatch`.** Many sources at once, bounded by `WithConcurrency`,
  results in input order, one document's failure isolated to that document. A
  loop that stops at the first bad scan in a thousand-file directory has thrown
  away everything that worked.

- **[ADR-0031](docs/adr/0031-documents-are-read-whole.md).** Documents are read
  whole; streaming is deferred, with its reasons written down rather than left
  as an unexplained open item.

- **`WithConcurrency` did nothing at all.** It set a config field nothing read,
  and the pipeline contained no goroutine, so a fifty-page scan made fifty
  serial OCR round-trips while `docs/architecture.md` promised
  `min(4, GOMAXPROCS)` at a time. Page acquisition is now genuinely concurrent,
  bounded by the option, order-preserving and cancellable.

- **The 0.35 ceiling on a fabricated value could never bind.** `CapUngrounded`
  required both that grounding had run and that it had not, so the library's
  headline safety property was unreachable and the worked example in
  `docs/explainability.md` described arithmetic that could not occur. It now
  produces exactly the documented 0.35.

- **A data race on a shared `Client`.** `WithCrossField` appends to the one
  slice in the configuration and `Extract` copies that configuration shallowly,
  so with spare capacity two concurrent extractions each adding a per-call rule
  wrote the same backing array slot — and each evaluated the other's rule.

- **`Result.Confidence` could not be reproduced from `Result.Fields`.** It
  averaged a snapshot taken during the field walk, but cross-field rules run
  afterwards and rescore the fields they read; a document with a failing rule
  reported 0.87 while its own fields averaged 0.78.

- **Provider chains never reported through the hook**, although `chain.go` and
  [ADR-0018](docs/adr/0018-fallback-is-a-decorator.md) both promised it. When a
  later provider succeeded the earlier failures were discarded, so "a system
  running on its worst provider for three weeks with nobody aware" — the exact
  failure the promise was about — happened invisibly. `OCRChain` and
  `ModelChain` also had no tests at all.

- **OCR cost never reached `Metadata.Usage`.** Every adapter filled
  `Recognition.Usage` and the pipeline discarded it, so `PageUnits` was
  structurally always zero, the OpenTelemetry page-unit metric was flat, and
  the evaluation harness's page-unit price could not produce a number.

- **An ambiguous date scored as though it were not a date.**
  `validate.FormatSignal` had the branch, the assembler called it and threw the
  answer away, and the scorer could not see ambiguity through `[]RuleResult`
  because the rule had passed. `FieldEvidence.Ambiguous` carries it, keeping
  `docs/schema.md`'s promise that the signal does not drop to zero.

- A source file that does not exist, or cannot be read, is now `ErrNoContent`
  rather than `ErrInternal`. `ErrInternal` means "file a bug against ovrin",
  and a typo in a path is the caller's to fix.

- Suspicious-content detection was keyed on the page a value was grounded to.
  An ungrounded value has no page, so exactly the fields an injection produces
  were the ones never flagged. It is now document-wide.

- `ocr/google`, `model/skyl`, `otel`, `render/pdfium` and `examples/receipt`
  could not be built outside the development workspace, which is how CI builds
  them.

- `render/pdfium`'s cancellation test timed the *first* render, which compiles
  four megabytes of WebAssembly — around eighteen seconds under `-race` against
  a warm render's one. It sized its cancellation window from that, cancelled
  long after the render had finished, and then failed claiming cancellation was
  ignored. It now times a warm render, which is what it always meant to.

- Nine lint findings across four modules, none of which had ever been reported
  because `golangci-lint` had not been run: three ineffectual assignments in
  `ocr/azure`, a comment in `ocr/tesseract` that read as a malformed
  `go:embed` directive, an error compared with `!=` instead of `errors.Is` in
  `ocr/textract`, a field-by-field struct literal that should be a conversion,
  and three unchecked error returns in `render/pdfium` that were deliberate but
  did not say so.

- `.github/dependabot.yml` was missing `/ocr/azure`, `/ocr/textract` and
  `/examples/receipt` — three of the nine modules got no dependency updates.
  The file's own comment warned that CI would not catch this.

- `go build ./...` inside `examples/receipt` wrote a 9MB binary into the tree,
  which is how one reached git six times. `make build` uses `-o /dev/null`:
  the same compile and link, without the output.

### Notes

- No release has been made. The install commands in the README will not work
  yet.
- The Go API in the documentation is a specification, not a description. It
  will change as it meets real documents.
- No accuracy figure is published, and none will be until the evaluation
  harness can reproduce it
  ([ADR-0023](docs/adr/0023-evaluation-corpus.md)).
- Confidence weights are provisional. Confidence is documented as a ranking
  signal, not a probability, until it is calibrated.

[Unreleased]: https://github.com/BAGOMBEKA-JOB-DEV/ovrin/commits/main
