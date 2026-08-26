package office

import (
	"errors"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// fuzzLimits are deliberately much smaller than the defaults.
//
// The point of fuzzing a parser is to find the input that does not terminate,
// and a ceiling of 512 MiB does not distinguish "bounded" from "unbounded" in
// the fraction of a second a fuzz iteration gets. Small ceilings turn an
// unbounded allocation into a fast failure, and turn a slow loop into a
// timeout that still points at the right input.
func fuzzLimits() detect.Limits {
	return detect.Limits{
		MaxSourceBytes:       1 << 18,
		MaxDecompressedBytes: 1 << 18,
		MaxStreamBytes:       1 << 16,
		MaxTextBytes:         1 << 15,
		MaxPages:             8,
		MaxDepth:             16,
		MaxObjects:           1000,
	}
}

// checkDocument asserts the invariants every extracted document must satisfy,
// whatever the input was.
func checkDocument(t *testing.T, doc *Document, lim detect.Limits) {
	t.Helper()
	if doc == nil {
		return
	}
	if len(doc.Pages) > lim.MaxPages {
		t.Fatalf("produced %d pages, above the ceiling of %d", len(doc.Pages), lim.MaxPages)
	}
	total := 0
	for i, p := range doc.Pages {
		if p.Number != i+1 {
			t.Fatalf("page %d is numbered %d; numbering must be dense and 1-based", i, p.Number)
		}
		if len(p.Words) > maxWordsPerPage {
			t.Fatalf("page %d produced %d words, above the ceiling of %d", p.Number, len(p.Words), maxWordsPerPage)
		}
		// The positions decision is an invariant, not a default: a code path
		// that invents geometry must fail here rather than in review.
		if p.Width != 0 || p.Height != 0 || p.Background != nil {
			t.Fatalf("page %d reports geometry these formats do not have", p.Number)
		}
		last := 0
		for j, w := range p.Words {
			if !w.Box.Zero() {
				t.Fatalf("page %d word %d carries a box", p.Number, j)
			}
			if w.Colour != nil {
				t.Fatalf("page %d word %d carries a colour", p.Number, j)
			}
			if w.Confidence != 1 {
				t.Fatalf("page %d word %d has Confidence %v, want 1", p.Number, j, w.Confidence)
			}
			if w.Line < 1 {
				t.Fatalf("page %d word %d has Line %d; normalise trusts hints only when every word carries one", p.Number, j, w.Line)
			}
			if w.Line < last || w.Line > last+1 {
				t.Fatalf("page %d word %d jumps from line %d to line %d", p.Number, j, last, w.Line)
			}
			last = w.Line
			if !hasGraphic(w.Text) {
				t.Fatalf("page %d word %d is blank; a word with no text has no meaning", p.Number, j)
			}
			total += len(w.Text)
		}
	}
	// Emitted text is bounded by the text ceiling. XLSX charges the shared
	// string table to a second budget of the same size, so a workbook may
	// legitimately reach twice the figure before it is refused.
	if int64(total) > 2*lim.MaxTextBytes {
		t.Fatalf("produced %d bytes of text, above what the ceilings allow", total)
	}
	for _, p := range doc.Skipped {
		switch p {
		case PartHeader, PartFooter, PartFootnote, PartEndnote, PartComment:
		default:
			t.Fatalf("Skipped names %q, which is not in the closed vocabulary", p)
		}
	}
	if doc.HiddenRuns < 0 {
		t.Fatalf("HiddenRuns = %d", doc.HiddenRuns)
	}
}

// checkError asserts that whatever failed, failed in a way the pipeline can
// classify and that a log can hold.
func checkError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, ErrMalformed),
		errors.Is(err, ErrEncrypted),
		errors.Is(err, ErrUnsupported),
		errors.Is(err, detect.ErrLimitExceeded):
	default:
		t.Fatalf("err = %v, which answers none of this package's sentinels; the pipeline cannot classify it", err)
	}
	// A hostile document chooses its own bytes, and an error message is a log
	// line. Nothing an arbitrary input produced may be echoed, so the message
	// must stay within the fixed phrases this package builds errors from.
	if n := len(err.Error()); n > 200 {
		t.Fatalf("error message is %d bytes long; it is built from literals and cannot be", n)
	}
	if strings.ContainsAny(err.Error(), "\x00\n\r") {
		t.Fatalf("error message %q contains a control character", err.Error())
	}
}

// FuzzOffice drives all three readers from arbitrary bytes.
//
// It is here from the first commit rather than added after the first bug,
// because this is a package that reads attacker-controlled input
// (docs/threat-model.md T2, T3). The properties it asserts are that extraction
// terminates, never panics, and never produces more than the ceilings allow.
// It asserts nothing about *what* is extracted: a mutated archive has no right
// answer, and a fuzzer that checks content only tests the mutation engine.
//
// Every input is driven through all three readers rather than the one its
// bytes look like, so that a CSV reader handed a zip and a zip reader handed a
// CSV are both exercised — which is the case a pipeline hits whenever
// detection is wrong.
func FuzzOffice(f *testing.F) {
	t := &testing.T{}
	f.Add(docxOf(t, `<w:p><w:r><w:t>Hello World</w:t></w:r></w:p>`))
	f.Add(docxOf(t, `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc>`+
		`<w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`))
	f.Add(docxOf(t, `<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>hidden</w:t></w:r><w:br/><w:r><w:t>x</w:t></w:r></w:p>`))
	f.Add(docxOf(t, `<w:p><mc:AlternateContent><mc:Choice Requires="wps"><w:r><w:t>c</w:t></w:r></mc:Choice>`+
		`<mc:Fallback><w:r><w:t>f</w:t></w:r></mc:Fallback></mc:AlternateContent></w:p>`))
	f.Add(docxOf(t, `<w:p><w:r><w:t>body</w:t></w:r></w:p>`,
		entry{name: "word/header1.xml", body: []byte(`<?xml version="1.0"?><w:hdr ` + wordNS + `><w:p><w:r><w:t>h</w:t></w:r></w:p></w:hdr>`)}))
	f.Add(xlsxOf(t, []string{"Name", "Ada"}, `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>`))
	f.Add(xlsxOf(t, nil, `<row r="1"><c r="A1" t="inlineStr"><is><r><t>x</t></r></is></c><c r="B1" t="b"><v>1</v></c></row>`))
	f.Add(xlsxOf(t, nil, `<row r="1"><c r="A1"><f>SUM(A2:A9)</f><v>3</v></c></row>`, `<row r="1"><c r="A1"><v>9</v></c></row>`))
	f.Add([]byte("name,amount\nAda,12\n"))
	f.Add([]byte("\ufeffa,\"one, two\"\nb,c\n"))
	f.Add([]byte(billionLaughs("w:document")))
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})

	lim := fuzzLimits()
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, kind := range []detect.Kind{detect.KindDOCX, detect.KindXLSX, detect.KindCSV} {
			doc, err := Extract(data, kind, lim, nil)
			checkError(t, err)
			checkDocument(t, doc, lim)
			if err != nil && doc != nil {
				t.Fatalf("kind %q returned both a document and an error", kind)
			}
		}
	})
}
