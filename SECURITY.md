# Security policy

## Reporting a vulnerability

**Do not open a public issue.**

Report through
[GitHub Security Advisories](https://github.com/BAGOMBEKA-JOB-DEV/ovrin/security/advisories/new),
which is private and lets us work on a fix before disclosure. If you cannot use
it, email **bagombekajob@gmail.com**.

Useful things to include: what an attacker can do, a document or input that
reproduces it (redacted of anything real), the affected module and version, and
your view of severity.

### What to expect, and the honest limit

| Stage | Target |
|---|---|
| Acknowledgement | 3 working days |
| Initial assessment | 10 working days |
| Fix or a public statement of why not | 90 working days |

**Ovrin has one maintainer.** Those targets are what will be attempted, not a
service-level agreement, and a serious illness or a busy month will blow
through them. If you have not heard back within the acknowledgement window,
email again — the likely cause is that a notification was missed.

Credit is given in the advisory unless you would rather it were not.

## Supported versions

Pre-v1, only the latest release of each module receives fixes. There are no
backports. See [ADR-0024](docs/adr/0024-versioning-and-stability.md).

## What is in scope

Ovrin's threat model is documented in full at
[`docs/threat-model.md`](docs/threat-model.md). In scope:

- A document that can crash, hang or exhaust the memory of the calling process
- A document that can cause ovrin to make an unintended network request
- Document content leaking into an error, event, trace or metric
- Credentials leaking into an error, event, trace or metric
- A parser defect reachable from a crafted document
- Prompt injection that ovrin's documented mitigations should have caught, and
  did not

## What is out of scope

Stated plainly so that reports are not wasted.

**Prompt injection that changes a value within the schema.** Ovrin does not
prevent prompt injection and does not claim to. What it guarantees is that such
a value is ungrounded, low-confidence and flagged for review rather than
silently accepted ([ADR-0017](docs/adr/0017-untrusted-document-content.md)).
A report showing an injected value that was **not** flagged is in scope. A
report showing that injection is possible at all is not.

**Extraction being wrong.** A wrong value is a bug, not a vulnerability. Open
a normal issue.

**Malware embedded in a document.** Ovrin does not execute embedded content and
is not a scanner. Scan before processing.

**Forged documents.** Ovrin does not verify signatures or detect forgery. A
well-forged invoice extracts perfectly, by design.

**Provider security.** How a third-party model or OCR provider handles your
data is their policy.

**Vulnerabilities in a dependency of an optional adapter**, unless ovrin's use
of it is what makes it reachable. Report those upstream; tell us too, and we
will bump.

## How ovrin handles credentials

Adapters read credentials only from what you pass them. Ovrin never reads the
environment itself (rule [§6.4](docs/rules.md#6-adapters)), so a program cannot
end up talking to the wrong account through a stray variable.

Credentials never appear in errors, events, traces or metrics. This is asserted
by a test in the shared adapter contract suite, not merely intended.

## How ovrin handles document content

Documents are not written to disk, not cached, and not retained between calls.
Content leaves your process only through providers you configure. The `Event`
struct has no field capable of carrying a document value, which closes the
observability exfiltration route structurally rather than by convention
([ADR-0021](docs/adr/0021-observability.md)).

Full detail in [`docs/data-handling.md`](docs/data-handling.md).

## Supply chain

The core module has zero external dependencies, which is the strongest
supply-chain control available — there is nothing to compromise. Adapter
dependencies are quarantined per module. `govulncheck` runs per module on every
pull request, GitHub Actions are pinned by commit SHA, and releases carry an
SBOM and build provenance attestation.
