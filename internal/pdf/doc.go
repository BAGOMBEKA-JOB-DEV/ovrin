// Package pdf reads the text layer of a PDF, with a box for every word.
//
// It exists because the alternatives are disqualified rather than because
// writing a PDF parser is a good idea: unipdf is AGPL, and pdfcpu — the
// strongest option — would break the core's zero-dependency rule. Neither
// gives positions cleanly, and positions are what provenance is built from
// (docs/adr/0011-pdf-text-extraction.md, docs/adr/0015-provenance.md).
//
// # Scope
//
// This is a reader for one purpose. It parses the cross-reference table and
// cross-reference streams, object streams, the filters the standard library
// already provides, font encodings and ToUnicode CMaps, and the text-showing
// and text-positioning operators. That is enough to reconstruct characters
// and their boxes.
//
// It also follows the colour operators far enough to say what colour a word
// was painted in and what colour the paper under it is. That is not imaging
// and it is not there for its own sake: text drawn in the page's background
// colour is one of the documented ways an instruction is hidden from the
// person reviewing a document, and internal/normalise cannot report a class
// of attack that no reading ever measures
// (docs/adr/0017-untrusted-document-content.md mitigation 4,
// docs/threat-model.md T1). A colour space this package will not convert —
// Separation, DeviceN, Lab, a pattern — yields no colour rather than a guess,
// and no colour skips the check.
//
// It does not render, edit, sign, decrypt, fill forms, manage colour or decode
// an image. Those are the hard parts of PDF and ovrin needs none of them; not
// attempting them is what makes the rest tractable.
//
// # What it refuses, by name
//
// A refusal is a better outcome than a plausible wrong answer, so three
// conditions are named rather than half-handled:
//
//   - An encrypted document returns [ErrEncrypted] naming the security
//     handler. internal/detect makes a cheap conservative check at the door;
//     this package is the authority.
//   - A stream filter this package does not implement — JBIG2Decode,
//     JPXDecode, DCTDecode, CCITTFaxDecode, Crypt — returns
//     [ErrUnsupportedFilter] naming it. Only names from that fixed vocabulary
//     are echoed; anything else is reported as unrecognised, because a filter
//     name is a byte string an attacker chooses and error messages end up in
//     logs (docs/rules.md §7.5).
//   - A page whose text layer decodes to nothing usable reports
//     [ErrNoTextLayer] through [Page.Unusable], so the pipeline falls through
//     to OCR (docs/adr/0012-text-first-ocr-on-demand.md). It never returns
//     partial gibberish as though it were text: a broken ToUnicode table
//     producing plausible rubbish poisons everything downstream, which is why
//     stage 2's three-threshold heuristic is measured here and exposed as
//     [Stats]. A symbolic TrueType with no /Encoding and no /ToUnicode is the
//     sharpest case: its codes mean whatever its own font program says, so
//     they are counted undecodable rather than read through a Latin encoding
//     that would produce the right shape and the wrong letters.
//
// # Limits
//
// Every limit primitive comes from internal/detect and none is reimplemented
// here (docs/adr/0020-resource-limits.md). Decompressors are constructed
// inside a detect.LimitedReader so a bomb's output is never allocated;
// recursion through the object graph, the page tree, form XObjects and nested
// content carries a detect.Depth budget passed as a parameter; decompressed
// and extracted bytes are charged to a detect.Counter shared by the whole
// document. Counts are checked before the allocation they authorise, never
// after.
//
// # Errors
//
// Nothing derived from the document goes in an error. Errors carry the
// operation, the page number and the object number — all of them ovrin's own
// structural coordinates — and never a text fragment, a font name, a
// dictionary key or a decoded byte (docs/rules.md §2.5).
//
// # Concurrency
//
// A [Doc] is not safe for concurrent use: object resolution memoises, so two
// goroutines calling [Doc.Page] at once race. Callers that extract pages in
// parallel should open one Doc per goroutine or serialise the calls.
//
// This package cannot import the root — the root imports the pipeline, which
// imports this — so it declares its own sentinels and the pipeline classifies
// them onto ovrin's. See docs/architecture.md, "Layout".
package pdf
