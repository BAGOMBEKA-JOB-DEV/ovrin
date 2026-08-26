package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// Every test in this file is one of the attacks named in docs/threat-model.md
// T2 and T3. They assert two different things and both matter: that the
// parser terminates and does not panic, and that it fails closed rather than
// returning whatever the attacker's truncated document happened to say.

func TestOpenRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{
			name: "a file with no pdf header",
			data: []byte("this is not a pdf, it is a sentence\n"),
			want: ErrMalformed,
		},
		{
			name: "an empty file",
			data: nil,
			want: ErrMalformed,
		},
		{
			name: "a header and nothing else",
			data: []byte("%PDF-1.7\n"),
			want: ErrMalformed,
		},
		{
			name: "a document whose trailer names the standard security handler",
			data: buildPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
				"<< /Filter /Standard /V 4 /R 4 >>",
			}, "/Encrypt 4 0 R "),
			want: ErrEncrypted,
		},
		{
			name: "a document encrypted with a handler we cannot name",
			data: buildPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
				"<< /Filter /SomeVendorHandler >>",
			}, "/Encrypt 4 0 R "),
			want: ErrEncrypted,
		},
		{
			name: "a catalog that leads to no pages and no page objects exist",
			data: buildPDF([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [] /Count 0 >>",
			}, ""),
			want: ErrMalformed,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Open(tt.data, detect.Limits{}, nil)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Open = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestEncryptionErrorNamesNoDocumentBytes(t *testing.T) {
	t.Parallel()
	// The handler name is chosen by the document, so it must not be echoed.
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
		"<< /Filter /PatientNameJaneDoe >>",
	}, "/Encrypt 4 0 R ")
	_, err := Open(data, detect.Limits{}, nil)
	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("Open = %v, want ErrEncrypted", err)
	}
	if strings.Contains(err.Error(), "PatientName") {
		t.Errorf("error %q repeats a name the document chose", err)
	}
}

func TestUnsupportedFilterIsRefusedByName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		filter string
		want   string
	}{
		{"jbig2", "/JBIG2Decode", "JBIG2Decode"},
		{"jpx", "/JPXDecode", "JPXDecode"},
		{"dct", "/DCTDecode", "DCTDecode"},
		{"ccitt fax", "/CCITTFaxDecode", "CCITTFaxDecode"},
		{"a filter name the document invented", "/PatientNameJaneDoe", "unrecognised filter"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := onePage("", helvetica, "")
			data = bytes.Replace(data, []byte("<<  /Length"), []byte("<< /Filter "+tt.filter+" /Length"), 1)
			doc, err := Open(data, detect.Limits{}, nil)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			// A page whose only content stream cannot be decoded is an empty
			// page rather than a failed extraction, so it is the stream that
			// has to name the filter.
			if p, perr := doc.Page(1); perr == nil && len(p.Content.Words) != 0 {
				t.Errorf("page produced %d words from an undecodable stream", len(p.Content.Words))
			}
			st, ok := doc.object(4, doc.lim.Depth()).(*Stream)
			if !ok {
				t.Fatalf("object 4 is not a stream")
			}
			_, err = st.Decode()
			if !errors.Is(err, ErrUnsupportedFilter) {
				t.Fatalf("Decode = %v, want ErrUnsupportedFilter", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not say %q", err, tt.want)
			}
		})
	}
}

func TestCrossReferenceCycleTerminates(t *testing.T) {
	t.Parallel()
	// A trailer whose /Prev points at its own section. Following it naively
	// never returns.
	data := onePage("BT /F1 12 Tf 72 720 Td (ok) Tj ET", helvetica, "")
	i := bytes.LastIndex(data, []byte("startxref\n"))
	if i < 0 {
		t.Fatal("test file has no startxref")
	}
	var off int
	if _, err := fmt.Sscanf(string(data[i+len("startxref\n"):]), "%d", &off); err != nil {
		t.Fatalf("reading startxref: %v", err)
	}
	data = bytes.Replace(data, []byte("/Root 1 0 R >>"),
		[]byte(fmt.Sprintf("/Root 1 0 R /Prev %d >>", off)), 1)
	if got := words(openPage(t, data)); got != "ok" {
		t.Errorf("words = %q, want %q: a self-referential /Prev must be visited once", got, "ok")
	}
}

func TestPageTreeThousandsDeep(t *testing.T) {
	t.Parallel()
	const depth = 3000
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
	for i := 2; i < depth; i++ {
		objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", i+1))
	}
	objs = append(objs, "<< /Type /Page /MediaBox [0 0 612 792] >>")
	_, err := Open(buildPDF(objs, ""), detect.Limits{}, nil)
	if !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Open = %v, want a depth limit failure", err)
	}
}

