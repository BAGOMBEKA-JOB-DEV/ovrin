# Project plan

Current state, what is blocked, and on what. Updated as work lands. For what
gets built and in what order, see [`roadmap.md`](roadmap.md).

**Last updated:** 2026-08-26

---

## Where the project is

**Implemented through v0.3. Unmeasured.**

| Artefact | State |
|---|---|
| Architecture decision records | **30**, all Accepted |
| Go modules | **9** — the core, six adapters, `otel`, and `examples/receipt` |
| Go source | ~40,000 lines outside tests, ~31,000 lines of tests |
| Core dependencies | **Zero**, asserted in CI |
| Public API surface | `api/ovrin.txt`, generated, **343** entries |
| Pipeline | All nine stages run end to end — [`pipeline.md`](pipeline.md) |
| Schema grammar | Implemented — [`schema.md`](schema.md) |
| Confidence model | Implemented, weights **still provisional** — [`confidence.md`](confidence.md) |
| Threat model | Complete, mitigations in code — [`threat-model.md`](threat-model.md) |
| Engineering rules | Complete — [`rules.md`](rules.md) |
| Community health files | Complete |
| CI workflows | All jobs run: build, vet, race, tidy, lint, `govulncheck`, coverage floor, cgo-free cross-compile, docs |
| Fuzzing | 9 targets, run on demand with `make fuzz` |
| Documentation checks | `scripts/check-docs.py`, green |
| Evaluation corpus | **25 documents**, five per category — all `source: synthetic` |
| Evaluation reports | **None.** `eval/report/` holds a placeholder no-run report |
| Release tags | **None.** Adapters `require` versions that do not exist and `replace` them with the checkout |

The design was written before the code deliberately. The alternative — building
the pipeline and discovering during implementation that confidence cannot come
from logprobs, or that the seam has to own prompt construction for the security
property to hold — would have produced a worse design and a rewrite.

That approach had a cost, and it was paid once. An audit before writing code
found **27 contradictions** between documents — `Extract`'s signature against
its own call sites, sentinels used but never defined, fourteen types referred
to but never declared. All were resolved, four of them by new decision records
([ADR-0026](adr/0026-extract-takes-per-call-options.md) through
[ADR-0029](adr/0029-v01-scope-corrected.md)). The lesson is in
`scripts/check-docs.py`: prose that nothing checks will drift, so now something
checks it.

The shape of the work has now inverted. Writing the code was the part with a
finite end; establishing that it is *right* is the part that is open-ended, and
that is what everything below is about.

---

## Blocked

| Item | Blocked on | Owner |
|---|---|---|
| Any accuracy statement | Every corpus document is synthetic; nothing has been run against a real one | maintainer, contributors |
| Confidence calibration | A real corpus, then a calibration run | — |
| v1.0 | All four [ADR-0024](adr/0024-versioning-and-stability.md) conditions; none is established yet | — |

The first two are small, mechanical and entirely within the maintainer's
control. The third is the one that matters and the one that will take longest,
and it is now the only thing standing between the library and an honest
accuracy claim.

---

## Immediate next steps

In order. Each is a prerequisite for the one after it in the same group.

**1. Cut the first release.** The name is no longer a blocker — the repository
was renamed to `ovrin` on 2026-08-26 and the module path resolves.
- Date the `[Unreleased]` section in `CHANGELOG.md` as `[0.3.0]`
- `make release-check VERSION=v0.3.0`, then tag and push by hand
- Wait for the proxy, then tag each adapter, then `examples/receipt`
- Drop each module's `replace` directive as its turn comes

**2. Get real documents into the corpus.**
- Public government and regulator forms first: they are redistributable, they
  are the documents this library was written for, and they are free
- Then donated documents with written permission and synthetic personal data
  substituted (rule [§7.6](rules.md#7-untrusted-input))
- The synthetic corpus stays. It is deterministic, offline and free, which
  makes it the right thing for regression testing; it is simply not evidence
  about real documents

**3. Run the harness and commit the report.**
- `make eval` against at least two provider generations
  ([ADR-0023](adr/0023-evaluation-corpus.md))
- Commit the report whatever it says. A public record of how wrong we are is
  the only kind that stays honest
- This is the point at which every ⚠️ in [`feature-matrix.md`](feature-matrix.md)
  stops being a prediction

**4. Calibrate the confidence weights.**
- They are provisional by their own ADR
  ([ADR-0013](adr/0013-multi-signal-confidence.md)) and the code says so
- Publish the expected calibration error and the accuracy within each band.
  Until then confidence is documented as a ranking signal, not a probability

**5. Find a production deployment that is not the maintainer's.**
- The condition nobody can plan and the one that will teach the most
- Its feedback is what turns "no known API change we would want to make" from
  an absence of evidence into evidence

**6. Publish per-stage benchmarks.**
- Only `render/pdfium` has any today (`make bench`)
- Not a v1.0 condition, but the thing most likely to surprise an adopter

---

## Risks

Named honestly, because a plan that lists none has not been thought about.

| Risk | Likelihood | Impact | Response |
|---|---|---|---|
| **In-tree PDF extraction is wrong on documents we have not seen** | high | high | The schedule risk is spent — it shipped. What replaces it is correctness on PDFs nobody here has looked at, which only real documents and the fuzz targets will find. The `pdfcpu`-backed fallback module is still available and the seam is still shaped for it. |
| **Corpus stays synthetic because redistributable real documents are hard to source** | high | high | Every accuracy claim stays unmakeable, and v1.0 does not ship. The synthetic half is done and is what a regression test needs; public government forms are the next cheapest source. Accept that the corpus will be unrepresentative before it is representative. |
| **Confidence weights never get calibrated** | medium | high | v1.0 does not ship. Confidence stays documented as a ranking signal — which is honest, and much less useful. |
| **Single maintainer** | certain | high | Stated in [`MAINTAINERS.md`](../MAINTAINERS.md). Bus factor is one. Apache-2.0 and a documented architecture mean a fork is viable. |
| **A provider changes its wire format and the sandbox becomes fiction** | medium | medium | Integration tier catches it, and it runs rarely. Schedule a monthly integration run. |
| **The design is wrong somewhere load-bearing** | medium | medium | ADRs are supersedable and numbered permanently. Amending a decision after contact with reality is the system working. |
| **Scope creep into a platform** | medium | medium | The non-goals in [`idea.md`](idea.md) and the deferred list in [`roadmap.md`](roadmap.md) exist to be cited when this happens. |

---

## How to tell whether this is going well

Signals worth more than a burndown chart:

- **The corpus gains documents nobody in this project wrote.** Synthetic
  documents measure the pipeline against itself; real ones are the only thing
  that converts opinion into measurement.
- **ADRs get superseded.** It means the design is meeting reality. A set of
  ADRs that never changes is a set nobody is testing.
- **The feature matrix gains ⚠️ rows.** Discovering what a provider silently
  ignores is progress, and hiding it is not.
- **Committed evaluation reports move in the right direction**, and are
  committed even when they do not.
