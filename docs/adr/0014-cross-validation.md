# ADR-0014: Two readings, and their disagreement is a result

**Status:** Accepted · **Date:** 2026-08-26

## Context

The original design named a specific scenario, and it is the right one to build
around:

```text
OCR reads:  Amount: 25,000
AI reads:   Amount:  2,500
```

Both readings are internally plausible. Both pass type validation — each is a
number. Both would pass a `min=0` rule. Nothing in a single-reading pipeline
can tell these apart, and one of them is off by a factor of ten in a field
somebody is going to pay.

This is the failure mode that matters most in practice, because it is silent.
A missing field is obvious and gets handled. A confidently wrong number in a
well-formed result is the one that reaches production.

Disagreement between two structurally different readings of the same document
is the strongest available evidence that a value is unreliable — stronger than
anything derivable from a single reading, because the two readings fail in
uncorrelated ways. OCR misreads glyphs; a model misassigns fields. When they
agree, they are probably both right. When they disagree, at least one is
definitely wrong, and we know which field.

## Decision

When two readings of the same document are available, ovrin extracts from both
and compares field by field. `WithReading(ovrin.ModeBoth)` requests this; it
is opt-in because it roughly doubles cost.

Disagreement is recorded, never resolved silently
(rule [§8.4](../rules.md#8-confidence-and-provenance)):

```go
type FieldResult struct {
    Value       any
    Found       bool
    Confidence  float64
    Valid       bool
    Signals     []Signal
    Provenance  []Provenance
    Candidates  []Candidate   // populated when readings disagreed
    Errors      []error
}

type Candidate struct {
    Value   any
    Reading Reading    // ReadingText, ReadingOCR, ReadingVision
    Source  Provenance
}
```

When candidates disagree:

- `Value` holds the higher-confidence candidate, so the caller who ignores all
  of this still gets the better answer.
- `Candidates` holds every candidate with its reading and provenance, so
  nothing is discarded.
- The `agreement` signal drops, which lowers the field's confidence
  ([ADR-0013](0013-multi-signal-confidence.md)).
- `Result.NeedsReview` becomes true and a `ReviewReason` naming the field and
  both values is appended.

Comparison is **type-aware, not textual**. `25,000`, `25000` and `25 000` are
the same number and do not constitute disagreement; `25,000` and `2,500` do.
Dates compare as dates, currency as amount plus currency, strings after
normalising whitespace and case unless the field's format says otherwise. The
normalisation rules are specified in
[`docs/confidence.md`](../confidence.md), because a comparison that flags
`ACME LTD` against `Acme Ltd` would make the feature useless through noise.

Cross-**field** validation is separate and always on: line items summing to the
total, a date within a plausible range, a checksum on an identifier. Those are
schema rules producing the `cross_field` signal, and they need only one reading.

## Consequences

**Good.** It catches the specific failure that single-reading pipelines cannot
catch at all, using two error sources that are genuinely uncorrelated. Nothing
is thrown away — a reviewer sees both values, both sources and both page
locations, which is exactly what they need to decide in seconds rather than
minutes. A caller who ignores `Candidates` entirely still receives the better
value, so the feature costs nothing to not use.

**Bad.** It roughly doubles cost and latency, which is why it cannot be the
default, which in turn means the users most likely to need it are the ones
least likely to know it exists. Type-aware comparison is a normalisation
problem with a long tail — every locale's number and date formatting is another
way to report a false disagreement, and false disagreements train reviewers to
dismiss the flag. Agreement is not correctness: two readings can be wrong the
same way, most obviously when both come from the same underlying model, and
agreement will report high confidence on a shared error. And `Candidates`
widens `FieldResult` for a case most fields never hit.

**Neutral.** Three or more readings, with majority voting, is a natural
extension the structure already supports. It is deferred until two readings
have been evaluated on real documents, because the cost triples and the benefit
is unmeasured.

## Alternatives considered

- **Resolve disagreements automatically by preferring one reading.** Rejected:
  it is exactly the silent wrongness this decision exists to prevent, and no
  fixed preference is right — OCR wins on printed amounts, the model wins on
  layout-dependent assignment.
- **Return an error on disagreement.** Rejected: contradicts
  [ADR-0004](0004-partial-results.md) and throws away eleven good fields
  because of one contested field.
- **Ask a model to adjudicate.** Rejected for v0.1: a third opinion from the
  same class of system that produced one of the two answers, at additional
  cost, with no way to know it is right. Reconsider once there is a corpus to
  measure it against.
- **Compare raw strings.** Rejected: `25,000` versus `25000` would flag on
  every document, and a flag that fires constantly is a flag nobody reads.
- **Always run both readings.** Rejected: doubles the cost of every easy
  document to protect the hard ones.
