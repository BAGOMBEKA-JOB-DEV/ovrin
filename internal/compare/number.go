package compare

import (
	"math"
	"reflect"
	"strings"
	"unicode"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// epsilon is the relative tolerance two readings of a number are compared
// with.
//
// Not a tuning knob and deliberately not an option: it is there because a
// value that made a round trip through JSON is not bit-identical to the one a
// parser produced, and it is small enough that no difference a document could
// express survives it. A tolerance wide enough to hide a real disagreement
// would defeat the only check that catches 25,000 read as 2,500.
const epsilon = 1e-9

// numbersEqual compares two values as numbers.
//
// This is the row of the table that matters most. [validate.ParseNumber] is
// the reader — thousands separators, currency symbols, accounting parentheses
// and a trailing sign all resolve there — so that "25,000" is 25000 here for
// exactly the same reasons it is 25000 during conversion.
func numbersEqual(a, b any) (bool, bool) {
	x, okx := numberOf(a)
	y, oky := numberOf(b)
	if !okx || !oky {
		return false, false
	}
	return nearlyEqual(x, y), true
}

// numberOf reads a value as a number, accepting the text a model returns.
func numberOf(v any) (float64, bool) {
	switch t := v.(type) {
	case string:
		return parseNumber(t)
	case bool:
		return 0, false
	}
	rv, ok := deref(v)
	if !ok {
		return 0, false
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return finite(rv.Float())
	case reflect.String:
		return parseNumber(rv.String())
	default:
		return 0, false
	}
}

// parseNumber reads a formatted figure out of text.
//
// [canonical] runs first so that fullwidth digits and the compatibility spaces
// are ASCII by the time validate sees them; validate reads ASCII, and a
// document that prints ２５，０００ means twenty-five thousand.
func parseNumber(s string) (float64, bool) {
	n, ok, _ := validate.ParseNumber(canonical(s))
	if !ok {
		return 0, false
	}
	return finite(n)
}

// finite rejects the values that have no place in a document.
//
// A NaN is not equal to itself, which would make comparison non-reflexive, and
// an infinity is not a figure anybody printed. Both fall through to the text
// comparison, where two readings that produced the same nonsense still agree
// that they did.
func finite(f float64) (float64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// nearlyEqual compares two numbers with a relative tolerance.
//
// The same arithmetic internal/ground uses, duplicated because it is
// unexported there. Relative rather than absolute: an invoice total of
// 1,250,000.00 and a fixed absolute tolerance would either be too loose for a
// line item or too tight for the total.
func nearlyEqual(a, b float64) bool {
	if a == b {
		return true
	}
	scale := math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
	return math.Abs(a-b) <= epsilon*scale
}

// amountsEqual compares two values as currency amounts: same amount and same
// currency, both (docs/confidence.md §Comparison).
//
// A reading that carries no currency at all is compared on the amount alone.
// It has not contradicted the reading that kept the symbol — a model asked for
// a float64 has nowhere to put "USD" — and treating a missing currency as a
// conflicting one would flag every currency field whose schema is a number.
func amountsEqual(a, b any) (bool, bool) {
	x, cx, okx := amountOf(a)
	y, cy, oky := amountOf(b)
	if !okx || !oky {
		return false, false
	}
	if !nearlyEqual(x, y) {
		return false, true
	}
	if cx == "" || cy == "" {
		return true, true
	}
	return cx == cy, true
}

// amountOf splits a value into an amount and a currency code, which is empty
// when the value carries none.
func amountOf(v any) (float64, string, bool) {
	s, ok := textOf(v)
	if !ok {
		n, ok := numberOf(v)
		return n, "", ok
	}
	c := canonical(s)
	n, ok, _ := validate.ParseNumber(c)
	if !ok {
		return 0, "", false
	}
	if n, ok = finite(n); !ok {
		return 0, "", false
	}
	code, _ := currencyOf(c)
	return n, code, true
}

// textOf reports the value's text, and whether it had any: a currency written
// as a string is the only form that can carry a currency at all.
func textOf(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	rv, ok := deref(v)
	if !ok || rv.Kind() != reflect.String {
		return "", false
	}
	return rv.String(), true
}

// currencySymbol pairs a symbol as a document writes it with the code a schema
// carries it as.
type currencySymbol struct {
	symbol string
	code   string
}

// currencySymbols is internal/ground's table, longest symbol first.
//
// Duplicated for the reason [fullFold] is: ground keeps it unexported. Ordered
// rather than a map because map iteration order is random and "R$" contains
// "$": a randomly ordered scan would call the same Brazilian amount BRL on one
// run and USD on the next, and a comparison whose answer depends on the run is
// worse than a wrong one.
//
// A bare dollar sign is USD, as it is in ground. A document writing Canadian
// dollars as "$" therefore agrees with a USD reading; the alternative is
// failing to read the currency of every US invoice.
var currencySymbols = []currencySymbol{
	{"R$", "BRL"},
	{"$", "USD"},
	{"€", "EUR"},
	{"£", "GBP"},
	{"¥", "JPY"},
	{"₹", "INR"},
	{"₦", "NGN"},
	{"₩", "KRW"},
	{"₽", "RUB"},
	{"₪", "ILS"},
	{"₫", "VND"},
	{"₱", "PHP"},
	{"₺", "TRY"},
	{"₴", "UAH"},
	{"₡", "CRC"},
	{"₸", "KZT"},
}

// currencyOf finds the currency a value is written in: a symbol, or a run of
// three letters that is an active ISO 4217 code.
//
// The code is checked against the table rather than by shape, through
// [validate.NormaliseCurrency], so that the "Ltd" in a company name is not a
// currency and neither is the "kg" in a weight.
func currencyOf(s string) (string, bool) {
	for _, c := range currencySymbols {
		if strings.Contains(s, c.symbol) {
			return c.code, true
		}
	}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if code, ok, _ := validate.NormaliseCurrency(f); ok {
			return code, true
		}
	}
	return "", false
}
