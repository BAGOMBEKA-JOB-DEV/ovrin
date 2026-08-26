package normalise

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Default thresholds for reading-order analysis.
//
// These are judgement, not measurement, and they are expressed as ratios
// because absolute point values would be wrong for a page typeset at eight
// points and wrong again for one at twenty-four. They will be tuned against
// the evaluation corpus (docs/adr/0023-evaluation-corpus.md); until they are,
// the honest description is that they work on the layouts we have looked at.
const (
	// DefaultGutterRatio is the share of the page width a vertical whitespace
	// run must reach before it is treated as a column gutter. Three per cent
	// of A4 is about eighteen points, which is wider than any inter-word gap
	// and narrower than any real gutter.
	DefaultGutterRatio = 0.03

	// DefaultLeadingRatio is the share of the median line height a vertical
	// whitespace run must reach before it separates two blocks. Single-spaced
	// text leaves roughly a fifth of a line height between lines and a
	// paragraph break leaves more than one, so this errs high on purpose:
	// blocks bound hyphenation repair, and splitting a paragraph into a block
	// per line repairs nothing, while merging two paragraphs costs one blank
	// line.
	DefaultLeadingRatio = 1.2

	// DefaultMaxCutDepth bounds the recursion in reading-order analysis. Every
	// limit has a finite default (docs/rules.md §5.2), and this one is also
	// what keeps a hostile page of ten thousand one-word lines from taking the
	// stack with it.
	DefaultMaxCutDepth = 12
)

type options struct {
	gutterRatio  float64
	leadingRatio float64
	maxCutDepth  int
}

// Option configures one normalisation run.
//
// Functional rather than an exported struct so that adding a threshold is not
// a breaking change even inside the module (docs/rules.md §1.4).
type Option func(*options)

// WithGutterRatio sets the share of the page width a vertical whitespace run
// must reach to be treated as a column gutter. Values outside (0,1) are
// ignored rather than clamped, because a caller passing one has made a
// mistake and silently substituting a different policy would hide it.
func WithGutterRatio(r float64) Option {
	return func(o *options) {
		if r > 0 && r < 1 {
			o.gutterRatio = r
		}
	}
}

// WithLeadingRatio sets the share of the median line height a vertical
// whitespace run must reach to separate two blocks. Values that are not
// positive are ignored.
func WithLeadingRatio(r float64) Option {
	return func(o *options) {
		if r > 0 {
			o.leadingRatio = r
		}
	}
}

// WithMaxCutDepth bounds the recursion in reading-order analysis. Values that
// are not positive are ignored.
func WithMaxCutDepth(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.maxCutDepth = n
		}
	}
}

// Result is the normalised text and everything needed to trace it back.
//
// It is immutable once returned and may be read from any number of
// goroutines. The slices are not copied on access, so a caller that mutates
// them has broken the mapping for everybody holding the same Result.
type Result struct {
	// Text is the normalised stream, page markers included.
	Text string

	// Segments map Text back to the reading, in Out order, without gaps and
	// without overlaps. Together they cover every byte of Text.
	Segments []Segment

	// Pages locates each page inside Text, in document order.
	Pages []PageSpan

	// Findings are the suspicious content that was detected and left in
	// place.
	Findings []Finding
}

// Marker returns the page marker text for a 1-based page number.
//
// Exported so that grounding, prompt construction and tests agree on it
// without any of them matching on a literal. Nothing depends on the format:
// the markers are located structurally through [PageSpan], because a document
// is free to contain text that looks exactly like one.
func Marker(page int) string { return "[page " + strconv.Itoa(page) + "]" }

