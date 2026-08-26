package pdf

import (
	"bytes"
	"errors"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// Doc is an opened PDF: its cross-reference information, its trailer, and its
// page list.
//
// Opening resolves structure only. Nothing is decompressed and no content
// stream is touched until a page is asked for, so a document with ten
// thousand streams costs ten thousand map entries rather than ten thousand
// decompressions, and the decompression budget is spent on the pages the
// caller actually wants.
//
// A Doc is not safe for concurrent use; see the package comment.
type Doc struct {
	data   []byte
	header int
	lim    detect.Limits
	cum    *detect.Counter
	text   *detect.Counter

	xref    map[int]xrefEntry
	trailer Dict
	pages   []pageEntry

	cache   map[int]Object
	loading map[int]bool
	streams map[int]*objStm

	scan     map[int]int
	scanDone bool

	// err is the first limit failure seen while resolving objects.
	//
	// Resolution is called from a hundred places that all want an Object and
	// none of which can do anything useful with an error, so a broken
	// reference resolves to null and reading continues — that is what every
	// viewer does and it is why a slightly corrupt file still extracts. A
	// limit failure is different in kind: it means an attacker is spending
	// our memory, and continuing would spend more. It is recorded here and
	// returned by whichever entry point is running.
	err error
}

// xrefEntry locates one object: either at a byte offset in the file, or at an
// index inside an object stream.
type xrefEntry struct {
	offset     int64
	stream     int
	index      int
	compressed bool
}

// objStm is a decoded object stream and the offsets of the objects in it.
type objStm struct {
	data    []byte
	nums    []int
	offsets []int
}

// maxXrefSections bounds how many cross-reference sections one chain may have.
//
// The visited-offset set already terminates a cycle; this bounds the linear
// case, where each section is distinct and there are two hundred thousand of
// them (docs/threat-model.md T2).
const maxXrefSections = 1024

// headerWindow is how far into the file the %PDF- header may be. It matches
// internal/detect, so a file that package called a PDF is one this package
// will at least try to open.
const headerWindow = 1024

// Open reads a PDF's structure from data.
//
// It does not copy data, and the slice must not be modified while the [Doc] is
// in use: a PDF is read by offset from end to beginning, so copying sixty
// mebibytes to protect against a caller mutating their own buffer would double
// the peak for every well-behaved caller.
//
// cum is the document-wide decompression budget, shared with whatever else is
// reading the same document. A nil cum gets one built from lim, so a caller
// with no budget of its own still gets a cumulative ceiling rather than none —
// which is the difference between one bomb being refused and a thousand small
// ones getting through (docs/adr/0020-resource-limits.md).
//
// It fails with [ErrEncrypted] for an encrypted document — this is the
// authoritative check, not internal/detect's cheap one — and with
// [ErrMalformed] for a file whose structure cannot be read at all.
func Open(data []byte, lim detect.Limits, cum *detect.Counter) (doc *Doc, err error) {
	// A panic while reading structure is a bug in this package. It is
	// converted rather than propagated because a malformed document must not
	// be able to take down the calling service; a recovered panic is still a
	// bug and is treated as one (docs/threat-model.md T3).
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, malformed("open", 0, "parser panicked")
		}
	}()
	lim = lim.Normalised()
	if cum == nil {
		cum = detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes)
	}
	head := data
	if len(head) > headerWindow+5 {
		head = head[:headerWindow+5]
	}
	hdr := bytes.Index(head, []byte("%PDF-"))
	if hdr < 0 {
		return nil, malformed("open", 0, "no pdf header in the first kibibyte")
	}
	d := &Doc{
		data:    data,
		header:  hdr,
		lim:     lim,
		cum:     cum,
		text:    detect.NewCounter(detect.LimitTextBytes, lim.MaxTextBytes),
		xref:    make(map[int]xrefEntry),
		cache:   make(map[int]Object),
		loading: make(map[int]bool),
		streams: make(map[int]*objStm),
	}
	// A broken cross-reference chain is not fatal on its own: the objects are
	// still in the file and a scan finds them. What is fatal is having
	// neither.
	if err := d.readXrefChain(); err != nil && !errors.Is(err, ErrMalformed) {
		return nil, err
	}
	if err := d.checkEncryption(); err != nil {
		return nil, err
	}
	if err := d.findPages(); err != nil {
		return nil, err
	}
	if d.err != nil {
		return nil, d.err
	}
	return d, nil
}

