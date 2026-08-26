package main

import "image/color"

// Recipe kinds.
const (
	kindPDF  = "pdf"
	kindPNG  = "png"
	kindJPEG = "jpeg"
)

// recipe is how a document is acquired: the file format it is delivered in and
// the chain of degradations between the clean rendering and it.
//
// Named presets rather than per-document parameters, because a difficulty
// label has to mean the same thing across the corpus. If "poor-scan" were a
// different chain in every document, a per-difficulty figure would be an
// average over incomparable things.
type recipe struct {
	kind    string
	quality int // JPEG quality, ignored otherwise
	scale   int // device pixels per font pixel
	paper   color.RGBA
	ink     color.RGBA
	steps   func(paper color.RGBA) []Degradation
}

// Paper and ink colours. Office white for a digital original, a warmer stock
// for anything that has been through a scanner, and a dull grey for a
// photograph taken indoors.
var (
	officeWhite = color.RGBA{R: 253, G: 253, B: 251, A: 255}
	scanCream   = color.RGBA{R: 246, G: 243, B: 234, A: 255}
	thermalGrey = color.RGBA{R: 236, G: 234, B: 228, A: 255}
	photoStock  = color.RGBA{R: 226, G: 222, B: 210, A: 255}
	blackInk    = color.RGBA{R: 22, G: 22, B: 26, A: 255}
	fadedInk    = color.RGBA{R: 96, G: 94, B: 92, A: 255}
)

// noSteps is the empty chain, for a rendering that is delivered as drawn.
func noSteps(color.RGBA) []Degradation { return nil }

// cleanDigital delivers the document as a PDF with a real text layer. This is
// the easiest thing ovrin will ever be given and the figure it produces should
// be read as a ceiling, not as an expectation.
func cleanDigital() recipe {
	return recipe{kind: kindPDF, paper: officeWhite, ink: blackInk, steps: noSteps}
}

// goodScan is an office flatbed: square to within a degree, the slight optical
// softness a real platen has, a little sensor noise, a few specks of dust.
//
// The blur is not only realism. Uncorrelated noise is incompressible, and a
// lossless scan of pure white noise costs the repository half a megabyte
// forever; one blur pass correlates neighbouring pixels, which is both what a
// lens does and what makes the file a quarter of the size.
func goodScan() recipe {
	return recipe{
		kind: kindPNG, scale: 3, paper: scanCream, ink: blackInk,
		steps: func(paper color.RGBA) []Degradation {
			return []Degradation{
				rotate(0.7, paper),
				blur(1, 1),
				noise(1.2),
				speckle(60, 1),
			}
		},
	}
}

// poorScan is the machine in the corridor: fed by hand so it is skewed, low on
// toner so the contrast is gone, and dirty enough that dust lands on glyphs.
// JPEG at a quality nobody would choose deliberately finishes the job.
func poorScan() recipe {
	return recipe{
		kind: kindJPEG, quality: 34, scale: 2, paper: scanCream, ink: fadedInk,
		steps: func(paper color.RGBA) []Degradation {
			return []Degradation{
				rotate(-1.8, paper),
				contrast(0.58, 16),
				blur(1, 1),
				noise(9),
				speckle(420, 2),
			}
		},
	}
}

// fadedThermal is a till receipt that spent a fortnight in a wallet: the print
// has gone grey, the paper has darkened, and the whole thing is slightly out
// of focus.
func fadedThermal() recipe {
	return recipe{
		kind: kindJPEG, quality: 40, scale: 3, paper: thermalGrey, ink: fadedInk,
		steps: func(paper color.RGBA) []Degradation {
			return []Degradation{
				rotate(1.3, paper),
				contrast(0.45, 24),
				blur(1, 2),
				noise(6),
				speckle(120, 2),
			}
		},
	}
}

// photograph is a phone held over a document on a desk: keystoned because the
// phone is not parallel to the page, shadowed on one side, warm because the
// room is lit by a bulb, and downsampled because the photographer stood too
// far back.
func photograph() recipe {
	return recipe{
		kind: kindJPEG, quality: 46, scale: 3, paper: photoStock, ink: blackInk,
		steps: func(paper color.RGBA) []Degradation {
			return []Degradation{
				keystone(0.07, paper),
				rotate(1.4, paper),
				lighting(0.34),
				warm(1.04, 0.99, 0.90),
				downsample(1.5),
				blur(1, 1),
				noise(7),
			}
		},
	}
}
