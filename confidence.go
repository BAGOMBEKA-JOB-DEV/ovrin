package ovrin

// The names of the built-in confidence signals.
//
// These are untyped string constants rather than a named enum type, because
// the set is open at the edges: a custom [Scorer] fitted to somebody's own
// corpus may emit signals ovrin has never heard of, and a named type would
// either forbid that or need an escape hatch that defeats the point. This is
// the same shape as http.MethodGet — well-known values in an open set.
//
// The six below are what ovrin's own scorer produces. A signal that does not
// apply to a field is absent, not zero: a text-layer PDF has no OCR signal,
// and scoring that as 0.0 would penalise the most accurate acquisition path
// ovrin has.
const (
	// SignalOCR is character-recognition confidence over the words backing
	// the value.
	SignalOCR = "ocr"

	// SignalSchema is whether the value satisfied its declared type and rules.
	SignalSchema = "schema"

	// SignalCrossField is consistency with sibling fields — line items summing
	// to a total, an issue date before a due date.
	SignalCrossField = "cross_field"

	// SignalAgreement is whether two independent readings produced the same
	// value. Available only when two readings ran.
	SignalAgreement = "agreement"

	// SignalFormat is whether the value matches the expected shape for its
	// kind: a date that parses, a currency code that exists.
	SignalFormat = "format"

	// SignalGrounding is whether the value actually appears in the source.
	//
	// The cheapest strong signal available, and the one that catches the
	// failure that matters most: a value appearing nowhere in the document it
	// was read from was not read from it.
	SignalGrounding = "grounding"
)

// Signal is one named input to a confidence score.
//
// Every score decomposes into these. No number is produced that a caller
// cannot take apart, because a confidence figure nobody can interrogate is a
// figure nobody should act on.
type Signal struct {
	// Name is one of the Signal* constants.
	Name string

	// Value is on 0..1.
	Value float64

	// Weight is this signal's share of the score, after redistribution across
	// the signals that actually applied.
	Weight float64

	// Note says why, in one line: "found verbatim, page 1", "12 backing words,
	// mean 0.97".
	Note string
}

// RuleResult is one validation rule and its outcome.
//
// Message is a string rather than an error so that an [Explanation] marshals to
// JSON — which is the point of [Result.Explain] returning data rather than
// formatted text.
type RuleResult struct {
	// Rule is the rule as written in the tag: "required", "min=0",
	// "format=date".
	Rule string

	// Passed reports the outcome.
	Passed bool

	// Message says why not, and is empty when Passed.
	Message string
}

// FieldEvidence is everything the pipeline collected about one field.
//
// It is the input to a [Scorer], and it is deliberately everything rather than
// a summary: a caller fitting a scorer to their own labelled documents should
// not be limited to the signals ovrin happened to think of.
type FieldEvidence struct {
	// Field is the field key.
	Field string

	// Value is what was extracted.
	Value any

	// Found reports whether the field was present at all.
	Found bool

	// Reading is which reading produced the value.
	Reading Reading

	// OCRConfidence is the mean confidence of the words backing the value, or
	// nil when the value did not come from OCR.
	OCRConfidence *float64

	// Grounding is on 0..1: 1.0 verbatim, 0.8 normalised, 0.5 derived, 0.0 not
	// found in the source.
	Grounding float64

	// Provenance is where the value came from.
	Provenance []Provenance

	// Candidates holds competing values when two readings disagreed.
	Candidates []Candidate

	// Agreement is whether two independent readings produced the same value,
	// or nil when only one reading ran.
	//
	// Nil rather than zero, because "no second opinion was taken" and "the
	// second opinion differed" are opposite facts and scoring them alike would
	// penalise every single-reading extraction (docs/confidence.md §Signals).
	Agreement *float64

	// AgreementNote says which, in one line, for the signal's Note.
	AgreementNote string

	// Validation is each rule and its outcome.
	Validation []RuleResult

	// Suspicious reports whether the source page carried content that looked
	// like an injection attempt.
	Suspicious bool
}

// Scorer combines evidence into a confidence score.
//
// Pluggable because a user with labelled documents can fit a better scorer to
// their own corpus than any default will manage. The consequence is that
// confidence is comparable within a deployment, not across organisations.
type Scorer interface {
	// Score returns the confidence and the signals that produced it. The
	// signals must account for the score: a caller must be able to check the
	// arithmetic.
	Score(f FieldEvidence) (confidence float64, signals []Signal)
}