// NumPages returns the page count, which is known only after the page tree has
// been walked — which is why stage 1 reports 0 for a PDF rather than guessing
// (docs/pipeline.md stage 1).
func (d *Doc) NumPages() int { return len(d.pages) }

// Trailer returns the document trailer, for the metadata the pipeline scans.
// It is never document text this package puts in an error.
func (d *Doc) Trailer() Dict { return d.trailer }

// checkEncryption refuses an encrypted document.
//
// The trailer's /Encrypt is the authority. internal/detect looks at the tail
// of the file for the same key and can miss it behind a cross-reference
// stream, which is why that check is conservative and this one is not.
func (d *Doc) checkEncryption() error {
	enc := d.trailer["Encrypt"]
	if enc == nil {
		return nil
	}
	handler := Name("")
	if dict, ok := d.resolveShallow(enc).(Dict); ok {
		if n, ok := toName(d.resolveShallow(dict["Filter"])); ok {
			handler = n
		}
	}
	return encrypted(handler)
}

// readXrefChain follows startxref and every /Prev and /XRefStm from it.
//
// Sections are visited at most once: a cross-reference cycle is a documented
// attack, and a chain that revisits an offset terminates here rather than
// recursing until the stack is gone (docs/threat-model.md T2).
func (d *Doc) readXrefChain() error {
	start, ok := d.startXref()
	if !ok {
		return d.reconstruct()
	}
	visited := make(map[int64]bool)
	queue := []int64{start}
	sections := 0
	for len(queue) > 0 {
		off := queue[0]
		queue = queue[1:]
		if visited[off] || off < 0 || off >= int64(len(d.data)) {
			continue
		}
		visited[off] = true
		sections++
		if sections > maxXrefSections {
			break
		}
		trailer, next, err := d.readXrefSection(off)
		if err != nil {
			continue
		}
		for k, v := range trailer {
			if _, have := d.trailer[k]; !have {
				if d.trailer == nil {
					d.trailer = Dict{}
				}
				d.trailer[k] = v
			}
		}
		queue = append(queue, next...)
	}
	if d.trailer == nil || len(d.xref) == 0 {
		return d.reconstruct()
	}
	if d.trailer["Root"] == nil {
		return d.reconstruct()
	}
	return nil
}

// startXref returns the offset the file's last startxref names.
func (d *Doc) startXref() (int64, bool) {
	const window = 2048
	tail := d.data
	if len(tail) > window {
		tail = tail[len(tail)-window:]
	}
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return 0, false
	}
	l := &lexer{data: tail, pos: i + len("startxref")}
	l.skipSpace()
	n, ok := l.number().(Integer)
	if !ok || n < 0 {
		return 0, false
	}
	return int64(n), true
}

// readXrefSection reads one cross-reference section, table or stream, and
// returns its trailer and the offsets of the sections it chains to.
func (d *Doc) readXrefSection(off int64) (Dict, []int64, error) {
	l := &lexer{data: d.data, pos: int(off)}
	if kw := l.peekKeyword(); kw == "xref" {
		return d.readXrefTable(l)
	}
	return d.readXrefStream(off)
}

