package pdf

import (
	"strconv"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// Object is any PDF object. The concrete types are [Bool], [Integer], [Real],
// [String], [Name], [Array], [Dict], [*Stream] and [Ref]; the null object and
// a missing object are both an untyped nil.
//
// It is `any` rather than a sealed interface because every use site is a type
// switch that must handle an unexpected type anyway — type confusion in the
// object graph is a documented attack (docs/threat-model.md T3), so "this was
// supposed to be a dictionary and is an integer" is a case the code has to
// carry regardless of what the type system promises.
type Object any

// Bool is the PDF boolean.
type Bool bool

// Integer is a PDF integer. It is int64 rather than int so that a length or
// an offset written to overflow a 32-bit int is a value that gets range
// checked rather than a value that has already wrapped.
type Integer int64

// Real is a PDF real number.
type Real float64

// String is a PDF string, after escape and hex decoding. It is bytes because
// a PDF string is bytes: its text encoding is a property of the font or of a
// metadata convention, not of the syntax.
type String []byte

// Name is a PDF name, without the leading slash and with #xx escapes decoded.
type Name string

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference. It is a value type so it can be a map key,
// which is how cycles through the object graph are detected.
type Ref struct {
	Num int
	Gen int
}

// Stream is a PDF stream: a dictionary and the raw, still-encoded bytes.
//
// The bytes stay encoded until [Stream.Decode] is asked for them, so a
// document full of streams nobody reads costs nothing, and so the
// decompression limit is spent at the moment of decompression rather than at
// the moment of parsing.
type Stream struct {
	// Dict is the stream dictionary.
	Dict Dict

	// Num is the object number the stream was found in, carried so a decode
	// failure can say which object without the caller tracking it.
	Num int

	raw []byte
	doc *Doc
}

// operator is a content-stream operator. It is unexported because it is not a
// PDF object: the object parser rejects one, and only the content interpreter
// accepts it.
type operator string

// lexer walks PDF syntax over a byte slice.
//
// It never copies the input and never seeks outside it. Every read is bounds
// checked against len(data) rather than against a length the document
// declared, which is the whole of the defence against T3's out-of-range
// indices.
type lexer struct {
	data []byte
	pos  int
}

// isSpace reports whether c is one of the six bytes the PDF specification
// calls white space. NUL is one of them, which is why this cannot be
// unicode.IsSpace.
func isSpace(c byte) bool {
	return c == 0x00 || c == 0x09 || c == 0x0A || c == 0x0C || c == 0x0D || c == 0x20
}

// isDelim reports whether c ends a token without being consumed by it.
func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// isRegular reports whether c may appear inside a name, number or keyword.
func isRegular(c byte) bool { return !isSpace(c) && !isDelim(c) }

// skipSpace advances past white space and comments.
func (l *lexer) skipSpace() {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch {
		case isSpace(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// atEOF reports whether the lexer has consumed its input.
func (l *lexer) atEOF() bool {
	l.skipSpace()
	return l.pos >= len(l.data)
}

// keyword reads a run of regular characters. It returns an empty string at a
// delimiter, which every caller treats as "not a keyword".
func (l *lexer) keyword() string {
	start := l.pos
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		l.pos++
	}
	return string(l.data[start:l.pos])
}

// peekKeyword reads a keyword without consuming it.
func (l *lexer) peekKeyword() string {
	save := l.pos
	l.skipSpace()
	kw := l.keyword()
	l.pos = save
	return kw
}

// hexVal returns the value of a hexadecimal digit, or -1.
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// name reads a name object, the leading slash already consumed.
func (l *lexer) name() Name {
	var b []byte
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		c := l.data[l.pos]
		if c == '#' && l.pos+2 < len(l.data) {
			hi, lo := hexVal(l.data[l.pos+1]), hexVal(l.data[l.pos+2])
			if hi >= 0 && lo >= 0 {
				b = append(b, byte(hi<<4|lo))
				l.pos += 3
				continue
			}
		}
		b = append(b, c)
		l.pos++
	}
	return Name(b)
}

// literalString reads a (…) string, the opening parenthesis already consumed.
//
// Unbalanced parentheses are the documented way to make a naive parser read
// the rest of the file as one string, so nesting is counted and the read stops
// at the end of the input rather than wrapping.
func (l *lexer) literalString() String {
	var b []byte
	depth := 1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		switch c {
		case '\\':
			if l.pos >= len(l.data) {
				return b
			}
			e := l.data[l.pos]
			l.pos++
			switch e {
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'b':
				b = append(b, '\b')
			case 'f':
				b = append(b, '\f')
			case '\n':
				// A backslash before a newline is a line continuation.
			case '\r':
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && l.pos < len(l.data); i++ {
						d := l.data[l.pos]
						if d < '0' || d > '7' {
							break
						}
						v = v<<3 | int(d-'0')
						l.pos++
					}
					b = append(b, byte(v))
				} else {
					b = append(b, e)
				}
			}
		case '(':
			depth++
			b = append(b, c)
		case ')':
			depth--
			if depth == 0 {
				return b
			}
			b = append(b, c)
		default:
			b = append(b, c)
		}
	}
	return b
}

