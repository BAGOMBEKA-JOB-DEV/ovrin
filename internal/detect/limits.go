package detect

import (
	"math"
	"sync"
)

// The finite defaults of ADR-0020.
//
// They mirror the Default* constants in the root package. The duplication is
// forced by the import direction — the root imports this package, so this
// package cannot read them — and it is why the root's constants are the ones
// callers express a change relative to. If these two ever disagree the root
// wins, because it is the one users read.
const (
	defaultMaxSourceBytes       int64 = 64 << 20  // 64 MiB
	defaultMaxDecompressedBytes int64 = 512 << 20 // 512 MiB
	defaultMaxStreamBytes       int64 = 64 << 20  // 64 MiB
	defaultMaxTextBytes         int64 = 32 << 20  // 32 MiB
	defaultMaxPages                   = 1000
	defaultMaxDepth                   = 64
	defaultMaxObjects                 = 500_000
	defaultMaxPagePixels              = 50_000_000
)

// Limits are the resource ceilings one detection or parse runs under.
//
// A struct rather than functional options because this is an internal seam:
// the root package translates its own options into one of these once, and the
// public surface stays functional options (docs/rules.md §1.4, §1.7).
//
// A zero value is not a set of zero ceilings. Any field that is not positive
// means "use the default" — see [Limits.Normalised] — because a ceiling of
// zero rejects every document, which is a configuration mistake and never an
// intent. Every entry point here normalises before it reads a field, so an
// internal caller that forgets gets the documented defaults rather than a
// package that refuses everything.
type Limits struct {
	// MaxSourceBytes bounds the source document.
	MaxSourceBytes int64

	// MaxDecompressedBytes bounds decompressed output across the whole
	// document. Cumulative, because a thousand streams of a mebibyte is the
	// same attack as one stream of a gibibyte.
	MaxDecompressedBytes int64

	// MaxStreamBytes bounds decompressed output from any single stream.
	MaxStreamBytes int64

	// MaxTextBytes bounds extracted text.
	MaxTextBytes int64

	// MaxPages bounds the page count. It also bounds spend: ten thousand
	// pages sent to a per-page-priced OCR provider is not a crash, it is an
	// invoice.
	MaxPages int

	// MaxDepth bounds recursion through the object graph.
	MaxDepth int

	// MaxObjects bounds the object count.
	MaxObjects int

	// MaxPagePixels bounds one rasterised page.
	MaxPagePixels int
}

// DefaultLimits returns the ceilings an unconfigured run uses.
//
// Every one of them is finite. The numbers are judgement rather than
// measurement — round values chosen to sit comfortably above real documents
// and comfortably below dangerous ones — and they will be revised against the
// evaluation corpus.
func DefaultLimits() Limits {
	return Limits{
		MaxSourceBytes:       defaultMaxSourceBytes,
		MaxDecompressedBytes: defaultMaxDecompressedBytes,
		MaxStreamBytes:       defaultMaxStreamBytes,
		MaxTextBytes:         defaultMaxTextBytes,
		MaxPages:             defaultMaxPages,
		MaxDepth:             defaultMaxDepth,
		MaxObjects:           defaultMaxObjects,
		MaxPagePixels:        defaultMaxPagePixels,
	}
}

