# Explainability

Every value ovrin produces can be traced back to the bytes it came from and
decomposed into the signals that scored it. This document describes what is
recorded and how to read it.

This is not a compliance feature bolted on afterwards. Three ordinary things
depend on the same data: human review is unusable without it, grounding — the
strongest cheap confidence signal — is built on it, and "why did this field
come out wrong" is unanswerable without it.

**Contents:** [Explain](#explain) · [Provenance](#provenance) ·
[Reading a score](#reading-a-score) · [Review](#review-workflows) ·
[Audit](#audit) · [Limits](#limits)

---

## Explain

```go
func (r *Result[T]) Explain(field string) (*Explanation, bool)

type Explanation struct {
    Field      string
    Value      any
    Found      bool
    Confidence float64
    Signals    []Signal
    Provenance []Provenance
    Candidates []Candidate
    Validation []RuleResult
    Reasons    []ReviewReason
}
```

It returns a value, not a string ([ADR-0016](adr/0016-explain-returns-data.md)),
because the consumers are review queues, audit stores, dashboards and JSON
APIs, none of which want text. The terminal rendering exists as `String()`:

```go
if e, ok := res.Explain("total"); ok {
    fmt.Println(e)            // for a person
    audit.Record(e)           // for a system
}
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

`String()` output is **not** part of the compatibility promise. Anyone parsing
it has taken a dependency we will break.

---

## Provenance

```go
type Provenance struct {
    Reading Reading   // ReadingText, ReadingOCR, ReadingVision
    Page    int       // 1-based
    Box     *Rect     // page points, origin top-left; nil if unknown
    Span    *Span     // byte offsets into the normalised text; nil if unknown
    Method  string    // "text-layer", "ocr:tesseract", "vision:gpt-5.2"
    Exact   bool      // the value appears verbatim in the source
}
```

Coordinates are **PDF points with a top-left origin**. That is neither PDF's
own convention (bottom-left) nor an image format's (pixels); one convention had
to be chosen and adapters normalise to it
([ADR-0009](adr/0009-ocr-seam.md)).

A nil `Box` or `Span` means **we do not know**, never "it is not there". The
distinction matters: derived values — a total computed from line items, a date
normalised from prose — are legitimately ungeometried and are not suspicious.

Coverage is honest about being uneven:

| Reading | `Box` | `Span` | `Exact` |
|---|---|---|---|
| text layer | usually | yes | usually |
| OCR | always | yes | usually |
| vision | rarely | sometimes | sometimes |

Vision gives the least provenance and is used on the hardest documents, so the
feature is weakest exactly where it would be most valuable. That is a real
limitation, not a temporary one.

---

## Reading a score

Two worked cases.

**A field you can trust.**

```text
grounding 1.00 · ocr 0.97 · schema 1.00 · format 1.00 · cross_field 1.00
→ 0.99
```

The value is in the document verbatim, the characters were read cleanly, it
satisfies its rules, it parses as the declared format, and it is consistent
with its siblings. Five independent things agree.

**A field you cannot.**

```text
grounding 0.00 · schema 1.00 · format 1.00
→ 0.35   (capped: grounding failed)

Reasons
  total — value not found in source; may be inferred
```

This is the important case. The value is well-formed, it satisfies every rule,
and it parses correctly — a single-signal pipeline would report high
confidence. It does not appear anywhere in the document. The floor catches it
and the reason says why.

**Disagreement.**

```text
Candidates
  25000.00   ocr:tesseract   page 1  (412,688)-(486,702)
   2500.00   vision:gpt-5.2  page 1  —

agreement 0.00 → capped at 0.50

Reasons
  total — readings disagree: 25000.00 (ocr) vs 2500.00 (vision)
```

`Value` holds the higher-confidence candidate so a caller who ignores all of
this still gets the better answer. Nothing is discarded, and a reviewer sees
both values with both locations.

---

## Review workflows

Ovrin builds no review interface. It produces what one needs.

```go
res, err := ovrin.Extract[Invoice](ctx, c, src)
if err != nil {
    return err
}
if !res.NeedsReview {
    return ledger.Post(res.Data)
}

task := review.Task{Document: docID}
for _, r := range res.Reasons {
    e, _ := res.Explain(r.Field)
    task.Fields = append(task.Fields, review.Field{
        Name:       r.Field,
        Value:      e.Value,
        Why:        r.Why,
        Confidence: e.Confidence,
        Highlight:  e.Provenance,   // page and box → draw a rectangle
        Options:    e.Candidates,   // both readings → offer both
    })
}
return queue.Send(task)
```

The three things that make review fast rather than tedious: `Highlight` lets
the interface show the source region so the reviewer does not search a 40-page
scan; `Options` turns a typing task into a two-button choice where readings
disagreed; and `Why` tells the reviewer what to look at rather than making them
work it out.

---

## Audit

An `Explanation` marshals to JSON. `RuleResult` carries a `Message string`
rather than an `error` for exactly this reason; `FieldResult`, which does carry
`[]error`, does not marshal usefully and is not intended to. Storing one per extracted field gives a
record that answers, years later and without reprocessing: what value was
extracted, from which page and region, by which reading and which provider,
scored how and on what evidence, whether a human intervened.

Two cautions.

**Provenance is often larger than the data.** A twelve-field invoice produces
twelve explanations with signals, spans and boxes. At scale, store what your
retention policy requires and not more.

**Never store the document content in the audit record via ovrin.** Errors,
events and traces carry pages and counts — never field values
(rule [§7.5](rules.md#7-untrusted-input)). If your audit needs the source
document, store the document, deliberately, under your own controls.

---

## Limits

Stated plainly, because an explainability feature that oversells itself is
worse than none.

**Explanation is not causation.** The signals say what evidence supported a
value. They do not reveal why a model produced it — that is not observable from
outside the model, by us or by anyone.

**Grounding proves presence, not correctness.** A value found verbatim in the
document may still have been assigned to the wrong field. Grounding catches
invention; it does not catch misassignment.

**Vision readings explain least.** No geometry, often no span. The hardest
documents produce the thinnest explanations.

**Confidence is not yet calibrated.** The decomposition is accurate; the number
it sums to is a ranking, not a probability, until the corpus says otherwise
([`confidence.md`](confidence.md)).

## See also

- [ADR-0015](adr/0015-provenance.md) — why provenance is mandatory
- [ADR-0016](adr/0016-explain-returns-data.md) — why a struct, not a string
- [`confidence.md`](confidence.md) — the signals in full
- [`data-handling.md`](data-handling.md) — what leaves the process
