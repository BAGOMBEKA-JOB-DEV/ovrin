package pdf

import (
	"fmt"
	"unicode/utf16"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// Page is one page's text layer, with the measurements that decide whether it
// is worth using.
//
// The two travel together on purpose. A caller that takes the words without
// looking at the numbers is the caller that ships a broken ToUnicode table's
// output as though it were text, which is the failure this package is written
// to prevent (docs/pipeline.md stage 2).
type Page struct {
	// Content is the page in the shape internal/normalise consumes: words
	// with boxes in points, origin top left.
	Content normalise.Page

	// Stats is what the usability heuristic measures.
	Stats Stats
}

// Stats are the measurements stage 2's usability heuristic runs on.
//
// They are counts rather than ratios so that a caller can aggregate them
// across pages, and so that "nine of nine hundred" and "one of one hundred"
// stay distinguishable.
type Stats struct {
	// Chars is how many character codes the page's content streams showed,
	// including the ones that decoded to nothing.
	Chars int

	// Replacement is how many U+FFFD characters the decoded text contains.
	// They come from a ToUnicode table that mapped a code to the replacement
	// character, which is a font subsetter's way of saying it did not know.
	Replacement int

	// Undecodable is how many codes mapped to no character at all: no
	// ToUnicode entry, and an encoding with no glyph name for the code.
	Undecodable int

	// WidthPt and HeightPt are the page size in points, after rotation.
	WidthPt  float64
	HeightPt float64
}

// pointsPerInch converts the page size to the unit the density threshold is
// expressed in.
const pointsPerInch = 72.0

// AreaSqIn returns the page area in square inches, or 0 when the page
// declared no size.
func (s Stats) AreaSqIn() float64 {
	if s.WidthPt <= 0 || s.HeightPt <= 0 {
		return 0
	}
	return (s.WidthPt / pointsPerInch) * (s.HeightPt / pointsPerInch)
}

// Density returns characters per square inch, or 0 when the page declared no
// size — in which case the density check is skipped rather than run against a
// guess.
func (s Stats) Density() float64 {
	area := s.AreaSqIn()
	if area <= 0 {
		return 0
	}
	return float64(s.Chars) / area
}

// ReplacementRatio returns the proportion of characters that are U+FFFD. An
// empty page has none, which is why it is the density check and not this one
// that rejects a blank page.
func (s Stats) ReplacementRatio() float64 {
	if s.Chars <= 0 {
		return 0
	}
	return float64(s.Replacement) / float64(s.Chars)
}

// DecodableRatio returns the proportion of character codes that mapped to a
// character through a ToUnicode entry or a known encoding.
func (s Stats) DecodableRatio() float64 {
	if s.Chars <= 0 {
		return 0
	}
	return float64(s.Chars-s.Undecodable) / float64(s.Chars)
}

// Thresholds are the three tests a page's text layer must pass to be used
// rather than sent to OCR.
//
// They are judgement rather than measurement and will be tuned against the
// evaluation corpus; the defaults are the ones in docs/pipeline.md stage 2,
// and the root package's options express a change relative to them.
//
// A non-positive field means "use the default", for the same reason
// detect.Limits works that way: a threshold of zero would be a configuration
// mistake and never an intent.
type Thresholds struct {
	// MinTextDensity is the fewest characters per square inch a page may
	// have. It is what distinguishes a scanned page carrying a stray label
	// from a page of text.
	MinTextDensity float64

	// MaxReplacementRatio is the most U+FFFD a page's text may contain.
	MaxReplacementRatio float64

	// MinDecodableRatio is the fewest characters that must map through a
	// ToUnicode entry or a standard encoding. It is the one that catches a
	// subset font with a custom encoding and no ToUnicode table — the case
	// that produces text which is the right shape and the wrong letters.
	MinDecodableRatio float64
}

// The defaults of docs/pipeline.md stage 2.
const (
	defaultMinTextDensity      = 0.5
	defaultMaxReplacementRatio = 0.02
	defaultMinDecodableRatio   = 0.90
)

// DefaultThresholds returns the thresholds an unconfigured run uses.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinTextDensity:      defaultMinTextDensity,
		MaxReplacementRatio: defaultMaxReplacementRatio,
		MinDecodableRatio:   defaultMinDecodableRatio,
	}
}

