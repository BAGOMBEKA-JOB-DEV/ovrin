package ground

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// groundable reports whether a literal is worth searching for.
//
// An empty or blank value matches everywhere and therefore means nothing, and
// reporting 1.0 for it would put a floor under exactly the fields a reviewer
// most needs to see. Those are reported as having no grounding signal at all
// rather than a perfect one.
func groundable(lit string) bool { return strings.TrimSpace(lit) != "" }

// findLiteral searches for the value's own bytes.
//
// This is the verbatim pass, and it runs first so that a value present in its
// own form is never demoted to a normalised match by a type-aware search that
// also happens to find it.
func findLiteral(doc *normalise.Result, lit string, kind Kind) (normalise.Span, bool) {
	numeric := kind == KindNumber || kind == KindCurrency || kind == KindDate
	from := 0
	for {
		i := strings.Index(doc.Text[from:], lit)
		if i < 0 {
			return normalise.Span{}, false
		}
		sp := normalise.Span{Start: from + i, End: from + i + len(lit)}
		if bounded(doc.Text, sp, numeric) && acceptable(doc, sp) {
			return sp, true
		}
		from = sp.Start + 1
	}
}

// findFolded searches the folded text for the folded value.
//
// Folding is Unicode normalisation, whitespace collapse and case folding, and
// it is what makes ACME LTD and Acme Ltd one string (docs/confidence.md,
// Comparison). The offset index built alongside the folded text is what turns
// a hit back into a span in the real text; without it the match would be
// found and could not be reported.
func findFolded(doc *normalise.Result, lit string, kind Kind) (normalise.Span, bool) {
	hay, index := fold(doc.Text)
	for _, probe := range probes(lit, kind) {
		needle, _ := fold(probe)
		if needle == "" {
			continue
		}
		from := 0
		for {
			i := strings.Index(hay[from:], needle)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(needle)
			if bounded(hay, normalise.Span{Start: start, End: end}, false) {
				sp := normalise.Span{Start: index[start], End: index[end]}
				if acceptable(doc, sp) {
					return sp, true
				}
			}
			from = start + 1
		}
	}
	return normalise.Span{}, false
}

// probes returns the strings to search for on behalf of a value.
//
// Only booleans have more than one. A document that means yes writes "yes" as
// often as "true", and grounding a boolean is weak enough already; the
// alternative is a signal that reads 0.0 on every correctly extracted
// checkbox in the corpus.
func probes(lit string, kind Kind) []string {
	if kind != KindBool {
		return []string{lit}
	}
	if lit == "true" {
		return []string{"true", "yes"}
	}
	return []string{"false", "no"}
}

// acceptable rejects a match that reaches into text ovrin inserted.
//
// A page marker is not the document. Without this, grounding the number 2
// against any document at all succeeds, on the marker introducing page two.
func acceptable(doc *normalise.Result, sp normalise.Span) bool {
	for _, p := range doc.Pages {
		if sp.Start < p.Marker.End && sp.End > p.Marker.Start {
			return false
		}
	}
	return true
}

// bounded reports whether a match begins and ends at a word boundary.
//
// Without it a search for "Smith" finds "Smithson", and a grounding signal
// that cannot tell those apart is worse than none: it reports 1.0 for a value
// the document does not contain.
//
// numeric widens the boundary to include the separators inside a formatted
// figure, because "25" sits at an ordinary word boundary inside "25,000" —
// a comma is not a letter — and grounding twenty-five against a document that
// says twenty-five thousand is the worst false positive this package could
// produce.
func bounded(s string, sp normalise.Span, numeric bool) bool {
	if sp.Start < 0 || sp.End > len(s) || sp.Start >= sp.End {
		return false
	}
	first, _ := utf8.DecodeRuneInString(s[sp.Start:sp.End])
	last, _ := utf8.DecodeLastRuneInString(s[sp.Start:sp.End])
	if sp.Start > 0 {
		before, _ := utf8.DecodeLastRuneInString(s[:sp.Start])
		if wordRune(before) && wordRune(first) {
			return false
		}
	}
	if sp.End < len(s) {
		after, _ := utf8.DecodeRuneInString(s[sp.End:])
		if wordRune(last) && wordRune(after) {
			return false
		}
	}
	return !numeric || numericEdges(s, sp)
}