// Normalise turns raw positioned page content into one text stream and the
// mapping back to it.
//
// It returns no error. Nothing here can fail on input: a page with no words,
// a word with no text, text that is not valid UTF-8 and geometry that is
// nonsense are all things a hostile document will contain, and each has a
// defined result rather than a failure. Limits that can fail belong to
// acquisition, before this stage sees anything
// (docs/adr/0020-resource-limits.md).
func Normalise(in Input, opts ...Option) *Result {
	o := options{
		gutterRatio:  DefaultGutterRatio,
		leadingRatio: DefaultLeadingRatio,
		maxCutDepth:  DefaultMaxCutDepth,
	}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}

	bd := &builder{}
	for i := range in.Pages {
		bd.page(&in.Pages[i], o)
	}
	bd.scanMetadata(in.Metadata)

	return &Result{
		Text:     string(bd.b),
		Segments: bd.segs,
		Pages:    bd.pages,
		Findings: bd.findings(),
	}
}

// builder accumulates the output, the segments and the findings together, so
// that no pass can add text without saying where it came from.
type builder struct {
	b     []byte
	segs  []Segment
	pages []PageSpan
	found map[findKey]*Finding
	order []findKey
}

type findKey struct {
	page int
	kind FindingKind
	r    rune
	key  string
}

// atBreak reports whether the output already ends in a separator, so that a
// second one is not written. It is what makes whitespace collapsing work
// across word, line and page boundaries as well as within a run.
func (bd *builder) atBreak() bool {
	if len(bd.b) == 0 {
		return true
	}
	switch bd.b[len(bd.b)-1] {
	case ' ', '\n':
		return true
	}
	return false
}

// insert writes text ovrin produced itself. It is marked Word == -1 so that
// grounding can refuse to match inside it.
func (bd *builder) insert(s string, page int) {
	if s == "" {
		return
	}
	start := len(bd.b)
	bd.b = append(bd.b, s...)
	bd.segs = append(bd.segs, Segment{
		Out:  Span{Start: start, End: len(bd.b)},
		Page: page,
		Word: -1,
		Line: -1,
	})
}

// push appends a segment, coalescing it into the previous one when both are
// verbatim and contiguous in output and in source. Coalescing is not an
// optimisation: without it a page of ASCII produces one segment per rune and
// the mapping costs more than the text.
func (bd *builder) push(s Segment) {
	if s.Out.Empty() {
		return
	}
	if n := len(bd.segs); n > 0 {
		p := &bd.segs[n-1]
		if p.Verbatim && s.Verbatim && p.Page == s.Page && p.Word == s.Word &&
			p.Line == s.Line && p.Box == s.Box &&
			p.Out.End == s.Out.Start && p.Src.End == s.Src.Start {
			p.Out.End = s.Out.End
			p.Src.End = s.Src.End
			return
		}
	}
	bd.segs = append(bd.segs, s)
}

// run writes one word's text, normalised, collapsing whitespace and recording
// a segment for every run of output bytes.
//
// text is a prefix of the word's own Text — shorter only when a trailing
// hyphen was cut off for a line-break repair — so the source offsets recorded
// here are offsets into Word.Text and need no adjustment.
func (bd *builder) run(pg, wi, li int, box *Rect, text string, suppressLead bool) {
	us := units(text)
	for i := 0; i < len(us); {
		u := us[i]
		if u.space {
			j := i
			for j < len(us) && us[j].space {
				j++
			}
			if !bd.atBreak() && (!suppressLead || i != 0) {
				start := len(bd.b)
				bd.b = append(bd.b, ' ')
				bd.push(Segment{
					Out:      Span{Start: start, End: len(bd.b)},
					Src:      Span{Start: u.srcStart, End: us[j-1].srcEnd},
					Page:     pg,
					Word:     wi,
					Line:     li,
					Box:      box,
					Verbatim: j == i+1 && u.verbatim && u.out == " ",
				})
			}
			i = j
			continue
		}
		start := len(bd.b)
		bd.b = append(bd.b, u.out...)
		bd.push(Segment{
			Out:      Span{Start: start, End: len(bd.b)},
			Src:      Span{Start: u.srcStart, End: u.srcEnd},
			Page:     pg,
			Word:     wi,
			Line:     li,
			Box:      box,
			Verbatim: u.verbatim,
		})
		bd.scanRune(pg, text[u.srcStart:u.srcEnd], Span{Start: start, End: len(bd.b)}, box)
		i++
	}
}

