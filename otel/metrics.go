package ovrinotel

import (
	"context"
	"fmt"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"go.opentelemetry.io/otel/metric"
)

// instruments holds every instrument this module records to.
//
// They are created once, when the [Option] is built, rather than per event: a
// Meter deduplicates instruments by name but doing that lookup a thousand
// times for a thousand-page document is work nobody asked for, and creating
// them up front turns a bad instrument name into a panic at construction
// instead of silence at runtime.
type instruments struct {
	extractions   metric.Int64Counter
	extractionDur metric.Float64Histogram
	stageDur      metric.Float64Histogram
	pages         metric.Int64Counter
	confidence    metric.Float64Histogram
	reviews       metric.Int64Counter
	tokens        metric.Int64Counter
	pageUnits     metric.Int64Counter
	errs          metric.Int64Counter
}

// newInstruments builds every instrument, panicking if the MeterProvider
// refuses one.
//
// Every name and unit here is a constant this package controls, so a refusal
// is a bug in this package or a broken provider, not a condition a caller can
// recover from. Returning an error would put it in [Option]'s signature, where
// it would be checked once by everybody and mean nothing; degrading silently
// to no metrics would violate docs/rules.md §6.1.
func newInstruments(m metric.Meter) *instruments {
	must := func(name string, err error) {
		if err != nil {
			panic(fmt.Sprintf("ovrin/otel: the MeterProvider refused instrument %s: %v", name, err))
		}
	}
	var ins instruments
	var err error

	ins.extractions, err = m.Int64Counter(metricExtractions,
		metric.WithUnit(unitExtraction),
		metric.WithDescription("Extractions completed, by document kind and outcome."))
	must(metricExtractions, err)

	ins.extractionDur, err = m.Float64Histogram(metricExtractionDur,
		metric.WithUnit(unitSeconds),
		metric.WithDescription("Wall time of one Extract call."))
	must(metricExtractionDur, err)

	ins.stageDur, err = m.Float64Histogram(metricStageDur,
		metric.WithUnit(unitSeconds),
		metric.WithDescription("Wall time of one pipeline stage."))
	must(metricStageDur, err)

	ins.pages, err = m.Int64Counter(metricPages,
		metric.WithUnit(unitPage),
		metric.WithDescription("Pages read, by which reading served them."))
	must(metricPages, err)

	ins.confidence, err = m.Float64Histogram(metricConfidence,
		metric.WithUnit(unitRatio),
		metric.WithDescription("Aggregate confidence of a completed extraction."))
	must(metricConfidence, err)

	ins.reviews, err = m.Int64Counter(metricReviews,
		metric.WithUnit(unitResult),
		metric.WithDescription("Results flagged for human review."))
	must(metricReviews, err)

	ins.tokens, err = m.Int64Counter(metricTokens,
		metric.WithUnit(unitToken),
		metric.WithDescription("Model tokens consumed, by provider and direction."))
	must(metricTokens, err)

	ins.pageUnits, err = m.Int64Counter(metricPageUnits,
		metric.WithUnit(unitPage),
		metric.WithDescription("Page units consumed, which is what OCR providers bill."))
	must(metricPageUnits, err)

	ins.errs, err = m.Int64Counter(metricErrors,
		metric.WithUnit(unitError),
		metric.WithDescription("Stage failures, by stage, sentinel and provider."))
	must(metricErrors, err)

	return &ins
}

// documentKind is what every metric carries for its kind attribute.
//
// docs/observability.md specifies the detected [ovrin.Kind] there, and
// [ovrin.Event] has no field carrying it — the format is resolved by the
// detect stage and then never reported to a hook. "unknown" is ovrin's own
// rendering of [ovrin.KindUnknown], so this is the library saying it does not
// know rather than this module inventing an answer.
//
// It is emitted rather than omitted so the attribute set is stable: a metric
// whose attributes come and go is two time series that look like one.
var documentKind = ovrin.KindUnknown.String()

// providerNone is the provider attribute for a stage no adapter served —
// normalisation, schema reflection and prompt construction are all core work.
// It is distinct from "unknown", which would claim a provider existed.
const providerNone = "none"