func TestCyclicPageTreeTerminates(t *testing.T) {
	t.Parallel()
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [2 0 R 3 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " +
			"/Resources << /Font << " + helvetica + " >> >> >>",
		streamObj("", "BT /F1 12 Tf 72 720 Td (ok) Tj ET"),
	}, "")
	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if doc.NumPages() != 1 {
		t.Fatalf("NumPages = %d, want 1: the cycle is visited once", doc.NumPages())
	}
}

func TestSelfReferentialStreamLength(t *testing.T) {
	t.Parallel()
	// /Length that refers to the object it is the length of. Resolving it
	// requires the object, which requires the length.
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " +
			"/Resources << /Font << " + helvetica + " >> >> >>",
		"<< /Length 4 0 R >>\nstream\nBT /F1 12 Tf 72 720 Td (ok) Tj ET\nendstream",
	}, "")
	if got := words(openPage(t, data)); got != "ok" {
		t.Errorf("words = %q, want %q: the length is found by searching for endstream", got, "ok")
	}
}

func TestEnormousDeclaredLength(t *testing.T) {
	t.Parallel()
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " +
			"/Resources << /Font << " + helvetica + " >> >> >>",
		"<< /Length 9999999999999 >>\nstream\nBT /F1 12 Tf 72 720 Td (ok) Tj ET\nendstream",
	}, "")
	if got := words(openPage(t, data)); got != "ok" {
		t.Errorf("words = %q, want %q: a declared length is a hint, never a bound", got, "ok")
	}
}

func TestCrossReferenceOffsetsOutsideTheFile(t *testing.T) {
	t.Parallel()
	data := onePage("BT /F1 12 Tf 72 720 Td (recovered) Tj ET", helvetica, "")
	// Rewrite every entry to point far past the end of the file.
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		if len(line) == 20 && bytes.HasSuffix(line, []byte(" 00000 n ")) {
			lines[i] = []byte("9999999999 00000 n ")
		}
	}
	data = bytes.Join(lines, []byte("\n"))
	if got := words(openPage(t, data)); got != "recovered" {
		t.Errorf("words = %q, want %q: unusable offsets fall back to a scan", got, "recovered")
	}
}

func TestTruncatedFileNeverPanics(t *testing.T) {
	t.Parallel()
	full := onePage("BT /F1 12 Tf 72 720 Td (Hello World) Tj ET", helvetica, "")
	for cut := 0; cut < len(full); cut += 7 {
		doc, err := Open(full[:cut], detect.Limits{}, nil)
		if err != nil {
			continue
		}
		for n := 1; n <= doc.NumPages() && n <= 4; n++ {
			if _, err := doc.Page(n); err != nil {
				continue
			}
		}
	}
}

func TestObjectStreamCountDisagreesWithContent(t *testing.T) {
	t.Parallel()
	// The stream declares five hundred objects and holds two. Honouring the
	// declaration means reading the object data as an offset table.
	body := "1 0 2 40 "
	first := len(body)
	body += "<< /Type /Catalog /Pages 2 0 R >>        " +
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	data := buildPDF([]string{
		streamObj(fmt.Sprintf("/Type /ObjStm /N 500 /First %d", first), body),
		"<< /Type /Page /MediaBox [0 0 612 792] >>",
	}, "")
	// Object 1 is the stream itself; the catalog and pages tree live inside
	// it, so a plain xref cannot name them. The scan fallback finds the page.
	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open = %v, want ErrMalformed or success", err)
		}
		return
	}
	if doc.NumPages() < 1 {
		t.Errorf("NumPages = %d, want at least one page found by the scan", doc.NumPages())
	}
}

func TestObjectStreamOffsetsOutsideTheStream(t *testing.T) {
	t.Parallel()
	body := "1 999999 "
	first := len(body)
	body += "<< /Type /Catalog >>"
	data := buildPDF([]string{
		streamObj(fmt.Sprintf("/Type /ObjStm /N 1 /First %d", first), body),
		"<< /Type /Page /MediaBox [0 0 612 792] >>",
	}, "")
	if _, err := Open(data, detect.Limits{}, nil); err != nil && !errors.Is(err, ErrMalformed) {
		t.Fatalf("Open = %v, want ErrMalformed or success", err)
	}
}

// flated returns data compressed with zlib, which is what FlateDecode means.
func flated(t *testing.T, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("writing test stream: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing test stream: %v", err)
	}
	return buf.String()
}

func TestDeflateBombIsRefusedBeforeAllocation(t *testing.T) {
	t.Parallel()
	bomb := flated(t, bytes.Repeat([]byte{0}, 1<<20))
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>",
		streamObj("/Filter /FlateDecode", bomb),
	}, "")
	doc, err := Open(data, detect.Limits{MaxStreamBytes: 4096}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = doc.Page(1)
	if !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Page = %v, want a limit failure", err)
	}
}

