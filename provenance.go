package ovrin

// Reading is how a value was actually read. It describes the past, and appears
// on [Provenance] and [Candidate].
//
// A value is read by exactly one reading. Requesting more than one is a
// different type, [ReadingMode], so that a Provenance claiming two readings at
// once is not representable. See
// docs/adr/0028-reading-and-readingmode.md.
type Reading string

const (
	// ReadingUnknown is the zero value, for a value whose origin was not
	// recorded. It is never a claim that the origin does not exist.
	ReadingUnknown Reading = ""

	// ReadingText is a PDF's own text layer: exact, and nearly free.
	ReadingText Reading = "text"

	// ReadingOCR is optical character recognition of a rasterised page.
	ReadingOCR Reading = "ocr"

	// ReadingVision is a multimodal model reading a page image.
	ReadingVision Reading = "vision"
)

// String returns the reading name, or "unknown" for the zero value.
func (r Reading) String() string {
	if r == ReadingUnknown {
		return "unknown"
	}
	return string(r)
}

// ReadingMode selects how a document should be read. It describes an intention,
// and is the argument to [WithReading].
//
// Distinct from [Reading] because ModeBoth has no meaning as a record of what
// happened: two readings produce two [Candidate] values, not one Provenance
// claiming both.
type ReadingMode string

const (
	// ReadingAuto tries the text layer, then OCR, then vision, using the first
	// that can serve the page. It is the default, and it is what most callers
	// want: cost scales with document difficulty rather than document count.
	ReadingAuto ReadingMode = "auto"

	// ModeText uses the text layer only, and fails rather than falling back.
	ModeText ReadingMode = "text"

	// ModeOCR rasterises and recognises, even where a text layer exists.
	ModeOCR ReadingMode = "ocr"

	// ModeVision sends page images to the model.
	ModeVision ReadingMode = "vision"

	// ModeBoth runs two independent readings and compares them field by field.
	// Roughly doubles cost and latency, which is why it is not the default;
	// disagreement between readings is the strongest signal ovrin has that a
	// value should not be trusted.
	ModeBoth ReadingMode = "both"
)

// Rect is a region of a page, in points, with the origin at the top left.
//
// That origin is neither PDF's (bottom left) nor an image format's (pixels,
// top left). One convention had to be chosen and adapters normalise to it, so
// the confidence engine and any review interface are written against one shape.
type Rect struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// Span is a byte range into the normalised text.
//
// Bytes rather than runes: Go strings are bytes, converting to []rune costs a
// copy, and every caller would convert back.
type Span struct {
	Start int
	End   int
}

// Provenance records where a value came from.
//
// It is what makes human review practical — an interface can highlight the
// region instead of making a reviewer search a 40-page scan — and it is what
// grounding is built on. It cannot be reconstructed after extraction, which is
// why it is always collected rather than being an option.
//
// A nil Box or Span means the position is not known, never that the value is
// not in the document. Some values are legitimately not groundable: a total
// computed from line items, a date normalised from prose.
type Provenance struct {
	// Reading is which reading produced the value.
	Reading Reading

	// Page is 1-based.
	Page int

	// Box is the region on the page, or nil if the reading gave no geometry.
	// Always present for OCR, usually for the text layer, rarely for vision.
	Box *Rect

	// Span is the range in the normalised text, or nil if unknown.
	Span *Span

	// Method names the reading and the provider that served it, as
	// "text-layer", "ocr:tesseract" or "vision:gpt-5.2".
	Method string

	// Exact reports whether the value appears verbatim in the source, rather
	// than having been reformatted or derived.
	Exact bool
}
