package detect

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Detection is stage 1 and every document pays for it, including the ones that
// are refused a moment later. It is also the only stage that runs before a
// limit has been applied to anything, so its cost is the cost of touching
// untrusted bytes: whatever it allocates, it allocates on behalf of an input
// nobody has vouched for yet. A regression here is a regression on the cheapest
// path in the library and on the most exposed one at the same time.
//
// The three signature formats should be very close to free — a signature is a
// handful of bytes at a known offset. The two container formats are the ones
// worth watching, because reaching an answer means opening a zip and inflating
// a part, and CSV is worth watching because it has no signature at all and is
// decided by reading the shape of the text.

// These keep a benchmarked result reachable so that the compiler cannot delete
// the work that produced it.
var (
	benchKind Kind
	benchDoc  *Document
)

// corpusFile reads a committed corpus document, or skips the benchmark naming
// what was missing. The corpus is the closest thing this package has to real
// input: a signature test can be written from the specification, but the cost
// of identifying a PDF depends on how much junk precedes its header and how
// far into the trailer the encryption check has to read.
func corpusFile(b *testing.B, name string) []byte {
	b.Helper()
	path := "../../eval/corpus/" + name
	data, err := os.ReadFile(path)
	if err != nil {
		b.Skipf("corpus document missing: %s: %v", path, err)
	}
	return data
}

// benchDOCX builds a Word package whose content types part names it, which is
// the only part detection reads. The body is present and of a realistic size so
// that the zip central directory has something to walk past.
func benchDOCX(b *testing.B) []byte {
	b.Helper()
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><w:document><w:body>`)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&body, `<w:p><w:r><w:t>Invoice line %d, forty pounds and no pence.</w:t></w:r></w:p>`, i)
	}
	body.WriteString(`</w:body></w:document>`)
	return buildZip(b,
		zipEntry{name: contentTypesName, body: docxContentTypes},
		zipEntry{name: "word/document.xml", body: body.String()},
	)
}

// benchXLSX is the spreadsheet equivalent: the same content types lookup over
// an archive with a workbook and a sheet in it.
func benchXLSX(b *testing.B) []byte {
	b.Helper()
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0"?><worksheet><sheetData>`)
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&sheet, `<row r="%d"><c r="A%d" t="inlineStr"><is><t>Row %d</t></is></c>`+
			`<c r="B%d"><v>%d.50</v></c></row>`, i, i, i, i, i*17)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	return buildZip(b,
		zipEntry{name: contentTypesName, body: xlsxContentTypes},
		zipEntry{name: "xl/workbook.xml", body: `<?xml version="1.0"?><workbook><sheets/></workbook>`},
		zipEntry{name: "xl/worksheets/sheet1.xml", body: sheet.String()},
	)
}

// benchCSV is a ledger export of the size a finance system produces. CSV is
// identified by reading rows and checking that they agree about how many
// columns they have, so unlike every other kind here its cost is a function of
// how much of the file the heuristic reads.
func benchCSV() []byte {
	var b strings.Builder
	b.WriteString("invoice,issued,vendor,currency,net,tax,total\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "INV-2026-%04d,2026-03-14,Nakawa Stationers Limited,UGX,%d,%d,%d\n",
			i, 1240000+i, 223200, 1463200+i)
	}
	return []byte(b.String())
}

// BenchmarkIdentify measures identification of bytes already in memory, one
// sub-benchmark per format, because the formats do genuinely different amounts
// of work and a single average over them would hide which one regressed.
func BenchmarkIdentify(b *testing.B) {
	lim := DefaultLimits()

	cases := []struct {
		name string
		data func(*testing.B) []byte
	}{
		{"pdf", func(b *testing.B) []byte { return corpusFile(b, "invoices/001.pdf") }},
		{"png", func(b *testing.B) []byte { return corpusFile(b, "invoices/003.png") }},
		{"jpeg", func(b *testing.B) []byte { return corpusFile(b, "invoices/004.jpg") }},
		{"docx", benchDOCX},
		{"xlsx", benchXLSX},
		{"csv", func(*testing.B) []byte { return benchCSV() }},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			data := c.data(b)
			// No SetBytes: identification reads at most the first kibibyte of
			// a signature format, so a throughput figure over the whole file
			// would say more about the fixture than about the code.
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k, err := Identify(data, lim)
				if err != nil {
					b.Fatalf("Identify: %v", err)
				}
				benchKind = k
			}
		})
	}
}

// BenchmarkDetect measures the whole of stage 1 rather than only the decision:
// the source is read under the source-byte limit, and the bytes that were read
// are handed on. It is reported per source kind because that reading is the
// part that differs — a byte slice is already there, a reader is copied
// through the limit, and a file is opened as well.
//
// The gap between this and BenchmarkIdentify/pdf is what the caller pays for
// getting bytes to the door, and it is expected to dominate: identification
// itself reads at most the first kibibyte.
func BenchmarkDetect(b *testing.B) {
	lim := DefaultLimits()
	data := corpusFile(b, "invoices/001.pdf")

	// A temporary copy, so the file source is measured without depending on a
	// path inside the repository staying where it is.
	path := writeTemp(b, data)

	cases := []struct {
		name string
		src  func() Source
	}{
		{"bytes", func() Source { return Bytes(data) }},
		{"reader", func() Source { return Reader(bytes.NewReader(data)) }},
		{"file", func() Source { return File(path) }},
	}

	for _, c := range cases {
		c := c
		b.Run(c.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, err := Detect(ctx, c.src(), lim)
				if err != nil {
					b.Fatalf("Detect: %v", err)
				}
				benchDoc = d
			}
		})
	}
}

// writeTemp puts the fixture somewhere the file source can open it. The
// directory is removed by the testing package when the benchmark ends.
func writeTemp(b *testing.B, data []byte) string {
	b.Helper()
	path := b.TempDir() + "/document.pdf"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatalf("writing the fixture: %v", err)
	}
	return path
}
