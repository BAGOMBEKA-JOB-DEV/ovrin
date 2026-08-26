package detect

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"io"
	"strings"
	"testing"
)

// deflateOf compresses b, which is how a bomb is built: the expensive
// direction is the one the victim runs.
func deflateOf(t testing.TB, b []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		t.Fatalf("new flate writer: %v", err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatalf("write flate: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close flate: %v", err)
	}
	return buf.Bytes()
}

func zlibOf(t testing.TB, b []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatalf("write zlib: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zlib: %v", err)
	}
	return buf.Bytes()
}

func TestLimitedReaderFailsRatherThanTruncating(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    int
		max     int64
		wantN   int64
		wantErr bool
	}{
		{name: "well inside the ceiling", body: 100, max: 1000, wantN: 100},
		{name: "exactly at the ceiling", body: 1000, max: 1000, wantN: 1000},
		{name: "one byte past the ceiling", body: 1001, max: 1000, wantErr: true},
		{name: "far past the ceiling", body: 1 << 20, max: 1000, wantErr: true},
		{name: "an empty source", body: 0, max: 1000, wantN: 0},
		{name: "a ceiling of one", body: 1, max: 1, wantN: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := bytes.Repeat([]byte("A"), tc.body)
			r := NewLimitedReader(bytes.NewReader(body), LimitStreamBytes, tc.max, nil)
			n, err := io.Copy(io.Discard, r)

			if tc.wantErr {
				if !errors.Is(err, ErrLimitExceeded) {
					t.Fatalf("Copy: got error %v, want one wrapping %v", err, ErrLimitExceeded)
				}
				if n > tc.max {
					t.Errorf("Copy: passed on %d bytes, want at most the ceiling of %d", n, tc.max)
				}
				// The failure is sticky: a caller that ignored the error must
				// not be able to read the rest of the bomb.
				if _, again := r.Read(make([]byte, 16)); !errors.Is(again, ErrLimitExceeded) {
					t.Errorf("Read after the limit: got %v, want the same limit error", again)
				}
				return
			}
			if err != nil {
				t.Fatalf("Copy: unexpected error: %v", err)
			}
			if n != tc.wantN {
				t.Errorf("Copy: got %d bytes, want %d", n, tc.wantN)
			}
			if r.Count() != tc.wantN {
				t.Errorf("Count: got %d, want %d", r.Count(), tc.wantN)
			}
		})
	}
}

func TestLimitedReaderNeverTakesMoreThanOneByteBeyondTheCeiling(t *testing.T) {
	t.Parallel()

	const max = 4096
	src := &endlessReader{}
	r := NewLimitedReader(src, LimitStreamBytes, max, nil)

	if _, err := io.Copy(io.Discard, r); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Copy: got error %v, want one wrapping %v", err, ErrLimitExceeded)
	}
	if src.handed > max+1 {
		t.Errorf("took %d bytes from an endless source, want at most %d", src.handed, max+1)
	}
}

func TestLimitedReaderReportsWhichLimitAndWhichOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limit  Limit
		option string
	}{
		{name: "a source ceiling", limit: LimitSourceBytes, option: "WithMaxSourceBytes"},
		{name: "a stream ceiling", limit: LimitStreamBytes, option: "WithMaxStreamBytes"},
		{name: "a document-wide decompression ceiling", limit: LimitDecompressedBytes, option: "WithMaxDecompressedBytes"},
		{name: "a text ceiling", limit: LimitTextBytes, option: "WithMaxTextBytes"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := NewLimitedReader(strings.NewReader("far too much"), tc.limit, 4, nil)
			_, err := io.Copy(io.Discard, r)

			var le *LimitError
			if !errors.As(err, &le) {
				t.Fatalf("Copy: got error %v, want a *LimitError", err)
			}
			if le.Limit != tc.limit {
				t.Errorf("Copy: got limit %s, want %s", le.Limit, tc.limit)
			}
			if !strings.Contains(le.Error(), tc.option) {
				t.Errorf("Copy: error %q does not name %s", le, tc.option)
			}
		})
	}
}

