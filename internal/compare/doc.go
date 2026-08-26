// Package compare answers one question: are these two values, read
// independently, the same value?
//
// It is the machinery behind the agreement signal
// (docs/adr/0014-cross-validation.md, docs/confidence.md §Comparison), which
// carries the second-heaviest weight of the six and is the only signal that
// can catch the failure ovrin was built around:
//
//	OCR reads:  Amount: 25,000
//	AI reads:   Amount:  2,500
//
// Both readings are well-formed numbers, both satisfy min=0, and both pass
// every other check in the library. Nothing derived from a single reading
// distinguishes them. Two readings do, because OCR misreads glyphs and a model
// misassigns fields, and those two error sources are uncorrelated.
//
// # Nothing here resolves anything
//
// This package reports same or different and stops. It never picks a winner
// and discards the loser: two readings that disagree mean at least one is
// wrong, no fixed preference is right — OCR wins on printed amounts, the model
// wins on layout-dependent assignment — and silently preferring either is the
// failure ADR-0014 exists to prevent (docs/rules.md §8.4). [Field] ranks the
// readings so the caller can put the higher-confidence value in FieldResult.Value,
// and keeps every one of them so nothing is thrown away.
//
// # False agreement is cheap; false disagreement is not
//
// A disagreement becomes a review reason, and a review flag that fires on
// every document trains reviewers to dismiss it — at which point the flag is
// worse than absent. So comparison is type-aware, exactly as
// docs/confidence.md §Comparison specifies:
//
//	kind      equal when                                          not equal
//	numeric   same value after separators and symbols are stripped 25,000 vs 2,500
//	currency  same amount and same currency                        100 USD vs 100 EUR
//	date      same instant after parsing                           03/04/26 and 2026-04-03 agree
//	string    equal after NFKC, whitespace collapse, case folding   Acme Ltd vs Acme Limited
//	bool      same value
//	slice     same length and every element equal
//
// 25,000 and 25000 and 25 000 are one number. ACME LTD and Acme Ltd are one
// string. Acme Ltd and Acme Limited are two, and that is a disagreement worth
// a reviewer's attention.
//
// # Where the rules come from
//
// Nowhere in this package. A second, subtly different notion of "the same
// value" living beside the first is the drift ovrin exists to prevent, so the
// readings are the ones the rest of the library already uses:
//
//   - [validate.ParseNumber] reads a formatted figure, including thousands
//     separators, a currency symbol or code, and accounting parentheses. Its
//     doc comment says it is exported for this package's benefit.
//   - [validate.ParseBool] reads the affirmative spellings a form uses, so that
//     "Yes" and "true" are one value here and in conversion.
//   - [validate.ParseDateTime] reads a date, and refuses to guess at an
//     ambiguous one.
//   - [normalise.Canonical] applies ovrin's Unicode subset and whitespace
//     collapse, so a value is compared using the same transformation the
//     document text went through.
//   - [ground.Kind] and [ground.KindOf] name the comparison kinds. There is one
//     enumeration of them, not two.
//
// Grounding itself is deliberately not reused. [ground.Ground] asks whether a
// value appears somewhere in a document, and containment is not equality: a
// search for "Acme" succeeds against text reading "Acme Ltd", which as a
// comparison would be a false agreement on the exact case the table calls out.
//
// # What it will get wrong
//
// False agreement, mostly, which is the direction chosen deliberately.
//
//   - Two readings can be wrong in the same way, most obviously when both come
//     from the same underlying model. Agreement is not correctness and this is
//     not detectable from inside (docs/confidence.md §Comparison).
//   - An amount carrying a currency in only one reading is compared on the
//     amount alone, because a model that returned a bare number for a currency
//     field has not contradicted the reading that kept the symbol.
//   - An ambiguous numeric date matches either of its readings unless
//     [WithDateOrder] settles the convention, so 03/04/26 agrees with both
//     3 April and 4 March.
//   - A bare "$" is read as USD, as it is in internal/ground, so a Canadian
//     dollar amount agrees with a US dollar one.
//
// And one false-disagreement source that is left in on purpose: strings are
// compared whole, so "Acme Ltd" and "Acme Ltd." differ. Stripping punctuation
// would also erase the difference between an identifier and a nearby one.
//
// # No values in reasons
//
// A disagreement is data, not an error, so this package returns none
// (docs/rules.md §2.6). [Result.Reason] and [FieldResult.Reason] say why in
// words that never contain a value: a reason becomes a ReviewReason, a
// ReviewReason is logged, and document content does not go in logs
// (docs/rules.md §7.5). The values themselves are on [FieldResult.Candidates],
// where the caller — who already holds the document — can use them.
package compare
