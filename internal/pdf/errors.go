package pdf

import (
	"errors"
	"strconv"
	"strings"
)

// The conditions PDF reading can fail on.
//
// These are the values to test with [errors.Is]. They are declared here rather
// than reused from the root package because the root imports the pipeline that
// imports this one, and the reverse would be an import cycle; the pipeline
// wraps them into ovrin's own sentinels at that boundary. Nothing above may
// branch on the text of a message (docs/rules.md §2.2).
//
// The text is lowercase and unpunctuated; the "ovrin: " prefix belongs to the
// package boundary rather than here (docs/rules.md §2.3).
var (
	// ErrMalformed means the file is not a PDF this package can make
	// structural sense of: no header, no cross-reference information that
	// resolves, a catalog that is not a dictionary. It is deliberately one
	// sentinel rather than twenty, because a caller can do exactly one thing
	// about any of them.
	ErrMalformed = errors.New("pdf structure could not be read")

	// ErrEncrypted means the document is encrypted. This package makes the
	// authoritative determination; internal/detect only convicts the obvious
	// cases at the door. Password support is a later ADR
	// (docs/adr/0011-pdf-text-extraction.md).
	ErrEncrypted = errors.New("pdf is encrypted")

	// ErrUnsupportedFilter means a stream is compressed with a filter this
	// package does not implement. It is refused by name rather than skipped,
	// because a silently dropped content stream is a page that looks blank
	// (docs/rules.md §6.1).
	ErrUnsupportedFilter = errors.New("unsupported pdf stream filter")

	// ErrNoTextLayer means a page's text layer decoded to nothing usable, by
	// the measurements in [Stats] against the thresholds in [Thresholds]. It
	// is the signal that the page should be acquired some other way, and it
	// is a deliberate decision rather than an empty string a caller has to
	// interpret (docs/pipeline.md stage 2).
	ErrNoTextLayer = errors.New("no usable text layer")
)

// Error locates a failure in the document's own structural coordinates.
//
// It carries an operation, a page number and an object number, and nothing
// else. It never carries a byte of the document: not a text fragment, not a
// font name, not a dictionary key, not the length a header declared. An error
// string is a log line that ends up in five systems nobody audited, and a
// document is somebody's medical record (docs/rules.md §2.5, §7.5).
//
// Detail is a literal at every construction site in this package and must stay
// one, for the same reason.
type Error struct {
	// Op is the operation that failed: "open", "xref", "object", "stream",
	// "page", "content" or "font".
	Op string

	// Page is the 1-based page number, or 0 when the failure belongs to the
	// document rather than to a page.
	Page int

	// Object is the object number, or 0 when the failure belongs to no
	// particular object. An object number is ovrin's coordinate into the
	// file, not content: it is what a maintainer needs to reproduce the
	// failure against a document they may never be allowed to see.
	Object int

	// Detail says what was observed, never what it said.
	Detail string

	// Err is the sentinel this failure answers.
	Err error
}

// Error renders the coordinates and the detail, in that order.
func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	if e.Err != nil {
		b.WriteString(e.Err.Error())
	}
	if e.Page > 0 {
		b.WriteString(", page ")
		b.WriteString(strconv.Itoa(e.Page))
	}
	if e.Object > 0 {
		b.WriteString(", object ")
		b.WriteString(strconv.Itoa(e.Object))
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Unwrap returns the sentinel, so every failure answers one errors.Is.
func (e *Error) Unwrap() error { return e.Err }

// malformed reports a structural failure at op, in object num (0 for none).
func malformed(op string, num int, detail string) error {
	return &Error{Op: op, Object: num, Detail: detail, Err: ErrMalformed}
}

// malformedPage reports a structural failure while reading a page.
func malformedPage(op string, page int, detail string) error {
	return &Error{Op: op, Page: page, Detail: detail, Err: ErrMalformed}
}

// The filter names this package is willing to repeat back.
//
// A filter name is a byte string the document chooses, so echoing an arbitrary
// one into an error would put document bytes in a log. Only a name on this
// fixed list is named; anything else is reported as unrecognised, which costs
// a maintainer one grep and costs an attacker their exfiltration channel.
var namedFilters = map[Name]string{
	"JBIG2Decode":    "JBIG2Decode",
	"JPXDecode":      "JPXDecode",
	"DCTDecode":      "DCTDecode",
	"CCITTFaxDecode": "CCITTFaxDecode",
	"Crypt":          "Crypt",
}

// unsupportedFilter refuses a filter by name where the name is one this
// package recognises, and anonymously where it is not.
func unsupportedFilter(num int, f Name) error {
	detail, ok := namedFilters[f]
	if !ok {
		detail = "unrecognised filter"
	}
	return &Error{Op: "stream", Object: num, Detail: detail, Err: ErrUnsupportedFilter}
}

// The security handlers this package is willing to name, for the same reason
// namedFilters exists: /Filter in an encryption dictionary is document bytes.
var namedHandlers = map[Name]string{
	"Standard": "standard security handler",
}

// encrypted refuses a document, naming the handler where it is one of ours.
func encrypted(f Name) error {
	detail, ok := namedHandlers[f]
	if !ok {
		detail = "unrecognised security handler"
	}
	return &Error{Op: "open", Detail: detail, Err: ErrEncrypted}
}
