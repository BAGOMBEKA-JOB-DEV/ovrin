package ovrin_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The per-stage benchmarks live beside the packages they measure. This one
// measures the pipeline, which is not the same thing as their sum: assembly,
// scoring, provenance and the conversion at every seam belong to nobody's
// package and would otherwise never be counted.
//
// The model is in-process and returns a fixed reply, so what is timed is
// everything ovrin does and nothing a provider does. That is the honest way to
// benchmark this library. A real extraction is dominated by a network call to a
// model — hundreds of milliseconds against the microseconds below — and the
// number worth defending is not "how fast is an extraction" but "how much of an
// extraction is ovrin". If that ever stops being a rounding error next to the
// provider, it is a regression whoever caused it should see here.
//
// The document is a corpus invoice with a real text layer, so the acquisition
// path is the one most PDFs take: internal/pdf reads it, no renderer runs, no
// OCR provider is called (ADR-0012).

// Kept reachable so the compiler cannot delete the extraction.
var benchSink any

// benchParty is the nested struct on the invoice below.
type benchParty struct {
	Name    string `ovrin:"the party's legal name"`
	Address string `ovrin:"the party's postal address"`
	TaxID   string `ovrin:"the party's tax identifier"`
}

// benchItem is one line of the invoice, which is what makes the schema, the
// validation and the grounding recurse over a slice.
type benchItem struct {
	Description string  `ovrin:"what was supplied"`
	Quantity    float64 `ovrin:"how many"`
	UnitPrice   float64 `ovrin:"price per unit"`
	Amount      float64 `ovrin:"the line total"`
}

// benchInvoice is the shape of eval/corpus/invoices/001.pdf: the schema a
// realistic caller declares, with formats, rules, a nested struct and a slice.
type benchInvoice struct {
	Number   string      `ovrin:"invoice number,required"`
	Issued   time.Time   `ovrin:"date of issue,required,format=date"`
	Vendor   benchParty  `ovrin:"the supplier"`
	BillTo   benchParty  `ovrin:"the customer"`
	Currency string      `ovrin:"the currency of every amount,required,format=currency"`
	Subtotal float64     `ovrin:"net total before tax,min=0"`
	Tax      float64     `ovrin:"tax charged,min=0"`
	Total    float64     `ovrin:"gross total including tax,required,min=0"`
	Items    []benchItem `ovrin:"the line items"`
}

// benchReceipt is the other end of the range: three fields and no nesting,
// measured alongside the invoice so the per-field cost of the stages after the
// model — validation, grounding, scoring — can be read off the difference.
type benchReceipt struct {
	Merchant string    `ovrin:"merchant name,required"`
	Date     time.Time `ovrin:"date of purchase,format=date"`
	Total    float64   `ovrin:"amount paid,required,min=0"`
}

// benchInvoiceReply is what the corpus document actually says, so every field
// grounds and the benchmark measures the path a good extraction takes. A reply
// of invented values would measure the not-found path in grounding for every
// field at once, which is a different and slower thing.
var benchInvoiceReply = map[string]any{
	"number":   "INV-2026-0417",
	"issued":   "2026-03-14",
	"currency": "UGX",
	"vendor": map[string]any{
		"name":    "Nakawa Stationers Limited",
		"address": "Plot 14 Jinja Road, Kampala",
		"tax_id":  "1002938475",
	},
	"bill_to": map[string]any{
		"name":    "Makindye Secondary School",
		"address": "PO Box 7712, Kampala",
		"tax_id":  "1009988771",
	},
	"subtotal": 1240000.0,
	"tax":      223200.0,
	"total":    1463200.0,
	"items": []any{
		map[string]any{
			"description": "A4 paper 80gsm",
			"quantity":    40.0,
			"unit_price":  12500.0,
			"amount":      500000.0,
		},
		map[string]any{
			"description": "Toner cartridge HP 26A",
			"quantity":    4.0,
			"unit_price":  185000.0,
			"amount":      740000.0,
		},
	},
}

var benchReceiptReply = map[string]any{
	"merchant": "Nakawa Stationers Limited",
	"date":     "2026-03-14",
	"total":    1463200.0,
}

// benchCorpusPDF reads the corpus invoice, or skips naming what was missing.
func benchCorpusPDF(b *testing.B) []byte {
	b.Helper()
	const path = "eval/corpus/invoices/001.pdf"
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("corpus document missing: %s: %v", path, err)
	}
	return data
}

