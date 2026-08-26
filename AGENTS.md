# Coding Agent Kickoff — ovrin (`BAGOMBEKA-JOB-DEV/ovrin`)

> This file is the operating contract for any AI coding agent or new
> contributor working in this repository. Read it fully before your first edit.

Ovrin is a Go library that turns documents — PDFs, scans, images — into
validated, typed Go structs with per-field confidence and provenance. It is a
public, Apache-2.0, pre-v1 open-source library that other people's production
systems will depend on.

**Status check (read before assuming).** As of 2026-08-26 this repository
contains **documentation only**. There is no `go.mod`, no Go source and no
release. If you find code here, this paragraph is stale — check
[`docs/project-plan.md`](docs/project-plan.md) for current state before
trusting anything else in this file.

**Precedence.** If this file conflicts with something in `docs/`, this file
wins for *conventions* and `docs/` wins for *decisions*. An ADR beats both.

---

## 1. Your role

You are a **senior Go library engineer**. The stack is locked: Go 1.22 or
newer, standard library only in the core module, no cgo in the core, Apache-2.0.

You are **not** allowed to invent requirements. Twenty-five architecture
decision records already settle the load-bearing questions. If a task seems to
require contradicting one, stop and say so — the answer is a superseding ADR,
not a quiet deviation.

You are writing a library, not an application. Every exported identifier is a
promise to strangers.

---

## 2. Authoritative references — read these *before* you touch a keyboard

| # | Path | Why |
|---|---|---|
| 1 | [`docs/rules.md`](docs/rules.md) | The engineering rules. CI enforces most; review enforces the rest. Everything below cites its § numbers. |
| 2 | [`docs/adr/README.md`](docs/adr/README.md) | Index of 25 decisions. Read the ones your task touches. |
| 3 | [`docs/architecture.md`](docs/architecture.md) | Modules, seams, and which way dependencies point |
| 4 | [`docs/pipeline.md`](docs/pipeline.md) | The nine stages and what each one owes the next |
| 5 | [`docs/schema.md`](docs/schema.md) | The tag grammar — the spec, not a summary |
| 6 | [`docs/confidence.md`](docs/confidence.md) | Signals, weights, floors |
| 7 | [`docs/threat-model.md`](docs/threat-model.md) | Read before touching parsing, normalisation or prompting |
| 8 | [`docs/providers.md`](docs/providers.md) | Read before touching an adapter |
| 9 | [`CONTRIBUTING.md`](CONTRIBUTING.md) | Branch, commit and PR mechanics |

The single most important is `docs/rules.md`. If you read only one, read that.

---

## 3. Non-negotiable rules

Violating any of these means the change is rejected.

1. **The core module has zero external dependencies.** Not few — zero. Adding
   one requires an ADR explaining what could not be written in a hundred lines
   of standard library. (§4.1)

2. **No cgo in the core, ever.** `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build
   ./...` must keep working. (§4.3)

3. **No AGPL or GPL dependency anywhere**, including in adapter modules. This
   excludes UniPDF, MuPDF and Ghostscript regardless of technical merit. Check
   the licence before the benchmark. (§4.4, ADR-0025)

4. **Document content never reaches an error, event, trace, metric or log.**
   Errors carry operation, page, field and provider. The `Event` struct has no
   field a value could occupy, and it stays that way. (§2.5, §7.5)

5. **Document text is data, never instruction.** Never concatenate document
   content into an instruction region. The separation is a security boundary,
   not a style preference. (§7.2, ADR-0017)

6. **Never fabricate a value to satisfy a schema.** A field that could not be
   found is absent and marked absent. Returning a plausible zero because the
   struct has a `float64` in it is the single worst thing this library could
   do. (§8.5)

7. **A failed field is not a failed extraction.** Errors are for conditions
   that make the whole result meaningless. Everything else goes on the field.
   (§2.6, ADR-0004)

8. **Every limit has a finite default, checked before allocation.** Not after.
   (§5.2, ADR-0020)

9. **Adapters map; they do not decide.** No retry, fallback, timeout, limit or
   prompt construction in an adapter. (§6.2)

10. **Never silently drop data.** If something cannot be represented, return
    `ErrUnsupported` naming it. (§6.1)

11. **Classify errors; never branch on message text.** (§2.2)

12. **No network in the default test suite.** `httptest` or the sandbox. (§3.3)

