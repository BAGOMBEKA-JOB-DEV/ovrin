package detect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The smallest byte sequences that are honestly each format. They are built
// here rather than committed because what is under test is the signature, and
// a signature is the same bytes whoever produced the file.
var (
	samplePNG      = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R'}...)
	sampleJPEG     = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, []byte("JFIF\x00")...)
	sampleTIFFLE   = []byte{'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00}
	sampleTIFFBE   = []byte{'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08}
	sampleWebP     = append(append([]byte("RIFF"), []byte{0x1A, 0, 0, 0}...), []byte("WEBPVP8 ")...)
	sampleRIFFWave = append(append([]byte("RIFF"), []byte{0x1A, 0, 0, 0}...), []byte("WAVEfmt ")...)
	sampleCSV      = []byte("invoice,total,currency\nINV-1,10.00,GBP\nINV-2,20.00,GBP\n")
)

const (
	samplePDF = "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer\n<</Size 2/Root 1 0 R>>\nstartxref\n9\n%%EOF\n"

	sampleEncryptedPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer\n<</Size 2/Root 1 0 R/Encrypt 9 0 R>>\nstartxref\n9\n%%EOF\n"

	// /Encrypt inside an object rather than in the trailer. A document is
	// encrypted because its trailer says so, not because the word appears.
	sampleEncryptWordPDF = "%PDF-1.4\n1 0 obj<</Note(/Encrypt 9 0 R)>>endobj\ntrailer\n<</Size 2/Root 1 0 R>>\nstartxref\n9\n%%EOF\n"
)

func TestIdentify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     []byte
		wantKind Kind
		wantErr  error
	}{
		{name: "pdf header at offset zero", data: []byte(samplePDF), wantKind: KindPDF},
		{
			name:     "pdf header after junk a mail gateway prepended",
			data:     append(bytes.Repeat([]byte(" "), 300), samplePDF...),
			wantKind: KindPDF,
		},
		{
			name:    "pdf header beyond the first kibibyte is not a pdf",
			data:    append(bytes.Repeat([]byte("x"), 2000), samplePDF...),
			wantErr: ErrUnsupportedFormat,
		},
		{name: "png", data: samplePNG, wantKind: KindPNG},
		{name: "jpeg", data: sampleJPEG, wantKind: KindJPEG},
		{name: "tiff little endian", data: sampleTIFFLE, wantKind: KindTIFF},
		{name: "tiff big endian", data: sampleTIFFBE, wantKind: KindTIFF},
		{name: "webp", data: sampleWebP, wantKind: KindWebP},
		{name: "riff that is not webp", data: sampleRIFFWave, wantErr: ErrUnsupportedFormat},
		{name: "empty source", data: nil, wantErr: ErrUnsupportedFormat},
		{name: "single byte", data: []byte{'%'}, wantErr: ErrUnsupportedFormat},
		{name: "truncated png signature", data: samplePNG[:4], wantErr: ErrUnsupportedFormat},
		{name: "truncated pdf signature", data: []byte("%PD"), wantErr: ErrUnsupportedFormat},
		{name: "truncated jpeg signature", data: sampleJPEG[:2], wantErr: ErrUnsupportedFormat},
		{name: "csv with a header and two rows", data: sampleCSV, wantKind: KindCSV},
		{
			name:     "csv with rows of differing length is not csv",
			data:     []byte("a,b,c\nd,e\nf,g,h\n"),
			wantErr:  ErrUnsupportedFormat,
			wantKind: KindUnknown,
		},
		{name: "single column is not csv", data: []byte("alpha\nbeta\ngamma\n"), wantErr: ErrUnsupportedFormat},
		{name: "one line is not csv", data: []byte("alpha,beta,gamma\n"), wantErr: ErrUnsupportedFormat},
		{name: "prose is not csv", data: []byte("Dear Sir,\nPlease find enclosed.\n"), wantErr: ErrUnsupportedFormat},
		{
			name:    "binary containing commas is not csv",
			data:    []byte{0x01, ',', 0x02, '\n', 0x03, ',', 0x04, '\n'},
			wantErr: ErrUnsupportedFormat,
		},
		{
			name:    "utf-16 text is not csv",
			data:    []byte{'a', 0, ',', 0, 'b', 0, '\n', 0, 'c', 0, ',', 0, 'd', 0, '\n', 0},
			wantErr: ErrUnsupportedFormat,
		},
		{name: "unencrypted pdf trailer", data: []byte(samplePDF), wantKind: KindPDF},
		{name: "pdf trailer naming an encryption dictionary", data: []byte(sampleEncryptedPDF), wantErr: ErrEncrypted},
		{name: "the word encrypt outside the trailer is not encryption", data: []byte(sampleEncryptWordPDF), wantKind: KindPDF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Identify(tc.data, DefaultLimits())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Identify: got error %v, want one wrapping %v", err, tc.wantErr)
				}
				if got != KindUnknown {
					t.Errorf("Identify: got kind %s with an error, want unknown", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Identify: unexpected error: %v", err)
			}
			if got != tc.wantKind {
				t.Errorf("Identify: got kind %s, want %s", got, tc.wantKind)
			}
		})
	}
}

