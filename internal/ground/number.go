package ground

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// groupSeparators are the characters that separate thousands.
//
// The space forms are here because a normalised document has already turned
// every compatibility space into U+0020, so one entry covers the non-breaking
// space, the narrow no-break space and the thin space that typesetters
// actually use.
const groupSeparators = " '’"

// numTok is one numeric literal found in the text.
type numTok struct {
	span normalise.Span

	// vals holds every value the token can be read as. It has two entries
	// only for a token whose separator convention is genuinely ambiguous;
	// see [numberValues].
	vals []float64
}

// findNumber searches for a numeric token whose value equals want.
//
// This is what makes 25,000 and 25000 and 25 000 one number. A naive string
// search reports a false negative on nearly every formatted figure in every
// document, and a check that fires constantly is a check nobody reads.
func findNumber(doc *normalise.Result, want float64) (normalise.Span, bool) {
	for _, t := range scanNumbers(doc.Text) {
		if !acceptable(doc, t.span) {
			continue
		}
		for _, v := range t.vals {
			if nearlyEqual(v, want) {
				return t.span, true
			}
		}
	}
	return normalise.Span{}, false
}

// findNumberLiteral searches for a numeric token written exactly as the value
// was.
//
// Verbatim matching for a number goes through the token scanner rather than
// through a plain string search, so that "a number" means one thing in this
// package. A string search finds 25 inside 25,000 — a comma is not a letter,
// so an ordinary word boundary is satisfied — and grounding twenty-five
// against a page that says twenty-five thousand is the worst false positive
// available here.
func findNumberLiteral(doc *normalise.Result, lit string) (normalise.Span, bool) {
	for _, t := range scanNumbers(doc.Text) {
		if acceptable(doc, t.span) && doc.Text[t.span.Start:t.span.End] == lit {
			return t.span, true
		}
	}
	return normalise.Span{}, false
}

