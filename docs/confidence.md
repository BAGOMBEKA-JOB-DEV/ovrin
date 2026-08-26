# Confidence

The confidence score decides whether a value is used automatically or sent to a
person. It is the most consequential number ovrin produces, and this document
describes exactly how it is computed and what it does not mean.

> **Read this first.** Confidence in v0.x is a **ranking** signal, not a
> probability. A field at 0.82 is more trustworthy than one at 0.61. It is
> **not** correct 82% of the time. The weights below are provisional and will
> be calibrated against the evaluation corpus
> ([ADR-0023](adr/0023-evaluation-corpus.md)); until they are, thresholds
> should be chosen by looking at your own documents, not by reading a number.

**Contents:** [Why not logprobs](#why-not-logprobs) · [Signals](#signals) ·
[Combining](#combining) · [Comparison](#comparison) · [Review](#review) ·
[Custom scorers](#custom-scorers) · [Calibration](#calibration)

---

## Why not logprobs

Three obvious sources of confidence are each unusable, and they get proposed
often enough to be worth recording.

**Asking the model.** A `confidence` field in the schema produces well-formed
numbers that are close to uncorrelated with correctness. Models are
systematically overconfident and will report 0.95 on a value they invented.
Ovrin does not collect it.

**Token logprobs.** The textbook answer, and unavailable in practice. Anthropic
exposes no logprobs on any model. OpenAI's reasoning models hide them when
reasoning is on. And where they do exist, the constrained JSON decoding ovrin
relies on everywhere else destroys them: measurements find 99.4–100% of
logprobs saturating above 0.999, because once the grammar has fixed the output
there is very little left to be uncertain about. A signal that reads 0.999 for
correct and incorrect values alike carries no information.

**OCR confidence alone.** Real and useful, and it measures the wrong thing. An
engine can be 99% certain it read `25,000` correctly while the field that value
was assigned to is wrong.

So confidence is built from several signals that fail in uncorrelated ways
([ADR-0013](adr/0013-multi-signal-confidence.md)).

---

## Signals

```go mirror
type Signal struct {
    Name   string
    Value  float64  // 0..1
    Weight float64
    Note   string   // why, in one line
}
```

| Signal | Measures | Source | Available when |
|---|---|---|---|
| `ocr` | character recognition over the words backing this value | OCR provider, normalised | the value came from OCR |
| `schema` | the value satisfied its type and rules | validation | always |
| `cross_field` | consistency with sibling fields | cross-field rules | the schema declares any |
| `agreement` | two independent readings agree | comparison | `ModeBoth` |
| `format` | the value matches the expected shape for its kind | format checks | the field declares `format` |
| `grounding` | the value actually appears in the source | grounding search | always |

**Signals are absent, not zero, when unavailable.** A text-layer PDF has no
`ocr` signal. Treating that as 0.0 would penalise the most accurate acquisition
path ovrin has, so the scorer redistributes weight across the signals that do
apply.

### `grounding` deserves emphasis

It is the cheapest strong signal available and it catches the failure that
matters most.

| Outcome | Value | Meaning |
|---|---|---|
| Verbatim match in the source | 1.0 | the value is in the document |
| Normalised match — same value, different formatting | 0.8 | almost certainly right |
| Derived — computed or reformatted from present content | 0.5 | plausible, not directly verifiable |
| No match | 0.0 | **the model may have invented this** |

A value that appears nowhere in the document it was given was not read from it.
Detecting that costs a string search.

---

## Combining

The default scorer is a weighted mean over available signals, with hard floors.

**Provisional weights**, normalised across whichever signals apply:

| Signal | Weight |
|---|---|
| `grounding` | 0.30 |
| `agreement` | 0.25 |
| `ocr` | 0.20 |
| `schema` | 0.15 |
| `format` | 0.05 |
| `cross_field` | 0.05 |

**Hard floors**, applied after the mean, because some failures should not be
averaged away. A ceiling that actually binds is recorded as an extra
zero-weight signal named `capped:…`, so a reader doing the arithmetic can see
why the reported number is below the mean. Zero weight leaves the mean itself
untouched:

| Condition | Confidence capped at |
|---|---|
| A `required` rule failed | 0.0 — the field is absent |
| Any other declared rule failed | 0.40 |
| `grounding` is 0.0 | 0.35 |
| Readings disagreed | 0.50 |
| Suspicious content was flagged on the source page | 0.60 |

A worked example — an invoice total read by OCR, present verbatim, passing its
rules, with line items that sum correctly:

```text
grounding    1.00 × 0.30  = 0.300     value found verbatim on page 1
ocr          0.97 × 0.20  = 0.194     backing words averaged 0.97
schema       1.00 × 0.15  = 0.150     float64, min=0 satisfied
cross_field  1.00 × 0.05  = 0.050     line items sum to the total
format       1.00 × 0.05  = 0.050     parsed as currency
             ─────────────────────
             weights present: 0.75
             0.744 / 0.75  = 0.99
```

`agreement` is absent because only one reading ran, so its 0.25 is excluded
from the denominator rather than counted as a failure.

Had a ceiling bound here, a further line would appear:

```text
capped:grounding   —          the value is not in the source
```

and the reported confidence would be the ceiling rather than the mean. A
ceiling only binds when it is **below** the mean — it is a maximum, not a
replacement. A value grounded at 0.0 whose other signals already drag the mean
to 0.33 stays at 0.33, because `capped:grounding` is 0.35.

**Aggregate confidence** on `Result` is the mean over fields, weighted by
whether a field is `required`. A missing optional field should not drag down a
document that is otherwise clean.

---

## Comparison

When two readings run, values are compared field by field
([ADR-0014](adr/0014-cross-validation.md)). Comparison is type-aware, because
comparing raw strings would flag on nearly every document and a flag that fires
constantly is a flag nobody reads.

| Type | Equal when | Not equal |
|---|---|---|
| numeric | same value after stripping separators, symbols and whitespace | `25,000` vs `2,500` |
| currency | same amount **and** same currency | `100 USD` vs `100 EUR` |
| date | same instant after parsing | `03/04/26` vs `2026-04-03` are equal |
| string | equal after NFKC, whitespace collapse and case folding | `Acme Ltd` vs `Acme Limited` differ |
| bool | same value | |
| slice | same length and every element equal | |

`25,000`, `25000` and `25 000` are one number. `ACME LTD` and `Acme Ltd` are
one string. `Acme Ltd` and `Acme Limited` are two, and that is a genuine
disagreement worth a reviewer's attention.

**Agreement is not correctness.** Two readings can be wrong in the same way,
most obviously when both come from the same underlying model. High `agreement`
on a shared error is a real limitation and is not detectable from inside.

---

## Review

`NeedsReview` is set when any of these hold. Each appends a `ReviewReason`
naming the field and the cause.

| Trigger | Default | Option |
|---|---|---|
| Field confidence below threshold | 0.70 | `WithReviewThreshold` |
| Readings disagreed | always | — |
| A `required` field is absent | always | — |
| `grounding` is 0.0 | always | — |
| Suspicious content flagged on the source | always | — |
| A cross-field rule failed | always | — |

```go
if res.NeedsReview {
    for _, r := range res.Reasons {
        log.Printf("review: %s — %s", r.Field, r.Why)
    }
    return queue.Send(res)
}
```

Ovrin builds no review interface. It produces the data one needs: the partial
value, both candidates where they disagree, the page and box for each, the
signals, and the reason.

---

## Custom scorers

The default scorer is a starting point. A user with labelled documents can fit
a better one to their own corpus, and it will beat our defaults on their
documents.

```go mirror
type Scorer interface {
    Score(f FieldEvidence) (confidence float64, signals []Signal)
}
```

```go
c := ovrin.New(ovrin.WithScorer(myScorer))
```

`FieldEvidence` carries everything the pipeline collected: raw signal values,
candidates, provenance, validation results and the field's schema entry.

A consequence worth stating: **two ovrin deployments with different scorers
report different confidence for the same document.** Confidence is comparable
within a deployment, not across organisations.

---

## Calibration

Calibration — establishing that a score of 0.8 corresponds to being correct
about 80% of the time — requires labelled documents and is v1.0 work
([ADR-0024](adr/0024-versioning-and-stability.md)).

The harness reports expected calibration error and accuracy within confidence
bands ([ADR-0023](adr/0023-evaluation-corpus.md)). Until those numbers are
published:

- Use confidence to **order** a review queue. It is good at that today.
- Choose thresholds by running your own documents and looking at where errors
  fall, not by picking a number that sounds high.
- Do not present it to end users as a probability.

## See also

- [ADR-0013](adr/0013-multi-signal-confidence.md) — why multi-signal
- [ADR-0014](adr/0014-cross-validation.md) — why disagreement is a result
- [ADR-0015](adr/0015-provenance.md) — where grounding comes from
- [`pipeline.md`](pipeline.md) — stages 8 and 9
- [`explainability.md`](explainability.md) — reading a score back
