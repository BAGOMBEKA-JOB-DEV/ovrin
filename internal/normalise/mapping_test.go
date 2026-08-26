package normalise

import (
	"strings"
	"testing"
)

// mappingCorpus is the set of documents every offset property is asserted
// over. Each one exercises a transformation that changes the relationship
// between output bytes and source bytes: collapsing, expanding, dropping,
// reordering, or inserting.
func mappingCorpus() []struct {
	name string
	in   Input
} {
	white := Colour{R: 1, G: 1, B: 1}
	return []struct {
		name string
		in   Input
	}{
		{"plain ascii", Input{Pages: []Page{text(1, "Invoice number 42")}}},
		{"several words and lines", Input{Pages: []Page{text(1, "Invoice|number|42", "Total|1,250.00")}}},
		{"whitespace runs", Input{Pages: []Page{text(1, "a   b\t\t\tc", "  d  ")}}},
		{"ligatures", Input{Pages: []Page{text(1, "ofﬁce|ﬄuent|ﬅop")}}},
		{"compatibility forms", Input{Pages: []Page{text(1, "２５，０００|m²|½|Ⅳ|①")}}},
		{"combining marks", Input{Pages: []Page{text(1, "café|résumé|Đắk")}}},
		{"hyphenated across lines", Input{Pages: []Page{text(1, "annual|deprec-", "iation|charge")}}},
		{"soft hyphen across lines", Input{Pages: []Page{text(1, "depre\u00adc\u00ad", "iation")}}},
		{"zero width and bidi", Input{Pages: []Page{text(1, "ign\u200bore|tot\u202eal")}}},
		{"invalid utf-8", Input{Pages: []Page{text(1, "a\xffb|\xc3")}}},
		{"empty words", Input{Pages: []Page{text(1, "||a||", "||")}}},
		{"several pages", Input{Pages: []Page{text(1, "one"), {Number: 2}, text(3, "three|four")}}},
		{"two columns", Input{Pages: []Page{laid(1, 600, 800,
			at("left", 40, 100, 90, 112), at("right", 340, 100, 395, 112),
			at("one", 40, 114, 90, 126), at("two", 340, 114, 395, 126),
		)}}},
		{"kerned runs", Input{Pages: []Page{laid(1, 600, 800,
			at("To", 40, 100, 52, 112), at("tal", 52, 100, 70, 112),
			at("42", 100, 100, 118, 112),
			at("Su", 40, 116, 52, 128), at("m", 52, 116, 60, 128),
		)}}},
		{"off page and background", Input{Pages: []Page{{
			Number: 1, Width: 600, Height: 800, Background: &white,
			Words: []Word{
				{Text: "visible", Line: 0, Box: Rect{MinX: 40, MinY: 40, MaxX: 90, MaxY: 52}},
				{Text: "hidden", Line: 1, Box: Rect{MinX: 4000, MinY: 40, MaxX: 4100, MaxY: 52}},
				{Text: "white", Line: 2, Colour: &white},
			},
		}}}},
		{"nothing at all", Input{}},
		{"one blank page", Input{Pages: []Page{{Number: 1}}}},
	}
}

// TestSegmentsTileTheText asserts the structural invariant every other
// property rests on: the segments are ordered, do not overlap, leave no gap,
// and together cover exactly the text.
func TestSegmentsTileTheText(t *testing.T) {
	t.Parallel()
	for _, c := range mappingCorpus() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Normalise(c.in)
			checkTiling(t, r)
		})
	}
}

func checkTiling(t *testing.T, r *Result) {
	t.Helper()
	at := 0
	for i, s := range r.Segments {
		if s.Out.Start != at {
			t.Fatalf("segment %d starts at %d, want %d: the segments do not tile the text", i, s.Out.Start, at)
		}
		if s.Out.End <= s.Out.Start {
			t.Fatalf("segment %d is empty: %v", i, s.Out)
		}
		if s.Out.End > len(r.Text) {
			t.Fatalf("segment %d ends at %d, past the %d bytes of text", i, s.Out.End, len(r.Text))
		}
		at = s.Out.End
	}
	if at != len(r.Text) {
		t.Fatalf("segments cover %d bytes of %d", at, len(r.Text))
	}
}

