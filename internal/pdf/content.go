package pdf

import (
	"bytes"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// matrix is a PDF transformation matrix, in the order [a b c d e f] that the
// file writes it.
//
// PDF matrices are 3×3 with a fixed third column, so six numbers is the whole
// of one. Keeping the file's own order means a `cm` operator's operands are
// the matrix, with nothing to transpose and so nothing to transpose wrongly.
type matrix [6]float64

// identityMatrix is the matrix that changes nothing.
var identityMatrix = matrix{1, 0, 0, 1, 0, 0}

// mul returns m followed by n.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// apply transforms a point.
func (m matrix) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// The glyph box this package assumes, in fractions of an em.
//
// The real extents are in the font program, which this package does not read.
// These are the conventional Latin values and they are a deliberate
// approximation: being wrong about them moves the top and bottom edges of a
// highlight by a point or two, where reading the font program to be exact
// would mean parsing TrueType and CFF — a renderer's work, for a rectangle
// nobody measures (docs/adr/0011-pdf-text-extraction.md).
const (
	glyphAscent  = 0.88
	glyphDescent = -0.22
)

// wordGapEm is the gap, as a fraction of the font size, that separates two
// words when no space character was shown.
//
// Justified text is set by moving the pen rather than by showing spaces, so
// without this every line of a justified paragraph would be one word. The
// value sits above the kerning adjustments a generator emits between letters
// — those are a few hundredths of an em — and below the space of any face
// this package will meet.
const wordGapEm = 0.16

// maxWordsPerPage bounds how many runs one page may produce.
//
// The text-byte counter already bounds the characters; this bounds the
// per-word overhead, which a content stream of nothing but one-character
// shows would otherwise multiply by the ceiling
// (docs/adr/0020-resource-limits.md).
const maxWordsPerPage = 1 << 18

// maxOperands bounds the operand stack, so a content stream that is a million
// numbers and no operator does not accumulate them.
const maxOperands = 64

// maxGraphicsStack bounds how deep q may nest.
const maxGraphicsStack = 256

// run is one positioned word in PDF device space, before the page's own
// coordinate system is applied.
type run struct {
	text                   string
	minX, minY, maxX, maxY float64
	line                   int

	// colour is the colour the run was painted in, and colourKnown says
	// whether it is one this package worked out. Unknown is reported as no
	// colour rather than as a default, because the only consumer compares it
	// with the paper (internal/normalise, Word.Colour).
	colour      colour
	colourKnown bool
}

// textState is the part of the graphics state that survives BT and ET.
type textState struct {
	font      *font
	size      float64
	charSpace float64
	wordSpace float64
	hscale    float64
	leading   float64
	rise      float64

	// render is the text rendering mode set by Tr. It decides whether a
	// glyph is painted with the fill colour, the stroke colour, both or
	// neither, which is the difference between reading the colour of the
	// text and reading a colour nothing was drawn in.
	render int
}

// gstate is the graphics state that q and Q save and restore.
//
// Colour is in here rather than beside it because that is where the
// specification puts it, and because a q that saves the matrix but not the
// colour would report the wrong colour for every word after the first Q — the
// exact shape of bug that makes a background-colour detector untrustworthy.
type gstate struct {
	ctm matrix
	ts  textState

	// The non-stroking and stroking colours, and the spaces they are
	// components of. A PDF page starts in DeviceGray at black.
	fillSpace   *colourSpace
	strokeSpace *colourSpace
	fill        colour
	stroke      colour
	fillKnown   bool
	strokeKnown bool

	// clip is a box round the clipping region, and clipExact says whether the
	// region is that box rather than something smaller inside it.
	clip      rect
	clipExact bool
}

// textColour returns the colour a glyph shown in this state is painted in.
//
// Mode 3 and mode 7 paint nothing at all — which is the searchable text layer
// a scanner writes under a page image, present in a large share of real
// documents — so they report no colour rather than an invisible one. Modes 2
// and 6 paint the glyph twice, and are only hidden when both colours are
// hidden, so they answer only when the two agree.
func (g *gstate) textColour() (colour, bool) {
	mode := g.ts.render
	if mode < 0 || mode > 7 {
		mode = 0
	}
	switch mode & 3 {
	case 0:
		return g.fill, g.fillKnown
	case 1:
		return g.stroke, g.strokeKnown
	case 2:
		if g.fillKnown && g.strokeKnown && g.fill == g.stroke {
			return g.fill, true
		}
		return colour{}, false
	}
	return colour{}, false
}

// interp runs a content stream and collects positioned words.
//
// It is one struct rather than a set of functions over a state pointer because
// every operator reads or writes the same six things, and threading them would
// mean an argument list nobody reads.
type interp struct {
	doc *Doc

	gs    gstate
	stack []gstate
	tm    matrix
	tlm   matrix

	runs []run
	line int

	// cur is the word being accumulated.
	cur       strings.Builder
	curBox    [4]float64
	curHas    bool
	curColour colour
	curKnown  bool
	lastY     float64

	chars       int
	replacement int
	undecodable int

	fonts  map[Name]*font
	spaces map[Name]*colourSpace

	// page is the media box in user space, which is what a fill is measured
	// against when deciding whether it painted the paper.
	page rect

	// The current path and whether a W is waiting for the operator that ends
	// it.
	path        pathState
	pendingClip bool

	// What has been worked out about the page's background: a colour taken
	// from a full-page fill, a flag saying the page was painted by something
	// whose colour could not be taken, and the area of the smaller such
	// paint. See interp.background.
	bg        colour
	bgKnown   bool
	bgUnknown bool
	painted   float64

	// err is the first limit failure. Extraction stops at it: a limit failure
	// means an attacker is spending our memory and continuing spends more.
	err error
}

// newInterp returns an interpreter for one page whose media box is page, in
// user space.
//
// The initial graphics state is the specification's: DeviceGray at black, an
// identity matrix, and a clip that is the page itself.
func newInterp(d *Doc, page rect) *interp {
	ip := &interp{
		doc: d,
		gs: gstate{
			ctm:         identityMatrix,
			ts:          textState{hscale: 1},
			fillSpace:   graySpace,
			strokeSpace: graySpace,
			fillKnown:   true,
			strokeKnown: true,
			clip:        page,
			clipExact:   true,
		},
		tm:     identityMatrix,
		tlm:    identityMatrix,
		fonts:  map[Name]*font{},
		spaces: map[Name]*colourSpace{},
		page:   page,
	}
	ip.resetPath()
	return ip
}

// runContent interprets a content stream against a resource dictionary.
//
// dp is spent by nesting: a form XObject drawn by a form XObject drawn by a
// page is three levels, and a form that draws itself runs out of budget rather
// than out of stack (docs/threat-model.md T2).
func (ip *interp) runContent(content []byte, res Dict, dp detect.Depth) {
	dp, err := dp.Descend()
	if err != nil {
		ip.fail(err)
		return
	}
	l := &lexer{data: content}
	var ops []Object
	for !l.atEOF() {
		if ip.err != nil {
			return
		}
		before := l.pos
		o, perr := l.object(dp)
		if perr != nil {
			// Dropping the operands and carrying on recovers the rest of a
			// page whose content stream has one bad object in it. It is only
			// safe while the lexer advances: an object that failed before
			// consuming anything — a depth budget already spent — would
			// otherwise be retried for ever, so that case stops instead.
			ops = ops[:0]
			if l.pos <= before {
				return
			}
			continue
		}
		op, isOp := o.(operator)
		if !isOp {
			if len(ops) < maxOperands {
				ops = append(ops, o)
			}
			continue
		}
		ip.do(op, ops, res, l, dp)
		ops = ops[:0]
	}
}

// fail records the first limit failure.
func (ip *interp) fail(err error) {
	if ip.err == nil {
		ip.err = err
	}
}

// num returns operand i counted from the end, or zero.
func num(ops []Object, fromEnd int) float64 {
	i := len(ops) - 1 - fromEnd
	if i < 0 || i >= len(ops) {
		return 0
	}
	v, _ := toFloat(ops[i])
	return v
}

// do executes one operator.
//
// The set is the text-showing, text-positioning and text-state operators, the
// four that move the coordinate system underneath them, and the colour and
// path operators that say what colour the text and the paper are. Everything
// else — shading detail, transparency, images beyond their extent — is ignored
// rather than implemented, which is the scope decision of ADR-0011 expressed
// as a switch statement. Colour is in scope because ADR-0017's fourth
// mitigation is text painted in the colour of the page, and a reader that
// cannot see colour cannot report it.
func (ip *interp) do(op operator, ops []Object, res Dict, l *lexer, dp detect.Depth) {
	switch op {
	case "q":
		// The graphics stack is bounded because q is one byte and a content
		// stream of nothing but q is a cheap way to ask for memory.
		if len(ip.stack) < maxGraphicsStack {
			ip.stack = append(ip.stack, ip.gs)
		}
	case "Q":
		if n := len(ip.stack); n > 0 {
			ip.gs = ip.stack[n-1]
			ip.stack = ip.stack[:n-1]
		}
	case "cm":
		if len(ops) >= 6 {
			m := matrix{num(ops, 5), num(ops, 4), num(ops, 3), num(ops, 2), num(ops, 1), num(ops, 0)}
			ip.gs.ctm = m.mul(ip.gs.ctm)
		}
	case "g", "G", "rg", "RG", "k", "K":
		ip.setDeviceColour(op, ops)
	case "cs", "CS":
		ip.setColourSpace(op, ops, res, dp)
	case "sc", "scn", "SC", "SCN":
		ip.setColourComponents(op, ops)
	case "BT":
		ip.flushWord()
		ip.tm, ip.tlm = identityMatrix, identityMatrix
	case "ET":
		ip.flushWord()
	case "Tf":
		if len(ops) >= 2 {
			if n, ok := toName(ops[len(ops)-2]); ok {
				ip.gs.ts.font = ip.lookupFont(n, res, dp)
			}
			ip.gs.ts.size = num(ops, 0)
		}
	case "Tc":
		ip.gs.ts.charSpace = num(ops, 0)
	case "Tw":
		ip.gs.ts.wordSpace = num(ops, 0)
	case "Tz":
		if v := num(ops, 0); v != 0 {
			ip.gs.ts.hscale = v / 100
		}
	case "TL":
		ip.gs.ts.leading = num(ops, 0)
	case "Ts":
		ip.gs.ts.rise = num(ops, 0)
	case "Tr":
		// The mode is recorded but never used to drop text. Mode 3 is
		// invisible text, which is exactly the text layer a scanner writes
		// under a page image — dropping it would discard the searchable layer
		// of every scanned PDF. What it decides is which colour the glyph is
		// painted in, and whether it is painted at all; see
		// [gstate.textColour].
		ip.gs.ts.render = int(num(ops, 0))
	case "Td":
		ip.newline(num(ops, 1), num(ops, 0))
	case "TD":
		ip.gs.ts.leading = -num(ops, 0)
		ip.newline(num(ops, 1), num(ops, 0))
	case "Tm":
		if len(ops) >= 6 {
			ip.flushWord()
			ip.tlm = matrix{num(ops, 5), num(ops, 4), num(ops, 3), num(ops, 2), num(ops, 1), num(ops, 0)}
			ip.tm = ip.tlm
			ip.markLine()
		}
	case "T*":
		ip.newline(0, -ip.gs.ts.leading)
	case "Tj":
		if s, ok := lastString(ops); ok {
			ip.show(s)
		}
	case "'":
		ip.newline(0, -ip.gs.ts.leading)
		if s, ok := lastString(ops); ok {
			ip.show(s)
		}
	case "\"":
		if len(ops) >= 3 {
			ip.gs.ts.wordSpace = num(ops, 2)
			ip.gs.ts.charSpace = num(ops, 1)
		}
		ip.newline(0, -ip.gs.ts.leading)
		if s, ok := lastString(ops); ok {
			ip.show(s)
		}
	case "TJ":
		if len(ops) == 0 {
			return
		}
		arr, ok := ops[len(ops)-1].(Array)
		if !ok {
			return
		}
		for _, e := range arr {
			switch v := e.(type) {
			case String:
				ip.show(v)
			case Integer, Real:
				adj, _ := toFloat(v)
				ip.adjust(adj)
			}
		}
	case "Do":
		if n, ok := lastName(ops); ok {
			ip.doXObject(n, res, dp)
		}
	case "BI":
		// An inline image's data is arbitrary bytes that would otherwise be
		// lexed as syntax. Skipping to EI is not an optimisation; it is what
		// keeps a binary blob from being read as a content stream.
		//
		// Its extent is still recorded: an inline image is drawn in the unit
		// square like any other, and one covering the page is a background
		// this package cannot name.
		ip.opaquePaintSolid(ip.ctmBox(unitSquare))
		skipInlineImage(l)
	default:
		// The path, painting, clipping and shading operators. They are in
		// their own file because they are about the paper rather than the
		// text; see internal/pdf/paint.go.
		ip.paint(op, ops)
	}
}

// setDeviceColour handles g, G, rg, RG, k and K, which set a colour and its
// space in one operator.
func (ip *interp) setDeviceColour(op operator, ops []Object) {
	var cs *colourSpace
	switch op {
	case "g", "G":
		cs = graySpace
	case "rg", "RG":
		cs = rgbSpace
	default:
		cs = cmykSpace
	}
	if len(ops) < cs.n {
		// A truncated operator sets nothing. Filling the missing operands in
		// with zeros would turn a malformed `rg` into black.
		return
	}
	v := make([]float64, cs.n)
	for i := range v {
		v[i] = num(ops, cs.n-1-i)
	}
	c, ok := cs.colour(v)
	if !ok {
		return
	}
	if op == "G" || op == "RG" || op == "K" {
		ip.gs.strokeSpace, ip.gs.stroke, ip.gs.strokeKnown = cs, c, true
		return
	}
	ip.gs.fillSpace, ip.gs.fill, ip.gs.fillKnown = cs, c, true
}

// setColourSpace handles cs and CS, which select a space and reset the colour
// to that space's initial value.
//
// The resolved space is cached by resource name for the page, as fonts are: a
// content stream names /CS0 once per graphic and resolving an ICCBased space
// walks the object graph every time.
func (ip *interp) setColourSpace(op operator, ops []Object, res Dict, dp detect.Depth) {
	n, ok := lastName(ops)
	if !ok {
		return
	}
	cs, cached := ip.spaces[n]
	if !cached {
		cs = ip.doc.colourSpaceFor(n, res, dp)
		ip.spaces[n] = cs
	}
	c, known := cs.initial()
	if op == "CS" {
		ip.gs.strokeSpace, ip.gs.stroke, ip.gs.strokeKnown = cs, c, known
		return
	}
	ip.gs.fillSpace, ip.gs.fill, ip.gs.fillKnown = cs, c, known
}

// setColourComponents handles sc, scn, SC and SCN, which set a colour in the
// space already selected.
//
// A trailing name operand is a pattern, whose colour is whatever its own
// content stream paints. That is not followed, so the colour becomes unknown —
// which is the honest answer and the one that skips the check rather than
// firing it wrongly.
func (ip *interp) setColourComponents(op operator, ops []Object) {
	stroking := op == "SC" || op == "SCN"
	cs := ip.gs.fillSpace
	if stroking {
		cs = ip.gs.strokeSpace
	}
	c, known := colour{}, false
	if !isPatternOperand(ops) {
		v := make([]float64, 0, len(ops))
		for _, o := range ops {
			if f, ok := toFloat(o); ok {
				v = append(v, f)
			}
		}
		// The components are the last n operands, not the first: an operand
		// stack carrying the residue of an operator this package ignored
		// would otherwise contribute its numbers to the colour.
		if cs != nil && cs.n > 0 && len(v) > cs.n {
			v = v[len(v)-cs.n:]
		}
		c, known = cs.colour(v)
	}
	if stroking {
		ip.gs.stroke, ip.gs.strokeKnown = c, known
		return
	}
	ip.gs.fill, ip.gs.fillKnown = c, known
}

// lastString returns the last string operand.
func lastString(ops []Object) (String, bool) {
	for i := len(ops) - 1; i >= 0; i-- {
		if s, ok := ops[i].(String); ok {
			return s, true
		}
	}
	return nil, false
}

// isPatternOperand reports whether an scn operand list ends in a pattern name.
// Only the last operand is looked at, because that is the only position the
// specification puts one in and because an earlier name is residue.
func isPatternOperand(ops []Object) bool {
	if len(ops) == 0 {
		return false
	}
	_, ok := ops[len(ops)-1].(Name)
	return ok
}

// lastName returns the last name operand.
func lastName(ops []Object) (Name, bool) {
	for i := len(ops) - 1; i >= 0; i-- {
		if n, ok := ops[i].(Name); ok {
			return n, true
		}
	}
	return "", false
}

// skipInlineImage advances the lexer past an inline image's data to its EI
// keyword.
//
// EI is found as a delimited token rather than as a substring, because the
// two bytes occur inside compressed image data often enough that a substring
// search resynchronises in the middle of the image and reads the rest of it
// as operators.
func skipInlineImage(l *lexer) {
	for i := l.pos; i+1 < len(l.data); i++ {
		if l.data[i] != 'E' || l.data[i+1] != 'I' {
			continue
		}
		if i > 0 && !isSpace(l.data[i-1]) {
			continue
		}
		if i+2 < len(l.data) && isRegular(l.data[i+2]) {
			continue
		}
		l.pos = i + 2
		return
	}
	l.pos = len(l.data)
}

// lookupFont resolves a font name against the resource dictionary, caching
// the result for the page.
func (ip *interp) lookupFont(n Name, res Dict, dp detect.Depth) *font {
	if f, ok := ip.fonts[n]; ok {
		return f
	}
	var dict Dict
	if fonts, ok := ip.doc.resolve(res["Font"], dp).(Dict); ok {
		dict, _ = ip.doc.resolve(fonts[n], dp).(Dict)
	}
	// A missing font dictionary is not fatal. The codes are still there and
	// are read through StandardEncoding, which recovers the text of a
	// document whose resources are damaged.
	f := ip.doc.loadFont(dict, dp)
	ip.fonts[n] = f
	return f
}

// doXObject draws a form XObject by interpreting its content stream.
//
// Form XObjects matter here because generators put whole page bodies in them:
// a header, a table, an entire imposed page. Ignoring them would produce a
// blank page for a document that displays text.
func (ip *interp) doXObject(n Name, res Dict, dp detect.Depth) {
	xobjs, ok := ip.doc.resolve(res["XObject"], dp).(Dict)
	if !ok {
		return
	}
	st, ok := ip.doc.resolve(xobjs[n], dp).(*Stream)
	if !ok {
		return
	}
	kind, _ := toName(ip.doc.resolve(st.Dict["Subtype"], dp))
	if kind == "Image" {
		// The image itself is never decoded — that is a renderer's work and
		// most of these are in filters this package refuses. Its extent is
		// cheap and is what says whether the paper underneath a word is
		// something this package can name.
		ip.opaquePaintSolid(ip.ctmBox(unitSquare))
		return
	}
	if kind != "Form" {
		return
	}
	data, err := st.Decode()
	if err != nil {
		// An image filter on a form is a malformed document; an unreadable
		// form is a missing part of the page, not a failed page.
		ip.doc.note(err)
		return
	}
	saved, savedTm, savedTlm := ip.gs, ip.tm, ip.tlm
	if m, ok := ip.doc.resolve(st.Dict["Matrix"], dp).(Array); ok && len(m) >= 6 {
		var fm matrix
		for i := 0; i < 6; i++ {
			fm[i], _ = toFloat(ip.doc.resolve(m[i], dp))
		}
		ip.gs.ctm = fm.mul(ip.gs.ctm)
	}
	sub := res
	savedFonts, savedSpaces := ip.fonts, ip.spaces
	savedPath, savedClipPending := ip.path, ip.pendingClip
	if r, ok := ip.doc.resolve(st.Dict["Resources"], dp).(Dict); ok {
		sub = r
		// The font and colour-space caches are keyed by resource name, so a
		// form with its own resources must not read the page's: /F1 and /CS0
		// mean something different in each.
		ip.fonts = map[Name]*font{}
		ip.spaces = map[Name]*colourSpace{}
	}
	// A form begins with no current path, and whatever it leaves half-built
	// does not become part of the caller's.
	ip.resetPath()
	ip.pendingClip = false
	ip.runContent(data, sub, dp)
	ip.fonts, ip.spaces = savedFonts, savedSpaces
	ip.path, ip.pendingClip = savedPath, savedClipPending
	ip.gs, ip.tm, ip.tlm = saved, savedTm, savedTlm
}

// newline starts a new text line, tx and ty from the current line matrix.
func (ip *interp) newline(tx, ty float64) {
	ip.flushWord()
	ip.tlm = matrix{1, 0, 0, 1, tx, ty}.mul(ip.tlm)
	ip.tm = ip.tlm
	ip.markLine()
}

// markLine advances the line counter when the baseline has actually moved.
//
// A generator that positions every word with its own Tm would otherwise put
// each word on a line of its own, and normalisation trusts a reading's line
// grouping over its geometry — so a wrong grouping here is a wrong reading
// order there (internal/normalise, Word.Line).
func (ip *interp) markLine() {
	_, y := ip.tlm.mul(ip.gs.ctm).apply(0, 0)
	size := ip.gs.ts.size
	if size <= 0 {
		size = 12
	}
	if diff := y - ip.lastY; diff > size*0.3 || diff < -size*0.3 {
		ip.line++
	}
	ip.lastY = y
}

// adjust applies a TJ number, which moves the pen without drawing.
//
// A large enough movement is a word break. That is how justified text is set:
// the spaces are pen movements, not space characters, and a reader that only
// splits on U+0020 returns each line as one word.
func (ip *interp) adjust(adj float64) {
	ts := &ip.gs.ts
	tx := -adj / 1000 * ts.size * ts.hscale
	if tx > ts.size*wordGapEm*ts.hscale {
		ip.flushWord()
	}
	ip.tm = matrix{1, 0, 0, 1, tx, 0}.mul(ip.tm)
}

// show draws a string, accumulating its glyphs into words.
func (ip *interp) show(s String) {
	ts := &ip.gs.ts
	f := ts.font
	if f == nil {
		f = ip.doc.loadFont(nil, ip.doc.lim.Depth())
		ts.font = f
	}
	// The colour is fixed for the whole string: no operator inside a shown
	// string can change it.
	col, colKnown := ip.gs.textColour()
	for _, g := range f.decode(s) {
		if ip.err != nil {
			return
		}
		ip.chars++
		trm := matrix{ts.size * ts.hscale, 0, 0, ts.size, 0, ts.rise}.mul(ip.tm).mul(ip.gs.ctm)
		advance := (g.width*ts.size + ts.charSpace) * ts.hscale
		// Word spacing applies to the single byte 32, and to that byte only:
		// in a two-byte font the value 32 is half of some other character
		// and spacing it would move every line.
		if g.n == 1 && g.code == 32 {
			advance += ts.wordSpace * ts.hscale
		}
		switch {
		case g.text == "":
			ip.undecodable++
			// A code with no mapping is a hole in the text, not a
			// character. It is counted and skipped; writing U+FFFD here
			// would make the replacement ratio and the decodable ratio
			// measure the same thing.
			ip.flushWord()
		case isSpaceText(g.text):
			ip.flushWord()
		default:
			if !g.decoded {
				ip.undecodable++
			}
			ip.replacement += strings.Count(g.text, "�")
			ip.addGlyph(g, trm, col, colKnown)
		}
		ip.tm = matrix{1, 0, 0, 1, advance, 0}.mul(ip.tm)
	}
}

// isSpaceText reports whether a decoded glyph is nothing but white space.
func isSpaceText(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == 0xA0
	}) == ""
}

