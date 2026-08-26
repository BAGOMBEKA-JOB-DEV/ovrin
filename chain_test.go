// Provider fallback, tested.
//
// This file did not exist. OCRChain, ModelChain, advances and exhausted were
// the whole of the v0.2 fallback feature and no test referenced any of them —
// including the rule that decides whether a chain advances, which is what
// stands between an expired credential and three providers being billed for
// the same doomed request.

package ovrin_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// stubOCR fails with a fixed error, or succeeds, and counts its calls.
type stubOCR struct {
	name  string
	err   error
	calls int
}

func (s *stubOCR) Name() string { return s.name }

func (s *stubOCR) Recognise(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &ovrin.Recognition{Confidence: 0.9}, nil
}

func ocrErr(kind error) error {
	return fmt.Errorf("stub: %w", kind)
}

// A chain advances only on conditions the next provider might not share.
//
// The distinction is the whole point. Throttling and outages are worth trying
// elsewhere; a bad credential, a request no provider will accept, or a schema
// ovrin itself rejected will fail identically everywhere, and degrading
// silently to the third provider hides a misconfiguration that should be loud.
func TestChainAdvancesOnlyOnRetryableFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		first     error
		wantCalls int // how many providers should be tried
	}{
		{"rate limit advances", ovrin.ErrRateLimit, 2},
		{"an outage advances", ovrin.ErrUnavailable, 2},
		{"an unclassified transport error advances", errors.New("connection reset"), 2},

		{"a bad credential stops the chain", ovrin.ErrAuth, 1},
		{"a request no provider accepts stops the chain", ovrin.ErrBadRequest, 1},
		{"an unsupported format stops the chain", ovrin.ErrUnsupported, 1},
		{"a schema ovrin rejected stops the chain", ovrin.ErrSchema, 1},
		{"an internal error stops the chain", ovrin.ErrInternal, 1},
		{"a cancelled context stops the chain", context.Canceled, 1},
		{"an exceeded deadline stops the chain", context.DeadlineExceeded, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first := &stubOCR{name: "first", err: ocrErr(tc.first)}
			second := &stubOCR{name: "second"}
			chain := ovrin.OCRChain(first, second)

			_, err := chain.Recognise(context.Background(), ovrin.Page{Number: 1})

			if first.calls != 1 {
				t.Errorf("the first provider was called %d times, want 1", first.calls)
			}
			switch tc.wantCalls {
			case 1:
				if second.calls != 0 {
					t.Errorf("the chain advanced past %v to a second provider", tc.first)
				}
				if err == nil {
					t.Error("the chain returned no error although it did not advance")
				}
			case 2:
				if second.calls != 1 {
					t.Errorf("the chain did not advance past %v", tc.first)
				}
				if err != nil {
					t.Errorf("the second provider succeeded but the chain returned %v", err)
				}
			}
		})
	}
}

// Exhausting a chain reports every attempt, not only the last — otherwise the
// first provider's error, which is usually the informative one, is lost.
func TestAnExhaustedChainWrapsEveryAttempt(t *testing.T) {
	t.Parallel()

	first := &stubOCR{name: "alpha", err: ocrErr(ovrin.ErrRateLimit)}
	second := &stubOCR{name: "beta", err: ocrErr(ovrin.ErrUnavailable)}
	chain := ovrin.OCRChain(first, second)

	_, err := chain.Recognise(context.Background(), ovrin.Page{Number: 1})
	if err == nil {
		t.Fatal("an exhausted chain returned no error")
	}
	if !errors.Is(err, ovrin.ErrUnavailable) {
		t.Errorf("err = %v, want it to classify as the last failure", err)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !contains(err.Error(), want) {
			t.Errorf("err = %q, which does not name the %q attempt", err, want)
		}
	}
}

// Every attempt is reported through the hook.
//
// The dangerous failure mode of fallback is not a chain that runs out — that
// one is loud. It is a chain whose first provider fails on every request while
// the second quietly serves them, for weeks, with nobody aware. ADR-0018
// decided the hook would show this; until now nothing did.
func TestEveryChainAttemptReachesTheHook(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	var events []ovrin.Event
	c := ovrin.New(
		ovrin.WithModel(replyModel{reply: map[string]any{"total": 10.0}}),
		ovrin.WithOCR(ovrin.OCRChain(
			&stubOCR{name: "always-throttled", err: ocrErr(ovrin.ErrRateLimit)},
			&stubOCR{name: "the-one-doing-the-work"},
		)),
		ovrin.WithRenderer(stubRenderer{calls: new([]int)}),
		ovrin.WithHook(func(_ context.Context, ev ovrin.Event) {
			events = append(events, ev)
		}),
	)

	const fixture = "testdata/mixed-digital-and-scan.pdf"
	if _, err := ovrin.Extract[Doc](context.Background(), c, ovrin.File(fixture)); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var failed, succeeded int
	for _, ev := range events {
		if ev.Op != ovrin.OpOCR || ev.Provider == "" {
			continue
		}
		if ev.Err != nil {
			failed++
			if ev.Provider != "always-throttled" {
				t.Errorf("a failed attempt was attributed to %q", ev.Provider)
			}
		} else if ev.Provider == "the-one-doing-the-work" {
			succeeded++
		}
	}

	if failed == 0 {
		t.Error("the failing provider's attempts never reached the hook; " +
			"a chain silently running on its fallback is invisible")
	}
	if succeeded == 0 {
		t.Error("the succeeding provider's attempt never reached the hook")
	}
}

// A chain of nothing is a programming error, caught at construction rather
// than as a confusing failure on the first document.
func TestAnEmptyChainPanics(t *testing.T) {
	t.Parallel()

	t.Run("ocr", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("OCRChain() with no providers did not panic")
			}
		}()
		ovrin.OCRChain()
	})

	t.Run("model", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("ModelChain() with no models did not panic")
			}
		}()
		ovrin.ModelChain()
	})
}
