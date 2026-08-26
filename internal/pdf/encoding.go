package pdf

import (
	"strconv"
	"strings"
)

// A simple font maps a byte to a glyph name and a glyph name to a character.
// That indirection is why these tables exist: /Differences speaks in glyph
// names, so a table of code-to-character alone cannot answer a document that
// re-encodes one code.
//
// The tables are the four base encodings of the PDF specification's Annex D,
// minus the expert sets, which carry no text a document is read for. What is
// deliberately not here is the full Adobe Glyph List: four thousand names,
// almost all of them for scripts and ornaments, against a long tail this
// package answers with uniXXXX parsing and an honest undecodable count. A
// character this package cannot name is counted as undecodable rather than
// guessed, and a page with too many of them is refused as a text layer
// (docs/pipeline.md stage 2).

// asciiGlyphs names codes 32..126, which every Latin base encoding agrees on
// except for the two apostrophes StandardEncoding moves.
var asciiGlyphs = [95]string{
	"space", "exclam", "quotedbl", "numbersign", "dollar", "percent", "ampersand", "quotesingle",
	"parenleft", "parenright", "asterisk", "plus", "comma", "hyphen", "period", "slash",
	"zero", "one", "two", "three", "four", "five", "six", "seven",
	"eight", "nine", "colon", "semicolon", "less", "equal", "greater", "question",
	"at", "A", "B", "C", "D", "E", "F", "G",
	"H", "I", "J", "K", "L", "M", "N", "O",
	"P", "Q", "R", "S", "T", "U", "V", "W",
	"X", "Y", "Z", "bracketleft", "backslash", "bracketright", "asciicircum", "underscore",
	"grave", "a", "b", "c", "d", "e", "f", "g",
	"h", "i", "j", "k", "l", "m", "n", "o",
	"p", "q", "r", "s", "t", "u", "v", "w",
	"x", "y", "z", "braceleft", "bar", "braceright", "asciitilde",
}

// latin1Glyphs names codes 161..255, whose characters are the code points
// themselves. WinAnsiEncoding is Latin-1 over this range, which is why it
// needs no table of its own for it.
var latin1Glyphs = [95]string{
	"exclamdown", "cent", "sterling", "currency", "yen", "brokenbar", "section", "dieresis",
	"copyright", "ordfeminine", "guillemotleft", "logicalnot", "hyphen", "registered", "macron", "degree",
	"plusminus", "twosuperior", "threesuperior", "acute", "mu", "paragraph", "periodcentered", "cedilla",
	"onesuperior", "ordmasculine", "guillemotright", "onequarter", "onehalf", "threequarters", "questiondown", "Agrave",
	"Aacute", "Acircumflex", "Atilde", "Adieresis", "Aring", "AE", "Ccedilla", "Egrave",
	"Eacute", "Ecircumflex", "Edieresis", "Igrave", "Iacute", "Icircumflex", "Idieresis", "Eth",
	"Ntilde", "Ograve", "Oacute", "Ocircumflex", "Otilde", "Odieresis", "multiply", "Oslash",
	"Ugrave", "Uacute", "Ucircumflex", "Udieresis", "Yacute", "Thorn", "germandbls", "agrave",
	"aacute", "acircumflex", "atilde", "adieresis", "aring", "ae", "ccedilla", "egrave",
	"eacute", "ecircumflex", "edieresis", "igrave", "iacute", "icircumflex", "idieresis", "eth",
	"ntilde", "ograve", "oacute", "ocircumflex", "otilde", "odieresis", "divide", "oslash",
	"ugrave", "uacute", "ucircumflex", "udieresis", "yacute", "thorn", "ydieresis",
}

// codedGlyph is one entry of a base encoding's non-Latin-1 range.
type codedGlyph struct {
	code byte
	name string
	r    rune
}

