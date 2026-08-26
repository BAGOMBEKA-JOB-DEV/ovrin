package ovrinotel

import (
	"context"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// fullRun is one extraction's worth of events: every stage, then the final
// event the core sends when the result is assembled.
//
// It is deliberately the shape the current pipeline produces — an acquisition
// that reports pages, an OCR reading that reports page units, a model call
// that reports tokens — so a test asserting metric totals is asserting
// something a real document would produce.
func fullRun() []ovrin.Event {
	return []ovrin.Event{
		{Op: ovrin.OpDetect, Duration: time.Millisecond, Bytes: 40960, Pages: 2},
		{Op: ovrin.OpRender, Provider: "pdfium", Page: 1, Duration: 30 * time.Millisecond},
		{Op: ovrin.OpRender, Provider: "pdfium", Page: 2, Duration: 30 * time.Millisecond},
		{Op: ovrin.OpOCR, Provider: "tesseract", Pages: 2, Duration: 900 * time.Millisecond, Usage: ovrin.Usage{PageUnits: 2}},
		{Op: ovrin.OpNormalise, Duration: 2 * time.Millisecond, Bytes: 8192},
		{Op: ovrin.OpSchema, Duration: time.Microsecond, Fields: 12},
		{Op: ovrin.OpPrompt, Duration: time.Microsecond},
		{Op: ovrin.OpGenerate, Provider: "skyl", Attempt: 1, Duration: 2 * time.Second, Usage: ovrin.Usage{InputTokens: 900, OutputTokens: 120}},
		{Op: ovrin.OpValidate, Duration: time.Millisecond, Fields: 12},
		{Op: ovrin.OpGround, Duration: 5 * time.Millisecond},
		{Op: ovrin.OpScore, Duration: 3 * time.Second, Pages: 2, Fields: 12,
			Confidence: 0.91, Usage: ovrin.Usage{InputTokens: 900, OutputTokens: 120, PageUnits: 2}},
	}
}

func drive(t *testing.T, events []ovrin.Event) *harness {
	t.Helper()
	h := newHarness(t)
	for _, ev := range events {
		h.hook(context.Background(), ev)
	}
	return h
}

// TestEveryDocumentedMetricIsEmitted checks the names and units in
// docs/observability.md against what an extraction actually produces. They are
// API: renaming one breaks a dashboard.
func TestEveryDocumentedMetricIsEmitted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		metric string
		unit   string
	}{
		{"ovrin.extractions", "{extraction}"},
		{"ovrin.extraction.duration", "s"},
		{"ovrin.stage.duration", "s"},
		{"ovrin.pages.processed", "{page}"},
		{"ovrin.confidence", "1"},
		{"ovrin.tokens", "{token}"},
		{"ovrin.page_units", "{page}"},
	}

	h := drive(t, fullRun())
	for _, tt := range tests {
		tt := tt
		t.Run(tt.metric, func(t *testing.T) {
			m, ok := h.metricByName(t, tt.metric)
			if !ok {
				t.Fatalf("%s was never recorded", tt.metric)
			}
			if m.Unit != tt.unit {
				t.Errorf("unit = %q, want %q", m.Unit, tt.unit)
			}
		})
	}
}

// TestReviewsAndErrorsAreEmittedWhenTheyHappen covers the two instruments a
// clean run does not touch.
func TestReviewsAndErrorsAreEmittedWhenTheyHappen(t *testing.T) {
	t.Parallel()

	h := drive(t, []ovrin.Event{
		{Op: ovrin.OpOCR, Provider: "tesseract", Duration: time.Millisecond, Err: ovrin.ErrUnavailable},
		{Op: ovrin.OpScore, Duration: time.Second, Confidence: 0.3, Review: true},
	})

	reviews, ok := h.metricByName(t, "ovrin.reviews")
	if !ok {
		t.Fatal("ovrin.reviews was never recorded")
	}
	if reviews.Unit != "{result}" {
		t.Errorf("ovrin.reviews unit = %q, want {result}", reviews.Unit)
	}
	total, attrs := sumInt64(t, reviews)
	if total != 1 {
		t.Errorf("ovrin.reviews = %d, want 1", total)
	}
	if len(attrs) != 1 || attrs[0]["reason"] != "unknown" {
		t.Errorf("ovrin.reviews attributes = %v, want reason=unknown", attrs)
	}

	errs, ok := h.metricByName(t, "ovrin.errors")
	if !ok {
		t.Fatal("ovrin.errors was never recorded")
	}
	if errs.Unit != "{error}" {
		t.Errorf("ovrin.errors unit = %q, want {error}", errs.Unit)
	}
}

func TestExtractionMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   ovrin.Event
		outcome string
		// wantConfidence is whether the confidence histogram was recorded to.
		wantConfidence bool
	}{
		{
			name:           "a clean result",
			event:          ovrin.Event{Op: ovrin.OpScore, Duration: time.Second, Confidence: 0.95},
			outcome:        "ok",
			wantConfidence: true,
		},
		{
			name:           "a result that wants a person",
			event:          ovrin.Event{Op: ovrin.OpScore, Duration: time.Second, Confidence: 0.4, Review: true},
			outcome:        "review",
			wantConfidence: true,
		},
		{
			name:  "a failure, which has no meaningful confidence",
			event: ovrin.Event{Op: ovrin.OpScore, Duration: time.Second, Err: ovrin.ErrNoProvider, Review: true},
			// An error outranks review: the error is what an operator acts on.
			outcome:        "error",
			wantConfidence: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := drive(t, []ovrin.Event{tt.event})

			m, ok := h.metricByName(t, "ovrin.extractions")
			if !ok {
				t.Fatal("ovrin.extractions was never recorded")
			}
			total, attrs := sumInt64(t, m)
			if total != 1 {
				t.Errorf("ovrin.extractions = %d, want 1", total)
			}
			if len(attrs) != 1 {
				t.Fatalf("got %d attribute sets, want 1", len(attrs))
			}
			if attrs[0]["outcome"] != tt.outcome {
				t.Errorf("outcome = %q, want %q", attrs[0]["outcome"], tt.outcome)
			}
			if attrs[0]["kind"] != "unknown" {
				t.Errorf("kind = %q, want unknown: Event carries no Kind", attrs[0]["kind"])
			}

			dur, ok := h.metricByName(t, "ovrin.extraction.duration")
			if !ok {
				t.Fatal("ovrin.extraction.duration was never recorded")
			}
			vals, _ := histFloat64(t, dur)
			if len(vals) != 1 || vals[0] != tt.event.Duration.Seconds() {
				t.Errorf("extraction duration = %v, want %v", vals, tt.event.Duration.Seconds())
			}

			if _, ok := h.metricByName(t, "ovrin.confidence"); ok != tt.wantConfidence {
				t.Errorf("ovrin.confidence recorded = %v, want %v", ok, tt.wantConfidence)
			}
		})
	}
}