// readXrefTable reads a classic cross-reference table and its trailer.
func (d *Doc) readXrefTable(l *lexer) (Dict, []int64, error) {
	l.skipSpace()
	if l.keyword() != "xref" {
		return nil, nil, malformed("xref", 0, "cross-reference table has no keyword")
	}
	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			return nil, nil, malformed("xref", 0, "input ended inside a cross-reference table")
		}
		if l.peekKeyword() == "trailer" {
			break
		}
		first, ok := l.number().(Integer)
		if !ok {
			return nil, nil, malformed("xref", 0, "cross-reference subsection has no first object number")
		}
		l.skipSpace()
		count, ok := l.number().(Integer)
		if !ok || count < 0 || first < 0 {
			return nil, nil, malformed("xref", 0, "cross-reference subsection has no count")
		}
		// The count is a number the document chose and each entry authorises
		// a map insertion, so it is measured against the object ceiling
		// before any of them happens.
		if err := d.lim.CheckObjects(len(d.xref) + int(count)); err != nil {
			return nil, nil, err
		}
		for i := int64(0); i < int64(count); i++ {
			l.skipSpace()
			offset, ok := l.number().(Integer)
			if !ok {
				return nil, nil, malformed("xref", 0, "cross-reference entry has no offset")
			}
			l.skipSpace()
			if _, ok := l.number().(Integer); !ok {
				return nil, nil, malformed("xref", 0, "cross-reference entry has no generation")
			}
			l.skipSpace()
			kind := l.keyword()
			num := int(first) + int(i)
			if kind != "n" || offset <= 0 || int64(offset) >= int64(len(d.data)) {
				continue
			}
			// Earlier sections in the chain are newer, so the first entry
			// for an object wins and an older section cannot overwrite it.
			if _, have := d.xref[num]; !have {
				d.xref[num] = xrefEntry{offset: int64(offset)}
			}
		}
	}
	l.skipSpace()
	if l.keyword() != "trailer" {
		return nil, nil, malformed("xref", 0, "cross-reference table has no trailer")
	}
	o, err := l.object(d.lim.Depth())
	if err != nil {
		return nil, nil, err
	}
	trailer, ok := o.(Dict)
	if !ok {
		return nil, nil, malformed("xref", 0, "trailer is not a dictionary")
	}
	var next []int64
	// A hybrid file's /XRefStm holds entries the table cannot express, and is
	// read first so that its entries win over the table's.
	if v, ok := toInt(trailer["XRefStm"]); ok {
		next = append(next, v)
	}
	if v, ok := toInt(trailer["Prev"]); ok {
		next = append(next, v)
	}
	return trailer, next, nil
}

// readXrefStream reads a cross-reference stream: the same information as a
// table, compressed, and the only place object streams are indexed.
func (d *Doc) readXrefStream(off int64) (Dict, []int64, error) {
	obj, _, err := d.parseIndirectAt(int(off), d.lim.Depth())
	if err != nil {
		return nil, nil, err
	}
	st, ok := obj.(*Stream)
	if !ok {
		return nil, nil, malformed("xref", 0, "cross-reference section is neither a table nor a stream")
	}
	data, err := st.Decode()
	if err != nil {
		return nil, nil, err
	}
	widths, ok := d.resolveShallow(st.Dict["W"]).(Array)
	if !ok || len(widths) < 3 {
		return nil, nil, malformed("xref", st.Num, "cross-reference stream has no field widths")
	}
	var w [3]int
	total := 0
	for i := 0; i < 3; i++ {
		v, ok := toInt(d.resolveShallow(widths[i]))
		if !ok || v < 0 || v > 8 {
			return nil, nil, malformed("xref", st.Num, "cross-reference field width out of range")
		}
		w[i] = int(v)
		total += int(v)
	}
	if total == 0 {
		return nil, nil, malformed("xref", st.Num, "cross-reference stream has zero-width fields")
	}
	size, _ := toInt(d.resolveShallow(st.Dict["Size"]))
	index, _ := d.resolveShallow(st.Dict["Index"]).(Array)
	if len(index) == 0 {
		index = Array{Integer(0), Integer(size)}
	}
	pos := 0
	read := func(n int) int64 {
		var v int64
		for i := 0; i < n; i++ {
			v = v<<8 | int64(data[pos])
			pos++
		}
		return v
	}
	for i := 0; i+1 < len(index); i += 2 {
		first, ok1 := toInt(d.resolveShallow(index[i]))
		count, ok2 := toInt(d.resolveShallow(index[i+1]))
		if !ok1 || !ok2 || first < 0 || count < 0 {
			continue
		}
		// The declared count is checked against the bytes that are actually
		// present before anything is allocated, so a subsection claiming a
		// million entries in a twenty-byte stream stops here.
		if count > int64((len(data)-pos)/total) {
			count = int64((len(data) - pos) / total)
		}
		if err := d.lim.CheckObjects(len(d.xref) + int(count)); err != nil {
			return nil, nil, err
		}
		for j := int64(0); j < count; j++ {
			kind := int64(1)
			if w[0] > 0 {
				kind = read(w[0])
			}
			f2 := read(w[1])
			f3 := read(w[2])
			num := int(first + j)
			if _, have := d.xref[num]; have {
				continue
			}
			switch kind {
			case 1:
				if f2 > 0 && f2 < int64(len(d.data)) {
					d.xref[num] = xrefEntry{offset: f2}
				}
			case 2:
				if f2 >= 0 && f2 <= maxObjectNumber && f3 >= 0 && f3 <= maxObjectNumber {
					d.xref[num] = xrefEntry{stream: int(f2), index: int(f3), compressed: true}
				}
			}
		}
	}
	var next []int64
	if v, ok := toInt(st.Dict["Prev"]); ok {
		next = append(next, v)
	}
	return st.Dict, next, nil
}

