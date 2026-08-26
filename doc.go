// Package ovrin turns documents into structured data.
//
// Give it a Go struct describing what you want and a document — a PDF, a scan,
// a photograph — and it returns that struct populated, alongside the evidence
// for every value: a confidence score you can decompose, a record of which page
// and which region each value came from, and an explicit signal when a person
// should look before the data is used.
//
//	type Invoice struct {
//		Number   string  `ovrin:"invoice number,required"`
//		Vendor   string  `ovrin:"vendor company name"`
//		Currency string  `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
//		Total    float64 `ovrin:"total amount including tax,required,min=0"`
//	}
//
//	c := ovrin.New(ovrin.WithModel(model))
//
//	res, err := ovrin.Extract[Invoice](ctx, c, ovrin.File("invoice.pdf"))
//	if err != nil {
//		return err
//	}
//	if !res.Valid || res.NeedsReview {
//		return review.Queue(res)
//	}
//	return ledger.Post(res.Data)
//
// # Two answers, not one
//
// A non-nil error means nothing usable came back: the source could not be read,
// no provider was configured for it, a limit was exceeded, or the context was
// cancelled. It does not mean the data is good — that is [Result.Valid].
//
// A field that could not be read is marked absent rather than filled with a
// zero value. A payments system must be able to tell "the total is zero" from
// "we could not read the total", so [FieldResult.Found] reports presence and
// nothing is ever guessed to satisfy a struct.
//
// # Not a prompt with extra steps
//
// Ovrin runs a staged pipeline rather than handing a document to a model and
// hoping. When a PDF carries its own text, reading it is exact and nearly free;
// rendering those characters to pixels for a model to read back is a lossy
// round trip. OCR runs when there is no text layer. Vision is a third reading,
// not a shortcut past the pipeline.
//
// That staging is also what makes the rest possible. Confidence is computed
// from named signals that fail in uncorrelated ways — whether the value appears
// in the document at all, how cleanly the characters read, whether it satisfies
// its declared rules, whether it agrees with its siblings — because a model's
// self-reported confidence is uncorrelated with correctness, and token
// logprobs are unavailable on one major provider and saturate to a constant
// under the constrained decoding used here.
//
// # Provider independent
//
// Three small interfaces — [Model], [OCR] and [Renderer] — and no vendor is
// privileged. Implementations live in their own modules, so a user who wants
// Tesseract does not inherit a cloud SDK, and a user who wants neither
// inherits nothing.
//
// This package has no external dependencies and uses no cgo, so it
// cross-compiles and builds static.
//
// # Untrusted input
//
// Documents arrive from claimants, suppliers and email attachments. They are
// parsed with finite limits on every dimension, and their text reaches a model
// as data, never as instruction. Ovrin does not promise that prompt injection
// is impossible — nobody can — but a value an injected instruction produces
// tends not to appear anywhere in the document, and ovrin reports that rather
// than accepting it silently.
//
// See docs/pipeline.md for the nine stages, docs/schema.md for the struct tag
// grammar, docs/confidence.md for what the score does and does not mean, and
// docs/threat-model.md before processing documents from the public.
package ovrin
