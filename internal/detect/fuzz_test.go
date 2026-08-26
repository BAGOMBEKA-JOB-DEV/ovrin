package detect

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fuzzSourceBytes is the source ceiling the fuzzer runs under.
//
// Deliberately far below the default, and deliberately above the window CSV is
// judged from, so that both the ceiling and the windowed paths are reachable
// in inputs small enough for a fuzzer to generate millions of.
const fuzzSourceBytes = 128 << 10

// fuzzLimits are smaller than the defaults for the same reason: a ceiling that
// only a sixty-mebibyte input can reach is a ceiling the fuzzer never tests.
func fuzzLimits() Limits {
	return Limits{
		MaxSourceBytes:       fuzzSourceBytes,
		MaxDecompressedBytes: 4 << 20,
		MaxStreamBytes:       1 << 20,
		MaxTextBytes:         1 << 20,
		MaxPages:             1000,
		MaxDepth:             64,
		MaxObjects:           1000,
		MaxPagePixels:        50_000_000,
	}
}

// FuzzDetect drives the only code in ovrin that reads attacker-chosen bytes
// with nothing in front of it.
//
// It is not a search for a wrong answer — an arbitrary byte string has no
// right one — but for the three ways this package could fail its callers: a
// panic, which is a crash in somebody's service; an error that is none of the
// three conditions the caller knows how to handle; and an error carrying a
// piece of the document into a log line.
//
// It exists from the first commit rather than being added after the first
// crash, because a parser of hostile input that has never been fuzzed has
// simply not been tested.
func FuzzDetect(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		{'%'},
		[]byte(samplePDF),
		[]byte(sampleEncryptedPDF),
		[]byte(sampleEncryptWordPDF),
		samplePNG,
		sampleJPEG,
		sampleTIFFLE,
		sampleTIFFBE,
		sampleWebP,
		sampleRIFFWave,
		sampleCSV,
		[]byte("a,b\nc,d\n"),
		[]byte("alpha\nbeta\n"),
		[]byte("%PDF-"),
		[]byte("PK\x03\x04"),
		[]byte("PK\x05\x06"),
		{0x89, 'P', 'N', 'G'},
		{0xFF, 0xD8},
		{'I', 'I', 0x2A},
		[]byte("RIFF"),
		bytes.Repeat([]byte("A"), fuzzSourceBytes+1),
		[]byte(strings.Repeat("a,b,c\n", fuzzSourceBytes/6)),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Add(buildZip(f, zipEntry{name: contentTypesName, body: docxContentTypes}, zipEntry{name: "word/document.xml", body: "<document/>"}))
	f.Add(buildZip(f, zipEntry{name: contentTypesName, body: xlsxContentTypes}, zipEntry{name: "xl/workbook.xml", body: "<workbook/>"}))
	f.Add(deflateOf(f, make([]byte, 4<<20)))

	f.Fuzz(func(t *testing.T, data []byte) {
		lim := fuzzLimits()

		doc, err := Detect(context.Background(), Bytes(data), lim)
		if err != nil {
			if doc != nil {
				t.Fatalf("Detect: returned a document alongside error %v", err)
			}
			// Every failure has to be one of the three the caller was told
			// about. Anything else reaches the root package as an
			// unclassified error and reaches the caller as a surprise.
			if !errors.Is(err, ErrUnsupportedFormat) &&
				!errors.Is(err, ErrLimitExceeded) &&
				!errors.Is(err, ErrEncrypted) {
				t.Fatalf("Detect: unclassified error %v", err)
			}
			if !fromTheFixedVocabulary(err.Error()) {
				t.Fatalf("Detect: error %q is not built from the fixed vocabulary", err)
			}
			return
		}

		if doc.Kind == KindUnknown {
			t.Fatal("Detect: nil error with an unresolved kind")
		}
		if doc.Pages < 0 {
			t.Fatalf("Detect: negative page count %d", doc.Pages)
		}
		if doc.Decompressed == nil {
			t.Fatal("Detect: nil decompression budget")
		}
		if doc.Size() != int64(len(data)) {
			t.Fatalf("Detect: size %d, want the %d bytes it was given", doc.Size(), len(data))
		}
		if !bytes.Equal(doc.Data, data) {
			t.Fatal("Detect: the bytes handed on are not the bytes read")
		}

		// Identification is a function of the bytes and nothing else. A
		// detector that answers differently on a second look cannot be
		// reasoned about at all.
		again, err := Identify(data, lim)
		if err != nil {
			t.Fatalf("Identify: got error %v where Detect got kind %s", err, doc.Kind)
		}
		if again != doc.Kind {
			t.Fatalf("Identify: got kind %s, want the %s Detect got", again, doc.Kind)
		}
	})
}
