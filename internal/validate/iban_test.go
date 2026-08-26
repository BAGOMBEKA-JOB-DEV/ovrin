package validate

import "testing"

func TestNormaliseIBAN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"United Kingdom, printed in groups", "GB82 WEST 1234 5698 7654 32", "GB82WEST12345698765432"},
		{"Germany", "DE89 3704 0044 0532 0130 00", "DE89370400440532013000"},
		{"France, with a letter in the account number", "FR14 2004 1010 0505 0001 3M02 606", "FR1420041010050500013M02606"},
		{"Netherlands", "NL91 ABNA 0417 1643 00", "NL91ABNA0417164300"},
		{"Switzerland", "CH93 0076 2011 6238 5295 7", "CH9300762011623852957"},
		{"Belgium, the shortest registered length", "BE68 5390 0754 7034", "BE68539007547034"},
		{"Norway, the shortest IBAN there is", "NO93 8601 1117 947", "NO9386011117947"},
		{"Malta, one of the longest", "MT84 MALT 0110 0001 2345 MTLC AST0 01S", "MT84MALT011000012345MTLCAST001S"},
		{"Brazil", "BR15 0000 0000 0000 1093 2840 814 P2", "BR1500000000000010932840814P2"},
		{"Kosovo, which has no ISO country code", "XK05 1212 0123 4567 8906", "XK051212012345678906"},
		{"lowercase is uppercased", "gb82 west 1234 5698 7654 32", "GB82WEST12345698765432"},
		{"hyphens are printed grouping too", "GB82-WEST-1234-5698-7654-32", "GB82WEST12345698765432"},
		{"no grouping at all", "GB82WEST12345698765432", "GB82WEST12345698765432"},
		{"a country with no registered length is accepted on its checksum", "GA8740002000055000078025300", "GA8740002000055000078025300"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormaliseIBAN(c.in)
			if !ok {
				t.Fatalf("rejected: %s", reason)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormaliseIBANRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"too short for any country", "GB82WEST123"},
		{"longer than the standard allows", "GB82WEST1234569876543212345678901234"},
		{"a transposed pair, which is the error people actually make", "GB82WEST12345698765423"},
		{"a single wrong digit", "GB82WEST12345698765433"},
		{"wrong check digits", "GB00WEST12345698765432"},
		{"no country code", "1282WEST12345698765432"},
		{"no check digits", "GBXXWEST12345698765432"},
		{"a punctuation character in the account number", "GB82WEST1234569876543!"},
		{"the right checksum at the wrong length for its country", "DE291234567890123456"},
		{"an account number one character short", "GB82WEST1234569876543"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := NormaliseIBAN(c.in); ok {
				t.Errorf("accepted as %q, want a refusal", got)
			}
		})
	}
}

// TestMod97 checks the arithmetic itself, because everything above depends on
// it and a subtly wrong remainder accepts one bad IBAN in ninety-seven.
func TestMod97(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"a valid IBAN rearranged", "WEST12345698765432GB82", 1},
		{"the same with one digit changed", "WEST12345698765433GB82", 28},
		{"a letter expands to two digits", "A", 10},
		{"Z is thirty-five", "Z", 35},
		{"digits alone", "9799", 2},
		{"a value that is exactly a multiple", "97", 0},
		{"a character an IBAN cannot contain", "GB82!", -1},
		{"a lowercase letter, which squash should have removed", "a", -1},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := mod97(c.in); got != c.want {
				t.Errorf("mod97(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestNormaliseSWIFT(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"eight characters", "DEUTDEFF", "DEUTDEFF"},
		{"eleven characters with a branch", "DEUTDEFF500", "DEUTDEFF500"},
		{"the primary office branch code", "DEUTDEFFXXX", "DEUTDEFFXXX"},
		{"lowercase is uppercased", "deutdeff", "DEUTDEFF"},
		{"printed with a space", "DEUT DE FF", "DEUTDEFF"},
		{"a digit in the location code", "CITIUS33", "CITIUS33"},
		{"Uganda", "SBICUGKX", "SBICUGKX"},
		{"Kosovo, which SWIFT spells XK", "RBKOXKPR", "RBKOXKPR"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok, reason := NormaliseSWIFT(c.in)
			if !ok {
				t.Fatalf("rejected: %s", reason)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormaliseSWIFTRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"nine characters, which is neither length", "DEUTDEFF5"},
		{"seven characters", "DEUTDEF"},
		{"twelve characters", "DEUTDEFF5000"},
		{"a digit in the institution code", "1EUTDEFF"},
		{"a country that does not exist", "DEUTQQFF"},
		{"a digit in the country code", "DEUT1EFF"},
		{"a zero starting the location code", "DEUTDE0F"},
		{"a one starting the location code", "DEUTDE1F"},
		{"the letter O ending the location code", "DEUTDEFO"},
		{"punctuation in the branch code", "DEUTDEFF5!0"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok, _ := NormaliseSWIFT(c.in); ok {
				t.Errorf("accepted as %q, want a refusal", got)
			}
		})
	}
}
