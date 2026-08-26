package detect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// endlessReader yields the same byte for ever and counts what it has handed
// over, which is the measurement that matters: a limit that is enforced after
// the fact would show the whole stream here.
type endlessReader struct{ handed int64 }

func (e *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	e.handed += int64(len(p))
	return len(p), nil
}

// lyingSource declares one size and yields another, which no honest producer
// does and every attacker can.
type lyingSource struct {
	declared int64
	body     []byte
	opened   bool
	read     int64
}

func (s *lyingSource) Open() (io.ReadCloser, int64, error) {
	s.opened = true
	return io.NopCloser(&countingReader{r: bytes.NewReader(s.body), n: &s.read}), s.declared, nil
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}

func TestLoadSourceByteLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	body := bytes.Repeat([]byte("A"), 4096)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name    string
		src     Source
		max     int64
		wantErr bool
	}{
		{name: "a slice inside the ceiling", src: Bytes(body), max: 8192},
		{name: "a slice over the ceiling", src: Bytes(body), max: 1024, wantErr: true},
		{name: "a slice exactly at the ceiling", src: Bytes(body), max: 4096},
		{name: "a stream inside the ceiling", src: Reader(bytes.NewReader(body)), max: 8192},
		{name: "a stream over the ceiling", src: Reader(bytes.NewReader(body)), max: 1024, wantErr: true},
		{name: "a stream exactly at the ceiling", src: Reader(bytes.NewReader(body)), max: 4096},
		{name: "a file inside the ceiling", src: File(path), max: 8192},
		{name: "a file over the ceiling", src: File(path), max: 1024, wantErr: true},
		{name: "a file exactly at the ceiling", src: File(path), max: 4096},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Load(context.Background(), tc.src, Limits{MaxSourceBytes: tc.max})
			if tc.wantErr {
				var le *LimitError
				if !errors.As(err, &le) {
					t.Fatalf("Load: got error %v, want a *LimitError", err)
				}
				if le.Limit != LimitSourceBytes {
					t.Errorf("Load: got limit %s, want %s", le.Limit, LimitSourceBytes)
				}
				if !strings.Contains(le.Error(), "WithMaxSourceBytes") {
					t.Errorf("Load: error %q does not name the option that raises the limit", le)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: unexpected error: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("Load: got %d bytes, want %d", len(got), len(body))
			}
		})
	}
}

func TestLoadNeverBuffersPastTheCeiling(t *testing.T) {
	t.Parallel()

	const max = 4096
	src := &endlessReader{}

	_, err := Load(context.Background(), Reader(src), Limits{MaxSourceBytes: max})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Load: got error %v, want one wrapping %v", err, ErrLimitExceeded)
	}
	// One byte past the ceiling is the proof that there was more; anything
	// beyond that would be a stream read into memory and measured afterwards.
	if src.handed > max+1 {
		t.Errorf("Load: took %d bytes from an endless source, want at most %d", src.handed, max+1)
	}
}

func TestLoadTreatsADeclaredSizeAsAClaim(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("A"), 4096)

	t.Run("a declaration over the ceiling is refused without reading", func(t *testing.T) {
		t.Parallel()

		src := &lyingSource{declared: 1 << 40, body: body}
		if _, err := Load(context.Background(), src, Limits{MaxSourceBytes: 8192}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("Load: got error %v, want one wrapping %v", err, ErrLimitExceeded)
		}
		if src.read != 0 {
			t.Errorf("Load: read %d bytes from a source that declared too much, want 0", src.read)
		}
	})

	t.Run("a declaration under the content does not bound the read", func(t *testing.T) {
		t.Parallel()

		src := &lyingSource{declared: 16, body: body}
		got, err := Load(context.Background(), src, Limits{MaxSourceBytes: 8192})
		if err != nil {
			t.Fatalf("Load: unexpected error: %v", err)
		}
		if len(got) != len(body) {
			t.Errorf("Load: got %d bytes, want the %d that were there", len(got), len(body))
		}
	})

	t.Run("a declaration under the content does not raise the ceiling", func(t *testing.T) {
		t.Parallel()

		src := &lyingSource{declared: 16, body: bytes.Repeat([]byte("A"), 1<<16)}
		if _, err := Load(context.Background(), src, Limits{MaxSourceBytes: 4096}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("Load: got error %v, want one wrapping %v", err, ErrLimitExceeded)
		}
		if src.read > 4097 {
			t.Errorf("Load: read %d bytes, want at most %d", src.read, 4097)
		}
	})
}

func TestLoadDegenerateSources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	single := filepath.Join(dir, "one.bin")
	if err := os.WriteFile(single, []byte{'%'}, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name string
		src  Source
		want int
	}{
		{name: "an empty file", src: File(empty), want: 0},
		{name: "an empty slice", src: Bytes(nil), want: 0},
		{name: "an empty stream", src: Reader(strings.NewReader("")), want: 0},
		{name: "a one-byte file", src: File(single), want: 1},
		{name: "a one-byte slice", src: Bytes([]byte{'%'}), want: 1},
		{name: "a one-byte stream", src: Reader(strings.NewReader("%")), want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Load(context.Background(), tc.src, DefaultLimits())
			if err != nil {
				t.Fatalf("Load: unexpected error: %v", err)
			}
			if len(got) != tc.want {
				t.Errorf("Load: got %d bytes, want %d", len(got), tc.want)
			}
			// Whatever the source, the answer is the same: nothing this small
			// is a document, and detection says so rather than guessing.
			if _, err := Detect(context.Background(), tc.src, DefaultLimits()); !errors.Is(err, ErrUnsupportedFormat) {
				t.Errorf("Detect: got error %v, want one wrapping %v", err, ErrUnsupportedFormat)
			}
		})
	}
}

func TestLoadFileFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		name    string
		src     Source
		wantIs  error
		wantAny bool
	}{
		{name: "a path that does not exist", src: File(filepath.Join(dir, "absent.pdf")), wantAny: true},
		{name: "a directory", src: File(dir), wantIs: ErrUnsupportedFormat},
		{name: "a nil reader", src: Reader(nil), wantIs: ErrUnsupportedFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(context.Background(), tc.src, DefaultLimits())
			if err == nil {
				t.Fatal("Load: no error, want one")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("Load: got error %v, want one wrapping %v", err, tc.wantIs)
			}
		})
	}
}

func TestLoadDoesNotCopyASlice(t *testing.T) {
	t.Parallel()

	// The root package documents that a slice source is not copied, and the
	// peak memory of a sixty-mebibyte document depends on it staying true.
	body := []byte(samplePDF)
	got, err := Load(context.Background(), Bytes(body), DefaultLimits())
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if len(got) == 0 || &got[0] != &body[0] {
		t.Error("Load: a slice source was copied, want the caller's own backing array")
	}
}

func TestSourceOpenContract(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "four.bin")
	if err := os.WriteFile(path, []byte("ABCD"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name         string
		src          Source
		wantDeclared int64
		wantBody     string
	}{
		{name: "a slice knows its size", src: Bytes([]byte("ABCD")), wantDeclared: 4, wantBody: "ABCD"},
		{name: "an empty slice knows its size", src: Bytes(nil), wantDeclared: 0, wantBody: ""},
		{name: "a stream declares nothing", src: Reader(strings.NewReader("ABCD")), wantDeclared: -1, wantBody: "ABCD"},
		{name: "a file declares its directory entry", src: File(path), wantDeclared: 4, wantBody: "ABCD"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rc, declared, err := tc.src.Open()
			if err != nil {
				t.Fatalf("Open: unexpected error: %v", err)
			}
			defer func() { _ = rc.Close() }() // read-only

			if declared != tc.wantDeclared {
				t.Errorf("Open: declared %d, want %d", declared, tc.wantDeclared)
			}
			body, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("ReadAll: unexpected error: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Errorf("ReadAll: got %q, want %q", body, tc.wantBody)
			}
		})
	}
}

// failingReader stops part way, as a truncated upload or a dropped connection
// does.
type failingReader struct {
	body []byte
	at   int
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.at >= len(f.body) {
		return 0, f.err
	}
	n := copy(p, f.body[f.at:])
	f.at += n
	return n, nil
}

func TestLoadPropagatesAReadFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("connection reset")
	src := Reader(&failingReader{body: []byte(samplePDF[:10]), err: want})

	if _, err := Load(context.Background(), src, DefaultLimits()); !errors.Is(err, want) {
		t.Fatalf("Load: got error %v, want one wrapping %v", err, want)
	}
}

func TestLoadFileWithNoMeaningfulSize(t *testing.T) {
	t.Parallel()

	// A character device reports a size of zero in its directory entry and
	// may yield for ever, which is the case a declared size cannot bound and
	// the wrapped reader has to.
	tests := []struct {
		name    string
		path    string
		max     int64
		wantErr error
		wantLen int
	}{
		{name: "a device that is empty", path: "/dev/null", max: 4096},
		{name: "a device that never ends", path: "/dev/zero", max: 4096, wantErr: ErrLimitExceeded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fi, err := os.Stat(tc.path)
			if err != nil || fi.Mode().IsRegular() {
				t.Skipf("%s is not a character device here", tc.path)
			}

			got, err := Load(context.Background(), File(tc.path), Limits{MaxSourceBytes: tc.max})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Load: got error %v, want one wrapping %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("Load: got %d bytes, want %d", len(got), tc.wantLen)
			}
		})
	}
}

// cancellingReader cancels the context part way through, as a caller's
// deadline does while a body is still arriving.
type cancellingReader struct {
	cancel context.CancelFunc
	reads  int
}

func (c *cancellingReader) Read(p []byte) (int, error) {
	c.reads++
	if c.reads == 2 {
		c.cancel()
	}
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}

func TestLoadHonoursCancellation(t *testing.T) {
	t.Parallel()

	t.Run("a context cancelled before the call reads nothing", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		src := &lyingSource{declared: -1, body: []byte(samplePDF)}
		if _, err := Load(ctx, src, DefaultLimits()); !errors.Is(err, context.Canceled) {
			t.Fatalf("Load: got error %v, want one wrapping %v", err, context.Canceled)
		}
		if src.opened {
			t.Error("Load: opened a source for a context that was already cancelled")
		}
	})

	t.Run("a context cancelled mid-stream stops at the next check", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := &cancellingReader{cancel: cancel}
		// The ceiling is enormous, so nothing but the cancellation can stop
		// this: an endless source and a limit it will not reach for hours.
		if _, err := Load(ctx, Reader(r), Limits{MaxSourceBytes: 1 << 40}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Load: got error %v, want one wrapping %v", err, context.Canceled)
		}
		if r.reads > 8 {
			t.Errorf("Load: read %d times after cancellation, want to stop at the next check", r.reads)
		}
	})
}
