package validate

// NormaliseIBAN returns the account number uppercased with its printed spacing
// removed, and validates the ISO 7064 mod-97-10 check digits.
//
// The checksum is the point. An IBAN's shape can be produced by anyone; its
// check digits cannot survive a transposed pair of characters, which is the
// error a person copying twenty-two characters off a scan actually makes. A
// shape check would pass exactly the values that lose money.
//
// The length is checked against the country's registered IBAN length where one
// is known, because mod-97 alone accepts a truncated number about one time in
// ninety-seven. An unregistered country is accepted at any length the standard
// permits rather than refused, so a country joining the registry does not turn
// valid documents into review items.
func NormaliseIBAN(s string) (string, bool, string) {
	iban := squash(s)
	if len(iban) < 15 || len(iban) > 34 {
		return "", false, "not a valid IBAN: wrong length"
	}
	if !isUpperAlpha(iban[0]) || !isUpperAlpha(iban[1]) {
		return "", false, "not a valid IBAN: it does not start with a country code"
	}
	if !isDigit(iban[2]) || !isDigit(iban[3]) {
		return "", false, "not a valid IBAN: the check digits are missing"
	}
	for i := 4; i < len(iban); i++ {
		if !isUpperAlnum(iban[i]) {
			return "", false, "not a valid IBAN: it contains characters an IBAN cannot"
		}
	}
	if want, known := ibanLength(iban[:2]); known && len(iban) != want {
		return "", false, "not a valid IBAN: wrong length for its country"
	}
	if mod97(iban[4:]+iban[:4]) != 1 {
		return "", false, "not a valid IBAN: the check digits do not match"
	}
	return iban, true, ""
}

// mod97 computes the ISO 7064 mod-97-10 residue of an IBAN's rearranged form.
//
// Letters expand to two digits (A is 10, Z is 35), which makes the number far
// too large for any integer type, so the remainder is carried a chunk at a time
// exactly as the standard describes. There is no shortcut here and no library
// to borrow: this is the arithmetic.
func mod97(s string) int {
	rem := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			rem = rem*10 + int(c-'0')
		case c >= 'A' && c <= 'Z':
			rem = rem*100 + int(c-'A') + 10
		default:
			return -1
		}
		rem %= 97
	}
	return rem
}

// NormaliseSWIFT returns the business identifier code uppercased with its
// printed spacing removed, and validates its structure.
//
// A BIC carries no check digits — ISO 9362 gives it none — so what can be
// checked is the structure and, more usefully, the country: characters five and
// six are an ISO 3166-1 code, and a BIC naming a country that does not exist is
// wrong however well-formed it looks. Claiming a checksum here would be a
// stronger promise than the standard supports.
func NormaliseSWIFT(s string) (string, bool, string) {
	bic := squash(s)
	if len(bic) != 8 && len(bic) != 11 {
		return "", false, "not a valid BIC: it must be 8 or 11 characters"
	}
	for i := 0; i < 4; i++ {
		if !isUpperAlpha(bic[i]) {
			return "", false, "not a valid BIC: the institution code must be letters"
		}
	}
	if !isCountryCode(bic[4:6]) {
		return "", false, "not a valid BIC: it does not name a real country"
	}
	// The location code is alphanumeric. 0 and 1 are excluded from its first
	// character and O from its second, so that a BIC cannot be confused with a
	// number when it is read aloud or keyed.
	if !isUpperAlnum(bic[6]) || bic[6] == '0' || bic[6] == '1' {
		return "", false, "not a valid BIC: the location code is malformed"
	}
	if !isUpperAlnum(bic[7]) || bic[7] == 'O' {
		return "", false, "not a valid BIC: the location code is malformed"
	}
	for i := 8; i < len(bic); i++ {
		if !isUpperAlnum(bic[i]) {
			return "", false, "not a valid BIC: the branch code is malformed"
		}
	}
	return bic, true, ""
}

// isUpperAlpha reports whether c is an uppercase ASCII letter.
func isUpperAlpha(c byte) bool { return c >= 'A' && c <= 'Z' }

// isUpperAlnum reports whether c is an uppercase letter or a digit.
func isUpperAlnum(c byte) bool { return isUpperAlpha(c) || isDigit(c) }