// winAnsiHigh is WinAnsiEncoding's 128..159 range, which is where Windows put
// the typographic punctuation that Latin-1 does not have. Codes it leaves
// undefined are absent, and a document using one gets an undecodable count
// rather than a plausible wrong character.
var winAnsiHigh = []codedGlyph{
	{128, "Euro", 0x20AC}, {130, "quotesinglbase", 0x201A}, {131, "florin", 0x0192},
	{132, "quotedblbase", 0x201E}, {133, "ellipsis", 0x2026}, {134, "dagger", 0x2020},
	{135, "daggerdbl", 0x2021}, {136, "circumflex", 0x02C6}, {137, "perthousand", 0x2030},
	{138, "Scaron", 0x0160}, {139, "guilsinglleft", 0x2039}, {140, "OE", 0x0152},
	{142, "Zcaron", 0x017D}, {145, "quoteleft", 0x2018}, {146, "quoteright", 0x2019},
	{147, "quotedblleft", 0x201C}, {148, "quotedblright", 0x201D}, {149, "bullet", 0x2022},
	{150, "endash", 0x2013}, {151, "emdash", 0x2014}, {152, "tilde", 0x02DC},
	{153, "trademark", 0x2122}, {154, "scaron", 0x0161}, {155, "guilsinglright", 0x203A},
	{156, "oe", 0x0153}, {158, "zcaron", 0x017E}, {159, "Ydieresis", 0x0178},
	{160, "space", 0x00A0},
}

// standardHigh is StandardEncoding's 161..251 range.
var standardHigh = []codedGlyph{
	{161, "exclamdown", 0x00A1}, {162, "cent", 0x00A2}, {163, "sterling", 0x00A3},
	{164, "fraction", 0x2044}, {165, "yen", 0x00A5}, {166, "florin", 0x0192},
	{167, "section", 0x00A7}, {168, "currency", 0x00A4}, {169, "quotesingle", 0x0027},
	{170, "quotedblleft", 0x201C}, {171, "guillemotleft", 0x00AB}, {172, "guilsinglleft", 0x2039},
	{173, "guilsinglright", 0x203A}, {174, "fi", 0xFB01}, {175, "fl", 0xFB02},
	{177, "endash", 0x2013}, {178, "dagger", 0x2020}, {179, "daggerdbl", 0x2021},
	{180, "periodcentered", 0x00B7}, {182, "paragraph", 0x00B6}, {183, "bullet", 0x2022},
	{184, "quotesinglbase", 0x201A}, {185, "quotedblbase", 0x201E}, {186, "quotedblright", 0x201D},
	{187, "guillemotright", 0x00BB}, {188, "ellipsis", 0x2026}, {189, "perthousand", 0x2030},
	{191, "questiondown", 0x00BF}, {193, "grave", 0x0060}, {194, "acute", 0x00B4},
	{195, "circumflex", 0x02C6}, {196, "tilde", 0x02DC}, {197, "macron", 0x00AF},
	{198, "breve", 0x02D8}, {199, "dotaccent", 0x02D9}, {200, "dieresis", 0x00A8},
	{202, "ring", 0x02DA}, {203, "cedilla", 0x00B8}, {205, "hungarumlaut", 0x02DD},
	{206, "ogonek", 0x02DB}, {207, "caron", 0x02C7}, {208, "emdash", 0x2014},
	{225, "AE", 0x00C6}, {227, "ordfeminine", 0x00AA}, {232, "Lslash", 0x0141},
	{233, "Oslash", 0x00D8}, {234, "OE", 0x0152}, {235, "ordmasculine", 0x00BA},
	{241, "ae", 0x00E6}, {245, "dotlessi", 0x0131}, {248, "lslash", 0x0142},
	{249, "oslash", 0x00F8}, {250, "oe", 0x0153}, {251, "germandbls", 0x00DF},
}