// TestPagesAreCountedOnce guards the one arithmetic mistake this module can
// make: rendering a page and then recognising it is one page read, not two.
func TestPagesAreCountedOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		events  []ovrin.Event
		want    int64
		reading string
	}{
		{
			name: "a scanned document rendered then recognised",
			events: []ovrin.Event{
				{Op: ovrin.OpRender, Provider: "pdfium", Page: 1, Duration: time.Millisecond},
				{Op: ovrin.OpRender, Provider: "pdfium", Page: 2, Duration: time.Millisecond},
				{Op: ovrin.OpOCR, Provider: "tesseract", Pages: 2, Duration: time.Millisecond},
			},
			want:    2,
			reading: "ocr",
		},
		{
			name: "a text layer, whose reading the event cannot name",
			events: []ovrin.Event{
				{Op: ovrin.OpAcquire, Pages: 5, Duration: time.Millisecond},
			},
			want:    5,
			reading: "unknown",
		},
		{
			name: "a single page recognised on its own",
			events: []ovrin.Event{
				{Op: ovrin.OpOCR, Provider: "tesseract", Page: 1, Duration: time.Millisecond},
			},
			want:    1,
			reading: "ocr",
		},
		{
			name: "a failed reading counts no pages",
			events: []ovrin.Event{
				{Op: ovrin.OpOCR, Provider: "tesseract", Pages: 3, Duration: time.Millisecond, Err: ovrin.ErrUnavailable},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := drive(t, tt.events)

			m, ok := h.metricByName(t, "ovrin.pages.processed")
			if tt.want == 0 {
				if ok {
					total, _ := sumInt64(t, m)
					t.Errorf("ovrin.pages.processed = %d, want no record at all", total)
				}
				return
			}
			if !ok {
				t.Fatal("ovrin.pages.processed was never recorded")
			}
			total, attrs := sumInt64(t, m)
			if total != tt.want {
				t.Errorf("ovrin.pages.processed = %d, want %d", total, tt.want)
			}
			if len(attrs) != 1 || attrs[0]["reading"] != tt.reading {
				t.Errorf("attributes = %v, want reading=%s", attrs, tt.reading)
			}
		})
	}
}

// TestUsageIsCountedPerStageAndNotAgainAtTheEnd is the double-billing guard.
//
// The final event carries Metadata.Usage, which is the total across every
// provider call. Counting it as well as the stages that produced it would
// report twice the tokens anybody was charged for.
func TestUsageIsCountedPerStageAndNotAgainAtTheEnd(t *testing.T) {
	t.Parallel()

	h := drive(t, fullRun())

	tokens, ok := h.metricByName(t, "ovrin.tokens")
	if !ok {
		t.Fatal("ovrin.tokens was never recorded")
	}
	total, attrs := sumInt64(t, tokens)
	if want := int64(900 + 120); total != want {
		t.Errorf("ovrin.tokens = %d, want %d (the final event's totals must not be counted again)", total, want)
	}
	seen := map[string]bool{}
	for _, a := range attrs {
		if a["provider"] != "skyl" {
			t.Errorf("ovrin.tokens provider = %q, want skyl", a["provider"])
		}
		seen[a["direction"]] = true
	}
	if !seen["input"] || !seen["output"] {
		t.Errorf("ovrin.tokens directions = %v, want both input and output", seen)
	}

	units, ok := h.metricByName(t, "ovrin.page_units")
	if !ok {
		t.Fatal("ovrin.page_units was never recorded")
	}
	total, attrs = sumInt64(t, units)
	if total != 2 {
		t.Errorf("ovrin.page_units = %d, want 2", total)
	}
	if len(attrs) != 1 || attrs[0]["provider"] != "tesseract" {
		t.Errorf("ovrin.page_units attributes = %v, want provider=tesseract", attrs)
	}
}

// TestStageDurationCarriesOpAndProvider checks the attribute a cost or latency
// dashboard groups by, including the case nobody thinks about: a stage that no
// adapter served.
func TestStageDurationCarriesOpAndProvider(t *testing.T) {
	t.Parallel()

	h := drive(t, []ovrin.Event{
		{Op: ovrin.OpNormalise, Duration: 2 * time.Millisecond},
		{Op: ovrin.OpGenerate, Provider: "skyl", Duration: time.Second},
	})

	m, ok := h.metricByName(t, "ovrin.stage.duration")
	if !ok {
		t.Fatal("ovrin.stage.duration was never recorded")
	}
	_, attrs := histFloat64(t, m)
	got := map[string]string{}
	for _, a := range attrs {
		got[a["op"]] = a["provider"]
	}
	want := map[string]string{"normalise": "none", "generate": "skyl"}
	for op, provider := range want {
		if got[op] != provider {
			t.Errorf("stage %s provider = %q, want %q", op, got[op], provider)
		}
	}
}
