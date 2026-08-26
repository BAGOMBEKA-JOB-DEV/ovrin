package ovrin_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

type batchDoc struct {
	Total float64 `ovrin:"total amount,required,min=0"`
}

func csvSource(total string) ovrin.Source {
	return ovrin.Bytes([]byte("item,total\nconsulting," + total + "\n"))
}

// perSourceModel answers with whatever the document says, so each result is
// distinguishable and an out-of-order batch is visible.
type perSourceModel struct {
	delay time.Duration

	mu    sync.Mutex
	live  int
	peak  int
	calls int
}

func (m *perSourceModel) Generate(_ context.Context, req ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	m.mu.Lock()
	m.live++
	if m.live > m.peak {
		m.peak = m.live
	}
	m.calls++
	m.mu.Unlock()

	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Echo the amount back out of the page text, so each reply belongs to the
	// document that produced it and a mix-up is detectable.
	//
	// The text arrives normalised and wrapped in the untrusted-content
	// delimiters, so this looks for the last number rather than matching the
	// CSV it started as.
	var total float64
	for _, c := range req.Content {
		if v, ok := lastNumber(c.Text); ok {
			total = v
			break
		}
	}

	m.mu.Lock()
	m.live--
	m.mu.Unlock()

	return &ovrin.ModelResponse{JSON: []byte(fmt.Sprintf(`{"total":%v}`, total))}, nil
}

func (m *perSourceModel) peakInFlight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peak
}

// Results come back in the order the sources were given, whatever order they
// finished in. A batch API that returned them in completion order would make
// every caller build the mapping itself, and get it wrong once.
func TestBatchKeepsInputOrder(t *testing.T) {
	t.Parallel()

	m := &perSourceModel{}
	c := ovrin.New(ovrin.WithModel(m), ovrin.WithConcurrency(4))

	amounts := []string{"10.00", "20.00", "30.00", "40.00", "50.00", "60.00"}
	srcs := make([]ovrin.Source, len(amounts))
	for i, a := range amounts {
		srcs[i] = csvSource(a)
	}

	out := ovrin.ExtractBatch[batchDoc](context.Background(), c, srcs)

	if len(out) != len(srcs) {
		t.Fatalf("got %d results for %d sources", len(out), len(srcs))
	}
	for i, r := range out {
		if r.Err != nil {
			t.Errorf("source %d: %v", i, r.Err)
			continue
		}
		if r.Index != i {
			t.Errorf("result %d carries Index %d", i, r.Index)
		}
		want := float64((i + 1) * 10)
		if r.Result.Data.Total != want {
			t.Errorf("result %d has total %v, want %v — results are out of order "+
				"or a reply was matched to the wrong document",
				i, r.Result.Data.Total, want)
		}
	}
}

// One bad document must not cost the good ones. A loop that stops at the first
// failure in a thousand-file directory throws away everything that worked.
func TestBatchIsolatesFailures(t *testing.T) {
	t.Parallel()

	c := ovrin.New(ovrin.WithModel(&perSourceModel{}), ovrin.WithConcurrency(3))

	srcs := []ovrin.Source{
		csvSource("10.00"),
		ovrin.Bytes([]byte("this is not a document ovrin can detect")),
		csvSource("30.00"),
		ovrin.File("testdata/does-not-exist.pdf"),
		csvSource("50.00"),
	}

	out := ovrin.ExtractBatch[batchDoc](context.Background(), c, srcs)

	if len(out) != len(srcs) {
		t.Fatalf("got %d results for %d sources", len(out), len(srcs))
	}
	for _, i := range []int{0, 2, 4} {
		if out[i].Err != nil {
			t.Errorf("source %d failed although it is a good document: %v", i, out[i].Err)
		} else if out[i].Result == nil {
			t.Errorf("source %d returned neither a result nor an error", i)
		}
	}
	for _, i := range []int{1, 3} {
		if out[i].Err == nil {
			t.Errorf("source %d succeeded although it is not readable", i)
		}
		if out[i].Result != nil {
			t.Errorf("source %d returned a Result alongside an error", i)
		}
	}

	// A missing file is the caller's typo, not a bug in ovrin.
	if !errors.Is(out[3].Err, ovrin.ErrNoContent) {
		t.Errorf("a missing file classified as %v", out[3].Err)
	}
}

// The batch runs several documents at once, bounded by WithConcurrency.
func TestBatchRunsConcurrentlyWithinTheBound(t *testing.T) {
	t.Parallel()

	m := &perSourceModel{delay: 30 * time.Millisecond}
	c := ovrin.New(ovrin.WithModel(m), ovrin.WithConcurrency(4))

	srcs := make([]ovrin.Source, 8)
	for i := range srcs {
		srcs[i] = csvSource("10.00")
	}

	out := ovrin.ExtractBatch[batchDoc](context.Background(), c, srcs)
	for i, r := range out {
		if r.Err != nil {
			t.Fatalf("source %d: %v", i, r.Err)
		}
	}

	if got := m.peakInFlight(); got < 2 {
		t.Errorf("peak documents in flight = %d; the batch ran one at a time", got)
	}
	if got := m.peakInFlight(); got > 4 {
		t.Errorf("peak documents in flight = %d, above the bound of 4", got)
	}
}

// A cancelled batch says what happened to each document rather than returning
// one error for the whole run.
func TestBatchReportsCancellationPerSource(t *testing.T) {
	t.Parallel()

	c := ovrin.New(ovrin.WithModel(&perSourceModel{}), ovrin.WithConcurrency(1))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srcs := []ovrin.Source{csvSource("10.00"), csvSource("20.00"), csvSource("30.00")}
	out := ovrin.ExtractBatch[batchDoc](ctx, c, srcs)

	if len(out) != len(srcs) {
		t.Fatalf("got %d results for %d sources", len(out), len(srcs))
	}
	for i, r := range out {
		if r.Err == nil {
			t.Errorf("source %d succeeded under a cancelled context", i)
		}
		if r.Index != i {
			t.Errorf("result %d carries Index %d", i, r.Index)
		}
	}
}

// An empty batch is an ordinary thing to ask for — a directory with no
// documents in it — and is not an error.
func TestAnEmptyBatchIsNotAnError(t *testing.T) {
	t.Parallel()

	c := ovrin.New(ovrin.WithModel(&perSourceModel{}))
	if out := ovrin.ExtractBatch[batchDoc](context.Background(), c, nil); out != nil {
		t.Errorf("ExtractBatch(nil) = %v, want nil", out)
	}
}

// lastNumber returns the final decimal number in s.
func lastNumber(s string) (float64, bool) {
	fields := strings.Fields(s)
	for i := len(fields) - 1; i >= 0; i-- {
		if v, err := strconv.ParseFloat(strings.Trim(fields[i], "],"), 64); err == nil {
			return v, true
		}
	}
	return 0, false
}
