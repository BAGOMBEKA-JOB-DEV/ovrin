package normalise

import (
	"strings"
	"unicode/utf8"
)

// record accumulates a finding, merging repeats of the same cause on the same
// page.
//
// One zero-width character is a typo and four hundred is a payload, so the
// count is what carries the weight. Out keeps the first occurrence, because a
// reviewer needs somewhere to start looking and a span covering everything
// from the first to the last occurrence would cover the page.
func (bd *builder) record(f Finding) {
	k := findKey{page: f.Page, kind: f.Kind, r: f.Rune, key: f.Key}
	if bd.found == nil {
		bd.found = make(map[findKey]*Finding, 4)
	}
	if prev, ok := bd.found[k]; ok {
		prev.Count += f.Count
		if prev.Box != nil && f.Box != nil {
			u := prev.Box.Union(*f.Box)
			prev.Box = &u
		}
		return
	}
	cp := f
	bd.found[k] = &cp
	bd.order = append(bd.order, k)
}

// scanRune reports a source rune that renders as nothing or reorders what is
// around it. The rune is left in the output: reporting is the whole policy
// (docs/adr/0017-untrusted-document-content.md, mitigation 4).
func (bd *builder) scanRune(page int, src string, out Span, box *Rect) {
	r, size := utf8.DecodeRuneInString(src)
	if size == 0 || r == utf8.RuneError {
		return
	}
	switch {
	case isBidiControl(r):
		bd.record(Finding{Kind: FindingBidiControl, Page: page, Out: out, Box: box, Rune: r, Count: 1})
	case isZeroWidth(r):
		bd.record(Finding{Kind: FindingZeroWidth, Page: page, Out: out, Box: box, Rune: r, Count: 1})
	}
}

// checkWord reports a run positioned where nobody will see it, or painted in
// the colour of the paper.
//
// Both checks are skipped rather than guessed at when the reading did not
// supply what they need. An assumed media box would flag every reading with
// no geometry, and an assumed white background would flag every white-on-dark
// design as an attack; a detector that fires on ordinary documents is one
// operators learn to ignore, which costs more than it saves.
func (bd *builder) checkWord(p *Page, w *Word) {
	if strings.TrimSpace(w.Text) == "" {
		return
	}
	if p.Width > 0 && p.Height > 0 && !w.Box.Zero() && offPage(w.Box, p.Width, p.Height) {
		b := w.Box
		bd.record(Finding{Kind: FindingOffPage, Page: p.Number, Box: &b, Count: 1})
	}
	if p.Background != nil && w.Colour != nil && w.Colour.near(*p.Background) {
		b := w.Box
		var bp *Rect
		if !b.Zero() {
			bp = &b
		}
		bd.record(Finding{Kind: FindingBackgroundColour, Page: p.Number, Box: bp, Count: 1})
	}
}

// offPage reports whether a box lies wholly outside the media box.
//
// Wholly, not partly: a descender crossing the bottom edge and a footer laid
// out a point past the margin are ordinary, and only text placed somewhere it
// cannot be seen at all is worth an operator's attention.
func offPage(b Rect, w, h float64) bool {
	return b.MaxX <= 0 || b.MinX >= w || b.MaxY <= 0 || b.MinY >= h
}

// strongPhrases each identify metadata addressed to a model on their own.
//
// The list is closed and deliberately short. Every entry is a phrase that has
// no reason to appear in a Title or a Keywords field of a real document, so
// each one that fires is worth reading. Broadening it is how a detector turns
// into noise (docs/adr/0017-untrusted-document-content.md).
var strongPhrases = []string{
	"ignore previous",
	"ignore all previous",
	"ignore the previous",
	"ignore the above",
	"ignore any previous",
	"ignore prior",
	"ignore your",
	"ignore the schema",
	"disregard previous",
	"disregard the above",
	"disregard all",
	"disregard your",
	"forget previous",
	"forget everything",
	"previous instructions",
	"prior instructions",
	"above instructions",
	"new instructions",
	"system prompt",
	"you are an ai",
	"you are a language model",
	"as an ai",
	"<|im_start|>",
	"<|im_end|>",
	"[/inst]",
}

// weakPhrases are imperative shapes that a legitimate document does
// occasionally contain, so two distinct ones are required before a finding is
// raised.
var weakPhrases = []string{
	"you must",
	"do not ",
	"respond with",
	"output only",
	"return only",
	"instead of",
	"override",
	"instruction",
	"must be set",
	"set the value",
}

// scanMetadata reports instruction-shaped language in document metadata.
//
// Metadata never enters the normalised stream, so this is the only thing that
// looks at it. Detection normalises the value the same way the text is
// normalised and then removes the invisible characters, because a payload
// written as "i<U+200B>gnore previous" is the same payload; the value itself
// is not modified, because there is nothing to modify — it was never going to
// be sent anywhere.
func (bd *builder) scanMetadata(ms []Meta) {
	for _, m := range ms {
		v, _ := Canonical(m.Value)
		v = strings.ToLower(stripInvisible(v))
		if v == "" {
			continue
		}
		hits := 0
		for _, p := range strongPhrases {
			if strings.Contains(v, p) {
				hits = 2
				break
			}
		}
		if hits < 2 {
			seen := 0
			for _, p := range weakPhrases {
				if strings.Contains(v, p) {
					seen++
				}
			}
			hits = seen
		}
		if hits < 2 {
			continue
		}
		bd.record(Finding{Kind: FindingInstruction, Key: safeKey(m.Key), Count: 1})
	}
}

// stripInvisible removes the characters that render as nothing, for matching
// only. Nothing in the output stream is ever stripped.
func stripInvisible(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return isZeroWidth(r) || isBidiControl(r) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isZeroWidth(r) || isBidiControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// safeKey returns key if it looks like a structural field name, and empty
// otherwise.
//
// A hostile document chooses its own metadata keys and this value is printed
// into a review reason, which is a log line that ends up in systems nobody
// audited. Anything that is not letters, digits and the punctuation a
// namespaced property uses is dropped rather than escaped (docs/rules.md
// §7.5).
func safeKey(key string) string {
	if key == "" || len(key) > 40 {
		return ""
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '.', r == ':', r == '-':
		default:
			return ""
		}
	}
	return key
}
