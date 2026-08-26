package eval

import (
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// TestObserve covers the reduction from an [ovrin.Result] to the three things
// scoring needs.
//
// This is where a bug would quietly change every number in every report, and
// it is testable without a provider because a Result is a struct: the test
// builds one by hand.
func TestObserve(t *testing.T) {
	res := &ovrin.Result[struct{}]{
		Confidence: 0.71,
		Valid:      false,
		Fields: map[string]ovrin.FieldResult{
			"total":  {Value: 1463200.0, Found: true, Confidence: 0.93},
			"due":    {Found: false, Confidence: 0.0},
			"vendor": {Value: "Acme", Found: true, Confidence: 0.55},
		},
		Reasons: []ovrin.ReviewReason{
			{Field: "vendor", Why: "value not found in source; may be inferred"},
			{Field: "due", Why: "a required field is absent"},
		},
		Metadata: ovrin.Metadata{
			Usage:    ovrin.Usage{InputTokens: 2100, OutputTokens: 180, PageUnits: 1},
			Duration: 5 * time.Second,
		},
	}

	o := Observe(res, 7*time.Second)

	if o.Confidence != 0.71 || o.Valid {
		t.Errorf("aggregate fields not carried across: %+v", o)
	}
	if o.Usage.InputTokens != 2100 || o.Usage.OutputTokens != 180 || o.Usage.PageUnits != 1 {
		t.Errorf("usage = %+v", o.Usage)
	}
	if o.Duration != 7*time.Second {
		t.Errorf("duration = %v; the measured elapsed time should win over the metadata", o.Duration)
	}
	if len(o.Fields) != 3 {
		t.Fatalf("fields = %d, want 3; absent fields must survive the reduction", len(o.Fields))
	}

	total := o.Fields["total"]
	if !total.Found || total.Value != 1463200.0 || total.Confidence != 0.93 || total.Flagged {
		t.Errorf("total = %+v", total)
	}
	// Found is not Value != zero: an absent field must stay absent rather than
	// becoming a zero somebody could pay.
	if due := o.Fields["due"]; due.Found || due.Value != nil {
		t.Errorf("due = %+v, want absent with no value", due)
	}
	for _, k := range []string{"vendor", "due"} {
		if !o.Fields[k].Flagged {
			t.Errorf("%s was named in a review reason but is not flagged", k)
		}
	}
	if o.Fields["total"].Flagged {
		t.Error("total was flagged but no reason named it")
	}
	if o.Failed {
		t.Error("a successful extraction was recorded as failed")
	}
}

// TestObserveFallsBackToPipelineDuration checks the case where the caller has
// no elapsed time of its own, so that latency is never silently reported as
// zero.
func TestObserveFallsBackToPipelineDuration(t *testing.T) {
	res := &ovrin.Result[struct{}]{
		Fields:   map[string]ovrin.FieldResult{},
		Metadata: ovrin.Metadata{Duration: 3 * time.Second},
	}
	if o := Observe(res, 0); o.Duration != 3*time.Second {
		t.Errorf("duration = %v, want 3s", o.Duration)
	}
}
