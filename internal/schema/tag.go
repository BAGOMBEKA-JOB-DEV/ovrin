package schema

import (
	"strconv"
	"strings"
)

// TagSkip is the tag value that excludes a field from the schema, spelled the
// same as [encoding/json]'s so it needs no explanation.
//
// A field with no `ovrin` tag at all is excluded too; TagSkip exists for the
// field that would otherwise look like an oversight.
const TagSkip = "-"

// The closed format vocabulary, the value side of `format=`.
//
// Closed for the same reason the rule vocabulary is: an unrecognised format is
// a typo, and a typo that silently disables a check is worse than one that
// fails to compile a schema. Each value's parsing and normalisation is
// specified in docs/schema.md, "format".
const (
	// FormatDate normalises to a time.Time at midnight UTC.
	FormatDate = "date"

	// FormatDatetime normalises to a time.Time with a time of day.
	FormatDatetime = "datetime"

	// FormatEmail normalises to a lowercased RFC 5322 addressable form.
	FormatEmail = "email"

	// FormatPhone normalises to E.164 where a country can be determined.
	FormatPhone = "phone"

	// FormatCurrency normalises to an uppercased ISO 4217 code.
	FormatCurrency = "currency"

	// FormatIBAN normalises to uppercase with spaces removed, checksum checked.
	FormatIBAN = "iban"

	// FormatSWIFT normalises to uppercase with spaces removed.
	FormatSWIFT = "swift"

	// FormatUUID normalises to the lowercased hyphenated RFC 4122 form.
	FormatUUID = "uuid"
)

// knownFormat reports whether v is in the format vocabulary.
func knownFormat(v string) bool {
	switch v {
	case FormatDate, FormatDatetime, FormatEmail, FormatPhone,
		FormatCurrency, FormatIBAN, FormatSWIFT, FormatUUID:
		return true
	}
	return false
}

// parseTag splits one `ovrin` tag value into its description and its rules.
//
// goPath is the Go path of the field — "Invoice.Vendor.Name" — and appears in
// every error, because the developer reading the error is looking at their own
// source and not at a key path.
//
// Only the grammar and the type-independent shape of each rule are checked
// here. Whether a rule can apply to the field's type is checked by checkRules,
// which needs a [Kind] this function cannot see.
func parseTag(goPath, tag string) (description string, rules []Rule, err error) {
	elems := splitTag(tag)

	// Surrounding space in a description is invisible in source and useless in
	// a prompt; trimming it also means `ovrin:" ,required"` derives a
	// description rather than sending the model a space.
	description = strings.TrimSpace(elems[0])

	for _, elem := range elems[1:] {
		name, value, hasValue := strings.Cut(elem, "=")
		switch name {
		case RuleRequired:
			if hasValue {
				return "", nil, errf("rule required on field %s takes no value", goPath)
			}
			rules = append(rules, Rule{Name: name})
		case RuleMin, RuleMax, RuleFormat:
			if !hasValue || value == "" {
				return "", nil, errf("rule %s on field %s needs a value", name, goPath)
			}
			rules = append(rules, Rule{Name: name, Value: value})
		case RuleEnum:
			if !hasValue || value == "" {
				return "", nil, errf("rule enum on field %s needs at least one alternative", goPath)
			}
			for _, alt := range strings.Split(value, "|") {
				if alt == "" {
					return "", nil, errf("rule enum on field %s has an empty alternative", goPath)
				}
			}
			rules = append(rules, Rule{Name: name, Value: value})
		default:
			// The whole element, quoted, so that the leading space in
			// `ovrin:"name, address"` is visible in the error rather than
			// leaving the author staring at a rule name that looks correct.
			return "", nil, errf("unknown rule %q on field %s", elem, goPath)
		}
	}
	return description, rules, nil
}

// splitTag splits a tag value on unescaped commas, resolving the two escapes as
// it goes: `\,` is a comma inside an element and `\\` is a backslash.
//
// A backslash before anything else is a literal backslash, so a description
// mentioning a Windows path survives rather than becoming a parse error about
// an escape nobody was trying to write.
//
// The result always has at least one element, so callers can index [0] for the
// description without a length check.
func splitTag(tag string) []string {
	var (
		elems []string
		b     strings.Builder
	)
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		switch {
		case c == '\\' && i+1 < len(tag) && (tag[i+1] == ',' || tag[i+1] == '\\'):
			b.WriteByte(tag[i+1])
			i++
		case c == ',':
			elems = append(elems, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(elems, b.String())
}

// checkRules reports whether every rule on f can apply to f's kind, and whether
// each rule's value is the shape that kind needs.
//
// This is where "min on a bool" is caught. A rule that cannot apply is an error
// and never a silent no-op: a schema that quietly does not enforce what it
// appears to enforce is the failure mode this vocabulary exists to prevent
// (docs/rules.md §6.1).
func checkRules(goPath string, f *Field) error {
	formats := 0
	for _, r := range f.Rules {
		switch r.Name {
		case RuleRequired:
			// Applies to every kind: absence is meaningful everywhere.
		case RuleMin, RuleMax:
			switch f.Kind {
			case KindInt, KindFloat:
				if _, err := strconv.ParseFloat(r.Value, 64); err != nil {
					return errf("rule %s on field %s needs a number", r.Name, goPath)
				}
			case KindString, KindArray:
				if _, err := strconv.ParseUint(r.Value, 10, 64); err != nil {
					return errf("rule %s on field %s needs a whole number of zero or more, because on a %s it bounds the length",
						r.Name, goPath, f.Kind)
				}
			default:
				return inapplicable(r.Name, goPath, f.Type)
			}
		case RuleFormat:
			formats++
			switch f.Kind {
			case KindString:
				if !knownFormat(r.Value) {
					return errf("unknown format %q on field %s", r.Value, goPath)
				}
			case KindTime:
				if r.Value != FormatDate && r.Value != FormatDatetime {
					return errf("format %q on field %s of type %s must be %s or %s",
						r.Value, goPath, f.Type, FormatDate, FormatDatetime)
				}
			default:
				return inapplicable(r.Name, goPath, f.Type)
			}
		case RuleEnum:
			if f.Kind != KindString {
				return inapplicable(r.Name, goPath, f.Type)
			}
		}
	}

	// Two formats contradict each other and one of them would be silently
	// ignored. Two mins do not: both hold, so both are kept.
	if formats > 1 {
		return errf("field %s has more than one format rule", goPath)
	}

	// A time.Time with no format has no defined parse: 03/04/2026 is two
	// different days and nothing downstream may guess (docs/schema.md, Types).
	if f.Kind == KindTime && formats == 0 {
		return errf("field %s of type %s needs format=%s or format=%s",
			goPath, f.Type, FormatDate, FormatDatetime)
	}
	return nil
}
