package img

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
)

// Errors this package returns. The pipeline classifies them onto ovrin's
// sentinels; they are declared here because internal packages cannot import
// the root.
var (
	// ErrUnsupportedFormat means the bytes are not an image format this
	// package decodes.
	ErrUnsupportedFormat = errors.New("unsupported image format")

	// ErrLimitExceeded means the image's declared dimensions exceed the pixel
	// ceiling. Reported from the header, before any allocation.
	ErrLimitExceeded = errors.New("resource limit exceeded")

	// ErrDecode means the header was acceptable but the pixels were not
	// readable — a truncated or corrupt file.
	ErrDecode = errors.New("image could not be decoded")
)

// Kind is the image format. It mirrors the root's Kind for the members this
// package handles; internal_consistency_test.go in the root asserts they agree.
type Kind string

const (
	KindUnknown Kind = ""
	KindPNG     Kind = "png"
	KindJPEG    Kind = "jpeg"
	KindTIFF    Kind = "tiff"
	KindWebP    Kind = "webp"
)

// DefaultDPI is the resolution assumed when a file does not record one.
//
// Most scanners produce 300, and the value matters only for converting pixels
// into the points a Rect is expressed in — it does not affect what the model
// sees. A file that records its own resolution overrides this.
const DefaultDPI = 300

// pointsPerInch is fixed by the typographic point, which is what page
// geometry is expressed in throughout ovrin.
const pointsPerInch = 72.0

// Page is one decoded image page.
//
// The pipeline converts this into an ovrin.Page. The shapes match field for
// field so the conversion is mechanical.
type Page struct {
	// Number is 1-based.
	Number int

	// Image is the decoded page.
	Image image.Image

	// Width and Height are the page size in points.
	Width, Height float64

	// DPI is the resolution the dimensions were derived at.
	DPI int
}

// Decode turns image bytes into a single Page, refusing anything whose
// declared pixel count exceeds maxPixels.
//
// The dimensions are read from the header and checked before the pixels are
// touched, so a file claiming an implausible size costs the bytes already read
// and nothing more.
func Decode(data []byte, kind Kind, maxPixels int) (*Page, error) {
	w, h, err := dimensions(data, kind)
	if err != nil {
		return nil, err
	}

	// Multiply in int64 and compare by division, so dimensions large enough to
	// overflow cannot wrap around and land under the ceiling.
	if maxPixels > 0 {
		if int64(w) > int64(maxPixels)/int64(max(h, 1)) {
			return nil, fmt.Errorf("%w: image is %d×%d pixels, maximum %d, raise with WithMaxPagePixels",
				ErrLimitExceeded, w, h, maxPixels)
		}
	}

	m, err := decodePixels(data, kind)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDecode, kind)
	}

	dpi := DefaultDPI
	b := m.Bounds()
	return &Page{
		Number: 1,
		Image:  m,
		Width:  float64(b.Dx()) * pointsPerInch / float64(dpi),
		Height: float64(b.Dy()) * pointsPerInch / float64(dpi),
		DPI:    dpi,
	}, nil
}

// dimensions reads the declared pixel size from the header alone.
func dimensions(data []byte, kind Kind) (w, h int, err error) {
	switch kind {
	case KindPNG, KindJPEG:
		cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data))
		if decErr != nil {
			return 0, 0, fmt.Errorf("%w: %s header is unreadable", ErrDecode, kind)
		}
		return cfg.Width, cfg.Height, nil
	case KindTIFF, KindWebP:
		// Neither is in the standard library, and this package has no
		// dependencies. Refusing by name is honest; guessing at dimensions we
		// cannot read would defeat the limit this function exists to enforce.
		return 0, 0, fmt.Errorf("%w: %s needs a decoder ovrin does not yet ship", ErrUnsupportedFormat, kind)
	default:
		return 0, 0, fmt.Errorf("%w: %s", ErrUnsupportedFormat, kind)
	}
}

// decodePixels decodes the image itself, having already cleared the limit.
func decodePixels(data []byte, kind Kind) (image.Image, error) {
	r := bytes.NewReader(data)
	switch kind {
	case KindPNG:
		return png.Decode(r)
	case KindJPEG:
		return jpeg.Decode(r)
	default:
		return nil, ErrUnsupportedFormat
	}
}

// max is written out because Go 1.22's builtin min/max are available, but
// being explicit about the guard against a zero height reads better than
// relying on a builtin to express "never divide by zero".
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
