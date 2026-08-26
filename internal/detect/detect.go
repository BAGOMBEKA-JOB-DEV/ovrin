// Package detect identifies a document's format by content and enforces every
// resource limit before a byte of it is allocated.
//
// It is stage 1 of the pipeline (docs/pipeline.md) and the near side of the
// input trust boundary (docs/threat-model.md). Everything downstream assumes
// two things this package is responsible for: that the bytes it is handed have
// been counted, and that whatever produced them was identified by what it
// contains rather than by what it was called.
//
// Format is never taken from a filename, an extension, or a caller-supplied
// media type. A file named invoice.pdf that is actually a JPEG is common —
// mail systems rename things — and trusting the name is how a parser is handed
// input it was not written for.
//
// The limit primitives live here rather than beside each parser so that
// enforcement is structural rather than remembered. [LimitedReader] wraps a
// decompressor so its output is bounded before it exists rather than measured
// after it does; [Counter] is shared across every stream of one document,
// because a thousand streams of a mebibyte is the same attack as one stream of
// a gibibyte; [Depth] is passed down a recursive parser as an argument, so a
// page tree thousands deep runs out of budget rather than out of stack. See
// docs/adr/0020-resource-limits.md and docs/rules.md §5.2, §7.3.
//
// This package does not import the root package — the root imports it, and the
// reverse would be a cycle — so it has its own [Kind], its own [Limits] and
// its own three sentinels, which the root wraps into its own at that boundary.
//
// Everything here is safe for concurrent use by multiple goroutines except
// [LimitedReader], which is a reader and is read by one goroutine at a time.
package detect

import (
	"bytes"
	"context"
)

// Kind is a document format, always determined by content.
//
// The values match the root package's Kind exactly, so the root converts
// rather than maps: a translation table between two enumerations is a table
// that will fall out of step with one of them.
type Kind string

const (
	// KindUnknown is the zero value, for a format detection has not resolved.
	// It is never returned with a nil error.
	KindUnknown Kind = ""

	KindPDF  Kind = "pdf"
	KindPNG  Kind = "png"
	KindJPEG Kind = "jpeg"
	KindTIFF Kind = "tiff"
	KindWebP Kind = "webp"
	KindDOCX Kind = "docx"
	KindXLSX Kind = "xlsx"
	KindCSV  Kind = "csv"
)

// String returns the format name, or "unknown" for the zero value.
func (k Kind) String() string {
	if k == KindUnknown {
		return "unknown"
	}
	return string(k)
}

// Document is a source whose format has been identified, with the bytes that
// identified it.
type Document struct {
	// Kind is the detected format. Never [KindUnknown].
	Kind Kind

	// Pages is the page count where the format fixes it — one, for a
	// single-image format. Zero means not yet known: counting the pages of a
	// PDF, a multi-page TIFF or a spreadsheet means parsing it, which is
	// stage 2's work and not detection's. It is never guessed, because a
	// guessed page count is a guessed bill from a per-page OCR provider.
	Pages int

	// Data is the whole source, read once under the source-byte limit.
	//
	// It may alias the slice given to [Bytes] and so must not be modified.
	// The container formats need random access — a PDF's cross-reference
	// table is at the end of the file — so the alternative to holding it is
	// reading the source twice, which a stream cannot do.
	Data []byte

	// Decompressed is the document-wide decompression budget, already charged
	// with whatever detection had to decompress to reach its answer.
	//
	// Later stages spend what is left of it rather than starting again from a
	// full budget, because the budget belongs to the document and not to the
	// stage. Never nil.
	Decompressed *Counter
}

// Size returns the source size in bytes.
//
// It is what was read, not what anything declared: a size in a directory entry
// or a header is the document's word for it, and this is not.
func (d *Document) Size() int64 { return int64(len(d.Data)) }

