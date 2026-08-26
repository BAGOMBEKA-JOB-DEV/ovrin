package ovrinotel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// harness is an emitter wired to in-memory exporters.
//
// OTel's own tracetest and ManualReader are used rather than a hand-rolled
// double, so what the tests assert is what an SDK would actually export. No
// test in this module touches the network (docs/rules.md §3.3).
type harness struct {
	*emitter
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
	tp     *sdktrace.TracerProvider
	mp     *sdkmetric.MeterProvider
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// Both providers are shut down, so a test that leaves a background
	// exporter goroutine running fails here rather than in somebody else's
	// test (docs/rules.md §3.6).
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
		if err := mp.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown: %v", err)
		}
	})

	return &harness{
		emitter: newEmitter(tp, mp),
		spans:   sr,
		reader:  reader,
		tp:      tp,
		mp:      mp,
	}
}

// ended returns the spans that have finished, in the order they finished.
func (h *harness) ended() []sdktrace.ReadOnlySpan { return h.spans.Ended() }

// collect gathers the current metric state.
func (h *harness) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

// metricByName finds one instrument's collected data, and whether it was
// recorded to at all.
func (h *harness) metricByName(t *testing.T, name string) (metricdata.Metrics, bool) {
	t.Helper()
	for _, sm := range h.collect(t).ScopeMetrics {
		if sm.Scope.Name != scopeName {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// sumInt64 totals an Int64 counter's points and returns the attribute sets
// seen, so a test can assert both the number and how it was labelled.
func sumInt64(t *testing.T, m metricdata.Metrics) (int64, []map[string]string) {
	t.Helper()
	s, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %s is %T, want Sum[int64]", m.Name, m.Data)
	}
	var total int64
	attrs := make([]map[string]string, 0, len(s.DataPoints))
	for _, dp := range s.DataPoints {
		total += dp.Value
		attrs = append(attrs, attrMap(dp.Attributes.ToSlice()))
	}
	return total, attrs
}

// histFloat64 returns the recorded values of a Float64 histogram and their
// attribute sets.
func histFloat64(t *testing.T, m metricdata.Metrics) ([]float64, []map[string]string) {
	t.Helper()
	h, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %s is %T, want Histogram[float64]", m.Name, m.Data)
	}
	vals := make([]float64, 0, len(h.DataPoints))
	attrs := make([]map[string]string, 0, len(h.DataPoints))
	for _, dp := range h.DataPoints {
		vals = append(vals, dp.Sum)
		attrs = append(attrs, attrMap(dp.Attributes.ToSlice()))
	}
	return vals, attrs
}

// allAttrs flattens every attribute of every data point of one instrument, so
// a test can sweep the whole metric surface without knowing which instrument
// kind it is looking at.
func allAttrs(t *testing.T, m metricdata.Metrics) map[string]string {
	t.Helper()
	out := map[string]string{}
	add := func(set attribute.Set) {
		for _, kv := range set.ToSlice() {
			out[string(kv.Key)] = kv.Value.Emit()
		}
	}
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	case metricdata.Sum[float64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	case metricdata.Histogram[int64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	case metricdata.Histogram[float64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	case metricdata.Gauge[int64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	case metricdata.Gauge[float64]:
		for _, dp := range d.DataPoints {
			add(dp.Attributes)
		}
	default:
		t.Fatalf("metric %s has unhandled data type %T; the canary sweep must cover every instrument kind", m.Name, m.Data)
	}
	return out
}
