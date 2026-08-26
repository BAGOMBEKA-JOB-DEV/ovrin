package validate

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// timeType is the one concrete type the time kind maps to.
var timeType = reflect.TypeOf(time.Time{})

// conversion is the outcome of turning a raw value into a field's Go type.
//
// Unexported because it is the internal shape of a step whose public form is
// [Result]; keeping it here means adding a step never changes the API.
type conversion struct {
	value     any
	ok        bool
	message   string
	ambiguity *DateAmbiguity
}

// fail returns a conversion that produced no value.
//
// There is no "failed with a zero value" case on purpose: the zero value never
// leaves this package (docs/rules.md §8.5).
func fail(msg string) conversion { return conversion{message: msg} }

// convert turns raw into f's Go type, applying the declared format first.
//
// Format normalisation runs before type conversion because the two are the same
// step for a dated field — parsing "3 April 2026" is both — and running them in
// the other order would mean parsing the text twice with different rules.
func (v *Validator) convert(f schema.Field, raw any, format string) conversion {
	target := targetType(f)

	if format != "" {
		return v.formatted(f, raw, format, target)
	}

	switch f.Kind {
	case schema.KindString:
		return store(target, rawText(raw))

	case schema.KindInt, schema.KindFloat:
		n, ok, msg := number(raw)
		if !ok {
			return fail(msg)
		}
		return store(target, n)

	case schema.KindBool:
		b, ok, msg := boolean(raw)
		if !ok {
			return fail(msg)
		}
		return store(target, b)

	case schema.KindTime:
		// docs/schema.md requires format=date or format=datetime on a
		// time.Time, and the schema package rejects one without. Parsing
		// anyway rather than refusing means a schema bug costs a lowered
		// signal instead of a lost value.
		return v.dated(raw, false)

	case schema.KindArray, schema.KindObject:
		// Composites are assembled by the caller: only it holds the results
		// for the nested fields. Their own rules — required, min, max — are
		// still evaluated here.
		return conversion{}
	}
	return fail("field has no extractable kind")
}

// formatted normalises raw to the declared format and stores it in the field's
// type.
func (v *Validator) formatted(f schema.Field, raw any, format string, target reflect.Type) conversion {
	switch format {
	case schema.FormatDate, schema.FormatDatetime:
		c := v.dated(raw, format == schema.FormatDate)
		if !c.ok || target == nil || target == timeType {
			return c
		}
		// A string field may declare a date format. RFC 3339 is the
		// normalisation, because a date normalised to a locale's spelling is
		// not normalised.
		t, isTime := c.value.(time.Time)
		if !isTime {
			return c
		}
		if format == schema.FormatDate {
			return store(target, t.Format(dateLayout))
		}
		return store(target, t.Format(time.RFC3339))
	}

	text := rawText(raw)
	var out string
	var ok bool
	var msg string
	switch format {
	case schema.FormatEmail:
		out, ok, msg = NormaliseEmail(text)
	case schema.FormatPhone:
		out, ok, msg = NormalisePhone(text)
	case schema.FormatCurrency:
		out, ok, msg = NormaliseCurrency(text)
	case schema.FormatIBAN:
		out, ok, msg = NormaliseIBAN(text)
	case schema.FormatSWIFT:
		out, ok, msg = NormaliseSWIFT(text)
	case schema.FormatUUID:
		out, ok, msg = NormaliseUUID(text)
	default:
		// The schema package rejects an unknown format before extraction.
		return fail("unknown format")
	}
	if !ok {
		return fail(msg)
	}
	if f.Kind != schema.KindString {
		return fail("this format produces text, which does not fit the field's type")
	}
	return store(target, out)
}

