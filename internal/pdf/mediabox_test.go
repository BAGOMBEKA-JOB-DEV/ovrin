// The media-box ceiling, fired.
//
// Gap closed here: every MediaBox in this package's other tests is
// [0 0 612 792], so the check that stands between a declared page size and a
// rasterisation had never run in the failing direction. ADR-0020 names the
// attack in the sentence that justifies the whole limit set — "a media box can
// declare a page that rasterises larger than physical memory" — and
// docs/threat-model.md T2 counts on the refusal. A guard that never fires reads
// as a safety property and is not one.
//
// The check is in Doc.Page rather than in Open on purpose, and these tests
// assert that shape: the structure of such a document is perfectly legal, and
// what is refused is the attempt to lay the page out.
package pdf

import (
	"errors"
	"fmt"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// boxed builds a one-page document whose MediaBox is the given array and whose
// content stream shows a word, so that a page which is refused can be told from
// one that was merely empty.
func boxed(box string) []byte {
	const content = "BT /F1 12 Tf 72 720 Td (visible) Tj ET"
	return buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox " + box + " " +
			"/Resources << /Font << " + helvetica + " >> >> /Contents 4 0 R >>",
		streamObj("", content),
	}, "")
}

// A media box that would rasterise larger than memory is refused, and refused
// before the page is laid out.
//
// The numbers are the point: 200000 × 200000 points is a page four kilometres
// on a side, fifty thousand megapixels at any resolution anyone would render it
// at, and a parser that believes it will try to allocate for it. The ceiling is
// checked by division rather than multiplication, so a box large enough to
// overflow the product cannot wrap around and land under the limit — the last
// two rows are that case.
func TestAnEnormousMediaBoxIsRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		box  string
	}{
		{"a page four kilometres square", "[0 0 200000 200000]"},
		{"a page enormous in one dimension only", "[0 0 4000000 40]"},
		{"a box written from its far corner back to its origin", "[200000 200000 0 0]"},
		{"a box offset so that only its extent is enormous", "[100000 100000 400000 400000]"},
		{"a box whose product overflows a 32-bit count", "[0 0 3000000000 3000000000]"},
		{"a box whose product overflows a 64-bit count", "[0 0 4000000000 4000000000]"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The document's structure is legal, and Open reads structure.
			// Refusing here instead would mean a document could be rejected
			// for a page nobody asked to read.
			doc, err := Open(boxed(tt.box), detect.Limits{}, nil)
			if err != nil {
				t.Fatalf("Open: %v: the document is structurally sound and only its page size is hostile", err)
			}

			p, err := doc.Page(1)
			if !errors.Is(err, detect.ErrLimitExceeded) {
				t.Fatalf("Page(1) = %v, want a limit failure for MediaBox %s", err, tt.box)
			}
			// Nothing was laid out. A page returned alongside the refusal
			// would mean the interpreter had already run over a surface this
			// size, which is the allocation the ceiling exists to prevent.
			if len(p.Content.Words) != 0 || p.Stats.Chars != 0 {
				t.Errorf("the refused page came back with %d word(s) and %d character(s); "+
					"the ceiling is being checked after the page is built, not before",
					len(p.Content.Words), p.Stats.Chars)
			}
		})
	}
}

// The ceiling that is enforced is the one the caller configured.
//
// Without this, a hardcoded size test would pass just as well, and a
// WithMaxPagePixels that never reached the parser would look exactly like a
// working one. An ordinary US Letter page is refused here and read in the test
// below, and the only difference between them is the limit.
func TestTheMediaBoxCeilingIsTheConfiguredOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		max    int
		box    string
		wantOK bool
	}{
		{"letter, under a generous ceiling", 50_000_000, "[0 0 612 792]", true},
		{"letter, under a ceiling below its own area", 1000, "[0 0 612 792]", false},
		{"letter, exactly at its own area", 612 * 792, "[0 0 612 792]", true},
		{"letter, one unit under its own area", 612*792 - 1, "[0 0 612 792]", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := Open(boxed(tt.box), detect.Limits{MaxPagePixels: tt.max}, nil)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			p, err := doc.Page(1)
			switch {
			case tt.wantOK && err != nil:
				t.Fatalf("Page(1) = %v, want the page to be read at MaxPagePixels = %d", err, tt.max)
			case !tt.wantOK && !errors.Is(err, detect.ErrLimitExceeded):
				t.Fatalf("Page(1) = %v, want a limit failure at MaxPagePixels = %d", err, tt.max)
			}
			if tt.wantOK && words(p) != "visible" {
				t.Errorf("words = %q, want %q: the page was accepted but not read", words(p), "visible")
			}
		})
	}
}

// A page whose size is missing or degenerate falls back to US Letter rather
// than being refused, and the fallback is what the ceiling is then applied to.
//
// This is the other half of the guard: a document that declares nothing must
// not be treated as declaring something enormous, or every PDF with an
// inherited or absent box would fail.
func TestAnAbsentOrDegenerateMediaBoxIsNotRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "no media box anywhere in the page's ancestry",
			data: buildPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /Resources << /Font << " + helvetica +
					" >> >> /Contents 4 0 R >>",
				streamObj("", "BT /F1 12 Tf 72 720 Td (visible) Tj ET"),
			}, ""),
		},
		{name: "a box of zero extent", data: boxed("[0 0 0 0]")},
		{name: "a box whose entries are not numbers", data: boxed("[(a) (b) (c) (d)]")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc, err := Open(tt.data, detect.Limits{}, nil)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			p, err := doc.Page(1)
			if err != nil {
				t.Fatalf("Page(1) = %v, want the US Letter fallback rather than a refusal", err)
			}
			if got := fmt.Sprintf("%.0f×%.0f", p.Stats.WidthPt, p.Stats.HeightPt); got != "612×792" {
				t.Errorf("page size = %s, want the 612×792 fallback", got)
			}
			if words(p) != "visible" {
				t.Errorf("words = %q, want %q", words(p), "visible")
			}
		})
	}
}

// A media box written in exponent notation is not an enormous page.
//
// PDF real numbers have no exponent form, so "5e18" is the number 5 followed by
// a token that is not one: the page is five points across and is read like any
// other small page. This is here because it looks exactly like the attack the
// test above refuses, and a reader who writes it into a fixture expecting a
// refusal is entitled to find out from a test why there is none — and because
// a lexer that started accepting exponents would change what this document
// means, which is worth being told about.
func TestExponentNotationIsNotAnEnormousPage(t *testing.T) {
	t.Parallel()

	doc, err := Open(boxed("[0 0 5e18 5e18]"), detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1) = %v, want the page to be read: 5e18 is not one PDF number", err)
	}
	if p.Stats.WidthPt > 1000 || p.Stats.HeightPt > 1000 {
		t.Errorf("page size = %.0f×%.0f: the exponent is being read as part of the number, "+
			"and a document of this size must be refused rather than laid out",
			p.Stats.WidthPt, p.Stats.HeightPt)
	}
}
