package eval

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// Equal reports whether an extracted value matches ground truth.
//
// Comparison is type-aware, matching the table in docs/confidence.md, because
// string equality would report a failure on nearly every correctly extracted
// formatted number. A corpus scored with == reports that ovrin cannot read an
// invoice total, when what it cannot do is print one the way the labeller did.
//
//	25,000  25000  25 000        one number
//	03/04/26  2026-04-03         one instant
//	ACME LTD  Acme Ltd           one string
//	Acme Ltd  Acme Limited       two strings, and a real disagreement
//
// want comes from ground truth and is a JSON value ([json.Number], string,
// bool, slice or map). got comes from [ovrin.FieldResult].Value and is
// whatever the schema's Go type produced.
func Equal(want, got any) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}

	// Dates first, and only when at least one side is already a time.Time or
	// both sides parse under an unambiguous layout. Deciding that "03/04/2026"
	// is a date would mean deciding which date, and this package refuses that
	// for the same reason the library does.
	if wt, ok := asTime(want); ok {
		if gt, ok := asTime(got); ok {
			return wt.Equal(gt)
		}
		return false
	} else if _, isTime := got.(time.Time); isTime {
		return false
	}

	if wb, ok := asBool(want); ok {
		gb, ok := asBool(got)
		return ok && wb == gb
	}

	// Numbers, when either side is a native number. Two strings are compared
	// as strings even if both happen to parse: an invoice number "007" is not
	// the invoice number "7", and the schema, not the characters, is what says
	// which a field is.
	if nativeNumber(want) || nativeNumber(got) {
		wn, wok := asRat(want)
		gn, gok := asRat(got)
		if !wok || !gok {
			return false
		}
		if isFloat(want) || isFloat(got) {
			// A float64 cannot hold 18430.55, so an exact rational comparison
			// against the decimal a labeller wrote would report a failure that
			// is the comparison's own rounding rather than the extractor's
			// mistake. Where one side has already been through binary floating
			// point, both sides are compared there.
			wf, _ := wn.Float64()
			gf, _ := gn.Float64()
			return nearlyEqual(wf, gf)
		}
		return wn.Cmp(gn) == 0
	}

	if ws, ok := want.([]any); ok {
		return equalSlice(ws, got)
	}
	if wm, ok := want.(map[string]any); ok {
		return equalMap(wm, got)
	}
	if reflect.ValueOf(want).Kind() == reflect.Slice || reflect.ValueOf(got).Kind() == reflect.Slice {
		return equalReflectSlice(want, got)
	}

	ws, wok := asString(want)
	gs, gok := asString(got)
	if !wok || !gok {
		return false
	}
	if wa, wc, ok := asMoney(ws); ok {
		if ga, gc, ok := asMoney(gs); ok {
			return wc == gc && wa.Cmp(ga) == 0
		}
		return false
	}
	return fold(ws) == fold(gs)
}

// isFloat reports whether v arrived as a binary floating-point value, and so
// has already lost whatever decimal digits it could not represent.
func isFloat(v any) bool {
	switch v.(type) {
	case float32, float64:
		return true
	}
	return false
}

// nearlyEqual compares two float64 values with a relative tolerance.
//
// The tolerance is nine significant figures, which is far tighter than any
// document's precision and far looser than the last bit of a float64. A
// difference this survives is a rounding artefact; a difference it does not is
// a misread digit.
func nearlyEqual(a, b float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff <= math.Max(1e-9, 1e-9*scale)
}

