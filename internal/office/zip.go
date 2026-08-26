package office

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// maxZipEntries bounds how many entries a container may declare.
//
// It is far below detect.Limits.MaxObjects on purpose. MaxObjects is a ceiling
// for a PDF's object graph, where half a million is a large but real document;
// an OOXML package with more than a few thousand parts is not a document
// anybody wrote. The number is checked before the directory is walked, so a
// container declaring a hundred thousand entries costs one comparison.
const maxZipEntries = 8192

// zipEncryptedFlag is bit 0 of an entry's general-purpose flags, set when the
// entry's data is encrypted.
const zipEncryptedFlag = 0x1

// container is a zip archive that parts are taken out of by exact name, under
// a cumulative budget shared by every part.
//
// Taking parts by exact name rather than by walking is the main structural
// defence. A container holding ten thousand decompression bombs and one
// word/document.xml costs the ten thousand nothing, because only the parts
// this package asks for are ever opened. It is also why a zip nested inside a
// zip is not a recursion: an entry's bytes are never handed to a zip reader,
// and a part inside a nested archive does not have the outer name this package
// looks up (docs/rules.md §7.4).
type container struct {
	byName map[string]*zip.File
	names  []string
	lim    detect.Limits
	cum    *detect.Counter
}

// openContainer reads the central directory of a zip and refuses everything
// about it that can be refused before a byte is decompressed.
//
// cum is the document-wide decompression budget, cumulative across every entry
// this container yields. A nil cum gets one built from lim, so a caller with no
// budget of its own still gets a cumulative ceiling rather than none.
func openContainer(data []byte, lim detect.Limits, cum *detect.Counter) (*container, error) {
	lim = lim.Normalised()
	if cum == nil {
		cum = detect.NewCounter(detect.LimitDecompressedBytes, lim.MaxDecompressedBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// archive/zip's error text is not repeated: it is free to quote an
		// entry name, and an entry name is document content
		// (docs/rules.md §2.5). This is the same reasoning internal/detect
		// applies at the same boundary.
		return nil, malformed("container", PartContainer, "zip central directory could not be read")
	}
	if n := len(zr.File); n > maxZipEntries {
		return nil, unsupported("container", PartContainer, "too many container entries")
	}
	// The count is also charged to the configured object ceiling, so a caller
	// who lowered it below maxZipEntries gets the lower number honoured.
	if err := lim.CheckObjects(len(zr.File)); err != nil {
		return nil, err
	}

	c := &container{
		byName: make(map[string]*zip.File, len(zr.File)),
		names:  make([]string, 0, len(zr.File)),
		lim:    lim,
		cum:    cum,
	}
	for _, f := range zr.File {
		if f.Flags&zipEncryptedFlag != 0 {
			// An OOXML package is encrypted as a whole rather than per entry,
			// so this is a container encrypted by something else. It is still
			// a document nothing here can read without a credential.
			return nil, encrypted("container entry is encrypted")
		}
		// The first entry of a given name wins, and nothing later replaces
		// it. A container carrying two parts of the same name is trying to
		// have two readers disagree about which one is the document; picking
		// the first, always, is what makes this package's answer the same
		// answer every time.
		if _, seen := c.byName[f.Name]; seen {
			continue
		}
		c.byName[f.Name] = f
		c.names = append(c.names, f.Name)
	}
	return c, nil
}

// has reports whether the container holds a part of exactly this name.
func (c *container) has(name string) bool {
	_, ok := c.byName[name]
	return ok
}

// namesWithPrefix returns the entry names beginning with prefix, in central
// directory order.
//
// The names are this package's own lookup keys and never leave it. Nothing
// derived from one reaches an error, a finding or an extracted word.
func (c *container) namesWithPrefix(prefix string) []string {
	out := make([]string, 0, 8)
	for _, n := range c.names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

// open returns a bounded reader over one part, or reports that it is missing.
//
// The declared uncompressed size is checked first, because refusing a
// declaration of half a gibibyte needs no decompressor. It is then not
// believed: the reader is wrapped whatever the declaration said, so this path
// is bounded by ovrin's own ceiling rather than by archive/zip's care about
// its own field. Both ceilings are enforced, and they answer different
// attacks — the per-stream one stops a single enormous part, the cumulative
// one stops a thousand merely large ones (docs/adr/0020-resource-limits.md).
//
// Closing the result closes the decompressor.
func (c *container) open(name string, part Part) (io.ReadCloser, error) {
	f, ok := c.byName[name]
	if !ok {
		return nil, malformed("part", part, "part is not present in the container")
	}
	// Store and Deflate are what OOXML uses and what archive/zip decodes
	// without a registered decompressor. Anything else is refused by name
	// rather than attempted, so an unregistered method is a clear answer
	// instead of an opaque failure deeper in.
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return nil, unsupported("part", part, "container entry uses an unsupported compression method")
	}
	if f.UncompressedSize64 > uint64(c.lim.MaxStreamBytes) {
		return nil, exceededStream(c.lim.MaxStreamBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, malformed("part", part, "container entry could not be opened")
	}
	return boundedPart{
		Reader: detect.NewLimitedReader(rc, detect.LimitStreamBytes, c.lim.MaxStreamBytes, c.cum),
		closer: rc,
	}, nil
}

// boundedPart presents a part's bounded bytes while closing the decompressor
// underneath, so a caller cannot hold the bounded reader and leak the thing
// being bounded.
type boundedPart struct {
	io.Reader
	closer io.Closer
}

// Close closes the decompressor.
func (b boundedPart) Close() error { return b.closer.Close() }

// exceededStream reports the per-stream ceiling in the shape internal/detect
// reports every other one, so a caller's errors.Is against
// detect.ErrLimitExceeded works identically whichever package refused.
func exceededStream(max int64) error {
	return &detect.LimitError{Limit: detect.LimitStreamBytes, Max: max}
}
