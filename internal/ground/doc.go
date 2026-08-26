// Package ground searches the normalised text for an extracted value and
// produces the grounding confidence signal.
//
// A value that appears nowhere in the document it was read from was not read
// from it. Detecting that costs a string search, it is the cheapest strong
// signal ovrin has, and it catches the failure that matters most — a model
// that invented a number, or one that was told to by the document
// (docs/adr/0015-provenance.md, docs/adr/0017-untrusted-document-content.md).
//
// # The four outcomes
//
// From docs/pipeline.md stage 8 and docs/confidence.md. The values are
// specified, not chosen here:
//
//	outcome                                      Exact   Span   grounding
//	verbatim match                               true    set    1.0
//	normalised match, same value different form  false   set    0.8
//	derived: computed or reformatted             false   nil    0.5
//	no match                                     false   nil    0.0 + a reason
//
// # Matching is type-aware
//
// A naive string search reports a false negative on nearly every formatted
// number, and a check that fires constantly is a check nobody reads. So
// matching follows the comparison table in docs/confidence.md: 25,000 and
// 25000 and 25 000 are one number, dates compare as instants, and strings
// compare after the Unicode normalisation in internal/normalise, whitespace
// collapse and case folding.
//
// The comparison runs in two passes and the order is the outcome. A search
// for the value's own bytes runs first, and a hit there is verbatim, 1.0. Only
// when that fails does the type-aware pass run, and a hit there is a
// normalised match, 0.8. The two can never disagree about which is which.
//
// Every match is bounded: a match may not begin or end in the middle of a
// word, so a search for "Smith" does not find "Smithson" and a search for the
// number 25 does not find 255. Matches that reach into a page marker are
// rejected — [normalise.Marker] is text ovrin inserted, and a marker
// containing "2" is not the document saying two.
//
// # Derived is declared, not detected
//
// Ovrin cannot tell a value computed from content that is present — a total
// summed from line items — from a value that was invented, because neither
// appears in the text. A search cannot produce the derived row of the table
// on its own, and pretending otherwise would turn the strongest signal here
// into a shrug.
//
// So the caller declares it, with [WithDerivable], from what the validation
// stage already knows: a field whose cross-field rule passed is consistent
// with its siblings, and that is the evidence that makes 0.5 honest. Without
// the option a value that is not in the document scores 0.0 and carries a
// review reason, which is the answer that is correct when nothing else is
// known.
//
// # What this package will get wrong
//
// It will produce false negatives, and each one becomes a review reason that
// is technically correct and practically noise (docs/adr/0015-provenance.md
// names this as a cost of the decision).
//
// Known sources: a value paraphrased rather than copied; a number written in
// words; a string the model expanded ("Acme Ltd" from "Acme"), which the
// comparison table says is a different string; a date whose prose form this
// package does not parse; a number in a script whose digits are not ASCII;
// and any document convention outside the documented ones in number.go and
// date.go.
//
// A [Result] never carries the value it was looking for, and neither does
// [Result.Reason]. A reason becomes a ReviewReason, a ReviewReason is logged,
// and document values do not go in logs (docs/rules.md §7.5).
package ground
