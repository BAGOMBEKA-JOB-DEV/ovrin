// Package normalise turns raw positioned page content into one text stream
// while keeping a mapping from every output byte back to where it came from.
//
// The mapping is the point. Whitespace collapsing, ligature expansion,
// hyphenation repair and reading-order reconstruction are each three lines of
// string rewriting; doing them without losing the source position of every
// surviving byte is what makes this stage harder than it looks. Grounding and
// provenance are built on that mapping and cannot be reconstructed afterwards,
// so it is not optional and it is not deferrable
// (docs/adr/0015-provenance.md, docs/pipeline.md stage 3).
//
// # The mapping
//
// [Normalise] returns a [Result] holding the text and an ordered, gap-free,
// non-overlapping list of [Segment] values. Each segment names an output byte
// range, the page and word it came from, the byte range inside that word's
// text, and the word's box on the page. [Result.Locate] clips that list to any
// query range, so every output span answers "which page, which words, which
// region" — which is exactly what [Result.Regions] returns and what a
// Provenance is filled from.
//
// A segment is Verbatim when its output bytes are byte-identical to its source
// bytes. Sub-ranges of a verbatim segment map by offset arithmetic, so the
// mapping stays exact under clipping. Segments produced by a rewrite —
// a ligature expanded, a space substituted, a run of whitespace folded — are
// not verbatim, and clipping widens to the whole source range rather than
// inventing a correspondence that does not exist. Text ovrin inserted itself
// (page markers, the separators between words and lines) carries Word == -1
// and an empty source range; a caller must not attribute it to the document.
//
// # Unicode: what is normalised and what is not
//
// golang.org/x/text is not available: the core module has zero external
// dependencies (docs/rules.md §4.1) and the standard library ships no Unicode
// decomposition data at all. What this package implements is therefore a
// documented subset of NFKC, not NFKC. Anyone relying on full NFKC semantics
// should read this section rather than the stage name.
//
// Compatibility mappings are applied for every code point in these ranges,
// generated from Unicode 16.0.0 and listed in tables.go:
//
//	U+00A0–U+00FF   Latin-1: nbsp, ª º ² ³ ¹ µ ¼ ½ ¾ and the spacing accents
//	U+0100–U+024F   Latin Extended-A/B: Ĳ ĳ Ǆ–ǌ Ǳǲǳŉ ſ and the other digraphs
//	U+02B0–U+02FF   spacing modifier letters (ʰ ʲ ˡ …)
//	U+0340–U+0387   the Greek and combining-mark singletons
//	U+1D2C–U+1D6A   phonetic modifier capitals and small letters
//	U+2000–U+206F   general punctuation: every compatibility space, ‥ … ‼ ⁇ ⁈ ⁉
//	U+2070–U+209F   superscripts and subscripts
//	U+20A8          the rupee sign
//	U+2100–U+214F   letterlike symbols: ™ ℅ № ℓ, and Ω K Å (canonical singletons)
//	U+2150–U+218F   vulgar fractions and Roman numerals
//	U+2460–U+24FF   circled and parenthesised digits and Latin letters
//	U+2A74–U+2A76   the ::= ligatures
//	U+3000          the ideographic space
//	U+FB00–U+FB06   the Latin ligatures — ﬁ becomes fi, ﬄ becomes ffl
//	U+FB13–U+FB17   the Armenian ligatures
//	U+FF01–U+FF60   fullwidth ASCII and brackets
//	U+FFE0–U+FFE6   fullwidth currency and symbols
//	U+1D400–U+1D6A3 mathematical alphanumeric Latin letters, mapped
//	U+1D7CE–U+1D7FF mathematical alphanumeric digits, mapped
//
// The last two are handled arithmetically rather than by table, and the
// twenty-four unassigned code points whose glyphs live in Letterlike Symbols
// are left alone, as NFKC leaves them. They are covered because a payload
// written as 𝐈𝐠𝐧𝐨𝐫𝐞 𝐭𝐡𝐞 𝐬𝐜𝐡𝐞𝐦𝐚 renders as ordinary words and, unnormalised,
// tokenises as something a reviewer searching for "ignore" will never find.
//
// Canonical composition — e followed by U+0301 becoming é — is applied for
// Latin (U+00C0–U+024F), Greek (U+0386–U+03CE), Cyrillic (U+0400–U+045F) and
// Latin Extended Additional (U+1E00–U+1EFF, which is where Vietnamese lives),
// composing one mark at a time. Composition exclusions are respected.
//
// Not implemented, and each of these is a real gap:
//
//   - Canonical ordering. Two or more combining marks in non-canonical order
//     are left in the order they arrived, so a base with both an above-mark
//     and a below-mark written the wrong way round will not compose. Doing
//     this correctly needs the canonical combining class of every code point,
//     which the standard library does not expose.
//   - Hangul. Neither jamo composition nor the compatibility jamo block.
//   - Arabic and Hebrew presentation forms (U+FB1D–U+FDFF, U+FE70–U+FEFF).
//   - Halfwidth katakana (U+FF61–U+FF9F). A voiced halfwidth katakana is two
//     code points that NFKC composes into one, and composing them needs the
//     Hiragana-Katakana composition data this package does not carry.
//     Mapping the base characters and leaving the voice marks behind would be
//     worse than leaving the text alone, so the range is untouched.
//   - CJK compatibility ideographs, squared units (㎡), and CJK compatibility
//     forms.
//   - Everything outside the ranges listed above. A code point this package
//     does not know is passed through unchanged, never replaced by U+FFFD.
//
// Invalid UTF-8 is passed through byte for byte. The document is somebody's
// evidence, and silently rewriting undecodable bytes to U+FFFD would both
// destroy that and mask the replacement-character ratio the acquisition stage
// measures.
//
// # Suspicious content is reported, never removed
//
// Zero-width characters, bidirectional overrides, text positioned outside the
// media box, text drawn in the page background colour and instruction-shaped
// metadata all produce a [Finding]. None of them is stripped, and none of them
// changes the text. Silently sanitising means the operator never learns they
// are under attack, and a filter that is ninety per cent effective is worse
// than a detector that is honest (docs/adr/0017-untrusted-document-content.md,
// mitigation 4).
//
// A [Finding] carries a classification, a page, a span and at most one code
// point. It never carries document text, because a finding becomes a
// ReviewReason and a ReviewReason ends up in systems nobody audited
// (docs/rules.md §7.5).
//
// # Types
//
// [Span] and [Rect] duplicate the layout of ovrin.Span and ovrin.Rect rather
// than importing them, because the root package will import the pipeline that
// imports this one and Go has no cycles. The field names and types are
// identical, so ovrin.Span(s) and ovrin.Rect(r) convert without a copy loop.
//
// Nothing in this package is safe for concurrent mutation, but a [Result] is
// immutable once returned and may be read from any number of goroutines.
package normalise
