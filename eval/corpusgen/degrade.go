package main

import (
	"image"
	"image/color"
	"math"
	"math/rand"
)

// Degradation turns a clean rendering into a harder one.
//
// The corpus needs difficulty on purpose. A corpus of clean documents reports
// excellent accuracy and predicts nothing about a phone photograph, so these
// synthesise the specific failures scanning and photography actually cause:
// skew, sensor noise, low contrast, uneven lighting, dust and JPEG ringing.
// Each generated document records in its metadata which of these were applied,
// so a difficulty label is a description of a process rather than an opinion.
type Degradation func(src *image.RGBA, rng *rand.Rand) *image.RGBA

// rotate turns the page by deg degrees about its centre, sampling bilinearly
// and filling the corners with paper.
//
// Skew is the single most common scanning defect: a page fed by hand is never
// square, and a few degrees is enough to break a naive row-of-text assumption.
func rotate(deg float64, paper color.RGBA) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		b := src.Bounds()
		w, h := b.Dx(), b.Dy()
		rad := deg * math.Pi / 180
		sin, cos := math.Sin(rad), math.Cos(rad)

		// Grow the canvas so the rotated page is not clipped: a scan that cut
		// off the total would be testing cropping, not skew.
		nw := int(math.Ceil(math.Abs(float64(w)*cos) + math.Abs(float64(h)*sin)))
		nh := int(math.Ceil(math.Abs(float64(w)*sin) + math.Abs(float64(h)*cos)))
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		fill(dst, paper)

		cx, cy := float64(w)/2, float64(h)/2
		ncx, ncy := float64(nw)/2, float64(nh)/2
		for y := 0; y < nh; y++ {
			for x := 0; x < nw; x++ {
				dx, dy := float64(x)-ncx, float64(y)-ncy
				sx := cos*dx + sin*dy + cx
				sy := -sin*dx + cos*dy + cy
				if sx < 0 || sy < 0 || sx >= float64(w-1) || sy >= float64(h-1) {
					continue
				}
				dst.SetRGBA(x, y, sample(src, sx, sy))
			}
		}
		return dst
	}
}

// keystone applies a horizontal perspective, as a camera held at an angle
// does. The top edge is compressed by frac and the bottom is not.
func keystone(frac float64, paper color.RGBA) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		b := src.Bounds()
		w, h := b.Dx(), b.Dy()
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		fill(dst, paper)
		for y := 0; y < h; y++ {
			// Scale runs from 1-frac at the top to 1 at the bottom.
			s := 1 - frac + frac*float64(y)/float64(h-1)
			for x := 0; x < w; x++ {
				sx := (float64(x)-float64(w)/2)/s + float64(w)/2
				if sx < 0 || sx >= float64(w-1) {
					continue
				}
				dst.SetRGBA(x, y, sample(src, sx, float64(y)))
			}
		}
		return dst
	}
}

// sample reads a bilinearly interpolated pixel.
func sample(m *image.RGBA, x, y float64) color.RGBA {
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)
	c00 := m.RGBAAt(x0, y0)
	c10 := m.RGBAAt(x0+1, y0)
	c01 := m.RGBAAt(x0, y0+1)
	c11 := m.RGBAAt(x0+1, y0+1)
	mix := func(a, b, c, d uint8) uint8 {
		top := float64(a)*(1-fx) + float64(b)*fx
		bot := float64(c)*(1-fx) + float64(d)*fx
		return clamp(top*(1-fy) + bot*fy)
	}
	return color.RGBA{
		R: mix(c00.R, c10.R, c01.R, c11.R),
		G: mix(c00.G, c10.G, c01.G, c11.G),
		B: mix(c00.B, c10.B, c01.B, c11.B),
		A: 255,
	}
}

// noise adds zero-mean Gaussian sensor noise with the given standard
// deviation, in levels.
func noise(sigma float64) Degradation {
	return func(src *image.RGBA, rng *rand.Rand) *image.RGBA {
		b := src.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := src.RGBAAt(x, y)
				n := rng.NormFloat64() * sigma
				src.SetRGBA(x, y, color.RGBA{
					R: clamp(float64(c.R) + n),
					G: clamp(float64(c.G) + n),
					B: clamp(float64(c.B) + n),
					A: 255,
				})
			}
		}
		return src
	}
}

// contrast rescales levels about mid-grey and shifts brightness, which is what
// a worn photocopier and a faded thermal receipt both do.
func contrast(factor, brightness float64) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		b := src.Bounds()
		adj := func(v uint8) uint8 {
			return clamp((float64(v)-128)*factor + 128 + brightness)
		}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := src.RGBAAt(x, y)
				src.SetRGBA(x, y, color.RGBA{R: adj(c.R), G: adj(c.G), B: adj(c.B), A: 255})
			}
		}
		return src
	}
}

// blur applies a box blur of the given radius, n times. Three passes of a box
// blur approximate a Gaussian closely enough for a fixture, and cost nothing
// to write.
func blur(radius, passes int) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		for i := 0; i < passes; i++ {
			src = boxBlur(src, radius)
		}
		return src
	}
}

