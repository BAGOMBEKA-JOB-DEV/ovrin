package schema

import (
	"strconv"
	"strings"
	"unicode"
)

// IndexKey returns the key of element i of the slice whose key is sliceKey, so
// IndexKey("items", 0) is "items[0]".
//
// A [Schema] is built from a type and cannot know how many elements a document
// will yield, so a slice element's [Field.Key] carries an empty index —
// "items[]", "items[].unit_price" — and the index is filled in during
// extraction. The format lives here rather than at each call site because
// validate, ground and the result builder all have to agree on what a key looks
// like, and three separate fmt calls are three chances to disagree.
//
// A child of an element is rebased the same way: take IndexKey(f.Key, i) and
// append what follows f.Elem.Key in the child's key, so "items[].unit_price"
// becomes "items[0].unit_price".
func IndexKey(sliceKey string, i int) string {
	return sliceKey + "[" + strconv.Itoa(i) + "]"
}

// fieldKey is the [Field.Key] segment for a Go field name: lowercase, with
// multi-word names in snake case. UnitPrice is unit_price and VATRate is
// vat_rate. See docs/schema.md, "Field keys".
func fieldKey(goName string) string {
	return join(goName, "_")
}

// derivedDescription is the description for a tag whose first element is empty:
// the field name split on camel case and lowercased, so InvoiceNumber describes
// itself as "invoice number".
//
// It exists because the obvious fields — Number, Total, Currency — cost more to
// describe than the description is worth. It is deliberately worse than a
// written description for every other field, which is why it is opt-in.
func derivedDescription(goName string) string {
	return join(goName, " ")
}

// join splits a Go field name into words and joins them, lowercased, with sep.
func join(goName, sep string) string {
	w := words(goName)
	if len(w) == 0 {
		// Nothing splittable — a name of only separators. Lowercasing what we
		// were given beats returning an empty key that collides with the next
		// such field.
		return strings.ToLower(goName)
	}
	return strings.ToLower(strings.Join(w, sep))
}

// words splits a Go identifier on camel-case boundaries and underscores.
//
// The initialism case is the one that matters: VATRate has to split as
// VAT + Rate, not V + A + T + Rate, so a run of capitals ends one rune before
// the capital that starts a lowercase word.
func words(goName string) []string {
	runes := []rune(goName)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || unicode.IsSpace(r):
			flush()
		case unicode.IsUpper(r):
			if len(cur) > 0 {
				prev := cur[len(cur)-1]
				switch {
				case !unicode.IsUpper(prev):
					// lower-to-upper: Unit|Price, Line1|Item.
					flush()
				case i+1 < len(runes) && unicode.IsLower(runes[i+1]):
					// end of an initialism: VAT|Rate.
					flush()
				}
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}