// macRomanHigh is MacRomanEncoding's 128..255 range.
var macRomanHigh = []codedGlyph{
	{128, "Adieresis", 0x00C4}, {129, "Aring", 0x00C5}, {130, "Ccedilla", 0x00C7},
	{131, "Eacute", 0x00C9}, {132, "Ntilde", 0x00D1}, {133, "Odieresis", 0x00D6},
	{134, "Udieresis", 0x00DC}, {135, "aacute", 0x00E1}, {136, "agrave", 0x00E0},
	{137, "acircumflex", 0x00E2}, {138, "adieresis", 0x00E4}, {139, "atilde", 0x00E3},
	{140, "aring", 0x00E5}, {141, "ccedilla", 0x00E7}, {142, "eacute", 0x00E9},
	{143, "egrave", 0x00E8}, {144, "ecircumflex", 0x00EA}, {145, "edieresis", 0x00EB},
	{146, "iacute", 0x00ED}, {147, "igrave", 0x00EC}, {148, "icircumflex", 0x00EE},
	{149, "idieresis", 0x00EF}, {150, "ntilde", 0x00F1}, {151, "oacute", 0x00F3},
	{152, "ograve", 0x00F2}, {153, "ocircumflex", 0x00F4}, {154, "odieresis", 0x00F6},
	{155, "otilde", 0x00F5}, {156, "uacute", 0x00FA}, {157, "ugrave", 0x00F9},
	{158, "ucircumflex", 0x00FB}, {159, "udieresis", 0x00FC}, {160, "dagger", 0x2020},
	{161, "degree", 0x00B0}, {162, "cent", 0x00A2}, {163, "sterling", 0x00A3},
	{164, "section", 0x00A7}, {165, "bullet", 0x2022}, {166, "paragraph", 0x00B6},
	{167, "germandbls", 0x00DF}, {168, "registered", 0x00AE}, {169, "copyright", 0x00A9},
	{170, "trademark", 0x2122}, {171, "acute", 0x00B4}, {172, "dieresis", 0x00A8},
	{173, "notequal", 0x2260}, {174, "AE", 0x00C6}, {175, "Oslash", 0x00D8},
	{176, "infinity", 0x221E}, {177, "plusminus", 0x00B1}, {178, "lessequal", 0x2264},
	{179, "greaterequal", 0x2265}, {180, "yen", 0x00A5}, {181, "mu", 0x00B5},
	{182, "partialdiff", 0x2202}, {183, "summation", 0x2211}, {184, "product", 0x220F},
	{185, "pi", 0x03C0}, {186, "integral", 0x222B}, {187, "ordfeminine", 0x00AA},
	{188, "ordmasculine", 0x00BA}, {189, "Omega", 0x03A9}, {190, "ae", 0x00E6},
	{191, "oslash", 0x00F8}, {192, "questiondown", 0x00BF}, {193, "exclamdown", 0x00A1},
	{194, "logicalnot", 0x00AC}, {195, "radical", 0x221A}, {196, "florin", 0x0192},
	{197, "approxequal", 0x2248}, {198, "Delta", 0x2206}, {199, "guillemotleft", 0x00AB},
	{200, "guillemotright", 0x00BB}, {201, "ellipsis", 0x2026}, {202, "space", 0x00A0},
	{203, "Agrave", 0x00C0}, {204, "Atilde", 0x00C3}, {205, "Otilde", 0x00D5},
	{206, "OE", 0x0152}, {207, "oe", 0x0153}, {208, "endash", 0x2013},
	{209, "emdash", 0x2014}, {210, "quotedblleft", 0x201C}, {211, "quotedblright", 0x201D},
	{212, "quoteleft", 0x2018}, {213, "quoteright", 0x2019}, {214, "divide", 0x00F7},
	{215, "lozenge", 0x25CA}, {216, "ydieresis", 0x00FF}, {217, "Ydieresis", 0x0178},
	{218, "fraction", 0x2044}, {219, "currency", 0x00A4}, {220, "guilsinglleft", 0x2039},
	{221, "guilsinglright", 0x203A}, {222, "fi", 0xFB01}, {223, "fl", 0xFB02},
	{224, "daggerdbl", 0x2021}, {225, "periodcentered", 0x00B7}, {226, "quotesinglbase", 0x201A},
	{227, "quotedblbase", 0x201E}, {228, "perthousand", 0x2030}, {229, "Acircumflex", 0x00C2},
	{230, "Ecircumflex", 0x00CA}, {231, "Aacute", 0x00C1}, {232, "Edieresis", 0x00CB},
	{233, "Egrave", 0x00C8}, {234, "Iacute", 0x00CD}, {235, "Icircumflex", 0x00CE},
	{236, "Idieresis", 0x00CF}, {237, "Igrave", 0x00CC}, {238, "Oacute", 0x00D3},
	{239, "Ocircumflex", 0x00D4}, {241, "Ograve", 0x00D2}, {242, "Uacute", 0x00DA},
	{243, "Ucircumflex", 0x00DB}, {244, "Ugrave", 0x00D9}, {245, "dotlessi", 0x0131},
	{246, "circumflex", 0x02C6}, {247, "tilde", 0x02DC}, {248, "macron", 0x00AF},
	{249, "breve", 0x02D8}, {250, "dotaccent", 0x02D9}, {251, "ring", 0x02DA},
	{252, "cedilla", 0x00B8}, {253, "hungarumlaut", 0x02DD}, {254, "ogonek", 0x02DB},
	{255, "caron", 0x02C7},
}

