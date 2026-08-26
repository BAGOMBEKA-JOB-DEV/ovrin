package ground

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// Grounding runs once per extracted field, so a document with twenty fields
// pays for it twenty times over the same text. That makes its cost a function
// of the document rather than of the value, and the three outcomes cost very
// different amounts:
//
//   - a verbatim match stops at the first hit, so its cost depends on where in
//     the text the value happens to appear and nothing else;
//   - a normalised match pays for the verbatim pass first — the two passes are
//     ordered, and the order is the outcome — and then for a type-aware scan;
//   - no match pays for both passes over the whole text and cannot stop early.
//
// The last is the one that matters. It is the worst case by construction, and
// it is the case fabrication detection is made of: a model that invented a
// number produces exactly this search. If grounding ever becomes too expensive
// to run, the check that catches invention is the one that gets turned off.
//
// The sub-benchmarks below therefore ground the same document three ways, and
// the not-found figure is the one to quote as the cost of the stage.

// Kept reachable so the compiler cannot delete the search.
var benchGround Result

// benchDocument builds a normalised invoice of roughly the length of a real
// one: a header, a body of line items and a totals block, over the given number
// of pages. Values are grounded against the whole document, so length is the
// variable that matters and it is varied rather than fixed.
func benchDocument(pages int) *normalise.Result {
	var in normalise.Input
	for n := 1; n <= pages; n++ {
		lines := []string{
			"Nakawa Stationers Limited",
			"Plot 14 Jinja Road, Kampala",
			"Invoice INV-2026-0417",
			"Issued 14 March 2026",
			"Due 13 April 2026",
			"Bill to Makindye Secondary School",
			"PO Box 7712, Kampala",
		}
		for i := 0; i < 40; i++ {
			lines = append(lines, fmt.Sprintf(
				"A4 paper 80gsm %02d   40   12,500   %d", i, 500000+i*13))
		}
		lines = append(lines,
			"Subtotal 1,240,000",
			"Tax at 18% 223,200",
			"Total UGX 1 463 200",
			"accounts@nakawastationers.example",
		)

		p := normalise.Page{Number: n, Width: 595, Height: 842}
		for l, line := range lines {
			y := 60 + float64(l)*14
			p.Words = append(p.Words, normalise.Word{
				Text: line,
				Line: l,
				Box:  normalise.Rect{MinX: 40, MinY: y, MaxX: 555, MaxY: y + 11},
			})
		}
		in.Pages = append(in.Pages, p)
	}
	return normalise.Normalise(in)
}

// BenchmarkGround measures the three outcomes over one document, one
// sub-benchmark each, because averaging a hit that stops early with a miss that
// cannot would report a number that describes no real extraction.
func BenchmarkGround(b *testing.B) {
	doc := benchDocument(1)

	cases := []struct {
		name  string
		value any
		kind  Kind
		want  float64
	}{
		// Verbatim: the value's own bytes are in the text, and the first pass
		// finds them.
		{"verbatim_string", "Nakawa Stationers Limited", KindString, Verbatim},
		{"verbatim_number", 500000, KindNumber, Verbatim},

		// Normalised: the document says "1 463 200" and the model said
		// 1463200, so the verbatim pass fails over the whole text before the
		// type-aware pass succeeds. This is the cost of both passes plus a hit.
		{"normalised_number", 1463200, KindNumber, Normalised},
		{"normalised_string", "NAKAWA  stationers   limited", KindString, Normalised},

		// Not found: both passes run to the end. A fabricated total is the
		// motivating example and is what is measured here.
		{"not_found_number", 987654321, KindNumber, NotFound},
		{"not_found_string", "Kololo Office Supplies Limited", KindString, NotFound},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			// The outcome is checked once, outside the loop: a benchmark whose
			// case has silently stopped measuring what its name says is worse
			// than no benchmark.
			if got := Ground(doc, c.value, c.kind).Grounding; got != c.want {
				b.Fatalf("grounding %q gives %v, want %v — the case no longer measures what it names", c.name, got, c.want)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchGround = Ground(doc, c.value, c.kind)
			}
		})
	}
}

// BenchmarkGroundNotFoundByLength measures the worst case against document
// length, since that is the relationship a caller needs in order to predict
// what grounding a fifty-page contract will cost.
func BenchmarkGroundNotFoundByLength(b *testing.B) {
	for _, pages := range []int{1, 4, 16} {
		pages := pages
		b.Run(fmt.Sprintf("pages_%d", pages), func(b *testing.B) {
			doc := benchDocument(pages)
			b.ReportAllocs()
			b.SetBytes(int64(len(doc.Text)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchGround = Ground(doc, 987654321, KindNumber)
			}
		})
	}
}

// BenchmarkGroundKinds measures the type-aware comparisons against each other.
// Numbers and dates parse candidates out of the text before comparing, and a
// date has by far the most forms to try, so the kinds are not interchangeable
// and a schema full of dates does not cost what a schema full of strings does.
func BenchmarkGroundKinds(b *testing.B) {
	doc := benchDocument(1)
	issued := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		value any
		kind  Kind
	}{
		{"string", "Kololo Office Supplies Limited", KindString},
		{"number", 987654321, KindNumber},
		{"date", issued.AddDate(3, 0, 0), KindDate},
		{"long_string", strings.Repeat("absent phrase ", 8), KindString},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchGround = Ground(doc, c.value, c.kind)
			}
		})
	}
}