// TestVerbatimSegmentsHoldTheirSourceBytes is the heart of the offset
// obligation. Every segment claiming to be verbatim must hold exactly the
// bytes of the word it names, at the offsets it names. Nothing downstream can
// recover from this being wrong, and nothing else would notice.
func TestVerbatimSegmentsHoldTheirSourceBytes(t *testing.T) {
	t.Parallel()
	for _, c := range mappingCorpus() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Normalise(c.in)
			checkSources(t, c.in, r)
		})
	}
}

func checkSources(t *testing.T, in Input, r *Result) {
	t.Helper()
	for i, s := range r.Segments {
		if s.Inserted() {
			if !s.Src.Empty() {
				t.Errorf("segment %d is inserted text but claims source %v", i, s.Src)
			}
			if s.Box != nil {
				t.Errorf("segment %d is inserted text but carries a box", i)
			}
			continue
		}
		page := pageNumbered(in, s.Page)
		if page == nil {
			t.Fatalf("segment %d names page %d, which is not in the input", i, s.Page)
		}
		if s.Word < 0 || s.Word >= len(page.Words) {
			t.Fatalf("segment %d names word %d of %d on page %d", i, s.Word, len(page.Words), s.Page)
		}
		w := page.Words[s.Word]
		if s.Src.Start < 0 || s.Src.End > len(w.Text) || s.Src.Start > s.Src.End {
			t.Fatalf("segment %d source %v is outside the %d bytes of its word", i, s.Src, len(w.Text))
		}
		if s.Box == nil {
			if !w.Box.Zero() {
				t.Errorf("segment %d has no box but its word does", i)
			}
		} else if *s.Box != w.Box {
			t.Errorf("segment %d box = %v, want the word's %v", i, *s.Box, w.Box)
		}
		if !s.Verbatim {
			continue
		}
		got := r.Text[s.Out.Start:s.Out.End]
		want := w.Text[s.Src.Start:s.Src.End]
		if got != want {
			t.Errorf("segment %d claims verbatim: text has %q, source has %q", i, got, want)
		}
	}
}

func pageNumbered(in Input, n int) *Page {
	for i := range in.Pages {
		if in.Pages[i].Number == n {
			return &in.Pages[i]
		}
	}
	return nil
}

// TestEveryOutputByteLocatesToItsSource walks every byte of every normalised
// document and asserts that Locate answers for it, that the answer is the
// segment covering it, and that a verbatim answer identifies the exact source
// byte. This is the property the whole package exists to provide.
func TestEveryOutputByteLocatesToItsSource(t *testing.T) {
	t.Parallel()
	for _, c := range mappingCorpus() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Normalise(c.in)
			for i := 0; i < len(r.Text); i++ {
				got := r.Locate(Span{Start: i, End: i + 1})
				if len(got) != 1 {
					t.Fatalf("Locate of byte %d returned %d segments, want 1", i, len(got))
				}
				s := got[0]
				if s.Out.Start != i || s.Out.End != i+1 {
					t.Fatalf("Locate of byte %d returned out %v", i, s.Out)
				}
				if s.Inserted() {
					continue
				}
				w := pageNumbered(c.in, s.Page).Words[s.Word]
				if !s.Verbatim {
					if s.Src.Start < 0 || s.Src.End > len(w.Text) {
						t.Fatalf("byte %d maps to source %v outside its word", i, s.Src)
					}
					continue
				}
				if s.Src.Len() != 1 {
					t.Fatalf("byte %d maps verbatim to %d source bytes, want 1", i, s.Src.Len())
				}
				if r.Text[i] != w.Text[s.Src.Start] {
					t.Fatalf("byte %d is %q but its source byte is %q", i, r.Text[i], w.Text[s.Src.Start])
				}
			}
		})
	}
}

// TestLocateClipsToTheQuery asserts that a query narrower than a segment
// narrows the answer, and that a verbatim segment narrows its source with it.
func TestLocateClipsToTheQuery(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{text(1, "Invoice number 42")}})
	i := strings.Index(r.Text, "number")
	q := Span{Start: i, End: i + len("number")}

	got := r.Locate(q)
	if len(got) != 1 {
		t.Fatalf("Locate returned %d segments, want 1", len(got))
	}
	s := got[0]
	if s.Out != q {
		t.Errorf("Locate out = %v, want %v", s.Out, q)
	}
	if !s.Verbatim {
		t.Fatal("expected a verbatim segment")
	}
	word := "Invoice number 42"
	if got := word[s.Src.Start:s.Src.End]; got != "number" {
		t.Errorf("clipped source = %q, want %q", got, "number")
	}

	if n := len(r.Locate(Span{Start: 5, End: 5})); n != 0 {
		t.Errorf("Locate of an empty span returned %d segments, want 0", n)
	}
	if n := len(r.Locate(Span{Start: len(r.Text), End: len(r.Text) + 10})); n != 0 {
		t.Errorf("Locate past the end returned %d segments, want 0", n)
	}
}

