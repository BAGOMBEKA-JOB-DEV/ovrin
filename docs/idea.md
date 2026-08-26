# The idea

## The problem

Applications receive information as documents. PDFs, scans, phone photographs
of receipts, forms filled in by hand, spreadsheets exported from something
else. Getting usable data out of them is a solved problem in the sense that
every organisation has solved it, and an unsolved problem in the sense that
every organisation has solved it differently and badly.

The typical shape of the solution:

```text
PDF parser
    + OCR provider
    + layout analysis
    + an AI provider
    + structured extraction
    + validation
    + confidence analysis
    + error handling
    + provider fallback
```

Nine integrations, assembled once per organisation, by whoever was available.
The result is complex, provider-locked, inconsistently validated, hard to
observe, and — the part that causes actual harm — unable to say how much it
should be trusted.

In Go this is worse than in Python. The document-intelligence ecosystem is
almost entirely Python: Docling, Unstructured, Marker, LlamaParse. A Go service
that needs to read documents either shells out to a Python process, stands up a
sidecar, or posts the file to a SaaS. There is no Go-native equivalent, and the
Go libraries that do exist are OCR wrappers or bindings to a Rust core.

## The thing that actually goes wrong

Every organisation solving this eventually meets the same failure, and it is
not "the extraction was wrong". It is **the extraction was wrong and nothing
said so**.

```text
Amount: 25,000     ← what the scan says
Amount:  2,500     ← what the pipeline returned
```

Both are well-formed numbers. Both pass type validation. Both satisfy a
`min=0` rule. A pipeline that returns one number and no context cannot
distinguish them, so the wrong one is paid.

This is the problem ovrin is built around. Extraction is table stakes; every
frontier model does it adequately. **Knowing when not to trust the extraction
is the part nobody ships**, and it is the part that decides whether a document
pipeline can be run without a human reading every page.

## The approach

Ovrin is a Go library that turns unstructured documents into validated, typed
data. You declare the shape you want as a Go struct; ovrin runs a staged
pipeline and returns that struct alongside the evidence for it.

```go
type Invoice struct {
    Number string  `ovrin:"invoice number,required"`
    Vendor string  `ovrin:"vendor company name"`
    Total  float64 `ovrin:"total amount including tax,required,min=0"`
}

res, err := ovrin.Extract[Invoice](ctx, client, file)
```

Three commitments distinguish it from a wrapper around a model call.

**Staged, not direct.** Text layer first — when a PDF carries its characters,
reading them is exact and nearly free, and rendering them to pixels for a model
to read back is a lossy round trip. OCR when there is no text layer. Vision as
a distinct reading, not a shortcut. Published comparison finds a multistage
pipeline consistently outperforming the direct document-to-model baseline by a
substantial margin.

**Confidence from several signals that fail differently.** Not the model's
self-report, which is uncorrelated with correctness. Not token logprobs, which
one major provider does not expose at all and which saturate to a constant
under the constrained decoding we rely on. Instead: does the value appear in
the document, do the characters read cleanly, does it satisfy its rules, is it
consistent with its siblings, and — when you ask for two readings — do two
independent readings agree.

**Every value points back at the document.** Page, bounding box, source span,
and which reading produced it. That is what makes review fast enough to be
viable, audit possible years later, and fabrication detectable at all.

## Design principles

**Provider independent.** Three small interfaces. No vendor is privileged, and
a local model is as first-class as a frontier one.

**Schema first.** The struct is the contract. It determines what is asked for,
what is validated and what comes back — and because the output shape is fixed
before the document is read, a document cannot change it.

**Strongly typed.** `res.Data.Total` is a `float64` at compile time. This is
the entire reason to do this in Go.

**Honest about uncertainty.** A field that could not be read is absent and says
so. Nothing is guessed to satisfy a struct. A value that appears nowhere in the
document is flagged rather than returned as fact.

**Zero dependencies in the core, no cgo.** `go get` pulls nothing. Static
builds and cross-compilation keep working, because that is substantially why
people choose Go for backend services.

**Untrusted input by default.** Documents arrive from claimants and suppliers.
They are parsed with finite limits and prompted as data, never as instruction.

**Observable.** Hooks in the core, OpenTelemetry in its own module, and
structurally no way for a document value to reach a trace.

## Non-goals

Stated so they are not proposed repeatedly.

**Not a document classifier.** Ovrin extracts against the schema you give it.
Deciding whether a file is an invoice or a receipt is a different problem.

**Not a SaaS.** A library, and eventually a CLI and an optional service you run
yourself. No hosted endpoint, no API key from us.

**Not an OCR engine.** Ovrin orchestrates OCR. Writing a better one is somebody
else's decade.

**Not a review interface.** Ovrin produces everything a review UI needs and
builds none of it. That is an application concern and every organisation's is
different.

**Not a PDF toolkit.** No editing, signing, merging or form filling. It reads
documents for one purpose.

**Not a guarantee against prompt injection.** Nobody can offer that. Ovrin is
built so the obvious attacks fail and the rest are flagged rather than silently
accepted.

## Who it is for

Go services that receive documents and must do something reliable with them:

- **Government** — registration forms, applications, certificates, identity
  documents
- **Finance** — bank statements, invoices, receipts, KYC packets
- **Education** — transcripts, admission forms, certificates
- **Healthcare** — intake forms, lab reports, referrals
- **Enterprise** — contracts, purchase orders, supplier invoices

The common thread is that a wrong value has a consequence, so the pipeline has
to say when it is unsure.

## Why this project

The maintainer has spent years on large institutional systems — an education
management information system holding tens of millions of records, with the
data validation, duplicate detection and bulk import workflows that implies —
and has already built [skyl](https://github.com/BAGOMBEKA-JOB-DEV/skyl), a Go
library abstracting AI providers.

Ovrin is not "AI is popular, so here is an AI project". It is that **large
systems constantly receive unstructured data, turning it into reliable
structured information is still unnecessarily difficult, and the difficult part
is not the extraction — it is knowing whether to believe it.**

## See also

- [`architecture.md`](architecture.md) — how it is built
- [`pipeline.md`](pipeline.md) — what happens to a document
- [`roadmap.md`](roadmap.md) — what exists and what is next
- [`adr/`](adr/) — every decision, with its costs