// Normalised returns t with every non-positive field replaced by its default.
// It is idempotent, and it is called by [Page.Unusable] so there is no path on
// which an unset threshold becomes no threshold.
func (t Thresholds) Normalised() Thresholds {
	d := DefaultThresholds()
	if t.MinTextDensity <= 0 {
		t.MinTextDensity = d.MinTextDensity
	}
	if t.MaxReplacementRatio <= 0 {
		t.MaxReplacementRatio = d.MaxReplacementRatio
	}
	if t.MinDecodableRatio <= 0 {
		t.MinDecodableRatio = d.MinDecodableRatio
	}
	return t
}

// Unusable reports why the page's text layer should not be used, or nil when
// it should.
//
// The error wraps [ErrNoTextLayer] and names the check that failed and ovrin's
// own threshold for it. It does not carry the measured value: a caller that
// wants the numbers has [Page.Stats], and an error goes in a log
// (docs/rules.md §7.5).
func (p Page) Unusable(t Thresholds) error {
	t = t.Normalised()
	s := p.Stats
	n := p.Content.Number
	switch {
	case s.Chars == 0:
		return &Error{Op: "acquire", Page: n, Detail: "page shows no characters", Err: ErrNoTextLayer}
	case s.AreaSqIn() > 0 && s.Density() < t.MinTextDensity:
		return &Error{Op: "acquire", Page: n, Err: ErrNoTextLayer,
			Detail: fmt.Sprintf("character density below %g per square inch", t.MinTextDensity)}
	case s.ReplacementRatio() > t.MaxReplacementRatio:
		return &Error{Op: "acquire", Page: n, Err: ErrNoTextLayer,
			Detail: fmt.Sprintf("replacement characters above %g of the page", t.MaxReplacementRatio)}
	case s.DecodableRatio() < t.MinDecodableRatio:
		return &Error{Op: "acquire", Page: n, Err: ErrNoTextLayer,
			Detail: fmt.Sprintf("decodable characters below %g of the page", t.MinDecodableRatio)}
	}
	return nil
}

// Usable reports whether the page's text layer passes every threshold.
func (p Page) Usable(t Thresholds) bool { return p.Unusable(t) == nil }

// pageEntry is one page of the tree, with its inherited attributes already
// resolved.
//
// Inheritance is resolved during the walk rather than looked up per page,
// because the parent chain is where a cycle lives: walking up from a page to
// find its Resources is a second recursion over an attacker-controlled graph,
// and one is enough.
type pageEntry struct {
	dict      Dict
	resources Dict
	media     [4]float64
	hasMedia  bool
	rotate    int
}

// findPages walks the page tree, flattening it into a list.
//
// The visited set is what makes a cyclic /Kids terminate, and the depth budget
// is what makes a page tree thousands deep run out of budget rather than out
// of stack. Both are needed: one answers a cycle, the other answers a chain
// (docs/threat-model.md T2).
func (d *Doc) findPages() error {
	root, _ := d.resolveShallow(d.trailer["Root"]).(Dict)
	var start Object
	if root != nil {
		start = root["Pages"]
	}
	visited := map[Ref]bool{}
	inherited := pageEntry{}
	if err := d.walkPages(start, inherited, visited, d.lim.Depth()); err != nil {
		return err
	}
	if len(d.pages) == 0 {
		// A catalog that does not lead to pages is common in damaged files.
		// The page objects are still there, and a scan finds them.
		d.scanPages()
	}
	if len(d.pages) == 0 {
		return malformed("page", 0, "document has no pages")
	}
	return nil
}

// walkPages is the recursive half of findPages.
func (d *Doc) walkPages(o Object, in pageEntry, visited map[Ref]bool, dp detect.Depth) error {
	dp, err := dp.Descend()
	if err != nil {
		return err
	}
	if ref, ok := o.(Ref); ok {
		if visited[ref] {
			return nil
		}
		visited[ref] = true
	}
	node, ok := d.resolve(o, dp).(Dict)
	if !ok {
		return nil
	}
	cur := d.inherit(node, in, dp)
	kids, hasKids := d.resolve(node["Kids"], dp).(Array)
	kind, _ := toName(d.resolve(node["Type"], dp))
	if !hasKids || kind == "Page" {
		if err := d.lim.CheckPages(len(d.pages) + 1); err != nil {
			return err
		}
		cur.dict = node
		d.pages = append(d.pages, cur)
		return nil
	}
	for _, kid := range kids {
		if err := d.walkPages(kid, cur, visited, dp); err != nil {
			return err
		}
	}
	return nil
}

