package ovrin

import "context"

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
	_ = ctx
	_ = page
	panic("ovrin: OCRChain.Recognise is not implemented yet")
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
	_ = ctx
	_ = req
	panic("ovrin: ModelChain.Generate is not implemented yet")
}
