package pdf

import (
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// colour is a device colour with each channel on 0..1.
//
// It exists for one question — "is this text the same colour as the paper it
// is drawn on" — which is how white-on-white injected instructions are found
// (docs/adr/0017-untrusted-document-content.md mitigation 4,
// docs/threat-model.md T1). Nothing here renders, so no colour management
// happens: an approximate device model answers that question and a correct one
// would not answer it any better.
type colour struct{ r, g, b float64 }

// colourKind is the family of a colour space, which is all this package needs
// to know about one.
//
// A family this package cannot convert is csUnknown, and a csUnknown colour is
// reported as no colour at all rather than as a guess. That matters more than
// coverage: a wrong colour turns the background check into either a detector
// that misses the attack or one that fires on every legitimate document, and
// the second is worse (docs/adr/0017, "Bad").
type colourKind uint8

const (
	csUnknown colourKind = iota
	csGray
	csRGB
	csCMYK
	csIndexed
)

// colourSpace is as much of a PDF colour space as answering "what colour is
// this glyph" needs.
//
// The device families and the ones that are a device family wearing a hat —
// CalRGB, CalGray, ICCBased with a component count — convert. Separation,
// DeviceN and Lab do not: each needs a tint transform or a white point
// evaluated, which is a renderer's work, and a Separation tint of 1 is full
// ink where a DeviceGray value of 1 is white, so guessing one for the other
// would invert the very comparison this is for.
//
// A value of this type is immutable once built, which is what lets one be
// shared by every copy of a graphics state that q made.
type colourSpace struct {
	kind colourKind

	// n is how many components a colour in this space takes.
	n int

	// base, lut and hival are the Indexed case: an index into a lookup table
	// of base-space components.
	base  *colourSpace
	lut   []byte
	hival int
}

// The device spaces, shared rather than allocated: they are immutable and a
// content stream names them thousands of times.
var (
	graySpace    = &colourSpace{kind: csGray, n: 1}
	rgbSpace     = &colourSpace{kind: csRGB, n: 3}
	cmykSpace    = &colourSpace{kind: csCMYK, n: 4}
	unknownSpace = &colourSpace{kind: csUnknown}
)

// maxIndexedLookup bounds an Indexed space's lookup table.
//
// A table can only ever be read at 256 indices of at most four components, so
// anything past that is bytes the document is asking us to hold for nothing
// (docs/adr/0020-resource-limits.md).
const maxIndexedLookup = 256 * 4

// clamp01 pins a component to the range a colour component has. Out-of-range
// components are a malformation, and clamping is what a viewer does.
func clamp01(v float64) float64 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 1
	default:
		return v
	}
}

// grey returns the colour a single DeviceGray component stands for.
func grey(v float64) colour {
	v = clamp01(v)
	return colour{v, v, v}
}

// fromCMYK converts subtractive components to the additive ones the
// comparison works in, by the naive formula every viewer uses in the absence
// of a profile. It is wrong by a few percent against a real conversion, and a
// few percent does not change whether text is the colour of its paper.
func fromCMYK(c, m, y, k float64) colour {
	c, m, y, k = clamp01(c), clamp01(m), clamp01(y), clamp01(k)
	return colour{(1 - c) * (1 - k), (1 - m) * (1 - k), (1 - y) * (1 - k)}
}

// colour converts components in this space, and reports whether it could.
//
// Too few components is a malformed operator and yields no colour rather than
// a colour built from zeros: an operand list the document truncated must not
// become black text on a black page.
func (cs *colourSpace) colour(v []float64) (colour, bool) {
	if cs == nil {
		return colour{}, false
	}
	switch cs.kind {
	case csGray:
		if len(v) < 1 {
			return colour{}, false
		}
		return grey(v[0]), true
	case csRGB:
		if len(v) < 3 {
			return colour{}, false
		}
		return colour{clamp01(v[0]), clamp01(v[1]), clamp01(v[2])}, true
	case csCMYK:
		if len(v) < 4 {
			return colour{}, false
		}
		return fromCMYK(v[0], v[1], v[2], v[3]), true
	case csIndexed:
		return cs.indexed(v)
	}
	return colour{}, false
}

// indexed looks one index up in the palette.
//
// The index is bounds checked against both the declared /HiVal and the table
// actually present, because the two disagree in a document that wants to be
// read past the end of its own palette (docs/threat-model.md T3).
func (cs *colourSpace) indexed(v []float64) (colour, bool) {
	if len(v) < 1 || cs.base == nil || cs.base.n <= 0 {
		return colour{}, false
	}
	i := int(v[0])
	if i < 0 || i > cs.hival {
		return colour{}, false
	}
	n := cs.base.n
	off := i * n
	if off < 0 || off+n > len(cs.lut) {
		return colour{}, false
	}
	comps := make([]float64, n)
	for j := 0; j < n; j++ {
		comps[j] = float64(cs.lut[off+j]) / 255
	}
	return cs.base.colour(comps)
}

