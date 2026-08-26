package office

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// These three formats take the fast exact path: no renderer, no OCR provider,
// no model call to acquire content (ADR-0012). Whatever this package spends is
// therefore the whole acquisition bill for a spreadsheet or a Word document,
// and it is worth knowing how it compares with the PDF parser next door, which
// is doing far more work to produce the same normalise.Page.
//
// The three are not alike. DOCX and XLSX are zip containers, so most of what
// they cost is inflating and parsing XML — and XLSX has a second part to read,
// the shared string table, which most of a real workbook's text lives in. CSV
// is not a container at all: it is a scan of the bytes, and it should be very
// much cheaper per byte than either.
//
// Fixtures are built in memory rather than committed, for the reason the tests
// in this package give: the XML is the interesting part, and a committed binary
// would hide it from a reviewer (rules §3).

// Kept reachable so the compiler cannot delete the extraction.
var benchExtracted *Document

// benchZip builds a fixture archive. It is a benchmark's version of the test
// helper alongside it, taking a testing.TB so it can be called from either.
func benchZip(b *testing.B, entries ...benchEntry) []byte {
	b.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			b.Fatalf("creating fixture entry: %v", err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			b.Fatalf("writing fixture entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		b.Fatalf("closing fixture archive: %v", err)
	}
	return buf.Bytes()
}

// benchEntry is one member of a fixture archive.
type benchEntry struct {
	name string
	body string
}

// benchDOCX builds a Word document of rows paragraphs, each a run of a
// sentence, plus a table — which is the shape a delivery note or a contract
// schedule takes, and the shape that exercises paragraph and row structure
// rather than only the run reader.
func benchDOCX(b *testing.B, rows int) []byte {
	b.Helper()
	var body strings.Builder
	body.WriteString(`<w:p><w:r><w:t>Nakawa Stationers Limited</w:t></w:r></w:p>`)
	body.WriteString(`<w:p><w:r><w:t>Invoice INV-2026-0417</w:t></w:r>` +
		`<w:r><w:t xml:space="preserve"> issued 14 March 2026</w:t></w:r></w:p>`)
	body.WriteString(`<w:tbl>`)
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&body,
			`<w:tr><w:tc><w:p><w:r><w:t>A4 paper 80gsm %02d</w:t></w:r></w:p></w:tc>`+
				`<w:tc><w:p><w:r><w:t>40</w:t></w:r></w:p></w:tc>`+
				`<w:tc><w:p><w:r><w:t>%d</w:t></w:r></w:p></w:tc></w:tr>`, i, 500000+i*13)
	}
	body.WriteString(`</w:tbl>`)
	body.WriteString(`<w:p><w:r><w:t>Total UGX 1,463,200</w:t></w:r></w:p>`)

	doc := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document ` + wordNS + `><w:body>` + body.String() + `</w:body></w:document>`
	return benchZip(b,
		benchEntry{name: contentTypesName, body: contentTypes},
		benchEntry{name: docxBody, body: doc},
	)
}

// benchXLSX builds a workbook of one sheet with rows rows, where the text is in
// a shared string table. That is how Excel actually stores repeated strings, so
// a fixture with inline text would measure a path most real workbooks do not
// take.
func benchXLSX(b *testing.B, rows int) []byte {
	b.Helper()

	shared := []string{"Description", "Quantity", "Amount"}
	for i := 0; i < rows; i++ {
		shared = append(shared, fmt.Sprintf("A4 paper 80gsm %02d", i))
	}

	var sst strings.Builder
	fmt.Fprintf(&sst, `<?xml version="1.0"?><sst %s count="%d">`, sheetNS, len(shared))
	for _, s := range shared {
		fmt.Fprintf(&sst, `<si><t>%s</t></si>`, s)
	}
	sst.WriteString(`</sst>`)

	var sheet strings.Builder
	sheet.WriteString(`<row r="1"><c r="A1" t="s"><v>0</v></c>` +
		`<c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row>`)
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&sheet,
			`<row r="%d"><c r="A%d" t="s"><v>%d</v></c>`+
				`<c r="B%d"><v>40</v></c><c r="C%d"><v>%d</v></c></row>`,
			i+2, i+2, i+3, i+2, i+2, 500000+i*13)
	}

	wb := `<?xml version="1.0"?><workbook ` + sheetNS + `><sheets>` +
		`<sheet name="Items" sheetId="1" r:id="rId1"/></sheets></workbook>`
	rels := `<?xml version="1.0"?><Relationships ` + relsNS + `>` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" ` +
		`Target="worksheets/sheet1.xml"/></Relationships>`

	return benchZip(b,
		benchEntry{name: contentTypesName, body: contentTypes},
		benchEntry{name: xlsxWorkbook, body: wb},
		benchEntry{name: xlsxWorkbookRels, body: rels},
		benchEntry{name: xlsxSharedString, body: sst.String()},
		benchEntry{
			name: xlsxSheetPrefix + "sheet1.xml",
			body: `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData>` +
				sheet.String() + `</sheetData></worksheet>`,
		},
	)
}

