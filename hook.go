package ovrin

import (
	"context"
	"time"
)

// Hook receives an [Event] for each pipeline stage.
//
// Hooks run synchronously on the calling goroutine. A hook that blocks slows
// the extraction and a hook that does I/O will, which is the caller's
// responsibility — making it asynchronous is one line in your own hook, and
// doing it here would hide ordering and leak a goroutine per client.
//
// The core emits hooks and depends on nothing. OpenTelemetry lives in its own
// module, so a user who wants no telemetry carries none and a user on a
// different stack writes five lines instead of adopting OTel.
type Hook func(ctx context.Context, ev Event)

// Event is one pipeline stage, reported as it completes.
//
// There is deliberately no field an extracted value could occupy — no
// map[string]any, no Raw, no free-text note. A span attribute carrying a value
// would ship somebody's national ID number to an observability vendor they
// never heard of, and a rule saying "do not do that" gets violated the first
// time it is convenient. Field counts are here; field values are not
// representable. See docs/adr/0021-observability.md and docs/rules.md §7.5.
type Event struct {
	// Op is the stage that ran.
	Op Op

	// Provider names the adapter that served it, if one did.
	Provider string

	// Page is 1-based, and zero for whole-document stages.
	Page int

	// Attempt is 1 for the first try.
	Attempt int

	// Duration is how long the stage took.
	Duration time.Duration

	// Err is non-nil if the stage failed. A failed stage still emits an event.
	Err error

	// Bytes read or produced by the stage.
	Bytes int64

	// Pages in the document.
	Pages int

	// Fields is a count. Not the names, and certainly not the values.
	Fields int

	// Usage is what the stage consumed.
	Usage Usage

	// Confidence is the aggregate, set on the final stage only.
	Confidence float64

	// Review reports whether the result needs a person, set on the final
	// stage only.
	Review bool
}

// Usage counts what an extraction consumed.
//
// Tokens are what models bill; page units are what OCR providers bill. Both are
// here because a pipeline that uses both should be costable from one value.
type Usage struct {
	InputTokens  int
	OutputTokens int
	PageUnits    int
}

// Metadata records how a result was produced.
type Metadata struct {
	// Readings is which readings were taken, in order.
	Readings []Reading

	// Providers names the adapter that served each stage, so a result carries
	// the evidence of where its content went — which matters when a fallback
	// chain means that was not decided in advance.
	Providers map[Op]string

	// Kind is the detected format.
	Kind Kind

	// Pages in the document.
	Pages int

	// Usage is the total across every provider call.
	Usage Usage

	// Duration is the wall time of the extraction.
	Duration time.Duration
}

// ReviewReason names a field that needs a person, and why.
//
// It carries a field name and a cause, never the value: a review queue is a
// system that stores things, and document values should reach it because the
// caller decided to put them there, not because ovrin leaked them in a reason
// string.
type ReviewReason struct {
	// Field is the field key, as in [Result.Fields].
	Field string

	// Why is a short cause, such as "value not found in source; may be
	// inferred" or "readings disagree".
	Why string
}
