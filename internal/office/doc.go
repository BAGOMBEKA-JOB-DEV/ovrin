// Package office reads the text of a DOCX, XLSX or CSV, with line structure
// but deliberately without invented geometry.
//
// It is the sibling of internal/pdf and exists for the same reason: these
// formats carry a real text layer, so reading them needs neither OCR nor a
// renderer, and the fast exact path is the only path worth having
// (docs/pipeline.md stage 2, docs/adr/0012-text-first-ocr-on-demand.md).
//
// # Positions, and why there are none
//
// [normalise.Word] wants a box in points. These formats have none to give. A
// DOCX has no fixed layout until a renderer with particular font metrics and
// a particular paper size paginates it, and a spreadsheet cell has a grid
// address rather than a point on paper. Every box this package could emit
// would be a box it made up.
//
// So it emits the zero [normalise.Rect] for every word, and it is precise
// about what that costs. internal/normalise treats zero geometry as "the
// reading gave no positions" and responds by skipping the checks that need
// them rather than running them against a guess: the off-page check and the
// background-colour check are both conditioned on non-zero geometry, and
// reading-order analysis falls back to the reading's own order instead of
// running an XY-cut over rectangles. That fallback behaviour is exactly right
// here and a synthesised layout would break all three of them:
//
//   - The off-page detector would begin reporting findings that are
//     properties of the page size this package invented rather than of the
//     document. A security detector that fires on an assumption is worse than
//     one that abstains (docs/adr/0017-untrusted-document-content.md).
//   - The XY-cut would discover columns that are artefacts of an invented
//     column width, and reorder text that was already in document order —
//     which for these three formats is authoritative, because all three store
//     content in reading order.
//   - A review interface would draw a rectangle over a coordinate that
//     corresponds to nothing.
//
// What is supplied instead is [normalise.Word.Line], which is real structure
// and not a guess: a DOCX paragraph, a table row, a spreadsheet row, a CSV
// record. internal/normalise trusts a reading's own line grouping over
// geometry whenever every word carries one, so line and paragraph structure
// survives intact with no coordinate invented anywhere.
//
// The honest consequence, stated once so no caller is surprised: a span
// grounded in one of these documents produces **no** [normalise.Region], so a
// review interface can highlight the extracted text and name the page, and
// cannot draw a box over a rendered document. That is a real loss of a real
// feature. It is preferred to a highlight that points at the wrong place with
// full confidence (docs/rules.md §8.5, in spirit: a box is a value, and a
// fabricated box is a fabricated value).
//
// # Pages
//
//   - DOCX is always one page. A Word document's page breaks are computed by
//     the renderer; an explicit w:type="page" break is only some of them, so
//     honouring those would produce numbering that agrees with Word on
//     documents that use only explicit breaks and disagrees on every other
//     one. One page always is a property a caller can rely on; a page count
//     that is right sometimes is not.
//   - XLSX is one page per worksheet, in workbook order. Page N is the Nth
//     sheet the workbook lists.
//   - CSV is one page.
//
// # What is read, and what is not
//
// For DOCX only the body, word/document.xml, is extracted. Headers, footers,
// footnotes, endnotes and comments live in their own parts and are not put
// into the text: a running header belongs to a rendered page boundary that
// this package does not have, so placing it anywhere in a single-page stream
// asserts a position it does not occupy. They are not dropped silently
// either, which would breach docs/rules.md §6.1 — a part that actually holds
// text is named in [Document.Skipped], by a value from this package's own
// closed [Part] vocabulary and never by a zip entry name.
//
// Tracked deletions (w:delText) and field instructions (w:instrText) are not
// text a reader of the document sees, and are excluded. Text boxes, content
// controls, hyperlinks and tracked insertions are text a reader does see, and
// are included. In an mc:AlternateContent, only the first mc:Choice is read,
// or the mc:Fallback when there is no Choice — reading both would emit the
// same content twice.
//
// For XLSX, cell values are read from the shared string table, from inline
// strings and from cached formula results. Number formats are **not** applied:
// a cell storing 44927 with a date format is emitted as 44927, not as a date.
// Applying formats means implementing Excel's format language against
// xl/styles.xml, and a half-implementation produces confidently wrong dates,
// which is worse for an extraction library than a raw number a later stage can
// still interpret. Sheet names are read only to order the sheets and are never
// emitted and never put in an error, because a sheet name is document content.
//
// For CSV the delimiter is a comma and only a comma, matching what
// internal/detect is willing to recognise as a CSV in the first place. A
// leading UTF-8 byte order mark is removed, because U+FEFF is one of the
// characters internal/normalise reports as a zero-width finding and a BOM is
// not an attack.
//
// # Colour, and one attack this reading cannot see
//
// No word carries a colour and no page carries a background, so
// internal/normalise skips the background-colour check for these documents.
// White-on-white text in a DOCX is a real attack and this package does not
// detect it: doing so means resolving the style inheritance chain through
// styles.xml, theme1.xml, table shading and direct formatting, and a partial
// resolution produces false negatives that look like coverage. Reporting no
// colour makes the check abstain, which is honest, rather than pass, which
// would not be.
//
// The related case that is cheap is handled: a run marked hidden (w:vanish) is
// invisible to a person reviewing the document and visible to a model. Its
// text is extracted — dropping it would be silently dropping data — and the
// count is reported in [Document.HiddenRuns] so the pipeline has a typed
// scalar to raise a review reason from. The count is a number, never content.
//
// # Limits
//
// Every limit primitive comes from internal/detect and none is reimplemented
// (docs/adr/0020-resource-limits.md). Two of these three formats are ZIP
// containers, which are the classic decompression bomb, so:
//
//   - Entry counts are checked against a ceiling before the directory is
//     walked, and before anything is opened.
//   - Every entry is read through a detect.LimitedReader charging a
//     detect.Counter that is cumulative across the whole archive. A thousand
//     entries of one mebibyte is the same attack as one entry of a gibibyte,
//     and it fails the same way.
//   - The declared uncompressed size is checked before an entry is opened,
//     because that rejection is free, and it is not believed afterwards: the
//     reader is wrapped whatever the declaration said.
//   - Only Store and Deflate are accepted, and only entries this package
//     needs are ever opened. Parts are located by exact name, so a container
//     full of bombs is a container whose bombs are never decompressed.
//   - A zip nested inside a zip is never followed. Parts are addressed by
//     exact name in the outer archive, so there is no code path that hands
//     entry bytes to a zip reader (docs/rules.md §7.4).
//   - XML nesting spends a detect.Depth budget passed as a parameter.
//     Subtree skipping is iterative, because encoding/xml's own Decoder.Skip
//     recurses once per level and a deep document would exhaust the stack.
//
// # XML entity expansion
//
// The billion-laughs attack is structurally impossible here rather than
// bounded, and the reasons were confirmed against the standard library rather
// than assumed:
//
//   - xml.Decoder.Entity is nil unless a caller sets it, and this package
//     never sets it. With it nil, only the five predefined entities and
//     numeric character references resolve.
//   - Parsing a DOCTYPE internal subset does not populate Entity. A decoder
//     run over a document declaring nine levels of nested entities finishes
//     with an empty Entity map: the standard library reports the whole
//     DOCTYPE as one opaque xml.Directive and never reads the declarations.
//   - Decoder.Strict is true by default and is left true, which makes a
//     reference to any other entity a hard error instead of passthrough text.
//     A recursive entity therefore fails at its first reference, having
//     produced no character data at all.
//   - An external entity is the same case: encoding/xml has no code path that
//     opens a URL, so SYSTEM "file:///etc/passwd" and SYSTEM "http://..."
//     both fail as unresolvable references and nothing is fetched
//     (docs/rules.md §7.4).
//
// Belt and braces, a DOCTYPE declaration is refused outright. No OOXML part
// has a legitimate one.
//
// # Errors
//
// Nothing derived from the document goes in an error. Errors carry the
// operation, the page number and a [Part] from a fixed vocabulary, and never
// a cell value, a sheet name, a paragraph, or a zip entry name — an entry
// name is document content, which is why internal/detect declines to repeat
// archive/zip's error text and why this package declines too
// (docs/rules.md §2.5, §7.5).
//
// # Concurrency
//
// A [Document] is a value and is safe to read from several goroutines once
// [Extract] has returned. Extraction itself is single-goroutine.
//
// This package cannot import the root — the root imports the pipeline, which
// imports this — so it declares its own sentinels and the pipeline classifies
// them onto ovrin's. See docs/architecture.md, "Layout".
package office