// benchCSV is a ledger export, the commonest thing anybody points a CSV
// extractor at. Quoted fields containing commas are included because the
// quoting rules are what make CSV parsing more than a split.
func benchCSV(rows int) []byte {
	var b strings.Builder
	b.WriteString("invoice,issued,vendor,currency,net,tax,total\n")
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "INV-2026-%04d,2026-03-14,%q,UGX,%d,223200,%d\n",
			i, "Nakawa Stationers Limited, Kampala", 1240000+i, 1463200+i)
	}
	return []byte(b.String())
}

// BenchmarkExtract measures the three formats against each other over the same
// quantity of content, since the point of this package is that all three arrive
// at internal/normalise in the same shape and only the reading differs.
func BenchmarkExtract(b *testing.B) {
	lim := detect.DefaultLimits()

	const rows = 200

	cases := []struct {
		name string
		kind detect.Kind
		data func(*testing.B) []byte
	}{
		{"docx", detect.KindDOCX, func(b *testing.B) []byte { return benchDOCX(b, rows) }},
		{"xlsx", detect.KindXLSX, func(b *testing.B) []byte { return benchXLSX(b, rows) }},
		{"csv", detect.KindCSV, func(*testing.B) []byte { return benchCSV(rows) }},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			data := c.data(b)
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// A fresh counter per iteration: the decompression budget
				// belongs to a document, and one shared across iterations
				// would run out partway through and change what is measured.
				d, err := Extract(data, c.kind, lim,
					detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes))
				if err != nil {
					b.Fatalf("Extract %s: %v", c.name, err)
				}
				benchExtracted = d
			}
		})
	}
}

// BenchmarkExtractBySize measures each format against document length, which is
// what a caller needs to know before pointing this at a spreadsheet export with
// fifty thousand rows in it. The container formats have a fixed cost — opening
// the zip, reading the content types, the workbook and its relationships —
// that a small document pays in full, and this is where that floor is visible.
func BenchmarkExtractBySize(b *testing.B) {
	lim := detect.DefaultLimits()

	for _, rows := range []int{10, 200, 2000} {
		rows := rows
		b.Run(fmt.Sprintf("docx_rows_%d", rows), func(b *testing.B) {
			data := benchDOCX(b, rows)
			benchmarkExtract(b, data, detect.KindDOCX, lim)
		})
		b.Run(fmt.Sprintf("xlsx_rows_%d", rows), func(b *testing.B) {
			data := benchXLSX(b, rows)
			benchmarkExtract(b, data, detect.KindXLSX, lim)
		})
		b.Run(fmt.Sprintf("csv_rows_%d", rows), func(b *testing.B) {
			benchmarkExtract(b, benchCSV(rows), detect.KindCSV, lim)
		})
	}
}

// benchmarkExtract is the timed loop the size sweep repeats nine times.
func benchmarkExtract(b *testing.B, data []byte, kind detect.Kind, lim detect.Limits) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := Extract(data, kind, lim,
			detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes))
		if err != nil {
			b.Fatalf("Extract: %v", err)
		}
		benchExtracted = d
	}
}
