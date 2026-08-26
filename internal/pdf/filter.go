package pdf

import (
	"bytes"
	"errors"
	"io"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// sink accumulates decoded bytes under two ceilings at once, and refuses
// before it allocates rather than after.
//
// The per-stream ceiling stops one enormous stream; the cumulative counter
// stops a thousand merely large ones, which is the same attack spread out
// (docs/adr/0020-resource-limits.md). It exists because the hand-written
// filters produce bytes rather than a reader, so there is nothing for a
// detect.LimitedReader to wrap — the check has to be at the append.
type sink struct {
	buf []byte
	max int64
	cum *detect.Counter
}

// newSink returns a sink bounded by lim's per-stream ceiling and charging cum.
func newSink(lim detect.Limits, cum *detect.Counter) *sink {
	return &sink{max: lim.Normalised().MaxStreamBytes, cum: cum}
}

// Write implements [io.Writer], refusing the whole write when it would cross
// either ceiling. Nothing partial is kept: a truncated decode is a document
// whose content an attacker chose, which is the failure mode
// detect.LimitedReader exists to avoid.
func (s *sink) Write(p []byte) (int, error) {
	if int64(len(s.buf))+int64(len(p)) > s.max {
		return 0, &limitError{limit: detect.LimitStreamBytes, max: s.max}
	}
	if err := s.cum.Add(int64(len(p))); err != nil {
		return 0, err
	}
	s.buf = append(s.buf, p...)
	return len(p), nil
}

// writeByte appends one byte under the same ceilings.
func (s *sink) writeByte(b byte) error {
	var one [1]byte
	one[0] = b
	_, err := s.Write(one[:])
	return err
}

// limitError reports a per-stream ceiling from a sink.
//
// detect owns the limit vocabulary but not a constructor this package can
// call, so this is the one place a limit failure is built here. It unwraps to
// detect.ErrLimitExceeded, so callers above test exactly one sentinel.
type limitError struct {
	limit detect.Limit
	max   int64
}

// Error names the limit, its ceiling and the option that raises it.
func (e *limitError) Error() string {
	return (&detect.LimitError{Limit: e.limit, Max: e.max}).Error()
}

// Unwrap returns detect.ErrLimitExceeded.
func (e *limitError) Unwrap() error { return detect.ErrLimitExceeded }

// filters returns the stream's filter chain and the matching decode
// parameters, both normalised to slices of the same length.
//
// The two are separate objects in the file and either may be a single value
// or an array, so the shapes are reconciled here rather than at three call
// sites.
func (s *Stream) filters() ([]Name, []Dict) {
	var names []Name
	switch f := s.doc.resolveShallow(s.Dict["Filter"]).(type) {
	case Name:
		names = []Name{f}
	case Array:
		for _, o := range f {
			if n, ok := toName(s.doc.resolveShallow(o)); ok {
				names = append(names, n)
			}
		}
	}
	parms := make([]Dict, len(names))
	switch p := s.doc.resolveShallow(s.Dict["DecodeParms"]).(type) {
	case Dict:
		if len(parms) > 0 {
			parms[0] = p
		}
	case Array:
		for i, o := range p {
			if i >= len(parms) {
				break
			}
			if d, ok := s.doc.resolveShallow(o).(Dict); ok {
				parms[i] = d
			}
		}
	}
	return names, parms
}

// Decode returns the stream's decoded bytes.
//
// It refuses rather than half-succeeds. A filter this package does not
// implement returns [ErrUnsupportedFilter] naming it; a stream that names an
// external file returns [ErrMalformed], because nothing a document references
// is ever fetched (docs/rules.md §7.4).
//
// The result is not cached. A stream is decoded once by everything in this
// package, and caching it would keep every content stream in the document
// alive for the lifetime of the [Doc].
func (s *Stream) Decode() ([]byte, error) {
	if s == nil || s.doc == nil {
		return nil, malformed("stream", 0, "stream has no document")
	}
	if _, external := s.Dict["F"]; external {
		if _, isDict := s.Dict["F"].(Dict); !isDict {
			return nil, malformed("stream", s.Num, "stream data is in an external file")
		}
	}
	names, parms := s.filters()
	data := s.raw
	for i, f := range names {
		var err error
		data, err = s.applyFilter(f, data, parms[i])
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// applyFilter runs one filter over data, then its predictor if it has one.
func (s *Stream) applyFilter(f Name, data []byte, parm Dict) ([]byte, error) {
	var out []byte
	var err error
	switch f {
	case "FlateDecode", "Fl":
		out, err = s.inflate(data)
	case "LZWDecode", "LZW":
		early := int64(1)
		if v, ok := toInt(s.doc.resolveShallow(parm["EarlyChange"])); ok {
			early = v
		}
		out, err = lzwDecode(data, early != 0, newSink(s.doc.lim, s.doc.cum))
	case "ASCII85Decode", "A85":
		out, err = ascii85Decode(data, newSink(s.doc.lim, s.doc.cum))
	case "ASCIIHexDecode", "AHx":
		out, err = asciiHexDecode(data, newSink(s.doc.lim, s.doc.cum))
	case "RunLengthDecode", "RL":
		out, err = runLengthDecode(data, newSink(s.doc.lim, s.doc.cum))
	case "":
		// An absent filter is the identity, and so is /Crypt with an
		// identity name — but this package never sees an encrypted document,
		// so anything else is refused.
		return data, nil
	default:
		return nil, unsupportedFilter(s.Num, f)
	}
	if err != nil {
		return nil, err
	}
	return s.predict(out, parm)
}

// inflate decompresses a FlateDecode stream.
//
// The decompressor is constructed inside a detect.LimitedReader, so the
// expansion is bounded before it is allocated rather than measured after
// (docs/adr/0020-resource-limits.md). Both the zlib and the raw-deflate
// framing are tried, because FlateDecode is specified as zlib and produced as
// raw deflate often enough that a reader needs both.
//
// A stream that decompresses partially and then fails keeps what it got. That
// is the one place this package accepts a truncated decode, and it is
// deliberate: a content stream whose last object is cut off still holds the
// text of the page, and the alternative is losing the page to a single
// corrupt byte. It never applies to a limit failure, which discards
// everything.
func (s *Stream) inflate(data []byte) ([]byte, error) {
	out, err := s.inflateWith(data, true)
	if err == nil || len(out) > 0 {
		return out, nil
	}
	// A leading byte that is not a zlib header is the common malformation.
	// Retry as raw deflate, and if the stream begins with a stray newline try
	// once past it.
	if out, err2 := s.inflateWith(data, false); err2 == nil || len(out) > 0 {
		return out, nil
	}
	for i := 0; i < len(data) && i < 2 && isSpace(data[i]); i++ {
		if out, err2 := s.inflateWith(data[i+1:], true); err2 == nil || len(out) > 0 {
			return out, nil
		}
	}
	if errors.Is(err, detect.ErrLimitExceeded) {
		return nil, err
	}
	return nil, malformed("stream", s.Num, "flate stream could not be decompressed")
}

// inflateWith decompresses data in one framing, discarding everything on a
// limit failure.
func (s *Stream) inflateWith(data []byte, zlibFraming bool) ([]byte, error) {
	var r io.ReadCloser
	if zlibFraming {
		var err error
		r, err = detect.Zlib(bytes.NewReader(data), s.doc.lim, s.doc.cum)
		if err != nil {
			return nil, err
		}
	} else {
		r = detect.Flate(bytes.NewReader(data), s.doc.lim, s.doc.cum)
	}
	defer func() { _ = r.Close() }()
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	if errors.Is(err, detect.ErrLimitExceeded) {
		return nil, err
	}
	return buf.Bytes(), err
}

// predict undoes a PNG or TIFF predictor.
//
// Cross-reference streams are almost always written with PNG predictor 12, so
// this is not an optional extra: without it the xref stream decodes to bytes
// that are structurally plausible and numerically wrong, which is precisely
// the class of failure this package is written to avoid.
func (s *Stream) predict(data []byte, parm Dict) ([]byte, error) {
	if parm == nil {
		return data, nil
	}
	pred, ok := toInt(s.doc.resolveShallow(parm["Predictor"]))
	if !ok || pred <= 1 {
		return data, nil
	}
	colors := int64(1)
	if v, ok := toInt(s.doc.resolveShallow(parm["Colors"])); ok {
		colors = v
	}
	bpc := int64(8)
	if v, ok := toInt(s.doc.resolveShallow(parm["BitsPerComponent"])); ok {
		bpc = v
	}
	columns := int64(1)
	if v, ok := toInt(s.doc.resolveShallow(parm["Columns"])); ok {
		columns = v
	}
	// Every one of these is a number the document chose. A row wider than the
	// stream ceiling cannot describe anything in the stream, so it is refused
	// before the row buffer is allocated.
	if colors < 1 || colors > 32 || columns < 1 || bpc < 1 || bpc > 32 {
		return nil, malformed("stream", s.Num, "predictor parameters out of range")
	}
	bpp := (colors*bpc + 7) / 8
	rowLen := (colors*bpc*columns + 7) / 8
	if rowLen <= 0 || rowLen > s.doc.lim.Normalised().MaxStreamBytes {
		return nil, malformed("stream", s.Num, "predictor row longer than the stream limit")
	}
	if pred == 2 {
		return tiffPredictor(data, int(colors), int(bpc), int(columns)), nil
	}
	return pngPredictor(data, int(rowLen), int(bpp))
}

// tiffPredictor undoes TIFF predictor 2, which is a horizontal difference.
// Only the 8-bit case is undone; other bit depths appear on images, which
// this package does not read, and are left alone rather than corrupted.
func tiffPredictor(data []byte, colors, bpc, columns int) []byte {
	if bpc != 8 {
		return data
	}
	rowLen := colors * columns
	if rowLen <= 0 {
		return data
	}
	for r := 0; r+rowLen <= len(data); r += rowLen {
		row := data[r : r+rowLen]
		for i := colors; i < len(row); i++ {
			row[i] += row[i-colors]
		}
	}
	return data
}

// pngPredictor undoes the per-row PNG filters, whose type byte precedes each
// row. A trailing partial row is dropped rather than read past the end.
func pngPredictor(data []byte, rowLen, bpp int) ([]byte, error) {
	out := make([]byte, 0, len(data))
	prev := make([]byte, rowLen)
	row := make([]byte, rowLen)
	for pos := 0; pos+1 <= len(data); pos += 1 + rowLen {
		ft := data[pos]
		end := pos + 1 + rowLen
		if end > len(data) {
			break
		}
		copy(row, data[pos+1:end])
		switch ft {
		case 0:
		case 1:
			for i := bpp; i < rowLen; i++ {
				row[i] += row[i-bpp]
			}
		case 2:
			for i := 0; i < rowLen; i++ {
				row[i] += prev[i]
			}
		case 3:
			for i := 0; i < rowLen; i++ {
				var left byte
				if i >= bpp {
					left = row[i-bpp]
				}
				row[i] += byte((int(left) + int(prev[i])) / 2)
			}
		case 4:
			for i := 0; i < rowLen; i++ {
				var left, upLeft byte
				if i >= bpp {
					left, upLeft = row[i-bpp], prev[i-bpp]
				}
				row[i] += paeth(left, prev[i], upLeft)
			}
		default:
			return nil, errors.New("unknown png predictor row filter")
		}
		out = append(out, row...)
		copy(prev, row)
	}
	return out, nil
}

// paeth is the PNG Paeth predictor.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

// abs returns the absolute value of n.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// asciiHexDecode undoes ASCIIHexDecode: hexadecimal digits, white space
// ignored, terminated by '>' with an odd trailing digit padded with zero.
func asciiHexDecode(data []byte, s *sink) ([]byte, error) {
	var cur, n int
	for _, c := range data {
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
			if err := s.writeByte(byte(cur)); err != nil {
				return nil, err
			}
			cur, n = 0, 0
		}
	}
	if n == 1 {
		if err := s.writeByte(byte(cur << 4)); err != nil {
			return nil, err
		}
	}
	return s.buf, nil
}

// ascii85Decode undoes ASCII85Decode.
//
// It is written by hand rather than taken from encoding/ascii85 because the
// standard library's decoder does not accept the leading <~ that PDF writers
// emit, does not stop at PDF's ~> terminator on its own terms, and reports a
// corrupt group as an error where a partly-readable content stream is worth
// more than a refusal.
func ascii85Decode(data []byte, s *sink) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("<~")) {
		data = data[2:]
	}
	var group [5]byte
	n := 0
	flush := func(count int) error {
		for i := count; i < 5; i++ {
			group[i] = 'u'
		}
		var v uint32
		for i := 0; i < 5; i++ {
			d := uint32(group[i] - '!')
			if d > 84 {
				d = 84
			}
			// The largest valid group is 0xFFFFFFFF; anything above it is a
			// corrupt group, clamped rather than allowed to wrap.
			if v > (^uint32(0)-d)/85 {
				v = ^uint32(0)
				break
			}
			v = v*85 + d
		}
		out := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		_, err := s.Write(out[:count-1])
		return err
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case isSpace(c):
			continue
		case c == '~':
			if n > 1 {
				if err := flush(n); err != nil {
					return nil, err
				}
			}
			return s.buf, nil
		case c == 'z' && n == 0:
			if _, err := s.Write([]byte{0, 0, 0, 0}); err != nil {
				return nil, err
			}
			continue
		case c < '!' || c > 'u':
			continue
		}
		group[n] = c
		n++
		if n == 5 {
			if err := flush(5); err != nil {
				return nil, err
			}
			n = 0
		}
	}
	if n > 1 {
		if err := flush(n); err != nil {
			return nil, err
		}
	}
	return s.buf, nil
}

