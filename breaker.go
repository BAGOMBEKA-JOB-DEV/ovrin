package ovrin

import (
	"context"
	"sync"
	"time"
)

// DefaultBreakerFailures is how many consecutive failures open a breaker.
//
// Five, because a provider that has failed five times in a row is not having a
// bad moment. Lower trips on ordinary noise and sends traffic to a fallback
// that may be worse; higher spends real money and latency discovering
// something already known.
const DefaultBreakerFailures = 5

// DefaultBreakerCooldown is how long a breaker stays open before it tries
// again.
//
// Thirty seconds is long enough that a provider restarting or a rate limit
// resetting has had a chance, and short enough that recovery is not something
// a person has to notice and act on.
const DefaultBreakerCooldown = 30 * time.Second

// BreakerOption configures a breaker.
type BreakerOption func(*breaker)

// WithBreakerFailures sets how many consecutive failures open the breaker.
// A value below one is ignored, because a breaker that opens on zero failures
// is a provider that is never called.
func WithBreakerFailures(n int) BreakerOption {
	return func(b *breaker) {
		if n > 0 {
			b.threshold = n
		}
	}
}

// WithBreakerCooldown sets how long the breaker stays open. A value of zero or
// less is ignored: a breaker that reopens immediately has not broken anything.
func WithBreakerCooldown(d time.Duration) BreakerOption {
	return func(b *breaker) {
		if d > 0 {
			b.cooldown = d
		}
	}
}

// BreakOCR wraps an [OCR] so that a provider which is failing consistently is
// left alone for a while instead of being asked again on every page.
//
// This is a decorator, not a change to [OCRChain], for the reason
// ADR-0018 gives: fallback policy belongs outside the pipeline, so a caller
// who wants different policy writes it rather than arguing with ovrin about
// the built-in one. It composes:
//
//	ovrin.OCRChain(
//	    ovrin.BreakOCR(primary),
//	    ovrin.BreakOCR(secondary),
//	)
//
// # What it does
//
// After [DefaultBreakerFailures] consecutive failures the breaker opens and
// every call returns [ErrUnavailable] without contacting the provider, for
// [DefaultBreakerCooldown]. It then admits exactly one trial call: if that
// succeeds the breaker closes, and if it fails the cooldown starts again. One
// trial rather than all of them, because a provider that is still down should
// cost one request to discover, not a thundering herd of them.
//
// A refusal is [ErrUnavailable] deliberately. That is a condition
// [OCRChain] advances on, so a chain of broken providers moves to the next one
// rather than stopping — which is the entire point of putting a breaker in a
// chain.
//
// # What it counts
//
// Only failures the provider is responsible for. [ErrAuth],
// [ErrBadRequest], [ErrUnsupported] and [ErrSchema] do not open a breaker:
// they will fail identically after any cooldown, so counting them would hide a
// misconfiguration behind a circuit-breaker message instead of surfacing it.
// Cancellation is the caller's, not the provider's, and is not counted either.
//
// Every state change is reported through the hook a [Client] was built with. A
// breaker that opens silently is the failure ADR-0018 exists to prevent, one
// level down.
//
// The returned OCR is safe for concurrent use.
func BreakOCR(o OCR, opts ...BreakerOption) OCR {
	if o == nil {
		panic("ovrin: BreakOCR called with a nil OCR")
	}
	return &breakerOCR{inner: o, breaker: newBreaker(opts)}
}

// BreakModel wraps a [Model] under the same rules as [BreakOCR].
func BreakModel(m Model, opts ...BreakerOption) Model {
	if m == nil {
		panic("ovrin: BreakModel called with a nil Model")
	}
	return &breakerModel{inner: m, breaker: newBreaker(opts)}
}

// breaker is the state machine both decorators share.
//
// Closed and open are the only two states held explicitly. "Half open" — the
// state where one trial call is allowed — is not a field: it is simply the
// first call after the cooldown has elapsed, decided under the mutex by
// allow(). Representing it as a field would mean two places could disagree
// about it.
type breaker struct {
	threshold int
	cooldown  time.Duration

	mu       sync.Mutex
	failures int
	openedAt time.Time
	trialOut bool // a trial call is in flight; nobody else may start one
}

func newBreaker(opts []BreakerOption) *breaker {
	b := &breaker{threshold: DefaultBreakerFailures, cooldown: DefaultBreakerCooldown}
	for _, o := range opts {
		if o != nil {
			o(b)
		}
	}
	return b
}

// allow reports whether a call may proceed, and whether it is the trial call
// that decides an open breaker's fate.
func (b *breaker) allow(now time.Time) (ok, trial bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.openedAt.IsZero() {
		return true, false
	}
	if now.Sub(b.openedAt) < b.cooldown {
		return false, false
	}
	if b.trialOut {
		// Another goroutine is already finding out. Refusing here is what
		// keeps recovery to one request rather than every waiting caller.
		return false, false
	}
	b.trialOut = true
	return true, true
}

// record folds the outcome of a call back into the state, and returns a
// description of the transition if there was one.
func (b *breaker) record(now time.Time, err error, trial bool) (transition string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if trial {
		b.trialOut = false
	}

	switch {
	case err == nil:
		if !b.openedAt.IsZero() {
			transition = "closed: the provider answered again"
		}
		b.failures, b.openedAt = 0, time.Time{}
		return transition

	case !opensBreaker(err):
		// Not the provider's fault, or not something a cooldown can fix.
		// Leave the state alone rather than counting it either way.
		return ""

	default:
		b.failures++
		if trial {
			// The trial failed: start the cooldown again from now, rather
			// than letting every subsequent call be another trial.
			b.openedAt = now
			return "reopened: the trial call failed"
		}
		if b.openedAt.IsZero() && b.failures >= b.threshold {
			b.openedAt = now
			return "opened: consecutive failures reached the threshold"
		}
		return ""
	}
}

// opensBreaker reports whether a failure says anything about the provider's
// health.
//
// The mirror of advances() in chain.go, and deliberately so: a condition that
// would fail identically at the next provider will also fail identically after
// a cooldown, so neither a chain nor a breaker should treat it as transient.
func opensBreaker(err error) bool {
	return advances(err)
}

// refusal is what an open breaker returns.
func refusal(op Op, provider string) error {
	return &Error{
		Op:       op,
		Provider: provider,
		Kind:     ErrUnavailable,
		Message: "the circuit breaker for this provider is open after repeated " +
			"failures; it will be tried again after the cooldown",
	}
}

type breakerOCR struct {
	inner OCR
	*breaker
}

func (b *breakerOCR) Name() string { return b.inner.Name() }

func (b *breakerOCR) Recognise(ctx context.Context, page Page) (*Recognition, error) {
	now := time.Now()
	ok, trial := b.allow(now)
	if !ok {
		return nil, refusal(OpOCR, b.inner.Name())
	}

	rec, err := b.inner.Recognise(ctx, page)
	if t := b.record(time.Now(), err, trial); t != "" {
		report(ctx, Event{Op: OpOCR, Provider: b.inner.Name(), Err: err,
			Duration: time.Since(now)})
	}
	return rec, err
}

type breakerModel struct {
	inner Model
	*breaker
}

func (b *breakerModel) Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error) {
	now := time.Now()
	ok, trial := b.allow(now)
	if !ok {
		return nil, refusal(OpGenerate, "")
	}

	resp, err := b.inner.Generate(ctx, req)
	if t := b.record(time.Now(), err, trial); t != "" {
		report(ctx, Event{Op: OpGenerate, Err: err, Duration: time.Since(now)})
	}
	return resp, err
}