// dated parses a date or datetime, honouring the configured date order.
func (v *Validator) dated(raw any, dateOnly bool) conversion {
	if t, ok := raw.(time.Time); ok {
		if dateOnly {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		return conversion{value: t, ok: true}
	}
	var p DateParse
	if dateOnly {
		p = ParseDate(rawText(raw), v.dateOrder)
	} else {
		p = ParseDateTime(rawText(raw), v.dateOrder)
	}
	if p.Ambiguity != nil {
		return conversion{message: p.Reason, ambiguity: p.Ambiguity}
	}
	if !p.OK {
		return fail(p.Reason)
	}
	return conversion{value: p.Time, ok: true}
}

// targetType returns the concrete type to convert into, with any pointer
// indirection removed.
//
// The pointer is the caller's business: it decides whether to take the address,
// because it is the one writing into the destination struct. A field with no
// reflect.Type — which happens only in tests — falls back to the obvious type
// for its kind rather than refusing to convert.
func targetType(f schema.Field) reflect.Type {
	t := f.Type
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t != nil {
		return t
	}
	switch f.Kind {
	case schema.KindString:
		return reflect.TypeOf("")
	case schema.KindInt:
		return reflect.TypeOf(int(0))
	case schema.KindFloat:
		return reflect.TypeOf(float64(0))
	case schema.KindBool:
		return reflect.TypeOf(false)
	case schema.KindTime:
		return timeType
	}
	return nil
}

// store converts a parsed value into the field's declared type.
//
// It is where an integer field rejects 2.5 and where a value too large for an
// int8 is refused rather than wrapped. Silent wrapping is the worst available
// outcome: it produces a plausible number that is wrong.
func store(target reflect.Type, parsed any) conversion {
	if target == nil {
		return conversion{value: parsed, ok: true}
	}
	out := reflect.New(target).Elem()

	switch target.Kind() {
	case reflect.String:
		s, ok := parsed.(string)
		if !ok {
			return fail("value is not text")
		}
		out.SetString(s)

	case reflect.Bool:
		b, ok := parsed.(bool)
		if !ok {
			return fail("value is not a yes or no")
		}
		out.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := parsed.(float64)
		if !ok {
			return fail("value is not a number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return fail("value is not a whole number")
		}
		if n < math.MinInt64 || n >= math.MaxInt64 || out.OverflowInt(int64(n)) {
			return fail("value is out of range for this field's type")
		}
		out.SetInt(int64(n))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := parsed.(float64)
		if !ok {
			return fail("value is not a number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return fail("value is not a whole number")
		}
		if n < 0 {
			return fail("value is negative and this field cannot hold a negative number")
		}
		if n >= math.MaxUint64 || out.OverflowUint(uint64(n)) {
			return fail("value is out of range for this field's type")
		}
		out.SetUint(uint64(n))

	case reflect.Float32, reflect.Float64:
		n, ok := parsed.(float64)
		if !ok {
			return fail("value is not a number")
		}
		if out.OverflowFloat(n) {
			return fail("value is out of range for this field's type")
		}
		out.SetFloat(n)

	default:
		if target == timeType {
			t, ok := parsed.(time.Time)
			if !ok {
				return fail("value is not a date")
			}
			out.Set(reflect.ValueOf(t))
			break
		}
		return fail("field's type cannot hold a value of this kind")
	}
	return conversion{value: out.Interface(), ok: true}
}

// rawText renders a raw extracted value as the text a reviewer would recognise.
//
// A number arrives from JSON as a float64, and formatting it with %v would give
// 1.2345e+06 for a total. Reviewers do not read scientific notation.
func rawText(raw any) string {
	switch t := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	case time.Time:
		return t.Format(time.RFC3339)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	}
	v := reflect.ValueOf(raw)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		// A composite's text form is the caller's to build from its members.
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

// number turns a raw value into a float64.
func number(raw any) (float64, bool, string) {
	switch t := raw.(type) {
	case float64:
		return t, true, ""
	case float32:
		return float64(t), true, ""
	case json.Number:
		n, err := t.Float64()
		if err != nil {
			return 0, false, "not a number"
		}
		return n, true, ""
	case bool:
		return 0, false, "a yes or no is not a number"
	case string:
		return ParseNumber(t)
	}
	if n, ok := numeric(raw); ok {
		return n, true, ""
	}
	return 0, false, "not a number"
}

// numeric reads any Go numeric value as a float64.
func numeric(v any) (float64, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0, false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	}
	return 0, false
}

// boolean turns a raw value into a bool.
//
// The accepted spellings are exactly those docs/schema.md lists, plus a native
// bool. "1" and "0" are deliberately not accepted: a document that prints 1 in
// a box may mean "yes" or may mean "one", and a value sent to review costs less
// than a tick nobody made.
func boolean(raw any) (bool, bool, string) {
	if b, ok := raw.(bool); ok {
		return b, true, ""
	}
	return ParseBool(rawText(raw))
}

// ParseBool reads the affirmative and negative spellings a form uses.
//
// Exported because comparison of two readings needs the same reading of "Yes"
// and "true" that conversion used; two different answers to that question would
// flag a disagreement that does not exist.
func ParseBool(s string) (bool, bool, string) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "true", "checked":
		return true, true, ""
	case "no", "n", "false", "unchecked":
		return false, true, ""
	}
	return false, false, "not a yes or no"
}

// ParseNumber reads a number as a document prints one: with thousands
// separators, a currency symbol or code, and accounting parentheses for a
// negative.
//
// Exported for the same reason as [ParseBool]: 25,000 and 25000 must be one
// number everywhere, or comparison flags a disagreement that is a formatting
// difference (docs/confidence.md §Comparison).
func ParseNumber(s string) (float64, bool, string) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false, "not a number"
	}

	negative := false
	// Accounting notation: (1,234.56) is a negative amount.
	if len(t) >= 2 && t[0] == '(' && t[len(t)-1] == ')' {
		negative = true
		t = t[1 : len(t)-1]
	}

	t = stripCurrency(t)

	// A sign may sit on either end: -1,234.00 and 1,234.00- both occur.
	for _, sign := range []string{"-", "−", "–"} {
		if strings.HasPrefix(t, sign) {
			negative, t = !negative, strings.TrimSpace(t[len(sign):])
			break
		}
		if strings.HasSuffix(t, sign) {
			negative, t = !negative, strings.TrimSpace(t[:len(t)-len(sign)])
			break
		}
	}
	t = strings.TrimPrefix(t, "+")
	t = stripCurrency(t)

	digits, ok := decimalDigits(t)
	if !ok {
		return 0, false, "not a number"
	}
	n, err := strconv.ParseFloat(digits, 64)
	if err != nil {
		return 0, false, "not a number"
	}
	if math.IsInf(n, 0) {
		return 0, false, "number is too large"
	}
	if negative {
		n = -n
	}
	return n, true, ""
}