// runLengthDecode undoes RunLengthDecode: a length byte then either that many
// literal bytes or one byte repeated, with 128 as the end marker.
func runLengthDecode(data []byte, s *sink) ([]byte, error) {
	for i := 0; i < len(data); {
		l := int(data[i])
		i++
		switch {
		case l == 128:
			return s.buf, nil
		case l < 128:
			end := i + l + 1
			if end > len(data) {
				end = len(data)
			}
			if _, err := s.Write(data[i:end]); err != nil {
				return nil, err
			}
			i = end
		default:
			if i >= len(data) {
				return s.buf, nil
			}
			run := make([]byte, 257-l)
			for j := range run {
				run[j] = data[i]
			}
			if _, err := s.Write(run); err != nil {
				return nil, err
			}
			i++
		}
	}
	return s.buf, nil
}

// lzwDecode undoes LZWDecode.
//
// compress/lzw cannot be used for the default case. PDF's EarlyChange, which
// defaults to 1, widens the code one entry before the table is full; the
// standard library's MSB reader implements the variant that does not, and the
// difference does not fail — it produces bytes that are wrong from the first
// table growth onwards. Silently wrong output is the failure mode this whole
// package is written to avoid, so the decoder is here, with earlyChange as a
// parameter, and the earlyChange=false path is cross-checked against
// compress/lzw in the tests.
func lzwDecode(data []byte, earlyChange bool, s *sink) ([]byte, error) {
	const (
		clearCode = 256
		eodCode   = 257
		maxCode   = 4096
	)
	// The dictionary is preallocated at its documented ceiling: it is bounded
	// by the format rather than by the document, so there is nothing here for
	// a hostile file to grow.
	table := make([][]byte, maxCode)
	reset := func() int {
		for i := 0; i < 256; i++ {
			table[i] = []byte{byte(i)}
		}
		for i := 256; i < maxCode; i++ {
			table[i] = nil
		}
		return 258
	}
	next := reset()
	width := 9
	var prev []byte
	var bitBuf uint32
	var bitCnt int
	pos := 0
	for {
		for bitCnt < width {
			if pos >= len(data) {
				return s.buf, nil
			}
			bitBuf = bitBuf<<8 | uint32(data[pos])
			bitCnt += 8
			pos++
		}
		code := int(bitBuf>>(uint(bitCnt-width))) & ((1 << uint(width)) - 1)
		bitCnt -= width
		switch code {
		case eodCode:
			return s.buf, nil
		case clearCode:
			next = reset()
			width = 9
			prev = nil
			continue
		}
		var entry []byte
		if code == next && prev != nil {
			// The one self-referential case the algorithm allows: the entry
			// being defined by this very step.
			entry = append(append([]byte{}, prev...), prev[0])
		} else if code < next && table[code] != nil {
			entry = table[code]
		} else {
			// A code outside the table is a corrupt or hostile stream. What
			// has been decoded so far is kept; guessing past it would invent
			// content.
			return s.buf, nil
		}
		if _, err := s.Write(entry); err != nil {
			return nil, err
		}
		if prev != nil && next < maxCode {
			table[next] = append(append([]byte{}, prev...), entry[0])
			next++
		}
		prev = entry
		// Code width grows with the table. EarlyChange makes it grow one
		// entry sooner, which is the whole of the incompatibility with
		// compress/lzw.
		limit := next
		if earlyChange {
			limit = next + 1
		}
		switch {
		case limit >= 2048:
			width = 12
		case limit >= 1024:
			width = 11
		case limit >= 512:
			width = 10
		default:
			width = 9
		}
	}
}