// reconstruct rebuilds the cross-reference information by scanning the file
// for object headers.
//
// Every viewer does this, because files with broken cross-reference tables are
// common — a truncated download, a naive concatenation, a generator that
// writes offsets relative to the wrong origin. It is also the answer to an
// xref whose offsets point outside the file: the offsets are simply not used.
func (d *Doc) reconstruct() error {
	d.buildScan()
	if len(d.scan) == 0 {
		return malformed("xref", 0, "no objects found in the file")
	}
	for num, off := range d.scan {
		if _, have := d.xref[num]; !have {
			d.xref[num] = xrefEntry{offset: int64(off)}
		}
	}
	if d.trailer == nil {
		d.trailer = Dict{}
	}
	if d.trailer["Root"] == nil {
		d.trailer["Root"] = d.findRoot()
	}
	if d.trailer["Root"] == nil {
		return malformed("xref", 0, "no document catalog found")
	}
	return nil
}

// buildScan indexes every "n g obj" header in the file, once.
//
// The index is bounded by the object ceiling, checked as it grows rather than
// after: a file that is nothing but object headers is a cheap way to ask for
// an unbounded map (docs/adr/0020-resource-limits.md).
func (d *Doc) buildScan() {
	if d.scanDone {
		return
	}
	d.scanDone = true
	d.scan = make(map[int]int)
	max := d.lim.Normalised().MaxObjects
	for i := 0; i+3 <= len(d.data); {
		j := bytes.Index(d.data[i:], []byte("obj"))
		if j < 0 {
			return
		}
		at := i + j
		i = at + 3
		if at+3 < len(d.data) && isRegular(d.data[at+3]) {
			continue
		}
		num, start, ok := objectHeaderBefore(d.data, at)
		if !ok {
			continue
		}
		if len(d.scan) >= max {
			return
		}
		// A later definition of the same object supersedes an earlier one,
		// which is what an incremental update means.
		d.scan[num] = start
	}
}

// objectHeaderBefore reads the "n g" that precedes an "obj" keyword at at, and
// returns the object number and the offset the header starts at.
func objectHeaderBefore(data []byte, at int) (int, int, bool) {
	i := at - 1
	skipSp := func() {
		for i >= 0 && isSpace(data[i]) {
			i--
		}
	}
	digits := func() (int64, bool) {
		end := i
		for i >= 0 && data[i] >= '0' && data[i] <= '9' {
			i--
		}
		if i == end {
			return 0, false
		}
		var v int64
		for _, c := range data[i+1 : end+1] {
			v = v*10 + int64(c-'0')
			if v > maxObjectNumber {
				return 0, false
			}
		}
		return v, true
	}
	skipSp()
	if _, ok := digits(); !ok {
		return 0, 0, false
	}
	skipSp()
	num, ok := digits()
	if !ok {
		return 0, 0, false
	}
	return int(num), i + 1, true
}

// findRoot finds the document catalog by inspecting scanned objects, for a
// file whose trailer is missing or unreadable.
func (d *Doc) findRoot() Object {
	for num := range d.scan {
		o := d.object(num, d.lim.Depth())
		if dict, ok := o.(Dict); ok {
			if n, ok := toName(dict["Type"]); ok && n == "Catalog" {
				return Ref{Num: num}
			}
		}
	}
	return nil
}

// resolveShallow resolves indirect references using the document's own depth
// budget.
//
// It is the accessor everything in this package reads dictionary values
// through. A reference that cannot be resolved — missing, cyclic, out of
// budget — becomes null, and the caller sees an absent value rather than an
// error it has no way to act on. A limit failure is recorded on the Doc
// instead, because that one must not be shrugged off; see [Doc.err].
func (d *Doc) resolveShallow(o Object) Object {
	return d.resolve(o, d.lim.Depth())
}