func TestDetectReadsEverySourceKindTheSameWay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "document.bin")
	if err := os.WriteFile(path, []byte(samplePDF), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name string
		src  Source
	}{
		{name: "io.Reader", src: Reader(strings.NewReader(samplePDF))},
		{name: "byte slice", src: Bytes([]byte(samplePDF))},
		{name: "file path", src: File(path)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc, err := Detect(context.Background(), tc.src, DefaultLimits())
			if err != nil {
				t.Fatalf("Detect: unexpected error: %v", err)
			}
			if doc.Kind != KindPDF {
				t.Errorf("Detect: got kind %s, want %s", doc.Kind, KindPDF)
			}
			if doc.Size() != int64(len(samplePDF)) {
				t.Errorf("Detect: got size %d, want %d", doc.Size(), len(samplePDF))
			}
			if doc.Decompressed == nil {
				t.Error("Detect: nil decompression budget, want one later stages can spend")
			}
		})
	}
}

func TestDetectTrustsContentNotTheFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		data     []byte
		wantKind Kind
	}{
		{name: "jpeg named as a pdf", filename: "invoice.pdf", data: sampleJPEG, wantKind: KindJPEG},
		{name: "pdf named as a jpeg", filename: "photo.jpg", data: []byte(samplePDF), wantKind: KindPDF},
		{name: "png named as a csv", filename: "ledger.csv", data: samplePNG, wantKind: KindPNG},
		{name: "csv named as a docx", filename: "report.docx", data: sampleCSV, wantKind: KindCSV},
		{name: "pdf with no extension at all", filename: "attachment", data: []byte(samplePDF), wantKind: KindPDF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), tc.filename)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			doc, err := Detect(context.Background(), File(path), DefaultLimits())
			if err != nil {
				t.Fatalf("Detect: unexpected error: %v", err)
			}
			if doc.Kind != tc.wantKind {
				t.Errorf("Detect: got kind %s, want %s", doc.Kind, tc.wantKind)
			}
		})
	}
}

func TestDetectPageCountIsNeverGuessed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      []byte
		wantPages int
	}{
		{name: "png is structurally one page", data: samplePNG, wantPages: 1},
		{name: "jpeg is structurally one page", data: sampleJPEG, wantPages: 1},
		{name: "webp is structurally one page", data: sampleWebP, wantPages: 1},
		{name: "pdf page count needs a parse", data: []byte(samplePDF), wantPages: 0},
		{name: "tiff may hold many pages", data: sampleTIFFLE, wantPages: 0},
		{name: "csv page count needs a parse", data: sampleCSV, wantPages: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc, err := Detect(context.Background(), Bytes(tc.data), DefaultLimits())
			if err != nil {
				t.Fatalf("Detect: unexpected error: %v", err)
			}
			if doc.Pages != tc.wantPages {
				t.Errorf("Detect: got %d pages, want %d", doc.Pages, tc.wantPages)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "the zero value names itself", kind: KindUnknown, want: "unknown"},
		{name: "a known kind is its own name", kind: KindPDF, want: "pdf"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.kind.String(); got != tc.want {
				t.Errorf("String: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIdentifyLargeInputs(t *testing.T) {
	t.Parallel()

	// A row that repeats past the window detection judges CSV from, so the
	// prefix has to be cut back to a whole record.
	bigCSV := strings.Repeat("invoice,total,currency\n", 8000)

	tests := []struct {
		name     string
		data     []byte
		wantKind Kind
		wantErr  error
	}{
		{name: "a csv longer than the sniffing window", data: []byte(bigCSV), wantKind: KindCSV},
		{
			name:    "one line longer than the sniffing window is not a table",
			data:    []byte(strings.Repeat("a,", 40000) + "b\n"),
			wantErr: ErrUnsupportedFormat,
		},
		{
			name:     "a pdf whose trailer is far past the tail window",
			data:     append([]byte(samplePDF[:20]), append(bytes.Repeat([]byte("x"), 64<<10), []byte("\ntrailer\n<</Encrypt 9 0 R>>\n%%EOF\n")...)...),
			wantKind: KindUnknown,
			wantErr:  ErrEncrypted,
		},
		{
			name:     "a pdf whose encryption key is before the tail window",
			data:     append([]byte(samplePDF[:20]), append([]byte("\ntrailer\n<</Encrypt 9 0 R>>\n"), bytes.Repeat([]byte("x"), 64<<10)...)...),
			wantKind: KindPDF,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Identify(tc.data, DefaultLimits())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Identify: got error %v, want one wrapping %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Identify: unexpected error: %v", err)
			}
			if got != tc.wantKind {
				t.Errorf("Identify: got kind %s, want %s", got, tc.wantKind)
			}
		})
	}
}

func TestPDFEncryptionKeyMustBeFollowedByAReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		trailer string
		want    bool
	}{
		{name: "an indirect reference", trailer: "trailer<</Encrypt 9 0 R>>", want: true},
		{name: "no value at all", trailer: "trailer<</Encrypt>>", want: false},
		{name: "a name rather than a reference", trailer: "trailer<</Encrypt/None>>", want: false},
		{name: "the key with no space before the object number", trailer: "trailer<</Encrypt9 0 R>>", want: false},
		{name: "a false match before a real one", trailer: "trailer<</Encrypt/None/Encrypt 9 0 R>>", want: true},
		{name: "two false matches", trailer: "trailer<</Encrypt/None/Encrypt(x)>>", want: false},
		{name: "the key at the very end", trailer: "trailer<</Encrypt", want: false},
		{name: "no key", trailer: "trailer<</Root 1 0 R>>", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := []byte("%PDF-1.4\n" + tc.trailer + "\nstartxref\n9\n%%EOF\n")
			if got := pdfEncrypted(data); got != tc.want {
				t.Errorf("pdfEncrypted: got %v, want %v", got, tc.want)
			}
		})
	}
}
