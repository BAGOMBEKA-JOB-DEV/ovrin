# Validating ovrin before you adopt it

You are considering a dependency for something that matters. This document is
the honest version of "should you".

> **Ovrin is not implemented.** Today the answer is no: there is nothing to
> adopt. What follows is how to assess it when there is, and what to hold it
> to.

---

## The short version

| Question | Answer |
|---|---|
| Is it stable? | No. Pre-v1, breaking changes in minor releases ([ADR-0024](adr/0024-versioning-and-stability.md)) |
| Is it accurate? | Unmeasured. No figure is published because none can be reproduced yet |
| Is confidence trustworthy? | It ranks. It is not calibrated ([`confidence.md`](confidence.md)) |
| Will it break my build? | Core has zero dependencies and no cgo |
| Does data leave my process? | Only to providers you configure ([`data-handling.md`](data-handling.md)) |
| Is it safe against hostile documents? | Structurally hardened, not immune ([`threat-model.md`](threat-model.md)) |
| Who maintains it? | One person ([`../MAINTAINERS.md`](../MAINTAINERS.md)) |

---

## Check the claims yourself

**Zero dependencies.** Not "few". Verify:

```bash
go mod graph | grep -v '^github.com/BAGOMBEKA-JOB-DEV/ovrin' | head
# expect nothing for the core module
```

**No cgo.** The property most likely to be quietly broken by a dependency:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
```

**No network in the default test suite.** Run it with networking disabled. It
should pass ([ADR-0022](adr/0022-offline-testing.md)).

**Errors carry no document content.** Feed it a document with a distinctive
string, force a failure, and grep the error. This is asserted by the contract
suite; verify it anyway.

**Events carry no document content.** Read the `Event` struct. There should be
no field a value could occupy ([ADR-0021](adr/0021-observability.md)).

---

## Evaluate it on your own documents

Nobody's benchmark predicts your corpus. The published corpus will not contain
your document types, your scanner, your languages or your layouts.

1. Assemble thirty of your own documents, spanning your real difficulty range —
   including the ones that go wrong today.
2. Label them by hand. This is the expensive step and there is no substitute.
3. Run ovrin. Compare.
4. **Look at where errors fall relative to confidence.** This matters more than
   the headline accuracy: a library that is 92% accurate and knows which 8% is
   wrong is far more useful than one that is 96% accurate and cannot tell.
5. Choose your threshold from that curve, not from a number that sounds high.

The [evaluation harness](evaluation.md) works on your own corpus; the format is
documented and the corpus directory is just files.

---

## Questions to ask

**Of the confidence score.** Is it calibrated yet? If not, treat it as ordering
for a review queue, and never present it to an end user as a probability.

**Of the review rate.** What fraction of your documents get flagged? A library
that flags 60% has not saved you anything. Measure it before committing.

**Of the fabrication rate.** How often does it produce a value for a field the
document does not contain? Grounding is designed to catch this; verify it does
on your documents.

**Of the failure mode.** When it is wrong, is it obviously wrong or plausibly
wrong? Plausibly-wrong is the expensive kind.

**Of the cost.** Per document, at your volume, in your configuration. Two
readings roughly doubles it. A fallback chain can silently move you from a
cheap provider to an expensive one.

---

## Reasons not to adopt it

Stated because a document that lists only reasons to adopt is a sales page.

**You need stability now.** Pre-v1 means breaking changes in minor releases.
Wait for v1.0, or vendor a version and accept that you are on your own.

**You need a published accuracy guarantee.** There is none, and there will not
be one that is not reproducible.

**You need calibrated probabilities today.** v1.0 work.

**Your documents are all one fixed layout.** A template-based extractor, or a
dedicated OCR API with a per-document price, will beat a general pipeline on
cost and reliability. Ovrin's advantage is variety.

**You are not writing Go.** The Python ecosystem — Docling, Unstructured,
Marker — is mature, well-resourced and has been measured. Ovrin exists because
Go has nothing equivalent, not because it is better than those.

**You cannot accept a bus factor of one.** It is one. Apache-2.0 and a
documented architecture make a fork viable, and that is the mitigation, not a
denial.

---

## What we hold ourselves to

If any of these stops being true, it is a bug worth reporting:

- No accuracy claim that `go test -tags=eval` cannot reproduce
  (rule [§3.8](rules.md#3-testing))
- No confidence presented as a probability until it is calibrated
- No downside omitted from an ADR (rule [§9.4](rules.md#9-documentation))
- No document content in errors, events or traces (rule
  [§7.5](rules.md#7-untrusted-input))
- No AGPL dependency (rule [§4.4](rules.md#4-dependencies))
- No external dependency in the core (rule [§4.1](rules.md#4-dependencies))
- Every provider limitation in the feature matrix, including the ones we would
  rather not mention (rule [§6.5](rules.md#6-adapters))