// resolve resolves indirect references, spending dp.
func (d *Doc) resolve(o Object, dp detect.Depth) Object {
	for i := 0; i < 32; i++ {
		ref, ok := o.(Ref)
		if !ok {
			return o
		}
		next, err := dp.Descend()
		if err != nil {
			d.note(err)
			return nil
		}
		dp = next
		o = d.object(ref.Num, dp)
	}
	return nil
}

// note records the first limit failure seen during resolution.
func (d *Doc) note(err error) {
	if err != nil && d.err == nil && errors.Is(err, detect.ErrLimitExceeded) {
		d.err = err
	}
}

// object returns object num, loading it if necessary.
//
// The loading set is the cycle guard: an object stream whose container is
// itself, or a length that refers to the object it is the length of, resolves
// to null instead of recursing (docs/threat-model.md T2, T3).
func (d *Doc) object(num int, dp detect.Depth) Object {
	if num <= 0 {
		return nil
	}
	if o, ok := d.cache[num]; ok {
		return o
	}
	if d.loading[num] {
		return nil
	}
	d.loading[num] = true
	defer delete(d.loading, num)

	o := d.loadObject(num, dp)
	if o == nil {
		// The cross-reference information was wrong about this object. The
		// bytes may still be in the file, so fall back to the scan — which
		// is what makes a file with a damaged xref readable at all.
		if off, ok := d.scanOffset(num); ok {
			o = d.objectAt(off, num, dp)
		}
	}
	d.cache[num] = o
	return o
}

// scanOffset returns the offset of object num from the scan index, building
// the index on first use.
func (d *Doc) scanOffset(num int) (int, bool) {
	d.buildScan()
	off, ok := d.scan[num]
	return off, ok
}

// loadObject loads an object from its cross-reference entry.
func (d *Doc) loadObject(num int, dp detect.Depth) Object {
	e, ok := d.xref[num]
	if !ok {
		return nil
	}
	if !e.compressed {
		return d.objectAt(int(e.offset), num, dp)
	}
	return d.objectFromStream(e, num, dp)
}

// objectAt parses the indirect object at off, requiring it to be object num.
//
// Requiring the number to match is what stops a hostile cross-reference table
// from pointing every object at the same bytes and getting a different meaning
// out of each: the header is checked against what was asked for.
func (d *Doc) objectAt(off, num int, dp detect.Depth) Object {
	if off < 0 || off >= len(d.data) {
		return nil
	}
	o, got, err := d.parseIndirectAt(off, dp)
	if err != nil || got != num {
		// Some generators write offsets relative to the %PDF- header rather
		// than to the start of the file. One retry, at that origin.
		if d.header > 0 && off+d.header < len(d.data) {
			if o2, got2, err2 := d.parseIndirectAt(off+d.header, dp); err2 == nil && got2 == num {
				return o2
			}
		}
		return nil
	}
	return o
}

// parseIndirectAt parses "n g obj … endobj" at off and returns the object and
// its number.
func (d *Doc) parseIndirectAt(off int, dp detect.Depth) (Object, int, error) {
	if off < 0 || off >= len(d.data) {
		return nil, 0, malformed("object", 0, "offset outside the file")
	}
	l := &lexer{data: d.data, pos: off}
	l.skipSpace()
	num, ok := l.number().(Integer)
	if !ok || num < 0 || num > maxObjectNumber {
		return nil, 0, malformed("object", 0, "object header has no number")
	}
	l.skipSpace()
	if _, ok := l.number().(Integer); !ok {
		return nil, 0, malformed("object", int(num), "object header has no generation")
	}
	l.skipSpace()
	if l.keyword() != "obj" {
		return nil, 0, malformed("object", int(num), "object header has no obj keyword")
	}
	o, err := l.object(dp)
	if err != nil {
		return nil, int(num), err
	}
	dict, isDict := o.(Dict)
	if !isDict || l.peekKeyword() != "stream" {
		return o, int(num), nil
	}
	st, err := d.readStreamBody(l, dict, int(num), dp)
	if err != nil {
		return nil, int(num), err
	}
	return st, int(num), nil
}