// stripCurrency removes currency symbols, an ISO code or a short unit written
// beside the number, and the space separators that come with them.
func stripCurrency(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Sc, r):
			return -1
		case r == '\u00a0' || r == '\u202f' || r == '\u2009' || r == '\'':
			// Non-breaking, narrow and thin spaces, and the Swiss apostrophe,
			// are thousands separators. They are never decimal points.
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)

	// A code or unit beside the number: "UGX 1,200", "1 200 EUR", "1.5 kg".
	for _, trimLeading := range []bool{true, false} {
		letters := 0
		if trimLeading {
			for _, r := range s {
				if !unicode.IsLetter(r) {
					break
				}
				letters++
			}
			if letters > 0 && letters <= 4 && letters < len(s) {
				s = strings.TrimSpace(s[letters:])
			}
			continue
		}
		runes := []rune(s)
		for i := len(runes) - 1; i >= 0; i-- {
			if !unicode.IsLetter(runes[i]) {
				break
			}
			letters++
		}
		if letters > 0 && letters <= 4 && letters < len(runes) {
			s = strings.TrimSpace(string(runes[:len(runes)-letters]))
		}
	}
	return strings.TrimSpace(s)
}

// decimalDigits resolves the grouping and decimal separators and returns a
// plain decimal string.
//
// The hard case is one separator with three digits after it: 1,234 is a
// thousand and 1.234 is a thousand in half of Europe, but 0.500 is a half. The
// rule is that a grouped number's first group is never a bare zero, which
// resolves every real case except a three-decimal-place value between 1 and
// 999 — 1.500 litres reads as 1500. That is a genuine ambiguity in the world's
// notation and it is documented rather than guessed around.
func decimalDigits(s string) (string, bool) {
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return "", false
	}

	lastComma := strings.LastIndexByte(s, ',')
	lastDot := strings.LastIndexByte(s, '.')

	var decimal byte
	switch {
	case lastComma >= 0 && lastDot >= 0:
		// Whichever comes last is the decimal point: 1.234,56 and 1,234.56.
		if lastComma > lastDot {
			decimal = ','
		} else {
			decimal = '.'
		}
	case lastComma >= 0 || lastDot >= 0:
		sep := byte('.')
		at := lastDot
		if lastComma >= 0 {
			sep, at = ',', lastComma
		}
		switch {
		case strings.Count(s, string(sep)) > 1:
			// 1.234.567 can only be grouping.
		case len(s)-at-1 != 3:
			decimal = sep
		case at == 0 || s[:at] == "0":
			// ".500" and "0.500" are fractions, not groups.
			decimal = sep
		}
	}

	var b strings.Builder
	seenDecimal := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		case decimal != 0 && c == decimal && i == strings.LastIndexByte(s, decimal):
			if seenDecimal {
				return "", false
			}
			seenDecimal = true
			b.WriteByte('.')
		case c == ',' || c == '.':
			// A grouping separator. It must separate groups of three, or the
			// text is not a grouped number at all.
			if !groupBoundary(s, i, decimal) {
				return "", false
			}
		default:
			return "", false
		}
	}
	out := b.String()
	if out == "" || out == "." {
		return "", false
	}
	return out, true
}

// groupBoundary reports whether the separator at i sits where a grouping
// separator can sit.
//
// Groups of three, and a group of two where another separator follows it: the
// South Asian system writes a hundred thousand as 1,00,000 and a document from
// Kampala or Mumbai is as likely as one from Berlin. A run that is neither
// means the text is not a grouped number at all, which is how 1,2,3 is refused
// rather than read as 123.
func groupBoundary(s string, i int, decimal byte) bool {
	if i == 0 || !isDigit(s[i-1]) {
		return false
	}
	end := len(s)
	if decimal != 0 {
		if d := strings.LastIndexByte(s, decimal); d > i {
			end = d
		}
	}
	run := 0
	for j := i + 1; j < end && isDigit(s[j]); j++ {
		run++
	}
	next := i + 1 + run
	switch run {
	case 3:
		return next == end || s[next] == ',' || s[next] == '.'
	case 2:
		return next < end && (s[next] == ',' || s[next] == '.')
	}
	return false
}
