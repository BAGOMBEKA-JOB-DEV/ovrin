package normalise

import (
	"fmt"
	"strings"
	"testing"
)

// Normalisation runs on every reading of every document, and unlike the stages
// on either side of it there is no cheaper mode to fall back to: the mapping
// from output byte to source word cannot be reconstructed afterwards, so it is
// built whether or not anybody asks for provenance (ADR-0015).
//
// The variants below are the shapes that cost different amounts:
//
//   - a plain text layer, where the reading grouped its own lines and no
//     geometry is present, so reading-order analysis is skipped entirely;
//   - a positioned single column, where the XY-cut runs and finds one block;
//   - two columns, where the cut actually recurses, which is the case the
//     algorithm exists for and the expensive one;
//   - text needing rewriting — ligatures, hyphenation, compatibility
//     characters — which is where the mapping stops being offset arithmetic
//     and every rewritten run becomes a segment of its own.
//
// The gap between the first two is the price of geometry; the gap between the
// second and third is the price of column detection; the gap between the first
// and the last is the price of Unicode work. Each is a number somebody will
// want when deciding whether a page is worth normalising.

// Kept reachable so the compiler cannot delete the normalisation.
var (
	benchResult *Result
	benchText   string
)

// benchLine is the text of one line of an invoice body, repeated with a
// varying figure so that no two lines are identical and nothing is measuring a
// cache that does not exist.
func benchLine(i int) string {
	return fmt.Sprintf("Item %03d  A4 paper 80gsm  40 x 12,500  UGX %d", i, 500000+i)
}

// benchLaidPage builds a page of positioned words in columns points wide,
// the way a text layer or an OCR reading presents one. Words carry no line
// hint (Line: -1) so that reading order is reconstructed from the boxes, which
// is the work being measured; a reading that grouped its own lines is the
// separate "text_layer" case below.
func benchLaidPage(number, columns, lines int) Page {
	const (
		top      = 72.0
		leading  = 14.0
		colWidth = 240.0
		gutter   = 35.0
		charW    = 5.6
	)
	p := Page{Number: number, Width: 595, Height: 842}
	for c := 0; c < columns; c++ {
		x0 := 40 + float64(c)*(colWidth+gutter)
		for l := 0; l < lines; l++ {
			y0 := top + float64(l)*leading
			x := x0
			for _, w := range strings.Fields(benchLine(c*lines + l)) {
				width := float64(len(w)) * charW
				p.Words = append(p.Words, Word{
					Text:       w,
					Line:       -1,
					Confidence: 0.98,
					Box:        Rect{MinX: x, MinY: y0, MaxX: x + width, MaxY: y0 + 11},
				})
				x += width + charW
			}
		}
	}
	return p
}

// benchTextPage is the same content with the reading's own line grouping and
// no geometry, which is what internal/office produces and what a plain text
// source looks like.
func benchTextPage(number, lines int) Page {
	p := Page{Number: number}
	for l := 0; l < lines; l++ {
		for _, w := range strings.Fields(benchLine(l)) {
			p.Words = append(p.Words, Word{Text: w, Line: l, Confidence: 1})
		}
	}
	return p
}

// benchRewritePage is a page that exercises the rewriting paths rather than
// the layout ones: ligatures from a PDF font, a soft-hyphenated line break,
// non-breaking and fullwidth characters, and a compatibility fraction. Every
// one of those produces a non-verbatim segment, so this is the case where the
// mapping does the most work per output byte.
func benchRewritePage(number, lines int) Page {
	fragments := []string{
		"ﬁnal", "oﬃce", "conﬂict", "ﬂuent", "de-", "livery",
		"non breaking", "２０２６", "½", "№", "ℓ", "ﬀ",
		"Ｉｎｖｏｉｃｅ", "—", "quantity", "25,000",
	}
	p := Page{Number: number}
	for l := 0; l < lines; l++ {
		for _, f := range fragments {
			p.Words = append(p.Words, Word{Text: f, Line: l, Confidence: 1})
		}
	}
	return p
}

// BenchmarkNormalise measures the whole stage over the four shapes that cost
// different amounts. Sixty lines is about a page of invoice body; two columns
// of thirty is the same quantity of text arranged so the cut has to work.
func BenchmarkNormalise(b *testing.B) {
	cases := []struct {
		name string
		in   Input
	}{
		{"text_layer_one_column", Input{Pages: []Page{benchTextPage(1, 60)}}},
		{"positioned_one_column", Input{Pages: []Page{benchLaidPage(1, 1, 60)}}},
		{"positioned_two_columns", Input{Pages: []Page{benchLaidPage(1, 2, 30)}}},
		{"positioned_three_columns", Input{Pages: []Page{benchLaidPage(1, 3, 20)}}},
		{"rewriting_heavy", Input{Pages: []Page{benchRewritePage(1, 40)}}},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			// Throughput is reported over the normalised text rather than over
			// the input, because the output is what every later stage reads
			// and what the mapping is sized against.
			b.SetBytes(int64(len(Normalise(c.in).Text)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchResult = Normalise(c.in)
			}
		})
	}
}

// BenchmarkNormaliseDocument measures a multi-page document, because the text,
// the segment list and the page span list all grow with the document and a
// per-page cost that is not linear would only show up here.
func BenchmarkNormaliseDocument(b *testing.B) {
	for _, pages := range []int{1, 8, 32} {
		pages := pages
		b.Run(fmt.Sprintf("pages_%d", pages), func(b *testing.B) {
			in := Input{
				Metadata: []Meta{{Key: "Title", Value: "Invoice INV-2026-0417"}},
			}
			for n := 1; n <= pages; n++ {
				in.Pages = append(in.Pages, benchLaidPage(n, 2, 30))
			}
			b.SetBytes(int64(len(Normalise(in).Text)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchResult = Normalise(in)
			}
		})
	}
}

// BenchmarkCanonical measures the Unicode pass on its own, without the layout
// work around it. It is the routine every word goes through and the one that
// carries the index array the mapping is built from, so its allocation
// behaviour is worth watching separately from the stage that calls it.
func BenchmarkCanonical(b *testing.B) {
	cases := []struct {
		name string
		text string
	}{
		// The common case: nothing to rewrite. It is still not free, because
		// the index array that makes the mapping possible is built either way
		// — which is the deliberate trade in ADR-0015, and the reason this
		// case is measured rather than assumed to be cheap.
		{"already_canonical", strings.Repeat("Nakawa Stationers Limited ", 40)},
		{"ligatures", strings.Repeat("ﬁnal oﬃce conﬂict ﬂuent ", 40)},
		{"fullwidth", strings.Repeat("Ｉｎｖｏｉｃｅ ２０２６ ", 40)},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(c.text)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, _ := Canonical(c.text)
				benchText = s
			}
		})
	}
}
