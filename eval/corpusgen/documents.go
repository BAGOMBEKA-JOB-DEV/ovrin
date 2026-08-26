package main

// documents returns the whole seed corpus.
//
// Five documents per category, which is what ADR-0023 asks for: "five real
// documents per category beats zero, and a harness with a small corpus is
// infinitely more useful than a large corpus with no harness".
//
// The difficulty spread is deliberate and roughly even. A corpus that was all
// clean digital PDFs would report an excellent figure and predict nothing
// about a photograph of a receipt, which is the failure the difficulty
// breakdown in every report exists to prevent.
func documents() []document {
	var out []document
	out = append(out, invoices()...)
	out = append(out, receipts()...)
	out = append(out, forms()...)
	out = append(out, statements()...)
	out = append(out, identity()...)
	return out
}
