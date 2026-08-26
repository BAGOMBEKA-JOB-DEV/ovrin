# Glossary

Terms used consistently throughout ovrin's documentation and code. Where a word
has a common meaning that differs from ours, both are given.

---

**Acquisition** — the pipeline stage that obtains content from a document,
choosing between the text layer, OCR and vision. Stage 2 of
[`pipeline.md`](pipeline.md).

**Adapter** — an implementation of one of ovrin's three seams, living in its
own module. `ocr/tesseract` and `model/skyl` are adapters. An adapter maps; it
does not decide (rule [§6.2](rules.md#6-adapters)).

**Agreement** — the confidence signal measuring whether two independent
readings produced the same value. Not correctness: two readings can be wrong
the same way.

**Candidate** — one reading's answer for a field, when readings disagreed.
Carried on `FieldResult.Candidates` with its reading and provenance.

**Cross-field validation** — checking that fields are consistent with each
other: line items summing to the total, an issue date before a due date. Needs
one reading. Distinct from *cross-validation*.

**Cross-validation** — comparing two independent readings of the same document
field by field. Needs two readings ([ADR-0014](adr/0014-cross-validation.md)).

**Derived value** — a value the model computed or reformatted rather than
copying: a total summed from line items, a date normalised from prose. It
grounds at 0.5 and is not suspicious.

**Document** — a parsed source with a known format and page count. The input to
the pipeline after detection.

**Explanation** — the decomposition of a field's result: its signals,
provenance, candidates and validation outcomes. A struct, not a string
([ADR-0016](adr/0016-explain-returns-data.md)).

**Field key** — the path identifying a field in `Result.Fields`, lowercased
with dots and indices: `vendor.name`, `items[0].unit_price`.

**Grounding** — searching the source text for an extracted value. The cheapest
strong confidence signal. A value found nowhere in the document may have been
invented.

**Media box** — the PDF page rectangle defining the physical page. Text
positioned outside it is invisible to a reader and is a known
prompt-injection carrier.

**Model** — ovrin's seam for the component that turns content plus a schema
into JSON. One method ([ADR-0007](adr/0007-model-seam.md)). Not the same as "an
LLM" — a `Model` implementation could be a rules engine.

**Normalisation** — converting raw per-page content into one text stream with
reading order resolved, whitespace collapsed and Unicode normalised, **while
preserving a mapping back to original positions**. The offset-preservation
obligation is what makes this stage hard.

**OCR** — optical character recognition. In ovrin, also the name of the seam
for it. An OCR provider returns words with positions and confidences, never a
plain string ([ADR-0009](adr/0009-ocr-seam.md)).

**Provenance** — where a value came from: reading, page, bounding box, source
span, and the method that produced it ([ADR-0015](adr/0015-provenance.md)).

**Rasterise** — render a document page to an image, so it can be given to OCR
or a vision model. The hardest constraint in the project, because there is no
good pure-Go PDF renderer ([ADR-0010](adr/0010-no-cgo-in-core.md)).

**Reading** — one way of obtaining content from a document: `ReadingText`,
`ReadingOCR`, `ReadingVision`. Two readings can be run and compared.

**Review reason** — a named cause for `NeedsReview`, identifying the field and
why. Low confidence, disagreement, a missing required field, failed grounding,
or suspicious content.

**Rule** — a machine-checked constraint in a struct tag after the description:
`required`, `min`, `max`, `format`, `enum`. The vocabulary is closed
([ADR-0006](adr/0006-tag-grammar.md)).

**Scorer** — the pluggable component combining signals into a confidence
number. Ovrin ships a weighted-mean default with hard floors; a user with
labelled data can fit a better one.

**Seam** — an interface the core declares and never implements, crossed only by
adapters in separate modules. Ovrin has three: `Model`, `OCR`, `Renderer`.

**Signal** — one named input to confidence, with a value, a weight and a
one-line note. `ocr`, `schema`, `cross_field`, `agreement`, `format`,
`grounding`. Absent when it does not apply, never zero
([ADR-0013](adr/0013-multi-signal-confidence.md)).

**Source** — the input to `Extract`: an `io.Reader`, a `[]byte` or a path,
before detection has identified what it is.

**Span** — a byte range into the normalised text, identifying where a value
appears.

**Text layer** — the characters embedded in a PDF by whatever produced it, as
opposed to pixels. Present in most PDFs that were not scanned. Reading it is
exact and nearly free.

**Untrusted content** — every byte of every document (rule
[§7.1](rules.md#7-untrusted-input)). Text recovered from a document is data,
never instruction.

**Usability heuristic** — the test deciding whether a page's text layer can be
trusted, rather than being the output of a broken font encoding. Thresholds in
[`pipeline.md`](pipeline.md).

**Vision** — reading a page by giving its image to a multimodal model. A
reading, not a shortcut past the pipeline
([ADR-0012](adr/0012-text-first-ocr-on-demand.md)).