// boxBlur is one separable box-blur pass.
func boxBlur(src *image.RGBA, r int) *image.RGBA {
	b := src.Bounds()
	tmp := image.NewRGBA(b)
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var sr, sg, sb, n float64
			for dx := -r; dx <= r; dx++ {
				px := x + dx
				if px < b.Min.X || px >= b.Max.X {
					continue
				}
				c := src.RGBAAt(px, y)
				sr, sg, sb, n = sr+float64(c.R), sg+float64(c.G), sb+float64(c.B), n+1
			}
			tmp.SetRGBA(x, y, color.RGBA{R: clamp(sr / n), G: clamp(sg / n), B: clamp(sb / n), A: 255})
		}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var sr, sg, sb, n float64
			for dy := -r; dy <= r; dy++ {
				py := y + dy
				if py < b.Min.Y || py >= b.Max.Y {
					continue
				}
				c := tmp.RGBAAt(x, py)
				sr, sg, sb, n = sr+float64(c.R), sg+float64(c.G), sb+float64(c.B), n+1
			}
			dst.SetRGBA(x, y, color.RGBA{R: clamp(sr / n), G: clamp(sg / n), B: clamp(sb / n), A: 255})
		}
	}
	return dst
}

// speckle scatters dark dots, as dust on a platen and toner debris do. Some
// land on a glyph and change what it looks like, which is the point.
func speckle(count int, size int) Degradation {
	return func(src *image.RGBA, rng *rand.Rand) *image.RGBA {
		b := src.Bounds()
		for i := 0; i < count; i++ {
			x := b.Min.X + rng.Intn(b.Dx())
			y := b.Min.Y + rng.Intn(b.Dy())
			v := uint8(rng.Intn(90))
			for dy := 0; dy < size; dy++ {
				for dx := 0; dx < size; dx++ {
					if image.Pt(x+dx, y+dy).In(b) {
						src.SetRGBA(x+dx, y+dy, color.RGBA{R: v, G: v, B: v, A: 255})
					}
				}
			}
		}
		return src
	}
}

// lighting darkens the page along a diagonal, the way a hand-held phone
// shadows one corner of what it is photographing.
func lighting(strength float64) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		b := src.Bounds()
		w, h := float64(b.Dx()), float64(b.Dy())
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				// t is 0 at the bright corner and 1 at the shadowed one.
				t := (float64(x)/w + float64(y)/h) / 2
				k := 1 - strength*t
				c := src.RGBAAt(x, y)
				src.SetRGBA(x, y, color.RGBA{
					R: clamp(float64(c.R) * k),
					G: clamp(float64(c.G) * k),
					B: clamp(float64(c.B) * k),
					A: 255,
				})
			}
		}
		return src
	}
}

// warm tints the page, as tungsten light and a phone's white balance do.
func warm(r, g, b float64) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		bounds := src.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				c := src.RGBAAt(x, y)
				src.SetRGBA(x, y, color.RGBA{
					R: clamp(float64(c.R) * r),
					G: clamp(float64(c.G) * g),
					B: clamp(float64(c.B) * b),
					A: 255,
				})
			}
		}
		return src
	}
}

// downsample shrinks the page by a factor, as photographing a document from
// too far away does. Detail lost here cannot be recovered, which is the point.
func downsample(factor float64) Degradation {
	return func(src *image.RGBA, _ *rand.Rand) *image.RGBA {
		b := src.Bounds()
		nw := int(float64(b.Dx()) / factor)
		nh := int(float64(b.Dy()) / factor)
		if nw < 1 || nh < 1 {
			return src
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			for x := 0; x < nw; x++ {
				sx := float64(x) * factor
				sy := float64(y) * factor
				if sx >= float64(b.Dx()-1) || sy >= float64(b.Dy()-1) {
					dst.SetRGBA(x, y, src.RGBAAt(int(sx), int(sy)))
					continue
				}
				dst.SetRGBA(x, y, sample(src, sx, sy))
			}
		}
		return dst
	}
}

// clamp converts a computed level to a byte, saturating rather than wrapping.
// Wrapping would turn an over-bright pixel black and put a defect in the
// fixture that no camera produces.
func clamp(v float64) uint8 {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	}
	return uint8(math.Round(v))
}

// apply runs a chain of degradations in order.
func apply(src *image.RGBA, rng *rand.Rand, ds ...Degradation) *image.RGBA {
	for _, d := range ds {
		src = d(src, rng)
	}
	return src
}

// greyscale converts a rendering to a single channel, using the Rec. 601
// luma weights that every scanner and every camera applies.
func greyscale(src *image.RGBA) *image.Gray {
	b := src.Bounds()
	dst := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := src.RGBAAt(x, y)
			v := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			dst.SetGray(x, y, color.Gray{Y: clamp(v)})
		}
	}
	return dst
}