// readStreamBody reads the raw bytes of a stream whose dictionary has just
// been parsed.
//
// The declared /Length is a number the document chose, so it is a hint. It is
// range checked against the file and then confirmed by looking for endstream
// where it says the data ends; when that fails the real end is found by
// search. A stream whose declared length disagrees with its content is a
// documented parser attack, and trusting the declaration is how a parser is
// made to read the next object as data (docs/threat-model.md T3).
func (d *Doc) readStreamBody(l *lexer, dict Dict, num int, dp detect.Depth) (*Stream, error) {
	l.skipSpace()
	if l.keyword() != "stream" {
		return nil, malformed("stream", num, "stream keyword expected")
	}
	// Exactly CRLF or LF follows the keyword; anything else is data.
	if l.pos < len(l.data) && l.data[l.pos] == '\r' {
		l.pos++
	}
	if l.pos < len(l.data) && l.data[l.pos] == '\n' {
		l.pos++
	}
	start := l.pos
	end := -1
	if n, ok := toInt(d.resolve(dict["Length"], dp)); ok && n >= 0 && n <= int64(len(d.data)-start) {
		cand := start + int(n)
		probe := &lexer{data: d.data, pos: cand}
		if probe.peekKeyword() == "endstream" {
			end = cand
		}
	}
	if end < 0 {
		i := bytes.Index(d.data[start:], []byte("endstream"))
		if i < 0 {
			// A truncated file. What is there is still the stream, and a
			// content stream cut short still holds most of a page.
			end = len(d.data)
		} else {
			end = start + i
			// The keyword is preceded by an end-of-line that belongs to the
			// syntax rather than to the data.
			for end > start && (d.data[end-1] == '\n' || d.data[end-1] == '\r') {
				end--
			}
		}
	}
	l.pos = end
	return &Stream{Dict: dict, Num: num, raw: d.data[start:end], doc: d}, nil
}

// objectFromStream loads an object that lives inside an object stream.
func (d *Doc) objectFromStream(e xrefEntry, num int, dp detect.Depth) Object {
	dp, err := dp.Descend()
	if err != nil {
		d.note(err)
		return nil
	}
	stm := d.loadObjStm(e.stream, dp)
	if stm == nil {
		return nil
	}
	// The index the cross-reference gave and the object number in the stream
	// must agree. Where they do not, the number wins: it is the thing being
	// asked for, and an index that points at a different object is how one
	// object is made to masquerade as another.
	idx := e.index
	if idx < 0 || idx >= len(stm.nums) || stm.nums[idx] != num {
		idx = -1
		for i, n := range stm.nums {
			if n == num {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil
		}
	}
	off := stm.offsets[idx]
	if off < 0 || off > len(stm.data) {
		return nil
	}
	l := &lexer{data: stm.data, pos: off}
	o, err := l.object(dp)
	if err != nil {
		return nil
	}
	return o
}

// loadObjStm decodes an object stream and reads its offset table.
//
// The declared /N is checked against the pairs that are actually present, not
// trusted: a stream declaring five hundred objects and containing two is a
// documented malformation, and honouring the declaration is how a parser is
// made to read the object data as an offset table.
func (d *Doc) loadObjStm(num int, dp detect.Depth) *objStm {
	if s, ok := d.streams[num]; ok {
		return s
	}
	// Memoise the failure too, so a thousand objects pointing at one broken
	// container decode it once.
	d.streams[num] = nil

	st, ok := d.object(num, dp).(*Stream)
	if !ok {
		return nil
	}
	data, err := st.Decode()
	if err != nil {
		d.note(err)
		return nil
	}
	n, _ := toInt(d.resolve(st.Dict["N"], dp))
	first, _ := toInt(d.resolve(st.Dict["First"], dp))
	if n < 0 || first < 0 || first > int64(len(data)) {
		return nil
	}
	if err := d.lim.CheckObjects(int(n)); err != nil {
		d.note(err)
		return nil
	}
	// Each pair is at least three bytes ("0 0 "), so a stream that could not
	// hold N pairs never allocates for N of them.
	if n > int64(first)/3+1 {
		n = int64(first)/3 + 1
	}
	stm := &objStm{data: data}
	l := &lexer{data: data[:first], pos: 0}
	for i := int64(0); i < n; i++ {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}
		on, ok1 := l.number().(Integer)
		l.skipSpace()
		oo, ok2 := l.number().(Integer)
		if !ok1 || !ok2 || on < 0 || on > maxObjectNumber || oo < 0 {
			break
		}
		at := first + int64(oo)
		if at > int64(len(data)) {
			break
		}
		stm.nums = append(stm.nums, int(on))
		stm.offsets = append(stm.offsets, int(at))
	}
	d.streams[num] = stm
	return stm
}