// inherit layers a node's own attributes over the ones it inherited.
func (d *Doc) inherit(node Dict, in pageEntry, dp detect.Depth) pageEntry {
	out := in
	if r, ok := d.resolve(node["Resources"], dp).(Dict); ok {
		out.resources = r
	}
	if mb, ok := d.resolve(node["MediaBox"], dp).(Array); ok && len(mb) >= 4 {
		var box [4]float64
		good := true
		for i := 0; i < 4; i++ {
			v, ok := toFloat(d.resolve(mb[i], dp))
			if !ok {
				good = false
				break
			}
			box[i] = v
		}
		if good {
			out.media, out.hasMedia = box, true
		}
	}
	if v, ok := toInt(d.resolve(node["Rotate"], dp)); ok {
		out.rotate = int(((v%360)+360)%360) / 90 * 90
	}
	return out
}

// scanPages finds page objects directly, for a document whose page tree does
// not lead anywhere.
func (d *Doc) scanPages() {
	d.buildScan()
	nums := make([]int, 0, len(d.scan))
	for num := range d.scan {
		nums = append(nums, num)
	}
	// Object number order is not page order, but it is the order the file was
	// written in and it is very nearly always the same thing. Sorting matters
	// more than being right about the exception: without it the page order
	// would be a map iteration, which is a different order every run.
	sortInts(nums)
	for _, num := range nums {
		dict, ok := d.object(num, d.lim.Depth()).(Dict)
		if !ok {
			continue
		}
		if kind, _ := toName(dict["Type"]); kind != "Page" {
			continue
		}
		if d.lim.CheckPages(len(d.pages)+1) != nil {
			return
		}
		e := d.inherit(dict, pageEntry{}, d.lim.Depth())
		e.dict = dict
		d.pages = append(d.pages, e)
	}
}

// sortInts sorts in place. It is here rather than from the sort package
// because it is four lines and the slice is a page list.
func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// defaultMediaBox is US Letter, which is what a page with no MediaBox
// anywhere in its ancestry is displayed at.
var defaultMediaBox = [4]float64{0, 0, 612, 792}

// Page extracts the text layer of page n, which is 1-based.
//
// It returns a page even when the text layer is useless: the caller decides
// with [Page.Unusable], because "this page needs OCR" is a decision the
// pipeline makes and not one this package should make on its behalf
// (docs/adr/0012-text-first-ocr-on-demand.md).
//
// It fails on a limit, and on a page whose structure cannot be read at all. A
// content stream that is partly unreadable is not a failure: what could be
// read is returned, and the usability heuristic decides whether it is enough.
func (d *Doc) Page(n int) (page Page, err error) {
	// A panic here is a bug in this package and is reported as one. It is
	// converted rather than propagated because a malformed document must not
	// be able to take down the calling service, and because a crash tells the
	// caller nothing they can act on (docs/threat-model.md T3).
	defer func() {
		if r := recover(); r != nil {
			page = Page{}
			err = malformedPage("content", n, "parser panicked")
		}
	}()
	if n < 1 || n > len(d.pages) {
		return Page{}, malformedPage("page", n, "page number out of range")
	}
	e := d.pages[n-1]
	box := e.media
	if !e.hasMedia {
		box = defaultMediaBox
	}
	x0, x1 := minMax(box[0], box[2])
	y0, y1 := minMax(box[1], box[3])
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		x0, y0, x1, y1 = 0, 0, defaultMediaBox[2], defaultMediaBox[3]
		w, h = x1-x0, y1-y0
	}
	if err := d.lim.CheckPixels(int(w), int(h)); err != nil {
		return Page{}, err
	}

	dp := d.lim.Depth()
	content, cerr := d.concatContents(e.dict["Contents"], dp)
	if cerr != nil {
		d.note(cerr)
	}
	ip := newInterp(d, n)
	ip.runContent(content, e.resources, dp)
	ip.flushWord()
	if ip.err != nil {
		return Page{}, ip.err
	}
	if d.err != nil {
		return Page{}, d.err
	}

	pw, ph := w, h
	if e.rotate == 90 || e.rotate == 270 {
		pw, ph = h, w
	}
	out := Page{
		Content: normalise.Page{Number: n, Width: pw, Height: ph},
		Stats: Stats{
			Chars:       ip.chars,
			Replacement: ip.replacement,
			Undecodable: ip.undecodable,
			WidthPt:     pw,
			HeightPt:    ph,
		},
	}
	out.Content.Words = make([]normalise.Word, 0, len(ip.runs))
	for _, r := range ip.runs {
		out.Content.Words = append(out.Content.Words, normalise.Word{
			Text: r.text,
			Box:  toTopLeft(r, x0, y0, x1, y1, e.rotate),
			// A text layer is the characters themselves rather than a
			// reading of them, so there is nothing to be less than certain
			// about. The scorer averages this over the words backing a value
			// and a text-layer page contributes 1 to that average
			// (docs/confidence.md).
			Confidence: 1,
			Line:       r.line,
		})
	}
	return out, nil
}