// addGlyph appends one glyph's characters and its box to the current word.
//
// col is the colour the glyph is painted in, and known says whether it is one
// this package worked out.
func (ip *interp) addGlyph(g glyph, trm matrix, col colour, known bool) {
	// A change of colour ends the word. One run of text is one colour: an
	// injected white phrase inside a black sentence is a separate run, and
	// merging the two would give the whole thing one colour that is a
	// property of neither.
	if ip.curHas && (ip.curKnown != known || (known && ip.curColour != col)) {
		ip.flushWord()
	}
	x0, y0 := trm.apply(0, glyphDescent)
	x1, y1 := trm.apply(g.width, glyphAscent)
	x2, y2 := trm.apply(0, glyphAscent)
	x3, y3 := trm.apply(g.width, glyphDescent)
	minX, maxX := min4(x0, x1, x2, x3), max4(x0, x1, x2, x3)
	minY, maxY := min4(y0, y1, y2, y3), max4(y0, y1, y2, y3)

	// A jump inside one show — a Tm is not the only way to move — ends the
	// word too, so a run of glyphs positioned individually across a line does
	// not become one box spanning the page.
	size := ip.gs.ts.size
	if ip.curHas && size > 0 && minX-ip.curBox[2] > size*wordGapEm {
		ip.flushWord()
	}
	if !ip.curHas {
		ip.curBox = [4]float64{minX, minY, maxX, maxY}
		ip.curHas = true
		ip.curColour, ip.curKnown = col, known
	} else {
		if minX < ip.curBox[0] {
			ip.curBox[0] = minX
		}
		if minY < ip.curBox[1] {
			ip.curBox[1] = minY
		}
		if maxX > ip.curBox[2] {
			ip.curBox[2] = maxX
		}
		if maxY > ip.curBox[3] {
			ip.curBox[3] = maxY
		}
	}
	ip.cur.WriteString(g.text)
}