func TestNestedDeflateBombIsRefused(t *testing.T) {
	t.Parallel()
	inner := flated(t, bytes.Repeat([]byte{0}, 1<<20))
	outer := flated(t, []byte(inner))
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>",
		streamObj("/Filter [/FlateDecode /FlateDecode]", outer),
	}, "")
	doc, err := Open(data, detect.Limits{MaxStreamBytes: 4096}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := doc.Page(1); !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Page = %v, want a limit failure", err)
	}
}

func TestCumulativeDecompressionBudgetIsShared(t *testing.T) {
	t.Parallel()
	// Twelve streams, each comfortably under the per-stream ceiling, whose
	// total is over the document ceiling. A per-stream limit alone lets this
	// through, which is why the counter is cumulative.
	const each = 8 << 10
	one := flated(t, bytes.Repeat([]byte(" "), each))
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"",
	}
	refs := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		objs = append(objs, streamObj("/Filter /FlateDecode", one))
		refs = append(refs, fmt.Sprintf("%d 0 R", len(objs)))
	}
	objs[2] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents [" +
		strings.Join(refs, " ") + "] >>"
	cum := detect.NewCounter(detect.LimitDecompressedBytes, 32<<10)
	doc, err := Open(buildPDF(objs, ""), detect.Limits{MaxStreamBytes: 16 << 10}, cum)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := doc.Page(1); !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Page = %v, want the cumulative budget to be exhausted", err)
	}
}

func TestDeeplyNestedObjectsRunOutOfBudget(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Junk " + deep + " >>",
		streamObj("", "BT /F1 12 Tf 72 720 Td (ok) Tj ET"),
	}, "")
	// The nesting must not exhaust the stack. Whether the page reads is not
	// the assertion; terminating without a crash is.
	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		return
	}
	if _, err := doc.Page(1); err != nil {
		return
	}
}

func TestSelfDrawingFormXObjectTerminates(t *testing.T) {
	t.Parallel()
	data := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources " +
			"<< /XObject << /X1 5 0 R >> >> /Contents 4 0 R >>",
		streamObj("", "/X1 Do"),
		streamObj("/Type /XObject /Subtype /Form /Resources << /XObject << /X1 5 0 R >> >>", "/X1 Do"),
	}, "")
	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := doc.Page(1); err != nil && !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Page = %v, want success or a depth limit", err)
	}
}

func TestPageLimitIsCheckedBeforePagesAreBuilt(t *testing.T) {
	t.Parallel()
	kids := make([]string, 0, 40)
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	for i := 0; i < 40; i++ {
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")
		kids = append(kids, fmt.Sprintf("%d 0 R", len(objs)))
	}
	objs[1] = "<< /Type /Pages /Kids [" + strings.Join(kids, " ") + "] /Count 40 >>"
	_, err := Open(buildPDF(objs, ""), detect.Limits{MaxPages: 10}, nil)
	if !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Open = %v, want a page limit failure", err)
	}
}

func TestObjectLimitIsCheckedBeforeEntriesAreBuilt(t *testing.T) {
	t.Parallel()
	// A cross-reference subsection declaring far more entries than the
	// ceiling allows, before any of them is inserted.
	data := []byte("%PDF-1.7\n" +
		"xref\n0 5000000\n" +
		"0000000000 65535 f \n" +
		"trailer\n<< /Size 5000000 /Root 1 0 R >>\nstartxref\n9\n%%EOF\n")
	if _, err := Open(data, detect.Limits{MaxObjects: 100}, nil); err == nil {
		t.Fatal("Open succeeded on a file declaring five million objects")
	}
}

func TestTextByteBudgetStopsExtraction(t *testing.T) {
	t.Parallel()
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 72 720 Td ")
	for i := 0; i < 4000; i++ {
		content.WriteString("(wordwordword) Tj ")
	}
	content.WriteString("ET")
	doc, err := Open(onePage(content.String(), helvetica, ""), detect.Limits{MaxTextBytes: 1024}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := doc.Page(1); !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("Page = %v, want the text budget to be exhausted", err)
	}
}

func TestPageNumberOutOfRange(t *testing.T) {
	t.Parallel()
	doc, err := Open(onePage("", helvetica, ""), detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, n := range []int{-1, 0, 2, 1 << 30} {
		if _, err := doc.Page(n); !errors.Is(err, ErrMalformed) {
			t.Errorf("Page(%d) = %v, want ErrMalformed", n, err)
		}
	}
}
