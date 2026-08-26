package ovrin

import (
	"context"
	"runtime"
)

// Option configures a [Client], or a single [Extract] call.
//
// The interface is closed — apply is unexported — so options cannot be
// implemented outside this package. That keeps the configuration surface a
// thing ovrin controls, and it keeps godoc honest: an exported func type over
// an unexported struct would render as func(*config), naming a type the reader
// cannot see.
type Option interface {
	apply(*config)
}

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// config is the resolved configuration for one extraction. It is unexported so
// that adding a knob is never a breaking change.
type config struct {
	model    Model
	ocr      OCR
	renderer Renderer
	scorer   Scorer
	hook     Hook

	reading   ReadingMode
	dateOrder DateOrder

	reviewThreshold     float64
	minTextDensity      float64
	maxReplacementRatio float64
	minDecodableRatio   float64

	maxSourceBytes       int64
	maxDecompressedBytes int64
	maxStreamBytes       int64
	maxTextBytes         int64
	maxPages             int
	maxDepth             int
	maxObjects           int
	maxPagePixels        int
	concurrency          int
}

// defaults returns the configuration an unconfigured Client starts from.
func defaults() config {
	n := runtime.GOMAXPROCS(0)
	if n > 4 {
		n = 4
	}
	return config{
		reading:             ReadingAuto,
		reviewThreshold:     DefaultReviewThreshold,
		minTextDensity:      DefaultMinTextDensity,
		maxReplacementRatio: DefaultMaxReplacementRatio,
		minDecodableRatio:   DefaultMinDecodableRatio,

		maxSourceBytes:       DefaultMaxSourceBytes,
		maxDecompressedBytes: DefaultMaxDecompressedBytes,
		maxStreamBytes:       DefaultMaxStreamBytes,
		maxTextBytes:         DefaultMaxTextBytes,
		maxPages:             DefaultMaxPages,
		maxDepth:             DefaultMaxDepth,
		maxObjects:           DefaultMaxObjects,
		maxPagePixels:        DefaultMaxPagePixels,
		concurrency:          n,
	}
}

// DateOrder resolves ambiguous numeric dates such as 03/04/2026.
//
// The zero value does not guess. An ambiguous date is flagged for review
// instead, because silently reading 3 April as 4 March is exactly the kind of
// confidently wrong answer this library exists to catch.
type DateOrder string

const (
	// DateOrderUnknown flags ambiguous dates rather than resolving them.
	DateOrderUnknown DateOrder = ""

	// DayFirst reads 03/04/2026 as 3 April.
	DayFirst DateOrder = "dmy"

	// MonthFirst reads 03/04/2026 as 4 March.
	MonthFirst DateOrder = "mdy"

	// YearFirst reads 2026/03/04 as 4 March.
	YearFirst DateOrder = "ymd"
)

// Client holds the providers, limits and policy an extraction runs under.
//
// Build one and share it: every method is safe for concurrent use by multiple
// goroutines, and an [Extract] call never mutates it. A service processing
// trusted internal documents and untrusted public uploads should hold two,
// with different limits.
type Client struct {
	cfg config
}

// New returns a Client configured by opts.
//
// The only required option is [WithModel]. New panics if given a nil Model,
// because that is programmer error at construction and the alternative is a
// nil dereference on the first extraction, thousands of lines from the
// mistake. Omitting WithModel entirely is configuration rather than a mistake,
// and surfaces as [ErrNoProvider] from Extract.
func New(opts ...Option) *Client {
	cfg := defaults()
	for _, o := range opts {
		if o == nil {
			continue
		}
		o.apply(&cfg)
	}
	return &Client{cfg: cfg}
}

// WithModel sets the model that turns document content into structured JSON.
// Required.
func WithModel(m Model) Option {
	if m == nil {
		panic("ovrin: WithModel called with a nil Model")
	}
	return optionFunc(func(c *config) { c.model = m })
}

// WithOCR sets the provider used for pages with no usable text layer.
//
// Without one, a scanned document reaches a vision-capable model if there is
// one, and otherwise fails with [ErrNoProvider].
func WithOCR(o OCR) Option {
	if o == nil {
		panic("ovrin: WithOCR called with a nil OCR")
	}
	return optionFunc(func(c *config) { c.ocr = o })
}

// WithRenderer sets the renderer used to rasterise pages for OCR.
//
// Not needed for images, nor for a [DocumentOCR] provider that accepts a PDF
// and rasterises server-side.
func WithRenderer(r Renderer) Option {
	if r == nil {
		panic("ovrin: WithRenderer called with a nil Renderer")
	}
	return optionFunc(func(c *config) { c.renderer = r })
}

// WithScorer replaces the confidence scorer.
//
// The default is a weighted mean over the signals that applied, with hard
// floors. A caller with labelled documents can fit a better one to their own
// corpus; the consequence is that confidence is then comparable within that
// deployment rather than across organisations.
func WithScorer(s Scorer) Option {
	if s == nil {
		panic("ovrin: WithScorer called with a nil Scorer")
	}
	return optionFunc(func(c *config) { c.scorer = s })
}

// WithHook sets a function called once per pipeline stage.
//
// Hooks run synchronously on the calling goroutine.
func WithHook(h Hook) Option {
	if h == nil {
		panic("ovrin: WithHook called with a nil Hook")
	}
	return optionFunc(func(c *config) { c.hook = h })
}

// WithReading selects how a document is read.
//
// The default, [ReadingAuto], tries the text layer first because when it works
// it is exact and nearly free. [ModeBoth] runs two independent readings and
// compares them, which roughly doubles cost and is the strongest quality
// signal available.
func WithReading(m ReadingMode) Option {
	return optionFunc(func(c *config) { c.reading = m })
}

// WithDateOrder resolves ambiguous numeric dates for a corpus whose convention
// you know. Without it they are flagged rather than guessed.
func WithDateOrder(d DateOrder) Option {
	return optionFunc(func(c *config) { c.dateOrder = d })
}

// Extract reads src and returns it as a T.
//
// T is a struct whose fields carry `ovrin:"…"` tags describing what to extract.
// Reflection over it happens once per Client per type, and a malformed schema
// is [ErrSchema] raised before any provider is contacted, so a typo costs
// nothing.
//
// opts override the Client's configuration for this call only; the Client is
// not modified, so concurrent extractions with different options cannot
// interfere. Options that configure a provider — [WithModel], [WithOCR],
// [WithRenderer], [WithHook] — are rejected here rather than silently ignored.
//
// A non-nil error means nothing usable came back and the Result is nil. It does
// not mean the data is good: that is [Result.Valid], and the two are
// independent.
//
//	res, err := ovrin.Extract[Invoice](ctx, c, ovrin.File("invoice.pdf"))
//	if err != nil {
//		return err
//	}
//	if !res.Valid || res.NeedsReview {
//		return review.Queue(res)
//	}
//	return ledger.Post(res.Data)
func Extract[T any](ctx context.Context, c *Client, src Source, opts ...Option) (*Result[T], error) {
	_ = ctx
	_ = c
	_ = src
	_ = opts
	panic("ovrin: Extract is not implemented yet")
}
