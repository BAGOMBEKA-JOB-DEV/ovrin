// Every documented ceiling, tested at the value where it binds.
//
// A cap that never binds is worse than no cap: it reads as a safety property
// in the documentation, produces no `capped:` signal, and nothing notices. One
// of these — CapUngrounded — was exactly that, because its condition required
// both that grounding had run and that it had not.

package ovrin

import (
	"context"
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

// Result.Confidence must be the aggregate of the confidences the caller can
// see in Result.Fields.
//
// It was not. Cross-field rules run after every field has been scored and
// rescore the fields they read, but the aggregate was averaged from a snapshot
// taken before that — so a document with a failing cross-field rule reported a
// headline confidence that no arithmetic over Fields could reproduce. The
// whole promise of this library is that the number is checkable.
func TestAggregateMatchesTheFieldsAfterCrossFieldRules(t *testing.T) {
	t.Parallel()

	type Invoice struct {
		Subtotal float64 `ovrin:"subtotal,required,min=0"`
		Tax      float64 `ovrin:"tax,required,min=0"`
		Total    float64 `ovrin:"total,required,min=0"`
	}

	// 10 + 2 != 99. The rule fails, and every field it read is rescored.
	c := New(
		WithModel(fixedModel{`{"subtotal":10,"tax":2,"total":99}`}),
		WithCrossField(Sum("total", Tolerance{Absolute: 0.01}, "subtotal", "tax")),
	)

	res, err := Extract[Invoice](context.Background(), c,
		Bytes([]byte("subtotal,tax,total\n10.00,2.00,99.00\n")))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Recompute the documented aggregate from what the caller can see:
	// the mean over fields, required weighted 1 and optional 0.5. Every field
	// here is required.
	var sum float64
	for _, k := range []string{"subtotal", "tax", "total"} {
		f, ok := res.Fields[k]
		if !ok {
			t.Fatalf("field %q is missing from the result", k)
		}
		sum += f.Confidence
	}
	want := sum / 3

	if math.Abs(res.Confidence-want) > 0.005 {
		t.Errorf("Result.Confidence = %.4f, but the fields average %.4f; "+
			"the headline number cannot be reproduced from Fields",
			res.Confidence, want)
	}
}

// fixedModel returns one reply, whatever it is asked.
type fixedModel struct{ body string }

func (m fixedModel) Generate(context.Context, ModelRequest) (*ModelResponse, error) {
	return &ModelResponse{JSON: []byte(m.body)}, nil
}

// An ambiguous date is not a malformed one.
//
// docs/schema.md: "the format signal drops. It does not drop to zero — the
// text is a well-formed date". It used to drop to zero: validate.FormatSignal
// had the ambiguity branch, the assembler called it and discarded the answer
// with `_ = v`, and the scorer's own formatSignal only ever returned 1 or 0.
// So 03/04/2026 scored the same as "not a date at all".
func TestAnAmbiguousFormatDropsButNotToZero(t *testing.T) {
	t.Parallel()

	base := []RuleResult{{Rule: "format=date", Passed: true}}

	unambiguous, _ := defaultScorer{}.Score(FieldEvidence{
		Found: true, Validation: base,
	})
	ambiguous, signals := defaultScorer{}.Score(FieldEvidence{
		Found: true, Validation: base, Ambiguous: true,
	})

	if !(ambiguous < unambiguous) {
		t.Errorf("ambiguous %.2f is not below unambiguous %.2f; the signal did not drop",
			ambiguous, unambiguous)
	}
	if ambiguous == 0 {
		t.Error("an ambiguous date scored zero; it is a well-formed date")
	}

	var format *Signal
	for i := range signals {
		if signals[i].Name == SignalFormat {
			format = &signals[i]
		}
	}
	if format == nil {
		t.Fatal("no format signal at all")
	}
	if format.Value <= 0 || format.Value >= 1 {
		t.Errorf("format signal = %.2f, want strictly between 0 and 1", format.Value)
	}
	// The note has to say why, or a reviewer sees a lowered number with no
	// explanation — which is the failure Explain exists to prevent.
	if !contains(format.Note, "ambiguous") {
		t.Errorf("format note = %q, which does not mention ambiguity", format.Note)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
