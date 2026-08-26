# ADR-0015: Every field carries where it came from

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin is aimed at government registration, banking, insurance, healthcare and
education systems. In all five, "the computer extracted 2,500" is not an
adequate account of a number. Somebody will eventually need to know which page
it came from, where on that page, which reading produced it and whether it
actually appears in the document at all.

That requirement is usually framed as auditability, and it is, but three other
things depend on the same data and would be worth building even without an
auditor:

**Human review is unusable without it.** A reviewer shown a field name and a
value has to find the value in a 40-page scan themselves. A reviewer shown page
3 with a highlighted box decides in two seconds. The difference is whether
review is viable at scale.

**Grounding is the strongest cheap confidence signal.** A value that does not
appear anywhere in the source text was invented by the model. Detecting that
requires knowing what the source text was and being able to search it — which
is provenance ([ADR-0013](0013-multi-signal-confidence.md)).

**Debugging needs it.** "Why did this field come out wrong" is unanswerable
without knowing which bytes the model was looking at.

If provenance is not captured during extraction it cannot be reconstructed
afterwards, so this is a decision that has to be made before the pipeline
exists rather than added later.

## Decision

Every `FieldResult` carries provenance, and every stage that touches content
preserves it.

```go
type Provenance struct {
    Reading Reading   // ReadingText, ReadingOCR, ReadingVision
    Page    int       // 1-based
    Box     *Rect     // page coordinates, points, origin top-left; nil if unknown
    Span    *Span     // byte offsets into the normalised text; nil if unknown
    Method  string    // "text-layer", "ocr:tesseract", "vision:gpt-5.2"
    Exact   bool      // the value appears verbatim in the source
}
```

Three commitments make this work:

**Normalisation is offset-preserving.** Whitespace collapsing, ligature
expansion, hyphenation repair and reading-order reconstruction all maintain a
mapping back to original positions. This is the constraint that makes the
normalisation stage harder to write than it looks, and it is not negotiable —
normalisation that loses offsets makes every downstream guarantee impossible.

**Grounding is attempted for every field.** After extraction, ovrin searches the
normalised text for the value. A verbatim match sets `Exact` and fills `Span`.
A normalised match — the value with formatting differences — sets `Span` with
`Exact` false. No match leaves both nil, sets the `grounding` signal to zero
and adds a review reason. **A value with no grounding is a value the model may
have invented**, and it is reported as such.

**Best-effort, and honest about it.** Some fields are legitimately not
groundable: a total the model computed from line items, a date it normalised
from "the third of March". Those carry `Method` and `Page` with a nil `Box` and
`Exact` false. A nil field means "we do not know", never "it is not there".

`Box` is populated whenever the reading provides geometry — always for OCR,
usually for the text layer, rarely for vision.

## Consequences

**Good.** Review interfaces can highlight the source region, which is the
difference between review being practical and theoretical. Fabrication is
detectable cheaply and directly. Audit questions have answers years later, from
the stored result, without reprocessing. Debugging an extraction becomes
tractable.

**Bad.** Offset-preserving normalisation is materially harder than
string-rewriting normalisation, and every future normalisation step inherits
the obligation — it is a permanent tax on a part of the pipeline that would
otherwise be easy. Provenance data is often larger than the extracted data
itself, which matters when results are stored per document at scale. Vision
readings give almost no geometry, so the feature is weakest exactly where
documents are hardest. Grounding search costs time on large documents and will
produce false negatives on legitimately derived values, which means review
reasons that are technically correct and practically noise.

**Neutral.** `Rect` is in PDF points with a top-left origin, which is not PDF's
own convention (bottom-left) and not any image format's (pixels). One
convention had to be chosen, adapters normalise to it
([ADR-0009](0009-ocr-seam.md)), and the choice is arbitrary but fixed.

## Alternatives considered

- **Page numbers only, no geometry.** Rejected: cheap, and not enough to
  highlight anything, so review remains a manual search.
- **Provenance as an opt-in `WithProvenance()` option.** Rejected: it cannot be
  reconstructed after the fact, so an opt-out is a decision users would make
  before understanding what they were giving up — and grounding, which the
  confidence engine depends on, would silently stop working.
- **Store the full source text on the result instead of spans.** Rejected:
  duplicates the document into every result, and still leaves the caller to
  find the value themselves.
- **Character offsets instead of byte offsets.** Rejected: Go strings are
  bytes, `[]rune` conversion is a copy, and every caller would convert back.
