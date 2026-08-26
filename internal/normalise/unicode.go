package normalise

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Blocks of the Mathematical Alphanumeric Symbols that this package maps
// arithmetically. Each letter block is 26 uppercase followed by 26 lowercase;
// each digit block is ten digits. doc.go says why these are covered.
var (
	mathLetterBlocks = [...]rune{
		0x1D400, 0x1D434, 0x1D468, 0x1D49C, 0x1D4D0, 0x1D504, 0x1D538,
		0x1D56C, 0x1D5A0, 0x1D5D4, 0x1D608, 0x1D63C, 0x1D670,
	}
	mathDigitBlocks = [...]rune{0x1D7CE, 0x1D7D8, 0x1D7E2, 0x1D7EC, 0x1D7F6}

	// mathHoles are the twenty-four code points reserved in those blocks
	// because their glyphs live in Letterlike Symbols. They are unassigned,
	// NFKC leaves them alone, and so does this package.
	mathHoles = [...]rune{
		0x1D455, 0x1D49D, 0x1D4A0, 0x1D4A1, 0x1D4A3, 0x1D4A4, 0x1D4A7,
		0x1D4A8, 0x1D4AD, 0x1D4BA, 0x1D4BC, 0x1D4C4, 0x1D506, 0x1D50B,
		0x1D50C, 0x1D515, 0x1D51D, 0x1D53A, 0x1D53F, 0x1D545, 0x1D547,
		0x1D548, 0x1D549, 0x1D551,
	}
)

// zeroWidth is the closed set of invisible characters this package reports.
//
// It is a list rather than unicode.Cf because Cf holds characters that
// legitimate Arabic and Indic text uses in every paragraph, and a detector
// that fires on every Arabic document is a detector operators learn to
// ignore. Zero-width joiner and non-joiner are on the list even though they
// are legitimate in several scripts: that false positive is acknowledged in
// docs/adr/0017-untrusted-document-content.md and accepted, because the
// alternative is a payload hidden inside an ordinary English word.
var zeroWidth = map[rune]bool{
	0x00AD: true, // soft hyphen
	0x180E: true, // mongolian vowel separator
	0x200B: true, // zero width space
	0x200C: true, // zero width non-joiner
	0x200D: true, // zero width joiner
	0x2060: true, // word joiner
	0x2061: true, // function application
	0x2062: true, // invisible times
	0x2063: true, // invisible separator
	0x2064: true, // invisible plus
	0xFEFF: true, // zero width no-break space
	0xFFF9: true, // interlinear annotation anchor
	0xFFFA: true, // interlinear annotation separator
	0xFFFB: true, // interlinear annotation terminator
}

// isZeroWidth reports whether r renders as nothing.
func isZeroWidth(r rune) bool {
	if zeroWidth[r] {
		return true
	}
	// Tag characters: deprecated, invisible, and a current vector for hiding
	// an instruction inside a string that looks like one word.
	return r == 0xE0001 || (r >= 0xE0020 && r <= 0xE007F)
}

// isBidiControl reports whether r changes the order text renders in without
// changing the order it is stored in.
func isBidiControl(r rune) bool { return unicode.Is(unicode.Bidi_Control, r) }

// Ignorable reports whether r renders as nothing or reorders what is around
// it, which is exactly the set this package reports as a [Finding].
//
// Exported for grounding, which removes these characters before comparing so
// that a payload hidden inside a word does not also hide the word from the
// search. That is not sanitising: the normalised text keeps every one of
// them, and the finding still stands.
func Ignorable(r rune) bool { return isZeroWidth(r) || isBidiControl(r) }

// compatMap returns the NFKC compatibility mapping of r within the ranges
// this package covers, and whether one exists.
func compatMap(r rune) (string, bool) {
	if r < 0x00A0 {
		return "", false // ASCII and C1 are already normal
	}
	if r >= 0x1D400 {
		if s, ok := mathAlnum(r); ok {
			return s, true
		}
	}
	if i := sort.Search(len(compatOne), func(i int) bool { return compatOne[i][0] >= r }); i < len(compatOne) && compatOne[i][0] == r {
		return string(compatOne[i][1]), true
	}
	if i := sort.Search(len(compatMany), func(i int) bool { return compatMany[i].r >= r }); i < len(compatMany) && compatMany[i].r == r {
		return compatMany[i].s, true
	}
	return "", false
}

