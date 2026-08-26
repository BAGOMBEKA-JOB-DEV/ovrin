package validate

import (
	"sort"
	"strings"
	"testing"
)

func TestNormaliseEmail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already normalised", "a@example.com", "a@example.com"},
		{"mixed case is lowercased", "Amina.Nakato@Example.COM", "amina.nakato@example.com"},
		{"surrounding whitespace", "  a@example.com  ", "a@example.com"},
		{"a display name is discarded", "Amina Nakato <a@example.com>", "a@example.com"},
		{"a quoted display name", `"Nakato, Amina" <A@Example.com>`, "a@example.com"},
		{"a plus tag", "invoices+ovrin@example.com", "invoices+ovrin@example.com"},
		{"a subdomain", "a@mail.example.co.ug", "a@mail.example.co.ug"},
		{"a hyphenated domain", "a@my-company.com", "a@my-company.com"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormaliseEmail(c.in)
			if !ok {
				t.Fatalf("rejected: %s", reason)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormaliseEmailRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"no at sign", "nobody.example.com"},
		{"prose", "nobody at example dot com"},
		{"no local part", "@example.com"},
		{"no domain", "nobody@"},
		{"a domain with no dot", "nobody@localhost"},
		{"a numeric top-level domain", "nobody@example.123"},
		{"a one-letter top-level domain", "nobody@example.c"},
		{"a label starting with a hyphen", "nobody@-example.com"},
		{"a double dot", "nobody@example..com"},
		{"two addresses", "a@example.com, b@example.com"},
		{"a space in the middle", "no body@example.com"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := NormaliseEmail(c.in); ok {
				t.Errorf("accepted as %q, want a refusal", got)
			}
		})
	}
}

func TestNormalisePhone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already E.164", "+256414234567", "+256414234567"},
		{"spaces and brackets", "+256 (41) 423-4567", "+256414234567"},
		{"a trunk prefix printed for local dialling", "+256 (0) 41 4234567", "+256414234567"},
		{"the 00 international prefix", "00256414234567", "+256414234567"},
		{"dots as separators", "+1.415.555.0132", "+14155550132"},
		{"a hyphenated North American number", "+1-415-555-0132", "+14155550132"},
		{"a national number keeps its trunk zero", "0771 234 567", "0771234567"},
		{"a national number with brackets", "(0771) 234-567", "0771234567"},
		{"a non-breaking space", "+256 414 234 567", "+256414234567"},
		{"an en dash", "+1–415–555–0132", "+14155550132"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormalisePhone(c.in)
			if !ok {
				t.Fatalf("rejected: %s", reason)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormalisePhoneRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"a vanity number", "+1 800 CALL NOW"},
		{"an extension nobody can dial from E.164", "+256414234567 ext 12"},
		{"too few digits to dial", "12345"},
		{"more digits than E.164 allows", "+1234567890123456"},
		{"an international number whose country code starts with zero", "+0256414234567"},
		{"prose", "call the office"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := NormalisePhone(c.in); ok {
				t.Errorf("accepted as %q, want a refusal", got)
			}
		})
	}
}

