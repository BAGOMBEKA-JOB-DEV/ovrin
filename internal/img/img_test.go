package img

import (
	"bytes"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// pngOf encodes a w×h PNG, for tests that need real bytes rather than a
// hand-built header.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	m.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return buf.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, nil); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return buf.Bytes()
}

func TestDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      func(*testing.T) []byte
		kind      Kind
		maxPixels int
		want      error
	}{
		{"a small png", func(t *testing.T) []byte { return pngOf(t, 8, 4) }, KindPNG, 1000, nil},
		{"a small jpeg", func(t *testing.T) []byte { return jpegOf(t, 8, 4) }, KindJPEG, 1000, nil},
		{"an unlimited decode", func(t *testing.T) []byte { return pngOf(t, 8, 4) }, KindPNG, 0, nil},
		{"a png over the pixel ceiling", func(t *testing.T) []byte { return pngOf(t, 40, 40) }, KindPNG, 100, ErrLimitExceeded},
		{"a png exactly on the ceiling", func(t *testing.T) []byte { return pngOf(t, 10, 10) }, KindPNG, 100, nil},
		{"tiff, which we do not decode", func(t *testing.T) []byte { return []byte("II*\x00") }, KindTIFF, 1000, ErrUnsupportedFormat},
		{"webp, which we do not decode", func(t *testing.T) []byte { return []byte("RIFF") }, KindWebP, 1000, ErrUnsupportedFormat},
		{"an unknown kind", func(t *testing.T) []byte { return []byte("xx") }, KindUnknown, 1000, ErrUnsupportedFormat},
		{"a truncated png header", func(t *testing.T) []byte { return pngOf(t, 8, 4)[:12] }, KindPNG, 1000, ErrDecode},
		{"empty input", func(t *testing.T) []byte { return nil }, KindPNG, 1000, ErrDecode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := Decode(tc.data(t), tc.kind, tc.maxPixels)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("Decode = %v, want %v", err, tc.want)
				}
				if p != nil {
					t.Error("a page was returned alongside an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if p.Number != 1 {
				t.Errorf("Number = %d, want 1", p.Number)
			}
			if p.Image == nil {
				t.Error("Image is nil")
			}
			if p.Width <= 0 || p.Height <= 0 {
				t.Errorf("size = %v×%v points, want both positive", p.Width, p.Height)
			}
		})
	}
}

// The limit exists so that a file declaring an implausible size costs the bytes
// already read and nothing more. A decoder that allocated first and checked
// afterwards would pass every other test in this file.
func TestPixelLimitIsCheckedBeforeAllocating(t *testing.T) {
	t.Parallel()

	// 20,000 × 20,000 is 400M pixels — 1.6 GiB decoded. The header is a few
	// dozen bytes, and this test completing quickly is the assertion.
	header := pngOf(t, 1, 1)
	big := forgeDimensions(t, header, 20000, 20000)

	if _, err := Decode(big, KindPNG, DefaultMaxPagePixelsForTest); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Decode = %v, want ErrLimitExceeded", err)
	}
}

// DefaultMaxPagePixelsForTest mirrors the root's DefaultMaxPagePixels. The root
// asserts the real constants agree; this is only a plausible ceiling to test
// against.
const DefaultMaxPagePixelsForTest = 50_000_000

// forgeDimensions rewrites a PNG's IHDR width and height and repairs the
// chunk CRC, so the header declares a size the file does not contain.
//
// The genuine attack needs no forgery — a 20,000 × 20,000 blank PNG encodes to
// a few kilobytes and decodes to 1.6 GiB — but producing one here would
// allocate exactly the memory this test exists to prove we never allocate. A
// forged header is the same input from the decoder's point of view and costs
// nothing to build.
func forgeDimensions(t *testing.T, data []byte, w, h uint32) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	// 8-byte signature, then the IHDR chunk: 4-byte length, 4-byte type,
	// 13-byte data (width, height, and six more), 4-byte CRC over type+data.
	const typeOff = 8 + 4
	const dataOff = typeOff + 4
	const dataLen = 13
	if len(out) < dataOff+dataLen+4 {
		t.Fatalf("fixture is too short to carry an IHDR")
	}
	put := func(at int, v uint32) {
		out[at] = byte(v >> 24)
		out[at+1] = byte(v >> 16)
		out[at+2] = byte(v >> 8)
		out[at+3] = byte(v)
	}
	put(dataOff, w)
	put(dataOff+4, h)
	// PNG's CRC covers the chunk type and its data, not the length.
	put(dataOff+dataLen, crc32.ChecksumIEEE(out[typeOff:dataOff+dataLen]))
	return out
}

// FuzzDecode asserts that no input panics and that a page is never returned
// alongside an error. Image decoders are a classic source of both.
func FuzzDecode(f *testing.F) {
	f.Add(pngOf(&testing.T{}, 2, 2), "png")
	f.Add([]byte("\x89PNG\r\n\x1a\n"), "png")
	f.Add([]byte{0xFF, 0xD8, 0xFF}, "jpeg")
	f.Add([]byte(""), "png")

	f.Fuzz(func(t *testing.T, data []byte, kind string) {
		p, err := Decode(data, Kind(kind), 1_000_000)
		if err != nil && p != nil {
			t.Fatalf("both a page and an error were returned")
		}
		if err == nil && p == nil {
			t.Fatalf("neither a page nor an error was returned")
		}
	})
}
