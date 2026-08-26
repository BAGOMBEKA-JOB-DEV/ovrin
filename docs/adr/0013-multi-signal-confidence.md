# ADR-0013: Confidence is multi-signal; logprobs are not the source

**Status:** Accepted · **Date:** 2026-08-26

## Context

A confidence score decides whether a value is used automatically or sent to a
human. Get it wrong in one direction and people review everything, which
defeats the purpose. Get it wrong in the other and wrong data enters a payments
or benefits system with a green tick beside it. This is the most consequential
number ovrin produces.

Three tempting sources are each unusable, and it is worth recording why so that
nobody re-proposes them.

**Asking the model.** A field in the schema for the model's own confidence. It
produces well-formed numbers that are close to uncorrelated with correctness;
models are systematically overconfident and will report 0.95 on a value they
hallucinated.

**Token logprobs.** The textbook answer, and unavailable. Anthropic exposes no
logprobs at all, on any model. OpenAI's reasoning models hide them when
reasoning is on. Where they do exist, constrained JSON decoding destroys them:
measurements find 99.4–100% of logprobs saturating above 0.999, because once
the grammar has constrained the output there is very little left to be
uncertain about. A signal that is 0.999 for both correct and incorrect values
carries no information.

**The OCR engine's own confidence.** Real and useful, but it measures character
recognition, not extraction. An OCR engine can be 99% confident it read
`25,000` and be entirely right, while the field it was assigned to is wrong.

The literature has converged on the same conclusion — that logprobs alone are
insufficient and that reliable confidence has to be grounded in structurally
different readings of the same document, combined across several signals and
calibrated against outcomes.

## Decision

Confidence is computed from **named signals**, each recorded on the result, and
combined by a **pluggable scorer**. No score is ever produced that the caller
cannot decompose (rule [§8.1](../rules.md#8-confidence-and-provenance)).

```go
type Signal struct {
    Name   string   // "ocr", "schema", "cross_field", "agreement", "format", "grounding"
    Value  float64  // 0..1
    Weight float64
    Note   string   // why, in one line
}

type Scorer interface {
    Score(field FieldEvidence) (confidence float64, signals []Signal)
}
```

The v0.1 signal set:

| Signal | What it measures | Where it comes from |
|---|---|---|
| `ocr` | character-recognition confidence over the words backing this value | OCR provider, normalised ([ADR-0009](0009-ocr-seam.md)) |
| `schema` | did the value satisfy its declared type and rules | validation pass |
| `cross_field` | is the value consistent with its siblings — do line items sum to the total | cross-field rules |
| `agreement` | do two independent readings agree | [ADR-0014](0014-cross-validation.md) |
| `format` | does the value match the expected shape for its kind — date, currency, identifier | format checks |
| `grounding` | does the value actually appear in the source text | [ADR-0015](0015-provenance.md) |

`grounding` deserves emphasis: it is the cheapest strong signal available. A
value the model returned that does not appear anywhere in the document it was
given was invented, and that is detectable with a string search. It catches the
failure mode that matters most and costs nothing.

Signals are **absent, not zero, when unavailable.** A text-layer PDF has no
`ocr` signal; the scorer redistributes weight rather than treating the missing
signal as evidence of low confidence.

The default scorer is a **weighted mean over available signals, with hard
floors**: any failed `schema` rule caps the field, and a `grounding` failure
caps it lower still. Weights are documented in
[`docs/confidence.md`](../confidence.md) and are explicitly **provisional** —
they are a starting point to be calibrated against the evaluation corpus
([ADR-0023](0023-evaluation-corpus.md)), not a result.

Model-reported confidence is not collected. Logprobs may be added later as one
signal among several; they may never be the score
(rule [§8.2](../rules.md#8-confidence-and-provenance)).

## Consequences

**Good.** Every score is explainable down to its inputs, which is what a
regulated deployment needs and what a debugging session needs. Signals degrade
independently, so one unavailable input weakens the score rather than breaking
it. `grounding` catches fabrication cheaply and directly. Making `Scorer`
pluggable means a user with labelled data can fit a better scorer to their own
corpus, which will beat our defaults on their documents.

**Bad.** The weights are guesses until calibrated, and a confidence number
derived from uncalibrated weights is a number that looks more authoritative
than it is — the docs must say so loudly and repeatedly, because users will
threshold on it from day one regardless. Six signals is six things to compute,
and some are not free: `agreement` requires a second reading and roughly
doubles cost. Weighted means are not calibrated probabilities, so 0.8 does not
mean "correct 80% of the time" and users will assume it does. And a pluggable
scorer means two ovrin deployments can report different confidence for the same
document, which makes cross-organisation comparison meaningless.

**Neutral.** Calibration — reporting expected calibration error and
risk-coverage from the evaluation corpus, so thresholds can be chosen on
evidence — is v1.0 work, tracked in [`docs/roadmap.md`](../roadmap.md). Until
then the docs describe confidence as a **ranking** signal, useful for ordering
a review queue, not as a probability.

## Alternatives considered

- **Ask the model for a confidence field.** Rejected: uncorrelated with
  correctness and systematically overconfident. Worse than no score, because it
  looks like one.
- **Token logprobs as the score.** Rejected: unavailable on a major provider,
  and saturated to a constant by the constrained decoding we rely on
  everywhere else.
- **OCR confidence as the score.** Rejected: measures the wrong thing. It is a
  good signal and a bad score.
- **A trained calibration model shipped with ovrin.** Rejected for now: it
  needs a large labelled corpus we do not have, and a model trained on invoices
  would be miscalibrated on medical forms while looking equally confident.
- **No confidence at all; return values and let the caller decide.** Rejected:
  the caller has strictly less information than we do. Declining to score is
  not neutrality, it is pushing the hardest problem onto someone worse equipped
  to solve it.
