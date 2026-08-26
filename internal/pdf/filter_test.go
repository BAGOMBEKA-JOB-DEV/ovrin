package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// decodeThrough runs data through the filter chain named in dict, using a
// document just real enough to own the stream.
func decodeThrough(t *testing.T, dict, data string, lim detect.Limits) ([]byte, error) {
	t.Helper()
	file := buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
		streamObj(dict, data),
	}, "")
	doc, err := Open(file, lim, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st, ok := doc.object(4, doc.lim.Depth()).(*Stream)
	if !ok {
		t.Fatalf("object 4 is not a stream")
	}
	return st.Decode()
}

func TestFilters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dict string
		data string
		want string
	}{
		{
			name: "no filter is the identity",
			dict: "",
			data: "plain bytes",
			want: "plain bytes",
		},
		{
			name: "ascii hex, with white space and the end marker",
			dict: "/Filter /ASCIIHexDecode",
			data: "48 65 6C\n6C 6F>",
			want: "Hello",
		},
		{
			name: "ascii hex with an odd final digit is padded with zero",
			dict: "/Filter /ASCIIHexDecode",
			data: "414>",
			want: "A@",
		},
		{
			name: "ascii85, with the leading and trailing markers",
			dict: "/Filter /ASCII85Decode",
			data: "<~87cURD]i,\"Ebo80~>",
			want: "Hello World!",
		},
		{
			name: "ascii85 with a partial final group",
			dict: "/Filter /ASCII85Decode",
			data: "87cURD]j7BEbo7~>",
			want: "Hello world",
		},
		{
			name: "ascii85 z stands for four zero bytes",
			dict: "/Filter /ASCII85Decode",
			data: "z~>",
			want: "\x00\x00\x00\x00",
		},
		{
			name: "run length, a literal run then a repeat then the end marker",
			dict: "/Filter /RunLengthDecode",
			data: "\x02abc\xfdX\x80",
			want: "abcXXXX",
		},
		{
			name: "the abbreviated filter names mean the same thing",
			dict: "/Filter /AHx",
			data: "4869>",
			want: "Hi",
		},
		{
			name: "a chain is applied in order",
			dict: "/Filter [/ASCIIHexDecode /RunLengthDecode]",
			data: "02616263fd5880>",
			want: "abcXXXX",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeThrough(t, tt.dict, tt.data, detect.Limits{})
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Decode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlateDecodeAcceptsBothFramings(t *testing.T) {
	t.Parallel()
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	if _, err := zw.Write([]byte("framed as zlib")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := decodeThrough(t, "/Filter /FlateDecode", zbuf.String(), detect.Limits{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(got) != "framed as zlib" {
		t.Errorf("Decode = %q", got)
	}
}

func TestLZWDecode(t *testing.T) {
	t.Parallel()
	t.Run("the specification's own worked example", func(t *testing.T) {
		t.Parallel()
		// PDF 32000-1 §7.4.4.2's worked example, whose plaintext is
		// "-----A---B". It is short enough that the code width never grows,
		// so it says nothing about early change — that is the next case's
		// job — but it does check the clear code and the two forms of table
		// lookup, including the code that is one past the table.
		encoded := []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}
		want := []byte("-----A---B")
		got, err := lzwDecode(encoded, true, newSink(detect.Limits{}, nil))
		if err != nil {
			t.Fatalf("lzwDecode: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("lzwDecode = % x, want % x", got, want)
		}
	})

	t.Run("without early change it agrees with compress/lzw", func(t *testing.T) {
		t.Parallel()
		// The standard library implements the variant PDF selects with
		// EarlyChange 0. Agreeing with it on input long enough to grow the
		// code width several times is the check that the table management
		// here is right; the early-change path differs from it by exactly one
		// entry, which is the whole reason this decoder exists.
		var plain bytes.Buffer
		for i := 0; i < 6000; i++ {
			fmt.Fprintf(&plain, "row %d of a repetitive table\n", i%700)
		}
		var enc bytes.Buffer
		w := lzw.NewWriter(&enc, lzw.MSB, 8)
		if _, err := w.Write(plain.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := lzwDecode(enc.Bytes(), false, newSink(detect.Limits{}, nil))
		if err != nil {
			t.Fatalf("lzwDecode: %v", err)
		}
		if !bytes.Equal(got, plain.Bytes()) {
			t.Errorf("lzwDecode produced %d bytes, want %d", len(got), plain.Len())
		}
	})

	t.Run("a code outside the table stops rather than guesses", func(t *testing.T) {
		t.Parallel()
		// 0x80 0x18 selects code 256 (clear) then a code far beyond the
		// table. Nothing legitimate follows, so nothing is invented.
		got, err := lzwDecode([]byte{0xFF, 0xFF, 0xFF, 0xFF}, true, newSink(detect.Limits{}, nil))
		if err != nil {
			t.Fatalf("lzwDecode: %v", err)
		}
		if len(got) > 4 {
			t.Errorf("lzwDecode invented %d bytes from four bytes of nonsense", len(got))
		}
	})
}

func TestStreamDataInAnExternalFileIsRefused(t *testing.T) {
	t.Parallel()
	// Nothing a document references is ever fetched (docs/rules.md §7.4).
	_, err := decodeThrough(t, "/F (/etc/passwd)", "ignored", detect.Limits{})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Decode = %v, want ErrMalformed", err)
	}
}

func TestFilterOutputIsBoundedBeforeAllocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dict string
		data string
	}{
		{"run length expands a repeat", "/Filter /RunLengthDecode", strings.Repeat("\x81X", 4000)},
		{"ascii hex expands nothing but is still counted", "/Filter /ASCIIHexDecode", strings.Repeat("41", 4000)},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeThrough(t, tt.dict, tt.data, detect.Limits{MaxStreamBytes: 512})
			if !errors.Is(err, detect.ErrLimitExceeded) {
				t.Fatalf("Decode = %v, want a limit failure", err)
			}
		})
	}
}

// pngUp encodes rows with the PNG Up predictor, which is what predictor 12
// means and what a cross-reference stream is almost always written with.
func pngUp(rows [][]byte) []byte {
	var out bytes.Buffer
	prev := make([]byte, len(rows[0]))
	for _, row := range rows {
		out.WriteByte(2)
		for i := range row {
			out.WriteByte(row[i] - prev[i])
		}
		prev = row
	}
	return out.Bytes()
}

// buildXrefStreamPDF assembles a document whose structure lives in an object
// stream and whose cross-reference information is a cross-reference stream,
// optionally with a PNG predictor. This is the shape every modern generator
// produces, and none of it is reachable through a classic xref table.
func buildXrefStreamPDF(t *testing.T, predictor bool) []byte {
	t.Helper()
	inner := []struct {
		num  int
		body string
	}{
		{2, "<< /Type /Catalog /Pages 3 0 R >>"},
		{3, "<< /Type /Pages /Kids [4 0 R] /Count 1 >>"},
		{4, "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 612 792] /Resources " +
			"<< /Font << " + helvetica + " >> >> /Contents 5 0 R >>"},
	}
	var pairs, bodies bytes.Buffer
	for _, o := range inner {
		fmt.Fprintf(&pairs, "%d %d ", o.num, bodies.Len())
		bodies.WriteString(o.body)
		bodies.WriteByte(' ')
	}
	first := pairs.Len()
	objstm := pairs.String() + bodies.String()

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	off := make(map[int]int)
	write := func(num int, body string) {
		off[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	write(1, streamObj(fmt.Sprintf("/Type /ObjStm /N %d /First %d", len(inner), first), objstm))
	write(5, streamObj("", "BT /F1 12 Tf 72 720 Td (compressed) Tj ET"))

	xrefOff := buf.Len()
	entry := func(kind byte, f2 uint32, f3 uint16) []byte {
		return []byte{kind,
			byte(f2 >> 24), byte(f2 >> 16), byte(f2 >> 8), byte(f2),
			byte(f3 >> 8), byte(f3)}
	}
	rows := [][]byte{
		entry(0, 0, 0),
		entry(1, uint32(off[1]), 0),
		entry(2, 1, 0),
		entry(2, 1, 1),
		entry(2, 1, 2),
		entry(1, uint32(off[5]), 0),
		entry(1, uint32(xrefOff), 0),
	}
	var raw []byte
	dict := "/Type /XRef /Size 7 /W [1 4 2] /Root 2 0 R"
	if predictor {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write(pngUp(rows)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		raw = z.Bytes()
		dict += " /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 7 >>"
	} else {
		for _, r := range rows {
			raw = append(raw, r...)
		}
	}
	write(6, streamObj(dict, string(raw)))
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func TestCrossReferenceStreamAndObjectStream(t *testing.T) {
	t.Parallel()
	for _, predictor := range []bool{false, true} {
		predictor := predictor
		name := "raw entries"
		if predictor {
			name = "flate with a png predictor"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := openPage(t, buildXrefStreamPDF(t, predictor))
			if got := words(p); got != "compressed" {
				t.Errorf("words = %q, want %q", got, "compressed")
			}
		})
	}
}