// hexString reads a <…> string, the opening angle bracket already consumed. An
// odd final digit is padded with zero, as the specification requires.
func (l *lexer) hexString() String {
	var b []byte
	var cur, n int
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		if c == '>' {
			break
		}
		v := hexVal(c)
		if v < 0 {
			continue
		}
		cur = cur<<4 | v
		n++
		if n == 2 {
			b = append(b, byte(cur))
			cur, n = 0, 0
		}
	}
	if n == 1 {
		b = append(b, byte(cur<<4))
	}
	return b
}

// number reads an integer or a real.
//
// PDF in the wild contains numbers no grammar admits — "--3", "3.4.5", a lone
// full stop — and refusing the file over one of them loses a document that
// every viewer opens. They are parsed as leniently as strconv allows and
// otherwise read as zero, because a wrong coordinate costs a box and a refused
// file costs the extraction.
func (l *lexer) number() Object {
	start := l.pos
	real := false
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if c == '.' {
			real = true
		} else if !(c >= '0' && c <= '9') && c != '+' && c != '-' {
			break
		}
		l.pos++
	}
	tok := string(l.data[start:l.pos])
	if !real {
		if v, err := strconv.ParseInt(tok, 10, 64); err == nil {
			return Integer(v)
		}
	}
	if v, err := strconv.ParseFloat(tok, 64); err == nil {
		return Real(v)
	}
	return Integer(0)
}

// object parses one object, spending one level of d.
//
// Depth is a parameter rather than a field so that it cannot be forgotten on a
// path added later and needs no unwinding (docs/adr/0020-resource-limits.md).
// An array nested ten thousand deep exhausts the budget instead of the stack.
func (l *lexer) object(d detect.Depth) (Object, error) {
	d, err := d.Descend()
	if err != nil {
		return nil, err
	}
	l.skipSpace()
	if l.pos >= len(l.data) {
		return nil, malformed("object", 0, "input ended mid-object")
	}
	c := l.data[l.pos]
	switch {
	case c == '/':
		l.pos++
		return l.name(), nil
	case c == '(':
		l.pos++
		return l.literalString(), nil
	case c == '<':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return l.dict(d)
		}
		l.pos++
		return l.hexString(), nil
	case c == '[':
		l.pos++
		return l.array(d)
	case c == ']' || c == '>' || c == ')' || c == '}':
		l.pos++
		return nil, malformed("object", 0, "unbalanced delimiter")
	case c == '{':
		l.pos++
		return operator("{"), nil
	case c >= '0' && c <= '9', c == '+', c == '-', c == '.':
		return l.numberOrRef(), nil
	}
	kw := l.keyword()
	switch kw {
	case "true":
		return Bool(true), nil
	case "false":
		return Bool(false), nil
	case "null":
		return nil, nil
	case "":
		// A delimiter that is not an object start and not a token. Consume it
		// so the caller cannot spin.
		l.pos++
		return nil, malformed("object", 0, "unexpected delimiter")
	}
	return operator(kw), nil
}

