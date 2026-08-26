# Support

Ovrin has one maintainer ([`MAINTAINERS.md`](MAINTAINERS.md#bus-factor)).
Everything on this page follows from that, and saying it first is more useful
than listing channels nobody is watching.

## Read this first

Most questions about ovrin are questions about a decision, and the decisions
are written down. Checking takes a minute and usually beats waiting for a
reply:

- [`docs/`](docs/) — the architecture, the pipeline, the schema grammar, the
  confidence model and the threat model.
- [`docs/getting-started.md`](docs/getting-started.md) — if the answer you want
  is "how do I do the thing at all".
- [`docs/adr/`](docs/adr/) — thirty decision records. Nearly every "why not X"
  is in one of them, together with the alternatives that lost and what the
  choice cost.
- [`docs/feature-matrix.md`](docs/feature-matrix.md) — what each adapter
  actually supports, including the options it silently ignores.

## Where to go

| What you have | Where it goes |
|---|---|
| A bug | [A new issue](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/issues/new/choose), using the bug template |
| A question about using ovrin | [A new issue](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/issues/new/choose) — a blank one is fine |
| An idea for a feature | [A new issue](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/issues/new/choose), using the feature template |
| Disagreement with an ADR | An issue naming the ADR. This is welcome, not a nuisance |
| A security vulnerability | **Never an issue.** [`SECURITY.md`](SECURITY.md#reporting-a-vulnerability) |
| A Code of Conduct concern | [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) |
| A change you have written | [`CONTRIBUTING.md`](CONTRIBUTING.md) |

There is no chat, no mailing list and no Discussions tab. One maintainer
answering in one place is slower to look at and faster to actually get an
answer from, and it leaves a record the next person can find.

Questions in issues are welcome even when they turn out not to be bugs. A
question that had to be asked is usually a documentation defect, and it gets
closed by fixing the documentation.

## What to expect

**No service-level agreement, because there is nobody to hold to one.** In
practice: issues are usually read within a week, and a question with a small
reproducer is answered much faster than one without. A quiet month is a busy
maintainer, not a dead project — [`MAINTAINERS.md`](MAINTAINERS.md) says how
long to wait before concluding otherwise, and that number is 90 days.

Security reports are the one exception. They have stated targets, in
[`SECURITY.md`](SECURITY.md#what-to-expect-and-the-honest-limit), and those
targets are attempted rather than promised for the same reason.

Ovrin is pre-v1. Answers may be "that is not decided yet" or "that is a known
gap", and those are real answers rather than deflections.

## What is out of scope

Stated so that nobody spends an evening writing a report that will be closed.

- **Debugging your documents.** Extraction being wrong on a document we cannot
  see is not something anyone can act on. A minimal reproducer, or a document
  you are allowed to share, turns it into a bug report.
- **Support for your provider account.** Credentials, quotas and billing at
  OpenAI, AWS, Google or Azure are between you and them.
- **Integration consulting.** How to fit ovrin into your system is a good
  question and not one this project can staff.
- **Private support.** There is no paid tier and no private channel other than
  the security one, which is for vulnerabilities only.
- **Anything covered by an ADR, reopened without new information.** Disagreeing
  with a decision is fine; the useful form is an argument the ADR did not
  already consider.

## The fastest way to get something fixed

Send a pull request. [`CONTRIBUTING.md`](CONTRIBUTING.md) has the setup, the
gate to run before opening one, and the engineering standard review will hold
it to. For a change to an adapter, or a document for the evaluation corpus
([`docs/evaluation.md`](docs/evaluation.md#contributing-documents)), that is
comfortably the shortest path from problem to release.
