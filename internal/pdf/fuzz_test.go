package pdf

import (
	"errors"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// fuzzLimits are deliberately much smaller than the defaults.
//
// The point of fuzzing a parser is to find the input that does not terminate,
// and a ceiling of 512 MiB does not distinguish "bounded" from "unbounded" in
// the seconds a fuzz iteration gets. Small ceilings turn an unbounded
// allocation into a fast failure, and turn a slow loop into a timeout that
// still points at the right input.
func fuzzLimits() detect.Limits {
	return detect.Limits{
		MaxSourceBytes:       1 << 20,
		MaxDecompressedBytes: 1 << 20,
		MaxStreamBytes:       1 << 18,
		MaxTextBytes:         1 << 16,
		MaxPages:             8,
		MaxDepth:             16,
		MaxObjects:           2000,
	}
}

// checkPage asserts the invariants every extracted page must satisfy, whatever
// the input was.
func checkPage(t *testing.T, p Page, lim detect.Limits) {
	t.Helper()
	if len(p.Content.Words) > maxWordsPerPage {
		t.Fatalf("page produced %d words, above the ceiling of %d", len(p.Content.Words), maxWordsPerPage)
	}
	total := 0
	for _, w := range p.Content.Words {
		if w.Text == "" {
			t.Fatal("an empty word was emitted; a word with no text has no meaning and no box")
		}
		if w.Confidence != 1 {
			t.Fatalf("Confidence = %v, want 1 for a text layer", w.Confidence)
		}
		total += len(w.Text)
	}
	if int64(total) > lim.MaxTextBytes {
		t.Fatalf("page produced %d bytes of text, above the ceiling of %d", total, lim.MaxTextBytes)
	}
	if s := p.Stats; s.Undecodable > s.Chars || s.Replacement > s.Chars {
		t.Fatalf("stats are inconsistent: %+v", s)
	}
}

// FuzzPDF drives the whole parser from arbitrary bytes.
//
// This is the package that reads attacker-controlled input, so the fuzzer is
// here from the first commit rather than added after the first bug
// (docs/threat-model.md T3). The properties it asserts are that Open and Page
// terminate, never panic, and never produce more than the ceilings allow. It
// asserts nothing about *what* is extracted: a mutated PDF has no right
// answer, and a fuzzer that checks content only tests the mutation engine.
func FuzzPDF(f *testing.F) {
	f.Add(onePage("BT /F1 12 Tf 72 720 Td (Hello World) Tj ET", helvetica, ""))
	f.Add(onePage("BT /F1 12 Tf 72 720 Td [(one) -600 (two)] TJ ET", helvetica, "/Rotate 90 "))
	f.Add(buildXrefStreamPDF(&testing.T{}, true))
	f.Add(buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>",
		streamObj("/Filter /ASCIIHexDecode", "42542045540a>"),
	}, ""))
	f.Add([]byte("%PDF-1.7\ntrailer<</Root 1 0 R>>\nstartxref\n0\n%%EOF"))
	f.Add([]byte("%PDF-"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		lim := fuzzLimits()
		doc, err := Open(data, lim, nil)
		if err != nil {
			// Every failure must be one of the four this package declares,
			// so the pipeline above can classify it without reading a
			// message (docs/rules.md §2.2).
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrEncrypted) &&
				!errors.Is(err, ErrUnsupportedFilter) && !errors.Is(err, detect.ErrLimitExceeded) {
				t.Fatalf("Open returned an unclassified error: %v", err)
			}
			return
		}
		if doc.NumPages() < 1 {
			t.Fatal("Open succeeded with no pages; a document with no pages is malformed")
		}
		if doc.NumPages() > lim.MaxPages {
			t.Fatalf("NumPages = %d, above the ceiling of %d", doc.NumPages(), lim.MaxPages)
		}
		_ = doc.Metadata()
		for n := 1; n <= doc.NumPages(); n++ {
			p, err := doc.Page(n)
			if err != nil {
				continue
			}
			checkPage(t, p, lim)
			_ = p.Usable(Thresholds{})
		}
	})
}

// FuzzContent drives the content-stream interpreter directly.
//
// It exists because a mutating fuzzer almost never produces a byte string that
// is both a valid PDF and an interesting content stream: the cross-reference
// table makes every useful mutation invalid. Wrapping the fuzzer's bytes in a
// valid document puts the mutations where the operators are, which is where
// the arithmetic and the font handling live.
func FuzzContent(f *testing.F) {
	f.Add("BT /F1 12 Tf 72 720 Td (Hello World) Tj ET")
	f.Add("BT /F1 12 Tf 1 0 0 1 72 720 Tm [(a) -600 (b)] TJ T* (c) Tj ET")
	f.Add("q 2 0 0 2 0 0 cm BT /F1 6 Tf 0 0 Td (scaled) Tj ET Q")
	f.Add("BT /F1 12 Tf 200 Tz 5 Tc 3 Tw 4 Ts 72 720 Td (spaced) Tj ET")
	f.Add("BI /W 1 /H 1 ID \x00\x01\x02 EI BT /F1 12 Tf 0 0 Td (after) Tj ET")
	f.Add("BT ET Q Q Q q q q")

	f.Fuzz(func(t *testing.T, content string) {
		lim := fuzzLimits()
		doc, err := Open(onePage(content, helvetica, ""), lim, nil)
		if err != nil {
			return
		}
		p, err := doc.Page(1)
		if err != nil {
			return
		}
		checkPage(t, p, lim)
	})
}

// FuzzCMap drives the CMap parser, which decides what characters a document's
// text is made of and is therefore the highest-consequence parser here after
// the object graph.
func FuzzCMap(f *testing.F) {
	f.Add("begincmap 1 begincodespacerange <00> <ff> endcodespacerange " +
		"2 beginbfchar <01> <0041> <02> <00E9> endbfchar endcmap")
	f.Add("1 begincodespacerange <0000> <ffff> endcodespacerange " +
		"1 beginbfrange <0001> <0003> <0058> endbfrange")
	f.Add("1 begincidrange <00> <ff> 0 endcidrange")
	f.Add("beginbfchar")

	f.Fuzz(func(t *testing.T, data string) {
		c := parseCMap([]byte(data), detect.NewDepth(16))
		if len(c.single) > maxCMapEntries || len(c.cids) > maxCMapEntries {
			t.Fatalf("cmap holds %d single and %d cid entries, above the ceiling",
				len(c.single), len(c.cids))
		}
		if len(c.spaces) == 0 {
			t.Fatal("a parsed cmap must always have a codespace; without one nothing knows how many bytes a code takes")
		}
		// next must always advance, or the decoding loop that drives it does
		// not terminate.
		for _, probe := range [][]byte{{0}, {0xFF}, {0x00, 0x01}, {0xFF, 0xFF, 0xFF, 0xFF}} {
			if _, n := c.next(probe); n <= 0 || n > len(probe) {
				t.Fatalf("next(% x) consumed %d bytes", probe, n)
			}
		}
	})
}