func TestNormaliseCurrency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"uppercase already", "UGX", "UGX", true},
		{"lowercase is uppercased", "usd", "USD", true},
		{"mixed case", "eUr", "EUR", true},
		{"whitespace", "  GBP  ", "GBP", true},
		{"the first code in the table", "AED", "AED", true},
		{"the last code in the table", "ZWG", "ZWG", true},
		{"a fund code", "XDR", "XDR", true},
		{"a metal", "XAU", "XAU", true},
		{"a code nobody issues", "XYZ", "", false},
		{"a withdrawn code", "HRK", "", false},
		{"a currency name rather than a code", "dollars", "", false},
		{"a symbol", "$", "", false},
		{"two letters", "US", "", false},
		{"four letters", "USDX", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormaliseCurrency(c.in)
			if ok != c.ok {
				t.Fatalf("ok = %v (%s), want %v", ok, reason, c.ok)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormaliseUUID(t *testing.T) {
	t.Parallel()
	const want = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	cases := []struct{ name, in string }{
		{"canonical", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
		{"uppercase", "6BA7B810-9DAD-11D1-80B4-00C04FD430C8"},
		{"mixed case", "6Ba7b810-9dAd-11d1-80B4-00c04FD430c8"},
		{"unhyphenated", "6ba7b8109dad11d180b400c04fd430c8"},
		{"braced", "{6ba7b810-9dad-11d1-80b4-00c04fd430c8}"},
		{"braced and unhyphenated", "{6BA7B8109DAD11D180B400C04FD430C8}"},
		{"a URN", "urn:uuid:6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
		{"a URN in capitals", "URN:UUID:6BA7B810-9DAD-11D1-80B4-00C04FD430C8"},
		{"surrounding whitespace", "  6ba7b810-9dad-11d1-80b4-00c04fd430c8  "},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormaliseUUID(c.in)
			if !ok {
				t.Fatalf("rejected: %s", reason)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestNormaliseUUIDAcceptsEveryVariant(t *testing.T) {
	t.Parallel()
	// The nil UUID, the max UUID and a pre-RFC 4122 GUID are all values a
	// document legitimately carries. Checking the version bits would reject
	// them for being older than the rule.
	for _, in := range []string{
		"00000000-0000-0000-0000-000000000000",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
		"c56a4180-65aa-42ec-a945-5fd21dec0538",
	} {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if _, ok, reason := NormaliseUUID(in); !ok {
				t.Errorf("rejected: %s", reason)
			}
		})
	}
}

func TestNormaliseUUIDRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"one digit short", "6ba7b810-9dad-11d1-80b4-00c04fd430c"},
		{"one digit long", "6ba7b810-9dad-11d1-80b4-00c04fd430c89"},
		{"a non-hex digit", "6ba7b810-9dad-11d1-80b4-00c04fd430cg"},
		{"hyphens in the wrong places", "6ba7b8-1099dad-11d1-80b4-00c04fd430c8"},
		{"a closing brace with no opening one", "6ba7b810-9dad-11d1-80b4-00c04fd430c8}"},
		{"an unrelated identifier", "INV-2026-0001"},
		{"spaces instead of hyphens", "6ba7b810 9dad 11d1 80b4 00c04fd430c8"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := NormaliseUUID(c.in); ok {
				t.Errorf("accepted as %q, want a refusal", got)
			}
		})
	}
}

// TestTablesAreSorted guards the binary searches: an unsorted table does not
// fail loudly, it silently stops finding valid codes.
func TestTablesAreSorted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		table    string
		width    int
		keyWidth int
	}{
		{"ISO 4217", iso4217, 3, 3},
		{"ISO 3166-1", iso3166, 2, 2},
		{"IBAN lengths", ibanLengths, 4, 2},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if len(c.table)%c.width != 0 {
				t.Fatalf("table length %d is not a multiple of %d", len(c.table), c.width)
			}
			var keys []string
			for i := 0; i < len(c.table); i += c.width {
				keys = append(keys, c.table[i:i+c.width])
			}
			if !sort.StringsAreSorted(keys) {
				t.Error("table is not sorted, so the binary search will miss entries")
			}
			for i := 1; i < len(keys); i++ {
				if keys[i] == keys[i-1] {
					t.Errorf("duplicate entry at %d", i)
				}
			}
			// Every entry must be findable by the search that reads the table.
			for _, k := range keys {
				if lookup(c.table, c.width, k[:c.keyWidth]) < 0 {
					t.Errorf("entry %q is in the table but cannot be found", k)
				}
			}
			if strings.ToUpper(c.table) != c.table {
				t.Error("table must be uppercase")
			}
		})
	}
}

func TestReferenceTableSpotChecks(t *testing.T) {
	t.Parallel()
	if n, ok := ibanLength("GB"); !ok || n != 22 {
		t.Errorf("ibanLength(GB) = %d %v, want 22 true", n, ok)
	}
	if n, ok := ibanLength("NO"); !ok || n != 15 {
		t.Errorf("ibanLength(NO) = %d %v, want 15 true", n, ok)
	}
	if _, ok := ibanLength("ZZ"); ok {
		t.Error("ibanLength(ZZ) found an entry that does not exist")
	}
	if !isCountryCode("UG") || !isCountryCode("XK") || isCountryCode("QQ") || isCountryCode("XX") {
		t.Error("country table does not hold what it should")
	}
	if !isCurrencyCode("UGX") || isCurrencyCode("UGZ") {
		t.Error("currency table does not hold what it should")
	}
}

func TestLookupRefusesAKeyWiderThanTheTable(t *testing.T) {
	t.Parallel()
	if got := lookup(iso3166, 2, "UGX"); got >= 0 {
		t.Errorf("lookup returned %d for an over-long key, want -1", got)
	}
	if got := lookup(iso3166, 2, ""); got >= 0 {
		t.Errorf("lookup returned %d for an empty key, want -1", got)
	}
}
