package prompt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// Building a request is the last thing that happens before the network call
// that dominates every extraction, so nothing here is going to decide whether
// ovrin is fast. It is measured anyway, for two reasons.
//
// The first is the delimiter. Every request draws a fresh identifier from
// crypto/rand and then verifies it appears in none of the pages, and that
// verification is a scan of the whole document. It is a security property paid
// for per request, and the honest way to keep it is to know what it costs — a
// check that becomes expensive enough to be worth skipping is a check that will
// eventually be skipped.
//
// The second is that the instruction is built from the schema alone and is
// byte-identical for a given schema, which is what prompt caching at the
// provider depends on. Measuring it separately from Build says how much of a
// request is the cacheable half.

// Kept reachable so the compiler cannot delete the build.
var (
	benchRequest     Request
	benchInstruction string
)

// benchSchema is an invoice schema of the size a real caller declares: enough
// fields, formats and nesting that the instruction is a page of text rather
// than three lines.
func benchSchema() schema.Schema {
	party := func(prefix, what string) []schema.Field {
		return []schema.Field{
			{Key: prefix + ".name", GoName: "Name", Kind: schema.KindString,
				Description: "the " + what + "'s legal name",
				Rules:       []schema.Rule{{Name: schema.RuleRequired}}},
			{Key: prefix + ".address", GoName: "Address", Kind: schema.KindString,
				Description: "the " + what + "'s postal address"},
			{Key: prefix + ".tax_id", GoName: "TaxID", Kind: schema.KindString,
				Description: "the " + what + "'s tax identifier"},
		}
	}

	return schema.Schema{
		Name: "Invoice",
		Fields: []schema.Field{
			{Key: "number", GoName: "Number", Kind: schema.KindString,
				Description: "invoice number",
				Rules:       []schema.Rule{{Name: schema.RuleRequired}}},
			{Key: "issued", GoName: "Issued", Kind: schema.KindTime,
				Description: "date of issue",
				Rules: []schema.Rule{
					{Name: schema.RuleRequired},
					{Name: schema.RuleFormat, Value: schema.FormatDate},
				}},
			{Key: "vendor", GoName: "Vendor", Kind: schema.KindObject,
				Description: "the supplier", Fields: party("vendor", "supplier")},
			{Key: "bill_to", GoName: "BillTo", Kind: schema.KindObject,
				Description: "the customer", Fields: party("bill_to", "customer")},
			{Key: "currency", GoName: "Currency", Kind: schema.KindString,
				Description: "the currency of every amount",
				Rules:       []schema.Rule{{Name: schema.RuleFormat, Value: schema.FormatCurrency}}},
			{Key: "total", GoName: "Total", Kind: schema.KindFloat,
				Description: "the gross total including tax",
				Rules: []schema.Rule{
					{Name: schema.RuleRequired},
					{Name: schema.RuleMin, Value: "0"},
				}},
			{Key: "items[]", GoName: "Items", Kind: schema.KindArray,
				Description: "the line items",
				Elem: &schema.Field{
					Key: "items[]", GoName: "Items", Kind: schema.KindObject,
					Fields: []schema.Field{
						{Key: "items[].description", GoName: "Description",
							Kind: schema.KindString, Description: "what was supplied"},
						{Key: "items[].amount", GoName: "Amount",
							Kind: schema.KindFloat, Description: "the line total"},
					},
				}},
		},
	}
}

// benchJSONSchema stands in for what internal/jsonschema produces. Build passes
// it through unchanged, so its content does not matter and its size does.
var benchJSONSchema = []byte(strings.Repeat(
	`{"type":"object","properties":{"number":{"type":"string"}},"additionalProperties":false}`, 8))

// benchPageText is a page of invoice text of the length internal/normalise
// emits for an A4 page, which is what the boundary check has to scan.
func benchPageText(n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Nakawa Stationers Limited\nInvoice INV-2026-%04d\n", n)
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&b, "A4 paper 80gsm %02d   40   12,500   %d\n", i, 500000+i*13)
	}
	b.WriteString("Total UGX 1 463 200\n")
	return b.String()
}

// benchPages builds n pages of text content, the shape a text-layer or OCR
// reading hands to Build.
func benchPages(n int) []PageContent {
	pages := make([]PageContent, 0, n)
	for i := 1; i <= n; i++ {
		pages = append(pages, PageContent{Number: i, Reading: ReadingText, Text: benchPageText(i)})
	}
	return pages
}

// BenchmarkBuild measures a whole request against page count. The instruction
// is the same in every case, so the slope is the per-page cost: the boundary
// verification scan, the markers, and the content item itself.
func BenchmarkBuild(b *testing.B) {
	s := benchSchema()

	for _, pages := range []int{1, 8, 32} {
		pages := pages
		b.Run(fmt.Sprintf("pages_%d", pages), func(b *testing.B) {
			content := benchPages(pages)
			var bytes int64
			for _, p := range content {
				bytes += int64(len(p.Text))
			}
			b.ReportAllocs()
			b.SetBytes(bytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req, err := Build(s, benchJSONSchema, content)
				if err != nil {
					b.Fatalf("Build: %v", err)
				}
				benchRequest = req
			}
		})
	}
}

// BenchmarkBuildVision measures a request carrying page images instead of text.
// An image is passed through undelimited — there is no marker to forge in a
// PNG — so this is the same request with the boundary work removed, and the
// difference from the text case is what delimiting costs.
func BenchmarkBuildVision(b *testing.B) {
	s := benchSchema()
	// A page image at 150dpi is measured in megabytes; the bytes are never
	// read by this package, so a slice of the right size is the right fixture.
	image := make([]byte, 2<<20)
	content := []PageContent{{Number: 1, Reading: ReadingVision, Image: image, MediaType: "image/png"}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := Build(s, benchJSONSchema, content)
		if err != nil {
			b.Fatalf("Build: %v", err)
		}
		benchRequest = req
	}
}

// BenchmarkInstruction measures the half of a request that contains no document
// content and never changes for a given schema. It is what a provider's prompt
// cache would hold, and it is the part a caller pays for on every request
// whether or not they have any pages.
func BenchmarkInstruction(b *testing.B) {
	s := benchSchema()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchInstruction = Instruction(s)
	}
}