// numericEdges reports whether a match is a whole numeric literal rather than
// part of a longer one.
//
// It applies the continuation rules [scanNumbers] uses, so that the two agree
// on where a number starts and stops. Getting this wrong in either direction
// is expensive: too loose and twenty-five grounds against a page saying
// twenty-five thousand, too tight and "3" fails to ground against "page 3 4".
func numericEdges(s string, sp normalise.Span) bool {
	if sp.Start > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:sp.Start])
		prev := sp.Start - size
		switch {
		case r == '.' || r == ',':
			if prev > 0 && isDigit(s[prev-1]) {
				return false
			}
		case strings.ContainsRune(groupSeparators, r):
			if prev > 0 && isDigit(s[prev-1]) && groupOf3(s, sp.Start) {
				return false
			}
		}
	}
	if sp.End < len(s) {
		r, size := utf8.DecodeRuneInString(s[sp.End:])
		next := sp.End + size
		switch {
		case r == '.' || r == ',':
			if next < len(s) && isDigit(s[next]) {
				return false
			}
		case strings.ContainsRune(groupSeparators, r):
			if groupOf3(s, next) {
				return false
			}
		}
	}
	return true
}

func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// fold reduces s to the form strings are compared in, and returns an index
// mapping each byte of the result back to a byte offset in s.
//
// The three steps are the ones docs/confidence.md specifies — Unicode
// normalisation, whitespace collapse, case folding — plus the removal of the
// characters that render as nothing. That last one is not sanitising: the
// text is unchanged and the finding normalisation raised still stands. It
// means only that a payload hidden inside a word does not also hide the word
// from the search that would have found it.
//
// Normalisation is [normalise.Canonical] rather than a second implementation,
// because two implementations of a Unicode subset drift and the first symptom
// would be a grounding signal reading zero on a value that is plainly there.
//
// index has one entry per byte of the result plus a final entry equal to
// len(s).
func fold(s string) (string, []int) {
	canon, idx := normalise.Canonical(s)

	var b strings.Builder
	b.Grow(len(canon))
	out := make([]int, 0, len(canon)+1)

	for i := 0; i < len(canon); {
		r, size := utf8.DecodeRuneInString(canon[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteByte(canon[i])
			out = append(out, idx[i])
			i++
			continue
		}
		if normalise.Ignorable(r) {
			i += size
			continue
		}
		rep := foldRune(r)
		for k := 0; k < len(rep); k++ {
			out = append(out, idx[i])
		}
		b.WriteString(rep)
		i += size
	}
	out = append(out, len(s))
	return b.String(), out
}

// fullFold are the case foldings that are not a single lower-case rune.
//
// The standard library exposes simple case mapping, and simple mapping leaves
// ß as ß while full case folding makes it ss. The set of characters where the
// two differ and a document might plausibly contain one is small enough to
// enumerate, and enumerating it is the honest alternative to claiming full
// case folding this package does not implement. The ligatures are here for
// completeness: normalisation has already expanded them by the time text
// reaches this function.
var fullFold = map[rune]string{
	0x00DF: "ss", // ß
	0x1E9E: "ss", // ẞ
	0x0130: "i̇", // İ
	0x01F0: "ǰ",
	0x1E96: "ẖ",
	0x1E97: "ẗ",
	0x1E98: "ẘ",
	0x1E99: "ẙ",
	0x1E9A: "aʾ",
	0x0149: "ʼn",
	0x0587: "եւ",
	0xFB13: "մն",
	0xFB14: "մե",
	0xFB15: "մի",
	0xFB16: "վն",
	0xFB17: "մխ",
}

// foldRune returns the case-folded form of one rune.
//
// Simple folding via unicode.ToLower, with the enumerated full foldings above
// and the two Greek sigmas mapped together, which is the one case in ordinary
// running text where simple lower-casing gets folding wrong.
func foldRune(r rune) string {
	if s, ok := fullFold[r]; ok {
		return s
	}
	switch r {
	case 0x03C2: // final sigma folds to sigma
		return "σ"
	case 0x0345: // combining ypogegrammeni
		return "ι"
	}
	if l := unicode.ToLower(r); l != r {
		return string(l)
	}
	return string(r)
}
