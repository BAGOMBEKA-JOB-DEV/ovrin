package pdf

import (
	"os"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// This is the stage the roadmap calls the largest single piece of work in v0.1,
// and it is the one most documents pay for: a PDF with a text layer never
// reaches a renderer, an OCR provider or anything else that costs money, so
// whatever this package spends is the whole of the acquisition bill (ADR-0012).
//
// The two halves are measured apart because they fail differently. Open parses
// the cross-reference table, follows the trailer and builds the page tree — its
// cost is a function of the document's structure, and a regression there is
// paid once per document. Page decodes a content stream, resolves fonts and
// their ToUnicode tables and accumulates a box for every word — its cost is a
// function of how much text is on the page, and a regression there is paid once
// per page. A single number over both would locate neither.
//
// Positions are not optional and are therefore not a variant here: provenance
// is built from them (ADR-0015), so there is no faster mode to compare against
// and the number below is the real one.

// Kept reachable so the compiler cannot discard the parse that produced them.
var (
	benchOpened *Doc
	benchPage   Page
	benchStats  Stats
)

// benchCorpus is a committed corpus invoice: one A4 page of digital text with
// a real text layer, several fonts and a table. It was produced by
// eval/corpusgen rather than by this package, so the parser is not reading its
// own writing (rules §3.5).
func benchCorpus(b *testing.B, name string) []byte {
	b.Helper()
	path := "../../eval/corpus/invoices/" + name
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("corpus document missing: %s: %v", path, err)
	}
	return data
}

// benchLimits mirrors what the pipeline hands this package: the default
// ceilings and a fresh decompression counter per document, since the budget
// belongs to the document and a counter shared across iterations would run out
// partway through the benchmark and change what is being measured.
func benchLimits() (detect.Limits, func() *detect.Counter) {
	lim := detect.DefaultLimits()
	return lim, func() *detect.Counter {
		return detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes)
	}
}

// BenchmarkOpen measures parsing a document's structure: header, cross
// reference, trailer, page tree. Nothing is decoded here that a page does not
// ask for, so this is the floor on what reading any PDF costs.
func BenchmarkOpen(b *testing.B) {
	lim, counter := benchLimits()

	for _, name := range []string{"001.pdf", "002.pdf"} {
		name := name
		b.Run(name, func(b *testing.B) {
			data := benchCorpus(b, name)
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, err := Open(data, lim, counter())
				if err != nil {
					b.Fatalf("Open: %v", err)
				}
				benchOpened = d
			}
		})
	}
}

// BenchmarkPageText measures extracting one page's text with a box for every
// word, over a document that is already open. This is the number that scales
// with page count, and the one that a change to the content-stream interpreter,
// the font machinery or the word accumulator moves.
func BenchmarkPageText(b *testing.B) {
	lim, counter := benchLimits()

	for _, name := range []string{"001.pdf", "002.pdf"} {
		name := name
		b.Run(name, func(b *testing.B) {
			data := benchCorpus(b, name)
			// The document is opened once, outside the loop, because a page is
			// what is being timed. Opening again per iteration would fold the
			// structural cost back in and hide it.
			d, err := Open(data, lim, counter())
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			if d.NumPages() == 0 {
				b.Fatalf("%s has no pages", name)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p, err := d.Page(1)
				if err != nil {
					b.Fatalf("Page: %v", err)
				}
				benchPage = p
			}
		})
	}
}

// BenchmarkUsability measures the three-threshold heuristic that decides
// whether a page's text layer is trustworthy or whether the pipeline should
// fall through to OCR. It runs on every page of every PDF, and a page that
// fails it costs an OCR call — so it is worth knowing that the decision itself
// is free next to the extraction it guards.
func BenchmarkUsability(b *testing.B) {
	lim, counter := benchLimits()
	data := benchCorpus(b, "001.pdf")

	d, err := Open(data, lim, counter())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	p, err := d.Page(1)
	if err != nil {
		b.Fatalf("Page: %v", err)
	}
	th := DefaultThresholds()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Usable(th) {
			b.Fatal("the corpus page is not usable, so the benchmark is measuring the refusal path")
		}
		benchStats = p.Stats
	}
}

// BenchmarkAcquire measures what the pipeline actually does for a text-layer
// PDF: open the document, then walk every page. It is the figure to quote for
// "what does reading a PDF cost", and the sum of the two benchmarks above is
// the check that nothing else is hiding between them.
func BenchmarkAcquire(b *testing.B) {
	lim, counter := benchLimits()
	th := DefaultThresholds()

	for _, name := range []string{"001.pdf", "002.pdf"} {
		name := name
		b.Run(name, func(b *testing.B) {
			data := benchCorpus(b, name)
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, err := Open(data, lim, counter())
				if err != nil {
					b.Fatalf("Open: %v", err)
				}
				for n := 1; n <= d.NumPages(); n++ {
					p, err := d.Page(n)
					if err != nil {
						b.Fatalf("Page %d: %v", n, err)
					}
					if !p.Usable(th) {
						b.Fatalf("page %d of %s is not usable", n, name)
					}
					benchPage = p
				}
				benchOpened = d
			}
		})
	}
}
