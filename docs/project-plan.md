# Project plan

Current state, what is blocked, and on what. Updated as work lands. For what
gets built and in what order, see [`roadmap.md`](roadmap.md).

**Last updated:** 2026-08-26

---

## Where the project is

**Design complete. No code.**

| Artefact | State |
|---|---|
| Architecture decision records | **29**, all Accepted |
| Pipeline specification | Complete — [`pipeline.md`](pipeline.md) |
| Schema grammar | Complete — [`schema.md`](schema.md) |
| Confidence model | Complete, weights provisional — [`confidence.md`](confidence.md) |
| Threat model | Complete — [`threat-model.md`](threat-model.md) |
| Engineering rules | Complete — [`rules.md`](rules.md) |
| Community health files | Complete |
| CI workflows | The `docs` job runs and passes; the Go jobs wait for code |
| Public API specification | `api/ovrin.txt`, hand-authored, 254 entries |
| Documentation checks | `scripts/check-docs.py`, green |
| Go source | **None** |
| `go.mod` | **None** |
| Evaluation corpus | **Empty** |

The design was written before the code deliberately. The alternative — building
the pipeline and discovering during implementation that confidence cannot come
from logprobs, or that the seam has to own prompt construction for the security
property to hold — would have produced a worse design and a rewrite.

That approach has a cost, and it has now been paid once. An audit before writing
code found **27 contradictions** between documents — `Extract`'s signature
against its own call sites, sentinels used but never defined, fourteen types
referred to but never declared. All are resolved, four of them by new decision
records ([ADR-0026](adr/0026-extract-takes-per-call-options.md) through
[ADR-0029](adr/0029-v01-scope-corrected.md)). The lesson is in
`scripts/check-docs.py`: prose that nothing checks will drift, so now something
checks it.

---

## Blocked

| Item | Blocked on | Owner |
|---|---|---|
| Module path resolving | GitHub repository still named `vellum`; must be renamed to `ovrin` | maintainer |
| ~~`model/skyl` pinning a real version~~ | **Not blocked.** `ResponseFormat` is in skyl `v0.1.0`; the earlier claim was wrong | — |
| Any accuracy statement | Evaluation corpus is empty | maintainer, contributors |
| Confidence calibration | Corpus, then a calibration run | — |
| CI proving anything | No Go code to build | — |

The first two are small, mechanical and entirely within the maintainer's
control. The third is the one that matters and the one that will take longest.

---

## Immediate next steps

In order. Each is a prerequisite for the one after it in the same group.

**1. Unblock the name.**
- Rename the GitHub repository to `ovrin`, update the remote
- `go mod init github.com/BAGOMBEKA-JOB-DEV/ovrin`, `go 1.22`
- Verify every documentation link still resolves after the rename

**2. Make CI mean something.**
- The workflows exist. Add a `doc.go` and confirm build, vet, lint, tidy and
  `govulncheck` all run green on an empty module
- A CI that has never passed is not a CI

**3. Establish the shapes.**
- `Result[T]`, `FieldResult`, `Signal`, `Provenance`, `Candidate` — types and
  godoc, no implementations
- The three seams
- Sentinels and `*Error`
- This is the point at which the design meets Go's type system and stops being
  prose. Expect ADR amendments here, and record them properly rather than
  quietly changing the docs.

**4. Schema reflection.**
- Tag parsing, the closed rule vocabulary, error cases
- Fully testable with no provider and no document — the cheapest real progress
  available

**5. PDF text extraction.**
- The largest piece of v0.1 ([ADR-0011](adr/0011-pdf-text-extraction.md))
- Structure, filters, font encodings, `ToUnicode`, positioned text
- Fuzz targets from the first commit, not retrofitted

**6. Seed the corpus.**
- Five redistributable documents per category, with ground truth
- Do this **before** the pipeline is finished, so the first end-to-end run can
  be measured rather than eyeballed

---

## Risks

Named honestly, because a plan that lists none has not been thought about.

| Risk | Likelihood | Impact | Response |
|---|---|---|---|
| **In-tree PDF extraction takes far longer than estimated** | high | high | The fallback is an optional `pdfcpu`-backed module; the seam is already shaped for it. Reconsider at the point it has eaten a month. |
| **Corpus stays empty because redistributable documents are hard to source** | high | high | Every accuracy claim becomes unmakeable. Start with synthetic-but-realistic and public government forms; accept the corpus will be unrepresentative before it is representative. |
| **Confidence weights never get calibrated** | medium | high | v1.0 does not ship. Confidence stays documented as a ranking signal — which is honest, and much less useful. |
| **Single maintainer** | certain | high | Stated in [`MAINTAINERS.md`](../MAINTAINERS.md). Bus factor is one. Apache-2.0 and a documented architecture mean a fork is viable. |
| **A provider changes its wire format and the sandbox becomes fiction** | medium | medium | Integration tier catches it, and it runs rarely. Schedule a monthly integration run. |
| **The design is wrong somewhere load-bearing** | medium | medium | ADRs are supersedable and numbered permanently. Amending a decision after contact with reality is the system working. |
| **Scope creep into a platform** | medium | medium | The non-goals in [`idea.md`](idea.md) and the deferred list in [`roadmap.md`](roadmap.md) exist to be cited when this happens. |

---

## How to tell whether this is going well

Signals worth more than a burndown chart:

- **The corpus grows.** It is the only thing that converts opinion into
  measurement.
- **ADRs get superseded.** It means the design is meeting reality. A set of
  ADRs that never changes is a set nobody is testing.
- **The feature matrix gains ⚠️ rows.** Discovering what a provider silently
  ignores is progress, and hiding it is not.
- **Committed evaluation reports move in the right direction**, and are
  committed even when they do not.