// Normalised returns l with every non-positive field replaced by its default.
//
// It is idempotent and it is called by everything in this package that reads a
// ceiling, so there is no path on which an unset field becomes an unlimited
// one — nor a path on which it becomes a ceiling of zero that rejects a
// perfectly good document.
func (l Limits) Normalised() Limits {
	d := DefaultLimits()
	if l.MaxSourceBytes <= 0 {
		l.MaxSourceBytes = d.MaxSourceBytes
	}
	if l.MaxDecompressedBytes <= 0 {
		l.MaxDecompressedBytes = d.MaxDecompressedBytes
	}
	if l.MaxStreamBytes <= 0 {
		l.MaxStreamBytes = d.MaxStreamBytes
	}
	if l.MaxTextBytes <= 0 {
		l.MaxTextBytes = d.MaxTextBytes
	}
	if l.MaxPages <= 0 {
		l.MaxPages = d.MaxPages
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxObjects <= 0 {
		l.MaxObjects = d.MaxObjects
	}
	if l.MaxPagePixels <= 0 {
		l.MaxPagePixels = d.MaxPagePixels
	}
	return l
}

// CheckPages rejects a page count before the pages are allocated.
//
// A document declaring a hundred thousand pages costs nothing to declare and
// everything to honour, so the declaration is measured against the ceiling
// before anything is built from it.
func (l Limits) CheckPages(n int) error {
	l = l.Normalised()
	if n > l.MaxPages {
		return exceeded(LimitPages, int64(l.MaxPages))
	}
	return nil
}

// CheckObjects rejects an object count before the objects are allocated.
func (l Limits) CheckObjects(n int) error {
	l = l.Normalised()
	if n > l.MaxObjects {
		return exceeded(LimitObjects, int64(l.MaxObjects))
	}
	return nil
}

// CheckPixels rejects a rasterised page before the image is allocated.
//
// A single page can declare a media box of implausible size, so that
// rasterising it at 300 DPI asks for an image larger than physical memory. The
// product is tested by division rather than computed, because a width and a
// height chosen to overflow int64 would otherwise multiply their way to a
// small number and pass. Non-positive dimensions allocate nothing and so are
// not a limit failure; whether they are a malformed document is the caller's
// question, not this one's.
func (l Limits) CheckPixels(width, height int) error {
	l = l.Normalised()
	w, h := int64(width), int64(height)
	if w <= 0 || h <= 0 {
		return nil
	}
	max := int64(l.MaxPagePixels)
	if w > max/h {
		return exceeded(LimitPagePixels, max)
	}
	return nil
}

// Depth returns a fresh recursion budget of MaxDepth levels, for handing to a
// recursive parser.
func (l Limits) Depth() Depth {
	return NewDepth(l.Normalised().MaxDepth)
}

// Counter is a budget shared by everything that spends it.
//
// Byte counters are cumulative across a document rather than per stream,
// because a thousand streams of a mebibyte is the same attack as one stream of
// a gibibyte (ADR-0020). The same type counts objects and pages, which are
// spent the same way and exhausted the same way.
//
// A nil *Counter is a valid counter with no ceiling, so a caller that has no
// cumulative budget to charge passes nil rather than branching at every call.
//
// Safe for concurrent use by multiple goroutines: pages are processed in
// parallel and they share one.
type Counter struct {
	mu    sync.Mutex
	limit Limit
	max   int64
	n     int64
}

// NewCounter returns a counter of at most max units of limit. The limit is
// carried so that whatever exhausts the budget reports which budget it was.
func NewCounter(limit Limit, max int64) *Counter {
	return &Counter{limit: limit, max: max}
}

// Add spends n units and reports a [*LimitError] if that would exceed the
// budget.
//
// Nothing is spent on the call that fails, so a caller that adds before it
// allocates never allocates. A non-positive n spends nothing and always
// succeeds: there is no unspending, because a budget that can be given back is
// a budget an attacker can cycle.
func (c *Counter) Add(n int64) error {
	if c == nil || n <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n > c.max-n {
		return exceeded(c.limit, c.max)
	}
	c.n += n
	return nil
}

// Used returns how much of the budget has been spent.
func (c *Counter) Used() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Remaining returns how much of the budget is left. A nil counter has all of
// it, forever.
func (c *Counter) Remaining() int64 {
	if c == nil {
		return math.MaxInt64
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n >= c.max {
		return 0
	}
	return c.max - c.n
}

// Limit returns which ceiling this counter is counting towards.
func (c *Counter) Limit() Limit {
	if c == nil {
		return LimitUnknown
	}
	return c.limit
}

// Depth is a recursion budget, passed down a recursive parser as an argument.
//
// A budget in a parameter rather than a counter in a struct is the whole
// point: it cannot be forgotten on a code path added later, it needs no
// unwinding on the way back up, and it is still correct when two goroutines
// walk the same graph. A page tree nested thousands deep runs out of budget
// rather than out of stack, and a cross-reference cycle terminates instead of
// recursing until the stack is gone (ADR-0020).
//
//	func walk(o object, d detect.Depth) error {
//		d, err := d.Descend()
//		if err != nil {
//			return err
//		}
//		for _, kid := range o.kids {
//			if err := walk(kid, d); err != nil {
//				return err
//			}
//		}
//		return nil
//	}
//
// The zero value has no budget and refuses to descend at all. Unlike [Limits],
// which is configuration and so takes the documented default when it is unset,
// a Depth is constructed in code: an unconstructed one is a mistake, and one
// that fails on the first descent is a mistake found by the first test rather
// than by the first hostile document.
type Depth struct {
	remaining int
	max       int
}

// NewDepth returns a budget of max levels.
func NewDepth(max int) Depth {
	return Depth{remaining: max, max: max}
}

// Descend spends one level and returns the budget for the level below, or a
// [*LimitError] naming the depth limit when there is none left.
//
// It is a value method returning a value: the budget the caller holds is
// unchanged, so recursion down one branch cannot deepen the ceiling for a
// sibling.
func (d Depth) Descend() (Depth, error) {
	if d.remaining <= 0 {
		return d, exceeded(LimitDepth, int64(d.max))
	}
	return Depth{remaining: d.remaining - 1, max: d.max}, nil
}

// Remaining returns how many levels are left.
func (d Depth) Remaining() int { return d.remaining }