// mathAlnum maps a Mathematical Alphanumeric Symbol to its ASCII letter or
// digit. The blocks are regular, so this is arithmetic rather than a thousand
// table entries.
func mathAlnum(r rune) (string, bool) {
	if r < 0x1D400 || r > 0x1D7FF {
		return "", false
	}
	for _, h := range mathHoles {
		if r == h {
			return "", false
		}
	}
	for _, b := range mathLetterBlocks {
		if r >= b && r < b+52 {
			i := r - b
			if i < 26 {
				return string('A' + i), true
			}
			return string('a' + i - 26), true
		}
	}
	for _, b := range mathDigitBlocks {
		if r >= b && r < b+10 {
			return string('0' + r - b), true
		}
	}
	return "", false
}

// composeCanon returns the canonical composition of a base code point and a
// following combining mark, and whether one exists in the covered ranges.
func composeCanon(base, mark rune) (rune, bool) {
	i := sort.Search(len(canonCompose), func(i int) bool {
		if canonCompose[i][0] != base {
			return canonCompose[i][0] > base
		}
		return canonCompose[i][1] >= mark
	})
	if i < len(canonCompose) && canonCompose[i][0] == base && canonCompose[i][1] == mark {
		return canonCompose[i][2], true
	}
	return 0, false
}

// isMark reports whether r is a non-spacing combining mark, which is the only
// thing this package will try to compose onto a base.
func isMark(r rune) bool { return unicode.Is(unicode.Mn, r) }

// unit is one source rune, or one source byte that is not a rune, together
// with what it normalises to. Carrying the source range on every unit is what
// makes the whole mapping possible: nothing downstream has to reconstruct it.
type unit struct {
	out      string
	srcStart int
	srcEnd   int
	verbatim bool
	space    bool
}

// units decomposes s into normalised units, applying the compatibility
// mappings and then composing combining marks onto the bases in front of
// them. Invalid UTF-8 is emitted one byte at a time, unchanged.
func units(s string) []unit {
	out := make([]unit, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			out = append(out, unit{out: s[i : i+1], srcStart: i, srcEnd: i + 1, verbatim: true})
			i++
			continue
		}
		u := unit{srcStart: i, srcEnd: i + size}
		if m, ok := compatMap(r); ok {
			u.out = m
		} else {
			u.out = s[i : i+size]
			u.verbatim = true
		}
		u.space = allSpace(u.out)
		out = append(out, u)
		i += size
	}
	return compose(out)
}

// compose merges a unit that is a single combining mark into the unit before
// it, where the two have a canonical composition. One mark at a time, and no
// canonical reordering: doc.go says why.
func compose(in []unit) []unit {
	out := in[:0]
	for _, u := range in {
		if len(out) > 0 && !u.space {
			m, size := utf8.DecodeRuneInString(u.out)
			if size == len(u.out) && isMark(m) {
				prev := &out[len(out)-1]
				b, bsize := utf8.DecodeLastRuneInString(prev.out)
				if bsize > 0 && b != utf8.RuneError {
					if c, ok := composeCanon(b, m); ok {
						prev.out = prev.out[:len(prev.out)-bsize] + string(c)
						prev.srcEnd = u.srcEnd
						prev.verbatim = false
						continue
					}
				}
			}
		}
		out = append(out, u)
	}
	return out
}

// allSpace reports whether every rune of s is whitespace. An empty string is
// not whitespace: it is nothing, and nothing does not start a collapse run.
func allSpace(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// Canonical returns s with this package's Unicode subset applied and runs of
// whitespace collapsed to one space, together with an index that maps each
// byte offset of the result back to a byte offset in s.
//
// It is exported so that grounding compares a value against the text using
// exactly the transformation the text went through. A second implementation
// would drift, and the first symptom would be a grounding signal that reads
// zero on a value that is plainly there.
//
// index has one entry per byte of the result plus a final entry equal to
// len(s), so index[i:j] bounds the source of result[i:j].
func Canonical(s string) (string, []int) {
	us := units(s)
	var b strings.Builder
	b.Grow(len(s))
	index := make([]int, 0, len(s)+1)

	for i := 0; i < len(us); {
		u := us[i]
		if u.space {
			j := i
			for j < len(us) && us[j].space {
				j++
			}
			b.WriteByte(' ')
			index = append(index, u.srcStart)
			i = j
			continue
		}
		for k := 0; k < len(u.out); k++ {
			index = append(index, u.srcStart)
		}
		b.WriteString(u.out)
		i++
	}
	index = append(index, len(s))
	return b.String(), index
}
