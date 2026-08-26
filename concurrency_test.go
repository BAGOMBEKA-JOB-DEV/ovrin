// The bound WithConcurrency has always documented, tested at the place it is
// applied.
//
// An internal test rather than an external one because the unit under test is
// acquirePages, and driving it through Extract would need a many-page scanned
// fixture whose real cost is rasterisation — which would make this a slow test
// measuring the renderer rather than a fast one measuring the bound.

package ovrin

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"
)

// countingRenderer records how many renders overlap.
//
// The peak is the assertion, not wall-clock: elapsed time is a property of the
// machine, but "were two pages ever in flight at once" is a property of the
// code, and it is exactly what WithConcurrency promises.
type countingRenderer struct {
	delay time.Duration

	mu    sync.Mutex
	live  int
	peak  int
	pages []int
}

func (r *countingRenderer) Render(ctx context.Context, _ Document, page, dpi int) (image.Image, error) {
	r.mu.Lock()
	r.live++
	if r.live > r.peak {
		r.peak = r.live
	}
	r.pages = append(r.pages, page)
	r.mu.Unlock()

	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
	}

	r.mu.Lock()
	r.live--
	r.mu.Unlock()
	return image.NewRGBA(image.Rect(0, 0, 8, 11)), nil
}

func (r *countingRenderer) peakInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

// wordOCRStub returns one word for any page.
type wordOCRStub struct{}

func (wordOCRStub) Name() string { return "stub" }

func (wordOCRStub) Recognise(context.Context, Page) (*Recognition, error) {
	return &Recognition{
		Words: []Word{{
			Text:       "PAGE",
			Box:        Rect{MinX: 10, MinY: 10, MaxX: 60, MaxY: 24},
			Confidence: 0.9,
			Line:       1,
		}},
		Confidence: 0.9,
	}, nil
}

func acquireCfg(r Renderer, concurrency int) *config {
	c := defaults()
	c.renderer = r
	c.ocr = wordOCRStub{}
	c.concurrency = concurrency
	return &c
}

func TestAcquirePagesHonoursTheConcurrencyBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		concurrency int
		pages       int
		wantPeakMin int
		wantPeakMax int
	}{
		{
			// The case that regressed: WithConcurrency was set and never
			// read, so every page went one at a time whatever was asked for.
			name:        "four at a time overlaps",
			concurrency: 4, pages: 8, wantPeakMin: 2, wantPeakMax: 4,
		},
		{
			// A bound of one must mean one, or the option is decoration in
			// the other direction.
			name:        "one at a time is sequential",
			concurrency: 1, pages: 6, wantPeakMin: 1, wantPeakMax: 1,
		},
		{
			// Asking for more parallelism than there is work must not spawn
			// idle goroutines or exceed the page count.
			name:        "the bound is capped by the work available",
			concurrency: 16, pages: 2, wantPeakMin: 1, wantPeakMax: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &countingRenderer{delay: 30 * time.Millisecond}
			cfg := acquireCfg(r, tc.concurrency)

			want := make([]int, tc.pages)
			for i := range want {
				want[i] = i + 1
			}

			doc := Document{Kind: KindPDF, Pages: tc.pages, Data: []byte("%PDF-1.7\n")}
			byPage, unread, _ := acquirePages(context.Background(), cfg, doc, want, time.Now())

			if len(unread) != 0 {
				t.Fatalf("unread = %v, want none", unread)
			}
			if len(byPage) != tc.pages {
				t.Fatalf("acquired %d pages, want %d", len(byPage), tc.pages)
			}
			// Every page is present under its own number: a page finishing
			// early must not be filed against a page that started first.
			for _, n := range want {
				c, ok := byPage[n]
				if !ok {
					t.Errorf("page %d is missing from the result", n)
					continue
				}
				if c.Number != n {
					t.Errorf("page %d is filed as page %d", n, c.Number)
				}
			}

			if got := r.peakInFlight(); got < tc.wantPeakMin || got > tc.wantPeakMax {
				t.Errorf("peak pages in flight = %d, want between %d and %d",
					got, tc.wantPeakMin, tc.wantPeakMax)
			}
		})
	}
}

// A cancelled context stops pages that have not started, and reports them as
// unread rather than silently returning a short document.
func TestAcquirePagesStopsOnCancellation(t *testing.T) {
	t.Parallel()

	r := &countingRenderer{delay: 50 * time.Millisecond}
	cfg := acquireCfg(r, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pages := []int{1, 2, 3, 4}
	doc := Document{Kind: KindPDF, Pages: len(pages), Data: []byte("%PDF-1.7\n")}
	byPage, unread, _ := acquirePages(ctx, cfg, doc, pages, time.Now())

	if len(byPage) != 0 {
		t.Errorf("acquired %d pages under a cancelled context, want none", len(byPage))
	}
	if len(unread) != len(pages) {
		t.Errorf("unread = %v, want all %d pages reported", unread, len(pages))
	}
	// Reported in order, so a review reason naming them is diffable.
	for i, n := range unread {
		if n != i+1 {
			t.Errorf("unread = %v, want ascending page order", unread)
			break
		}
	}
}

// billingOCR reports one page unit per page, the way ocr/azure and
// ocr/textract do.
type billingOCR struct{}

func (billingOCR) Name() string { return "billing" }

func (billingOCR) Recognise(context.Context, Page) (*Recognition, error) {
	return &Recognition{
		Words: []Word{{
			Text: "PAGE", Box: Rect{MinX: 1, MinY: 1, MaxX: 9, MaxY: 9},
			Confidence: 0.9, Line: 1,
		}},
		Confidence: 0.9,
		Usage:      Usage{PageUnits: 1},
	}, nil
}

// What a reading cost has to reach the caller, or the field is decoration.
//
// Every OCR adapter fills Recognition.Usage and the pipeline used to discard
// it, so Metadata.Usage.PageUnits was structurally always zero — which made
// the otel page-unit metric flat and the evaluation harness's page-unit price
// unusable.
func TestOCRPageUnitsReachMetadata(t *testing.T) {
	t.Parallel()

	r := &countingRenderer{}
	cfg := defaults()
	cfg.renderer = r
	cfg.ocr = billingOCR{}
	cfg.concurrency = 2

	pages := []int{1, 2, 3}
	doc := Document{Kind: KindPDF, Pages: len(pages), Data: []byte("%PDF-1.7\n")}

	_, unread, used := acquirePages(context.Background(), &cfg, doc, pages, time.Now())
	if len(unread) != 0 {
		t.Fatalf("unread = %v, want none", unread)
	}
	if used.PageUnits != len(pages) {
		t.Errorf("PageUnits = %d after reading %d pages, want %d",
			used.PageUnits, len(pages), len(pages))
	}
}
