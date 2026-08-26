package ground

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// date is a calendar date, and an instant only when the value carried a time
// of day.
//
// Dates compare as instants (docs/confidence.md, Comparison), but a document
// that writes 3 March 2026 is not claiming midnight — it is claiming a day.
// Comparing at day granularity unless both sides carry a clock is what makes
// 03/04/26 and 2026-04-03 one date instead of two.
type date struct {
	y, m, d int
	clock   bool
	t       time.Time
}

func dateOf(t time.Time) date {
	y, m, d := t.Date()
	return date{
		y: y, m: int(m), d: d,
		clock: t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0,
		t:     t,
	}
}

func (a date) equal(b date) bool {
	if a.clock && b.clock {
		return a.t.Equal(b.t)
	}
	return a.y == b.y && a.m == b.m && a.d == b.d
}

// valid reports whether the components name a real day, so that 31 February
// is discarded rather than rolled into March.
func (a date) valid() bool {
	if a.y < 1 || a.m < 1 || a.m > 12 || a.d < 1 || a.d > 31 {
		return false
	}
	t := time.Date(a.y, time.Month(a.m), a.d, 0, 0, 0, 0, time.UTC)
	return t.Year() == a.y && int(t.Month()) == a.m && t.Day() == a.d
}

// findDate searches for a written date equal to any of want.
func findDate(doc *normalise.Result, want []date, order DateOrder) (normalise.Span, bool) {
	for _, h := range scanDates(doc.Text, order) {
		if !acceptable(doc, h.span) || !bounded(doc.Text, h.span, true) {
			continue
		}
		for _, a := range h.dates {
			for _, b := range want {
				if a.equal(b) {
					return h.span, true
				}
			}
		}
	}
	return normalise.Span{}, false
}

// dateHit is one written date, with every reading of it that is a real day.
type dateHit struct {
	span  normalise.Span
	dates []date
}

// The token kinds the date scanner works on.
const (
	tokDigits = iota
	tokLetters
	tokOther
)

type tok struct {
	s    string
	kind int
	span normalise.Span
}

// tokenize splits s into runs of digits, runs of letters and single other
// runes, skipping whitespace. Spans are byte offsets into s, which is what
// lets a matched date be reported as a span in the normalised text.
func tokenize(s string) []tok {
	var out []tok
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case unicode.IsDigit(r):
			j := i
			for j < len(s) {
				r2, s2 := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsDigit(r2) {
					break
				}
				j += s2
			}
			out = append(out, tok{s: s[i:j], kind: tokDigits, span: normalise.Span{Start: i, End: j}})
			i = j
		case unicode.IsLetter(r):
			j := i
			for j < len(s) {
				r2, s2 := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsLetter(r2) {
					break
				}
				j += s2
			}
			out = append(out, tok{s: s[i:j], kind: tokLetters, span: normalise.Span{Start: i, End: j}})
			i = j
		default:
			out = append(out, tok{s: s[i : i+size], kind: tokOther, span: normalise.Span{Start: i, End: i + size}})
			i += size
		}
	}
	return out
}

// scanDates returns every date written in s.
//
// The forms understood are listed here because the list is the contract, and
// a form outside it produces a false negative rather than a wrong answer:
//
//	2026-03-03   2026/03/03   2026.03.03
//	03/03/2026   3-3-26       3.3.2026        read per order
//	3 March 2026    3rd March 2026    3rd of March 2026
//	March 3 2026    March 3rd, 2026
//	the third of March 2026        ordinals to thirty-first
//
// Month names are the English full names and their three-letter
// abbreviations. A date with no year is not scanned: it cannot be compared as
// an instant, and guessing the year is how a claim ends up dated to the wrong
// decade.
func scanDates(s string, order DateOrder) []dateHit {
	toks := tokenize(s)
	var out []dateHit
	for i := 0; i < len(toks); i++ {
		if h, next, ok := dateAt(toks, i, order); ok {
			out = append(out, h)
			i = next - 1
		}
	}
	return out
}

