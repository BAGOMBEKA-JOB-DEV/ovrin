package normalise

import "fmt"

// Span is a byte range, half-open: Start is included, End is not.
//
// It mirrors ovrin.Span field for field so that ovrin.Span(s) converts, which
// is why it is bytes rather than runes — Go strings are bytes, []rune costs a
// copy, and every caller would convert back.
type Span struct {
	Start int
	End   int
}

// Len returns the number of bytes the span covers.
func (s Span) Len() int { return s.End - s.Start }

// Empty reports whether the span covers no bytes.
func (s Span) Empty() bool { return s.End <= s.Start }

// Rect is a region of a page, in points, with the origin at the top left.
//
// It mirrors ovrin.Rect field for field so that ovrin.Rect(r) converts. The
// origin is neither PDF's nor an image format's; one convention had to be
// chosen and adapters normalise to it (docs/adr/0015-provenance.md).
type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Zero reports whether the rectangle carries no geometry, which is how a
// reading that gives no positions describes every word it produces.
func (r Rect) Zero() bool {
	return r.MinX == 0 && r.MinY == 0 && r.MaxX == 0 && r.MaxY == 0
}

// Union returns the smallest rectangle containing both.
//
// A zero operand is ignored rather than included, so that a word the reading
// gave no geometry for does not drag the box back to the origin and produce a
// highlight covering the top-left quarter of the page.
func (r Rect) Union(o Rect) Rect {
	if r.Zero() {
		return o
	}
	if o.Zero() {
		return r
	}
	if o.MinX < r.MinX {
		r.MinX = o.MinX
	}
	if o.MinY < r.MinY {
		r.MinY = o.MinY
	}
	if o.MaxX > r.MaxX {
		r.MaxX = o.MaxX
	}
	if o.MaxY > r.MaxY {
		r.MaxY = o.MaxY
	}
	return r
}

func (r Rect) width() float64  { return r.MaxX - r.MinX }
func (r Rect) height() float64 { return r.MaxY - r.MinY }

// Colour is an sRGB colour with each channel on 0..1.
//
// Only ever compared, never rendered, so no colour space conversion happens
// here: the comparison that matters is "is this text the same colour as the
// paper it is drawn on", and that survives an approximate colour model.
type Colour struct {
	R float64
	G float64
	B float64
}

// near reports whether two colours are close enough that text in one would be
// invisible on the other. The threshold is deliberately tight, because a
// legitimate document uses pale grey for watermarks and flagging those would
// spend the operator's attention on nothing.
func (c Colour) near(o Colour) bool {
	const tol = 0.06
	d := func(a, b float64) float64 {
		if a > b {
			return a - b
		}
		return b - a
	}
	return d(c.R, o.R) <= tol && d(c.G, o.G) <= tol && d(c.B, o.B) <= tol
}

// Word is one positioned run of text, exactly as a reading produced it.
//
// A word here is whatever unit the reading emits — an OCR engine's word, a
// PDF text-showing operator's string — and may contain spaces. Normalisation
// does not re-tokenise it, because the box belongs to the whole run and
// splitting the text would leave the fragments with a box that is a guess.
type Word struct {
	// Text is the run, unnormalised. It may be empty, may contain whitespace,
	// and may not be valid UTF-8.
	Text string

	// Box is the run's region on the page, in points with the origin top
	// left. A zero Rect means the reading gave no geometry, which is the
	// normal case for a plain-text source and never true of OCR.
	Box Rect

	// Confidence is the reading's own confidence for this run, on 0..1. It is
	// carried through unchanged so the scorer can average it over the words
	// backing a value; normalisation never interprets it.
	Confidence float64

	// Line is the reading's own line grouping, or -1 when it does not group.
	// When present it is trusted over geometry, because the reading saw the
	// baselines and this package only sees boxes.
	Line int

	// Colour is the fill colour the run was drawn in, or nil when the reading
	// does not report one. OCR never does: an engine that can read the text
	// has already proved it is visible.
	Colour *Colour
}

// Page is one page of raw positioned content.
type Page struct {
	// Number is 1-based, and is what appears in a Provenance.
	Number int

	// Width and Height are the media box size in points. Zero means the
	// reading did not report one, and the off-page check is skipped rather
	// than run against a guess.
	Width  float64
	Height float64

	// Words are in whatever order the reading produced them, which for a
	// two-column page is frequently not reading order.
	Words []Word

	// Background is the page's background colour, or nil when the reading did
	// not report one. Nil skips the background-colour check: an assumed white
	// page would flag every genuine white-on-dark design as an attack.
	Background *Colour
}

// Meta is one document metadata entry — a PDF Info key, an XMP property.
//
// Metadata never enters the normalised stream. It is not visible content, a
// model has no use for it, and carrying it into the prompt would hand an
// injection payload the one thing the design denies it. It is scanned, and
// findings are reported.
//
// Key is echoed in a [Finding], so a reader must put a structural field name
// there and never document text. Keys that do not look like field names are
// dropped from the finding rather than trusted; see [Finding.Key].
type Meta struct {
	Key   string
	Value string
}

// Input is everything one normalisation runs over.
type Input struct {
	// Pages are in document order. A page with no words still produces a page
	// marker, so that "page 4 was blank" is representable.
	Pages []Page

	// Metadata is the document's metadata, scanned but not included in the
	// text.
	Metadata []Meta
}

