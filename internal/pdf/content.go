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
}

// gstate is the graphics state that q and Q save and restore.
type gstate struct {
	ctm matrix
	ts  textState
}

// interp runs a content stream and collects positioned words.
//
// It is one struct rather than a set of functions over a state pointer because
// every operator reads or writes the same six things, and threading them would
// mean an argument list nobody reads.
type interp struct {
	doc  *Doc
	page int

	gs    gstate
	stack []gstate
	tm    matrix
	tlm   matrix

	runs []run
	line int

	// cur is the word being accumulated.
	cur    strings.Builder
	curBox [4]float64
	curHas bool
	lastY  float64

	chars       int
	replacement int
	undecodable int

	fonts map[Name]*font

	// err is the first limit failure. Extraction stops at it: a limit failure
	// means an attacker is spending our memory and continuing spends more.
	err error
}

// newInterp returns an interpreter for one page.
func newInterp(d *Doc, page int) *interp {
	return &interp{
		doc:   d,
		page:  page,
		gs:    gstate{ctm: identityMatrix, ts: textState{hscale: 1}},
		tm:    identityMatrix,
		tlm:   identityMatrix,
		fonts: map[Name]*font{},
	}
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
		o, perr := l.object(dp)
		if perr != nil {
			// The lexer always advances past what it could not read, so
			// dropping the operands and carrying on terminates and recovers
			// the rest of the page.
			ops = ops[:0]
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
// The set is exactly the text-showing, text-positioning and text-state
// operators, plus the four that move the coordinate system underneath them.
// Everything else — colour, paths, shading, images — is ignored rather than
// implemented, which is the scope decision of ADR-0011 expressed as a switch
// statement.
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
	case "Tr", "Tk":
		// Rendering mode is read and ignored on purpose. Mode 3 is invisible
		// text, which is exactly the text layer a scanner writes under a
		// page image — dropping it would discard the searchable layer of
		// every scanned PDF.
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
		skipInlineImage(l)
	}
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
	if sub, ok := toName(ip.doc.resolve(st.Dict["Subtype"], dp)); !ok || sub != "Form" {
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
	savedFonts := ip.fonts
	if r, ok := ip.doc.resolve(st.Dict["Resources"], dp).(Dict); ok {
		sub = r
		// The font cache is keyed by resource name, so a form with its own
		// resources must not read the page's cache: /F1 means something
		// different in each.
		ip.fonts = map[Name]*font{}
	}
	ip.runContent(data, sub, dp)
	ip.fonts = savedFonts
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
			ip.addGlyph(g, trm)
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
func (ip *interp) addGlyph(g glyph, trm matrix) {
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
		line: ip.line,
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