// flushWord ends the word being accumulated and appends it to the page.
//
// The text-byte budget is charged here rather than at the end, so a page that
// would exceed it stops producing words at the moment it does rather than
// after it has built them all (docs/adr/0020-resource-limits.md).
func (ip *interp) flushWord() {
	if !ip.curHas || ip.cur.Len() == 0 {
		ip.cur.Reset()
		ip.curHas = false
		return
	}
	text := ip.cur.String()
	ip.cur.Reset()
	ip.curHas = false
	if len(ip.runs) >= maxWordsPerPage {
		ip.fail(&limitError{limit: detect.LimitTextBytes, max: maxWordsPerPage})
		return
	}
	if err := ip.doc.text.Add(int64(len(text))); err != nil {
		ip.fail(err)
		return
	}
	ip.runs = append(ip.runs, run{
		text: text,
		minX: ip.curBox[0], minY: ip.curBox[1],
		maxX: ip.curBox[2], maxY: ip.curBox[3],
		line:        ip.line,
		colour:      ip.curColour,
		colourKnown: ip.curKnown,
	})
}

// min4 and max4 pick the extremes of a transformed rectangle's four corners,
// which is what makes a rotated run's box axis-aligned rather than wrong.
func min4(a, b, c, d float64) float64 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	if d < a {
		a = d
	}
	return a
}

func max4(a, b, c, d float64) float64 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	if d > a {
		a = d
	}
	return a
}

// concatContents joins a page's content streams.
//
// /Contents may be one stream or an array of them, and the array is one
// stream conceptually — an operator may begin in one element and end in the
// next — so they are joined with a newline rather than interpreted
// separately.
func (d *Doc) concatContents(o Object, dp detect.Depth) ([]byte, error) {
	switch v := d.resolve(o, dp).(type) {
	case *Stream:
		return v.Decode()
	case Array:
		var buf bytes.Buffer
		for _, e := range v {
			st, ok := d.resolve(e, dp).(*Stream)
			if !ok {
				continue
			}
			b, err := st.Decode()
			if err != nil {
				// One unreadable stream out of six is a partly readable
				// page, which is worth more than no page.
				d.note(err)
				continue
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		return buf.Bytes(), nil
	}
	return nil, nil
}