// Segment maps one run of the normalised text back to the reading that
// produced it. Segments are ordered by Out, do not overlap, and together cover
// every byte of [Result.Text].
type Segment struct {
	// Out is the byte range in [Result.Text].
	Out Span

	// Src is the byte range within Pages[…].Words[Word].Text, and is empty
	// for text ovrin inserted.
	Src Span

	// Page is 1-based, and 0 for text that belongs to no page.
	Page int

	// Word indexes into that page's Words, and is -1 for text ovrin inserted
	// — a page marker, a separator between words, a line break.
	Word int

	// Line is the reading-order line this segment belongs to, counted within
	// the page. It is -1 for inserted text, and is what [Result.Regions]
	// groups by so a word rejoined across two lines yields two boxes rather
	// than one box covering everything between them.
	Line int

	// Box is the source word's region, or nil when the reading gave no
	// geometry. Nil means unknown, never "not on the page".
	Box *Rect

	// Verbatim reports that Out and Src hold the same bytes, so a sub-range
	// of Out maps to Src by offset arithmetic. When false the transformation
	// changed the bytes and a sub-range can only be widened to all of Src.
	Verbatim bool
}

// Inserted reports whether the segment is text ovrin added rather than text
// the document contained. Grounding must not match inside inserted text: a
// page marker containing "2" is not the document saying two.
func (s Segment) Inserted() bool { return s.Word < 0 }

// Region is a page and a box, and is what a review interface highlights.
type Region struct {
	// Page is 1-based.
	Page int

	// Box is the union of the source boxes in this region. It is a value
	// rather than a pointer because a Region is only produced when geometry
	// exists; a span with no geometry produces no regions at all.
	Box Rect
}

// PageSpan locates one page inside the normalised text.
type PageSpan struct {
	// Page is 1-based.
	Page int

	// Marker is the byte range of the inserted page marker. It is never
	// document content, and grounding excludes it.
	Marker Span

	// Body is the byte range of the page's normalised content, marker
	// excluded. It is empty for a blank page.
	Body Span
}

// FindingKind classifies suspicious content.
//
// The set is closed and each member names something an attacker does on
// purpose. A reading that produces something not on this list produces no
// finding, because a detector that fires on the merely unusual trains
// operators to ignore it (docs/adr/0017-untrusted-document-content.md).
type FindingKind string

const (
	// FindingUnknown is the zero value and never appears in a Result.
	FindingUnknown FindingKind = ""

	// FindingZeroWidth is a zero-width or otherwise invisible formatting
	// character inside text. It renders as nothing and tokenises as
	// something, which is how a payload is hidden inside an ordinary word.
	FindingZeroWidth FindingKind = "zero_width"

	// FindingBidiControl is a bidirectional override or embedding. It makes
	// text render in an order different from the order it is stored in, so
	// what a reviewer reads and what a model reads are different documents.
	FindingBidiControl FindingKind = "bidi_control"

	// FindingOffPage is text positioned outside the page media box. It is
	// invisible when the page is displayed or printed, which is the whole
	// attraction.
	FindingOffPage FindingKind = "off_page"

	// FindingBackgroundColour is text drawn in the page's own background
	// colour: white on white, the oldest trick here.
	FindingBackgroundColour FindingKind = "background_colour"

	// FindingInstruction is instruction-shaped language in document
	// metadata — a Title or Keywords field carrying a sentence addressed to a
	// model rather than describing the document.
	FindingInstruction FindingKind = "instruction"
)

// String returns the kind, or "unknown" for the zero value.
func (k FindingKind) String() string {
	if k == FindingUnknown {
		return "unknown"
	}
	return string(k)
}

// Finding is one piece of suspicious content, reported and never removed.
//
// It carries no document text. A finding becomes a ReviewReason, a
// ReviewReason is logged, and document content does not go in logs
// (docs/rules.md §7.5). What it carries instead is enough to find the thing:
// a classification, a page, a span, and for a character finding the code point
// itself — which is a property of the attack, not of the applicant.
type Finding struct {
	// Kind is what was found.
	Kind FindingKind

	// Page is 1-based, and 0 for a metadata finding, which belongs to the
	// document rather than to a page.
	Page int

	// Out is the range in [Result.Text] the finding covers, and is empty for
	// a metadata finding because metadata is not in the text.
	Out Span

	// Box is the region on the page, or nil when the reading gave no
	// geometry.
	Box *Rect

	// Rune is the offending code point for a character finding, and 0
	// otherwise.
	Rune rune

	// Key is the metadata key for [FindingInstruction], and is empty for
	// every other kind. It is blanked unless it looks like a structural field
	// name — letters, digits, and the punctuation a namespaced property uses,
	// at most 40 bytes — because a hostile document chooses its own keys and
	// this value is going to be printed.
	Key string

	// Count is how many characters, words or phrases tripped the check. One
	// zero-width character is a typo; four hundred is a payload.
	Count int
}

// Why returns a one-line cause suitable for a ReviewReason.
//
// It contains no document text, and it is a fixed phrase per kind rather than
// a formatted sentence, so nothing branches on it and nothing leaks through
// it (docs/rules.md §2.2, §7.5).
func (f Finding) Why() string {
	switch f.Kind {
	case FindingZeroWidth:
		return fmt.Sprintf("zero-width character U+%04X in page %d text, %d occurrence(s)", f.Rune, f.Page, f.Count)
	case FindingBidiControl:
		return fmt.Sprintf("bidirectional override U+%04X in page %d text, %d occurrence(s)", f.Rune, f.Page, f.Count)
	case FindingOffPage:
		return fmt.Sprintf("text positioned outside the media box of page %d, %d run(s)", f.Page, f.Count)
	case FindingBackgroundColour:
		return fmt.Sprintf("text in the background colour of page %d, %d run(s)", f.Page, f.Count)
	case FindingInstruction:
		if f.Key == "" {
			return "instruction-shaped language in document metadata"
		}
		return "instruction-shaped language in document metadata field " + f.Key
	default:
		return "suspicious content"
	}
}
