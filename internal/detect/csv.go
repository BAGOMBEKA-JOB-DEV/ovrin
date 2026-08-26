package detect

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"unicode/utf8"
)

// The bounds on how much evidence a CSV judgement is made from, and how much
// of it there has to be.
//
// CSV has no signature, so it is inferred; an inference that is too willing
// turns every text file in the world into a table. Two rows and two columns is
// the least that distinguishes a table from a line of prose with a comma in
// it, and consistency across the rows is what distinguishes it from prose with
// several.
const (
	csvSniffBytes = 64 << 10
	csvMaxRows    = 100
	csvMinRows    = 2
	csvMinFields  = 2
)

// isCSV reports whether data is comma-separated values.
//
// It runs last and refuses by default. Every test below is a reason to say no:
// the bytes have to be text, they have to parse as records, the records have
// to agree on how many fields they have, and there have to be enough of both
// to be a table rather than a coincidence. Anything that fails one of them is
// left unidentified, which is the honest answer for a format that never says
// what it is.
//
// The delimiter is a comma. A semicolon file from a European spreadsheet and a
// tab-separated file are both real and neither is recognised here, because
// widening the delimiter widens what counts as a table by rather more than it
// widens what is one.
func isCSV(data []byte) bool {
	prefix := data
	if len(prefix) > csvSniffBytes {
		prefix = prefix[:csvSniffBytes]
		// The cut lands mid-record, and a record cut in half is evidence of
		// nothing, so the last partial line is dropped. A first line longer
		// than the window is not a table anyone wrote.
		i := bytes.LastIndexByte(prefix, '\n')
		if i < 0 {
			return false
		}
		prefix = prefix[:i+1]
	}
	if !isText(prefix) {
		return false
	}

	r := csv.NewReader(bytes.NewReader(prefix))
	r.FieldsPerRecord = 0 // the first record fixes the count; a later disagreement is an error
	r.LazyQuotes = false  // an unbalanced quote is a malformed table, not a lazily written one
	r.ReuseRecord = true  // nothing here outlives the loop

	rows, fields := 0, 0
	for rows < csvMaxRows {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false
		}
		if rows == 0 {
			fields = len(rec)
		}
		rows++
	}
	return rows >= csvMinRows && fields >= csvMinFields
}

// isText reports whether b is plausibly text: valid UTF-8, with no NUL and no
// control character other than the three that appear in a text file.
//
// This is what keeps a binary format nobody recognised from being read as a
// table because it happened to contain commas in the right places.
func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, c := range b {
		if c >= 0x20 || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return false
	}
	return true
}
