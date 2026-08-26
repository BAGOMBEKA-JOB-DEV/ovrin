package normalise

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCanonical(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ascii is untouched", "Invoice 42", "Invoice 42"},
		{"empty stays empty", "", ""},
		{"latin ligature fi expands", "ofﬁce", "office"},
		{"latin ligature ffl expands", "waﬄe", "waffle"},
		{"long s becomes s", "Congreſs", "Congress"},
		{"ij digraph expands", "Ĳsselmeer", "IJsselmeer"},
		{"non-breaking space becomes space", "25 000", "25 000"},
		{"ideographic space becomes space", "a　b", "a b"},
		{"whitespace run collapses to one space", "a  \t\n  b", "a b"},
		{"leading whitespace collapses", "   a", " a"},
		{"fullwidth digits become ascii", "２５０００", "25000"},
		{"fullwidth comma becomes ascii", "２５，０００", "25,000"},
		{"superscript two becomes two", "12 m²", "12 m2"},
		{"vulgar half expands", "½", "1⁄2"},
		{"roman numeral becomes letters", "Ⅳ", "IV"},
		{"circled digit becomes digit", "①", "1"},
		{"trade mark expands", "Acme™", "AcmeTM"},
		{"ohm sign becomes omega", "Ω", "Ω"},
		{"angstrom sign becomes a ring", "Å", "Å"},
		{"math bold latin becomes ascii", "\U0001D408\U0001D420\U0001D427\U0001D428\U0001D42B\U0001D41E", "Ignore"},
		{"math double struck digits become ascii", "\U0001D7D9\U0001D7DA", "12"},
		{"math sans bold letters become ascii", "\U0001D5D8\U0001D5E5", "ER"},
		{"unassigned math hole is left alone", "\U0001D455", "\U0001D455"},
		{"combining acute composes", "é", "é"},
		{"combining acute on capital composes", "É", "É"},
		{"vietnamese two marks compose in order", "ế", "ế"},
		{"cyrillic breve composes", "й", "й"},
		{"zero width space is kept", "a\u200bb", "a\u200bb"},
		{"soft hyphen is kept", "a\u00adb", "a\u00adb"},
		{"bidi override is kept", "a\u202eb", "a\u202eb"},
		{"unknown code point passes through", "中文", "中文"},
		{"halfwidth katakana is left alone", "ｶﾞ", "ｶﾞ"},
		{"hangul is left alone", "가", "가"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, index := Canonical(c.in)
			if got != c.want {
				t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
			}
			checkIndex(t, c.in, got, index)
		})
	}
}

// checkIndex asserts the contract of the index Canonical returns: one entry
// per output byte plus a terminator, never decreasing, always inside the
// source.
func checkIndex(t *testing.T, src, out string, index []int) {
	t.Helper()
	if len(index) != len(out)+1 {
		t.Fatalf("index has %d entries for %d output bytes, want %d", len(index), len(out), len(out)+1)
	}
	if index[len(index)-1] != len(src) {
		t.Errorf("index terminator = %d, want %d", index[len(index)-1], len(src))
	}
	for i := 1; i < len(index); i++ {
		if index[i] < index[i-1] {
			t.Fatalf("index is not monotonic at %d: %d then %d", i, index[i-1], index[i])
		}
	}
	for i, v := range index {
		if v < 0 || v > len(src) {
			t.Fatalf("index[%d] = %d, outside the source of %d bytes", i, v, len(src))
		}
	}
}

func TestCanonicalInvalidUTF8IsPreserved(t *testing.T) {
	t.Parallel()
	in := "a\xffb\xc3"
	got, index := Canonical(in)
	if got != in {
		t.Errorf("Canonical(% x) = % x, want the bytes unchanged", in, got)
	}
	checkIndex(t, in, got, index)
}

func TestIgnorable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		r    rune
		want bool
	}{
		{"zero width space", 0x200B, true},
		{"zero width joiner", 0x200D, true},
		{"soft hyphen", 0x00AD, true},
		{"byte order mark", 0xFEFF, true},
		{"tag latin small letter a", 0xE0061, true},
		{"right to left override", 0x202E, true},
		{"left to right mark", 0x200E, true},
		{"first strong isolate", 0x2068, true},
		{"ordinary space", ' ', false},
		{"letter", 'a', false},
		{"combining acute", 0x0301, false},
		{"variation selector 16", 0xFE0F, false},
		{"arabic number sign", 0x0600, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Ignorable(c.r); got != c.want {
				t.Errorf("Ignorable(U+%04X) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}

// TestCompatTablesAreSorted pins the invariant the binary searches depend on.
// A regenerated table that is not sorted would silently stop matching rather
// than fail to compile.
func TestCompatTablesAreSorted(t *testing.T) {
	t.Parallel()
	for i := 1; i < len(compatOne); i++ {
		if compatOne[i][0] <= compatOne[i-1][0] {
			t.Fatalf("compatOne is not sorted at %d: U+%04X after U+%04X", i, compatOne[i][0], compatOne[i-1][0])
		}
	}
	for i := 1; i < len(compatMany); i++ {
		if compatMany[i].r <= compatMany[i-1].r {
			t.Fatalf("compatMany is not sorted at %d: U+%04X after U+%04X", i, compatMany[i].r, compatMany[i-1].r)
		}
	}
	for i := 1; i < len(canonCompose); i++ {
		a, b := canonCompose[i-1], canonCompose[i]
		if a[0] > b[0] || (a[0] == b[0] && a[1] >= b[1]) {
			t.Fatalf("canonCompose is not sorted at %d", i)
		}
	}
}

// TestCanonicalIsIdempotent asserts that normalising an already normalised
// string changes nothing, which is the property every downstream comparison
// assumes when it folds a value and the text with the same function.
func TestCanonicalIsIdempotent(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"Invoice 42", "ofﬁce", "２５，０００",
		"é", "a  b", "①②", "\U0001D408\U0001D420", "½",
		"a\u200bb", "中文", "ｶﾞ",
	}
	for _, in := range inputs {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			once, _ := Canonical(in)
			twice, _ := Canonical(once)
			if once != twice {
				t.Errorf("Canonical is not idempotent: %q then %q", once, twice)
			}
			if !utf8.ValidString(in) {
				return
			}
			if strings.ContainsRune(once, utf8.RuneError) && !strings.ContainsRune(in, utf8.RuneError) {
				t.Errorf("Canonical(%q) introduced a replacement character", in)
			}
		})
	}
}
