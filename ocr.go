package ovrin

import "context"

// OCR recovers text and layout from a rasterised page.
//
// Recognition is not a string. Ovrin needs word positions for provenance and
// per-word confidence as a scoring signal, so a seam returning text alone would
// discard the inputs two other subsystems are built on.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type OCR interface {
	// Recognise reads one page.
	Recognise(ctx context.Context, page Page) (*Recognition, error)

	// Name identifies the provider, and appears in [Provenance.Method] so a
	// result records which provider produced each value.
	Name() string
}

// DocumentOCR is an [OCR] provider that also accepts a whole document.
//
// Cloud providers that rasterise server-side implement this, and it is what
// lets a scanned PDF be processed with no local renderer at all — the route
// that makes scanned documents work before render/pdfium exists.
type DocumentOCR interface {
	OCR

	// RecogniseDocument reads every page, returning one Recognition per page
	// in page order.
	RecogniseDocument(ctx context.Context, doc Document) ([]*Recognition, error)
}

// Recognition is what an OCR provider read from one page.
//
// Every implementation normalises to this shape: coordinates in page points
// with the origin top left, confidence on 0..1, and words in reading order
// rather than in whatever order the provider's API returned them.
type Recognition struct {
	// Words are in reading order.
	Words []Word

	// Lines group the words by baseline.
	Lines []Line

	// Confidence is the provider's own, over the whole page, on 0..1.
	Confidence float64

	// Language is the detected language, or empty if the provider does not
	// report one.
	Language string

	// Raw is the provider's own response, for callers willing to type-assert.
	// Providers that detect tables or key-value pairs expose them here; ovrin
	// itself uses words and lines.
	Raw any

	// Usage is what recognising this page consumed.
	//
	// OCR providers bill per page rather than per token, and without a place
	// to report that the cost of a reading cannot reach [Metadata.Usage] or a
	// metric at all: the seam would be the one stage of the pipeline whose
	// spend is invisible. A provider that does not meter a request leaves this
	// zero rather than guessing a page count.
	Usage Usage
}

// Word is one recognised word.
type Word struct {
	Text string

	// Box is in page points, origin top left.
	Box Rect

	// Confidence is on 0..1. A provider that reports no per-word confidence
	// sets the page confidence here and records that it did, rather than
	// fabricating 1.0.
	Confidence float64

	// Line indexes into [Recognition.Lines].
	Line int
}

// Line is a run of words sharing a baseline.
type Line struct {
	Text string
	Box  Rect
	Page int
}
