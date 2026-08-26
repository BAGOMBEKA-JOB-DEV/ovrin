package pdf

import (
	"unicode/utf16"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// cmap is a parsed CMap: either a ToUnicode CMap, which maps character codes
// to text, or an encoding CMap, which maps them to CIDs.
//
// One type serves both because they share a grammar and, more importantly, a
// codespace: how many bytes the next character code occupies is decided by the
// codespace ranges, and getting that wrong turns a two-byte CJK document into
// twice as many wrong characters. Splitting the type would mean writing that
// decision twice.
type cmap struct {
	spaces    []codespace
	single    map[uint32]string
	ranges    []bfrange
	cids      map[uint32]int
	cidRanges []cidrange

	// identity marks Identity-H and Identity-V, where the code is the CID and
	// codes are always two bytes.
	identity bool
}

// codespace is one range of character codes of a fixed byte length.
type codespace struct {
	n    int
	low  uint32
	high uint32
}

// bfrange maps a contiguous run of codes to text, either by incrementing the
// last code unit of dst or by indexing into a list.
type bfrange struct {
	lo, hi uint32
	dst    string
	list   []string
}

// cidrange maps a contiguous run of codes to a run of CIDs.
type cidrange struct {
	lo, hi uint32
	cid    int
}

// maxCMapEntries bounds how many single mappings one CMap may hold.
//
// A CMap is bounded by the stream ceiling, but at roughly eight bytes of input
// per mapping a stream at the ceiling would build a map of eight million
// entries. The ceiling here is comfortably above a complete two-byte encoding,
// which is the largest thing a legitimate CMap describes
// (docs/adr/0020-resource-limits.md).
const maxCMapEntries = 1 << 18

// identityCMap returns the CMap for Identity-H and Identity-V: two-byte codes
// that are their own CIDs.
func identityCMap() *cmap {
	return &cmap{
		identity: true,
		spaces:   []codespace{{n: 2, low: 0, high: 0xFFFF}},
	}
}

// parseCMap reads a CMap from decoded bytes.
//
// It never fails: a CMap it cannot understand yields the mappings it could
// read and no more, and the codes that are left unmapped are counted as
// undecodable by the caller. That is the honest outcome — a partially
// understood ToUnicode table that guesses at the rest is exactly the
// "plausible rubbish" the usability heuristic exists to catch
// (docs/pipeline.md stage 2).
func parseCMap(data []byte, dp detect.Depth) *cmap {
	c := &cmap{single: map[uint32]string{}, cids: map[uint32]int{}}
	l := &lexer{data: data}
	for !l.atEOF() {
		before := l.pos
		o, err := l.object(dp)
		if err != nil {
			// An object that failed without consuming anything — an
			// exhausted depth budget — would be retried for ever.
			if l.pos <= before {
				return c
			}
			continue
		}
		op, isOp := o.(operator)
		if !isOp {
			// Operands before a section keyword are the counts the file
			// declares, and they are deliberately dropped: each section is
			// read until its end keyword, so a count that disagrees with the
			// content cannot make the reader believe there is more of it.
			continue
		}
		switch op {
		case "begincodespacerange":
			c.readCodespaces(l, dp)
		case "beginbfchar":
			c.readBFChars(l, dp)
		case "beginbfrange":
			c.readBFRanges(l, dp)
		case "begincidchar":
			c.readCIDChars(l, dp)
		case "begincidrange":
			c.readCIDRanges(l, dp)
		}
	}
	if len(c.spaces) == 0 {
		c.spaces = c.inferCodespaces()
	}
	return c
}

// inferCodespaces guesses the code length when a CMap declares no codespace,
// which malformed generators do.
//
// The guess is made from the codes that were actually mapped rather than from
// a default, because a two-byte CMap read one byte at a time produces twice as
// many characters as the page has and every one of them is wrong.
func (c *cmap) inferCodespaces() []codespace {
	wide := false
	for code := range c.single {
		if code > 0xFF {
			wide = true
			break
		}
	}
	if !wide {
		for _, r := range c.ranges {
			if r.hi > 0xFF {
				wide = true
				break
			}
		}
	}
	if wide {
		return []codespace{{n: 2, low: 0, high: 0xFFFF}}
	}
	return []codespace{{n: 1, low: 0, high: 0xFF}}
}

// section reads objects up to the matching end operator, calling fn with each
// object. It bounds nothing itself: the input is a decoded stream, which is
// already bounded.
func section(l *lexer, dp detect.Depth, end operator, fn func(Object) bool) {
	for !l.atEOF() {
		before := l.pos
		o, err := l.object(dp)
		if err != nil || l.pos <= before {
			return
		}
		if op, ok := o.(operator); ok {
			if op == end {
				return
			}
			continue
		}
		if !fn(o) {
			return
		}
	}
}

// beCode reads a big-endian code out of a CMap string. Codes are at most four
// bytes; a longer string is a malformation and its tail is ignored rather than
// allowed to wrap.
func beCode(s String) (uint32, int) {
	n := len(s)
	if n > 4 {
		n = 4
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<8 | uint32(s[i])
	}
	return v, n
}

// readCodespaces reads a codespace range section.
func (c *cmap) readCodespaces(l *lexer, dp detect.Depth) {
	var lo String
	section(l, dp, "endcodespacerange", func(o Object) bool {
		s, ok := o.(String)
		if !ok {
			return true
		}
		if lo == nil {
			lo = s
			return true
		}
		lv, n := beCode(lo)
		hv, _ := beCode(s)
		lo = nil
		if n >= 1 && n <= 4 && hv >= lv && len(c.spaces) < 64 {
			c.spaces = append(c.spaces, codespace{n: n, low: lv, high: hv})
		}
		return true
	})
}

// utf16Text decodes a CMap destination string, which is UTF-16BE.
func utf16Text(s String) string {
	if len(s) == 1 {
		return string(rune(s[0]))
	}
	units := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		units = append(units, uint16(s[i])<<8|uint16(s[i+1]))
	}
	return string(utf16.Decode(units))
}

