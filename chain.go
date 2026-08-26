package ovrin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OCRChain returns an [OCR] that tries each provider in order.
//
// A chain is an ordinary provider, so the pipeline cannot tell the difference
// and no fallback logic lives in the core. That also means a caller who wants
// circuit breaking, weighted routing or cost-aware selection writes their own
// implementation and passes it to [WithOCR].
//
// It advances on [ErrRateLimit], [ErrUnavailable] and unclassified transport
// errors. It never advances on [ErrAuth], [ErrBadRequest], [ErrUnsupported] or
// [ErrSchema]: a misconfigured credential should fail loudly on the first
// provider rather than silently degrade to the third.
//
// Every attempt is reported through the hook, because the dangerous failure
// mode of fallback is a system running on its worst provider for three weeks
// with nobody aware. Exhausting the chain returns an error wrapping every
// attempt, not only the last.
func OCRChain(providers ...OCR) OCR {
	if len(providers) == 0 {
		panic("ovrin: OCRChain called with no providers")
	}
	return &ocrChain{providers: providers}
}

type ocrChain struct{ providers []OCR }

func (c *ocrChain) Name() string { return "chain" }

func (c *ocrChain) Recognise(ctx context.Context, page Page) (*Recognition, error) {
	var attempts []error
	for i, p := range c.providers {
		start := time.Now()
		rec, err := p.Recognise(ctx, page)
		report(ctx, Event{
			Op: OpOCR, Provider: p.Name(), Page: page.Number,
			Attempt: i + 1, Duration: time.Since(start), Err: err,
		})
		if err == nil {
			return rec, nil
		}
		attempts = append(attempts, fmt.Errorf("%s: %w", p.Name(), err))
		if !advances(err) {
			break
		}
	}
	return nil, exhausted("ocr", attempts)
}

// ModelChain returns a [Model] that tries each model in order, under the same
// advance rules as [OCRChain].
func ModelChain(models ...Model) Model {
	if len(models) == 0 {
		panic("ovrin: ModelChain called with no models")
	}
	return &modelChain{models: models}
}

type modelChain struct{ models []Model }

func (c *modelChain) Generate(ctx context.Context, req ModelRequest) (*ModelResponse, error) {
	var attempts []error
	for i, m := range c.models {
		start := time.Now()
		resp, err := m.Generate(ctx, req)
		report(ctx, Event{
			Op: OpGenerate, Attempt: i + 1, Duration: time.Since(start), Err: err,
		})
		if err == nil {
			return resp, nil
		}
		attempts = append(attempts, fmt.Errorf("model %d: %w", i+1, err))
		if !advances(err) {
			break
		}
	}
	return nil, exhausted("model", attempts)
}

// report emits an event through the hook on ctx, if there is one.
//
// It is what makes the promise above true. A chain that only surfaces failures
// when every provider has failed hides the case that actually costs money: the
// first provider failing on every request while the second quietly serves
// them. That is a system running on its fallback for three weeks with nobody
// aware, and it is the failure ADR-0018 accepted the decorator design in order
// to make visible.
func report(ctx context.Context, ev Event) {
	if h := hookFrom(ctx); h != nil {
		h(ctx, ev)
	}
}

// advances reports whether a chain should try the next provider.
//
// It advances on conditions the next provider might not have — throttling, an
// outage, a transport failure. It does not advance on a bad credential, a
// request no provider will accept, or a schema ovrin itself rejected: those
// will fail identically everywhere, and degrading silently to the third
// provider hides a misconfiguration that should be loud (ADR-0018).
func advances(err error) bool {
	switch {
	case errors.Is(err, ErrAuth),
		errors.Is(err, ErrBadRequest),
		errors.Is(err, ErrUnsupported),
		errors.Is(err, ErrSchema),
		errors.Is(err, ErrInternal),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return false
	}
	return true
}

// exhausted reports every attempt, not only the last.
//
// A chain that reported the final failure alone would say "tesseract is down"
// when the real story was an expired Google credential followed by an AWS
// throttle. The whole sequence is the diagnosis.
func exhausted(what string, attempts []error) error {
	if len(attempts) == 0 {
		return &Error{Kind: ErrNoProvider, Message: "the " + what + " chain is empty"}
	}
	var b strings.Builder
	for i, a := range attempts {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(a.Error())
	}
	last := attempts[len(attempts)-1]
	kind := ErrUnavailable
	for _, sentinel := range []error{ErrAuth, ErrBadRequest, ErrUnsupported, ErrSchema, ErrRateLimit, ErrInternal} {
		if errors.Is(last, sentinel) {
			kind = sentinel
			break
		}
	}
	return (&Error{
		Kind:    kind,
		Message: b.String(),
	}).WithCause(errors.Join(attempts...))
}
