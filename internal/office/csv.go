package office

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// utf8BOM is the byte order mark a spreadsheet writes at the front of a CSV so
// that another spreadsheet opens it as UTF-8.
//
// It is removed rather than carried, because U+FEFF is one of the code points
// internal/normalise reports as a zero-width finding. A byte order mark is a
// convention, not an attack, and leaving it in would put a finding on the
// first field of a large share of the CSVs anybody exports — which trains an
// operator to ignore the one that matters
// (docs/adr/0017-untrusted-document-content.md).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ExtractCSV reads a comma-separated file as one page, one line per record and
// one word per field.
//
// The delimiter is a comma and only a comma. Semicolon-separated files from
// European spreadsheets and tab-separated files are both real and neither is
// read here, for the same reason internal/detect declines to recognise them:
// widening the delimiter widens what counts as a table by rather more than it
// widens what is one. A file this package would need a different delimiter for
// is a file that never arrives, because detection refused it at the door.
//
// No cumulative decompression budget is taken, because nothing here is
// decompressed. Extracted text is charged to the text ceiling.
func ExtractCSV(data []byte, lim detect.Limits) (doc *Document, err error) {
	defer recovered(&doc, &err)
	lim = lim.Normalised()

	data = bytes.TrimPrefix(data, utf8BOM)
	text := detect.NewCounter(detect.LimitTextBytes, lim.MaxTextBytes)

	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = ','
	// The first record does not fix the count. internal/detect judged this
	// file a table from at most the first 64 KiB, so a ragged record further
	// in is a file it already accepted; refusing the whole document over one
	// short line would discard everything before it (docs/rules.md §2.6, in
	// spirit — a bad record is not a bad document).
	r.FieldsPerRecord = -1
	// An unbalanced quote is left an error, matching the strictness detection
	// applied. Under LazyQuotes the parse becomes a guess about where a field
	// ends, and a guess about field boundaries is a guess about values.
	r.LazyQuotes = false
	// The record slice is reused; the strings in it are not, so copying a
	// field into a word is safe.
	r.ReuseRecord = true

	b := newPageBuilder(1, text)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// csv's message quotes neither the field nor the line's content,
			// but it does carry a line and column, and it is another
			// package's wording. It is classified rather than repeated
			// (docs/rules.md §2.2).
			return nil, malformedPage("record", PartUnknown, 1, "record could not be parsed")
		}
		for _, f := range rec {
			if err := text.Add(int64(len(f))); err != nil {
				return nil, err
			}
			if err := b.addWord(f); err != nil {
				return nil, err
			}
		}
		b.endLine()
	}

	return &Document{
		Kind:  detect.KindCSV,
		Pages: []normalise.Page{b.page()},
	}, nil
}
