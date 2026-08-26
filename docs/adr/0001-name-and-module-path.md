# ADR-0001: The project is named ovrin

**Status:** Accepted · **Date:** 2026-08-26

## Context

The project began under the name **vellum** — a writing surface made from
prepared animal skin, chosen because it connects to documents without
containing the words "document", "extract" or "AI". A pre-commitment
availability check found three separate collisions, each disqualifying on its
own.

`github.com/blevesearch/vellum` is a finite-state-transducer library whose Go
package is literally named `vellum`. It is vendored by Bleve, Couchbase and
M3DB, so a meaningful number of Go programs already have `vellum` bound in
their file scope. Those programs could still import ours, but only with a
rename directive at every call site.

`vellum.ai` is an active commercial LLM development platform. It is not in an
adjacent market — it publishes directly on document data extraction, which is
this project's entire subject. Any search for "vellum document extraction"
belongs to them and always will.

"VELLUM" is additionally a registered United States word mark in the computer
and software services class (registration 7109496). Nominative use of a
descriptive Latin-derived word in an Apache-2.0 library is defensible, but
"defensible" means "you may have to defend it", which is not a position an
unfunded open-source project should choose voluntarily.

A sweep over replacement candidates checked, for each: exact package-name
matches on pkg.go.dev, existing software projects on GitHub, and `.dev`/`.io`
registration. `uncial` came back clean on every axis; `quire` and `octavo` came
back clean in Go but contested elsewhere; `verso`, `rubric`, `incipit`,
`colophon` and `deckle` each had live Go modules, `deckle` being a PDF
generation API.

The maintainer selected **ovrin**, an invented word. It was checked on the same
axes.

## Decision

The project is named **ovrin**. The module path is
`github.com/BAGOMBEKA-JOB-DEV/ovrin` and the package name is `ovrin`. The
struct tag key is `ovrin`.

The findings on the name, recorded so that a future reader is not surprised:

| Axis | Finding |
|---|---|
| pkg.go.dev exact matches | **None.** Zero modules. |
| Go package-name collision | **None.** |
| `ovrin.io`, `ovrin.ai` | Unregistered at time of writing. |
| `ovrin.com`, `ovrin.dev` | Registered. |
| Other software | OVRIN Labs (`ovrinlabs.ca`), "sovereign intelligence infrastructure" — AI infrastructure, not document extraction. |
| Company registers | OVRIN LTD, United Kingdom, company 16750460. |

The Go namespace — the one that determines whether a user can type
`import "github.com/BAGOMBEKA-JOB-DEV/ovrin"` without a rename — is completely
clear. That is the collision that would have cost users something, and it is
absent.

## Consequences

**Good.** No Go package-name collision, so no downstream program needs an
import alias. No competing product in document extraction, so search results
belong to us as soon as there are any. Two sensible domains are available. An
invented word is the strongest kind of trademark and the easiest to clear.

**Bad.** An invented word means nothing to anybody, so the README must do all
the work the name used to do; "vellum" told a reader something before they
read a word of prose, and "ovrin" tells them nothing. OVRIN Labs occupies
adjacent AI-infrastructure territory, which is close enough that some search
confusion is likely even though the categories differ. The two obvious domains
are gone. And the name has the shape of the invented-startup-name pattern the
project originally set out to avoid.

The GitHub repository is still named `vellum` and must be renamed before the
module path resolves. That is the maintainer's action, tracked in
[`docs/project-plan.md`](../project-plan.md).

## Alternatives considered

- **Keep vellum.** Rejected: the `blevesearch/vellum` package-name clash taxes
  downstream users at every call site, and competing with `vellum.ai` for the
  exact phrase "document extraction" is unwinnable.
- **uncial** — the majuscule script of late-antique manuscripts. The cleanest
  candidate found: zero Go modules, zero software projects, both domains free.
  Rejected by the maintainer. Obscure, and ambiguously pronounced.
- **quire** — the gathering of folded sheets a manuscript is built from. Clean
  in Go, but Quire.io is an established task-management product and Getty's
  Quire is a publishing tool.
- **octavo** — a book format. Go namespace effectively free (one abandoned,
  unlicensed 2019 module) but several non-Go projects use the name.
- **Keep the brand, rename the package.** Rejected: in Go the last element of
  the module path should match the package name, and violating that convention
  to preserve a contested brand trades a small problem for a permanent one.
