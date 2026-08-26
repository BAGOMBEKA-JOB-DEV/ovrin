package ovrin

import (
	"context"
	"sync"
)

// BatchResult is what one source in a batch produced.
//
// Exactly one of Result and Err is set. They are kept as separate fields
// rather than collapsed into a Result with an error inside it for the reason
// ADR-0004 gives: an extraction that failed produced nothing usable, and a
// half-filled Result invites a caller to read fields that were never extracted.
type BatchResult[T any] struct {
	// Index is the position of this source in the slice passed to
	// [ExtractBatch].
	//
	// Results come back in that same order, so this is redundant for a caller
	// ranging over them — and load-bearing for one that filters, sorts, or
	// collects only the failures and still needs to say which document.
	Index int

	// Result is what was extracted, or nil when Err is set.
	Result *Result[T]

	// Err is why this source produced nothing, or nil.
	//
	// It is classified exactly as [Extract]'s error is: test it with
	// [errors.Is] against the sentinels, never by its text.
	Err error
}

// ExtractBatch extracts from many sources, several at a time.
//
// Results are returned in the order the sources were given, whatever order
// they finished in. One document failing does not fail the batch: its entry
// carries the error and every other document is still extracted. That is the
// difference between a batch API and a loop — a loop that stops at the first
// bad scan in a thousand-file directory has thrown away nine hundred and
// ninety-nine good extractions.
//
// Concurrency is bounded by [WithConcurrency], which also bounds page-level
// work inside each extraction. The two multiply, so a batch of eight with a
// concurrency of four can have thirty-two page reads in flight; set it with
// the provider's rate limit in mind rather than the machine's core count.
//
// Cancelling ctx stops sources that have not started. Sources already running
// observe the cancellation through their own provider calls and return
// whatever error that produced, so a cancelled batch reports per document what
// happened to it rather than one error for the whole run.
//
// Passing no sources returns nil. That is not an error: a directory with no
// documents in it is an ordinary thing to point this at.
func ExtractBatch[T any](ctx context.Context, c *Client, srcs []Source, opts ...Option) []BatchResult[T] {
	if len(srcs) == 0 {
		return nil
	}

	limit := 1
	if c != nil && c.cfg.concurrency > 0 {
		limit = c.cfg.concurrency
	}
	if limit > len(srcs) {
		limit = len(srcs)
	}

	out := make([]BatchResult[T], len(srcs))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, src := range srcs {
		out[i].Index = i

		if err := ctx.Err(); err != nil {
			// Not started, and it is going to be told so rather than left as a
			// zero value that reads like a successful empty extraction.
			out[i].Err = (&Error{Op: OpUnknown, Kind: ErrUnavailable,
				Message: "the context ended before this source was read"}).WithCause(err)
			continue
		}

		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Written to out[i] only. Each goroutine owns one element of a
			// slice whose header never changes, so no lock is needed and the
			// order is the caller's order by construction.
			res, err := Extract[T](ctx, c, src, opts...)
			out[i].Result, out[i].Err = res, err
		}(i, src)
	}
	wg.Wait()

	return out
}
