package eval

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	evalschema "github.com/BAGOMBEKA-JOB-DEV/ovrin/eval/schema"
)

// Runner extracts one corpus category and reduces the result to an
// [Observation].
//
// A function per category rather than one reflective call, because
// [ovrin.Extract] is generic and the type parameter has to be written down
// somewhere. Writing it down here keeps the harness free of reflection over
// the very mechanism it is meant to be measuring.
type Runner func(ctx context.Context, c *ovrin.Client, src ovrin.Source, opts ...ovrin.Option) (Observation, error)

// entry ties one corpus category to its schema, once.
//
// The runner and the type come from the same type parameter so that they
// cannot drift apart: a registry with two lists is a registry where one of
// them is eventually wrong, and the symptom would be a category scored against
// another category's schema.
type entry struct {
	run Runner
	typ reflect.Type
}

// registered builds a registry entry for one schema type.
func registered[T any]() entry {
	return entry{run: runnerFor[T](), typ: reflect.TypeOf((*T)(nil)).Elem()}
}

// registry is the one place corpus directory names and Go types meet.
var registry = map[string]entry{
	"invoices":   registered[evalschema.Invoice](),
	"receipts":   registered[evalschema.Receipt](),
	"forms":      registered[evalschema.Form](),
	"statements": registered[evalschema.Statement](),
	"identity":   registered[evalschema.Identity](),
}

// RunnerFor returns the extractor for a category.
//
// The error names the category rather than returning a nil Runner, because a
// corpus directory added without a schema is a mistake that should stop the
// run: scoring it against nothing would report a perfect fabrication rate over
// zero opportunities and look like good news.
func RunnerFor(category string) (Runner, error) {
	e, ok := registry[category]
	if !ok {
		return nil, fmt.Errorf("eval: no schema is registered for category %q", category)
	}
	return e.run, nil
}

// SchemaType returns the Go type a category is extracted against.
//
// Exported so that the corpus loader can check ground truth against the schema
// it will be scored with. A key in expected.json that no schema field can
// produce is a labelling mistake that would otherwise read as a permanent
// hundred-percent miss on a field that does not exist.
func SchemaType(category string) (reflect.Type, error) {
	e, ok := registry[category]
	if !ok {
		return nil, fmt.Errorf("eval: no schema is registered for category %q", category)
	}
	return e.typ, nil
}

// runnerFor builds a [Runner] for one schema type.
func runnerFor[T any]() Runner {
	return func(ctx context.Context, c *ovrin.Client, src ovrin.Source, opts ...ovrin.Option) (Observation, error) {
		start := time.Now()
		res, err := ovrin.Extract[T](ctx, c, src, opts...)
		elapsed := time.Since(start)
		if err != nil {
			// A failed extraction is still a scored document. An extractor
			// that errors on every poor scan and succeeds on every clean one
			// has not scored 100%, and dropping the failures would report
			// exactly that.
			return Observation{Duration: elapsed, Failed: true}, err
		}
		return Observe(res, elapsed), nil
	}
}

// Observe reduces an [ovrin.Result] to the three things scoring needs.
//
// Separate from the extraction so that the reduction — which is where a bug
// would quietly change every number in the report — can be tested against a
// hand-built Result with no provider anywhere near it.
func Observe[T any](res *ovrin.Result[T], elapsed time.Duration) Observation {
	o := Observation{
		Fields:     make(map[string]Observed, len(res.Fields)),
		Confidence: res.Confidence,
		Valid:      res.Valid,
		Usage:      res.Metadata.Usage,
		Duration:   elapsed,
	}
	if o.Duration == 0 {
		o.Duration = res.Metadata.Duration
	}

	flagged := make(map[string]bool, len(res.Reasons))
	for _, r := range res.Reasons {
		flagged[r.Field] = true
	}
	for k, f := range res.Fields {
		o.Fields[k] = Observed{
			Value:      f.Value,
			Found:      f.Found,
			Confidence: f.Confidence,
			Flagged:    flagged[k],
		}
	}
	return o
}