// nativeNumber reports whether v arrived as a number rather than as text.
func nativeNumber(v any) bool {
	switch v.(type) {
	case json.Number, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// asRat converts a numeric value, or text holding one, to an exact rational.
//
// Exact rather than float64 because these are money. Comparing 1463200.00 to
// 1463200 through binary floating point works until the day it does not, and
// the day it does not the harness reports an extraction failure that never
// happened.
func asRat(v any) (*big.Rat, bool) {
	switch t := v.(type) {
	case json.Number:
		return ratFromString(string(t))
	case float32:
		return new(big.Rat).SetFloat64(float64(t)), true
	case float64:
		r := new(big.Rat).SetFloat64(t)
		return r, r != nil
	case int:
		return new(big.Rat).SetInt64(int64(t)), true
	case int8:
		return new(big.Rat).SetInt64(int64(t)), true
	case int16:
		return new(big.Rat).SetInt64(int64(t)), true
	case int32:
		return new(big.Rat).SetInt64(int64(t)), true
	case int64:
		return new(big.Rat).SetInt64(t), true
	case uint:
		return new(big.Rat).SetUint64(uint64(t)), true
	case uint8:
		return new(big.Rat).SetUint64(uint64(t)), true
	case uint16:
		return new(big.Rat).SetUint64(uint64(t)), true
	case uint32:
		return new(big.Rat).SetUint64(uint64(t)), true
	case uint64:
		return new(big.Rat).SetUint64(t), true
	case string:
		return ratFromString(t)
	}
	return nil, false
}

// separators are the characters a document may put inside a number that carry
// no value: thousands grouping, currency symbols and the spaces typesetters
// use for grouping. The apostrophe forms are Swiss grouping; the space forms
// are what a normalised document's non-breaking spaces have become.
const separators = " ,'’   "

// ratFromString parses a written number, stripping grouping and currency
// marks. It refuses anything with a letter in it so that "INV-2026-0417" is
// never silently read as arithmetic.
func ratFromString(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var b strings.Builder
	digits := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits = true
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '+':
			b.WriteRune(r)
		case strings.ContainsRune(separators, r):
			// dropped
		case unicode.IsLetter(r):
			return nil, false
		case unicode.IsSymbol(r) || unicode.IsPunct(r):
			// A currency symbol or a stray mark. Percent is not stripped
			// silently: a percentage is a different quantity, so let SetString
			// refuse it.
			if r == '%' {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	if !digits {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(b.String())
	return r, ok
}

// asMoney splits "1,463,200.00 UGX" or "UGX 1,463,200.00" into an amount and
// an ISO 4217 code, so that 100 USD and 100 EUR are two values rather than
// one. It accepts only a three-letter alphabetic code, because anything looser
// starts matching ordinary prose.
func asMoney(s string) (*big.Rat, string, bool) {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) != 2 {
		return nil, "", false
	}
	code, num := f[0], f[1]
	if isCurrencyCode(f[1]) {
		code, num = f[1], f[0]
	} else if !isCurrencyCode(f[0]) {
		return nil, "", false
	}
	r, ok := ratFromString(num)
	if !ok {
		return nil, "", false
	}
	return r, strings.ToUpper(code), true
}

// isCurrencyCode reports whether s has the shape of an ISO 4217 alphabetic
// code. Shape only — membership of the active list is the library's job, not
// the scorer's.
func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}

// timeLayouts are the forms this package will read as a date.
//
// Deliberately unambiguous ones only. "03/04/2026" is 3 April or 4 March
// depending on where it was printed, and a scorer that picks one would mark a
// correct extraction wrong half the time in one hemisphere. Ground truth is
// written ISO for exactly this reason.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// asTime converts a value to an instant, reporting whether it is one.
func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, true
	case string:
		for _, layout := range timeLayouts {
			if p, err := time.Parse(layout, t); err == nil {
				return p, true
			}
		}
	}
	return time.Time{}, false
}

// asBool converts a value to a boolean. Only a real bool counts: "yes" is the
// library's business to interpret, and a scorer that also interpreted it would
// hide the cases where the library did not.
func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// asString converts a value to text, for the string comparison path.
func asString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return string(t), true
	case fmtStringer:
		return t.String(), true
	}
	return "", false
}

// fmtStringer is fmt.Stringer, named locally so this file does not import fmt
// for one interface.
type fmtStringer interface{ String() string }

// fold reduces a string to the form two readings of the same text share:
// ovrin's own canonicalisation, then case folding.
//
// [normalise.Canonical] is reused rather than reimplemented because it is the
// transformation document text has already been through. A second
// implementation would drift, and the first symptom would be the harness
// reporting a mismatch on two strings that are the same string.
//
// Full NFKC would need golang.org/x/text, which rule §4.1 forbids in this
// module. Canonical covers the compatibility cases documents actually contain
// — the space and dash and quote variants — and the gap is recorded here
// rather than papered over.
func fold(s string) string {
	c, _ := normalise.Canonical(s)
	return strings.ToLower(strings.TrimSpace(c))
}

// equalSlice compares ground truth's []any against whatever the extraction
// produced, element by element in order.
//
// Order matters, and that is a choice: line items are printed in an order and
// an extractor that reverses them has got something wrong. It also means one
// dropped item misaligns every item after it, which shows up as a low score on
// a document whose real fault is a single omission. The alternative — matching
// elements by best fit — hides that omission, so the pessimistic reading is
// the one kept.
func equalSlice(want []any, got any) bool {
	gv := reflect.ValueOf(got)
	if gv.Kind() != reflect.Slice && gv.Kind() != reflect.Array {
		return false
	}
	if gv.Len() != len(want) {
		return false
	}
	for i := range want {
		if !Equal(want[i], gv.Index(i).Interface()) {
			return false
		}
	}
	return true
}

// equalReflectSlice handles the case where ground truth is not []any but one
// of the sides is still a slice.
func equalReflectSlice(want, got any) bool {
	wv, gv := reflect.ValueOf(want), reflect.ValueOf(got)
	if wv.Kind() != gv.Kind() || wv.Len() != gv.Len() {
		return false
	}
	for i := 0; i < wv.Len(); i++ {
		if !Equal(wv.Index(i).Interface(), gv.Index(i).Interface()) {
			return false
		}
	}
	return true
}

// equalMap compares ground truth's object against an extracted map. Both key
// sets must match: an extra key is an invented member, which is the failure
// this harness exists to count.
func equalMap(want map[string]any, got any) bool {
	gm, ok := got.(map[string]any)
	if !ok {
		return false
	}
	if len(gm) != len(want) {
		return false
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		gv, ok := gm[k]
		if !ok || !Equal(want[k], gv) {
			return false
		}
	}
	return true
}
