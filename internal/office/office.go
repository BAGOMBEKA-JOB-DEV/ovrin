package office

import (
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// maxWordsPerPage bounds how many runs one page may produce.
//
// The text budget alone does not bound this. Thirty-two mebibytes of text
// arriving as one-byte cells is thirty-two million normalise.Word values, each
// far larger than the byte it carries, so the slice costs orders of magnitude
// more than the budget that authorised it. The ceiling is the same one
// internal/pdf uses, for the same reason.
const maxWordsPerPage = 1 << 18

// Document is everything one office document yielded.
//
// It is a value with no reader inside it: extraction reads the whole document
// before returning, because none of these formats can be read incrementally in
// a way a caller could use, and holding a container open would hold the
// cumulative budget open with it.
type Document struct {
	// Kind is the format that was read. It is what the caller passed to
	// [Extract], echoed back so a Document is self-describing.
	Kind detect.Kind

	// Pages are in document order, numbered from 1. A DOCX and a CSV each
	// yield exactly one; an XLSX yields one per worksheet. A page with no
	// words is still a page, so that "sheet 3 was empty" is representable
	// rather than being the same as "there is no sheet 3".
	Pages []normalise.Page

	// Skipped names the parts that held text and were deliberately not
	// extracted — a DOCX's headers, footers, footnotes, endnotes and
	// comments. It exists so that not extracting them is a reported decision
	// rather than a silent loss (docs/rules.md §6.1), and it is a list of
	// this package's own [Part] constants, never a part's real name, because
	// a part name is document content.
	//
	// A part is named here only when it actually contained text. A Word
	// document that has never had a footnote still ships a footnotes part
	// carrying nothing but separators, and reporting that on every document
	// would make this field noise instead of a signal.
	Skipped []Part

	// HiddenRuns is how many DOCX runs were marked hidden (w:vanish). Their
	// text is extracted — dropping it would be dropping data silently — but
	// hidden text is invisible to the person reviewing the document and
	// visible to the model reading it, which is the shape of an injection.
	// The count is a number and never content, so it is safe to log and safe
	// to raise a review reason from (docs/rules.md §2.5).
	HiddenRuns int
}

// Extract reads a document of the given kind and returns its text.
//
// It dispatches on the kind internal/detect already determined rather than
// sniffing again, because format is decided once, at the door, from content
// (docs/pipeline.md stage 1). A kind this package does not handle is refused
// with [ErrUnsupported] rather than guessed at.
//
// data is not copied and must not be modified while extraction runs.
//
// cum is the document-wide decompression budget, shared with whatever else is
// reading the same document. A nil cum gets one built from lim, so a caller
// with no budget of its own still gets a cumulative ceiling rather than none.
func Extract(data []byte, kind detect.Kind, lim detect.Limits, cum *detect.Counter) (*Document, error) {
	switch kind {
	case detect.KindDOCX:
		return ExtractDOCX(data, lim, cum)
	case detect.KindXLSX:
		return ExtractXLSX(data, lim, cum)
	case detect.KindCSV:
		return ExtractCSV(data, lim)
	default:
		return nil, unsupported("open", PartUnknown, "kind is not an office document this package reads")
	}
}

// recovered converts a panic in a parser into an error.
//
// A panic while reading a hostile document is a bug in this package, and it is
// treated as one; converting it is so that the bug is this package's problem
// rather than the calling service's outage (docs/threat-model.md T3). It is
// installed at every entry point and nowhere else, so the conversion cannot
// hide a panic from a test that provoked it deeper in.
func recovered(doc **Document, err *error) {
	if r := recover(); r != nil {
		*doc, *err = nil, malformed("open", PartContainer, "parser panicked")
	}
}

// pageBuilder accumulates one page's words under the page's ceilings.
//
// Line numbers are assigned by this type rather than taken from the document,
// so they are dense, monotonic and not attacker-chosen. internal/normalise
// groups by them and trusts them over geometry, which is the whole reason this
// package can decline to invent geometry at all; a document that could choose
// them could choose how its own paragraphs are grouped.
type pageBuilder struct {
	number int
	words  []normalise.Word
	text   *detect.Counter
	line   int
}

// newPageBuilder starts page number n, charging text to the shared budget.
func newPageBuilder(n int, text *detect.Counter) *pageBuilder {
	return &pageBuilder{number: n, text: text, line: 1}
}

// addWord appends one run on the current line.
//
// An empty or all-whitespace run is dropped rather than emitted: a word with
// no text has nothing to ground against, no box to highlight and no meaning,
// and emitting one would put an empty segment into the normalised stream.
// Whitespace between runs is internal/normalise's business — with no geometry
// it separates every adjacent pair anyway — so nothing is lost by not carrying
// a run that was only a gap.
func (b *pageBuilder) addWord(s string) error {
	if !hasGraphic(s) {
		return nil
	}
	if len(b.words) >= maxWordsPerPage {
		return &detect.LimitError{Limit: detect.LimitTextBytes, Max: maxWordsPerPage}
	}
	b.words = append(b.words, normalise.Word{
		Text: s,
		// Box is deliberately the zero Rect. See the package comment: these
		// formats have no page geometry, and internal/normalise skips the
		// checks that need geometry rather than running them against a guess.
		Confidence: 1, // a text layer is read, not recognised; there is nothing to be unsure about
		Line:       b.line,
		// Colour is nil: this reading does not resolve the OOXML style chain,
		// so it reports no colour and the background-colour check abstains.
	})
	return nil
}

// endLine moves to the next line, and does nothing when the current line is
// still empty so that a run of blank paragraphs does not consume line numbers
// nothing will ever reference.
func (b *pageBuilder) endLine() {
	if len(b.words) > 0 && b.words[len(b.words)-1].Line == b.line {
		b.line++
	}
}

// page returns the finished page.
//
// Width and Height are left zero, which is how internal/normalise is told
// there is no media box: it skips the off-page check rather than running it
// against a page size this package would have had to invent. Background is
// left nil for the same reason.
func (b *pageBuilder) page() normalise.Page {
	return normalise.Page{Number: b.number, Words: b.words}
}

// hasGraphic reports whether s contains anything other than the ASCII
// whitespace this package inserts or reads as structure.
//
// It is deliberately not unicode.IsSpace over every rune: a non-breaking space
// or an ideographic space is content a document chose, and a zero-width
// character is something internal/normalise must be given the chance to report
// as a finding. Dropping either here would make a run vanish before the
// detector that exists to notice it ever ran.
func hasGraphic(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
		default:
			return true
		}
	}
	return false
}
