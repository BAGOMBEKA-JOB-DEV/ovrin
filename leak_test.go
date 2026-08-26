// The root pipeline, checked for goroutine leaks.
//
// Gap closed here: docs/rules.md §3.6 requires every test that starts a
// goroutine to assert it stopped, and the pipeline started none until page
// acquisition was given bounded concurrency (pipeline.go, acquirePages). Its
// goroutines are joined by a WaitGroup, and the whole value of that WaitGroup
// is a promise nothing here tested: that a page whose provider failed, and a
// page abandoned half way through by a cancelled context, are joined exactly
// like a page that succeeded. A leaked acquisition goroutine holds a renderer's
// page of pixels and an in-flight request at a provider that charges for it, so
// the failure mode is a service that grows until it is restarted.
//
// The three shapes below are the three ways acquirePages can exit: every page
// read, a provider failing, and the caller giving up. They run in one test
// because a leak is measured against a baseline and the baseline has to be
// taken once, with nothing else running.
package ovrin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

type leakDoc struct {
	Total float64 `ovrin:"total amount,required,min=0"`
}

type leakModel struct{}

func (leakModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	b, err := json.Marshal(map[string]any{"total": 2500.0})
	if err != nil {
		return nil, err
	}
	return &ovrin.ModelResponse{JSON: b}, nil
}

// countingRenderer rasterises a small blank page and counts the calls.
//
// Small on purpose: this test renders a page per goroutine per extraction, and
// a realistic 300 dpi surface would be thirty megabytes of pixels each. What is
// being measured is goroutines, not fidelity.
type countingRenderer struct {
	mu sync.Mutex
	n  int
}

func (r *countingRenderer) Render(_ context.Context, _ ovrin.Document, _, _ int) (image.Image, error) {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	return image.NewRGBA(image.Rect(0, 0, 100, 130)), nil
}

func (r *countingRenderer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// blockingRenderer parks until the context ends and then takes a moment to
// unwind, which is how a page is caught mid-acquisition by a caller that gave
// up.
//
// The moment matters. Tearing down a real renderer is not instantaneous, and
// the whole question this test asks is whether Extract returns before its
// acquisition goroutines have finished — which is unanswerable if they finish
// in the same instant they are told to. inFlight is what makes the answer
// checkable rather than a matter of timing.
type blockingRenderer struct {
	entered chan struct{}

	mu       sync.Mutex
	inFlight int
}

// unwind is how long a page takes to give up after its context ends. It is far
// longer than a missing join would take to be observable and far shorter than
// anything a person waits for.
const unwind = 100 * time.Millisecond

func (r *blockingRenderer) Render(ctx context.Context, _ ovrin.Document, _, _ int) (image.Image, error) {
	r.mu.Lock()
	r.inFlight++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}()

	select {
	case r.entered <- struct{}{}:
	default: // a later page: the first one already announced arrival
	}
	<-ctx.Done()
	time.Sleep(unwind)
	return nil, ctx.Err()
}

func (r *blockingRenderer) flight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight
}

// leakOCR reads a couple of words inside the rendered page's box.
type leakOCR struct{}

func (leakOCR) Name() string { return "leak" }

func (leakOCR) Recognise(_ context.Context, _ ovrin.Page) (*ovrin.Recognition, error) {
	return &ovrin.Recognition{Confidence: 0.9, Words: []ovrin.Word{
		{Text: "APPENDIX", Confidence: 0.9, Box: ovrin.Rect{MinX: 1, MinY: 2, MaxX: 12, MaxY: 6}},
		{Text: "A", Confidence: 0.9, Box: ovrin.Rect{MinX: 13, MinY: 2, MaxX: 16, MaxY: 6}},
	}}, nil
}

// failingOCR is a provider that is configured, reachable and broken, which is
// the ordinary way a page goes unread.
type failingOCR struct{}

func (failingOCR) Name() string { return "broken" }

func (failingOCR) Recognise(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
	return nil, fmt.Errorf("the recogniser is down")
}

