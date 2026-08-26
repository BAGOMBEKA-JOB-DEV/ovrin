package detect

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
)

// contentTypesName is the part every OOXML package must contain, at the root
// of the archive, naming what the other parts are.
const contentTypesName = "[Content_Types].xml"

// The main-document content types, and the part names that back them up. The
// index of a needle is its index in ooxmlKinds, so the two stay in step.
var (
	ooxmlNeedles = [][]byte{
		[]byte("wordprocessingml.document.main+xml"),
		[]byte("spreadsheetml.sheet.main+xml"),
	}
	ooxmlKinds = []Kind{KindDOCX, KindXLSX}

	ooxmlParts = map[string]Kind{
		"word/document.xml": KindDOCX,
		"xl/workbook.xml":   KindXLSX,
	}
)

// zipEncryptedFlag is bit 0 of a zip entry's general-purpose flags, which is
// set when the entry's data is encrypted.
const zipEncryptedFlag = 0x1

// ooxml decides which OOXML format a zip container holds, or refuses it.
//
// A PK signature is shared by DOCX, XLSX, ODF, EPUB, JAR and every other zip
// ever made, so guessing from the header would hand an OOXML parser an Android
// application. What settles it is the central directory: the content-type part
// names the main document part, and that name is the format.
//
// Nothing else in the archive is opened, and no archive inside it is followed.
// A zip nested inside a zip is a zip that is neither of these formats, which
// is the answer that makes a container bomb a rejection rather than a
// recursion (docs/rules.md §7.4).
func ooxml(data []byte, lim Limits, budget *Counter) (Kind, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// The zip package's error text is not repeated: it is free to quote a
		// part name, and a part name is document content (docs/rules.md §2.5).
		return KindUnknown, unsupported("zip container could not be read")
	}
	// The entry count is checked before the entries are walked, and the
	// archive/zip reader has already refused to preallocate for a count its
	// own size cannot support.
	if err := lim.CheckObjects(len(zr.File)); err != nil {
		return KindUnknown, err
	}

	var contentTypes *zip.File
	byPart := KindUnknown
	for _, f := range zr.File {
		if f.Flags&zipEncryptedFlag != 0 {
			// An OOXML package is encrypted as a whole rather than per entry,
			// so this is a zip that was encrypted by something else — but it
			// is still a document nothing here can read without a credential,
			// and saying so is more use than "unsupported format".
			return KindUnknown, encrypted("zip entry is encrypted")
		}
		if f.Name == contentTypesName {
			contentTypes = f
			continue
		}
		if k, ok := ooxmlParts[f.Name]; ok && byPart == KindUnknown {
			byPart = k
		}
	}

	if contentTypes != nil {
		k, err := ooxmlContentType(contentTypes, lim, budget)
		if err != nil {
			return KindUnknown, err
		}
		if k != KindUnknown {
			return k, nil
		}
	}
	// The part names are the fallback, not the answer. A producer that omits
	// or mangles the content types still lays the parts out where the format
	// requires them, and a macro-enabled document is the same format with a
	// different content type, so this reads one where the authoritative
	// signal was absent rather than refusing a document every other reader
	// opens.
	if byPart != KindUnknown {
		return byPart, nil
	}
	return KindUnknown, unsupported("zip container is neither docx nor xlsx")
}

// ooxmlContentType reads the content-type part and reports which main document
// part it names, or [KindUnknown] if it names neither.
func ooxmlContentType(f *zip.File, lim Limits, budget *Counter) (Kind, error) {
	// The declared size is checked before the entry is opened, because the
	// rejection is free and a declaration of half a gibibyte needs no
	// decompressor to refuse.
	//
	// It is not believed afterwards. archive/zip does stop a stream that runs
	// past its own declaration, but that is its behaviour rather than our
	// contract, and the ceiling here is ours: the reader is wrapped whatever
	// the declaration said, so this path is bounded by the same limit as every
	// other decompressor in ovrin rather than by another package's care.
	if f.UncompressedSize64 > uint64(lim.MaxStreamBytes) {
		return KindUnknown, exceeded(LimitStreamBytes, lim.MaxStreamBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return KindUnknown, unsupported("content type part could not be read")
	}
	defer func() { _ = rc.Close() }() // read-only; a close error cannot change what was read

	i, err := scanFor(NewLimitedReader(rc, LimitStreamBytes, lim.MaxStreamBytes, budget), ooxmlNeedles)
	switch {
	case errors.Is(err, ErrLimitExceeded):
		return KindUnknown, err
	case err != nil:
		// A truncated or corrupt part is not a format we can identify, and
		// the underlying error is not repeated for the same reason as above.
		return KindUnknown, unsupported("content type part could not be read")
	case i < 0:
		return KindUnknown, nil
	default:
		return ooxmlKinds[i], nil
	}
}

// scanWindow is how much of a stream is held while searching it. Small enough
// that the search costs a fixed and trivial amount of memory whatever the
// stream turns out to hold.
const scanWindow = 32 << 10

// maxEmptyReads bounds a reader that returns nothing and no error for ever.
// The io.Reader contract discourages it rather than forbidding it, and this
// package's job is to survive things that do not follow contracts.
const maxEmptyReads = 100

// scanFor returns the index in needles of the first needle to appear in r, or
// -1 if none does.
//
// It searches a sliding window rather than reading the stream in. The stream
// is a decompressor's output and so is attacker-chosen in length whatever its
// header claims; holding a window means the memory cost of the search is fixed
// no matter what comes out, and the wrapped reader still stops the stream
// itself at its ceiling. The window overlaps by one byte less than the longest
// needle, so a needle straddling a boundary is still found.
//
// Order is by position in the stream, not by position in needles: the first
// one to appear wins, so a container naming two main document parts resolves
// the same way every time.
func scanFor(r io.Reader, needles [][]byte) (int, error) {
	overlap := 0
	for _, n := range needles {
		if len(n) > overlap {
			overlap = len(n)
		}
	}
	if overlap > 0 {
		overlap--
	}
	buf := make([]byte, scanWindow+overlap)
	filled := 0
	empty := 0
	for {
		n, err := r.Read(buf[filled:])
		filled += n
		switch {
		case n > 0:
			empty = 0
		case err == nil:
			// io.ReadFull would spin here for ever, which is why the fill is
			// written out rather than borrowed.
			empty++
			if empty > maxEmptyReads {
				return -1, io.ErrNoProgress
			}
			continue
		}
		// Searching only on a full window or at the end keeps the cost linear
		// in the stream rather than in the number of reads it arrives in.
		if err != nil || filled == len(buf) {
			for i, needle := range needles {
				if bytes.Contains(buf[:filled], needle) {
					return i, nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return -1, nil
			}
			return -1, err
		}
		if filled == len(buf) {
			// Carry the tail forward so a needle straddling the boundary is
			// still whole in the next window.
			copy(buf, buf[filled-overlap:filled])
			filled = overlap
		}
	}
}
