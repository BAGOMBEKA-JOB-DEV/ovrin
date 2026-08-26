# ADR-0023: An evaluation corpus lives in the repository

**Status:** Accepted · **Date:** 2026-08-26

## Context

Every claim ovrin will want to make is a distribution, not a boolean. "Field
accuracy on scanned invoices." "How often confidence below 0.7 corresponds to a
wrong value." "Whether the new prompt is better than the old one." Unit tests
answer none of these — a test asserts that one document produces one expected
value, which says nothing about a corpus.

Without a measurement harness three specific things go wrong, and all three are
predictable.

**Changes cannot be evaluated.** A prompt edit, a normalisation change, a new
confidence weight — is it better? Nobody knows, so changes are made on
intuition and regressions ship silently, because a regression here does not
crash, it just returns a slightly wrong number slightly more often.

**Confidence weights stay guesses.** [ADR-0013](0013-multi-signal-confidence.md)
ships provisional weights and says so. Turning them into calibrated numbers
requires labelled documents and a way to score against them.

**Accuracy claims become marketing.** A README figure that no one can reproduce
is a figure that will drift from reality and eventually be false.

## Decision

The repository contains an evaluation corpus and a harness that runs against
it.

```text
eval/
  corpus/
    invoices/  receipts/  forms/  statements/  identity/
      NNN.pdf              the document
      NNN.expected.json    ground truth
      NNN.meta.yaml        provenance, licence, redaction note, difficulty
  schema/                  the Go schemas each category extracts against
  report/                  committed results, one file per run
```

Run with `go test -tags=eval ./eval/...`, which needs credentials and costs
money, so it is not part of CI. It reports per-field precision and recall,
exact-match rate by category, confidence calibration (expected calibration
error, and accuracy within confidence bands), review rate, and cost and latency
per document.

Four commitments make it worth having:

**Every corpus document is licensed for redistribution and contains no real
personal data** (rule [§7.6](../rules.md#7-untrusted-input)). Public forms,
synthetic documents with realistic layouts, and donated documents with written
permission and every identifier replaced. The `.meta.yaml` records where each
one came from and what was redacted. **A document that cannot be redistributed
does not go in**, however useful it would be — this is the constraint that will
hurt most, and it is not negotiable.

**Difficulty is labelled.** Clean digital, good scan, poor scan, photograph,
handwritten, multi-column, multi-page. An aggregate number over an unbalanced
corpus is meaningless, and every reported figure is broken down by difficulty.

**Reports are committed.** A run's output goes in `eval/report/` with the date,
model, provider and ovrin commit. That makes regressions visible in a diff and
gives every published figure a reproducible origin.

**No accuracy claim is made that this harness cannot reproduce** (rule
[§3.8](../rules.md#3-testing)). Not in the README, not in a blog post, not in a
conference talk.

The corpus starts small — five documents per category — and grows. Five real
documents per category beats zero, and a harness with a small corpus is
infinitely more useful than a large corpus with no harness.

## Consequences

**Good.** Changes are evaluated instead of guessed at. Confidence weights have
a path from provisional to calibrated. Published figures are reproducible by a
sceptic, which is the only kind of figure worth publishing. Committed reports
turn quality regressions into reviewable diffs. And a corpus is the artefact
that lets a contributor demonstrate their change is an improvement.

**Bad.** Building a corpus that is genuinely representative *and* legally
redistributable is slow, and the redistribution constraint excludes exactly the
messy real-world documents that would be most valuable. Running the harness
costs money on every provider, so it will be run less often than it should be.
Documents in git are binary blobs that inflate the repository permanently.
Ground truth is hand-labelled and will contain errors, which will be blamed on
the extractor. And a committed report is a public record of how wrong we are,
which takes some nerve.

**Neutral.** Corpus growth is the most valuable contribution an outside
contributor can make and the one most likely to arrive with a licence problem.
[`CONTRIBUTING.md`](../../CONTRIBUTING.md) states the requirements before
anyone spends effort.

## Alternatives considered

- **No corpus; rely on unit tests.** Rejected: unit tests cannot measure a
  distribution, so quality would be unmanaged.
- **A private corpus held by the maintainer.** Rejected: contributors cannot
  evaluate their own changes, and published figures become unverifiable.
- **Point at a public benchmark instead.** Rejected as the only source:
  worth adding, but public document benchmarks do not cover the East African
  government and institutional forms this library is partly aimed at, and a
  benchmark nobody in the target domain recognises does not build trust.
- **Generate synthetic documents at run time.** Rejected as the basis: no
  scanner noise, no real-world layout variety, and it measures the generator.
