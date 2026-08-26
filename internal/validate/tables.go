package validate

import "strings"

// The reference tables, held as sorted fixed-width strings and searched by
// halving.
//
// A string constant rather than a map because a map is package-level mutable
// state, needs an init to build, and buys nothing at these sizes: a binary
// search over 179 three-character codes is a handful of comparisons. Every
// table is sorted, and the tests assert that it is, because a table that
// silently loses its order silently starts rejecting valid values.
const (
	// iso4217 is the active ISO 4217 currency codes as at the 2025 amendments.
	// Withdrawn codes are absent, so a document printing one is reviewed
	// rather than posted against a currency that no longer exists.
	iso4217 = "AEDAFNALLAMDANGAOAARSAUDAWGAZNBAMBBDBDTBGNBHDBIFBMDBNDBOBBOV" +
		"BRLBSDBTNBWPBYNBZDCADCDFCHECHFCHWCLFCLPCNYCOPCOUCRCCUPCVECZK" +
		"DJFDKKDOPDZDEGPERNETBEURFJDFKPGBPGELGHSGIPGMDGNFGTQGYDHKDHNL" +
		"HTGHUFIDRILSINRIQDIRRISKJMDJODJPYKESKGSKHRKMFKPWKRWKWDKYDKZT" +
		"LAKLBPLKRLRDLSLLYDMADMDLMGAMKDMMKMNTMOPMRUMURMVRMWKMXNMXVMYR" +
		"MZNNADNGNNIONOKNPRNZDOMRPABPENPGKPHPPKRPLNPYGQARRONRSDRUBRWF" +
		"SARSBDSCRSDGSEKSGDSHPSLESOSSRDSSPSTNSVCSYPSZLTHBTJSTMTTNDTOP" +
		"TRYTTDTWDTZSUAHUGXUSDUSNUYIUYUUYWUZSVEDVESVNDVUVWSTXAFXAGXAU" +
		"XBAXBBXBCXBDXCDXCGXDRXOFXPDXPFXPTXSUXTSXUAXXXYERZARZMWZWG"

	// iso3166 is the assigned ISO 3166-1 alpha-2 codes, plus XK: Kosovo has no
	// ISO code and both the IBAN registry and SWIFT use XK for it, so refusing
	// it would reject real bank identifiers.
	iso3166 = "ADAEAFAGAIALAMAOAQARASATAUAWAXAZBABBBDBEBFBGBHBIBJBLBMBNBOBQ" +
		"BRBSBTBVBWBYBZCACCCDCFCGCHCICKCLCMCNCOCRCUCVCWCXCYCZDEDJDKDM" +
		"DODZECEEEGEHERESETFIFJFKFMFOFRGAGBGDGEGFGGGHGIGLGMGNGPGQGRGS" +
		"GTGUGWGYHKHMHNHRHTHUIDIEILIMINIOIQIRISITJEJMJOJPKEKGKHKIKMKN" +
		"KPKRKWKYKZLALBLCLILKLRLSLTLULVLYMAMCMDMEMFMGMHMKMLMMMNMOMPMQ" +
		"MRMSMTMUMVMWMXMYMZNANCNENFNGNINLNONPNRNUNZOMPAPEPFPGPHPKPLPM" +
		"PNPRPSPTPWPYQARERORSRURWSASBSCSDSESGSHSISJSKSLSMSNSOSRSSSTSV" +
		"SXSYSZTCTDTFTGTHTJTKTLTMTNTOTRTTTVTWTZUAUGUMUSUYUZVAVCVEVGVI" +
		"VNVUWFWSXKYEYTZAZMZW"

	// ibanLengths is the registered IBAN length per country, as a country code
	// followed by two digits. A country absent from the table is not rejected:
	// see [NormaliseIBAN].
	ibanLengths = "AD24AE23AL28AT20AZ28BA20BE16BG22BH22BI27BR29BY28CH21CR22CY28" +
		"CZ24DE22DJ27DK18DO28EE20EG29ES24FI18FK18FO18FR27GB22GE22GI23" +
		"GL18GR27GT28HN28HR21HU28IE22IL23IQ23IS26IT27JO30KW30KZ20LB28" +
		"LC32LI21LT20LU20LV21LY25MC27MD24ME22MK19MN20MR27MT31MU30NI28" +
		"NL18NO15OM23PK24PL28PS29PT25QA29RO24RS22RU33SA24SC31SD18SE24" +
		"SI19SK24SM27SO23ST25SV28TL23TN24TR26UA29VA22VG24XK20YE30"
)

// isCurrencyCode reports whether code is an active ISO 4217 code.
func isCurrencyCode(code string) bool { return lookup(iso4217, 3, code) >= 0 }

// isCountryCode reports whether code is an assigned ISO 3166-1 alpha-2 code.
func isCountryCode(code string) bool { return lookup(iso3166, 2, code) >= 0 }

// ibanLength returns the registered IBAN length for a country, and whether one
// is registered.
func ibanLength(country string) (int, bool) {
	at := lookup(ibanLengths, 4, country)
	if at < 0 {
		return 0, false
	}
	d := ibanLengths[at+2 : at+4]
	return int(d[0]-'0')*10 + int(d[1]-'0'), true
}

// lookup binary-searches a sorted fixed-width table for a key, and returns the
// offset of its record or -1.
//
// The key is compared against only its own length, so a table whose records
// carry a payload after the key — as ibanLengths does — is searched by key
// alone.
func lookup(table string, width int, key string) int {
	if len(key) == 0 || len(key) > width {
		return -1
	}
	n := len(table) / width
	lo, hi := 0, n-1
	for lo <= hi {
		mid := (lo + hi) / 2
		at := mid * width
		switch strings.Compare(table[at:at+len(key)], key) {
		case 0:
			return at
		case -1:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return -1
}