func TestNestedDeflateBombFailsClosed(t *testing.T) {
	t.Parallel()

	// The documented attack: a small file whose nested FlateDecode filters
	// expand to more than the host has. Eight mebibytes stands in for ten
	// gigabytes; the ratio is what is being tested, not the absolute size.
	const expanded = 8 << 20
	inner := deflateOf(t, make([]byte, expanded))
	outer := deflateOf(t, inner)
	if len(outer) > 4096 {
		t.Fatalf("bomb is %d bytes on the wire, want a small one to be worth the name", len(outer))
	}

	lim := DefaultLimits()
	lim.MaxStreamBytes = 1 << 20
	lim.MaxDecompressedBytes = 4 << 20
	budget := NewCounter(LimitDecompressedBytes, lim.MaxDecompressedBytes)

	first := Flate(bytes.NewReader(outer), lim, budget)
	defer func() { _ = first.Close() }() // read-only

	second := Flate(first, lim, budget)
	defer func() { _ = second.Close() }() // read-only

	n, err := io.Copy(io.Discard, second)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Copy: got error %v after %d bytes, want one wrapping %v", err, n, ErrLimitExceeded)
	}
	if n > lim.MaxStreamBytes {
		t.Errorf("Copy: passed on %d bytes, want at most the stream ceiling of %d", n, lim.MaxStreamBytes)
	}
	if budget.Used() > lim.MaxDecompressedBytes {
		t.Errorf("budget: spent %d, want at most %d", budget.Used(), lim.MaxDecompressedBytes)
	}
}

func TestCumulativeBudgetCatchesManySmallStreams(t *testing.T) {
	t.Parallel()

	// A thousand streams of a mebibyte is the same attack as one stream of a
	// gibibyte, and only a cumulative counter sees it: every one of these is
	// comfortably inside the per-stream ceiling.
	const (
		perStream = 64 << 10
		streams   = 64
	)
	compressed := deflateOf(t, make([]byte, perStream))

	lim := DefaultLimits()
	lim.MaxStreamBytes = 1 << 20
	lim.MaxDecompressedBytes = 8 * perStream
	budget := NewCounter(LimitDecompressedBytes, lim.MaxDecompressedBytes)

	var lastErr error
	survived := 0
	for i := 0; i < streams; i++ {
		r := Flate(bytes.NewReader(compressed), lim, budget)
		_, err := io.Copy(io.Discard, r)
		_ = r.Close() // read-only
		if err != nil {
			lastErr = err
			break
		}
		survived++
	}

	if !errors.Is(lastErr, ErrLimitExceeded) {
		t.Fatalf("after %d streams: got error %v, want one wrapping %v", survived, lastErr, ErrLimitExceeded)
	}
	var le *LimitError
	if !errors.As(lastErr, &le) || le.Limit != LimitDecompressedBytes {
		t.Fatalf("got %v, want the document-wide decompression limit", lastErr)
	}
	if survived != 8 {
		t.Errorf("survived %d streams, want the 8 the budget pays for", survived)
	}
	if budget.Used() > lim.MaxDecompressedBytes {
		t.Errorf("budget: spent %d, want at most %d", budget.Used(), lim.MaxDecompressedBytes)
	}
}

func TestDecompressorsAreBoundedWhateverTheirWrapper(t *testing.T) {
	t.Parallel()

	const expanded = 4 << 20
	body := make([]byte, expanded)

	tests := []struct {
		name string
		open func(t testing.TB, lim Limits, budget *Counter) (io.ReadCloser, error)
	}{
		{
			name: "raw deflate",
			open: func(t testing.TB, lim Limits, budget *Counter) (io.ReadCloser, error) {
				return Flate(bytes.NewReader(deflateOf(t, body)), lim, budget), nil
			},
		},
		{
			name: "zlib",
			open: func(t testing.TB, lim Limits, budget *Counter) (io.ReadCloser, error) {
				return Zlib(bytes.NewReader(zlibOf(t, body)), lim, budget)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lim := DefaultLimits()
			lim.MaxStreamBytes = 1 << 16
			budget := NewCounter(LimitDecompressedBytes, lim.MaxDecompressedBytes)

			rc, err := tc.open(t, lim, budget)
			if err != nil {
				t.Fatalf("open: unexpected error: %v", err)
			}
			defer func() { _ = rc.Close() }() // read-only

			n, err := io.Copy(io.Discard, rc)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("Copy: got error %v after %d bytes, want one wrapping %v", err, n, ErrLimitExceeded)
			}
			if n > lim.MaxStreamBytes {
				t.Errorf("Copy: passed on %d bytes, want at most %d", n, lim.MaxStreamBytes)
			}
		})
	}
}