// stage records the metrics for one completed pipeline stage.
func (ins *instruments) stage(ctx context.Context, ev ovrin.Event) {
	ins.stageDur.Record(ctx, ev.Duration.Seconds(), metric.WithAttributes(
		mattrOp.String(ev.Op.String()),
		mattrProvider.String(providerOf(ev)),
	))

	if n := pagesRead(ev); n > 0 && ev.Err == nil {
		ins.pages.Add(ctx, n, metric.WithAttributes(
			mattrReading.String(readingOf(ev)),
		))
	}

	ins.usage(ctx, ev)
	ins.failure(ctx, ev)
}

// extraction records the metrics for one completed Extract call.
//
// Usage is deliberately not counted here. [ovrin.Metadata.Usage], which is
// what the final event carries, is the total across every provider call, so
// adding it to the per-stage counts would bill every token twice. It also
// carries no provider, and provider is an attribute the metric is specified to
// have.
func (ins *instruments) extraction(ctx context.Context, ev ovrin.Event) {
	kind := metric.WithAttributes(mattrKind.String(documentKind))

	ins.extractions.Add(ctx, 1, metric.WithAttributes(
		mattrKind.String(documentKind),
		mattrOutcome.String(outcomeOf(ev)),
	))
	ins.extractionDur.Record(ctx, ev.Duration.Seconds(), kind)

	// A failed extraction has no meaningful aggregate confidence, and
	// recording its zero would drag the distribution down with a number that
	// was never a judgement about a document.
	if ev.Err == nil {
		ins.confidence.Record(ctx, ev.Confidence, kind)
	}

	if ev.Review {
		ins.reviews.Add(ctx, 1, metric.WithAttributes(
			mattrReason.String(reasonUnknown),
		))
	}

	ins.failure(ctx, ev)
}

// usage counts what a stage consumed, by provider.
func (ins *instruments) usage(ctx context.Context, ev ovrin.Event) {
	if n := ev.Usage.InputTokens; n > 0 {
		ins.tokens.Add(ctx, int64(n), metric.WithAttributes(
			mattrProvider.String(providerOf(ev)),
			mattrDirection.String(directionIn),
		))
	}
	if n := ev.Usage.OutputTokens; n > 0 {
		ins.tokens.Add(ctx, int64(n), metric.WithAttributes(
			mattrProvider.String(providerOf(ev)),
			mattrDirection.String(directionOut),
		))
	}
	if n := ev.Usage.PageUnits; n > 0 {
		ins.pageUnits.Add(ctx, int64(n), metric.WithAttributes(
			mattrProvider.String(providerOf(ev)),
		))
	}
}

// failure counts a stage that returned an error, classified to a sentinel.
func (ins *instruments) failure(ctx context.Context, ev ovrin.Event) {
	if ev.Err == nil {
		return
	}
	ins.errs.Add(ctx, 1, metric.WithAttributes(
		mattrOp.String(ev.Op.String()),
		mattrKind.String(errorKind(ev.Err)),
		mattrProvider.String(providerOf(ev)),
	))
}

// outcomeOf classifies a finished extraction.
//
// Order matters: an extraction that failed is an error even if it also wanted
// review, because the error is the condition an operator has to act on.
func outcomeOf(ev ovrin.Event) string {
	switch {
	case ev.Err != nil:
		return outcomeError
	case ev.Review:
		return outcomeReview
	default:
		return outcomeOK
	}
}

// providerOf returns the adapter name, or providerNone.
func providerOf(ev ovrin.Event) string {
	if ev.Provider == "" {
		return providerNone
	}
	return ev.Provider
}

// pagesRead returns how many pages a stage read.
//
// Only acquisition and OCR are counted. Rendering rasterises a page that OCR
// then reads, so counting both would report every scanned page twice, and the
// remaining stages work on the document as a whole.
func pagesRead(ev ovrin.Event) int64 {
	switch ev.Op {
	case ovrin.OpAcquire, ovrin.OpOCR:
	default:
		return 0
	}
	switch {
	case ev.Pages > 0:
		return int64(ev.Pages)
	case ev.Page > 0:
		return 1
	default:
		return 0
	}
}

// readingOf says which reading served a stage.
//
// [ovrin.Event] does not carry an [ovrin.Reading], so only OCR can be
// identified: an acquisition event looks identical whether a PDF text layer or
// a vision model produced the content. The rest report unknown rather than a
// guess.
func readingOf(ev ovrin.Event) string {
	if ev.Op == ovrin.OpOCR {
		return ovrin.ReadingOCR.String()
	}
	return ovrin.ReadingUnknown.String()
}
