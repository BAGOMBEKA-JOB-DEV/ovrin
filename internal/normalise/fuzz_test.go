package normalise

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzNormalise runs the whole stage over arbitrary bytes.
//
// This package parses text recovered from a file somebody else chose, so a
// panic here is a crash in the calling service (docs/threat-model.md, T3).
// The fuzz target asserts more than the absence of a panic: it re-checks the
// offset mapping on every input, because a mapping that is merely
// self-consistent on the documents we thought of is not a mapping.
func FuzzNormalise(f *testing.F) {
	seeds := []string{
		"",
		"Invoice 42",
		"a  b\tc\nd",
		"ofﬁce ﬄuent",
		"deprec-\niation",
		"２５，０００ ½ Ⅳ ①",
		"café résumé",
		"ign​ore tot‮al",
		"a\xffb",
		"\x00\x00\x00",
		"one\ntwo\x00three\nfour",
		"­­­",
		"\U0001D408\U0001D420\U0001D427",
		"ｶﾞ 가 中文",
		strings.Repeat("word ", 200),
		strings.Repeat("a-\n", 50),
	}
	for _, s := range seeds {
		for _, seed := range []uint16{0, 1, 7, 21, 63} {
			f.Add([]byte(s), seed)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte, seed uint16) {
		in := fuzzInput(data, seed)
		r := Normalise(in)

		checkTiling(t, r)
		checkSources(t, in, r)

		for i := 0; i < len(r.Text); i++ {
			got := r.Locate(Span{Start: i, End: i + 1})
			if len(got) != 1 {
				t.Fatalf("Locate of byte %d returned %d segments, want 1", i, len(got))
			}
			s := got[0]
			if !s.Verbatim || s.Inserted() {
				continue
			}
			w := pageNumbered(in, s.Page).Words[s.Word]
			if s.Src.Len() != 1 || r.Text[i] != w.Text[s.Src.Start] {
				t.Fatalf("byte %d does not map back to its source byte", i)
			}
		}

		last := 0
		for _, p := range r.Pages {
			if p.Marker.Start != last || p.Marker.End > p.Body.Start || p.Body.End > len(r.Text) {
				t.Fatalf("page span %v is not contiguous with the text", p)
			}
			if !strings.Contains(r.Text[p.Marker.Start:p.Marker.End], Marker(p.Page)) {
				t.Fatalf("marker span for page %d does not hold its marker", p.Page)
			}
			last = p.Body.End
		}
		if len(r.Pages) > 0 && last != len(r.Text) {
			t.Fatalf("page spans cover %d bytes of %d", last, len(r.Text))
		}

		for _, fd := range r.Findings {
			if fd.Kind == FindingUnknown {
				t.Fatal("a finding was reported with no kind")
			}
			if fd.Out.Start < 0 || fd.Out.End > len(r.Text) || fd.Out.Start > fd.Out.End {
				t.Fatalf("finding span %v is outside the text", fd.Out)
			}
			if fd.Count < 1 {
				t.Fatalf("finding %s has count %d", fd.Kind, fd.Count)
			}
			if fd.Why() == "" {
				t.Fatalf("finding %s has no reason", fd.Kind)
			}
		}
	})
}

// fuzzInput turns arbitrary bytes into a document. The seed selects which of
// the input shapes the pipeline has to handle — geometry or none, line hints
// or none, colours, a media box, metadata — so that one corpus exercises
// every path rather than only the plain-text one.
func fuzzInput(data []byte, seed uint16) Input {
	const maxPages, maxWords = 8, 64

	var in Input
	pages := bytes.Split(data, []byte{0})
	if len(pages) > maxPages {
		pages = pages[:maxPages]
	}
	for pi, pb := range pages {
		p := Page{Number: pi + 1}
		if seed&1 != 0 {
			p.Width, p.Height = 600, 800
		}
		if seed&16 != 0 {
			bg := Colour{R: 1, G: 1, B: 1}
			p.Background = &bg
		}
		words := bytes.Split(pb, []byte{'\n'})
		if len(words) > maxWords {
			words = words[:maxWords]
		}
		for wi, wb := range words {
			w := Word{Text: string(wb), Line: -1}
			if seed&2 != 0 {
				w.Line = wi / 3
			}
			if seed&4 != 0 {
				x := float64((int(seed)*7 + wi*13) % 600)
				y := float64((wi * 11) % 800)
				w.Box = Rect{MinX: x, MinY: y, MaxX: x + float64(len(wb)%50) + 1, MaxY: y + 12}
			}
			if seed&8 != 0 && wi%3 == 0 {
				c := Colour{R: 1, G: 1, B: 1}
				w.Colour = &c
			}
			p.Words = append(p.Words, w)
		}
		in.Pages = append(in.Pages, p)
	}
	if seed&32 != 0 && len(data) > 0 {
		in.Metadata = []Meta{{Key: "Title", Value: string(data)}}
	}
	return in
}
