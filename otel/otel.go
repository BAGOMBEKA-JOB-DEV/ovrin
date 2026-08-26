// Package ovrinotel turns ovrin's hooks into OpenTelemetry spans and metrics.
//
// The core emits [ovrin.Event] values and depends on nothing. This module
// carries the OpenTelemetry graph so that a user who wants no telemetry
// carries none of it, and a user on a different stack — statsd, Prometheus
// directly, structured logs — writes a five-line hook instead of adopting OTel
// to get anything at all. See docs/adr/0021-observability.md.
//
//	c := ovrin.New(
//	    ovrin.WithModel(model),
//	    ovrinotel.Option(tracerProvider, meterProvider),
//	)
//
// # No document content, ever
//
// Nothing this module emits can carry a value read from a document. Field
// counts appear; field names and field values do not, and an error's message
// is classified to one of ovrin's sentinels rather than copied. Traces and
// metrics are shipped to third-party vendors, so an attribute holding an
// extracted value would send somebody's medical record to a SaaS the
// document's subject never heard of. [ovrin.Event] is built so that value
// cannot reach here; this module is written so it cannot arrive by another
// route.
//
// # What a trace shows, and what it does not
//
// A hook is called once per stage, as that stage completes, with the duration
// already measured. OpenTelemetry wants a start and an end, and this module is
// only ever handed endings, so each span is emitted at the end of its stage
// with its start reconstructed as end minus [ovrin.Event.Duration]. Durations
// and ordering are therefore accurate to the clock.
//
// The cost is nesting. Stage spans are siblings, parented to whatever span the
// caller already had in the context, and the ovrin.extract span that covers the
// whole call is emitted last — it spans the stages in time but is not their
// parent, because a parent has to exist before its children are created and at
// the first event the extraction has not finished. Reconstructing the tree
// would mean holding per-extraction state keyed on something in the context,
// and there is nothing in the context to key on that two concurrent
// extractions could not collide over; a trace that merges two documents is
// worse than a flat one. So: read a trace for stage timings and for which
// provider served what. Do not read it for a call tree.
package ovrinotel

import (
	"context"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Option returns an [ovrin.Option] that reports every pipeline stage to
// OpenTelemetry.
//
// It is called Option and not Hook because [ovrin.Hook] is already a type in
// the core, and a function named Hook returning an Option would leave a reader
// guessing which of the two they had.
//
// Either provider may be nil, which turns that signal off: a caller who wants
// traces without the cost of a metrics pipeline, or metrics without the
// sampling decisions of a tracer, should not have to construct a no-op
// provider to say so. Passing nil for both is legal and emits nothing.
//
// The returned Option configures a [ovrin.Client] and is refused by
// [ovrin.Extract], as every provider option is.
func Option(tp trace.TracerProvider, mp metric.MeterProvider) ovrin.Option {
	return ovrin.WithHook(newEmitter(tp, mp).hook)
}

// emitter holds the tracer and instruments for one configured Client.
//
// It carries no per-extraction state, and that is deliberate: hooks run on
// whatever goroutine the stage ran on, several at once for a document read
// concurrently, and any state keyed on a guess about which extraction an event
// belongs to would be wrong exactly when two documents are in flight.
type emitter struct {
	tracer trace.Tracer
	ins    *instruments
}

// newEmitter resolves the providers to a tracer and a set of instruments.
//
// It exists separately from [Option] so that the conversion from event to
// telemetry can be tested by calling it, rather than by running a whole
// extraction through a client: [ovrin.Option] is a closed interface, so a test
// holding one has no way to reach the hook inside it.
func newEmitter(tp trace.TracerProvider, mp metric.MeterProvider) *emitter {
	e := &emitter{}
	if tp != nil {
		e.tracer = tp.Tracer(scopeName)
	}
	if mp != nil {
		e.ins = newInstruments(mp.Meter(scopeName))
	}
	return e
}

// hook is the [ovrin.Hook] this module installs.
func (e *emitter) hook(ctx context.Context, ev ovrin.Event) {
	// Taken once, before any work, so the reconstructed start time is not
	// pushed later by the cost of reporting.
	end := time.Now()

	// The core folds the final scoring stage and the extraction summary into a
	// single OpScore event whose Duration is the wall time of the whole call
	// and which carries Confidence and Review. Labelling that ovrin.score
	// would report the entire extraction as time spent scoring, so it becomes
	// the root span instead. If the core ever emits a distinct per-stage
	// scoring event this needs revisiting, and spanNames already holds the
	// name it would use.
	if ev.Op == ovrin.OpScore {
		e.extraction(ctx, ev, end)
		return
	}
	e.stage(ctx, ev, end)
}

// stage emits the span and metrics for one completed pipeline stage.
func (e *emitter) stage(ctx context.Context, ev ovrin.Event, end time.Time) {
	if name, ok := spanNames[ev.Op]; ok {
		e.span(ctx, name, ev, stageAttrs(ev), end)
	}
	// An event whose Op is not in the table still counts. A stage that cannot
	// be named is exactly the one an operator needs to see in the error
	// counter rather than lose (docs/rules.md §6.1).
	if e.ins != nil {
		e.ins.stage(ctx, ev)
	}
}

// extraction emits the root span and the extraction-level metrics.
func (e *emitter) extraction(ctx context.Context, ev ovrin.Event, end time.Time) {
	e.span(ctx, spanExtract, ev, rootAttrs(ev), end)
	if e.ins != nil {
		e.ins.extraction(ctx, ev)
	}
}

// span records one already-finished interval.
//
// The span is started with an explicit timestamp in the past and ended with
// the timestamp of the event, which is the only way to give an SDK a duration
// that was measured before the span existed.
func (e *emitter) span(ctx context.Context, name string, ev ovrin.Event, attrs []attribute.KeyValue, end time.Time) {
	if e.tracer == nil {
		return
	}
	start := end.Add(-ev.Duration)
	_, span := e.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(start),
		trace.WithAttributes(attrs...),
	)
	if ev.Err != nil {
		// The description is the sentinel token from a fixed table, never the
		// error's own text and never trace.Span.RecordError, which would put
		// an unbounded provider message into the span. A provider that quotes
		// a prompt back in an error is how document content reaches a trace
		// (docs/rules.md §7.5).
		span.SetStatus(codes.Error, errorKind(ev.Err))
	}
	span.End(trace.WithTimestamp(end))
}

