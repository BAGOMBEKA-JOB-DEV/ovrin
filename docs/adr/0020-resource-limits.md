# ADR-0020: Every limit has a finite default

**Status:** Accepted · **Date:** 2026-08-26

## Context

Ovrin parses attacker-controlled binary formats
([ADR-0017](0017-untrusted-document-content.md)). PDF in particular is a
container format with compression, indirection and recursion, and all three are
weaponisable.

The documented attacks are concrete. A PDF stream may be encoded with nested
FlateDecode filters, so a 600 KB file on disk expands to ten gigabytes in
memory; in the worst case the host stops responding. A cross-reference table
can point objects at each other in a cycle, so a naive resolver recurses until
the stack is gone. A page tree can nest thousands deep. A single page can
declare a media box of implausible size, so rasterising it at 300 DPI asks for
an image larger than physical memory. A document can declare a hundred thousand
pages.

None of these require sophistication, and a library whose limits are "whatever
the machine has" turns every one of them into a denial of service against the
service embedding it.

The second-order problem is cost. A ten-thousand-page PDF sent to a
per-page-priced OCR provider is not a crash; it is an invoice. A library that
processes it without asking has spent somebody's money.

## Decision

**Every limit has a default and the default is finite** (rule
[§5.2](../rules.md#5-concurrency-and-resources)). Exceeding one returns an
error wrapping `ErrLimitExceeded` that names the limit and the option that
raises it.

| Limit | Default | Option |
|---|---|---|
| Source bytes | 64 MiB | `WithMaxSourceBytes` |
| Decompressed bytes, whole document | 512 MiB | `WithMaxDecompressedBytes` |
| Decompressed bytes, single stream | 64 MiB | `WithMaxStreamBytes` |
| Pages | 1000 | `WithMaxPages` |
| Object-graph depth | 64 | `WithMaxDepth` |
| Objects | 500 000 | `WithMaxObjects` |
| Rasterised pixels per page | 50 M | `WithMaxPagePixels` |
| Extracted text bytes | 32 MiB | `WithMaxTextBytes` |
| Concurrent pages | `min(4, GOMAXPROCS)` | `WithConcurrency` |
| Total wall time | none — use the context | `context.WithTimeout` |

Implementation commitments, because a limit that is not enforced structurally
is a limit that will be bypassed by the next code path added:

- Every decompressor is wrapped in a limited reader. Not checked afterwards —
  wrapped, so the bytes are never allocated.
- Every recursive parser takes a depth budget as a parameter.
- Byte counters are cumulative across the document, not per-stream, because
  a thousand streams of 1 MiB each is the same attack as one stream of 1 GiB.
- Limits are checked before allocation, never after.

There is deliberately **no default wall-clock timeout**. Go already has one
mechanism for that and inventing a second would let them disagree. The docs
recommend a context deadline on every extraction, and the quickstart shows one.

Limits are on the `Client`, so a service can run a permissive client for
trusted internal documents and a strict one for public uploads.

## Consequences

**Good.** The documented PDF resource attacks fail closed, on defaults, without
the user knowing they exist — which is the only security posture that works,
since users who have not read this ADR are the ones who need it. Runaway
provider spend is bounded by page limits. `min(4, GOMAXPROCS)` concurrency
means ovrin does not monopolise a host it shares.

**Bad.** Every default is wrong for somebody. A 1200-page loan file is
legitimate and will be rejected until someone reads an error message and raises
`WithMaxPages`. Ten limits is ten things to discover, ten to document and ten
to get wrong, and users will hit them one at a time in production rather than
all at once in testing. Checking before allocation constrains how the parser
can be written — some naturally streaming code has to become
count-then-allocate. And the numbers in that table are judgement, not
measurement; they are round numbers chosen to be comfortably above real
documents and comfortably below dangerous ones.

**Neutral.** Defaults will be revised against the evaluation corpus
([ADR-0023](0023-evaluation-corpus.md)) once there is evidence about real
document sizes. Raising a default is not breaking; lowering one is, and lands
in a minor release before v1 with a changelog note.

## Alternatives considered

- **No limits; let the caller impose them.** Rejected: the caller cannot — the
  allocation happens inside ovrin, and by the time they could observe it the
  memory is already gone.
- **Limits only when a `WithUntrustedInput()` option is set.** Rejected:
  secure by default is the only default that protects the people who did not
  read the documentation, and everyone believes their input is trusted.
- **A single memory budget instead of ten limits.** Rejected: attractive, and
  unimplementable in Go without arena allocation or reading runtime memory
  statistics, which are both approximate and racy.
- **Kill the process on limit exceeded.** Rejected: a library never terminates
  its host.