func dateAt(toks []tok, i int, order DateOrder) (dateHit, int, bool) {
	if h, n, ok := numericDate(toks, i, order); ok {
		return h, n, true
	}
	if h, n, ok := dayMonthYear(toks, i); ok {
		return h, n, true
	}
	if h, n, ok := monthDayYear(toks, i); ok {
		return h, n, true
	}
	return dateHit{}, i, false
}

// numericDate reads the all-digits forms.
func numericDate(toks []tok, i int, order DateOrder) (dateHit, int, bool) {
	if i+4 >= len(toks) {
		return dateHit{}, i, false
	}
	a, s1, b, s2, c := toks[i], toks[i+1], toks[i+2], toks[i+3], toks[i+4]
	if a.kind != tokDigits || b.kind != tokDigits || c.kind != tokDigits {
		return dateHit{}, i, false
	}
	if s1.kind != tokOther || s2.kind != tokOther || s1.s != s2.s || !strings.Contains("-/.", s1.s) {
		return dateHit{}, i, false
	}
	if s1.span.Start != a.span.End || b.span.Start != s1.span.End ||
		s2.span.Start != b.span.End || c.span.Start != s2.span.End {
		return dateHit{}, i, false
	}
	n1, n2, n3 := atoi(a.s), atoi(b.s), atoi(c.s)
	span := normalise.Span{Start: a.span.Start, End: c.span.End}

	var cands []date
	add := func(y, m, d int) {
		v := date{y: expandYear(y), m: m, d: d}
		if v.valid() {
			cands = append(cands, v)
		}
	}
	switch {
	case len(a.s) == 4:
		add(n1, n2, n3) // year first, unambiguous
	case len(c.s) == 4 || len(c.s) == 2:
		switch order {
		case DayFirst:
			add(n3, n2, n1)
		case MonthFirst:
			add(n3, n1, n2)
		case YearFirst:
			add(n1, n2, n3)
		default:
			add(n3, n2, n1)
			add(n3, n1, n2)
		}
	default:
		return dateHit{}, i, false
	}
	if len(cands) == 0 {
		return dateHit{}, i, false
	}
	return dateHit{span: span, dates: dedupe(cands)}, i + 5, true
}

// dayMonthYear reads "3 March 2026", "3rd of March 2026" and "the third of
// March 2026".
func dayMonthYear(toks []tok, i int) (dateHit, int, bool) {
	j := i
	day, ok := 0, false
	if j < len(toks) && toks[j].kind == tokDigits && len(toks[j].s) <= 2 {
		day, ok = atoi(toks[j].s), true
		j++
		j = skipOrdinalSuffix(toks, j)
	} else {
		if day, j, ok = ordinalWords(toks, j); !ok {
			return dateHit{}, i, false
		}
	}
	j = skipWord(toks, j, "of")
	if j >= len(toks) || toks[j].kind != tokLetters {
		return dateHit{}, i, false
	}
	m, ok := monthOf(toks[j].s)
	if !ok {
		return dateHit{}, i, false
	}
	j++
	j = skipPunct(toks, j, ",")
	if j >= len(toks) || toks[j].kind != tokDigits || len(toks[j].s) > 4 {
		return dateHit{}, i, false
	}
	v := date{y: expandYear(atoi(toks[j].s)), m: m, d: day}
	if !v.valid() {
		return dateHit{}, i, false
	}
	return dateHit{
		span:  normalise.Span{Start: toks[i].span.Start, End: toks[j].span.End},
		dates: []date{v},
	}, j + 1, true
}

