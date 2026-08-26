package compare

import (
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/validate"
)

// textEqual compares two values as strings, after the transformation
// docs/confidence.md §Comparison specifies: NFKC, whitespace collapse and case
// folding.
//
// That is what makes ACME LTD and Acme Ltd one string. It is deliberately not
// what makes Acme Ltd and Acme Limited one, because they are two companies as
// often as they are one and a reviewer is the right place to settle it.
func textEqual(a, b any, caseSensitive bool) (bool, bool) {
	x, okx := literal(a)
	y, oky := literal(b)
	if !okx || !oky {
		return false, false
	}
	return fold(x, caseSensitive) == fold(y, caseSensitive), true
}

// boolsEqual compares two values as booleans.
//
// Reading the text goes through [validate.ParseBool] so that "Yes", "true" and
// "checked" mean here exactly what they meant when the value was converted.
// Two answers to that question would report a disagreement between a reading
// that wrote Yes and one that wrote true, which is not a disagreement.
func boolsEqual(a, b any) (bool, bool) {
	x, okx := boolOf(a)
	y, oky := boolOf(b)
	if !okx || !oky {
		return false, false
	}
	return x == y, true
}

// boolOf reads a value as a boolean.
func boolOf(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, ok, _ := validate.ParseBool(canonical(t))
		return b, ok
	}
	rv, ok := deref(v)
	if !ok {
		return false, false
	}
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.String:
		b, ok, _ := validate.ParseBool(canonical(rv.String()))
		return b, ok
	default:
		return false, false
	}
}

// canonical applies ovrin's Unicode subset, collapses whitespace and trims.
//
// [normalise.Canonical] is the same transformation the document text went
// through, so a value compared through it is compared on the same terms the
// text was — and, usefully here, fullwidth digits, ligatures and the
// compatibility spaces are gone before anything tries to read a number out of
// it. The zero-width characters it leaves in place are dropped: they render as
// nothing, so two readings that differ only by one are not two values.
func canonical(s string) string {
	c, _ := normalise.Canonical(s)
	if !hasIgnorable(c) {
		return strings.TrimSpace(c)
	}
	var b strings.Builder
	b.Grow(len(c))
	for _, r := range c {
		if !normalise.Ignorable(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func hasIgnorable(s string) bool {
	for _, r := range s {
		if normalise.Ignorable(r) {
			return true
		}
	}
	return false
}

// fold reduces a string to the form strings are compared in.
//
// [canonical] plus case folding, which is the third step docs/confidence.md
// names. Case folding is not [strings.ToLower]: ß lower-cases to itself and
// folds to ss, and the handful of characters where the two differ are
// enumerated in [fullFold].
func fold(s string, caseSensitive bool) string {
	c := canonical(s)
	if caseSensitive {
		return c
	}
	var b strings.Builder
	b.Grow(len(c))
	for i := 0; i < len(c); {
		r, size := utf8.DecodeRuneInString(c[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteByte(c[i])
			i++
			continue
		}
		b.WriteString(foldRune(r))
		i += size
	}
	return b.String()
}

// fullFold are the case foldings that are not a single lower-case rune.
//
// It is internal/ground's table, and it is duplicated rather than shared
// because ground keeps it unexported and this package may not reach into it.
// The two must fold alike: a string that grounds against the document and then
// reports a disagreement with the other reading of the same text would be an
// incoherent answer, and the divergence would be invisible until a German
// document arrived. Hoisting the folding into a package both can import is the
// standing fix.
var fullFold = map[rune]string{
	0x00DF: "ss", // ß
	0x1E9E: "ss", // ẞ
	0x0130: "i̇", // İ
	0x01F0: "ǰ",
	0x1E96: "ẖ",
	0x1E97: "ẗ",
	0x1E98: "ẘ",
	0x1E99: "ẙ",
	0x1E9A: "aʾ",
	0x0149: "ʼn",
	0x0587: "եւ",
	0xFB13: "մն",
	0xFB14: "մե",
	0xFB15: "մի",
	0xFB16: "վն",
	0xFB17: "մխ",
}

// foldRune returns the case-folded form of one rune, matching
// internal/ground's foldRune for the reason given on [fullFold].
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

// literal returns a value's own text — what comparing it as a string looks at.
//
// A number formats without an exponent so that a reading carrying 25000 as a
// float64 and one carrying it as "25000" are the same text, which matters only
// on the fallback path but matters absolutely there.
func literal(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case time.Time:
		if isMidnight(t) {
			return t.Format("2006-01-02"), true
		}
		return t.Format(time.RFC3339Nano), true
	}
	rv, ok := deref(v)
	if !ok {
		return "", false
	}
	if rv.CanInterface() {
		switch t := rv.Interface().(type) {
		case string:
			return t, true
		case bool:
			return strconv.FormatBool(t), true
		case time.Time:
			return literal(t)
		}
	}
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), true
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), true
	default:
		return "", false
	}
}

// deref follows pointers and interfaces to the value underneath, and reports
// false for a nil anywhere along the way.
func deref(v any) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}, false
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return reflect.Value{}, false
	}
	return rv, true
}
