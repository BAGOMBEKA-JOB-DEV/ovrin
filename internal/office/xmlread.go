package office

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// newDecoder returns a decoder configured so that entity expansion cannot
// happen, with both settings written out rather than left to the default.
//
// The two lines are the whole billion-laughs defence and they are stated
// explicitly so that a later contributor has to delete something visible in
// order to reopen the attack:
//
//   - Entity nil means only the five predefined entities and numeric
//     character references resolve. Nothing populates this map — in
//     particular, parsing a DOCTYPE internal subset does not, because
//     encoding/xml reports a DOCTYPE as one opaque Directive and never reads
//     the declarations inside it. A document declaring nine levels of nested
//     entities therefore leaves this map empty.
//   - Strict true means a reference to any other entity is an error rather
//     than passthrough text, so a recursive entity fails at its first
//     reference having produced no character data at all.
//
// An external entity needs no separate defence: encoding/xml has no code path
// that opens a URL, so a SYSTEM identifier is just another unresolvable name
// and nothing is fetched (docs/rules.md §7.4).
func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Entity = nil
	d.Strict = true
	return d
}

// doctypePrefix is what a DOCTYPE directive's content begins with.
var doctypePrefix = []byte("DOCTYPE")

// nextToken returns the next token, refusing a DOCTYPE declaration.
//
// The refusal is belt and braces over the decoder settings above: no OOXML
// part has a legitimate DOCTYPE, and every reason to write one into a
// generated document part is a reason this package should decline to read it.
// Refusing costs nothing and removes a class of surprise if the standard
// library's handling of internal subsets ever changes.
//
// It returns io.EOF at the end of the stream, unwrapped, so callers can test
// for it directly.
func nextToken(d *xml.Decoder, part Part) (xml.Token, error) {
	t, err := d.Token()
	if err != nil {
		if err == io.EOF { //nolint:errorlint // Decoder.Token returns io.EOF itself, never wrapped
			return nil, io.EOF
		}
		// The decoder's message is free to quote the document — it prints the
		// offending entity name and surrounding text — so it is not repeated
		// (docs/rules.md §2.5). A limit failure from the reader underneath is
		// passed through, because it carries ovrin's number and not the
		// document's.
		if isLimit(err) {
			return nil, err
		}
		return nil, malformed("xml", part, "part is not well-formed xml")
	}
	if dir, ok := t.(xml.Directive); ok {
		if bytes.HasPrefix(bytes.TrimLeft(dir, " \t\r\n"), doctypePrefix) {
			return nil, unsupported("xml", part, "part declares a document type definition")
		}
	}
	return t, nil
}

// skipElement consumes the remainder of the element whose start tag was just
// read, without recursing.
//
// It is written out rather than borrowed from encoding/xml because
// Decoder.Skip calls itself once per level of nesting, so skipping a subtree
// nested a million deep exhausts the goroutine stack. This one keeps a counter
// instead, so the cost of skipping is a single int whatever arrives
// (docs/threat-model.md T2, T3).
func skipElement(d *xml.Decoder, part Part) error {
	depth := 1
	for depth > 0 {
		t, err := nextToken(d, part)
		if err != nil {
			if err == io.EOF { //nolint:errorlint // nextToken returns io.EOF unwrapped
				return malformed("xml", part, "part ended inside an element")
			}
			return err
		}
		switch t.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// isLimit reports whether an error is one of internal/detect's ceilings, which
// must travel to the caller intact rather than be flattened into ErrMalformed.
// A caller distinguishes "this document is too big" from "this document is
// broken", and only one of them is worth raising a limit for.
func isLimit(err error) bool {
	return errors.Is(err, detect.ErrLimitExceeded)
}

// attr returns the value of the attribute with this local name, ignoring the
// namespace.
//
// Local names are matched throughout this package rather than fully qualified
// ones, because OOXML exists in a transitional namespace and a strict one that
// differ only in their URIs. Matching locally reads both, and no element this
// package looks for has a local name that collides across the vocabularies it
// walks.
func attr(el xml.StartElement, local string) (string, bool) {
	for _, a := range el.Attr {
		if a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// textAccumulator collects character data into a word, charging every byte to
// the document's text budget as it arrives.
//
// Charging on arrival rather than on completion is what makes a part that is
// one enormous run of character data fail early instead of after it has been
// assembled in memory.
type textAccumulator struct {
	buf  bytes.Buffer
	text *detect.Counter
}

// add appends character data, or reports the text ceiling.
func (a *textAccumulator) add(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if err := a.text.Add(int64(len(b))); err != nil {
		return err
	}
	a.buf.Write(b)
	return nil
}

// addString appends a string this package produced rather than read.
func (a *textAccumulator) addString(s string) error {
	return a.add([]byte(s))
}

// take returns what has accumulated and resets the buffer.
func (a *textAccumulator) take() string {
	s := a.buf.String()
	a.buf.Reset()
	return s
}

// empty reports whether anything has accumulated.
func (a *textAccumulator) empty() bool { return a.buf.Len() == 0 }
