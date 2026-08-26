package ovrin_test

import (
	"context"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// Every Op in the vocabulary that a normal extraction passes through must
// actually reach the hook.
//
// OpValidate and OpGround were declared, mapped to span names by the otel
// module, documented as stages in pipeline.md — and never emitted. An Op that
// can appear on an Error and never on an Event makes a trace and a failure
// describe the same extraction in two different languages.
func TestEveryStageOfAPlainExtractionEmitsAnEvent(t *testing.T) {
	t.Parallel()

	type Doc struct {
		Total float64 `ovrin:"total amount,required,min=0"`
	}

	seen := map[ovrin.Op]bool{}
	c := ovrin.New(
		ovrin.WithModel(replyModel{reply: map[string]any{"total": 10.0}}),
		ovrin.WithHook(func(_ context.Context, ev ovrin.Event) {
			seen[ev.Op] = true
		}),
	)

	if _, err := ovrin.Extract[Doc](context.Background(), c,
		ovrin.Bytes([]byte("item,total\nconsulting,10.00\n"))); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// The stages a text-layer document with no OCR and no renderer goes
	// through. OpRender and OpOCR are absent by design here — nothing was
	// rasterised — which is why they are not in this list.
	for _, op := range []ovrin.Op{
		ovrin.OpDetect,
		ovrin.OpAcquire,
		ovrin.OpNormalise,
		ovrin.OpSchema,
		ovrin.OpPrompt,
		ovrin.OpGenerate,
		ovrin.OpValidate,
		ovrin.OpGround,
		ovrin.OpScore,
	} {
		if !seen[op] {
			t.Errorf("no event was emitted for stage %q", op)
		}
	}
}
