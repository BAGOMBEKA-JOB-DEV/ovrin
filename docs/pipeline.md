# The pipeline

Nine stages between a file and a typed value. This document describes what each
one does, what it can fail on, and what it hands to the next.

The design principle behind the whole thing: **ovrin is not "send a PDF to a
model and get JSON back."** Published comparison finds a staged pipeline
consistently outperforms the direct document-to-vision-model baseline by a
substantial margin, and the staging is also what makes confidence, provenance
and cross-validation possible at all.

**Stages:** [1 Detect](#1-detect) · [2 Acquire](#2-acquire) ·
[3 Normalise](#3-normalise) · [4 Schema](#4-schema) · [5 Prompt](#5-prompt) ·
[6 Generate](#6-generate) · [7 Validate](#7-validate) · [8 Ground](#8-ground) ·
[9 Score](#9-score)

---

## 1. Detect

**In:** a `Source` — an `io.Reader`, a `[]byte`, or a path.
**Out:** a `Document` with a known `Kind` and page count.

Format is determined by content, never by file extension or a caller-supplied
MIME type. A `.pdf` that is actually a JPEG is common enough — mail systems
rename things — and trusting the name is how a parser gets handed input it was
not written for.

Limits are checked here, before anything is read into memory
([ADR-0020](adr/0020-resource-limits.md)). Source size, page count and object
count are all rejected at the door rather than after allocation.

**Fails on:** an unrecognised format (`ErrUnsupportedFormat`), encryption
(`ErrEncrypted`), a limit (`ErrLimitExceeded`).

---

## 2. Acquire

**In:** a `Document`.
**Out:** page content — text with positions, or images, or both.

This is the stage that decides how the document gets read
([ADR-0012](adr/0012-text-first-ocr-on-demand.md)).

```text
text layer present and usable?  ──yes──>  use it. exact, free, done.
                                 no
OCR provider configured?        ──yes──>  rasterise if needed, then OCR.
                                 no
vision-capable model?           ──yes──>  page images go to the model.
                                 no
                                          error naming all three remedies
```

**The usability heuristic.** A PDF can have a text layer that decodes to
rubbish — a broken `ToUnicode` table, a subset font with a custom encoding.
Accepting it silently poisons everything downstream, so a page's text layer is
considered usable only when all of these hold:

| Check | Default threshold | Option |
|---|---|---|
| Characters per square inch of page | ≥ 0.5 | `WithMinTextDensity` |
| Proportion of U+FFFD replacement characters | ≤ 2% | `WithMaxReplacementRatio` |
| Proportion of characters mapping to a `ToUnicode` entry or a standard encoding | ≥ 90% | `WithMinDecodableRatio` |

These thresholds are **judgement, not measurement**, and will be tuned against
the evaluation corpus ([ADR-0023](adr/0023-evaluation-corpus.md)). A page that
fails any of them falls through to OCR; a page that passes is used as-is.

**Per-page, not per-document.** A scanned appendix bound onto a digital
contract is one document with two acquisition paths, and ovrin takes both.
`Metadata.Readings` records which path each page took.

**Fails on:** no readable content and no provider (`ErrNoProvider`), OCR
failure after the chain is exhausted, a page limit.

---

## 3. Normalise

**In:** raw text with positions, per page.
**Out:** one normalised text stream, plus a mapping back to original positions.

The obligation that makes this stage harder than it looks: **normalisation
preserves offsets** ([ADR-0015](adr/0015-provenance.md)). Every transformation
maintains a mapping from output byte range back to input position, because
without it grounding and provenance are impossible and cannot be reconstructed
later.

What happens here:

- **Reading order.** Multi-column layouts are reordered into reading order
  using position and gap analysis. A two-column page read left-to-right across
  the gutter produces interleaved nonsense.
- **Whitespace.** Runs collapse to single spaces; positions of the surviving
  characters are retained.
- **Hyphenation.** A word broken across lines is rejoined, with the span
  covering both fragments.
- **Ligatures.** `ﬁ` expands to `fi`, with the span covering the single source
  glyph.
- **Unicode.** NFKC normalisation, so visually identical characters compare
  equal.
- **Page markers.** Page boundaries are marked in the stream so extracted
  values can be attributed to a page.

**Suspicious content is flagged, not removed**
([ADR-0017](adr/0017-untrusted-document-content.md)). Zero-width characters,
text positioned outside the media box, text in the page background colour, and
instruction-shaped metadata all produce a `ReviewReason` and lower confidence.
Silently stripping them would mean the operator never learns they are under
attack.

---

## 4. Schema

**In:** the Go type `T`.
**Out:** an internal `Schema`, and a JSON Schema.

Reflection over `T` reads `ovrin` struct tags into a `Schema`
([ADR-0005](adr/0005-schemas-are-go-structs.md),
[ADR-0006](adr/0006-tag-grammar.md)). The result is cached per `*Client` per
type, so reflection happens once.

The `Schema` drives three things: the JSON Schema sent to the model, the
validation pass in stage 7, and the keys of `Result.Fields`.

This stage runs **before any provider is contacted**, so a malformed tag or an
unsupported field type is an immediate, free error rather than a failure
discovered after a paid call.

**Fails on:** `ErrSchema` — unknown rule name, unsupported field type,
unbounded recursion, a rule that cannot apply to its field's type.

---

## 5. Prompt

**In:** the normalised content and the JSON Schema.
**Out:** a `ModelRequest`.

This stage is in the core and not in adapters, which is what makes the security
property hold identically across every provider
([ADR-0007](adr/0007-model-seam.md)).

The request has three parts that never mix:

```text
Instruction   built by ovrin from the schema. Never contains document content.
Content       the document, delimited and labelled as untrusted material.
Schema        JSON Schema the reply must satisfy.
```

The separation is structural rather than a matter of wording
([ADR-0017](adr/0017-untrusted-document-content.md)). Where a provider
distinguishes system from user content, the adapter maps them accordingly.
Because the output shape is fixed by the schema before the document is read, an
injected instruction cannot add a field, change a type or return prose.

---

## 6. Generate

**In:** a `ModelRequest`.
**Out:** raw JSON bytes.

The adapter translates to a vendor's wire format, requests constrained output
where the vendor supports it, and returns bytes. It makes no decisions
(rule [§6.2](rules.md#6-adapters)).

Retry and fallback wrap this stage from outside
([ADR-0018](adr/0018-fallback-is-a-decorator.md)). The pipeline sees one
`Model`; whether that is a single provider or a chain of three is invisible to
it.

Bytes are returned raw rather than unmarshalled so that a model returning
invalid JSON produces an ovrin error with the offending bytes attached, rather
than a different error per adapter.

**Fails on:** `ErrAuth`, `ErrRateLimit`, `ErrUnavailable`, `ErrBadResponse`
after the chain is exhausted — with every attempt's error wrapped, not just the
last.

---

## 7. Validate

**In:** the JSON, unmarshalled into `T`.
**Out:** a per-field pass or fail, and populated `Data`.

Two levels:

**Field rules** come from the tag: `required`, `min`, `max`, `format`, `enum`.
Each produces a `RuleResult`.

**Cross-field rules** check consistency the fields cannot check alone — line
items summing to the total, an issue date before a due date, a checksum on an
identifier. These produce the `cross_field` confidence signal.

A failure here is **never an error**
([ADR-0004](adr/0004-partial-results.md)). It sets `FieldResult.Valid` to
false, appends to `FieldResult.Errors`, clears `Result.Valid`, and the
extraction continues. Eleven good fields are not discarded because of one bad
one.

A field the model did not return is `Found: false`. It is never filled with a
zero value (rule [§8.5](rules.md#8-confidence-and-provenance)).

---

## 8. Ground

**In:** each extracted value, and the normalised text.
**Out:** a `Provenance` per value, and the `grounding` signal.

Every value is searched for in the source
([ADR-0015](adr/0015-provenance.md)):

| Outcome | `Exact` | `Span` | `grounding` |
|---|---|---|---|
| Verbatim match | true | set | 1.0 |
| Normalised match — same value, different formatting | false | set | 0.8 |
| Derived — computed or reformatted from content that is present | false | nil | 0.5 |
| No match | false | nil | 0.0, plus a review reason |

The last row is the important one. **A value that appears nowhere in the
document may have been invented**, and detecting that costs a string search.
It is the cheapest strong signal ovrin has and it catches the failure mode that
matters most.

Some fields are legitimately not groundable — a total computed from line items,
a date normalised from "the third of March". Those land in the derived row, and
the docs are explicit that a derived value is not a suspicious one.

---

## 9. Score

**In:** every signal collected by the preceding stages.
**Out:** per-field and aggregate confidence, and the review decision.

The `Scorer` combines the available signals into a number and records every one
of them on the field ([ADR-0013](adr/0013-multi-signal-confidence.md)):

```text
ocr          character recognition confidence over the backing words
schema       did the value satisfy its declared type and rules
cross_field  is it consistent with its siblings
agreement    do two independent readings agree        ADR-0014
format       does it match the expected shape for its kind
grounding    does it actually appear in the source
```

Signals that do not apply are **absent, not zero** — a text-layer PDF has no
`ocr` signal, and the scorer redistributes weight rather than treating the
absence as evidence against the value.

`NeedsReview` is set when confidence falls below the threshold, when readings
disagree, when a required field is missing, when grounding failed, when a
cross-field rule failed, or when suspicious content was flagged. Each sets a `ReviewReason` naming the field and
the cause.

**Confidence is currently a ranking signal, not a probability.** The weights
are provisional until calibrated against the corpus. 0.8 does not mean "correct
80% of the time" and the documentation will keep saying so until it does.

---

## What the pipeline does not do

- **Classify documents.** Ovrin extracts against the schema you give it. It
  does not decide whether a file is an invoice or a receipt. That is a
  reasonable future feature and it is not this.
- **Correct the document.** Deskewing, denoising and contrast adjustment belong
  to the OCR provider or to the caller.
- **Store anything.** No cache, no database, no temporary files that outlive
  the call.
- **Fetch anything a document references** (rule
  [§7.4](rules.md#7-untrusted-input)).

## See also

- [`architecture.md`](architecture.md) — the module and dependency structure
- [`schema.md`](schema.md) — stage 4 in full
- [`confidence.md`](confidence.md) — stages 8 and 9 in full
- [`threat-model.md`](threat-model.md) — stages 1, 3 and 5 as security boundaries