// readBFChars reads a bfchar section: pairs of source code and destination.
func (c *cmap) readBFChars(l *lexer, dp detect.Depth) {
	var src String
	section(l, dp, "endbfchar", func(o Object) bool {
		if src == nil {
			s, ok := o.(String)
			if !ok {
				return true
			}
			src = s
			return true
		}
		code, _ := beCode(src)
		src = nil
		if len(c.single) >= maxCMapEntries {
			return false
		}
		switch v := o.(type) {
		case String:
			c.single[code] = utf16Text(v)
		case Name:
			if r, ok := runeForGlyph(string(v)); ok {
				c.single[code] = string(r)
			}
		}
		return true
	})
}

// readBFRanges reads a bfrange section: triples of low code, high code and
// destination.
//
// A range is stored as a range and never expanded. A single bfrange may
// legitimately span the whole two-byte space, and expanding one into a map is
// how a four-line CMap asks for sixty-five thousand allocations.
func (c *cmap) readBFRanges(l *lexer, dp detect.Depth) {
	var lo, hi String
	have := 0
	section(l, dp, "endbfrange", func(o Object) bool {
		switch have {
		case 0:
			s, ok := o.(String)
			if !ok {
				return true
			}
			lo, have = s, 1
			return true
		case 1:
			s, ok := o.(String)
			if !ok {
				have = 0
				return true
			}
			hi, have = s, 2
			return true
		}
		have = 0
		lv, _ := beCode(lo)
		hv, _ := beCode(hi)
		if hv < lv || len(c.ranges) >= maxCMapEntries {
			return len(c.ranges) < maxCMapEntries
		}
		switch v := o.(type) {
		case String:
			c.ranges = append(c.ranges, bfrange{lo: lv, hi: hv, dst: utf16Text(v)})
		case Name:
			if r, ok := runeForGlyph(string(v)); ok {
				c.ranges = append(c.ranges, bfrange{lo: lv, hi: hv, dst: string(r)})
			}
		case Array:
			list := make([]string, 0, len(v))
			for _, e := range v {
				switch d := e.(type) {
				case String:
					list = append(list, utf16Text(d))
				case Name:
					if r, ok := runeForGlyph(string(d)); ok {
						list = append(list, string(r))
					} else {
						list = append(list, "")
					}
				default:
					list = append(list, "")
				}
			}
			c.ranges = append(c.ranges, bfrange{lo: lv, hi: hv, list: list})
		}
		return true
	})
}