// monthDayYear reads "March 3, 2026" and "March 3rd 2026".
func monthDayYear(toks []tok, i int) (dateHit, int, bool) {
	if i >= len(toks) || toks[i].kind != tokLetters {
		return dateHit{}, i, false
	}
	m, ok := monthOf(toks[i].s)
	if !ok {
		return dateHit{}, i, false
	}
	j := i + 1
	if j >= len(toks) || toks[j].kind != tokDigits || len(toks[j].s) > 2 {
		return dateHit{}, i, false
	}
	day := atoi(toks[j].s)
	j++
	j = skipOrdinalSuffix(toks, j)
	j = skipPunct(toks, j, ",")
	if j >= len(toks) || toks[j].kind != tokDigits || len(toks[j].s) > 4 {
		return dateHit{}, i, false
	}
	v := date{y: expandYear(atoi(toks[j].s)), m: m, d: day}
	if !v.valid() {
		return dateHit{}, i, false
	}
	return dateHit{
		span:  normalise.Span{Start: toks[i].span.Start, End: toks[j].span.End},
		dates: []date{v},
	}, j + 1, true
}

func skipOrdinalSuffix(toks []tok, j int) int {
	if j < len(toks) && toks[j].kind == tokLetters {
		switch strings.ToLower(toks[j].s) {
		case "st", "nd", "rd", "th":
			return j + 1
		}
	}
	return j
}

func skipWord(toks []tok, j int, word string) int {
	if j < len(toks) && toks[j].kind == tokLetters && strings.EqualFold(toks[j].s, word) {
		return j + 1
	}
	return j
}

func skipPunct(toks []tok, j int, p string) int {
	if j < len(toks) && toks[j].kind == tokOther && toks[j].s == p {
		return j + 1
	}
	return j
}

// ordinalWords reads "third", "twenty-first" and the rest, which is the form
// docs/pipeline.md names when it says a date can be normalised from prose.
func ordinalWords(toks []tok, j int) (int, int, bool) {
	j = skipWord(toks, j, "the")
	if j >= len(toks) || toks[j].kind != tokLetters {
		return 0, j, false
	}
	w := strings.ToLower(toks[j].s)
	if n, ok := ordinals[w]; ok {
		return n, j + 1, true
	}
	tens, ok := ordinalTens[w]
	if !ok {
		return 0, j, false
	}
	k := j + 1
	if k < len(toks) && toks[k].kind == tokOther && toks[k].s == "-" {
		k++
	}
	if k < len(toks) && toks[k].kind == tokLetters {
		if n, ok := ordinals[strings.ToLower(toks[k].s)]; ok && n <= 9 {
			return tens + n, k + 1, true
		}
	}
	if v, ok := ordinalTensAlone[w]; ok {
		return v, j + 1, true
	}
	return 0, j, false
}

var ordinals = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5,
	"sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
	"eleventh": 11, "twelfth": 12, "thirteenth": 13, "fourteenth": 14,
	"fifteenth": 15, "sixteenth": 16, "seventeenth": 17, "eighteenth": 18,
	"nineteenth": 19, "twentieth": 20, "thirtieth": 30,
}

var ordinalTens = map[string]int{"twenty": 20, "thirty": 30}

var ordinalTensAlone = map[string]int{"twentieth": 20, "thirtieth": 30}

var months = map[string]int{
	"january": 1, "jan": 1,
	"february": 2, "feb": 2,
	"march": 3, "mar": 3,
	"april": 4, "apr": 4,
	"may":  5,
	"june": 6, "jun": 6,
	"july": 7, "jul": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"october": 10, "oct": 10,
	"november": 11, "nov": 11,
	"december": 12, "dec": 12,
}

func monthOf(s string) (int, bool) {
	m, ok := months[strings.ToLower(s)]
	return m, ok
}

// expandYear turns a two-digit year into a four-digit one on the POSIX pivot:
// 69 and above is the twentieth century, 68 and below the twenty-first. A
// convention had to be chosen and this is the one every C library uses.
func expandYear(y int) int {
	if y >= 100 {
		return y
	}
	if y <= 68 {
		return y + 2000
	}
	return y + 1900
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func dedupe(in []date) []date {
	out := in[:0]
	for _, d := range in {
		seen := false
		for _, o := range out {
			if o.y == d.y && o.m == d.m && o.d == d.d {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, d)
		}
	}
	return out
}
