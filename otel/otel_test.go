package ovrinotel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// attrMap renders an attribute slice as name to printed value, which is what
// the assertions compare. Emit is used rather than AsString so that an int or
// a bool attribute is compared as what it is.
func attrMap(kvs []attribute.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.Emit()
	}
	return m
}

func spanByName(spans []sdktrace.ReadOnlySpan, name string) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range spans {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

func TestStageEventsBecomeNamedSpans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event ovrin.Event
		span  string
		attrs map[string]string
	}{
		{
			name:  "detect reports bytes and pages but no provider",
			event: ovrin.Event{Op: ovrin.OpDetect, Duration: 3 * time.Millisecond, Bytes: 4096, Pages: 2},
			span:  "ovrin.detect",
			attrs: map[string]string{"ovrin.op": "detect", "ovrin.bytes": "4096", "ovrin.pages": "2"},
		},
		{
			name:  "ocr reports the provider and the page it read",
			event: ovrin.Event{Op: ovrin.OpOCR, Provider: "tesseract", Page: 3, Duration: time.Second},
			span:  "ovrin.ocr",
			attrs: map[string]string{"ovrin.op": "ocr", "ovrin.provider": "tesseract", "ovrin.page": "3"},
		},
		{
			name:  "generate reports the attempt and what it cost",
			event: ovrin.Event{Op: ovrin.OpGenerate, Provider: "skyl", Attempt: 2, Duration: 2 * time.Second, Usage: ovrin.Usage{InputTokens: 900, OutputTokens: 120}},
			span:  "ovrin.generate",
			attrs: map[string]string{
				"ovrin.op": "generate", "ovrin.provider": "skyl", "ovrin.attempt": "2",
				"ovrin.tokens.input": "900", "ovrin.tokens.output": "120",
			},
		},
		{
			name:  "schema reports a field count and never a field name",
			event: ovrin.Event{Op: ovrin.OpSchema, Duration: time.Microsecond, Fields: 11},
			span:  "ovrin.schema",
			attrs: map[string]string{"ovrin.op": "schema", "ovrin.fields": "11"},
		},
		{
			name:  "render is a page rasterised",
			event: ovrin.Event{Op: ovrin.OpRender, Provider: "pdfium", Page: 1, Duration: 40 * time.Millisecond},
			span:  "ovrin.render",
			attrs: map[string]string{"ovrin.op": "render", "ovrin.provider": "pdfium", "ovrin.page": "1"},
		},
		{
			name:  "page units reach a span from an ocr reading",
			event: ovrin.Event{Op: ovrin.OpOCR, Provider: "textract", Pages: 7, Duration: time.Second, Usage: ovrin.Usage{PageUnits: 7}},
			span:  "ovrin.ocr",
			attrs: map[string]string{"ovrin.op": "ocr", "ovrin.provider": "textract", "ovrin.pages": "7", "ovrin.page_units": "7"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.hook(context.Background(), tt.event)

			spans := h.ended()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			if got := spans[0].Name(); got != tt.span {
				t.Errorf("span name = %q, want %q", got, tt.span)
			}
			got := attrMap(spans[0].Attributes())
			if len(got) != len(tt.attrs) {
				t.Errorf("attributes = %v, want exactly %v", got, tt.attrs)
			}
			for k, want := range tt.attrs {
				if got[k] != want {
					t.Errorf("attribute %s = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

func TestSpanIntervalIsReconstructedFromDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
	}{
		{"a stage that took no measurable time", 0},
		{"a millisecond stage", time.Millisecond},
		{"a slow model call", 9 * time.Second},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			before := time.Now()
			h.hook(context.Background(), ovrin.Event{Op: ovrin.OpPrompt, Duration: tt.d})
			after := time.Now()

			spans := h.ended()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			s := spans[0]
			if got := s.EndTime().Sub(s.StartTime()); got != tt.d {
				t.Errorf("span duration = %v, want %v", got, tt.d)
			}
			// The end is the moment the hook was called, so it must fall
			// inside the window the test observed.
			if s.EndTime().Before(before) || s.EndTime().After(after) {
				t.Errorf("span ended at %v, outside [%v, %v]", s.EndTime(), before, after)
			}
		})
	}
}

func TestStageSpanIsParentedToTheCallersSpan(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx, caller := h.tp.Tracer("test").Start(context.Background(), "caller")
	h.hook(ctx, ovrin.Event{Op: ovrin.OpDetect, Duration: time.Millisecond})
	caller.End()

	spans := h.ended()
	stage, ok := spanByName(spans, "ovrin.detect")
	if !ok {
		t.Fatalf("no ovrin.detect span in %v", spans)
	}
	if got, want := stage.Parent().SpanID(), caller.SpanContext().SpanID(); got != want {
		t.Errorf("parent span = %v, want the caller's %v", got, want)
	}
}

func TestFinalEventBecomesTheRootSpan(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.hook(context.Background(), ovrin.Event{
		Op: ovrin.OpScore, Duration: 4 * time.Second,
		Pages: 3, Fields: 12, Confidence: 0.87, Review: true,
		Usage: ovrin.Usage{InputTokens: 900, OutputTokens: 120, PageUnits: 3},
	})

	spans := h.ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got := spans[0].Name(); got != "ovrin.extract" {
		t.Fatalf("span name = %q, want ovrin.extract", got)
	}

	want := map[string]string{
		"ovrin.kind": "unknown", "ovrin.pages": "3", "ovrin.fields": "12",
		"ovrin.confidence": "0.87", "ovrin.review": "true",
		"ovrin.tokens.input": "900", "ovrin.tokens.output": "120", "ovrin.page_units": "3",
	}
	got := attrMap(spans[0].Attributes())
	if len(got) != len(want) {
		t.Errorf("attributes = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("attribute %s = %q, want %q", k, got[k], v)
		}
	}
	// ovrin.op names a stage, and the root span is not one.
	if _, ok := got["ovrin.op"]; ok {
		t.Errorf("root span carries ovrin.op = %q; it is not a stage", got["ovrin.op"])
	}
}

func TestErrorSetsStatusToTheSentinelAndNeverTheMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		kind string
	}{
		{"a rate limited provider", fmt.Errorf("wrapped: %w", ovrin.ErrRateLimit), "rate_limit"},
		{"an unsupported format", ovrin.ErrUnsupportedFormat, "unsupported_format"},
		{"a provider that cannot do it", ovrin.ErrUnsupported, "unsupported"},
		{"a limit reached", &ovrin.Error{Op: ovrin.OpAcquire, Kind: ovrin.ErrLimitExceeded}, "limit_exceeded"},
		{"an internal failure", ovrin.ErrInternal, "internal"},
		{"a bare transport error", errors.New("connection reset by peer"), "unclassified"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.hook(context.Background(), ovrin.Event{
				Op: ovrin.OpOCR, Provider: "tesseract", Duration: time.Millisecond, Err: tt.err,
			})

			spans := h.ended()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			st := spans[0].Status()
			if st.Code != codes.Error {
				t.Errorf("status code = %v, want Error", st.Code)
			}
			if st.Description != tt.kind {
				t.Errorf("status description = %q, want the sentinel %q", st.Description, tt.kind)
			}
			if n := len(spans[0].Events()); n != 0 {
				t.Errorf("got %d span events, want 0: RecordError would copy the message", n)
			}

			m, ok := h.metricByName(t, "ovrin.errors")
			if !ok {
				t.Fatalf("ovrin.errors was not recorded")
			}
			total, attrs := sumInt64(t, m)
			if total != 1 {
				t.Errorf("ovrin.errors = %d, want 1", total)
			}
			if len(attrs) != 1 {
				t.Fatalf("got %d attribute sets, want 1", len(attrs))
			}
			want := map[string]string{"op": "ocr", "kind": tt.kind, "provider": "tesseract"}
			for k, v := range want {
				if attrs[0][k] != v {
					t.Errorf("attribute %s = %q, want %q", k, attrs[0][k], v)
				}
			}
		})
	}
}

func TestNilProvidersEmitNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withTracer bool
		withMeter  bool
		wantSpans  int
	}{
		{"neither signal", false, false, 0},
		{"metrics only", false, true, 0},
		{"traces only", true, false, 1},
		{"both", true, true, 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			full := newHarness(t)

			var e *emitter
			switch {
			case tt.withTracer && tt.withMeter:
				e = newEmitter(full.tp, full.mp)
			case tt.withTracer:
				e = newEmitter(full.tp, nil)
			case tt.withMeter:
				e = newEmitter(nil, full.mp)
			default:
				e = newEmitter(nil, nil)
			}

			e.hook(context.Background(), ovrin.Event{Op: ovrin.OpDetect, Duration: time.Millisecond})
			if got := len(full.ended()); got != tt.wantSpans {
				t.Errorf("got %d spans, want %d", got, tt.wantSpans)
			}

			_, recorded := full.metricByName(t, "ovrin.stage.duration")
			if recorded != tt.withMeter {
				t.Errorf("ovrin.stage.duration recorded = %v, want %v", recorded, tt.withMeter)
			}
		})
	}
}

func TestOptionIsAnOvrinOption(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	tests := []struct {
		name string
		opt  ovrin.Option
	}{
		{"both providers", Option(h.tp, h.mp)},
		{"no providers at all", Option(nil, nil)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.opt == nil {
				t.Fatal("Option returned nil")
			}
			// New accepts it, which is the only thing a caller does with it.
			if c := ovrin.New(tt.opt); c == nil {
				t.Fatal("New returned nil")
			}
			// It configures a Client, so Extract must refuse it rather than
			// silently ignore it.
			if _, err := ovrin.Extract[struct{}](context.Background(), ovrin.New(), ovrin.Bytes(nil), tt.opt); err == nil {
				t.Error("Extract accepted a provider option; it should be refused")
			}
		})
	}
}

