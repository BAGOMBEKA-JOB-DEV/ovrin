package main

import (
	"image"
	"image/color"
	"strings"
	"unicode"
)

// Layout constants for the bitmap renderer, in font pixels before scaling.
const (
	glyphW  = 5
	glyphH  = 7
	advance = 6 // one glyph plus the inter-character gap
)

// canvas is a page under construction.
type canvas struct {
	img     *image.RGBA
	scale   int // device pixels per font pixel
	leading int // device pixels between baselines, beyond the glyph height
	pad     int
	ink     color.RGBA
}

// newCanvas returns a page sized to hold cols characters and rows lines.
//
// Sizing to content rather than to a fixed page: a fixed width silently
// clipped the receipt number and every amount in examples/receipt/gen.go, and
// a fixture missing the fields it exists to exercise is worse than no fixture.
func newCanvas(cols, rows, scale int, paper, ink color.RGBA) *canvas {
	c := &canvas{scale: scale, leading: 4 * scale, pad: 10 * scale, ink: ink}
	w := c.pad*2 + cols*advance*scale
	h := c.pad*2 + rows*c.lineHeight()
	c.img = image.NewRGBA(image.Rect(0, 0, w, h))
	fill(c.img, paper)
	return c
}

// lineHeight is the vertical distance between baselines.
func (c *canvas) lineHeight() int { return glyphH*c.scale + c.leading }

// fill paints the whole image one colour.
func fill(m *image.RGBA, col color.RGBA) {
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			m.SetRGBA(x, y, col)
		}
	}
}

// text draws s with its first glyph's top-left at the given character cell.
func (c *canvas) text(col, row int, s string) {
	x := c.pad + col*advance*c.scale
	y := c.pad + row*c.lineHeight()
	for _, r := range strings.ToUpper(s) {
		g, ok := glyphs[unicode.ToUpper(r)]
		if !ok {
			g = glyphs[' ']
		}
		for gy := 0; gy < glyphH; gy++ {
			for gx := 0; gx < glyphW; gx++ {
				if g[gy]&(1<<(glyphW-1-gx)) == 0 {
					continue
				}
				for dy := 0; dy < c.scale; dy++ {
					for dx := 0; dx < c.scale; dx++ {
						px, py := x+gx*c.scale+dx, y+gy*c.scale+dy
						if image.Pt(px, py).In(c.img.Bounds()) {
							c.img.SetRGBA(px, py, c.ink)
						}
					}
				}
			}
		}
		x += advance * c.scale
	}
}

// rule draws a horizontal line across the given character columns, used for
// the table rules that separate a document's sections.
func (c *canvas) rule(row, from, to int) {
	y := c.pad + row*c.lineHeight() + glyphH*c.scale/2
	x0 := c.pad + from*advance*c.scale
	x1 := c.pad + to*advance*c.scale
	for x := x0; x < x1; x++ {
		for dy := 0; dy < c.scale; dy++ {
			if image.Pt(x, y+dy).In(c.img.Bounds()) {
				c.img.SetRGBA(x, y+dy, c.ink)
			}
		}
	}
}

// render draws a whole document body and returns the image.
//
// The body is the same slice of lines the PDF writer takes, so a document's
// content is written once and acquired two ways. That is the point: the same
// facts read from a clean digital original and from a photograph of it are
// what make a difficulty breakdown mean anything.
func render(body []string, scale int, paper, ink color.RGBA) *image.RGBA {
	cols := 0
	for _, l := range body {
		l = strings.TrimPrefix(strings.TrimPrefix(l, "@B "), "@H ")
		if n := len([]rune(l)); n > cols {
			cols = n
		}
	}
	c := newCanvas(cols, len(body), scale, paper, ink)
	for i, l := range body {
		switch {
		case strings.HasPrefix(l, "@R"):
			c.rule(i, 0, cols)
		case strings.HasPrefix(l, "@B "), strings.HasPrefix(l, "@H "):
			// The bitmap font has one weight. Emphasis in a scan is carried by
			// the layout, not by a second face, and pretending otherwise would
			// draw a fixture that no printer produces.
			c.text(0, i, l[3:])
		default:
			c.text(0, i, l)
		}
	}
	return c.img
}