// Detect identifies src by content and returns it with the bytes that
// identified it.
//
// It is the whole of stage 1: the source-byte limit is applied as src is read,
// the format is settled by what the bytes are, and nothing is parsed beyond
// what identification needs.
//
// The failures it decides are the three this package defines — an unrecognised
// format, an encrypted document, or a ceiling. It also returns whatever the
// source itself failed with: a file that is not there, a connection that
// dropped, a context that was cancelled. Those pass through untouched so that
// errors.Is finds them, and the root package attaches them as the cause.
func Detect(ctx context.Context, src Source, lim Limits) (*Document, error) {
	lim = lim.Normalised()
	data, err := Load(ctx, src, lim)
	if err != nil {
		return nil, err
	}
	budget := NewCounter(LimitDecompressedBytes, lim.MaxDecompressedBytes)
	kind, err := identify(data, lim, budget)
	if err != nil {
		return nil, err
	}
	return &Document{Kind: kind, Pages: pagesFor(kind), Data: data, Decompressed: budget}, nil
}

// Identify reports what data is, by content alone.
//
// It is [Detect] without the reading, for bytes that are already in memory and
// already counted. Anything it has to decompress to reach an answer — the
// content-type part of an OOXML container is the only one — is bounded by lim
// exactly as it would be in a full detection.
func Identify(data []byte, lim Limits) (Kind, error) {
	lim = lim.Normalised()
	return identify(data, lim, NewCounter(LimitDecompressedBytes, lim.MaxDecompressedBytes))
}

// identify is Identify against a caller's budget, so that a detection charges
// one budget rather than one per call.
//
// The order is not arbitrary. Formats that state what they are come first,
// cheapest and most specific before least; the ones that have to be inferred
// come last, so that inference only ever runs on bytes that have already
// declined to identify themselves.
func identify(data []byte, lim Limits, budget *Counter) (Kind, error) {
	if len(data) == 0 {
		return KindUnknown, unsupported("empty source")
	}
	if k := sniff(data); k != KindUnknown {
		return k, nil
	}
	if isZIP(data) {
		// PK is shared by DOCX, XLSX, ODF, JAR, EPUB and every other zip
		// anyone has ever made, so it settles nothing on its own. What the
		// container holds is what decides.
		return ooxml(data, lim, budget)
	}
	if pdfHeaderAt(data) >= 0 {
		if pdfEncrypted(data) {
			return KindUnknown, encrypted("pdf trailer names an encryption dictionary")
		}
		return KindPDF, nil
	}
	// CSV is the only format here with no signature at all, so it is inferred
	// rather than recognised, and it is inferred last: anything that reaches
	// this point has already failed to say what it is.
	if isCSV(data) {
		return KindCSV, nil
	}
	return KindUnknown, unsupported("no recognised format signature")
}

// pagesFor returns the page count a format fixes structurally, or zero when
// only a parse can answer.
//
// TIFF is absent on purpose: a multi-page TIFF is ordinary — fax and scanner
// output both produce them — so claiming one page for every TIFF would be a
// fabricated count, and counting the image file directories properly means
// walking a linked list of offsets out of the document, which is a parser and
// belongs with the other parsers.
func pagesFor(k Kind) int {
	switch k {
	case KindPNG, KindJPEG, KindWebP:
		return 1
	default:
		return 0
	}
}

// The signatures that settle a format outright. Each is at offset zero, is
// unique among the formats ovrin reads, and is long enough that a collision
// with an unrelated file is not a practical concern.
var (
	magicPNG      = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	magicJPEG     = []byte{0xFF, 0xD8, 0xFF}
	magicTIFFLE   = []byte{'I', 'I', 0x2A, 0x00} // little-endian, "Intel"
	magicTIFFBE   = []byte{'M', 'M', 0x00, 0x2A} // big-endian, "Motorola"
	magicRIFF     = []byte("RIFF")
	magicWEBP     = []byte("WEBP")
	magicPDF      = []byte("%PDF-")
	magicZIPLocal = []byte{'P', 'K', 0x03, 0x04}
	magicZIPEmpty = []byte{'P', 'K', 0x05, 0x06}
	magicZIPSpan  = []byte{'P', 'K', 0x07, 0x08}
)