// TestRegionsSplitAtALineBreak is the hyphenation half of the obligation: a
// word rejoined from two lines must report the box of each fragment, not one
// box covering both lines and everything between them.
func TestRegionsSplitAtALineBreak(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{laid(1, 600, 800,
		at("deprec-", 40, 100, 100, 112),
		at("iation", 40, 114, 90, 126),
	)}})
	if got := body(r); got != "depreciation" {
		t.Fatalf("body = %q, want %q", got, "depreciation")
	}
	i := strings.Index(r.Text, "depreciation")
	regions := r.Regions(Span{Start: i, End: i + len("depreciation")})
	if len(regions) != 2 {
		t.Fatalf("got %d regions, want 2: %v", len(regions), regions)
	}
	want := []Region{
		{Page: 1, Box: Rect{MinX: 40, MinY: 100, MaxX: 100, MaxY: 112}},
		{Page: 1, Box: Rect{MinX: 40, MinY: 114, MaxX: 90, MaxY: 126}},
	}
	for i, w := range want {
		if regions[i] != w {
			t.Errorf("region %d = %v, want %v", i, regions[i], w)
		}
	}
}

// TestRegionsUnionWithinALine asserts the other half: several words on one
// line highlight as one box.
func TestRegionsUnionWithinALine(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{laid(1, 600, 800,
		at("Acme", 40, 100, 80, 112),
		at("Holdings", 84, 100, 150, 112),
	)}})
	i := strings.Index(r.Text, "Acme")
	regions := r.Regions(Span{Start: i, End: i + len("Acme Holdings")})
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1: %v", len(regions), regions)
	}
	want := Region{Page: 1, Box: Rect{MinX: 40, MinY: 100, MaxX: 150, MaxY: 112}}
	if regions[0] != want {
		t.Errorf("region = %v, want %v", regions[0], want)
	}
	if n := len(r.Regions(Span{Start: 0, End: 3})); n != 0 {
		t.Errorf("a page marker produced %d regions, want 0", n)
	}
}

// TestRegionsAreEmptyWithoutGeometry pins that nil means unknown, never
// "not on the page" (docs/adr/0015-provenance.md).
func TestRegionsAreEmptyWithoutGeometry(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{text(1, "Invoice 42")}})
	if got := r.Regions(Span{Start: 0, End: len(r.Text)}); got != nil {
		t.Errorf("Regions with no geometry = %v, want nil", got)
	}
}

// TestLigatureSpanCoversTheSourceGlyph pins the example docs/pipeline.md
// gives: the two output bytes of "fi" map back to the one source glyph.
func TestLigatureSpanCoversTheSourceGlyph(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{text(1, "ofﬁce")}})
	i := strings.Index(r.Text, "fi")
	got := r.Locate(Span{Start: i, End: i + 2})
	if len(got) != 1 {
		t.Fatalf("got %d segments for the expanded ligature, want 1", len(got))
	}
	s := got[0]
	if s.Verbatim {
		t.Error("an expanded ligature is not verbatim")
	}
	if src := "ofﬁce"[s.Src.Start:s.Src.End]; src != "ﬁ" {
		t.Errorf("source of the expanded ligature = %q, want %q", src, "ﬁ")
	}
}

// TestInsertedTextIsMarked pins that page markers and the separators between
// words are attributable to ovrin rather than to the document, which is what
// grounding refuses to match inside.
func TestInsertedTextIsMarked(t *testing.T) {
	t.Parallel()
	r := Normalise(Input{Pages: []Page{text(1, "alpha|beta")}})
	marker := r.Pages[0].Marker
	if !r.Inserted(marker) {
		t.Error("the page marker is not reported as inserted")
	}
	i := strings.Index(r.Text, "alpha")
	if r.Inserted(Span{Start: i, End: i + 5}) {
		t.Error("a word is reported as inserted")
	}
	if !r.Inserted(Span{Start: i, End: i + len("alpha beta")}) {
		t.Error("a span crossing the separator is not reported as inserted")
	}
}
