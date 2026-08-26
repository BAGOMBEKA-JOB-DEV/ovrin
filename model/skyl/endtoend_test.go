package skyl_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/sandbox"
	ovrinskyl "github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl"
)

// Receipt mirrors examples/receipt so this test exercises the same schema the
// real-model example does.
type Receipt struct {
	Number   string  `ovrin:"receipt number as printed,required"`
	Vendor   string  `ovrin:"the trading name of the shop,required"`
	Currency string  `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
	Total    float64 `ovrin:"total amount including tax,required,min=0"`
}

// Everything else in this repository tests the pipeline against an in-process
// fake Model. This runs the whole thing over a real socket: image bytes →
// detect → decode → prompt → the skyl adapter → HTTP → a server speaking
// OpenAI's chat-completions format → the reply → validate → score → a typed
// struct.
//
// What it proves: the request ovrin builds serialises, crosses a connection,
// and comes back as a Go value with confidence and provenance attached. What
// it does NOT prove: that a real provider's strict mode accepts the JSON
// Schema ovrin emits. That is the one thing only a real provider can answer,
// and it is what examples/receipt exists for.
func TestPipelineOverARealSocket(t *testing.T) {
	t.Parallel()

	reply, err := json.Marshal(map[string]any{
		"number":   "INV-2026-0417",
		"vendor":   "Kampala Supplies Ltd",
		"currency": "UGX",
		"total":    1463200.0,
	})
	if err != nil {
		t.Fatalf("building the reply: %v", err)
	}

	s := sandbox.New(
		sandbox.WithAPIKey("test-key"),
		sandbox.WithModel("sandbox-model"),
		sandbox.WithReply(string(reply)),
	)
	t.Cleanup(s.Close)

	c := ovrin.New(ovrin.WithModel(
		ovrinskyl.Compatible(s.BaseURL(), "test-key", "requested-model"),
	))

	res, err := ovrin.Extract[Receipt](context.Background(), c,
		ovrin.File("../../examples/receipt/receipt.png"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if got, want := res.Data.Number, "INV-2026-0417"; got != want {
		t.Errorf("Number = %q, want %q", got, want)
	}
	if got, want := res.Data.Total, 1463200.0; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	if !res.Valid {
		t.Errorf("Valid = false; field errors: %v", fieldErrors(res.Fields))
	}
	if s.Requests() != 1 {
		t.Errorf("the provider was called %d times, want 1", s.Requests())
	}

	// The request really did carry the image and the schema.
	body := string(s.LastBody())
	for _, want := range []string{"image/png", "json_schema", "additionalProperties"} {
		if !strings.Contains(body, want) {
			t.Errorf("the request body does not mention %q", want)
		}
	}

	// A vision reading has no source text, so grounding is absent rather than
	// zero — and absent must not be scored as evidence against the value.
	total := res.Fields["total"]
	for _, sig := range total.Signals {
		if sig.Name == ovrin.SignalGrounding {
			t.Error("a grounding signal was recorded although there was no source text to ground against")
		}
	}
	if total.Confidence <= 0 {
		t.Errorf("confidence = %v; an ungroundable reading should still score on its other signals", total.Confidence)
	}
}

func fieldErrors(fields map[string]ovrin.FieldResult) []error {
	var out []error
	for _, f := range fields {
		out = append(out, f.Errors...)
	}
	return out
}