// page writes one page: its marker, then its blocks in reading order.
func (bd *builder) page(p *Page, o options) {
	markerStart := len(bd.b)
	sep := ""
	if len(bd.b) > 0 {
		sep = "\n\n"
	}
	bd.insert(sep+Marker(p.Number)+"\n", p.Number)
	markerEnd := len(bd.b)

	med := medianHeight(p)
	blocks := blocksOf(p, o)

	lineNo := 0
	for bi := range blocks {
		if bi > 0 {
			bd.insert("\n\n", p.Number)
		}
		joins, drops := hyphenJoins(p, blocks[bi])
		for lj := range blocks[bi].lines {
			joined := lj > 0 && joins[lj-1]
			if lj > 0 && !joined {
				bd.insert("\n", p.Number)
			}
			l := blocks[bi].lines[lj]
			for k, wi := range l.words {
				w := &p.Words[wi]
				bd.checkWord(p, w)
				text := w.Text
				if joins[lj] && k == len(l.words)-1 {
					text = text[:hyphenCut(text, drops[lj])]
				}
				if k > 0 && bd.needSpace(&p.Words[l.words[k-1]], w, med) {
					bd.insert(" ", p.Number)
				}
				bd.run(p.Number, wi, lineNo, boxOf(w), text, joined && k == 0)
			}
			lineNo++
		}
	}

	bd.pages = append(bd.pages, PageSpan{
		Page:   p.Number,
		Marker: Span{Start: markerStart, End: markerEnd},
		Body:   Span{Start: markerEnd, End: len(bd.b)},
	})
}

// boxOf returns one pointer per word, so that segments from the same word
// share it and coalescing can compare pointers rather than four floats.
func boxOf(w *Word) *Rect {
	if w.Box.Zero() {
		return nil
	}
	b := w.Box
	return &b
}

// needSpace decides whether two runs on the same line are separate words.
//
// Geometry decides it, because a PDF splits one word across several
// text-showing operators whenever it kerns, and joining those with a space
// turns "Total" into "T otal". A gap wider than a fifth of the line height is
// a space; anything less is the same word. With no geometry, or with two runs
// carrying the same box, the runs are separated: an incorrect space is
// recoverable by a reader and a missing one is not.
func (bd *builder) needSpace(prev, cur *Word, med float64) bool {
	if bd.atBreak() {
		return false
	}
	if prev.Box.Zero() || cur.Box.Zero() || prev.Box == cur.Box {
		return true
	}
	return cur.Box.MinX-prev.Box.MaxX > 0.2*med
}

// hyphens are the characters a typesetter breaks a word with.
var hyphens = map[rune]bool{
	'-':    true, // hyphen-minus
	0x00AD: true, // soft hyphen
	0x2010: true, // hyphen
	0x2011: true, // non-breaking hyphen
	0x2012: true, // figure dash
}

