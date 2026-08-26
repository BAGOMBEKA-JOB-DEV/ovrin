# ADR-0031: Documents are read whole, and streaming is deferred

**Status:** Accepted · **Date:** 2026-08-26

## Context

[`docs/roadmap.md`](../roadmap.md) lists "batch processing, and streaming for
documents that do not fit in memory" among the work wanted before v1.0. Batch
processing shipped as `ExtractBatch`. Streaming did not, and this records why
rather than leaving an unexplained gap in a list.

Today `internal/detect/source.go` reads a source to completion before anything
else happens. `Reader`, `Bytes` and `File` all end at the same `readAll`, which
grows one buffer under `MaxSourceBytes` and hands the whole document on as a
`[]byte`. Every stage downstream assumes that:

- **`internal/pdf` needs random access.** A PDF is read from its trailer
  backwards — the cross-reference table is at the end, and objects are then
  fetched by byte offset in whatever order the page tree demands. This is not
  an implementation choice that could be relaxed; it is the format.
- **The resource limits are enforced against a known size.** ADR-0020 puts the
  checks *before* allocation, and several of them —
  `MaxDecompressedBytes`, `MaxObjects`, `MaxPagePixels` — are meaningful only
  because the whole object graph is available to count.
- **`DocumentOCR` providers take the document.** `ocr/google`, `ocr/azure` and
  `ocr/textract` accept a complete PDF in one request. There is no partial
  form to send them.
- **Extraction needs the whole document.** A model cannot fill a schema from
  the first page and revise it on the fourth; the prompt carries every page at
  once, which is why `WithConcurrency` bounds page *reading* and not the model
  call.

So "streaming" cannot mean what it usually means. The realistic version is
narrower: hold the source on disk or in a memory-mapped region and read pages
from it lazily, so that peak memory is a page rather than a document.

## Decision

**Documents are read whole. Streaming is deferred, and is not a v1.0 gate.**

The four conditions in [ADR-0024](0024-versioning-and-stability.md) are the
gate, and none of them is this. The roadmap listed streaming alongside them,
which conflated a feature with a release criterion; that has been corrected.

When it is built, the shape is already constrained by the above:

1. `Source` gains an internal path that yields an `io.ReaderAt` and a size,
   rather than a `[]byte`. The public constructors do not change — `Reader`
   spills to a temporary file above a threshold, `File` opens directly, and
   `Bytes` wraps what it already has.
2. `internal/pdf` takes the `io.ReaderAt` instead of a slice. It already seeks
   by offset, so this is narrower than it sounds.
3. The limits move from "bytes in a buffer" to "bytes read", which is a
   stricter accounting, not a weaker one.
4. `DocumentOCR` adapters keep taking the whole document. A provider that
   wants a PDF in one request gets one; the saving is in ovrin's own memory,
   not in theirs.

Nothing about this is blocked. It is simply larger than it looks, and it
touches the code where the security properties live.

## Consequences

**Good.** The limit model stays simple, and simple is what makes it
auditable: every ceiling is checked against a number that is already known
rather than one that is still arriving. `internal/pdf` keeps a `[]byte` and
therefore keeps bounds checks the compiler can reason about, which matters for
a parser whose whole job is hostile input. Callers who need to process many
large documents have `ExtractBatch` and `WithConcurrency` to bound how many are
resident at once, which addresses the common case — a directory of scans —
without any of this work.

**Bad.** Peak memory is the size of the largest document, not the size of a
page, and ovrin is aimed at exactly the sectors that hold thousand-page
scanned files. A 500MB archival PDF needs 500MB before a single page is read,
and `WithMaxSourceBytes` turns that from an out-of-memory kill into a refusal —
which is safer and still a refusal. `Reader` is the worst case: a caller
streaming from object storage has already paid to download the whole object
before ovrin looks at it, and cannot discover in advance that it will be
rejected. Deferring also means the change lands later, against more callers
and more adapters, than it would today.

## Alternatives considered

- **Build it now.** Rejected on sequencing, not merit. It re-architects
  `internal/detect`, `internal/pdf` and the limit checks — the three places
  where the threat model is actually enforced — and doing that before there is
  a calibrated evaluation corpus means changing the security-critical path
  with no measurement to tell whether behaviour moved.

- **Stream only the easy formats.** CSV genuinely streams; `internal/office`
  could read rows without holding the file. Rejected: it would make peak memory
  depend on which format a document turned out to be, so a caller sizing a
  container would have to size it for PDF anyway. A limit that holds for some
  inputs is not a limit.

- **Memory-map every source.** Attractive, and it is close to the design
  sketched above. Rejected as the *stated* decision because `mmap` is not
  portable through the standard library, and ADR-0010 keeps this module free of
  cgo and of platform-specific build tags. `io.ReaderAt` over an `*os.File`
  gets most of the benefit with none of that.

- **Say nothing and leave the roadmap item open.** Rejected. An unexplained
  open item reads as an oversight, and the next person to pick it up would
  rediscover the same four constraints before finding out they are known.