// extraGlyphs are names no base encoding carries a code for but which appear
// in /Differences arrays often enough to matter — the f-ligatures a subset
// font uses, and the minus sign LaTeX reaches for.
var extraGlyphs = []codedGlyph{
	{0, "ff", 0xFB00}, {0, "ffi", 0xFB03}, {0, "ffl", 0xFB04},
	{0, "minus", 0x2212}, {0, "nbspace", 0x00A0}, {0, "Euro", 0x20AC},
	{0, "sfthyphen", 0x00AD}, {0, "middot", 0x00B7}, {0, "nbsp", 0x00A0},
}

// The three base encodings, as code-to-character tables. A zero rune means
// the encoding leaves that code undefined.
var (
	standardEncoding [256]rune
	winAnsiEncoding  [256]rune
	macRomanEncoding [256]rune

	// standardNames, winAnsiNames and macRomanNames are the same tables in
	// glyph names, needed because /Differences is applied to a base encoding
	// by name.
	standardNames [256]string
	winAnsiNames  [256]string
	macRomanNames [256]string

	// glyphRune maps a glyph name to the character it draws.
	glyphRune = map[string]rune{}
)

// init builds the encoding tables from the compact data above.
//
// This is the one place in ovrin with an init function, and it is here because
// the alternative is three 256-entry literals with the Latin-1 range written
// out three times. It touches no global state outside this package and has no
// side effect a caller could observe, which is the property rule §5.5 is
// protecting (docs/rules.md §5.5).
func init() {
	for i, name := range asciiGlyphs {
		code := byte(i + 32)
		r := rune(i + 32)
		standardEncoding[code], winAnsiEncoding[code], macRomanEncoding[code] = r, r, r
		standardNames[code], winAnsiNames[code], macRomanNames[code] = name, name, name
		glyphRune[name] = r
	}
	// StandardEncoding is the odd one: it draws typographic quotes where
	// ASCII has an apostrophe and a backtick, which is why text extracted
	// from an old Type 1 font comes out with U+2019 in its contractions.
	standardEncoding['\''], standardNames['\''] = 0x2019, "quoteright"
	standardEncoding['`'], standardNames['`'] = 0x2018, "quoteleft"
	glyphRune["quoteright"], glyphRune["quoteleft"] = 0x2019, 0x2018

	for i, name := range latin1Glyphs {
		code := byte(i + 161)
		r := rune(i + 161)
		winAnsiEncoding[code], winAnsiNames[code] = r, name
		glyphRune[name] = r
	}
	apply := func(tbl []codedGlyph, enc *[256]rune, names *[256]string) {
		for _, g := range tbl {
			if enc != nil {
				enc[g.code] = g.r
				names[g.code] = g.name
			}
			if _, have := glyphRune[g.name]; !have {
				glyphRune[g.name] = g.r
			}
		}
	}
	apply(winAnsiHigh, &winAnsiEncoding, &winAnsiNames)
	apply(standardHigh, &standardEncoding, &standardNames)
	apply(macRomanHigh, &macRomanEncoding, &macRomanNames)
	apply(extraGlyphs, nil, nil)
}