// hyphenJoins reports, per line of a block, whether the line's last word is
// broken across the line boundary, and whether the hyphen should be dropped.
//
// The rule is the conventional one: a lower-case continuation means the
// hyphen was inserted by the typesetter and goes, an upper-case continuation
// means it was in the word and stays. It is a heuristic and it is wrong on
// "e-\nmail", which is the price of being right on every ordinary word broken
// across a line.
func hyphenJoins(p *Page, b block) (joins, drops []bool) {
	joins = make([]bool, len(b.lines))
	drops = make([]bool, len(b.lines))
	for i := 0; i+1 < len(b.lines); i++ {
		cur, next := b.lines[i], b.lines[i+1]
		if len(cur.words) == 0 || len(next.words) == 0 {
			continue
		}
		t := strings.TrimRight(p.Words[cur.words[len(cur.words)-1]].Text, " \t")
		r, size := utf8.DecodeLastRuneInString(t)
		if size == 0 || !hyphens[r] {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(t[:len(t)-size])
		if !unicode.IsLetter(before) {
			continue
		}
		after, _ := utf8.DecodeRuneInString(strings.TrimLeft(p.Words[next.words[0]].Text, " \t"))
		if !unicode.IsLetter(after) {
			continue
		}
		joins[i] = true
		drops[i] = unicode.IsLower(after) || r == 0x00AD
	}
	return joins, drops
}

// hyphenCut returns the byte index to cut a line-final word at.
func hyphenCut(text string, drop bool) int {
	t := strings.TrimRight(text, " \t")
	if !drop {
		return len(t)
	}
	_, size := utf8.DecodeLastRuneInString(t)
	return len(t) - size
}

// findings returns the accumulated findings in the order they were first
// seen, which is document order.
func (bd *builder) findings() []Finding {
	if len(bd.order) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(bd.order))
	for _, k := range bd.order {
		out = append(out, *bd.found[k])
	}
	return out
}

// Locate returns the segments covering q, clipped to it.
//
// A verbatim segment is clipped in source as well as in output, because its
// bytes correspond one to one. A segment produced by a rewrite is returned
// with its whole source range: the output range narrows, the source range
// does not, and widening is the only honest answer when three source bytes
// became two output bytes with no correspondence between them.
//
// The returned slice is freshly allocated and may be retained.
func (r *Result) Locate(q Span) []Segment {
	if q.Empty() || len(r.Segments) == 0 {
		return nil
	}
	i := sort.Search(len(r.Segments), func(i int) bool { return r.Segments[i].Out.End > q.Start })
	var out []Segment
	for ; i < len(r.Segments) && r.Segments[i].Out.Start < q.End; i++ {
		s := r.Segments[i]
		lead, trail := 0, 0
		if q.Start > s.Out.Start {
			lead = q.Start - s.Out.Start
			s.Out.Start = q.Start
		}
		if q.End < s.Out.End {
			trail = s.Out.End - q.End
			s.Out.End = q.End
		}
		if s.Verbatim {
			s.Src.Start += lead
			s.Src.End -= trail
		}
		out = append(out, s)
	}
	return out
}

// Regions returns one box per contiguous run of q that shares a page and a
// line.
//
// A word rejoined across a line break therefore yields two boxes rather than
// one box covering both lines and everything between them, which is the
// difference between a highlight a reviewer can read and a highlight that
// covers half the page. A span with no geometry behind it yields no regions:
// nil means the position is unknown, never that the value is not on the page
// (docs/adr/0015-provenance.md).
func (r *Result) Regions(q Span) []Region {
	var out []Region
	page, lineNo := 0, -1
	for _, s := range r.Locate(q) {
		if s.Box == nil || s.Inserted() {
			continue
		}
		if len(out) > 0 && s.Page == page && s.Line == lineNo {
			out[len(out)-1].Box = out[len(out)-1].Box.Union(*s.Box)
			continue
		}
		out = append(out, Region{Page: s.Page, Box: *s.Box})
		page, lineNo = s.Page, s.Line
	}
	return out
}

// PageAt returns the 1-based page an output offset belongs to, or 0 if it
// belongs to none.
func (r *Result) PageAt(offset int) int {
	for _, p := range r.Pages {
		if offset >= p.Marker.Start && offset < p.Body.End {
			return p.Page
		}
	}
	return 0
}

// Inserted reports whether any byte of q is text ovrin added rather than text
// the document contained. Grounding uses it to reject a match that reaches
// into a page marker.
func (r *Result) Inserted(q Span) bool {
	for _, s := range r.Locate(q) {
		if s.Inserted() {
			return true
		}
	}
	return false
}
