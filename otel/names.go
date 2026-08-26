package ovrinotel

import (
	"errors"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"go.opentelemetry.io/otel/attribute"
)

// The names in this file are API. Renaming one breaks somebody's dashboard,
// which makes it a breaking change with a changelog entry and a migration note
// (docs/observability.md). They are unexported deliberately: the emitted
// strings are the promise, and exporting Go constants for them would add a
// second surface saying the same thing that has to be kept in step.

// scopeName is the instrumentation scope. It is the import path, which is what
// an operator can search for when a span turns up and they want to know what
// produced it.
const scopeName = "github.com/BAGOMBEKA-JOB-DEV/ovrin/otel"

// spanExtract is the root span for one Extract call.
//
// It is not in spanNames because it is not an [ovrin.Op]: it covers the whole
// extraction rather than a stage of it.
const spanExtract = "ovrin.extract"

// spanNames maps every pipeline stage to its span name.
//
// The table is exhaustive over the Op vocabulary on purpose. A new Op with no
// entry here is drift — the stage would run and no trace would show it — and
// TestSpanNamesCoverEveryOp fails when that happens.
var spanNames = map[ovrin.Op]string{
	ovrin.OpDetect:    "ovrin.detect",
	ovrin.OpAcquire:   "ovrin.acquire",
	ovrin.OpRender:    "ovrin.render",
	ovrin.OpOCR:       "ovrin.ocr",
	ovrin.OpNormalise: "ovrin.normalise",
	ovrin.OpSchema:    "ovrin.schema",
	ovrin.OpPrompt:    "ovrin.prompt",
	ovrin.OpGenerate:  "ovrin.generate",
	ovrin.OpValidate:  "ovrin.validate",
	ovrin.OpGround:    "ovrin.ground",

	// OpScore has a name here because the vocabulary is documented and a
	// future core that emits a distinct scoring event should get this span.
	// Today it is not reached: see [emitter.hook].
	ovrin.OpScore: "ovrin.score",
}

// Span attribute keys.
//
// There is deliberately no key for a field name or a field value. Field counts
// are here; values are not representable in an [ovrin.Event] and must not
// become representable here (docs/rules.md §7.5, ADR-0021).
const (
	attrOp         = attribute.Key("ovrin.op")
	attrProvider   = attribute.Key("ovrin.provider")
	attrPage       = attribute.Key("ovrin.page")
	attrAttempt    = attribute.Key("ovrin.attempt")
	attrKind       = attribute.Key("ovrin.kind")
	attrPages      = attribute.Key("ovrin.pages")
	attrBytes      = attribute.Key("ovrin.bytes")
	attrFields     = attribute.Key("ovrin.fields")
	attrConfidence = attribute.Key("ovrin.confidence")
	attrReview     = attribute.Key("ovrin.review")
	attrTokensIn   = attribute.Key("ovrin.tokens.input")
	attrTokensOut  = attribute.Key("ovrin.tokens.output")
	attrPageUnits  = attribute.Key("ovrin.page_units")
)

// Metric names and units.
const (
	metricExtractions   = "ovrin.extractions"
	metricExtractionDur = "ovrin.extraction.duration"
	metricStageDur      = "ovrin.stage.duration"
	metricPages         = "ovrin.pages.processed"
	metricConfidence    = "ovrin.confidence"
	metricReviews       = "ovrin.reviews"
	metricTokens        = "ovrin.tokens"
	metricPageUnits     = "ovrin.page_units"
	metricErrors        = "ovrin.errors"

	unitExtraction = "{extraction}"
	unitSeconds    = "s"
	unitPage       = "{page}"
	unitRatio      = "1"
	unitResult     = "{result}"
	unitToken      = "{token}"
	unitError      = "{error}"
)

// Metric attribute keys.
//
// These are unprefixed, as docs/observability.md specifies: a metric name is
// already namespaced by "ovrin.", and prefixing its attributes as well makes
// every dashboard query twice as long for no disambiguation.
const (
	mattrKind      = attribute.Key("kind")
	mattrOutcome   = attribute.Key("outcome")
	mattrOp        = attribute.Key("op")
	mattrProvider  = attribute.Key("provider")
	mattrReading   = attribute.Key("reading")
	mattrReason    = attribute.Key("reason")
	mattrDirection = attribute.Key("direction")
)

// Outcome values for ovrin.extractions.
//
// docs/observability.md also lists "invalid", which this module cannot emit:
// [ovrin.Event] has no field reporting whether validation passed, so an
// extraction that came back invalid is indistinguishable from one that came
// back clean. Reporting a wrong outcome would be worse than reporting a
// coarser one.
const (
	outcomeOK     = "ok"
	outcomeReview = "review"
	outcomeError  = "error"
)

// directionIn and directionOut split the ovrin.tokens counter, so one
// instrument answers both "what did this cost to send" and "what did it cost
// to receive" without two near-identical metric names.
const (
	directionIn  = "input"
	directionOut = "output"
)

// reasonUnknown is what ovrin.reviews carries for its reason attribute.
//
// docs/observability.md specifies the ReviewReason cause here — low_confidence,
// disagreement, missing_required, ungrounded, cross_field, suspicious — but
// [ovrin.Event] carries only a Review bool, not the reasons. The count is
// therefore real and the breakdown is not available. Guessing a cause would be
// fabrication; carrying the field name instead would leak what ADR-0021 exists
// to keep out of a vendor's index.
const reasonUnknown = "unknown"

// errorKinds classifies an error to one of ovrin's sentinels.
//
// The token is a fixed string from this table and never any part of the
// error's own message: a message from a provider is text ovrin did not write,
// and a provider that quotes a prompt back is how document content reaches a
// trace (docs/rules.md §7.5). Classification also survives a vendor rewording
// a response, which is why nothing here branches on message text
// (docs/rules.md §2.2).
//
// Order is most-specific first, because an error can satisfy errors.Is for
// more than one sentinel.
var errorKinds = []struct {
	sentinel error
	name     string
}{
	{ovrin.ErrUnsupportedFormat, "unsupported_format"},
	{ovrin.ErrUnsupported, "unsupported"},
	{ovrin.ErrNoContent, "no_content"},
	{ovrin.ErrNoProvider, "no_provider"},
	{ovrin.ErrSchema, "schema"},
	{ovrin.ErrLimitExceeded, "limit_exceeded"},
	{ovrin.ErrAuth, "auth"},
	{ovrin.ErrRateLimit, "rate_limit"},
	{ovrin.ErrUnavailable, "unavailable"},
	{ovrin.ErrBadResponse, "bad_response"},
	{ovrin.ErrBadRequest, "bad_request"},
	{ovrin.ErrEncrypted, "encrypted"},
	{ovrin.ErrInternal, "internal"},
}

// kindUnclassified is for an error that matches no sentinel — a transport
// error an adapter passed through, most often. It is a bounded value rather
// than the error's text.
const kindUnclassified = "unclassified"

// errorKind returns the sentinel token for err, or kindUnclassified.
func errorKind(err error) string {
	for _, k := range errorKinds {
		if errors.Is(err, k.sentinel) {
			return k.name
		}
	}
	return kindUnclassified
}
