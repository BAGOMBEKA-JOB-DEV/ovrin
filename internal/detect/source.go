package detect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
)

// Source is an unread document: a stream, a slice, or a path.
//
// One interface over the three so that the source-byte limit is applied in one
// place — [Load] — rather than in three. That is the only arrangement in which
// a source kind added later cannot arrive without a limit on it.
//
// It mirrors the root package's Source, which is a closed interface over the
// same three shapes. The root does the mapping: it cannot pass its own Source
// here, because this package may not import it.
type Source interface {
	// Open returns a reader over the document together with the size the
	// source declares — the length of a slice, the size in a directory entry
	// — or -1 when the size cannot be known without reading it.
	//
	// The declared size is a hint, used to reject an oversized document
	// before anything is read. It is never trusted as a bound: [Load] wraps
	// the reader whatever the declaration said, because a declared size that
	// disagrees with its content is a documented parser attack
	// (docs/threat-model.md T3).
	//
	// The caller closes the reader.
	Open() (io.ReadCloser, int64, error)
}

// readerSource, bytesSource and fileSource are the three concrete Sources.
type readerSource struct{ r io.Reader }
type bytesSource struct{ b []byte }
type fileSource struct{ path string }

// Reader returns a [Source] reading from r.
//
// This is the shape a document usually arrives in — an upload, a network body
// — and the reason the source-byte limit is enforced as the stream is read:
// buffering it first to find out how big it is would defeat the limit that
// exists to stop it being buffered.
//
// The reader is consumed once and is not closed here; the caller opened it.
func Reader(r io.Reader) Source { return readerSource{r: r} }

// Bytes returns a [Source] over b.
//
// The slice is not copied, and [Load] may return it rather than a copy, so it
// must not be modified while a detection or extraction is running. Copying
// sixty mebibytes to guard against a caller mutating their own buffer would
// double the peak for every well-behaved caller.
func Bytes(b []byte) Source { return bytesSource{b: b} }

// File returns a [Source] reading the file at path.
//
// Opening is deferred to [Load] so a missing file surfaces alongside every
// other failure rather than needing a separate check, and so the size in the
// directory entry can be measured against the ceiling before the contents are
// read. The path is the caller's; nothing a document points at is ever opened
// (docs/rules.md §7.4).
func File(path string) Source { return fileSource{path: path} }

// Open returns the reader itself. A stream declares no size, so it returns -1
// and the ceiling is enforced entirely as it reads.
func (s readerSource) Open() (io.ReadCloser, int64, error) {
	if s.r == nil {
		return nil, 0, unsupported("empty source")
	}
	return io.NopCloser(s.r), -1, nil
}

// Open returns a reader over the slice. Its size is known exactly and needs no
// checking against the content, because here they are the same thing.
func (s bytesSource) Open() (io.ReadCloser, int64, error) {
	return io.NopCloser(bytes.NewReader(s.b)), int64(len(s.b)), nil
}

// Open opens the file and reports the size in its directory entry.
//
// A directory is rejected here rather than read: on some platforms reading one
// succeeds and returns something that is not a document.
func (s fileSource) Open() (io.ReadCloser, int64, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close() // the file is already unusable; a close error adds nothing
		return nil, 0, err
	}
	switch {
	case fi.IsDir():
		_ = f.Close() // as above
		return nil, 0, unsupported("source is a directory")
	case !fi.Mode().IsRegular():
		// A pipe, a socket or a device has no meaningful size in its
		// directory entry — /dev/zero reports zero and yields for ever — so
		// there is nothing to check before reading and the wrapped reader is
		// the only bound. It is a sufficient one.
		return f, -1, nil
	default:
		return f, fi.Size(), nil
	}
}

// Load reads src into memory under the source-byte limit.
//
// The whole document is held because the container formats need random access
// — a PDF's cross-reference table is at the end of the file and a zip's
// central directory likewise — and because every stage after this one reads it
// more than once, which a stream cannot do. Holding it is what the source-byte
// limit is for, and it is why that limit is enforced here and not downstream.
//
// A size the source declares is checked before a byte is read, so an oversized
// file is rejected without being opened for reading at all. It is then ignored
// in favour of the wrapped reader, because a declaration is the document's
// word and the reader is the truth.
//
// The returned slice may alias the one given to [Bytes].
//
// ctx is checked between reads rather than recorded and ignored: a source can
// be a network body that has stopped arriving, and a caller's deadline is no
// use if the stage holding the connection open never looks at it
// (docs/rules.md §1.5, §5.4).
func Load(ctx context.Context, src Source, lim Limits) ([]byte, error) {
	lim = lim.Normalised()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bs, ok := src.(bytesSource); ok {
		// Nothing to read: the size is exact and the bytes are already the
		// caller's. Copying them would double the peak for no gain.
		if int64(len(bs.b)) > lim.MaxSourceBytes {
			return nil, exceeded(LimitSourceBytes, lim.MaxSourceBytes)
		}
		return bs.b, nil
	}
	rc, declared, err := src.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read
	if declared > lim.MaxSourceBytes {
		return nil, exceeded(LimitSourceBytes, lim.MaxSourceBytes)
	}
	return readAll(ctx, NewLimitedReader(rc, LimitSourceBytes, lim.MaxSourceBytes, nil), declared)
}

// readAll reads r to EOF, sized from hint where the source gave one.
//
// io.ReadAll would do, and would grow from 512 bytes through twenty-odd
// reallocations for a sixty-mebibyte file. The hint has already been measured
// against the ceiling by the time this is called, so honouring it allocates at
// most what the ceiling permits — and the wrapped reader still decides how
// much is actually read, so a hint that lies large wastes a buffer and a hint
// that lies small costs a regrow.
func readAll(ctx context.Context, r io.Reader, hint int64) ([]byte, error) {
	initial := int64(512)
	if hint > 0 {
		initial = hint + 1 // +1 so the EOF-detecting read needs no regrow
	}
	buf := make([]byte, 0, initial)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return nil, err
		}
	}
}