// sniff identifies the formats a fixed signature settles, and returns
// [KindUnknown] for everything else — including the zip containers, whose
// signature says only that they are zips.
func sniff(data []byte) Kind {
	switch {
	case bytes.HasPrefix(data, magicPNG):
		return KindPNG
	case bytes.HasPrefix(data, magicJPEG):
		return KindJPEG
	case bytes.HasPrefix(data, magicTIFFLE), bytes.HasPrefix(data, magicTIFFBE):
		// Both endiannesses. The byte order mark is the format's own, and a
		// big-endian TIFF from a scanner is not a corrupt little-endian one.
		return KindTIFF
	case len(data) >= 12 && bytes.HasPrefix(data, magicRIFF) && bytes.Equal(data[8:12], magicWEBP):
		// RIFF is a container: the four bytes at offset 8 are what make it a
		// WebP rather than a WAV.
		return KindWebP
	default:
		return KindUnknown
	}
}

// isZIP reports whether data begins with any of the three zip record
// signatures: a local file header, an empty archive's end-of-directory record,
// or a spanned archive's marker.
func isZIP(data []byte) bool {
	return bytes.HasPrefix(data, magicZIPLocal) ||
		bytes.HasPrefix(data, magicZIPEmpty) ||
		bytes.HasPrefix(data, magicZIPSpan)
}

// pdfHeaderWindow is how far into a file the PDF header may be.
//
// The specification puts it at offset zero and every mainstream reader accepts
// it within the first kibibyte, because real producers prepend junk — mail
// gateways, virus scanners and download wrappers all do it — and a file every
// other reader opens has to open here too. The cost is that a document with no
// signature of its own that happens to contain "%PDF-" in its first kibibyte
// is read as a PDF; it is checked after every format that does have one, which
// is what keeps that to a document that was not identifiable anyway.
const pdfHeaderWindow = 1024

// pdfHeaderAt returns the offset of the PDF header, or -1.
func pdfHeaderAt(data []byte) int {
	head := data
	if len(head) > pdfHeaderWindow+len(magicPDF) {
		head = head[:pdfHeaderWindow+len(magicPDF)]
	}
	return bytes.Index(head, magicPDF)
}

// pdfTrailerWindow is how much of the tail is searched for the trailer
// dictionary. A trailer sits at the end by construction, and searching the
// whole file would find the word inside a content stream.
const pdfTrailerWindow = 4 << 10

// pdfEncrypted reports whether the PDF's trailer names an encryption
// dictionary.
//
// Deliberately conservative, and deliberately incomplete. It reads the classic
// trailer only, so a document whose cross-references are in a stream — where
// the same key lives in the stream's dictionary — is not caught here and is
// caught by the PDF parser, which is the authority. That asymmetry is the
// right one: failing to report encryption costs a clearer error one stage
// later, while reporting it wrongly rejects a document that would have read
// perfectly well.
func pdfEncrypted(data []byte) bool {
	tail := data
	if len(tail) > pdfTrailerWindow {
		tail = tail[len(tail)-pdfTrailerWindow:]
	}
	i := bytes.LastIndex(tail, []byte("trailer"))
	if i < 0 {
		return false
	}
	return hasEncryptRef(tail[i:])
}

// hasEncryptRef reports whether b contains an /Encrypt key followed by an
// indirect reference, which is the only form the value takes. Requiring the
// reference and not just the key is what keeps the word "/Encrypt" appearing
// in some other position from convicting the document.
func hasEncryptRef(b []byte) bool {
	key := []byte("/Encrypt")
	for {
		j := bytes.Index(b, key)
		if j < 0 {
			return false
		}
		rest := b[j+len(key):]
		k := 0
		for k < len(rest) && isPDFSpace(rest[k]) {
			k++
		}
		if k > 0 && k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
			return true
		}
		b = rest
	}
}

// isPDFSpace reports whether c is one of the six bytes the PDF specification
// counts as white space.
func isPDFSpace(c byte) bool {
	switch c {
	case 0x00, '\t', '\n', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}
