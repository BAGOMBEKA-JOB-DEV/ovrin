# ADR-0029: The v0.1 scope, corrected

**Status:** Accepted · **Date:** 2026-08-26 · **Amends** [ADR-0006](0006-tag-grammar.md)

## Context

An audit before implementation found that the guides and the scope documents
describe two different products.

[`docs/getting-started.md`](../getting-started.md) opens by saying it is
"written to be executable the day v0.1 exists". The README's headline example
uses `enum=UGX|USD|EUR|GBP`. The README's most persuasive section — "ask why",
where a confidence decomposes into named signals — calls `res.Explain("total")`
and shows `cross_field` and `format` among the signals.
[`docs/schema.md`](../schema.md) opens with nested `Vendor` and `[]Item`.

[`docs/feature-matrix.md`](../feature-matrix.md) marks every one of those ⛔ in
v0.1, and [`docs/roadmap.md`](../roadmap.md) lists them under "Explicitly not in
v0.1". So the guides demonstrate a product the scope documents say will not
exist, and the sentence claiming otherwise is the one it breaks.

The scope documents also disagree with each other. The roadmap excludes "nested
schemas **beyond one level**", implying one level is included; the feature
matrix says "flat structs" ✅ and "nested structs, slices" ⛔, implying none.
And [ADR-0006](0006-tag-grammar.md) states plainly: *"The rule vocabulary for
v0.1 is `required`, `min`, `max`, `format` and `enum`"* — which contradicts both.

An ADR outranks a guide, so ADR-0006 already settled `format` and `enum`. The
rest was never settled at all.

## Decision

The v0.1 column was written too conservatively. Corrected by moving into v0.1
everything that is cheap, already decided, or load-bearing for the first
example a user reads:

**Into v0.1:**

| Feature | Why |
|---|---|
| `format` and `enum` rules | Already the decision in ADR-0006. Modest work, high value. |
| Nested structs and slices, to full depth | Invoices have line items. Without them this extracts flat key-value pairs, which is the difference between a library and a demonstration. |
| `Explain` | [ADR-0016](0016-explain-returns-data.md) already establishes it as "a view, not a second source of truth", assembled from data the pipeline records anyway. Withholding it means collecting the evidence and refusing to show it. |
| The `cross_field` and `format` signals | Both appear in the README's worked arithmetic. A confidence that omits them does not sum to the published example. |

**Deferred, and marked in the guides** so a reader always knows what works now:

| Feature | Version |
|---|---|
| Provider fallback chains | v0.2 |
| Local rasterising (`render/pdfium`) | v0.2 |
| The `otel` module | v0.2 |
| WebP input | v0.2 |
| Two readings, `ModeBoth`, cross-validation | v0.3 |

Guide sections covering a deferred feature carry a version marker. The claim in
`getting-started.md` that it is executable the day v0.1 exists is corrected to
name the exceptions.

The anti-drift harness gains a corresponding check: **no unmarked Go example may
use a feature the matrix marks ⛔ for the current version.** The contradiction
this ADR resolves was found by a human reading two documents side by side, which
is not a process that scales.

## Consequences

**Good.** The README's headline example works on the first release, which is the
one thing a first release has to get right. `Explain` — the feature that
distinguishes ovrin from a wrapper around a model call — is present when people
first look. The scope documents stop contradicting each other and stop
contradicting ADR-0006. And the harness check means this class of drift is
caught mechanically from now on.

**Bad.** v0.1 is meaningfully larger and later. Nested structs and slices are
real work across schema reflection, JSON Schema emission and field-key naming,
and slices complicate `Fields` enough to need their own amendment
([ADR-0004](0004-partial-results.md) had to be corrected for it). Version
markers put scaffolding in prose that is otherwise clean, and they rot the day a
feature ships unless something checks them — which is why the check is not
optional. And a bigger v0.1 means longer before any of it meets a real document,
which is the thing that actually validates the design.

## Alternatives considered

- **Shrink the guides to match the matrix.** Rejected: the README's most
  compelling section would be deleted to protect a scope decision made in
  passing, and a quickstart that cannot show a nested invoice is not a
  quickstart for document extraction.
- **Grow v0.1 to match the guides entirely** — including two readings, fallback,
  rasterising and otel. Rejected: that is most of v0.3, it pulls in the renderer
  with all of [ADR-0010](0010-no-cgo-in-core.md)'s difficulty, and it delays
  first contact with real documents by months for features nobody has asked for
  yet.
- **Ship v0.1 as documented and treat the guides as aspirational.** Rejected:
  documentation that describes software that does not exist is the failure this
  project's whole documentation-first approach was supposed to avoid.