13. **No global state.** No registry, no default client, no `init` side
    effects. (§5.5)

14. **Every exported symbol has a doc comment** starting with its name, saying
    *why* and not only *what*. (§1.1, §9.2)

15. **A decision that shapes the code gets an ADR, and the ADR names its
    costs.** An ADR that lists no downsides has not finished thinking. (§9.4)

16. **Never claim an accuracy figure that `go test -tags=eval` cannot
    reproduce.** Not in code comments, not in the README, not anywhere. (§3.8)

---

## 4. Repository layout

```text
ovrin/                     module github.com/BAGOMBEKA-JOB-DEV/ovrin
│                          zero dependencies · no cgo · Go 1.22
├── ovrin.go               Client, Option, New, Extract[T]
├── result.go              Result[T], FieldResult, Candidate, Explanation
├── source.go              Source, Document, Kind, detection
├── schema.go              struct-tag reflection
├── model.go   ocr.go   render.go      THE SEAMS — CODEOWNER-protected
├── chain.go               OCRChain, ModelChain
├── confidence.go          Signal, Scorer
├── provenance.go          Provenance, Rect, Span
├── limits.go  hook.go  errors.go
│
├── internal/              ← put implementation HERE by default
│   ├── pdf/               text-layer extraction
│   ├── pipeline/          stage orchestration
│   ├── prompt/            instruction construction — SECURITY BOUNDARY
│   ├── normalise/         offset-preserving normalisation
│   ├── validate/  ground/  jsonschema/  img/
│   ├── adaptertest/       the shared contract suite
│   └── sandbox/  testutil/
│
├── model/skyl/   ocr/tesseract/   ocr/google/   ocr/aws/
├── render/pdfium/   otel/         ← each its own go.mod
├── eval/                  corpus and harness
└── docs/
```

**Where does my change go?**

| Change | Location |
|---|---|
| New behaviour in the pipeline | `internal/pipeline/` — not the root package |
| A new public type | Root, and only with a reason. Default is `internal/` (§1.7) |
| A new provider | `ocr/<name>/` or `model/<name>/`, **its own `go.mod`** |
| Anything needing a dependency | An adapter module. Never the core (§4.1) |
| A new validation rule | `internal/validate/` + the vocabulary in `docs/schema.md` |
| A new confidence signal | `confidence.go` + `docs/confidence.md` + ADR-0013 |
| Prompt changes | `internal/prompt/` — read ADR-0017 first |

---

## 5. Standard workflow for **every** task

1. **Read the ADRs your task touches.** Use the index. If your task
   contradicts one, stop and raise it.
2. **Branch:** `feat/…`, `fix/…`, `docs/…`, `test/…`, `refactor/…`, `chore/…`
3. **Write the test first** where the behaviour is testable without a provider
   — schema reflection, validation, normalisation and grounding all are.
4. **Implement.** Smallest change that is correct.
5. **Update the documentation in the same commit.** A code change that makes a
   doc wrong is an incomplete change.
6. **Run the local gate:**

   ```bash
   gofmt -l .                     # must print nothing
   go build ./...
   go vet ./...
   go vet -tags=sandbox ./...
   go vet -tags=integration ./...
   go test -count=1 -race ./...
   go test -count=1 -race -tags=sandbox ./...
   go mod tidy && git diff --exit-code    # must be clean
   golangci-lint run
   govulncheck ./...
   ```

   Repeat per module. CI runs each module at its declared Go floor **and** at
   the newest release.

7. **Commit** with a Conventional Commit subject and `-s` to sign off.

---

## 6. Common pitfalls — the mistakes you will be tempted to make

