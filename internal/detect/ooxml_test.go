package detect

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

const (
	docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`

	xlsxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`</Types>`

	odfContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/content.xml" ContentType="application/vnd.oasis.opendocument.text"/>` +
		`</Types>`
)

type zipEntry struct {
	name  string
	body  string
	flags uint16
}

// buildZip writes an archive with the given entries, in order.
func buildZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Flags: e.flags})
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := io.WriteString(w, e.body); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestIdentifyZipContainers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		entries  []zipEntry
		wantKind Kind
		wantErr  error
	}{
		{
			name: "docx named by its content types",
			entries: []zipEntry{
				{name: contentTypesName, body: docxContentTypes},
				{name: "word/document.xml", body: "<document/>"},
			},
			wantKind: KindDOCX,
		},
		{
			name: "xlsx named by its content types",
			entries: []zipEntry{
				{name: contentTypesName, body: xlsxContentTypes},
				{name: "xl/workbook.xml", body: "<workbook/>"},
			},
			wantKind: KindXLSX,
		},
		{
			name: "docx recognised by its parts when content types are absent",
			entries: []zipEntry{
				{name: "word/document.xml", body: "<document/>"},
			},
			wantKind: KindDOCX,
		},
		{
			name: "xlsx recognised by its parts when content types are absent",
			entries: []zipEntry{
				{name: "xl/workbook.xml", body: "<workbook/>"},
			},
			wantKind: KindXLSX,
		},
		{
			name: "content types outrank a part name that disagrees",
			entries: []zipEntry{
				{name: contentTypesName, body: xlsxContentTypes},
				{name: "word/document.xml", body: "<document/>"},
				{name: "xl/workbook.xml", body: "<workbook/>"},
			},
			wantKind: KindXLSX,
		},
		{
			name: "opendocument is a zip and is not ours",
			entries: []zipEntry{
				{name: contentTypesName, body: odfContentTypes},
				{name: "content.xml", body: "<office/>"},
			},
			wantErr: ErrUnsupportedFormat,
		},
		{
			name:    "an ordinary zip is not a document",
			entries: []zipEntry{{name: "notes.txt", body: "hello"}},
			wantErr: ErrUnsupportedFormat,
		},
		{
			name:    "an empty zip is not a document",
			entries: nil,
			wantErr: ErrUnsupportedFormat,
		},
		{
			name: "an encrypted entry is reported as encryption",
			entries: []zipEntry{
				{name: contentTypesName, body: docxContentTypes, flags: zipEncryptedFlag},
				{name: "word/document.xml", body: "<document/>"},
			},
			wantErr: ErrEncrypted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Identify(buildZip(t, tc.entries...), DefaultLimits())
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

func TestIdentifyTruncatedZip(t *testing.T) {
	t.Parallel()

	full := buildZip(t,
		zipEntry{name: contentTypesName, body: docxContentTypes},
		zipEntry{name: "word/document.xml", body: "<document/>"},
	)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "the central directory is gone", data: full[:len(full)/2]},
		{name: "only the local file header survived", data: full[:32]},
		{name: "only the signature survived", data: full[:4]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Identify(tc.data, DefaultLimits()); !errors.Is(err, ErrUnsupportedFormat) {
				t.Fatalf("Identify: got error %v, want one wrapping %v", err, ErrUnsupportedFormat)
			}
		})
	}
}

// patchCentralDirectorySize rewrites the uncompressed size recorded for name
// in the archive's central directory, leaving the data alone.
//
// This is the only way to build the case that matters: an archive whose
// declaration disagrees with what it holds. No writer produces one, and every
// attacker can.
func patchCentralDirectorySize(t testing.TB, archive []byte, name string, size uint32) []byte {
	t.Helper()

	const (
		sigLen           = 4
		uncompressedSize = 24
		nameLen          = 28
		headerLen        = 46
	)
	out := append([]byte(nil), archive...)
	sig := []byte{'P', 'K', 0x01, 0x02}
	for i := 0; i+headerLen <= len(out); i++ {
		if !bytes.Equal(out[i:i+sigLen], sig) {
			continue
		}
		n := int(binary.LittleEndian.Uint16(out[i+nameLen:]))
		if i+headerLen+n > len(out) || string(out[i+headerLen:i+headerLen+n]) != name {
			continue
		}
		binary.LittleEndian.PutUint32(out[i+uncompressedSize:], size)
		return out
	}
	t.Fatalf("no central directory header for %q", name)
	return nil
}

