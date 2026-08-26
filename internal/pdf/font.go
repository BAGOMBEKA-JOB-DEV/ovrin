package pdf

import (
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// font is everything the text interpreter needs from a font dictionary: how
// many bytes a character code takes, what characters it stands for, and how
// far it advances the pen.
//
// It is deliberately not a font: nothing here parses a font program, reads a
// glyph outline or knows what the text looks like. The three questions above
// are the only ones a text layer poses, and answering only those is what keeps
// this package a reader rather than a renderer
// (docs/adr/0011-pdf-text-extraction.md).
type font struct {
	base      string
	composite bool

	// enc and encKnown are the simple font's code-to-character table.
	// encKnown is separate because a code that maps to no character and a
	// code that maps to NUL must not be confused: one is undecodable and one
	// is a character.
	enc      [256]rune
	encKnown [256]bool

	// toUnicode is the font's ToUnicode CMap, and is the authority when it
	// answers. It is the only mapping a subset font with a custom encoding
	// has, and a broken one is the reason the usability heuristic exists.
	toUnicode *cmap

	// codes is the encoding CMap of a composite font, which decides how many
	// bytes each character code takes.
	codes *cmap

	widths  map[int]float64
	missing float64

	// scale converts a glyph width to text-space units. It is a thousandth
	// for every font except Type 3, whose FontMatrix says otherwise.
	scale float64

	// builtin is the standard-14 face whose metrics stand in when the font
	// dictionary carries no /Widths.
	builtin string
}

// glyph is one character code as shown, after decoding.
type glyph struct {
	// code is the character code, one or two bytes wide as the font decided.
	code uint32

	// n is how many bytes of the shown string the code occupied, which is
	// what a word-spacing rule needs to know.
	n int

	// text is what the code stands for, and is empty when nothing mapped it.
	text string

	// decoded reports that text came from a ToUnicode entry or a known
	// encoding rather than from a guess. It is counted, and the count is what
	// [Stats] measures the page against (docs/pipeline.md stage 2).
	decoded bool

	// width is the advance in text-space units, before font size.
	width float64
}

// defaultGlyphWidth stands in for a font that declares no width for a code and
// no standard-14 metrics to fall back on. Half an em is the middle of the
// distribution for Latin text, and being wrong about it costs box accuracy
// rather than characters.
const defaultGlyphWidth = 0.5

// loadFont builds a font from a font dictionary.
//
// A dictionary that is missing, malformed or of a subtype this package does
// not know still produces a usable font: single-byte codes read through
// StandardEncoding, which is what a viewer falls back to and what recovers the
// text of a document whose font dictionaries are damaged. What it never does
// is invent a mapping and call it decoded — an unmappable code is counted.
func (d *Doc) loadFont(dict Dict, dp detect.Depth) *font {
	f := &font{scale: 0.001, widths: map[int]float64{}}
	f.enc, _, _ = baseEncoding("StandardEncoding")
	for i := 0; i < 256; i++ {
		f.encKnown[i] = f.enc[i] != 0
	}
	if dict == nil {
		f.builtin = "Helvetica"
		return f
	}
	if n, ok := toName(d.resolve(dict["BaseFont"], dp)); ok {
		f.base = string(n)
	}
	if st, ok := d.resolve(dict["ToUnicode"], dp).(*Stream); ok {
		if data, err := st.Decode(); err == nil {
			f.toUnicode = parseCMap(data, dp)
		} else {
			d.note(err)
		}
	}
	sub, _ := toName(d.resolve(dict["Subtype"], dp))
	if sub == "Type0" {
		d.loadComposite(f, dict, dp)
		return f
	}
	d.loadSimple(f, dict, sub, dp)
	return f
}

// loadSimple fills in a single-byte font: its encoding, its differences and
// its widths.
func (d *Doc) loadSimple(f *font, dict Dict, sub Name, dp detect.Depth) {
	if sub == "Type3" {
		// A Type 3 font's glyph space is whatever its FontMatrix says, so its
		// widths are not thousandths.
		if m, ok := d.resolve(dict["FontMatrix"], dp).(Array); ok && len(m) >= 1 {
			if v, ok := toFloat(d.resolve(m[0], dp)); ok && v != 0 {
				f.scale = v
			}
		}
	}
	switch e := d.resolve(dict["Encoding"], dp).(type) {
	case Name:
		if enc, _, ok := baseEncoding(e); ok {
			f.setBase(enc)
		}
	case Dict:
		if n, ok := toName(d.resolve(e["BaseEncoding"], dp)); ok {
			if enc, _, ok := baseEncoding(n); ok {
				f.setBase(enc)
			}
		}
		f.applyDifferences(d, e, dp)
	}
	first, _ := toInt(d.resolve(dict["FirstChar"], dp))
	if w, ok := d.resolve(dict["Widths"], dp).(Array); ok {
		for i, o := range w {
			code := first + int64(i)
			if code < 0 || code > 255 {
				continue
			}
			if v, ok := toFloat(d.resolve(o, dp)); ok {
				f.widths[int(code)] = v * f.scale
			}
		}
	}
	if fd, ok := d.resolve(dict["FontDescriptor"], dp).(Dict); ok {
		if v, ok := toFloat(d.resolve(fd["MissingWidth"], dp)); ok {
			f.missing = v * f.scale
		}
	}
	if len(f.widths) == 0 {
		f.builtin = f.base
		if f.builtin == "" {
			f.builtin = "Helvetica"
		}
	}
}

// setBase replaces the code-to-character table and its known-code flags.
func (f *font) setBase(enc [256]rune) {
	f.enc = enc
	for i := 0; i < 256; i++ {
		f.encKnown[i] = enc[i] != 0
	}
}

// applyDifferences applies an /Encoding dictionary's /Differences array, which
// is a sequence of a starting code followed by the glyph names that follow it.
func (f *font) applyDifferences(d *Doc, e Dict, dp detect.Depth) {
	diff, ok := d.resolve(e["Differences"], dp).(Array)
	if !ok {
		return
	}
	code := int64(-1)
	for _, o := range diff {
		o = d.resolve(o, dp)
		if v, ok := toInt(o); ok {
			code = v
			continue
		}
		n, ok := toName(o)
		if !ok || code < 0 {
			continue
		}
		if code >= 0 && code < 256 {
			r, known := runeForGlyph(string(n))
			f.enc[code] = r
			// A glyph name this package cannot resolve makes the code
			// undecodable. That is the honest answer and it is what the
			// usability heuristic counts; guessing would be the failure this
			// package exists to avoid.
			f.encKnown[code] = known
		}
		code++
	}
}

// loadComposite fills in a Type 0 font: its encoding CMap, its descendant's
// widths, and the default width its descendant declares.
func (d *Doc) loadComposite(f *font, dict Dict, dp detect.Depth) {
	f.composite = true
	f.missing = 1.0
	switch e := d.resolve(dict["Encoding"], dp).(type) {
	case Name:
		// Identity-H and Identity-V are what an embedded subset font uses and
		// are the overwhelming majority. Every other predefined CMap names a
		// CJK collection whose data is not in this package; its two-byte
		// codespace is honoured so the reader stays in step with the string,
		// and its characters come from ToUnicode or are counted undecodable.
		f.codes = identityCMap()
		_ = e
	case *Stream:
		if data, err := e.Decode(); err == nil {
			f.codes = parseCMap(data, dp)
		} else {
			d.note(err)
			f.codes = identityCMap()
		}
	default:
		f.codes = identityCMap()
	}
	desc, ok := d.resolve(dict["DescendantFonts"], dp).(Array)
	if !ok || len(desc) == 0 {
		return
	}
	cid, ok := d.resolve(desc[0], dp).(Dict)
	if !ok {
		return
	}
	if v, ok := toFloat(d.resolve(cid["DW"], dp)); ok {
		f.missing = v * f.scale
	}
	f.readCIDWidths(d, cid, dp)
}

// readCIDWidths reads a CID font's /W array, whose two forms are a list of
// widths from a starting CID and a single width for a range of CIDs.
func (f *font) readCIDWidths(doc *Doc, cid Dict, dp detect.Depth) {
	w, ok := doc.resolve(cid["W"], dp).(Array)
	if !ok {
		return
	}
	for i := 0; i < len(w); {
		start, ok := toInt(doc.resolve(w[i], dp))
		if !ok || start < 0 || start > 0xFFFF {
			i++
			continue
		}
		i++
		if i >= len(w) {
			return
		}
		if list, ok := doc.resolve(w[i], dp).(Array); ok {
			for j, o := range list {
				c := start + int64(j)
				if c > 0xFFFF {
					break
				}
				if v, ok := toFloat(doc.resolve(o, dp)); ok {
					f.widths[int(c)] = v * f.scale
				}
			}
			i++
			continue
		}
		end, ok := toInt(doc.resolve(w[i], dp))
		i++
		if !ok || i >= len(w) {
			return
		}
		v, ok := toFloat(doc.resolve(w[i], dp))
		i++
		// A range from 0 to 65535 is legal and costs 65536 map entries, which
		// is bounded by the CID space rather than by the document.
		if !ok || end < start || end > 0xFFFF {
			continue
		}
		for c := start; c <= end; c++ {
			f.widths[int(c)] = v * f.scale
		}
	}
}

// decode turns a shown string into glyphs.
//
// The loop is driven by the font's own code length, so a two-byte font is read
// two bytes at a time and a trailing odd byte is dropped rather than read as a
// character. Reading a composite font one byte at a time produces twice as
// many characters as the page has and every one of them is wrong, which is a
// failure that looks like success.
func (f *font) decode(s []byte) []glyph {
	out := make([]glyph, 0, len(s))
	for i := 0; i < len(s); {
		var code uint32
		var n int
		if f.composite && f.codes != nil {
			code, n = f.codes.next(s[i:])
		} else {
			code, n = uint32(s[i]), 1
		}
		if n <= 0 {
			break
		}
		i += n
		g := glyph{code: code, n: n}
		if t, ok := f.toUnicode.text(code); ok {
			g.text, g.decoded = t, true
		} else if !f.composite && code < 256 && f.encKnown[code] {
			g.text, g.decoded = string(f.enc[code]), true
		}
		g.width = f.width(code)
		out = append(out, g)
	}
	return out
}

// width returns the advance of a code, in text-space units before font size.
func (f *font) width(code uint32) float64 {
	key := int(code)
	if f.composite && f.codes != nil {
		if c, ok := f.codes.cid(code); ok {
			key = c
		}
	}
	if w, ok := f.widths[key]; ok {
		return w
	}
	if !f.composite && f.builtin != "" && code < 256 {
		if w, ok := builtinWidth(f.builtin, byte(code)); ok {
			return w * f.scale
		}
	}
	if f.missing != 0 {
		return f.missing
	}
	return defaultGlyphWidth
}
