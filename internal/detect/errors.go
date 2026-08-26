package detect

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The conditions detection can fail on.
//
// These are the values to test with [errors.Is]. Nothing here and nothing
// above may branch on the text of an error message: a reworded message must
// never change how a program behaves (docs/rules.md §2.2).
//
// They are declared here rather than reused from the root package because the
// root package imports this one and the reverse would be an import cycle. The
// root wraps them into its own sentinels at that boundary, so a caller still
// tests against ovrin.ErrLimitExceeded and never learns this package exists.
//
// The text is lowercase and unpunctuated; the "ovrin: " prefix belongs to the
// package boundary rather than here (docs/rules.md §2.3).
var (
	// ErrUnsupportedFormat means the bytes are not a format ovrin can read.
	// Format is settled by content, so this is never a filename problem: a
	// JPEG called invoice.pdf is a JPEG, and a PDF called photo.jpg is a PDF.
	ErrUnsupportedFormat = errors.New("unsupported document format")

	// ErrLimitExceeded means a resource ceiling was reached. Every error
	// wrapping it is a [*LimitError] naming the limit and the option that
	// raises it, because "limit exceeded" on its own tells the owner of a
	// legitimate 1200-page loan file nothing they can act on
	// (docs/adr/0020-resource-limits.md).
	ErrLimitExceeded = errors.New("resource limit exceeded")

	// ErrEncrypted means the document is encrypted and cannot be read without
	// a credential ovrin has no way to accept yet. Detection reports only the
	// encryption it can establish cheaply and exactly; the format's own parser
	// makes the authoritative determination later, so a document that reaches
	// stage 2 has not been cleared, only not convicted.
	ErrEncrypted = errors.New("document is encrypted")
)

// Limit identifies one of the resource ceilings in ADR-0020.
//
// The identity travels on the error rather than being flattened into its text,
// so a caller can branch on which limit was hit without reading a message
// (docs/rules.md §2.2) — and so the message can name the option that raises it.
type Limit string

// The limits that bound a quantity of work.
//
// Concurrency is a bound on parallelism rather than on a quantity and so has
// no error, and total wall time is deliberately the caller's context rather
// than a limit of ours: Go already has one mechanism for that and a second
// would only disagree with it.
const (
	// LimitUnknown is the zero value. It never appears on a returned error.
	LimitUnknown Limit = ""

	LimitSourceBytes       Limit = "source bytes"
	LimitDecompressedBytes Limit = "decompressed bytes"
	LimitStreamBytes       Limit = "stream bytes"
	LimitTextBytes         Limit = "text bytes"
	LimitPages             Limit = "pages"
	LimitDepth             Limit = "object-graph depth"
	LimitObjects           Limit = "objects"
	LimitPagePixels        Limit = "page pixels"
)

// String returns the limit's name, or "unknown" for the zero value.
func (l Limit) String() string {
	if l == LimitUnknown {
		return "unknown"
	}
	return string(l)
}

// Option returns the name of the functional option that raises this limit, or
// "" for a limit that has none.
//
// It is here, and on every limit error, because the remedy is the only part of
// a limit failure the caller can act on. A message that says a limit was
// exceeded without saying which one to raise makes the caller read our source.
func (l Limit) Option() string {
	switch l {
	case LimitSourceBytes:
		return "WithMaxSourceBytes"
	case LimitDecompressedBytes:
		return "WithMaxDecompressedBytes"
	case LimitStreamBytes:
		return "WithMaxStreamBytes"
	case LimitTextBytes:
		return "WithMaxTextBytes"
	case LimitPages:
		return "WithMaxPages"
	case LimitDepth:
		return "WithMaxDepth"
	case LimitObjects:
		return "WithMaxObjects"
	case LimitPagePixels:
		return "WithMaxPagePixels"
	default:
		return ""
	}
}

// inBytes reports whether the limit counts bytes rather than things, which is
// the only reason the two are formatted differently.
func (l Limit) inBytes() bool {
	switch l {
	case LimitSourceBytes, LimitDecompressedBytes, LimitStreamBytes, LimitTextBytes:
		return true
	default:
		return false
	}
}

// quantity renders n in the limit's own unit. Byte ceilings are round binary
// numbers and reading "64 MiB" back to 67108864 to check it against the option
// you just set is work nobody should do.
func (l Limit) quantity(n int64) string {
	if !l.inBytes() {
		return strconv.FormatInt(n, 10)
	}
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + " GiB"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + " KiB"
	default:
		return strconv.FormatInt(n, 10) + " bytes"
	}
}

// LimitError reports which ceiling stopped the work, what it was, and which
// option raises it.
//
// It carries no part of the document — not a length read out of a header, not
// how far the read got. An error string is a log line that ends up in five
// systems nobody audited (docs/rules.md §2.5, §7.5), and the ceiling is ovrin's
// own number rather than the document's.
type LimitError struct {
	// Limit is the ceiling that was reached.
	Limit Limit

	// Max is the value of that ceiling, in the limit's own unit: bytes for a
	// byte limit, things for a count.
	Max int64
}

// Error names the limit, its ceiling and the option that raises it.
func (e *LimitError) Error() string {
	var b strings.Builder
	b.WriteString(e.Limit.String())
	b.WriteString(" limit exceeded: maximum ")
	b.WriteString(e.Limit.quantity(e.Max))
	if opt := e.Limit.Option(); opt != "" {
		b.WriteString(", raise with ")
		b.WriteString(opt)
	}
	return b.String()
}

// Unwrap returns [ErrLimitExceeded] so that every limit failure answers one
// errors.Is regardless of which limit it was.
func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

// exceeded returns the error for reaching limit, whose ceiling is max.
func exceeded(limit Limit, max int64) error {
	return &LimitError{Limit: limit, Max: max}
}

// unsupported wraps [ErrUnsupportedFormat] with a reason.
//
// reason is a literal at every call site in this package, and must stay one.
// Nothing derived from the document — not a byte of it, not a zip entry name,
// not a length out of a header, not the text of an error from a standard
// library parser that may quote either — is ever put in an error
// (docs/rules.md §2.5, §7.5).
func unsupported(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedFormat, reason)
}

// encrypted wraps [ErrEncrypted] with a reason, under the same rule as
// [unsupported]: the reason says what was observed, never what it said.
func encrypted(reason string) error {
	return fmt.Errorf("%w: %s", ErrEncrypted, reason)
}
