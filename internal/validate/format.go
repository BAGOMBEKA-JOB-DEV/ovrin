package validate

import (
	"net/mail"
	"strings"
	"unicode"
)

// The eight formats a tag may declare are defined by the schema package, and
// implemented here: schema owns what a tag may legally say, this package owns
// what the value must then be. One vocabulary, two responsibilities, and no
// second list to drift out of step with the first.

// maxEmailText bounds what is handed to the address parser. RFC 5321 caps an
// address at 254 characters; the rest of the allowance is for a display name.
const maxEmailText = 1024

// NormaliseEmail returns the address lowercased, and reports whether the text
// is an addressable RFC 5322 address.
//
// A display name is accepted and discarded — "Amina Nakato <a@example.com>" is
// a form documents print — because the address is what the schema asked for.
// The whole address is lowercased, as docs/schema.md specifies, even though
// only the domain is formally case-insensitive: a local part that differs by
// case is a mailbox nobody deliberately runs.
func NormaliseEmail(s string) (string, bool, string) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmailText {
		// Bounded before parsing: every document is hostile until proven
		// otherwise (docs/rules.md §7.1), and nothing an address parser could
		// find beyond a kilobyte is an address.
		return "", false, "not a valid email address"
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return "", false, "not a valid email address"
	}
	at := strings.LastIndexByte(addr.Address, '@')
	if at <= 0 || at == len(addr.Address)-1 {
		return "", false, "not a valid email address"
	}
	local, domain := addr.Address[:at], addr.Address[at+1:]
	if len(local) > 64 || len(addr.Address) > 254 {
		return "", false, "email address is too long to be deliverable"
	}
	if !deliverableDomain(domain) {
		return "", false, "the email address has no deliverable domain"
	}
	return strings.ToLower(addr.Address), true, ""
}

// deliverableDomain reports whether a domain could name a real host.
//
// net/mail accepts "user@localhost", which is valid RFC 5322 and useless on an
// invoice. Requiring a dotted name with a non-numeric last label rejects that
// without rejecting anything a document plausibly contains.
func deliverableDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 || d[0] == '[' {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !alnum && c != '-' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	if len(last) < 2 {
		return false
	}
	for i := 0; i < len(last); i++ {
		if last[i] >= '0' && last[i] <= '9' {
			return false
		}
	}
	return true
}

// NormalisePhone returns the number in E.164 where a country can be determined,
// and the digits alone where it cannot.
//
// A country can be determined only from the number itself: a leading + or an
// international prefix of 00. A bare national number — 0771 234 567 — could be
// dialled in any of two hundred countries, and ovrin has no way to know which
// document it came from, so it is normalised to its digits and left national.
// Inventing a country code would be exactly the kind of plausible fabrication
// rule §8.5 forbids.
func NormalisePhone(s string) (string, bool, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false, "not a valid telephone number"
	}

	// 00 is the international access prefix in most of the world; 011 in North
	// America. Only 00 is treated as one: 011 is indistinguishable from a
	// national number that happens to start that way.
	international := false
	switch {
	case strings.HasPrefix(s, "+"):
		international, s = true, s[1:]
	case strings.HasPrefix(s, "00") && len(s) > 2:
		international, s = true, s[2:]
	}

	if international {
		// A parenthesised zero is the national trunk prefix, printed exactly
		// to say it is dropped when dialling from abroad. Removing it is the
		// notation's own instruction, not a guess about the number.
		s = strings.NewReplacer("(0)", "", "(0 )", "", "( 0)", "", "( 0 )", "").Replace(s)
	}

	var digits strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.' || r == '/':
		case r == '\u00a0' || r == '\u2009' || r == '\u202f':
		case r == '\u2013' || r == '\u2014' || r == '\u2212':
		default:
			// A letter is an extension, a vanity number or a stray word. None
			// of them can be represented in E.164.
			return "", false, "not a valid telephone number"
		}
	}
	d := digits.String()

	if len(d) < 7 || len(d) > 15 {
		// E.164 caps a number at 15 digits; below 7 nothing is dialable.
		return "", false, "not a valid telephone number"
	}
	if international {
		if d[0] == '0' {
			// A country calling code never starts with zero.
			return "", false, "not a valid telephone number"
		}
		if len(d) < 8 {
			return "", false, "not a valid telephone number"
		}
		return "+" + d, true, ""
	}
	return d, true, ""
}

// NormaliseCurrency returns the code uppercased, and reports whether it is an
// ISO 4217 code.
//
// The list is checked rather than the shape: "XYZ" has the shape of a currency
// code and buys nothing. Codes withdrawn by ISO are not accepted, so a document
// printing one is sent to review rather than silently posted against a currency
// that no longer exists.
func NormaliseCurrency(s string) (string, bool, string) {
	code := strings.ToUpper(strings.TrimSpace(s))
	if len(code) != 3 || !isCurrencyCode(code) {
		return "", false, "not a valid ISO 4217 currency code"
	}
	return code, true, ""
}

// NormaliseUUID returns the identifier lowercased and hyphenated.
//
// Every textual form RFC 4122 §3 and its successors describe is accepted: the
// canonical 8-4-4-4-12, the bare 32 hex digits, the Microsoft braced form, and
// the urn:uuid URN. The version and variant bits are not checked, because the
// nil UUID, the max UUID and every GUID minted before RFC 4122 existed are all
// values a document legitimately contains, and rejecting them would be a
// stricter rule than the one the schema declares.
func NormaliseUUID(s string) (string, bool, string) {
	t := strings.TrimSpace(s)
	if len(t) >= 2 && t[0] == '{' && t[len(t)-1] == '}' {
		t = t[1 : len(t)-1]
	}
	if len(t) >= 9 && strings.EqualFold(t[:9], "urn:uuid:") {
		t = t[9:]
	}

	var hex []byte
	switch len(t) {
	case 32:
		hex = []byte(t)
	case 36:
		if t[8] != '-' || t[13] != '-' || t[18] != '-' || t[23] != '-' {
			return "", false, "not a valid UUID"
		}
		hex = make([]byte, 0, 32)
		for _, group := range []string{t[:8], t[9:13], t[14:18], t[19:23], t[24:]} {
			hex = append(hex, group...)
		}
	default:
		return "", false, "not a valid UUID"
	}

	for i, c := range hex {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
			hex[i] = c + ('a' - 'A')
		default:
			return "", false, "not a valid UUID"
		}
	}

	var b strings.Builder
	b.Grow(36)
	for i, c := range hex {
		if i == 8 || i == 12 || i == 16 || i == 20 {
			b.WriteByte('-')
		}
		b.WriteByte(c)
	}
	return b.String(), true, ""
}

// squash removes the separators a printed identifier is grouped with and
// uppercases what is left.
//
// IBANs and BICs are printed in groups of four for legibility. Spaces and
// hyphens are both used to make those groups and neither is part of the value,
// so both go — docs/schema.md names spaces, and a hyphen in a printed IBAN is
// the same typographical convention.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return unicode.ToUpper(r)
	}, s)
}
