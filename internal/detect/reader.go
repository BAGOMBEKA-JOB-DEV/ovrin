package detect

import (
	"compress/flate"
	"compress/zlib"
	"io"
)

// LimitedReader bounds what may be read through it, and fails rather than
// truncating.
//
// It differs from [io.LimitedReader] in the one way that matters here. At the
// ceiling io.LimitedReader reports io.EOF, which a parser cannot tell apart
// from a document that simply ended, so a decompression bomb becomes a
// silently truncated document that parses to something its author chose. This
// one returns a [*LimitError], and the whole read fails closed.
//
// Reading never buffers. At most one byte beyond the ceiling is ever taken
// from the underlying reader, into the caller's own slice, which is enough to
// prove the source holds more than the budget allows without holding what it
// holds. That is what "wrapped, so the bytes are never allocated" means in
// ADR-0020, and it is why a decompressor is constructed inside one of these
// rather than checked after it has produced its output.
//
// One goroutine at a time, like any reader. The [Counter] it charges is not:
// that one is shared.
type LimitedReader struct {
	r     io.Reader
	limit Limit
	max   int64
	n     int64
	cum   *Counter
	err   error
}

// NewLimitedReader wraps r with a ceiling of max bytes, reported as limit.
//
// cum is the document-wide budget the same bytes are also charged against, and
// may be nil when there is none. Both are enforced, and they answer different
// attacks: the per-stream ceiling stops one enormous stream, the cumulative
// one stops a thousand merely large ones.
func NewLimitedReader(r io.Reader, limit Limit, max int64, cum *Counter) *LimitedReader {
	return &LimitedReader{r: r, limit: limit, max: max, cum: cum}
}

// Read implements [io.Reader].
//
// Once a ceiling has been reached the error is sticky: every later call
// returns it, so a caller that ignores one Read's error cannot go on to
// consume the rest of the bomb.
func (l *LimitedReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	// Never ask the underlying reader for more than one byte past the
	// ceiling. That byte is the proof the source exceeds it; the bytes behind
	// it are never requested, so they are never allocated.
	if room := l.max - l.n + 1; room < int64(len(p)) {
		if room <= 0 {
			l.err = exceeded(l.limit, l.max)
			return 0, l.err
		}
		p = p[:room]
	}
	n, err := l.r.Read(p)
	if n > 0 {
		l.n += int64(n)
		if l.n > l.max {
			l.err = exceeded(l.limit, l.max)
			return 0, l.err
		}
		if cerr := l.cum.Add(int64(n)); cerr != nil {
			l.err = cerr
			return 0, cerr
		}
	}
	if err != nil {
		l.err = err
	}
	return n, err
}

// Count returns how many bytes have been read through the reader so far.
//
// It is the honest measure of a source whose size nothing declared, which is
// why it is the one recorded as a document's size rather than anything the
// document said about itself.
func (l *LimitedReader) Count() int64 { return l.n }

// boundedReadCloser presents a decompressor's bounded output while closing the
// decompressor underneath it, so a caller cannot hold the bounded reader and
// leak the thing being bounded.
type boundedReadCloser struct {
	io.Reader
	closer io.Closer
}

// Close closes the decompressor.
func (b boundedReadCloser) Close() error { return b.closer.Close() }

// Flate returns a reader over the raw DEFLATE stream in r, bounded before its
// output is allocated.
//
// The wrapping is the point. A decompressor asked for its output allocates it,
// so the ceiling has to sit between the decompressor and the caller rather
// than be applied to what came back. A 600 KB PDF whose nested FlateDecode
// streams expand to ten gigabytes is a documented attack, and it fails at the
// first stream to pass the ceiling rather than when the host stops responding
// (docs/threat-model.md T2, docs/rules.md §7.3).
//
// Nesting is nesting: wrapping one of these in another charges both to cum, so
// the outer expansion spends what is left of the document's budget rather than
// starting again from a full one.
//
// Closing the result closes the decompressor.
func Flate(r io.Reader, lim Limits, cum *Counter) io.ReadCloser {
	lim = lim.Normalised()
	zr := flate.NewReader(r)
	return boundedReadCloser{
		Reader: NewLimitedReader(zr, LimitStreamBytes, lim.MaxStreamBytes, cum),
		closer: zr,
	}
}

// Zlib returns a reader over the zlib stream in r, bounded exactly as [Flate]
// bounds a raw one.
//
// It differs from Flate only in the two-byte header and the trailing checksum,
// which is why this one can fail here and Flate cannot. PDF's FlateDecode is
// specified as zlib and produced as raw deflate often enough that a parser
// needs both.
func Zlib(r io.Reader, lim Limits, cum *Counter) (io.ReadCloser, error) {
	lim = lim.Normalised()
	zr, err := zlib.NewReader(r)
	if err != nil {
		return nil, err
	}
	return boundedReadCloser{
		Reader: NewLimitedReader(zr, LimitStreamBytes, lim.MaxStreamBytes, cum),
		closer: zr,
	}, nil
}