// baseEncoding returns the table a base-encoding name selects, and whether the
// name was one this package knows.
func baseEncoding(n Name) ([256]rune, [256]string, bool) {
	switch n {
	case "WinAnsiEncoding":
		return winAnsiEncoding, winAnsiNames, true
	case "MacRomanEncoding":
		return macRomanEncoding, macRomanNames, true
	case "StandardEncoding", "PDFDocEncoding":
		return standardEncoding, standardNames, true
	}
	return standardEncoding, standardNames, false
}

// runeForGlyph returns the character a glyph name draws, and whether the name
// could be resolved at all.
//
// The uniXXXX and uXXXX forms are handled arithmetically because they are how
// a subset font names a glyph the Adobe Glyph List has no name for, and they
// are exact. A suffixed name — "a.sc", "one.oldstyle" — is resolved from its
// stem, which is the convention every font tool follows. Anything else fails,
// and failing is the point: a "g43" resolved to a guess is the gibberish this
// package exists to refuse.
func runeForGlyph(name string) (rune, bool) {
	if name == "" {
		return 0, false
	}
	if r, ok := glyphRune[name]; ok {
		return r, true
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		return runeForGlyph(name[:i])
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if v, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			return rune(v), true
		}
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil && v <= 0x10FFFF {
			return rune(v), true
		}
	}
	return 0, false
}

// The widths of the standard 14 fonts, for a font dictionary with no /Widths.
//
// A font with no widths array is legal for the fourteen fonts every viewer is
// required to have, and a document that uses one is common in generated PDFs.
// Without a width every glyph advances by the same amount, the boxes drift
// along the line and the word gaps stop meaning anything — so two of the
// fourteen metrics are carried here, in the smallest form that is honest:
// Helvetica for the sans faces and Times for the serif ones, both for codes
// 32..126, with Courier's single fixed width and a fallback for everything
// else. Bold and italic variants differ from these by a few thousandths and
// are approximated by them, which costs a fraction of a character's box and
// saves four more tables.
var helveticaWidths = [95]int16{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

var timesWidths = [95]int16{
	250, 333, 408, 500, 500, 833, 778, 180, 333, 333, 500, 564, 250, 333, 250, 278,
	500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 278, 278, 564, 564, 564, 444,
	921, 722, 667, 667, 722, 611, 556, 722, 722, 333, 389, 722, 611, 889, 722, 722,
	556, 722, 667, 556, 611, 722, 722, 944, 722, 722, 611, 333, 278, 333, 469, 500,
	333, 444, 500, 444, 500, 444, 333, 500, 500, 278, 278, 500, 278, 778, 500, 500,
	500, 500, 333, 389, 278, 500, 500, 722, 500, 500, 444, 480, 200, 480, 541,
}

// builtinWidth returns the width of code in a standard-14 face, in thousandths
// of an em, and whether a table applied.
func builtinWidth(base string, code byte) (float64, bool) {
	b := strings.ToLower(base)
	if i := strings.IndexByte(b, '+'); i >= 0 && i+1 < len(b) {
		// A subset prefix — "ABCDEF+Helvetica" — says nothing about metrics.
		b = b[i+1:]
	}
	switch {
	case strings.Contains(b, "courier") || strings.Contains(b, "mono"):
		return 600, true
	case strings.Contains(b, "times") || strings.Contains(b, "roman") ||
		strings.Contains(b, "serif") && !strings.Contains(b, "sans"):
		if code >= 32 && code <= 126 {
			return float64(timesWidths[code-32]), true
		}
		return 500, true
	case strings.Contains(b, "helvetica") || strings.Contains(b, "arial") || strings.Contains(b, "sans"):
		if code >= 32 && code <= 126 {
			return float64(helveticaWidths[code-32]), true
		}
		return 500, true
	}
	return 0, false
}
