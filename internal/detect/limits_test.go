package detect

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

// allLimits is every limit that can appear on an error. A new one added
// without an option name fails TestEveryLimitNamesTheOptionThatRaisesIt.
var allLimits = []Limit{
	LimitSourceBytes,
	LimitDecompressedBytes,
	LimitStreamBytes,
	LimitTextBytes,
	LimitPages,
	LimitDepth,
	LimitObjects,
	LimitPagePixels,
}

func TestEveryLimitNamesTheOptionThatRaisesIt(t *testing.T) {
	t.Parallel()

	for _, limit := range allLimits {
		t.Run(limit.String(), func(t *testing.T) {
			t.Parallel()

			option := limit.Option()
			if option == "" {
				t.Fatalf("%s has no option: a caller told only that a limit was hit cannot act on it", limit)
			}
			if !strings.HasPrefix(option, "With") {
				t.Errorf("%s names %q, want the name of a functional option", limit, option)
			}
			err := exceeded(limit, 1000)
			msg := err.Error()
			if !strings.Contains(msg, limit.String()) {
				t.Errorf("error %q does not name the limit", msg)
			}
			if !strings.Contains(msg, option) {
				t.Errorf("error %q does not name %s", msg, option)
			}
			if !errors.Is(err, ErrLimitExceeded) {
				t.Errorf("error %q does not wrap ErrLimitExceeded", msg)
			}
			if strings.HasSuffix(msg, ".") || msg != strings.ToLower(msg[:1])+msg[1:] {
				t.Errorf("error %q is not lowercase and unpunctuated", msg)
			}
		})
	}
}

func TestLimitErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit Limit
		max   int64
		want  string
	}{
		{
			name:  "a byte ceiling reads in the units it was set in",
			limit: LimitSourceBytes,
			max:   64 << 20,
			want:  "source bytes limit exceeded: maximum 64 MiB, raise with WithMaxSourceBytes",
		},
		{
			name:  "an odd byte ceiling falls back to bytes",
			limit: LimitStreamBytes,
			max:   1234,
			want:  "stream bytes limit exceeded: maximum 1234 bytes, raise with WithMaxStreamBytes",
		},
		{
			name:  "a count is a count",
			limit: LimitPages,
			max:   1000,
			want:  "pages limit exceeded: maximum 1000, raise with WithMaxPages",
		},
		{
			name:  "a depth is a count too",
			limit: LimitDepth,
			max:   64,
			want:  "object-graph depth limit exceeded: maximum 64, raise with WithMaxDepth",
		},
		{
			name:  "a limit with no option still says which one",
			limit: LimitUnknown,
			max:   1,
			want:  "unknown limit exceeded: maximum 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := (&LimitError{Limit: tc.limit, Max: tc.max}).Error()
			if got != tc.want {
				t.Errorf("Error:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestLimitsNormalisedFillsInDefaultsRatherThanRefusingEverything(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Limits
		want Limits
	}{
		{name: "the zero value is the defaults", in: Limits{}, want: DefaultLimits()},
		{name: "the defaults are unchanged", in: DefaultLimits(), want: DefaultLimits()},
		{
			name: "a negative field is a mistake and takes the default",
			in:   Limits{MaxPages: -1},
			want: DefaultLimits(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.in.Normalised(); got != tc.want {
				t.Errorf("Normalised: got %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("a set field survives", func(t *testing.T) {
		t.Parallel()

		got := Limits{MaxPages: 5}.Normalised()
		if got.MaxPages != 5 {
			t.Errorf("Normalised: got %d pages, want the 5 that were asked for", got.MaxPages)
		}
		if got.MaxSourceBytes != DefaultLimits().MaxSourceBytes {
			t.Errorf("Normalised: got %d source bytes, want the default", got.MaxSourceBytes)
		}
	})

	t.Run("every default is finite", func(t *testing.T) {
		t.Parallel()

		d := DefaultLimits()
		checks := []struct {
			name string
			n    int64
		}{
			{"source bytes", d.MaxSourceBytes},
			{"decompressed bytes", d.MaxDecompressedBytes},
			{"stream bytes", d.MaxStreamBytes},
			{"text bytes", d.MaxTextBytes},
			{"pages", int64(d.MaxPages)},
			{"depth", int64(d.MaxDepth)},
			{"objects", int64(d.MaxObjects)},
			{"page pixels", int64(d.MaxPagePixels)},
		}
		for _, c := range checks {
			if c.n <= 0 || c.n == math.MaxInt64 {
				t.Errorf("%s default is %d, want a finite positive ceiling", c.name, c.n)
			}
		}
	})
}

func TestCheckPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		max     int
		pages   int
		wantErr bool
	}{
		{name: "an ordinary invoice", max: 1000, pages: 3},
		{name: "exactly at the ceiling", max: 1000, pages: 1000},
		{name: "a legitimate loan file above the default", max: 1000, pages: 1200, wantErr: true},
		{name: "the loan file once the ceiling is raised", max: 2000, pages: 1200},
		{name: "a hundred thousand pages", max: 1000, pages: 100_000, wantErr: true},
		{name: "no pages at all", max: 1000, pages: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := Limits{MaxPages: tc.max}.CheckPages(tc.pages)
			if tc.wantErr {
				var le *LimitError
				if !errors.As(err, &le) || le.Limit != LimitPages {
					t.Fatalf("CheckPages: got %v, want the pages limit", err)
				}
				if !strings.Contains(le.Error(), "WithMaxPages") {
					t.Errorf("CheckPages: error %q does not name WithMaxPages", le)
				}
				return
			}
			if err != nil {
				t.Errorf("CheckPages: unexpected error: %v", err)
			}
		})
	}
}

func TestCheckPixels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		max           int
		width, height int
		wantErr       bool
	}{
		{name: "a4 at 300 dpi", max: 50_000_000, width: 2480, height: 3508},
		{name: "a media box that rasterises larger than memory", max: 50_000_000, width: 200_000, height: 200_000, wantErr: true},
		{name: "exactly at the ceiling", max: 1_000_000, width: 1000, height: 1000},
		{name: "one pixel past the ceiling", max: 1_000_000, width: 1000, height: 1001, wantErr: true},
		{
			name:    "dimensions chosen to overflow the product",
			max:     50_000_000,
			width:   math.MaxInt32,
			height:  math.MaxInt32,
			wantErr: true,
		},
		{name: "a zero dimension allocates nothing", max: 50_000_000, width: 0, height: 10_000_000},
		{name: "a negative dimension allocates nothing", max: 50_000_000, width: -1, height: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := Limits{MaxPagePixels: tc.max}.CheckPixels(tc.width, tc.height)
			if tc.wantErr {
				var le *LimitError
				if !errors.As(err, &le) || le.Limit != LimitPagePixels {
					t.Fatalf("CheckPixels: got %v, want the page pixel limit", err)
				}
				return
			}
			if err != nil {
				t.Errorf("CheckPixels: unexpected error: %v", err)
			}
		})
	}
}

func TestCheckObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		max     int
		objects int
		wantErr bool
	}{
		{name: "an ordinary document", max: 500_000, objects: 4_000},
		{name: "exactly at the ceiling", max: 500_000, objects: 500_000},
		{name: "a cross-reference table claiming millions", max: 500_000, objects: 5_000_000, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := Limits{MaxObjects: tc.max}.CheckObjects(tc.objects)
			if tc.wantErr != (err != nil) {
				t.Fatalf("CheckObjects: got %v, want an error: %v", err, tc.wantErr)
			}
		})
	}
}

func TestCounter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		max     int64
		adds    []int64
		wantErr bool
		wantUse int64
	}{
		{name: "inside the budget", max: 100, adds: []int64{10, 20, 30}, wantUse: 60},
		{name: "exactly at the budget", max: 100, adds: []int64{50, 50}, wantUse: 100},
		{name: "one unit past the budget", max: 100, adds: []int64{50, 51}, wantErr: true, wantUse: 50},
		{name: "many small spends add up", max: 100, adds: []int64{1, 1, 1, 1, 1}, wantUse: 5},
		{name: "a spend that would overflow", max: 100, adds: []int64{math.MaxInt64}, wantErr: true},
		{name: "nothing is spent and nothing is refused", max: 100, adds: []int64{0, -5}, wantUse: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := NewCounter(LimitDecompressedBytes, tc.max)
			var err error
			for _, n := range tc.adds {
				if err = c.Add(n); err != nil {
					break
				}
			}
			if tc.wantErr {
				if !errors.Is(err, ErrLimitExceeded) {
					t.Fatalf("Add: got error %v, want one wrapping %v", err, ErrLimitExceeded)
				}
				// The failed spend must not be charged: a caller that checks
				// before it allocates has not allocated.
				if c.Used() != tc.wantUse {
					t.Errorf("Used: got %d after a refused spend, want %d", c.Used(), tc.wantUse)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: unexpected error: %v", err)
			}
			if c.Used() != tc.wantUse {
				t.Errorf("Used: got %d, want %d", c.Used(), tc.wantUse)
			}
			if c.Remaining() != tc.max-tc.wantUse {
				t.Errorf("Remaining: got %d, want %d", c.Remaining(), tc.max-tc.wantUse)
			}
		})
	}
}

func TestNilCounterSpendsNothing(t *testing.T) {
	t.Parallel()

	var c *Counter
	if err := c.Add(1 << 40); err != nil {
		t.Errorf("Add: got error %v on a nil counter, want none", err)
	}
	if c.Used() != 0 {
		t.Errorf("Used: got %d, want 0", c.Used())
	}
	if c.Remaining() != math.MaxInt64 {
		t.Errorf("Remaining: got %d, want everything", c.Remaining())
	}
	if c.Limit() != LimitUnknown {
		t.Errorf("Limit: got %s, want unknown", c.Limit())
	}
}