// readCIDChars reads a cidchar section: pairs of code and CID.
func (c *cmap) readCIDChars(l *lexer, dp detect.Depth) {
	var src String
	section(l, dp, "endcidchar", func(o Object) bool {
		if src == nil {
			s, ok := o.(String)
			if !ok {
				return true
			}
			src = s
			return true
		}
		code, _ := beCode(src)
		src = nil
		if v, ok := toInt(o); ok && v >= 0 && v <= 0xFFFF {
			if len(c.cids) >= maxCMapEntries {
				return false
			}
			c.cids[code] = int(v)
		}
		return true
	})
}

// readCIDRanges reads a cidrange section: triples of low code, high code and
// first CID.
func (c *cmap) readCIDRanges(l *lexer, dp detect.Depth) {
	var lo, hi String
	have := 0
	section(l, dp, "endcidrange", func(o Object) bool {
		switch have {
		case 0:
			s, ok := o.(String)
			if !ok {
				return true
			}
			lo, have = s, 1
			return true
		case 1:
			s, ok := o.(String)
			if !ok {
				have = 0
				return true
			}
			hi, have = s, 2
			return true
		}
		have = 0
		lv, _ := beCode(lo)
		hv, _ := beCode(hi)
		v, ok := toInt(o)
		if ok && hv >= lv && v >= 0 && v <= 0xFFFF && len(c.cidRanges) < maxCMapEntries {
			c.cidRanges = append(c.cidRanges, cidrange{lo: lv, hi: hv, cid: int(v)})
		}
		return true
	})
}

// next reads the next character code from b, returning the code and how many
// bytes it took.
//
// The byte count comes from the codespace ranges, tried shortest first as the
// specification requires. A byte sequence in no codespace consumes the
// shortest declared code length, which keeps the reader advancing and marks
// the character undecodable rather than resynchronising into the middle of the
// next code.
func (c *cmap) next(b []byte) (uint32, int) {
	if len(b) == 0 {
		return 0, 0
	}
	var v uint32
	for n := 1; n <= 4 && n <= len(b); n++ {
		v = v<<8 | uint32(b[n-1])
		for _, s := range c.spaces {
			if s.n == n && v >= s.low && v <= s.high {
				return v, n
			}
		}
	}
	shortest := 1
	if len(c.spaces) > 0 {
		shortest = c.spaces[0].n
		for _, s := range c.spaces {
			if s.n < shortest {
				shortest = s.n
			}
		}
	}
	if shortest > len(b) {
		shortest = len(b)
	}
	v = 0
	for i := 0; i < shortest; i++ {
		v = v<<8 | uint32(b[i])
	}
	return v, shortest
}

// text returns the characters a code maps to, and whether it mapped at all.
func (c *cmap) text(code uint32) (string, bool) {
	if c == nil {
		return "", false
	}
	if s, ok := c.single[code]; ok {
		return s, s != ""
	}
	for _, r := range c.ranges {
		if code < r.lo || code > r.hi {
			continue
		}
		if r.list != nil {
			i := int(code - r.lo)
			if i < len(r.list) && r.list[i] != "" {
				return r.list[i], true
			}
			return "", false
		}
		if r.dst == "" {
			return "", false
		}
		// The destination increments in its last code unit, which is what
		// lets one entry cover a whole alphabet.
		rs := []rune(r.dst)
		rs[len(rs)-1] += rune(code - r.lo)
		return string(rs), true
	}
	return "", false
}

// cid returns the CID a code maps to. An identity CMap maps every code to
// itself, which is what Identity-H means and what almost every embedded
// subset font in a modern PDF uses.
func (c *cmap) cid(code uint32) (int, bool) {
	if c == nil {
		return 0, false
	}
	if c.identity {
		return int(code), true
	}
	if v, ok := c.cids[code]; ok {
		return v, true
	}
	for _, r := range c.cidRanges {
		if code >= r.lo && code <= r.hi {
			return r.cid + int(code-r.lo), true
		}
	}
	return 0, false
}
