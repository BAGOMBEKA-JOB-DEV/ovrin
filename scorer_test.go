// Every documented ceiling, tested at the value where it binds.
//
// A cap that never binds is worse than no cap: it reads as a safety property
// in the documentation, produces no `capped:` signal, and nothing notices. One
// of these — CapUngrounded — was exactly that, because its condition required
// both that grounding had run and that it had not.

package ovrin

import (
	"math"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/ground"
)

func TestCapsBind(t *testing.T) {
	t.Parallel()

	// A field the model returned that is nowhere in the document. Grounding
	// ran — there is a Provenance entry, because ground reported Applicable —
	// and found nothing. Every rule passed, because a fabricated value is
	// usually well formed; that is what makes fabrication dangerous and why
	// the ceiling exists (ADR-0013).
	fabricated := FieldEvidence{
		Found:      true,
		Grounding:  ground.NotFound,
		Provenance: []Provenance{{Page: 1, Method: "text"}},
		Validation: []RuleResult{
			{Rule: "required", Passed: true},
			{Rule: "min=0", Passed: true},
			// A format that parsed. This is the shape of the worked example
			// in docs/explainability.md, and it matters: with grounding and
			// schema alone the weighted mean is already 0.33, below the
			// ceiling, so the cap would not bind and the test would prove
			// nothing about the cap.
			{Rule: "format=currency", Passed: true},
		},
	}

	tests := []struct {
		name    string
		ev      FieldEvidence
		wantCap string
		wantMax float64
	}{
		{
			// The case docs/explainability.md documents as its worked
			// example. Before the fix this scored 0.40 with no cap line, so
			// the document described arithmetic that could not happen.
			name:    "a fabricated value is capped at CapUngrounded",
			ev:      fabricated,
			wantCap: "capped:grounding",
			wantMax: CapUngrounded,
		},
		{
			name: "a broken rule is capped at CapRuleFailed",
			ev: FieldEvidence{
				Found:      true,
				Grounding:  1.0,
				Provenance: []Provenance{{Page: 1, Method: "text"}},
				Validation: []RuleResult{{Rule: "min=100", Passed: false}},
			},
			wantCap: "capped:rule",
			wantMax: CapRuleFailed,
		},
		{
			name: "two readings that disagree are capped at CapDisagreement",
			ev: FieldEvidence{
				Found:      true,
				Grounding:  1.0,
				Provenance: []Provenance{{Page: 1, Method: "text"}},
				Validation: []RuleResult{{Rule: "required", Passed: true}},
				Candidates: []Candidate{{Value: "25000"}, {Value: "2500"}},
			},
			wantCap: "capped:disagreement",
			wantMax: CapDisagreement,
		},
		{
			name: "suspicious content is capped at CapSuspicious",
			ev: FieldEvidence{
				Found:      true,
				Grounding:  1.0,
				Provenance: []Provenance{{Page: 1, Method: "text"}},
				Validation: []RuleResult{{Rule: "required", Passed: true}},
				Suspicious: true,
			},
			wantCap: "capped:suspicious",
			wantMax: CapSuspicious,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			conf, signals := defaultScorer{}.Score(tc.ev)

			if conf > tc.wantMax+1e-9 {
				t.Errorf("confidence = %.2f, above the ceiling of %.2f", conf, tc.wantMax)
			}
			if !hasSignal(signals, tc.wantCap) {
				var names []string
				for _, s := range signals {
					names = append(names, s.Name)
				}
				t.Errorf("no %q signal; the cap did not bind. signals = %v",
					tc.wantCap, names)
			}
		})
	}
}

// A field grounded verbatim with every rule passing must not be capped at all
// — otherwise the ceilings are not ceilings, they are the score.
func TestACleanFieldIsNotCapped(t *testing.T) {
	t.Parallel()

	conf, signals := defaultScorer{}.Score(FieldEvidence{
		Found:      true,
		Grounding:  ground.Verbatim,
		Provenance: []Provenance{{Page: 1, Method: "text"}},
		Validation: []RuleResult{{Rule: "required", Passed: true}},
	})

	for _, s := range signals {
		if len(s.Name) >= len(capPrefix) && s.Name[:len(capPrefix)] == capPrefix {
			t.Errorf("a clean field carried a cap signal: %s (%s)", s.Name, s.Note)
		}
	}
	if conf < CapUngrounded {
		t.Errorf("confidence = %.2f for a verbatim, fully valid field", conf)
	}
}

// Grounding that never ran must not be treated as grounding that found
// nothing. A vision-only reading has no text to ground against, and scoring it
// as a fabrication would penalise every field on every scanned page.
func TestGroundingThatDidNotRunIsNotACap(t *testing.T) {
	t.Parallel()

	conf, signals := defaultScorer{}.Score(FieldEvidence{
		Found:      true,
		Grounding:  ground.NotFound, // zero, because nothing was attempted
		Provenance: nil,             // ground reported not Applicable
		Validation: []RuleResult{{Rule: "required", Passed: true}},
	})

	if hasSignal(signals, SignalGrounding) {
		t.Error("a grounding signal was emitted although grounding never ran")
	}
	if hasSignal(signals, "capped:grounding") {
		t.Error("the ungrounded cap bound although grounding never ran")
	}
	if math.Abs(conf-1.0) > 1e-9 {
		t.Errorf("confidence = %.2f; with only a passing schema signal it should be 1.00", conf)
	}
}
