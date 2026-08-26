package ovrin_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// scriptedOCR fails a fixed number of times, then succeeds.
type scriptedOCR struct {
	mu       sync.Mutex
	failures int // remaining failures to serve
	err      error
	calls    int
}

func (s *scriptedOCR) Name() string { return "scripted" }

func (s *scriptedOCR) Recognise(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		return nil, s.err
	}
	return &ovrin.Recognition{Confidence: 0.9}, nil
}

func (s *scriptedOCR) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func breakerErr(kind error) error { return fmt.Errorf("scripted: %w", kind) }

// After enough consecutive failures the breaker stops asking.
//
// The point is cost. Without it, a provider that is down is contacted once per
// page of every document, each call paying a full network timeout, and the
// caller waits for all of them to discover something the first three already
// established.
func TestABreakerOpensAndStopsCallingTheProvider(t *testing.T) {
	t.Parallel()

	inner := &scriptedOCR{failures: 100, err: breakerErr(ovrin.ErrUnavailable)}
	b := ovrin.BreakOCR(inner,
		ovrin.WithBreakerFailures(3),
		ovrin.WithBreakerCooldown(time.Hour), // long enough not to reopen mid-test
	)

	for i := 0; i < 10; i++ {
		if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err == nil {
			t.Fatalf("call %d unexpectedly succeeded", i+1)
		}
	}

	if got := inner.callCount(); got != 3 {
		t.Errorf("the provider was called %d times, want 3 — the breaker did not open", got)
	}
}

// An open breaker refuses with ErrUnavailable specifically, because that is a
// condition OCRChain advances on. A breaker that refused with anything else
// would stop a chain dead, which is the opposite of what it is for.
func TestAnOpenBreakerRefusesInAWayAChainAdvancesPast(t *testing.T) {
	t.Parallel()

	broken := &scriptedOCR{failures: 100, err: breakerErr(ovrin.ErrUnavailable)}
	healthy := &stubOCR{name: "healthy"}

	chain := ovrin.OCRChain(
		ovrin.BreakOCR(broken, ovrin.WithBreakerFailures(1), ovrin.WithBreakerCooldown(time.Hour)),
		healthy,
	)

	for i := 0; i < 5; i++ {
		if _, err := chain.Recognise(context.Background(), ovrin.Page{Number: 1}); err != nil {
			t.Fatalf("call %d: the chain failed although a healthy provider follows: %v", i+1, err)
		}
	}

	if got := broken.callCount(); got != 1 {
		t.Errorf("the broken provider was called %d times, want 1", got)
	}
	if healthy.calls != 5 {
		t.Errorf("the healthy provider served %d calls, want 5", healthy.calls)
	}
}

// After the cooldown the breaker admits exactly one trial call, and closes if
// it succeeds.
func TestABreakerRecoversAfterTheCooldown(t *testing.T) {
	t.Parallel()

	inner := &scriptedOCR{failures: 2, err: breakerErr(ovrin.ErrUnavailable)}
	b := ovrin.BreakOCR(inner,
		ovrin.WithBreakerFailures(2),
		ovrin.WithBreakerCooldown(20*time.Millisecond),
	)

	// Trip it.
	for i := 0; i < 2; i++ {
		if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err == nil {
			t.Fatal("a failing provider returned no error")
		}
	}
	if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err == nil {
		t.Fatal("the breaker did not open")
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("the provider was called %d times while open, want 2", got)
	}

	time.Sleep(40 * time.Millisecond)

	// The provider is healthy again; the trial call should reach it.
	if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err != nil {
		t.Fatalf("the trial call failed: %v", err)
	}
	// And the breaker should now be closed, not admitting one call per
	// cooldown.
	if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err != nil {
		t.Errorf("the breaker did not close after a successful trial: %v", err)
	}
}