// nearlyEqual compares two numbers with a relative tolerance, because a value
// that made a round trip through JSON is not bit-identical to the one parsed
// out of the page.
func nearlyEqual(a, b float64) bool {
	if a == b {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	return math.Abs(a-b) <= 1e-9*scale
}

// scanNumbers returns every numeric literal in s, with its span.
//
// A literal is a run of digits, extended over separators that are themselves
// followed by digits. A space only continues a literal when exactly three
// digits follow it, so "25 000" is one number and "page 3 4" is two.
func scanNumbers(s string) []numTok {
	var out []numTok
	for i := 0; i < len(s); {
		if !isDigit(s[i]) {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		start := i
		// A digit run that a letter runs into is part of an identifier, not a
		// figure: the 4 of "A4" and the 19 of "COVID19" are not quantities.
		if before, size := utf8.DecodeLastRuneInString(s[:i]); size > 0 && (unicode.IsLetter(before) || before == '_') {
			for i < len(s) && isDigit(s[i]) {
				i++
			}
			continue
		}
		if start > 0 && (s[start-1] == '-' || s[start-1] == '+') {
			if start == 1 || !wordRune(rune(s[start-2])) {
				start--
			}
		}
		end := i
		for end < len(s) {
			if isDigit(s[end]) {
				end++
				continue
			}
			r, size := utf8.DecodeRuneInString(s[end:])
			next := end + size
			if (r == '.' || r == ',') && next < len(s) && isDigit(s[next]) {
				end = next
				continue
			}
			if strings.ContainsRune(groupSeparators, r) && groupOf3(s, next) {
				end = next
				continue
			}
			break
		}
		if vals := numberValues(s[start:end]); len(vals) > 0 {
			out = append(out, numTok{span: normalise.Span{Start: start, End: end}, vals: vals})
		}
		out = append(out, spaceParts(s, start, end)...)
		i = end
	}
	return out
}

// spaceParts returns the pieces of a space-separated literal as tokens in
// their own right, when reading it as one grouped number is not the only
// sensible reading.
//
// "25 000" is one number: a leading group of one or two digits followed by
// groups of three is how half of Europe writes twenty-five thousand, and
// nothing else is written that way. "100 200" is not: it is equally a table
// row with two figures in it, and normalisation has by then collapsed the
// column gap that would have told them apart. Both readings are offered, and
// a match against either grounds the value — the alternative is a signal that
// reads zero on every table in the corpus.
func spaceParts(s string, start, end int) []numTok {
	lit := s[start:end]
	if !strings.ContainsAny(lit, groupSeparators) {
		return nil
	}
	lead := 0
	for lead < len(lit) && isDigit(lit[lead]) {
		lead++
	}
	if lead <= 2 {
		return nil
	}
	var out []numTok
	at := 0
	for at < len(lit) {
		if strings.ContainsRune(groupSeparators, rune(lit[at])) {
			at++
			continue
		}
		j := at
		for j < len(lit) && !strings.ContainsRune(groupSeparators, rune(lit[j])) {
			j++
		}
		if vals := numberValues(lit[at:j]); len(vals) > 0 {
			out = append(out, numTok{
				span: normalise.Span{Start: start + at, End: start + j},
				vals: vals,
			})
		}
		at = j
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// groupOf3 reports whether s at i is exactly three digits followed by
// something that is not a digit.
func groupOf3(s string, i int) bool {
	if i+3 > len(s) {
		return false
	}
	for k := 0; k < 3; k++ {
		if !isDigit(s[i+k]) {
			return false
		}
	}
	return i+3 == len(s) || !isDigit(s[i+3])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// numberValues returns every value a numeric literal can be read as.
//
// The convention is documented because it has to be chosen and no choice is
// right everywhere:
//
//   - Both a dot and a comma present: the later one is the decimal point.
//     1,234.56 and 1.234,56 are the same number.
//   - More than one of the same separator: it groups. 1,234,567.
//   - A single separator with one, two or more than three digits after it: it
//     is a decimal point. 25,50 is twenty-five and a half.
//   - A single separator with exactly three digits after it: genuinely
//     ambiguous, so both readings are returned and a match against either
//     counts. 1,234 is 1234 to an English typesetter and 1.234 to a German
//     one, and this package is not entitled to guess.
//
// The one exception to that last rule: when the three digits are all zero the
// decimal reading is dropped. Keeping it would make 25,000 ground the value
// 25, which is the false positive with the worst consequences here — it turns
// the signal that catches an invented figure into one that confirms it.
func numberValues(lit string) []float64 {
	s := lit
	for _, r := range groupSeparators {
		s = strings.ReplaceAll(s, string(r), "")
	}
	if s == "" {
		return nil
	}
	lastDot := strings.LastIndexByte(s, '.')
	lastComma := strings.LastIndexByte(s, ',')
	dots := strings.Count(s, ".")
	commas := strings.Count(s, ",")

	switch {
	case lastDot >= 0 && lastComma >= 0:
		if lastDot > lastComma {
			return one(parseWith(s, '.', ','))
		}
		return one(parseWith(s, ',', '.'))
	case lastDot >= 0:
		return single(s, '.', ',', lastDot, dots)
	case lastComma >= 0:
		return single(s, ',', '.', lastComma, commas)
	default:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		return []float64{v}
	}
}

// single resolves a literal carrying exactly one kind of separator.
func single(s string, sep, other byte, last, count int) []float64 {
	after := len(s) - last - 1
	if count > 1 || (after == 3 && allZero(s[last+1:])) {
		return one(parseWith(s, other, sep))
	}
	if after != 3 {
		return one(parseWith(s, sep, other))
	}
	out := one(parseWith(s, other, sep)) // grouped
	if v, ok := parseWith(s, sep, other); ok {
		out = append(out, v)
	}
	return out
}

func allZero(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return len(s) > 0
}

// parseWith reads s treating dec as the decimal point and grp as the group
// separator.
func parseWith(s string, dec, grp byte) (float64, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch byte(r) {
		case grp:
			return -1
		case dec:
			return '.'
		}
		return r
	}, s)
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func one(v float64, ok bool) []float64 {
	if !ok {
		return nil
	}
	return []float64{v}
}

// parseNumber reads a single numeric literal, for a value the model returned
// as text.
func parseNumber(s string) (float64, bool) {
	canon, _ := normalise.Canonical(s)
	toks := scanNumbers(canon)
	if len(toks) != 1 || len(toks[0].vals) == 0 {
		return 0, false
	}
	return toks[0].vals[0], true
}

// currencySymbols maps the symbols a document writes an amount with to the
// code a schema carries it as.
//
// A bare dollar sign is taken as USD. A document that writes Canadian dollars
// as "$" will therefore ground a USD value, which is a false positive this
// package accepts: the alternative is failing to ground every US invoice, and
// currency confusion is caught by the format check rather than by this one.
var currencySymbols = map[string]string{
	"$":  "USD",
	"€":  "EUR",
	"£":  "GBP",
	"¥":  "JPY",
	"₹":  "INR",
	"₦":  "NGN",
	"₩":  "KRW",
	"₽":  "RUB",
	"₪":  "ILS",
	"₫":  "VND",
	"₱":  "PHP",
	"₺":  "TRY",
	"₴":  "UAH",
	"₡":  "CRC",
	"₸":  "KZT",
	"R$": "BRL",
}

// currencyWindow is how far from the amount a currency marker may sit. Long
// enough for "USD 1,234.56" and for "1 234,56 EUR", short enough that the
// next sentence does not supply it.
const currencyWindow = 8

// findCurrency searches for an amount carrying the same currency.
//
// Same amount and same currency, both: 100 USD and 100 EUR are not one value
// (docs/confidence.md, Comparison). A value with no currency in it degrades
// to a plain numeric search rather than failing, because a schema is free to
// declare format=currency on a field the model returned as a bare number.
func findCurrency(doc *normalise.Result, lit string) (normalise.Span, bool) {
	amount, code, ok := parseCurrency(lit)
	if !ok {
		return normalise.Span{}, false
	}
	if code == "" {
		return findNumber(doc, amount)
	}
	for _, t := range scanNumbers(doc.Text) {
		if !acceptable(doc, t.span) {
			continue
		}
		matched := false
		for _, v := range t.vals {
			if nearlyEqual(v, amount) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if sp, ok := currencyNear(doc.Text, t.span, code); ok {
			return sp, true
		}
	}
	return normalise.Span{}, false
}

// currencyNear looks for the currency on either side of an amount and returns
// the span covering both.
func currencyNear(s string, amount normalise.Span, code string) (normalise.Span, bool) {
	before := amount.Start - currencyWindow
	if before < 0 {
		before = 0
	}
	after := amount.End + currencyWindow
	if after > len(s) {
		after = len(s)
	}
	if sp, ok := currencyIn(s[before:amount.Start], code); ok {
		return normalise.Span{Start: before + sp.Start, End: amount.End}, true
	}
	if sp, ok := currencyIn(s[amount.End:after], code); ok {
		return normalise.Span{Start: amount.Start, End: amount.End + sp.End}, true
	}
	return normalise.Span{}, false
}

// currencyIn finds the code, or a symbol equivalent to it, inside a window.
func currencyIn(window, code string) (normalise.Span, bool) {
	if i := strings.Index(strings.ToUpper(window), code); i >= 0 {
		return normalise.Span{Start: i, End: i + len(code)}, true
	}
	for sym, c := range currencySymbols {
		if c != code {
			continue
		}
		if i := strings.Index(window, sym); i >= 0 {
			return normalise.Span{Start: i, End: i + len(sym)}, true
		}
	}
	return normalise.Span{}, false
}

// parseCurrency splits a value such as "1,234.56 USD" or "€1 234,56" into an
// amount and a currency code. The code is empty when the value carries none.
func parseCurrency(lit string) (float64, string, bool) {
	canon, _ := normalise.Canonical(lit)
	toks := scanNumbers(canon)
	if len(toks) == 0 || len(toks[0].vals) == 0 {
		return 0, "", false
	}
	amount := toks[0].vals[0]

	rest := canon[:toks[0].span.Start] + " " + canon[toks[0].span.End:]
	if code, ok := currencyCode(rest); ok {
		return amount, code, true
	}
	return amount, "", true
}

// currencyCode finds a currency marker in text: a symbol, or a bare run of
// three ASCII letters, which is what an ISO 4217 code looks like and what a
// schema carries one as.
func currencyCode(s string) (string, bool) {
	for sym, code := range currencySymbols {
		if strings.Contains(s, sym) {
			return code, true
		}
	}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(f) == 3 && isASCIILetters(f) {
			return strings.ToUpper(f), true
		}
	}
	return "", false
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i] | 0x20
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