// stageAttrs are the attributes of one stage span.
//
// A zero-valued field is left off rather than reported as zero. Page zero
// means "whole document" and not "page nought", and an attribute asserting
// nought bytes when the stage never counted bytes is a claim nobody made.
func stageAttrs(ev ovrin.Event) []attribute.KeyValue {
	a := make([]attribute.KeyValue, 0, 10)
	a = append(a, attrOp.String(ev.Op.String()))
	if ev.Provider != "" {
		a = append(a, attrProvider.String(ev.Provider))
	}
	if ev.Page > 0 {
		a = append(a, attrPage.Int(ev.Page))
	}
	if ev.Attempt > 0 {
		a = append(a, attrAttempt.Int(ev.Attempt))
	}
	if ev.Pages > 0 {
		a = append(a, attrPages.Int(ev.Pages))
	}
	if ev.Bytes > 0 {
		a = append(a, attrBytes.Int64(ev.Bytes))
	}
	if ev.Fields > 0 {
		a = append(a, attrFields.Int(ev.Fields))
	}
	return append(a, usageAttrs(ev.Usage)...)
}

// rootAttrs are the attributes of the ovrin.extract span.
//
// Confidence and Review appear here and nowhere else, because they are
// judgements about the whole result rather than about a stage.
//
// ovrin.op is absent: the root span is not a stage, and stamping it "score"
// because that is the Op the core happened to attach to the final event would
// describe the span wrongly.
func rootAttrs(ev ovrin.Event) []attribute.KeyValue {
	a := make([]attribute.KeyValue, 0, 9)
	a = append(a, attrKind.String(documentKind))
	if ev.Pages > 0 {
		a = append(a, attrPages.Int(ev.Pages))
	}
	if ev.Fields > 0 {
		a = append(a, attrFields.Int(ev.Fields))
	}
	if ev.Bytes > 0 {
		a = append(a, attrBytes.Int64(ev.Bytes))
	}
	a = append(a,
		attrConfidence.Float64(ev.Confidence),
		attrReview.Bool(ev.Review),
	)
	return append(a, usageAttrs(ev.Usage)...)
}

// usageAttrs reports what a stage or an extraction cost.
//
// These are counts of tokens and page units — what a provider bills for. They
// say nothing about what was in the document, which is why cost attribution is
// possible at all without shipping content anywhere.
func usageAttrs(u ovrin.Usage) []attribute.KeyValue {
	var a []attribute.KeyValue
	if u.InputTokens > 0 {
		a = append(a, attrTokensIn.Int(u.InputTokens))
	}
	if u.OutputTokens > 0 {
		a = append(a, attrTokensOut.Int(u.OutputTokens))
	}
	if u.PageUnits > 0 {
		a = append(a, attrPageUnits.Int(u.PageUnits))
	}
	return a
}
