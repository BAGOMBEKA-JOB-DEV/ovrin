package ovrin_test

import (
	"context"
	"sync"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// A Client is documented as safe for concurrent use, and Extract is documented
// as copying the configuration so that two calls with different options cannot
// interfere. Both were true of every field except one.
//
// config carries exactly one slice — the cross-field rules — and Extract's copy
// is shallow, so the copy shares a backing array. WithCrossField appends. If
// the Client's slice had spare capacity, two concurrent Extract calls each
// adding a per-call rule wrote the same array slot: a data race under -race,
// and each call silently evaluating the other's rule.
func TestConcurrentExtractsDoNotShareCrossFieldRules(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	// Three rules added as two options: two, then one.
	//
	// The shape matters. append(nil, a, b) gives len 2 cap 2; appending c
	// then grows to len 3 cap 4, leaving one spare slot. That spare slot is
	// the bug — every concurrent Extract that appends a per-call rule writes
	// it. A Client built with a single option has len == cap and would not
	// reproduce this, which is exactly why the fix clips rather than trusting
	// append's growth.
	noop := func(name string) ovrin.CrossFieldRule {
		return ovrin.CrossFieldFunc(name, func(ovrin.CrossFields) ovrin.CrossFieldResult {
			return ovrin.CrossFieldResult{Name: name, Applicable: false}
		})
	}
	base := ovrin.New(
		ovrin.WithModel(replyModel{reply: map[string]any{"total": 10.0}}),
		ovrin.WithCrossField(noop("a"), noop("b")),
		ovrin.WithCrossField(noop("c")),
	)

	// Each goroutine adds its own rule and records which rules ran. Under the
	// bug, a goroutine sees a name it never added.
	const workers = 8
	var wg sync.WaitGroup
	seen := make([][]string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			mine := string(rune('A' + i))
			var mu sync.Mutex
			var ran []string

			rule := ovrin.CrossFieldFunc(mine, func(ovrin.CrossFields) ovrin.CrossFieldResult {
				mu.Lock()
				ran = append(ran, mine)
				mu.Unlock()
				return ovrin.CrossFieldResult{Name: mine, Applicable: false}
			})

			_, err := ovrin.Extract[Doc](context.Background(), base,
				ovrin.Bytes([]byte("item,total\nconsulting,10.00\n")),
				ovrin.WithCrossField(rule))
			if err != nil {
				t.Errorf("worker %d: Extract: %v", i, err)
				return
			}
			mu.Lock()
			seen[i] = append([]string(nil), ran...)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i, ran := range seen {
		mine := string(rune('A' + i))
		for _, name := range ran {
			if name != mine {
				t.Errorf("worker %d ran rule %q, which belongs to another extraction",
					i, name)
			}
		}
	}
}

// The Client itself must not be mutated by an extraction that overlays
// options, or the next caller inherits them.
func TestExtractDoesNotMutateTheClient(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	c := ovrin.New(ovrin.WithModel(replyModel{reply: map[string]any{"total": 10.0}}))
	src := ovrin.Bytes([]byte("item,total\nconsulting,10.00\n"))

	var ran int
	rule := ovrin.CrossFieldFunc("counted", func(ovrin.CrossFields) ovrin.CrossFieldResult {
		ran++
		return ovrin.CrossFieldResult{Name: "counted", Applicable: false}
	})

	if _, err := ovrin.Extract[Doc](context.Background(), c, src, ovrin.WithCrossField(rule)); err != nil {
		t.Fatalf("first Extract: %v", err)
	}
	before := ran

	// A second extraction, without the option. The rule must not run again.
	if _, err := ovrin.Extract[Doc](context.Background(), c, src); err != nil {
		t.Fatalf("second Extract: %v", err)
	}
	if ran != before {
		t.Errorf("a per-call cross-field rule survived into the next extraction "+
			"(ran %d times, want %d)", ran, before)
	}
}
