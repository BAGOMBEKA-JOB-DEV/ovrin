# ADR-0022: No network in unit tests; an offline sandbox serves the providers

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin's providers are network services that cost money per call. Testing
against them directly gives a suite that is slow, flaky, expensive, unavailable
on an aeroplane, and red whenever a vendor has an incident — which trains
everyone to ignore red builds, which is the actual failure.

But a suite that only exercises in-process mapping logic misses the failures
that matter most: a request that serialises wrongly, a response shape that
changed, a connection that drops mid-body, a context cancellation that leaks a
goroutine. Those only appear when bytes cross a socket.

Skyl resolved this with three tiers and a local sandbox that speaks real
provider wire protocols. The approach transfers, with one addition: ovrin also
needs *document* fixtures, and those carry a privacy obligation that API
fixtures do not.

## Decision

Three test tiers, distinguished by build tag.

**Untagged — `go test ./...`.** In-process. Fakes for every seam, `httptest`
for anything HTTP. No sockets to external hosts, no credentials, no cost. Runs
on every commit and must pass offline. This is where the pipeline, schema
reflection, validation, confidence, PDF parsing and normalisation are tested.

**`-tags=sandbox`.** The full stack over real sockets against
`internal/sandbox`, a local server implementing the wire protocols of the
OCR and model providers ovrin adapts, including their error responses,
malformed bodies and mid-response disconnections. No credentials, no cost, runs
in CI. This is where adapters, fallback chains, cancellation and goroutine
lifetimes are tested.

**`-tags=integration`.** Real providers, real credentials, real money. Skipped
without credentials, never run in CI. CI does run `go vet -tags=integration` so
the code cannot rot.

Supporting decisions:

**The sandbox is adversarial.** It serves malformed JSON, truncated responses,
wrong content types, 429s with and without `Retry-After`, and disconnections
mid-body — deliberately, on request. A sandbox that only serves happy paths
tests nothing an in-process fake would not.

**Document fixtures are real files, and redacted** (rules
[§3.5](../rules.md#3-testing), [§7.6](../rules.md#7-untrusted-input)). PDFs
produced by Word, LaTeX, Chrome print, Excel export and real scanners. A
synthetic PDF written by our own code proves only that we can read our own
writing, and the failures that matter come from other people's writers.

**Fakes, not mocks** (rule [§3.4](../rules.md#3-testing)). Hand-written structs
implementing the seams. No mocking framework, no assertion library.

**Every adapter passes the shared contract suite** in `internal/adaptertest`
(rule [§3.1](../rules.md#3-testing)).

Extraction *accuracy* is not tested here at all. It is measured by the
evaluation harness ([ADR-0023](0023-evaluation-corpus.md)), because accuracy is
a distribution and a unit test is a boolean.

## Consequences

**Good.** The default suite is fast, free, deterministic and works offline. The
sandbox catches the wire-level failures that in-process fakes cannot, at no
cost, in CI. Adversarial serving means error handling is exercised routinely
instead of only in production. Contributors need no credentials to contribute.

**Bad.** The sandbox is a substantial piece of code that must track what four
or five vendors actually do, and when a vendor changes its wire format the
sandbox silently becomes fiction — tests pass, production breaks. Only the
integration tier catches that, and it runs rarely. Three tiers means
contributors must learn which tier a test belongs in and will sometimes choose
wrong. Real document fixtures are large binaries in git history forever, and
redacting them properly is careful manual work that is easy to do badly.

**Neutral.** Cassette-style record and replay of real provider exchanges is
plausible later. It is not in v0.1 — the sandbox covers the same ground and
does not require a recording to be re-taken whenever a test changes.

## Alternatives considered

- **Unit tests only, no sandbox.** Rejected: never exercises serialisation,
  cancellation or connection failure, which is where adapter bugs live.
- **Integration tests as the primary suite.** Rejected: slow, costly, flaky,
  offline-hostile, and requires credentials to contribute.
- **A third-party HTTP mocking library.** Rejected: a dependency, and it
  records shapes rather than serving a protocol, so it cannot produce a
  mid-body disconnection.
- **Synthetic PDFs generated at test time.** Rejected: tests our own writer, not
  the world's. Useful as a supplement, useless as the basis.