// minMax orders two numbers, because a MediaBox may be written with either
// corner first and a negative width would invert every box on the page.
func minMax(a, b float64) (float64, float64) {
	if a > b {
		return b, a
	}
	return a, b
}

// toTopLeft converts a run's box from PDF user space to the top-left origin
// internal/normalise works in, applying the page's /Rotate.
//
// The origin is neither PDF's nor an image format's. One convention had to be
// chosen and every reading normalises to it, which is what lets a provenance
// rectangle from a text layer and one from an OCR engine mean the same thing
// (docs/adr/0015-provenance.md).
func toTopLeft(r run, x0, y0, x1, y1 float64, rotate int) normalise.Rect {
	corner := func(x, y float64) (float64, float64) {
		switch rotate {
		case 90:
			return y - y0, x - x0
		case 180:
			return x1 - x, y - y0
		case 270:
			return y1 - y, x1 - x
		default:
			return x - x0, y1 - y
		}
	}
	ax, ay := corner(r.minX, r.minY)
	bx, by := corner(r.maxX, r.maxY)
	minX, maxX := minMax(ax, bx)
	minY, maxY := minMax(ay, by)
	return normalise.Rect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

// Metadata returns the document information dictionary as normalisation
// metadata.
//
// It is here because reading a PDF string's encoding is this package's job,
// and it is separate from the page text because metadata never enters the
// normalised stream: it is scanned for instruction-shaped language and
// reported, and carrying it into the prompt would hand an injection payload
// the one thing the design denies it (internal/normalise, docs/adr/0017).
//
// Only the six standard keys are returned. A document chooses its own keys,
// and a finding prints the key, so an arbitrary one would put document bytes
// in a log (docs/rules.md §7.5).
func (d *Doc) Metadata() []normalise.Meta {
	info, ok := d.resolveShallow(d.trailer["Info"]).(Dict)
	if !ok {
		return nil
	}
	keys := []Name{"Title", "Author", "Subject", "Keywords", "Creator", "Producer"}
	out := make([]normalise.Meta, 0, len(keys))
	for _, k := range keys {
		s, ok := d.resolveShallow(info[k]).(String)
		if !ok || len(s) == 0 {
			continue
		}
		if err := d.text.Add(int64(len(s))); err != nil {
			d.note(err)
			return out
		}
		out = append(out, normalise.Meta{Key: string(k), Value: decodeTextString(s)})
	}
	return out
}

// decodeTextString decodes a PDF text string, which is UTF-16BE when it
// carries a byte order mark and PDFDocEncoding otherwise.
func decodeTextString(s String) string {
	if len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF {
		units := make([]uint16, 0, (len(s)-2)/2)
		for i := 2; i+1 < len(s); i += 2 {
			units = append(units, uint16(s[i])<<8|uint16(s[i+1]))
		}
		return string(utf16.Decode(units))
	}
	// PDFDocEncoding agrees with StandardEncoding over the printable range
	// and with Latin-1 above it, which is what this table gives.
	out := make([]rune, 0, len(s))
	for _, b := range s {
		if r := winAnsiEncoding[b]; r != 0 {
			out = append(out, r)
		}
	}
	return string(out)
}