// mixedPDF is one readable text page followed by blank ones.
//
// A page with no content stream has no characters, fails stage 2's density
// threshold, and is handed to acquirePages — so the number of blank pages is
// the number of goroutines the pipeline starts. It builds on pdfWith and
// pdfBody in findings_test.go rather than a fixture file, because a test about
// goroutine counts needs to choose the page count.
func mixedPDF(blanks int) []byte {
	stream := 3 + blanks + 1
	kids := "3 0 R"
	for i := 0; i < blanks; i++ {
		kids += fmt.Sprintf(" %d 0 R", 4+i)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, blanks+1),
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 << /Type /Font /Subtype /Type1 "+
			"/BaseFont /Helvetica /Encoding /WinAnsiEncoding >> >> >> /Contents %d 0 R >>", stream),
	}
	for i := 0; i < blanks; i++ {
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")
	}
	objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(pdfBody), pdfBody))
	return pdfWith(objs, "")
}

// The pipeline must not be running anything of its own once Extract has
// returned, whether the document was read, refused or abandoned.
//
// Deliberately not parallel: the count is process-wide, so a test running
// alongside this one is a goroutine this one gets blamed for.
func TestThePipelineLeavesNoGoroutinesRunning(t *testing.T) {
	const blanks = 6
	doc := mixedPDF(blanks)

	// The baseline is taken after a warm-up extraction, so that whatever the
	// runtime and the standard library start on first use — and never stop —
	// is counted on both sides rather than blamed on the pipeline.
	renderer := &countingRenderer{}
	c := ovrin.New(
		ovrin.WithModel(leakModel{}),
		ovrin.WithRenderer(renderer),
		ovrin.WithOCR(leakOCR{}),
	)
	if _, err := ovrin.Extract[leakDoc](context.Background(), c, ovrin.Bytes(doc)); err != nil {
		t.Fatalf("warm-up Extract: %v", err)
	}
	// A fixture check, not a leak check: it waits, so that a pipeline which
	// returned before its goroutines had finished still reaches the assertions
	// below that are about exactly that.
	if got := renderedPages(renderer, blanks); got != blanks {
		t.Fatalf("the fixture rendered %d pages, want %d: no page reached acquirePages, "+
			"so this test would prove nothing", got, blanks)
	}

	before := settledGoroutines()

	// Every page read.
	for i := 0; i < 3; i++ {
		if _, err := ovrin.Extract[leakDoc](context.Background(), c, ovrin.Bytes(doc)); err != nil {
			t.Fatalf("Extract %d: %v", i, err)
		}
	}

	// A provider that fails on every page. Acquisition collects the failures
	// rather than propagating them, so the extraction still returns a result —
	// and every goroutine that reported one must still be joined.
	broken := ovrin.New(
		ovrin.WithModel(leakModel{}),
		ovrin.WithRenderer(renderer),
		ovrin.WithOCR(failingOCR{}),
	)
	res, err := ovrin.Extract[leakDoc](context.Background(), broken, ovrin.Bytes(doc))
	if err != nil {
		t.Fatalf("Extract with a failing provider: %v", err)
	}
	if !res.NeedsReview {
		t.Error("every page but one went unread and the result was not flagged for review")
	}

	// A caller that gives up part way through. The renderer parks until the
	// context ends, so the cancellation lands while acquisition goroutines are
	// in flight, which is the case a WaitGroup that is not waited on loses.
	blocked := &blockingRenderer{entered: make(chan struct{}, 1)}
	hung := ovrin.New(
		ovrin.WithModel(leakModel{}),
		ovrin.WithRenderer(blocked),
		ovrin.WithOCR(leakOCR{}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ovrin.Extract[leakDoc](ctx, hung, ovrin.Bytes(doc)) //nolint:errcheck // abandonment is the point
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("no page reached the renderer, so nothing was abandoned mid-flight")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Extract did not return after its context was cancelled; " +
			"an acquisition goroutine is not being joined")
	}

	// Extract has returned. Nothing it started may still be running: a page
	// still in flight is a renderer holding pixels and, in a real deployment,
	// a request at a provider that is still being charged for.
	if n := blocked.flight(); n != 0 {
		t.Errorf("Extract returned with %d page(s) still being acquired; "+
			"acquirePages is not waiting for its goroutines", n)
	}

	after, ok := goroutinesSettleTo(before)
	if !ok {
		t.Errorf("goroutine leak: %d before, %d after (waited %v)\n\n%s",
			before, after, leakSettleTimeout, interestingLeakStacks())
	}
}

