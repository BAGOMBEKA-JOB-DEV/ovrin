package ovrin

// The default limits.
//
// Every limit has a default and every default is finite. Ovrin parses
// attacker-controlled binary formats, and the resource attacks against them are
// documented rather than hypothetical: a 600 KB PDF whose streams are nested
// FlateDecode expands to ten gigabytes in memory, a cross-reference cycle
// recurses until the stack is gone, a media box can declare a page that
// rasterises larger than physical memory.
//
// These numbers are judgement, not measurement — round values chosen to sit
// comfortably above real documents and comfortably below dangerous ones. They
// will be revised against the evaluation corpus. Raising a default is not
// breaking; lowering one is.
//
// They are exported so a caller raising a limit can express it relative to the
// default rather than restating a number that may change.
//
// See docs/adr/0020-resource-limits.md.
const (
	// DefaultMaxSourceBytes bounds the source document.
	DefaultMaxSourceBytes int64 = 64 << 20 // 64 MiB

	// DefaultMaxDecompressedBytes bounds decompressed output across the whole
	// document. Cumulative, because a thousand streams of one mebibyte is the
	// same attack as one stream of a gibibyte.
	DefaultMaxDecompressedBytes int64 = 512 << 20 // 512 MiB

	// DefaultMaxStreamBytes bounds decompressed output from any single stream.
	DefaultMaxStreamBytes int64 = 64 << 20 // 64 MiB

	// DefaultMaxTextBytes bounds extracted text.
	DefaultMaxTextBytes int64 = 32 << 20 // 32 MiB

	// DefaultMaxPages bounds the page count. Also bounds spend: ten thousand
	// pages sent to a per-page-priced OCR provider is not a crash, it is an
	// invoice.
	DefaultMaxPages = 1000

	// DefaultMaxDepth bounds recursion through the object graph.
	DefaultMaxDepth = 64

	// DefaultMaxObjects bounds the object count.
	DefaultMaxObjects = 500_000

	// DefaultMaxPagePixels bounds one rasterised page.
	DefaultMaxPagePixels = 50_000_000 // 50 M
)

// The default policy thresholds.
const (
	// DefaultReviewThreshold is the field confidence below which a result is
	// flagged for review.
	DefaultReviewThreshold = 0.70

	// DefaultMinTextDensity is the minimum characters per square inch for a
	// page's text layer to be considered usable.
	DefaultMinTextDensity = 0.5

	// DefaultMaxReplacementRatio is the maximum proportion of U+FFFD
	// replacement characters a usable text layer may contain.
	DefaultMaxReplacementRatio = 0.02

	// DefaultMinDecodableRatio is the minimum proportion of characters that
	// must map through a ToUnicode entry or a standard encoding for a text
	// layer to be considered usable.
	//
	// A PDF with a broken ToUnicode table can produce plausible-looking
	// rubbish that would poison everything downstream, so a page failing this
	// falls through to OCR rather than being trusted.
	DefaultMinDecodableRatio = 0.90
)

// WithMaxSourceBytes bounds the source document.
func WithMaxSourceBytes(n int64) Option {
	return optionFunc(func(c *config) { c.maxSourceBytes = n })
}

// WithMaxDecompressedBytes bounds decompressed output across the document.
func WithMaxDecompressedBytes(n int64) Option {
	return optionFunc(func(c *config) { c.maxDecompressedBytes = n })
}

// WithMaxStreamBytes bounds decompressed output from any single stream.
func WithMaxStreamBytes(n int64) Option {
	return optionFunc(func(c *config) { c.maxStreamBytes = n })
}

// WithMaxTextBytes bounds extracted text.
func WithMaxTextBytes(n int64) Option {
	return optionFunc(func(c *config) { c.maxTextBytes = n })
}

// WithMaxPages bounds the page count.
func WithMaxPages(n int) Option {
	return optionFunc(func(c *config) { c.maxPages = n })
}

// WithMaxDepth bounds recursion through the document's object graph.
func WithMaxDepth(n int) Option {
	return optionFunc(func(c *config) { c.maxDepth = n })
}

// WithMaxObjects bounds the number of objects in a document.
func WithMaxObjects(n int) Option {
	return optionFunc(func(c *config) { c.maxObjects = n })
}

// WithMaxPagePixels bounds the size of one rasterised page.
func WithMaxPagePixels(n int) Option {
	return optionFunc(func(c *config) { c.maxPagePixels = n })
}

// WithConcurrency bounds page-level parallelism.
//
// The default is min(4, GOMAXPROCS), so ovrin does not monopolise a host it
// shares. Model calls are not parallelised across pages: extraction needs the
// whole document.
func WithConcurrency(n int) Option {
	return optionFunc(func(c *config) { c.concurrency = n })
}

// WithReviewThreshold sets the field confidence below which a result is
// flagged for review.
func WithReviewThreshold(t float64) Option {
	return optionFunc(func(c *config) { c.reviewThreshold = t })
}

// WithMinTextDensity sets the minimum characters per square inch for a text
// layer to be considered usable.
func WithMinTextDensity(d float64) Option {
	return optionFunc(func(c *config) { c.minTextDensity = d })
}

// WithMaxReplacementRatio sets the maximum proportion of U+FFFD characters a
// usable text layer may contain.
func WithMaxReplacementRatio(r float64) Option {
	return optionFunc(func(c *config) { c.maxReplacementRatio = r })
}

// WithMinDecodableRatio sets the minimum proportion of decodable characters a
// usable text layer must contain.
func WithMinDecodableRatio(r float64) Option {
	return optionFunc(func(c *config) { c.minDecodableRatio = r })
}
