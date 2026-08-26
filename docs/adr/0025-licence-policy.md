# ADR-0025: Apache-2.0, and no AGPL dependencies

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin's intended users are government agencies, banks, insurers, hospitals and
schools, mostly through closed-source internal systems. Whether they can use it
is decided by a lawyer reading a licence, usually before anyone reads the code.

The document-processing ecosystem is unusually contaminated with copyleft, and
the strongest tools are the affected ones. UniPDF is the most capable Go PDF
library by a distance and is AGPL-or-paid. MuPDF, which backs the fastest
rasterisers, is AGPL. Ghostscript is AGPL. Each is technically the best
available option in its category, and each would make ovrin unusable for its
intended audience.

The AGPL's network clause is what does the damage: deploying a service that
uses AGPL code over a network triggers the source-disclosure obligation. A bank
running document extraction as an internal service is squarely inside it. The
obligation is transitive, so an AGPL dependency three levels down in an
optional adapter still lands on the user.

Ovrin's `LICENSE` is already Apache-2.0. This ADR records why, and what it
implies for dependencies.

## Decision

**Ovrin is licensed Apache-2.0**, in every module.

Apache-2.0 over MIT for two specific reasons: it grants patent rights
explicitly, which corporate legal review looks for and MIT does not provide;
and it includes a trademark clause, which matters given the naming history in
[ADR-0001](0001-name-and-module-path.md).

**No AGPL or GPL dependency in any module** (rule
[§4.4](../rules.md#4-dependencies)) — core, adapter, tool or test. Check the
licence before the benchmark; a disqualified dependency does not need
evaluating on merit.

The permitted set: **Apache-2.0, BSD (2 and 3 clause), MIT, ISC, MPL-2.0.**
MPL-2.0 is file-level copyleft and does not reach across a module boundary,
which is acceptable. Anything else requires an ADR.

Concrete consequences already binding on decisions made elsewhere:

| Excluded | Licence | Recorded in |
|---|---|---|
| UniPDF | AGPL or paid | [ADR-0011](0011-pdf-text-extraction.md) |
| MuPDF / `lazypdf` | AGPL | [ADR-0010](0010-no-cgo-in-core.md) |
| Ghostscript | AGPL | [ADR-0010](0010-no-cgo-in-core.md) |

Tesseract (Apache-2.0), PDFium (BSD-3-Clause) and Wazero (Apache-2.0) are all
permitted, which is why the recommended stack is built from them.

**Evaluation corpus documents must be licensed for redistribution**
([ADR-0023](0023-evaluation-corpus.md)). A document is a copyrighted work and
committing one without permission is an infringement that ships to everyone who
clones the repository.

A `NOTICE` file records attributions as required by Apache-2.0, and dependency
licences are listed there per module.

## Consequences

**Good.** Ovrin is adoptable by its intended users without a legal review that
ends in refusal. The patent grant removes the objection most likely to stop a
corporate adoption. The dependency rule is a fast filter — one field in a
`go.mod` decides it, before anyone spends a week on an integration that has to
be unwound.

**Bad.** It excludes the best-in-class tool in two categories, and
[ADR-0011](0011-pdf-text-extraction.md) pays for it directly: writing PDF text
extraction in-tree is the single largest piece of v0.1 work, and it exists
partly because UniPDF is unavailable. Rasterising is slower for the same
reason. Apache-2.0 also permits a well-resourced company to build a commercial
product on ovrin and contribute nothing — that is a real cost of permissive
licensing, accepted because the alternative excludes the users this exists for.
And the corpus constraint keeps out exactly the messy real documents that would
be most useful to test against.

**Neutral.** Contributions are accepted under the Developer Certificate of
Origin, signed off per commit
(rule [§10.4](../rules.md#10-git)), rather than under a contributor licence
agreement. A CLA would allow relicensing later; it also deters casual
contribution, and there is no relicensing plan.

## Alternatives considered

- **MIT.** Rejected: no explicit patent grant, which is a specific objection in
  corporate review, and no trademark clause.
- **AGPL for ovrin itself.** Rejected: it would exclude nearly every intended
  user. Appropriate for products seeking dual-licensing revenue; ovrin is not
  one.
- **Apache-2.0 core with AGPL optional adapters.** Rejected: an optional
  adapter is still a dependency the user deploys, and "optional" is not a
  defence under the network clause. It also makes the licence answer
  conditional, which is worse than a clear no.
- **Require a contributor licence agreement.** Rejected: deters contribution
  for a benefit — future relicensing — that is not planned.
