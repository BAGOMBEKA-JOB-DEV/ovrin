# Evaluation

How ovrin's accuracy is measured, and why no accuracy figure appears anywhere
that this harness cannot reproduce (rule [§3.8](rules.md#3-testing)).

> **The corpus is empty.** Nothing has been measured. Every number in this
> document is a worked example of the format, not a result.

**Contents:** [Why](#why-a-corpus) · [Structure](#structure) ·
[Ground truth](#ground-truth) · [Metrics](#metrics) · [Running](#running-it) ·
[Reports](#reports) · [Contributing documents](#contributing-documents)

---

## Why a corpus

Every claim worth making about ovrin is a distribution, not a boolean.

A unit test asserts that one document produces one value. That says nothing
about how often extraction is right, whether a prompt change helped, or whether
confidence below 0.7 actually corresponds to a wrong value. Without measurement
three things go wrong, all predictable
([ADR-0023](adr/0023-evaluation-corpus.md)):

**Changes cannot be evaluated.** A regression here does not crash. It returns a
slightly wrong number slightly more often, and ships.

**Confidence weights stay guesses.** They are provisional by their own ADR, and
calibrating them requires labelled documents.

**Accuracy claims become marketing.** A README figure nobody can reproduce
drifts from reality and eventually becomes false.

---

## Structure

```text
eval/
  corpus/
    invoices/
      001.pdf
      001.expected.json
      001.meta.yaml
    receipts/  forms/  statements/  identity/  transcripts/
  schema/
    invoice.go  receipt.go  form.go  …
  report/
    2026-08-26-gpt-5.2-tesseract.json
    2026-08-26-gpt-5.2-tesseract.md
  eval_test.go
```

`001.meta.yaml` records what the document is and where it came from:

```yaml
source: public-form          # public-form | synthetic | donated
licence: CC0-1.0
redacted: names, account numbers and dates of birth replaced with
          synthetic values of the same shape
difficulty: poor-scan        # clean-digital | good-scan | poor-scan |
                             # photograph | handwritten | multi-column
pages: 3
language: en
notes: |
  Skewed about 4 degrees. Staple hole through the top-left of page 2.
  Total appears twice — once in the summary box, once in the footer.
```

**Difficulty labels are mandatory.** An aggregate number over an unbalanced
corpus is meaningless, so every reported figure is broken down by difficulty.
A corpus of clean digital PDFs would report excellent accuracy and predict
nothing about a phone photograph of a receipt.

---

## Ground truth

`001.expected.json` is what a careful person reading the document says the
answer is:

```json
{
  "number": "INV-2026-0417",
  "vendor": "Kampala Supplies Ltd",
  "currency": "UGX",
  "total": 2500000.00,
  "issued": "2026-03-14",
  "items": [
    {"description": "A4 paper, 80gsm", "quantity": 40, "unit_price": 12500.00},
    {"description": "Toner cartridge",  "quantity":  4, "unit_price": 185000.00}
  ]
}
```

Rules for it:

- **A field that is genuinely absent is absent from the JSON.** Not null, not
  zero. Ground truth must be able to express "this document does not have a
  purchase order number", because the extractor must too.
- **Values are what the document says**, not what is true. A document with an
  arithmetic error has ground truth matching the document.
- **Ambiguity is excluded from scoring**, by naming the field in the meta file's
  `exclude:` list. Scoring a field two careful readers would disagree about
  measures the readers, not the extractor.

  ```yaml
  exclude:
    - due          # 03/04/2026 — the document does not say which reading
  ```

  The reason belongs in `notes:` alongside it. Excluded fields are counted and
  reported separately, so a corpus quietly excluding its hard cases is visible
  rather than flattering.

Ground truth is hand-labelled and will contain errors. When an apparent
extraction failure turns out to be a labelling error, fix the label in its own
commit with the reasoning in the message.

---

## Metrics

### Field accuracy

Per field, per category, per difficulty.

| Metric | Meaning |
|---|---|
| **Exact** | extracted value equals ground truth, type-aware comparison |
| **Precision** | of the values produced, how many were right |
| **Recall** | of the values present in the document, how many were found |
| **Fabrication rate** | values produced for fields absent from ground truth |

**Fabrication rate is the one to watch.** A missing field is visible and gets
handled. An invented one is well-formed, passes validation and reaches
production.

### Confidence calibration

The reason the harness exists at all, beyond regression testing.

| Metric | Meaning |
|---|---|
| **ECE** | expected calibration error — the gap between stated confidence and observed accuracy |
| **Accuracy by band** | of fields scored 0.9–1.0, what fraction were right; and so on down |
| **Risk-coverage** | if you auto-accept above threshold *t*, what fraction do you cover and what error rate do you carry |
| **Review precision** | of fields flagged for review, how many were actually wrong |

Risk-coverage is what an operator actually needs: it answers "where do I set
the threshold" with a curve rather than an opinion.

### Cost and latency

Per document and per page, by configuration. A 3% accuracy gain that triples
cost is a decision, not an improvement, and the report should let someone make
it.

---

## Running it

```bash
export OPENAI_API_KEY=…
export GOOGLE_APPLICATION_CREDENTIALS=…

go test -tags=eval ./eval/... -run TestCorpus
```

It needs credentials and **costs money**, so it is not part of CI
([ADR-0022](adr/0022-offline-testing.md)). Run it before a release, after any
change to prompting, normalisation, grounding or scoring, and when changing
provider or model.

```bash
go test -tags=eval ./eval/... -category invoices -difficulty poor-scan
go test -tags=eval ./eval/... -baseline report/2026-08-26-gpt-5.2-tesseract.json
```

The `-baseline` form reports the delta, which is the form worth running during
development.

---

## Reports

Committed to `eval/report/`, so a regression shows up in a diff.

```text
ovrin eval · 2026-08-26 · commit a1b2c3d
model gpt-5.2 · ocr tesseract 5.4 · reading text-first

invoices          n=40
  exact                     0.00
  precision                 0.00
  recall                    0.00
  fabrication               0.00

  by difficulty
    clean-digital  n=15     0.00
    good-scan      n=15     0.00
    poor-scan      n=10     0.00

confidence calibration
  ECE                       0.00
  0.9–1.0        n=0        accuracy 0.00
  0.7–0.9        n=0        accuracy 0.00
  below 0.7      n=0        accuracy 0.00

  auto-accept at 0.90       coverage 0.00  error 0.00
  auto-accept at 0.70       coverage 0.00  error 0.00

cost      $0.00 per document
latency   0.0s median · 0.0s p95
```

All zeros because nothing has been run. **Reports are committed even when they
are bad** — a public record of how wrong we are is the only kind that stays
honest ([`rules.md` §9.5](rules.md#9-documentation)).

---

## Contributing documents

The most valuable contribution to this project. Also the one most likely to
arrive with a licence problem, so read this before spending effort.

**Every document must be redistributable and free of real personal data**
(rule [§7.6](rules.md#7-untrusted-input)). Three acceptable sources:

1. **Public forms.** Government forms, published templates, documents already
   in the public domain.
2. **Synthetic.** Realistic layouts with invented data. Print and rescan them —
   a synthetic PDF that was never printed has no scanner noise and tests
   nothing about scans.
3. **Donated, with written permission**, and every identifier replaced with a
   synthetic value **of the same shape**. Replacing a 14-digit account number
   with `XXXX` destroys the format signal the extractor is being tested on;
   replace it with a different valid-shaped 14-digit number.

**A document that cannot be redistributed does not go in**, however useful it
would be. This constraint keeps out exactly the messy real-world documents that
would be most valuable, and it is not negotiable — a repository is forever and
`git rm` is not deletion.

What to submit:

- The document, in the right category directory
- `NNN.expected.json`, carefully labelled
- `NNN.meta.yaml`, with an honest `difficulty` and a real `redacted` note
- A pull request explaining where it came from and what makes it interesting

**Documents that are hard in a specific, describable way are worth more than
clean ones.** A skewed scan with a staple through a number teaches the corpus
something. A clean digital invoice, of which we already have fifteen, does not.

## See also

- [ADR-0023](adr/0023-evaluation-corpus.md) — why the corpus is in the repository
- [`confidence.md`](confidence.md) — what calibration would change
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — the contribution process