// TestNoDocumentContentReachesTelemetry is the assertion ADR-0021 exists for.
//
// The realistic leak is not somebody adding a "value" attribute on purpose; it
// is an error message. A provider that echoes a prompt back, or a parser that
// quotes the bytes it choked on, produces an error whose text is document
// content, and RecordError or a status description built from err.Error()
// would put it in a vendor's search index.
func TestNoDocumentContentReachesTelemetry(t *testing.T) {
	t.Parallel()

	const canary = "CANARY-NHS-4432-1198-MEDICAL-RECORD"

	events := []ovrin.Event{
		{Op: ovrin.OpDetect, Duration: time.Millisecond, Bytes: 900},
		{Op: ovrin.OpAcquire, Duration: time.Millisecond, Pages: 2, Err: errors.New(canary)},
		{Op: ovrin.OpOCR, Provider: "tesseract", Page: 1, Duration: time.Millisecond,
			Err: fmt.Errorf("provider said %q: %w", canary, ovrin.ErrBadResponse)},
		{Op: ovrin.OpNormalise, Duration: time.Millisecond, Bytes: int64(len(canary))},
		{Op: ovrin.OpGenerate, Provider: "skyl", Attempt: 1, Duration: time.Millisecond,
			Err: (&ovrin.Error{Op: ovrin.OpGenerate, Provider: "skyl", Kind: ovrin.ErrBadResponse, Message: canary}).WithCause(errors.New(canary))},
		{Op: ovrin.OpValidate, Duration: time.Millisecond, Fields: 4},
		{Op: ovrin.OpGround, Duration: time.Millisecond},
		{Op: ovrin.OpScore, Duration: 2 * time.Second, Fields: 4, Pages: 2, Confidence: 0.4, Review: true},
	}

	h := newHarness(t)
	for _, ev := range events {
		h.hook(context.Background(), ev)
	}

	contains := func(t *testing.T, where, s string) {
		t.Helper()
		if strings.Contains(s, canary) {
			t.Errorf("document content escaped into %s: %q", where, s)
		}
	}

	spans := h.ended()
	if len(spans) != len(events) {
		t.Fatalf("got %d spans for %d events; a sweep over nothing proves nothing", len(spans), len(events))
	}
	for _, s := range spans {
		contains(t, "a span name", s.Name())
		contains(t, "a span status description", s.Status().Description)
		for _, kv := range s.Attributes() {
			contains(t, "span attribute key "+string(kv.Key), string(kv.Key))
			contains(t, "span attribute "+string(kv.Key), kv.Value.Emit())
		}
		for _, ev := range s.Events() {
			contains(t, "a span event name", ev.Name)
			for _, kv := range ev.Attributes {
				contains(t, "span event attribute "+string(kv.Key), kv.Value.Emit())
			}
		}
		for _, l := range s.Links() {
			for _, kv := range l.Attributes {
				contains(t, "span link attribute "+string(kv.Key), kv.Value.Emit())
			}
		}
	}

	swept := 0
	for _, sm := range h.collect(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			swept++
			contains(t, "a metric name", m.Name)
			contains(t, "a metric description", m.Description)
			for k, v := range allAttrs(t, m) {
				contains(t, "metric attribute key "+k, k)
				contains(t, "metric attribute "+k, v)
			}
		}
	}
	if swept == 0 {
		t.Fatal("no metrics were collected; a sweep over nothing proves nothing")
	}
}