// A failure the provider is not responsible for must not open the breaker.
//
// A bad credential fails identically after any cooldown. Counting it would
// replace "your key is wrong" with "the circuit breaker is open", which sends
// the reader looking for an outage that is not happening.
func TestFailuresThatACooldownCannotFixDoNotOpenTheBreaker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind error
	}{
		{"a bad credential", ovrin.ErrAuth},
		{"a request no provider accepts", ovrin.ErrBadRequest},
		{"an unsupported format", ovrin.ErrUnsupported},
		{"a schema ovrin rejected", ovrin.ErrSchema},
		{"a cancelled context", context.Canceled},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			inner := &scriptedOCR{failures: 100, err: breakerErr(tc.kind)}
			b := ovrin.BreakOCR(inner,
				ovrin.WithBreakerFailures(2),
				ovrin.WithBreakerCooldown(time.Hour),
			)

			for i := 0; i < 6; i++ {
				_, err := b.Recognise(context.Background(), ovrin.Page{Number: 1})
				if !errors.Is(err, tc.kind) {
					t.Fatalf("call %d: err = %v, want the provider's own error", i+1, err)
				}
			}
			if got := inner.callCount(); got != 6 {
				t.Errorf("the provider was called %d times, want 6 — the breaker "+
					"opened on a failure a cooldown cannot fix", got)
			}
		})
	}
}

// A success resets the count, so occasional failures scattered over a long run
// never accumulate into an open breaker.
func TestASuccessResetsTheFailureCount(t *testing.T) {
	t.Parallel()

	inner := &scriptedOCR{err: breakerErr(ovrin.ErrUnavailable)}
	b := ovrin.BreakOCR(inner, ovrin.WithBreakerFailures(3), ovrin.WithBreakerCooldown(time.Hour))

	// fail, fail, succeed — twice over. Six calls, never three in a row.
	for round := 0; round < 2; round++ {
		inner.mu.Lock()
		inner.failures = 2
		inner.mu.Unlock()
		for i := 0; i < 2; i++ {
			if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err == nil {
				t.Fatal("a failing provider returned no error")
			}
		}
		if _, err := b.Recognise(context.Background(), ovrin.Page{Number: 1}); err != nil {
			t.Fatalf("round %d: the healthy call failed: %v", round, err)
		}
	}

	if got := inner.callCount(); got != 6 {
		t.Errorf("the provider was called %d times, want 6 — the breaker opened "+
			"although it never saw three consecutive failures", got)
	}
}

// Only one caller may test the water. Every other concurrent caller is refused
// until that trial resolves, or recovery costs a thundering herd.
func TestOnlyOneTrialCallIsAdmittedAtATime(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var inFlight, peak int
	var mu sync.Mutex

	slow := ocrFunc(func(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		<-release

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil, breakerErr(ovrin.ErrUnavailable)
	})

	b := ovrin.BreakOCR(slow, ovrin.WithBreakerFailures(1), ovrin.WithBreakerCooldown(time.Millisecond))

	// Trip it with one failure.
	go func() { close(release) }()
	_, _ = b.Recognise(context.Background(), ovrin.Page{Number: 1})

	// Now hold the trial call open and pile callers in behind it.
	release = make(chan struct{})
	time.Sleep(5 * time.Millisecond) // past the cooldown

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Recognise(context.Background(), ovrin.Page{Number: 1})
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if peak > 1 {
		t.Errorf("%d trial calls reached the provider at once, want at most 1", peak)
	}
}

// ocrFunc adapts a function to the OCR interface.
type ocrFunc func(context.Context, ovrin.Page) (*ovrin.Recognition, error)

func (ocrFunc) Name() string { return "func" }

func (f ocrFunc) Recognise(ctx context.Context, p ovrin.Page) (*ovrin.Recognition, error) {
	return f(ctx, p)
}

func TestBreakerConstructorsRejectNil(t *testing.T) {
	t.Parallel()

	t.Run("ocr", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("BreakOCR(nil) did not panic")
			}
		}()
		ovrin.BreakOCR(nil)
	})
	t.Run("model", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("BreakModel(nil) did not panic")
			}
		}()
		ovrin.BreakModel(nil)
	})
}
