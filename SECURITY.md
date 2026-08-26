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

### Which Go toolchain you need

`go.mod` declares `go 1.22`, which is a *language* floor
([ADR-0003](docs/adr/0003-go-floor-and-generics.md)). It is not a statement
that 1.22.0 is safe to run ovrin on.

Ovrin hands attacker-controlled bytes to `archive/zip`, `encoding/xml`,
`compress/flate` and `image/*`. Defects in those are defects in ovrin's
parsing path, and the only fix for them is a newer toolchain — ovrin cannot
patch the standard library. **Build with the latest patch release of whichever
minor version you use.** For 1.22 that currently means **1.22.4 or newer**,
which carries the fix for `GO-2024-2888`, a panic on a crafted zip central
directory reachable from any DOCX, XLSX or PDF ovrin accepts.

Run `govulncheck ./...` against your own build. It reports the toolchain you
are actually using, which is the only thing that matters here; ovrin's CI
result cannot tell you about yours.

Two consequences worth stating plainly:

- A stdlib advisory whose fix landed only in a *later minor* than the one you
  run cannot be obtained without upgrading that minor. `GO-2026-6088`
  (recursion depth in `encoding/xml`, fixed in 1.25.13) is the current example.
  We checked this one: every XML reader in the repository uses
  `xml.Decoder.Token` exclusively — no `Unmarshal`, no `DecodeElement`, and
  `Decoder.Skip` is deliberately replaced by an iterative equivalent — and
  `Token` maintains its element stack on the heap rather than recursing, which
  we verified holds at two million levels of nesting. Ovrin's own recursive
  walkers are separately bounded by `WithMaxDepth`, default 64. So that
  advisory does not appear to reach ovrin. We will say so if that changes.
- A toolchain vulnerability reachable through ovrin is in scope for a report
  even though the fix is not ours to ship, because the reachability may be.

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