func TestDecompressorsPassSmallStreamsThrough(t *testing.T) {
	t.Parallel()

	body := []byte(strings.Repeat("the quick brown fox\n", 100))

	t.Run("raw deflate", func(t *testing.T) {
		t.Parallel()

		rc := Flate(bytes.NewReader(deflateOf(t, body)), DefaultLimits(), nil)
		defer func() { _ = rc.Close() }() // read-only

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: unexpected error: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Error("ReadAll: the stream did not survive the wrapper intact")
		}
	})

	t.Run("zlib", func(t *testing.T) {
		t.Parallel()

		rc, err := Zlib(bytes.NewReader(zlibOf(t, body)), DefaultLimits(), nil)
		if err != nil {
			t.Fatalf("Zlib: unexpected error: %v", err)
		}
		defer func() { _ = rc.Close() }() // read-only

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: unexpected error: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Error("ReadAll: the stream did not survive the wrapper intact")
		}
	})

	t.Run("zlib rejects a header it does not have", func(t *testing.T) {
		t.Parallel()

		if _, err := Zlib(bytes.NewReader([]byte("not a zlib stream")), DefaultLimits(), nil); err == nil {
			t.Fatal("Zlib: no error for a stream with no zlib header")
		}
	})
}

func TestLimitedReaderEdges(t *testing.T) {
	t.Parallel()

	t.Run("a zero-length read asks the source for nothing", func(t *testing.T) {
		t.Parallel()

		src := &endlessReader{}
		r := NewLimitedReader(src, LimitStreamBytes, 100, nil)
		n, err := r.Read(nil)
		if n != 0 || err != nil {
			t.Errorf("Read: got (%d, %v), want (0, nil)", n, err)
		}
		if src.handed != 0 {
			t.Errorf("took %d bytes for a zero-length read, want 0", src.handed)
		}
	})

	t.Run("a ceiling below zero admits nothing", func(t *testing.T) {
		t.Parallel()

		src := &endlessReader{}
		r := NewLimitedReader(src, LimitStreamBytes, -1, nil)
		if _, err := r.Read(make([]byte, 16)); !errors.Is(err, ErrLimitExceeded) {
			t.Errorf("Read: got %v, want a limit error", err)
		}
		if src.handed != 0 {
			t.Errorf("took %d bytes past a negative ceiling, want 0", src.handed)
		}
	})

	t.Run("a ceiling of zero admits an empty source and nothing more", func(t *testing.T) {
		t.Parallel()

		if _, err := io.Copy(io.Discard, NewLimitedReader(strings.NewReader(""), LimitStreamBytes, 0, nil)); err != nil {
			t.Errorf("Copy: got error %v on an empty source, want none", err)
		}
		if _, err := io.Copy(io.Discard, NewLimitedReader(strings.NewReader("x"), LimitStreamBytes, 0, nil)); !errors.Is(err, ErrLimitExceeded) {
			t.Errorf("Copy: got error %v for one byte past a ceiling of zero, want a limit error", err)
		}
	})

	t.Run("a cumulative budget stops a stream inside its own ceiling", func(t *testing.T) {
		t.Parallel()

		budget := NewCounter(LimitDecompressedBytes, 10)
		r := NewLimitedReader(strings.NewReader(strings.Repeat("A", 100)), LimitStreamBytes, 1000, budget)

		var le *LimitError
		_, err := io.Copy(io.Discard, r)
		if !errors.As(err, &le) {
			t.Fatalf("Copy: got error %v, want a *LimitError", err)
		}
		if le.Limit != LimitDecompressedBytes {
			t.Errorf("Copy: got limit %s, want %s", le.Limit, LimitDecompressedBytes)
		}
	})
}