// BenchmarkExtract measures one whole extraction of a real document with a
// fixed in-process model.
//
// The two sub-benchmarks are the two ways callers use the library. A long-lived
// client reflects over its schema once and every later extraction takes the
// cached one — that is the shape a service has, and it is what the "warm"
// figure describes. A client built per call reflects every time, which is what
// a CLI does and what a handler that constructs its dependencies per request
// does by accident; the difference between the two is the price of that
// mistake, measured rather than asserted.
func BenchmarkExtract(b *testing.B) {
	data := benchCorpusPDF(b)
	ctx := context.Background()
	model := replyModel{reply: benchInvoiceReply}

	b.Run("warm_client", func(b *testing.B) {
		c := ovrin.New(ovrin.WithModel(model))
		// One extraction before the timer, so the schema cache is warm and a
		// reflection that belongs to the first call is not charged to all of
		// them.
		benchWarmInvoice(b, c)
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res, err := ovrin.Extract[benchInvoice](ctx, c, ovrin.Bytes(data))
			if err != nil {
				b.Fatalf("Extract: %v", err)
			}
			benchSink = res
		}
	})

	b.Run("new_client_each_call", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c := ovrin.New(ovrin.WithModel(model))
			res, err := ovrin.Extract[benchInvoice](ctx, c, ovrin.Bytes(data))
			if err != nil {
				b.Fatalf("Extract: %v", err)
			}
			benchSink = res
		}
	})
}

// BenchmarkExtractSchemaSize measures the same document against two schemas of
// very different size. Acquisition and normalisation are identical in both — it
// is the same PDF — so the difference is everything that runs per field:
// validation, grounding against the whole text, scoring and provenance.
//
// Grounding is the part that grows, and it grows in the number of fields times
// the length of the document. That product, not the page count, is what makes a
// large schema over a long contract expensive.
func BenchmarkExtractSchemaSize(b *testing.B) {
	data := benchCorpusPDF(b)
	ctx := context.Background()

	b.Run("fields_3", func(b *testing.B) {
		c := ovrin.New(ovrin.WithModel(replyModel{reply: benchReceiptReply}))
		if _, err := ovrin.Extract[benchReceipt](ctx, c, ovrin.Bytes(data)); err != nil {
			b.Fatalf("warming: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res, err := ovrin.Extract[benchReceipt](ctx, c, ovrin.Bytes(data))
			if err != nil {
				b.Fatalf("Extract: %v", err)
			}
			benchSink = res
		}
	})

	b.Run("fields_20_nested_and_slice", func(b *testing.B) {
		c := ovrin.New(ovrin.WithModel(replyModel{reply: benchInvoiceReply}))
		benchWarmInvoice(b, c)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res, err := ovrin.Extract[benchInvoice](ctx, c, ovrin.Bytes(data))
			if err != nil {
				b.Fatalf("Extract: %v", err)
			}
			benchSink = res
		}
	})
}

// BenchmarkExtractParallel measures whether one client serves several documents
// at once without getting in its own way. A client is documented as safe for
// concurrent use, and the schema cache holds a mutex across reflection — this
// is where that lock would show up if it mattered, which on a warm cache it
// should not.
func BenchmarkExtractParallel(b *testing.B) {
	data := benchCorpusPDF(b)
	ctx := context.Background()
	c := ovrin.New(ovrin.WithModel(replyModel{reply: benchInvoiceReply}))
	benchWarmInvoice(b, c)

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	// The result is checked rather than stored: writing the package sink from
	// several goroutines would be a data race.
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			res, err := ovrin.Extract[benchInvoice](ctx, c, ovrin.Bytes(data))
			if err != nil {
				b.Fatalf("Extract: %v", err)
			}
			if res.Data.Number == "" {
				b.Fatal("the extraction produced no invoice number")
			}
		}
	})
}

// benchWarmInvoice runs one extraction before the timer starts. It warms the
// schema cache, and it checks that the fixture and the schema still agree:
// a reply whose values had stopped appearing in the document would quietly turn
// every one of these benchmarks into a measurement of grounding's not-found
// path, under a name that says otherwise.
func benchWarmInvoice(b *testing.B, c *ovrin.Client) {
	b.Helper()
	res, err := ovrin.Extract[benchInvoice](context.Background(), c, ovrin.Bytes(benchCorpusPDF(b)))
	if err != nil {
		b.Fatalf("warming: %v", err)
	}
	if f := res.Fields["total"]; !f.Found || f.Provenance == nil {
		b.Fatalf("the total is not grounded in the corpus document (found=%v); the benchmark would measure the not-found path", f.Found)
	}
}