// renderedPages waits for the renderer to have been called want times, and
// returns what it saw.
func renderedPages(r *countingRenderer, want int) int {
	deadline := time.Now().Add(leakSettleTimeout)
	for {
		n := r.calls()
		if n >= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// leakSettleTimeout bounds how long the check waits for goroutines to wind
// down. One that is genuinely exiting does so in microseconds; the budget
// exists so a loaded runner does not produce a false positive.
const leakSettleTimeout = 2 * time.Second

// goroutinesSettleTo waits for the count to come back to want, and reports
// what it was when it gave up.
//
// Waiting rather than sampling once is the difference between a leak check and
// a race: a goroutine returning from Extract's last defer is still counted for
// the few microseconds it takes to reach its exit.
func goroutinesSettleTo(want int) (int, bool) {
	deadline := time.Now().Add(leakSettleTimeout)
	for {
		n := pipelineGoroutines()
		if n <= want {
			return n, true
		}
		if time.Now().After(deadline) {
			return n, false
		}
		// Yield rather than spin: the goroutines being waited on need
		// scheduler time to reach their exit.
		time.Sleep(5 * time.Millisecond)
	}
}

// settledGoroutines waits for the count to stop moving, then returns it.
//
// Without this, a baseline taken while an earlier test is still winding down
// reads low and this test is blamed for the difference.
func settledGoroutines() int {
	deadline := time.Now().Add(leakSettleTimeout)
	prev := pipelineGoroutines()
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		cur := pipelineGoroutines()
		if cur == prev {
			return cur
		}
		prev = cur
	}
	return prev
}

// pipelineGoroutines counts the goroutines that could plausibly belong to the
// pipeline.
//
// runtime.NumGoroutine counts everything in the process, including the test
// harness itself and whatever the runtime keeps parked, and those move for
// reasons that have nothing to do with the code under test. Counting stacks and
// excluding the ones the harness owns is the difference between a check that
// measures the pipeline and one that measures the test binary — the same
// approach internal/adaptertest takes, for the same reason.
func pipelineGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]

	n := 0
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if stack == "" || isHarnessGoroutine(stack) {
			continue
		}
		n++
	}
	return n
}

// harnessFrames names goroutines that belong to the test binary and are never
// the leak being hunted.
//
// Deliberately narrow, for the reason internal/adaptertest gives: a marker
// broad enough to match "anything parked" would match the leaked goroutine too
// and quietly turn the check into one that can never fail.
var harnessFrames = []string{
	"testing.tRunner",       // the harness running each test
	"testing.(*M).Run",      // the test binary's main
	"os/signal.signal_recv", // signal handler, started by the runtime
	"runtime.ensureSigM",    // its companion
}

func isHarnessGoroutine(stack string) bool {
	for _, marker := range harnessFrames {
		if strings.Contains(stack, marker) {
			return true
		}
	}
	return false
}

// interestingLeakStacks renders the goroutine dump with the harness removed, so
// a failure shows the leak rather than the scheduler.
func interestingLeakStacks() string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]

	var keep []string
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if stack == "" || isHarnessGoroutine(stack) {
			continue
		}
		keep = append(keep, stack)
	}
	sort.Strings(keep)

	const maxReported = 10
	if len(keep) > maxReported {
		keep = keep[:maxReported]
	}
	return strings.Join(keep, "\n\n")
}