// initial returns the colour a space starts at, which is black in every space
// this package converts.
//
// It is spelled as components rather than as a constant because DeviceCMYK's
// black is [0 0 0 1] and every other space's is zeros, and an Indexed space's
// is whatever its palette put at index 0.
func (cs *colourSpace) initial() (colour, bool) {
	if cs == nil {
		return colour{}, false
	}
	switch cs.kind {
	case csGray, csIndexed:
		return cs.colour([]float64{0})
	case csRGB:
		return cs.colour([]float64{0, 0, 0})
	case csCMYK:
		return cs.colour([]float64{0, 0, 0, 1})
	}
	return colour{}, false
}

// colourSpaceFor resolves a cs or CS operand to a space.
//
// dp is spent by nesting because a colour space names another one — an Indexed
// space names its base, a resource name names an entry that may name itself —
// and that is a second recursion over an attacker-controlled graph
// (docs/threat-model.md T2). An unresolvable name yields the unknown space,
// which yields no colour, which skips the check.
func (d *Doc) colourSpaceFor(o Object, res Dict, dp detect.Depth) *colourSpace {
	dp, err := dp.Descend()
	if err != nil {
		return unknownSpace
	}
	switch v := d.resolve(o, dp).(type) {
	case Name:
		if cs, ok := deviceSpace(v); ok {
			return cs
		}
		// Any other name is a key in the resource dictionary's /ColorSpace,
		// which is where a document puts its ICCBased and Indexed spaces.
		named, ok := d.resolve(res["ColorSpace"], dp).(Dict)
		if !ok {
			return unknownSpace
		}
		e, ok := named[v]
		if !ok {
			return unknownSpace
		}
		return d.colourSpaceFor(e, res, dp)
	case Array:
		return d.arraySpace(v, res, dp)
	}
	return unknownSpace
}

// deviceSpace maps the colour space names that stand for themselves, including
// the abbreviations an inline image is allowed to use.
func deviceSpace(n Name) (*colourSpace, bool) {
	switch n {
	case "DeviceGray", "CalGray", "G":
		return graySpace, true
	case "DeviceRGB", "CalRGB", "RGB":
		return rgbSpace, true
	case "DeviceCMYK", "CMYK":
		return cmykSpace, true
	case "Pattern":
		// A pattern's colour is whatever its own content stream paints, which
		// this package does not follow. Unknown is the honest answer.
		return unknownSpace, true
	}
	return unknownSpace, false
}

// arraySpace resolves the array form, which is how every space that carries
// parameters is written.
func (d *Doc) arraySpace(a Array, res Dict, dp detect.Depth) *colourSpace {
	if len(a) == 0 {
		return unknownSpace
	}
	fam, ok := toName(d.resolve(a[0], dp))
	if !ok {
		return unknownSpace
	}
	switch fam {
	case "CalGray", "DeviceGray", "G":
		return graySpace
	case "CalRGB", "DeviceRGB", "RGB":
		return rgbSpace
	case "DeviceCMYK", "CMYK":
		return cmykSpace
	case "ICCBased":
		// The profile is not read. /N is the component count the stream
		// itself declares, and the alternate space it stands for is the
		// device space with that many components — which is what the
		// specification says a reader without colour management shall do.
		if len(a) < 2 {
			return unknownSpace
		}
		st, ok := d.resolve(a[1], dp).(*Stream)
		if !ok {
			return unknownSpace
		}
		n, ok := toInt(d.resolve(st.Dict["N"], dp))
		if !ok {
			return unknownSpace
		}
		switch n {
		case 1:
			return graySpace
		case 3:
			return rgbSpace
		case 4:
			return cmykSpace
		}
		return unknownSpace
	case "Indexed", "I":
		return d.indexedSpace(a, res, dp)
	}
	// Separation, DeviceN, Lab, Pattern with a base: each needs a function
	// evaluated or a white point applied. Unconverted is better than
	// converted wrongly, for the reason on colourKind.
	return unknownSpace
}

// indexedSpace resolves [/Indexed base hival lookup].
func (d *Doc) indexedSpace(a Array, res Dict, dp detect.Depth) *colourSpace {
	if len(a) < 4 {
		return unknownSpace
	}
	base := d.colourSpaceFor(a[1], res, dp)
	// An Indexed space's base may not itself be Indexed, and a base this
	// package cannot convert makes the palette meaningless.
	if base.kind == csUnknown || base.kind == csIndexed || base.n <= 0 {
		return unknownSpace
	}
	hival, ok := toInt(d.resolve(a[2], dp))
	if !ok || hival < 0 || hival > 255 {
		return unknownSpace
	}
	var lut []byte
	switch t := d.resolve(a[3], dp).(type) {
	case String:
		lut = []byte(t)
	case *Stream:
		b, err := t.Decode()
		if err != nil {
			// An unreadable palette is a colour space we cannot answer for,
			// not a failed page.
			d.note(err)
			return unknownSpace
		}
		lut = b
	default:
		return unknownSpace
	}
	if len(lut) > maxIndexedLookup {
		lut = lut[:maxIndexedLookup]
	}
	return &colourSpace{kind: csIndexed, n: 1, base: base, lut: lut, hival: int(hival)}
}