// numberOrRef reads a number, and turns "n g R" into a [Ref].
//
// The lookahead is unavoidable: the three tokens are indistinguishable from
// three separate objects until the R arrives, which is why the position is
// saved and restored rather than the tokens being pushed back.
func (l *lexer) numberOrRef() Object {
	first := l.number()
	num, ok := first.(Integer)
	if !ok || num < 0 {
		return first
	}
	save := l.pos
	l.skipSpace()
	if l.pos >= len(l.data) || l.data[l.pos] < '0' || l.data[l.pos] > '9' {
		l.pos = save
		return first
	}
	gen, ok := l.number().(Integer)
	if !ok || gen < 0 {
		l.pos = save
		return first
	}
	l.skipSpace()
	if l.pos < len(l.data) && l.data[l.pos] == 'R' &&
		(l.pos+1 >= len(l.data) || !isRegular(l.data[l.pos+1])) {
		l.pos++
		// An object number beyond what an int can hold cannot index anything,
		// so it is a number rather than a reference.
		if num > maxObjectNumber || gen > maxObjectNumber {
			l.pos = save
			return first
		}
		return Ref{Num: int(num), Gen: int(gen)}
	}
	l.pos = save
	return first
}

// maxObjectNumber bounds an object number before it is converted to an int.
// The specification's own ceiling is smaller; this one only has to stop a
// value chosen to overflow (docs/threat-model.md T3).
const maxObjectNumber = 1 << 31

// array reads the body of an array, the opening bracket already consumed.
func (l *lexer) array(d detect.Depth) (Object, error) {
	arr := Array{}
	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			return arr, malformed("object", 0, "input ended inside an array")
		}
		if l.data[l.pos] == ']' {
			l.pos++
			return arr, nil
		}
		o, err := l.object(d)
		if err != nil {
			return arr, err
		}
		if op, isOp := o.(operator); isOp {
			// An operator inside an array is malformed. Dropping it and
			// continuing keeps the surrounding object readable, which is what
			// every viewer does; the alternative loses the page.
			_ = op
			continue
		}
		arr = append(arr, o)
	}
}

// dict reads the body of a dictionary, the opening << already consumed.
func (l *lexer) dict(d detect.Depth) (Object, error) {
	dict := Dict{}
	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			return dict, malformed("object", 0, "input ended inside a dictionary")
		}
		if l.data[l.pos] == '>' {
			l.pos++
			if l.pos < len(l.data) && l.data[l.pos] == '>' {
				l.pos++
			}
			return dict, nil
		}
		if l.data[l.pos] != '/' {
			// A key that is not a name. Skip one object and carry on rather
			// than lose the dictionary.
			if _, err := l.object(d); err != nil {
				return dict, err
			}
			continue
		}
		l.pos++
		key := l.name()
		val, err := l.object(d)
		if err != nil {
			return dict, err
		}
		if _, isOp := val.(operator); isOp {
			continue
		}
		dict[key] = val
	}
}

// The accessors below are the only way anything in this package reads a typed
// value out of an object, so type confusion has exactly one place to be
// handled and every caller handles it the same way (docs/threat-model.md T3).

// toInt returns the integer value of o, and whether it was a number.
func toInt(o Object) (int64, bool) {
	switch v := o.(type) {
	case Integer:
		return int64(v), true
	case Real:
		return int64(v), true
	}
	return 0, false
}

// toFloat returns the numeric value of o, and whether it was a number.
func toFloat(o Object) (float64, bool) {
	switch v := o.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// toName returns the name value of o, and whether it was a name.
func toName(o Object) (Name, bool) {
	v, ok := o.(Name)
	return v, ok
}
