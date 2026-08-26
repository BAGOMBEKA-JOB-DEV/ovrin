// Runnable documentation for ovrin's public API.
//
// docs/rules.md §9.3 requires examples to live here as Example functions, so
// that CI breaks when they rot rather than a reader discovering it.
//
// The package is ovrin_test rather than ovrin: only an external test package
// renders in godoc with the ovrin. qualifier a reader would actually type.
//
// Examples that produce output are backed by the fakes below rather than by a
// real provider. §3.3 forbids network access in tests, and an example is
// documentation — one whose output varied per run, or that showed an import a
// reader cannot copy, would be worse than none. Examples that need a
// credential carry no Output comment and are therefore compiled but not run,
// which is the most Go can check for code that needs a key.
package ovrin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// Invoice is the schema used throughout these examples.
type Invoice struct {
	Number   string  `ovrin:"invoice number,required"`
	Vendor   string  `ovrin:"vendor company name"`
	Currency string  `ovrin:"currency code,required,enum=UGX|USD|EUR|GBP"`
	Total    float64 `ovrin:"total amount including tax,required,min=0"`
}

// fakeModel returns a fixed reply, so the examples are deterministic and run
// offline. A real programme passes ovrinskyl.OpenAI(key, model) — the interface
// is the same either way, which is the point of the seam.
type fakeModel struct{ reply map[string]any }

func (m fakeModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	b, _ := json.Marshal(m.reply)
	return &ovrin.ModelResponse{JSON: b, Usage: ovrin.Usage{InputTokens: 812, OutputTokens: 46}}, nil
}

// fakeOCR returns fixed words with positions, standing in for a real provider.
// It matters that it returns *text*: grounding checks an extracted value
// against the document it came from, and a vision reading has no source text
// to check against.
type fakeOCR struct{ words []string }

func (fakeOCR) Name() string { return "example" }

func (o fakeOCR) Recognise(context.Context, ovrin.Page) (*ovrin.Recognition, error) {
	rec := &ovrin.Recognition{Confidence: 0.97}
	x := 10.0
	for i, w := range o.words {
		rec.Words = append(rec.Words, ovrin.Word{
			Text:       w,
			Box:        ovrin.Rect{MinX: x, MinY: 100, MaxX: x + 40, MaxY: 112},
			Confidence: 0.97,
			Line:       0,
		})
		x += 45
		_ = i
	}
	return rec, nil
}

// pngBytes is a tiny valid PNG, so the examples have a real image source.
func pngBytes() []byte {
	m := image.NewRGBA(image.Rect(0, 0, 4, 4))
	m.Set(0, 0, color.RGBA{A: 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, m)
	return buf.Bytes()
}

// Extracting an invoice from an image.
func ExampleExtract() {
	c := ovrin.New(
		ovrin.WithModel(fakeModel{reply: map[string]any{
			"number": "INV-2026-0417", "vendor": "Kampala Supplies Ltd",
			"currency": "UGX", "total": 2500000.0,
		}}),
		ovrin.WithOCR(fakeOCR{words: []string{"INV-2026-0417", "Kampala", "Supplies", "Ltd", "UGX", "2,500,000"}}),
	)

	res, err := ovrin.Extract[Invoice](context.Background(), c, ovrin.Bytes(pngBytes()))
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(res.Data.Number)
	fmt.Println(res.Data.Currency, res.Data.Total)
	fmt.Println("valid:", res.Valid)
	// Output:
	// INV-2026-0417
	// UGX 2.5e+06
	// valid: true
}

// A value the model invented is caught, because it appears nowhere in the
// document it was supposedly read from. This is the failure ovrin exists to
// notice: the value is well formed, it satisfies every rule, and it is wrong.
func ExampleResult_Explain_fabrication() {
	c := ovrin.New(
		// The document says 2,500,000. The model reports 9,999,999.
		ovrin.WithModel(fakeModel{reply: map[string]any{
			"number": "INV-2026-0417", "vendor": "Kampala Supplies Ltd",
			"currency": "UGX", "total": 9999999.0,
		}}),
		ovrin.WithOCR(fakeOCR{words: []string{"INV-2026-0417", "Kampala", "Supplies", "Ltd", "UGX", "2,500,000"}}),
	)

	res, _ := ovrin.Extract[Invoice](context.Background(), c, ovrin.Bytes(pngBytes()))

	total := res.Fields["total"]
	fmt.Printf("value      %.0f\n", total.Value)
	fmt.Printf("confidence %.2f\n", total.Confidence)
	fmt.Println("review:", res.NeedsReview)
	for _, r := range res.Reasons {
		if r.Field == "total" {
			fmt.Println("reason:", r.Why)
		}
	}
	// The cap is a ceiling, not a floor: grounding contributes 0.0 at weight
	// 0.30 and the rules contribute 1.0 at weight 0.15, so the mean is already
	// 0.33 and CapUngrounded never has to bind.
	//
	// Output:
	// value      9999999
	// confidence 0.33
	// review: true
	// reason: value not found in the source; it may have been inferred or invented
	// reason: confidence is below the review threshold
}