func TestZipDeclaredSizeIsCheckedAndThenDisbelieved(t *testing.T) {
	t.Parallel()

	// Highly compressible filler, so the archive stays small however much it
	// expands to. It deliberately names no content type.
	filler := strings.Repeat("<Default Extension=\"bin\" ContentType=\"application/octet-stream\"/>", 1<<16)
	honest := buildZip(t,
		zipEntry{name: contentTypesName, body: filler},
		zipEntry{name: "word/document.xml", body: "<document/>"},
	)

	t.Run("a declaration above the ceiling is refused before the entry is opened", func(t *testing.T) {
		t.Parallel()

		data := patchCentralDirectorySize(t, honest, contentTypesName, 1<<30)
		lim := DefaultLimits()
		lim.MaxStreamBytes = 1 << 16

		_, err := Identify(data, lim)
		var le *LimitError
		if !errors.As(err, &le) {
			t.Fatalf("Identify: got error %v, want a *LimitError", err)
		}
		if le.Limit != LimitStreamBytes {
			t.Errorf("Identify: got limit %s, want %s", le.Limit, LimitStreamBytes)
		}
	})

	t.Run("a declaration below the content does not license what is behind it", func(t *testing.T) {
		t.Parallel()

		// The declaration passes the ceiling check, and the entry holds far
		// more than it admits to. Nothing may hand that content on: what
		// matters is that the container is refused rather than expanded, not
		// which of the two bounds noticed first.
		data := patchCentralDirectorySize(t, honest, contentTypesName, 16)
		lim := DefaultLimits()
		lim.MaxStreamBytes = 1 << 16

		if _, err := Identify(data, lim); err == nil {
			t.Fatal("Identify: no error for an entry whose declared size disagrees with its content")
		} else if !errors.Is(err, ErrUnsupportedFormat) && !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("Identify: got error %v, want an unsupported format or a limit", err)
		}
	})
}

func TestZipEntryCountIsBounded(t *testing.T) {
	t.Parallel()

	entries := make([]zipEntry, 0, 64)
	entries = append(entries, zipEntry{name: contentTypesName, body: docxContentTypes})
	for i := 0; i < 63; i++ {
		entries = append(entries, zipEntry{name: "word/media/image" + string(rune('a'+i%26)) + ".png", body: "x"})
	}
	data := buildZip(t, entries...)

	lim := DefaultLimits()
	lim.MaxObjects = 8

	_, err := Identify(data, lim)
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("Identify: got error %v, want a *LimitError", err)
	}
	if le.Limit != LimitObjects {
		t.Errorf("Identify: got limit %s, want %s", le.Limit, LimitObjects)
	}
	if !strings.Contains(le.Error(), "WithMaxObjects") {
		t.Errorf("Identify: error %q does not name the option that raises the limit", le)
	}
}

func TestDetectionNeverRecursesIntoANestedContainer(t *testing.T) {
	t.Parallel()

	// Twenty archives inside one another, the innermost a valid docx. A
	// detector that followed containers would find it, and would follow the
	// same structure a thousand deep on the next document.
	inner := buildZip(t,
		zipEntry{name: contentTypesName, body: docxContentTypes},
		zipEntry{name: "word/document.xml", body: "<document/>"},
	)
	for i := 0; i < 20; i++ {
		inner = buildZip(t, zipEntry{name: "nested.zip", body: string(inner)})
	}

	if _, err := Identify(inner, DefaultLimits()); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Identify: got error %v, want one wrapping %v", err, ErrUnsupportedFormat)
	}
}

func TestScanForFindsANeedleAcrossTheWindowBoundary(t *testing.T) {
	t.Parallel()

	needle := ooxmlNeedles[1]
	tests := []struct {
		name   string
		before int
		want   int
	}{
		{name: "in the first window", before: 10, want: 1},
		{name: "straddling the first boundary", before: scanWindow - len(needle)/2, want: 1},
		{name: "in a later window", before: scanWindow * 3, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			buf.Write(bytes.Repeat([]byte("."), tc.before))
			buf.Write(needle)
			buf.Write(bytes.Repeat([]byte("."), 1000))

			got, err := scanFor(bytes.NewReader(buf.Bytes()), ooxmlNeedles)
			if err != nil {
				t.Fatalf("scanFor: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("scanFor: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestScanForStopsAtTheCeiling(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("."), 1<<20)
	r := NewLimitedReader(bytes.NewReader(body), LimitStreamBytes, 1<<14, nil)
	if _, err := scanFor(r, ooxmlNeedles); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("scanFor: got error %v, want one wrapping %v", err, ErrLimitExceeded)
	}
}

// idleReader returns nothing and no error, which the io.Reader contract
// discourages rather than forbids.
type idleReader struct{}

func (idleReader) Read([]byte) (int, error) { return 0, nil }

func TestScanForGivesUpOnAReaderThatNeverProgresses(t *testing.T) {
	t.Parallel()

	if _, err := scanFor(idleReader{}, ooxmlNeedles); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("scanFor: got error %v, want one wrapping %v", err, io.ErrNoProgress)
	}
}
