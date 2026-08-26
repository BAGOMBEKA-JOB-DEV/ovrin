package ovrin

import (
	"image"
	"io"
)

// Kind is a document format.
//
// It is always determined by content. A file named invoice.pdf that is
// actually a JPEG is common enough — mail systems rename things — that
// trusting the name is how a parser gets handed input it was not written for.
type Kind string

const (
	// KindUnknown is the zero value, for a format detection has not resolved.
	KindUnknown Kind = ""

	KindPDF  Kind = "pdf"
	KindPNG  Kind = "png"
	KindJPEG Kind = "jpeg"
	KindTIFF Kind = "tiff"
	KindWebP Kind = "webp"
	KindDOCX Kind = "docx"
	KindXLSX Kind = "xlsx"
	KindCSV  Kind = "csv"
)

// String returns the format name, or "unknown" for the zero value.
func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

// Source is an unread document.
//
// The interface is closed — it has an unexported method — so the only Sources
// are the ones [Reader], [Bytes] and [File] return. An open interface would let
// a caller supply something no pipeline stage knows how to read, turning a
// compile-time error into a runtime one.
type Source interface {
	isSource()
}

// readerSource, bytesSource and fileSource are the three concrete Sources.
// They carry no logic: opening, reading and limit enforcement happen in the
// pipeline, where the limits live.
type readerSource struct{ r io.Reader }
type bytesSource struct{ b []byte }
type fileSource struct{ path string }

func (readerSource) isSource() {}
func (bytesSource) isSource()  {}
func (fileSource) isSource()   {}

// Reader returns a Source reading from r.
//
// This is the primary constructor: a document usually arrives as a stream — an
// upload, a network body — and buffering it before ovrin can check it against
// the source-size limit would defeat the limit.
//
// The reader is consumed once. If it is an io.Closer, ovrin does not close it;
// that is the caller's, since the caller opened it.
func Reader(r io.Reader) Source { return readerSource{r: r} }

// Bytes returns a Source reading from b.
//
// The slice is not copied and must not be modified until [Extract] returns.
func Bytes(b []byte) Source { return bytesSource{b: b} }

// File returns a Source reading the file at path.
//
// Opening is deferred to [Extract], so a missing file surfaces as an extraction
// error alongside every other failure rather than needing a separate check.
func File(path string) Source { return fileSource{path: path} }

// Document is a Source whose format has been identified.
//
// It is what the pipeline works on after detection, and what a [Renderer] and a
// DocumentOCR receive.
type Document struct {
	// Kind is the detected format.
	Kind Kind

	// Pages is the page count. One, for a single image.
	Pages int

	// Bytes is the size of the source.
	Bytes int64
}

// Page is one rasterised page, handed to an [OCR] provider.
type Page struct {
	// Number is 1-based.
	Number int

	// Image is the rasterised page.
	Image image.Image

	// Width and Height are the page size in points, which is what lets a
	// provider return coordinates in points regardless of the DPI it was
	// given.
	Width  float64
	Height float64

	// DPI is the resolution the page was rendered at.
	DPI int
}