| Anti-pattern | Why it fails | Correct move |
|---|---|---|
| `go get` a helpful utility for the core | Breaks §4.1 permanently for every user | Write it, or put it in an adapter module |
| Returning a zero value for an unreadable field | Indistinguishable from a real zero. Causes wrong payments | `Found: false` (§8.5) |
| Returning an error because one field failed | Discards eleven good fields | Record it on the field (§2.6) |
| Concatenating document text into the instruction | Removes the injection boundary | Keep it in `Content` (§7.2) |
| Putting an extracted value in an error to help debugging | Ships a medical record to five log systems | Field name and page only (§2.5) |
| Adding a retry loop to an adapter | Two retry policies that disagree | Retry belongs to the core (§6.2) |
| Adding a `map[string]any` to `Event` "for flexibility" | Reopens the exfiltration route ADR-0021 closed structurally | Add a typed scalar field, or nothing |
| Exporting a type "so it can be tested" | Permanent API for a temporary need | Test it in-package, or via the contract suite |
| Branching on `strings.Contains(err.Error(), …)` | Breaks when a vendor rewords a message | Classify to a sentinel (§2.2) |
| Writing an ADR with only benefits | It is a press release (§9.4) | Name the costs. Every decision has some |
| Tuning a confidence weight because it "feels better" | Unmeasured change to the most consequential number | Run the eval harness, or do not change it |
| Using `pdfcpu`/`unipdf` "just for this bit" | §4.1, and `unipdf` is AGPL | `internal/pdf/`, ADR-0011 |

---

## 7. Definition of Done — per task

- [ ] The relevant ADRs were read, and nothing contradicts them
- [ ] `gofmt`, `go vet` (all tags), `golangci-lint`, `govulncheck` clean
- [ ] `go test -race` and `-tags=sandbox` pass, in every affected module
- [ ] `go mod tidy` leaves no diff
- [ ] New exported symbols have doc comments that say *why* (§1.1, §9.2)
- [ ] Tests are table-driven with names that identify the case (§3.2)
- [ ] Any goroutine started is asserted to stop (§3.6)
- [ ] Coverage floor not lowered (§3.7)
- [ ] Documentation updated in the same commit
- [ ] `CHANGELOG.md` `[Unreleased]` updated for any user-visible change
- [ ] A new decision has an ADR, and the ADR names its costs (§9.4)
- [ ] Commit is a Conventional Commit and is signed off (§10.1, §10.4)

---

## 8. Security posture — what you must preserve

Ovrin accepts attacker-controlled input and hands it to a system that follows
instructions. Four properties hold today. Do not weaken any of them; if a task
appears to require it, **stop and raise it**.

1. **Structural separation of instruction and content.** Built in the core so
   it holds identically across every provider (ADR-0007, ADR-0017).
2. **Schema-constrained output.** The output shape is fixed before the document
   is read, so an injected instruction cannot change it. This is the strongest
   mitigation in the system.
3. **Finite limits, enforced before allocation** (ADR-0020).
4. **No egress path for document content except a configured provider.** The
   `Event` struct enforces this by having nowhere to put one (ADR-0021).

Also: nothing a document references is ever fetched (§7.4), and committed
fixtures contain no real personal data (§7.6).

---

## 9. What to do **right now** when you pick up a task

1. Check [`docs/project-plan.md`](docs/project-plan.md) for current state and
   what is blocked. Much of this repository describes software that does not
   exist yet.
2. Read [`docs/rules.md`](docs/rules.md) in full. It is 340 lines and it is the
   contract.
3. Read the ADRs your task touches, from [`docs/adr/README.md`](docs/adr/README.md).
4. Search for an existing pattern before inventing one. The sibling project
   `skyl` (`github.com/BAGOMBEKA-JOB-DEV/skyl`) is where these conventions come
   from and is worth reading for the seam, error and testing patterns.
5. If the task is ambiguous, **ask** rather than guessing. A wrong guess in a
   library's public API is expensive to unwind.

---

## 10. Which document answers my question?

| Question | Document |
|---|---|
| How do I write this code? | [`docs/rules.md`](docs/rules.md) |
| Why is it like this? | [`docs/adr/`](docs/adr/) |
| What does the pipeline do? | [`docs/pipeline.md`](docs/pipeline.md) |
| What can a tag say? | [`docs/schema.md`](docs/schema.md) |
| What does the confidence number mean? | [`docs/confidence.md`](docs/confidence.md) |
| Is this safe? | [`docs/threat-model.md`](docs/threat-model.md) |
| Where does data go? | [`docs/data-handling.md`](docs/data-handling.md) |
| How do I write an adapter? | [`docs/providers.md`](docs/providers.md) |
| How do we know it works? | [`docs/evaluation.md`](docs/evaluation.md) |
| What is being built next? | [`docs/roadmap.md`](docs/roadmap.md) |
| What does this word mean? | [`docs/glossary.md`](docs/glossary.md) |

---

*Last updated 2026-08-26. Owner: BAGOMBEKA-JOB-DEV. Changes that alter §3, §4
or §7 require an ADR.*
