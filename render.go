package ovrin

import (
	"context"
	"image"
)

// Renderer rasterises a document page to an image, so it can be given to an
// [OCR] provider or a vision model.
//
// There is no default implementation, and that is the hardest constraint in the
// project: rasterising PDF means implementing a large part of the PDF imaging
// model, and nobody has done it well in pure Go. The recommended
// implementation runs PDFium as WebAssembly, which needs no cgo and
// cross-compiles, at the cost of speed. A renderer using cgo must say so on the
// first line of its package documentation.
//
// Most extractions never need one. A PDF with a text layer needs no
// rasterising, an image is already an image, and a [DocumentOCR] provider
// rasterises server-side. See docs/adr/0010-no-cgo-in-core.md.
//
// Implementations must be safe for concurrent use by multiple goroutines.
type Renderer interface {
	Render(ctx context.Context, doc Document, page, dpi int) (image.Image, error)
}