func TestCounterIsSharedAcrossGoroutines(t *testing.T) {
	t.Parallel()

	// Pages are processed in parallel and share one budget, so the budget has
	// to be exact under contention rather than approximately right.
	const (
		workers = 8
		spends  = 1000
		unit    = 1
	)
	c := NewCounter(LimitDecompressedBytes, workers*spends*unit)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < spends; j++ {
				if err := c.Add(unit); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := c.Used(); got != workers*spends*unit {
		t.Errorf("Used: got %d, want %d", got, workers*spends*unit)
	}
	if err := c.Add(1); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("Add: got %v past an exhausted budget, want a limit error", err)
	}
}

// chain is a structure nested as deep as anyone cares to build it, which is
// what a hostile page tree is.
type chain struct{ next *chain }

func buildChain(depth int) *chain {
	head := &chain{}
	for i := 0; i < depth; i++ {
		head = &chain{next: head}
	}
	return head
}

// walk is the shape every recursive parser in ovrin takes: the budget is a
// parameter, spent on the way down and never given back.
func walk(n *chain, d Depth) (int, error) {
	d, err := d.Descend()
	if err != nil {
		return 0, err
	}
	if n.next == nil {
		return 1, nil
	}
	reached, err := walk(n.next, d)
	return reached + 1, err
}

func TestDepthBudgetStopsADeeplyNestedStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		depth   int
		budget  int
		wantErr bool
		want    int
	}{
		{name: "an ordinary page tree", depth: 5, budget: 64, want: 6},
		{name: "exactly at the budget", depth: 63, budget: 64, want: 64},
		{name: "one level past the budget", depth: 64, budget: 64, wantErr: true, want: 64},
		{name: "a page tree thousands deep", depth: 10_000, budget: 64, wantErr: true, want: 64},
		{name: "a page tree thousands deep against a raised budget", depth: 10_000, budget: 20_000, want: 10_001},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reached, err := walk(buildChain(tc.depth), NewDepth(tc.budget))
			if tc.wantErr {
				var le *LimitError
				if !errors.As(err, &le) || le.Limit != LimitDepth {
					t.Fatalf("walk: got %v, want the depth limit", err)
				}
				if !strings.Contains(le.Error(), "WithMaxDepth") {
					t.Errorf("walk: error %q does not name WithMaxDepth", le)
				}
				if reached != tc.want {
					t.Errorf("walk: reached depth %d, want to stop at %d", reached, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("walk: unexpected error: %v", err)
			}
			if reached != tc.want {
				t.Errorf("walk: reached depth %d, want %d", reached, tc.want)
			}
		})
	}
}

func TestDepthZeroValueRefusesToDescend(t *testing.T) {
	t.Parallel()

	var d Depth
	if _, err := d.Descend(); !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("Descend: got %v on an unconstructed budget, want a limit error", err)
	}
	if got := (Limits{}).Depth().Remaining(); got != defaultMaxDepth {
		t.Errorf("Limits.Depth: got %d levels, want the default %d", got, defaultMaxDepth)
	}
}

func TestDepthDescentDoesNotDeepenASibling(t *testing.T) {
	t.Parallel()

	// A budget is a value, so spending it down one branch leaves the caller's
	// own budget where it was. A counter in a struct would not.
	d := NewDepth(4)
	child, err := d.Descend()
	if err != nil {
		t.Fatalf("Descend: unexpected error: %v", err)
	}
	if child.Remaining() != 3 {
		t.Errorf("child: got %d levels, want 3", child.Remaining())
	}
	if d.Remaining() != 4 {
		t.Errorf("parent: got %d levels, want the 4 it started with", d.Remaining())
	}
}

func TestLimitString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit Limit
		want  string
	}{
		{name: "the zero value names itself", limit: LimitUnknown, want: "unknown"},
		{name: "a known limit is its own name", limit: LimitPages, want: "pages"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.limit.String(); got != tc.want {
				t.Errorf("String: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimitQuantityUnits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit Limit
		max   int64
		want  string
	}{
		{name: "gibibytes", limit: LimitDecompressedBytes, max: 2 << 30, want: "2 GiB"},
		{name: "mebibytes", limit: LimitStreamBytes, max: 64 << 20, want: "64 MiB"},
		{name: "kibibytes", limit: LimitTextBytes, max: 32 << 10, want: "32 KiB"},
		{name: "an awkward number of bytes", limit: LimitSourceBytes, max: 3, want: "3 bytes"},
		{name: "a count carries no unit", limit: LimitObjects, max: 500_000, want: "500000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.limit.quantity(tc.max); got != tc.want {
				t.Errorf("quantity: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCounterReportsItsOwnLimit(t *testing.T) {
	t.Parallel()

	c := NewCounter(LimitTextBytes, 100)
	if c.Limit() != LimitTextBytes {
		t.Errorf("Limit: got %s, want %s", c.Limit(), LimitTextBytes)
	}
}
