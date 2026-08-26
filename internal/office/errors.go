package office

import (
	"errors"
	"strconv"
	"strings"
)

// The conditions office reading can fail on.
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
	// ErrMalformed means the file is not a document this package can make
	// structural sense of: a container that is not a readable zip, a part
	// that is not well-formed XML, a workbook naming no worksheets. It is one
	// sentinel rather than a dozen because a caller can do exactly one thing
	// about any of them.
	ErrMalformed = errors.New("office document structure could not be read")

	// ErrEncrypted means the container's data is encrypted and cannot be read
	// without a credential. Saying so is more use to a caller than reporting
	// an unreadable format, because it names something the caller can act on.
	ErrEncrypted = errors.New("office document is encrypted")

	// ErrUnsupported means the document uses something this package will not
	// read: a compression method other than Store or Deflate, or a kind that
	// is not one of the three this package handles. It is refused by name
	// rather than skipped, because a silently dropped part is a page that
	// looks blank (docs/rules.md §6.1).
	ErrUnsupported = errors.New("unsupported office document feature")
)

// Part names one component of a document, from a closed vocabulary.
//
// It is a closed set of this package's own constants, and that is the entire
// point of its existing. A part's real name inside the container is a string
// the document's author chose, so it is document content and may not appear in
// an error or in [Document.Skipped] (docs/rules.md §2.5). A Part is ovrin's
// own word for the role a part plays, so it is safe to print, safe to log, and
// stable enough for a caller to branch on.
type Part string

const (
	// PartUnknown is the zero value and names no part.
	PartUnknown Part = ""

	// PartDocument is a DOCX body.
	PartDocument Part = "document"

	// PartHeader is a DOCX page header.
	PartHeader Part = "header"

	// PartFooter is a DOCX page footer.
	PartFooter Part = "footer"

	// PartFootnote is a DOCX footnote part.
	PartFootnote Part = "footnote"

	// PartEndnote is a DOCX endnote part.
	PartEndnote Part = "endnote"

	// PartComment is a DOCX comment part.
	PartComment Part = "comment"

	// PartWorkbook is an XLSX workbook part, which names the sheets.
	PartWorkbook Part = "workbook"

	// PartWorksheet is one XLSX worksheet.
	PartWorksheet Part = "worksheet"

	// PartSharedStrings is an XLSX shared string table.
	PartSharedStrings Part = "shared strings"

	// PartRelationships is a relationship part, which maps a relationship
	// identifier to the part it points at.
	PartRelationships Part = "relationships"

	// PartContainer is the archive itself, for a failure that belongs to no
	// single part.
	PartContainer Part = "container"
)

// String returns the part, or "unknown" for the zero value.
func (p Part) String() string {
	if p == PartUnknown {
		return "unknown"
	}
	return string(p)
}

// Error locates a failure in the document's own structural coordinates.
//
// It carries an operation, a page number and a [Part], and nothing else. It
// never carries a byte of the document: not a cell value, not a sheet name,
// not a paragraph, not a zip entry name, not the length a header declared. An
// error string is a log line that ends up in five systems nobody audited, and
// a document is somebody's payroll (docs/rules.md §2.5, §7.5).
//
// Detail is a literal at every construction site in this package and must stay
// one, for the same reason.
type Error struct {
	// Op is the operation that failed: "open", "container", "part", "xml",
	// "sheet" or "record".
	Op string

	// Page is the 1-based page number — for XLSX, the sheet's position in the
	// workbook — or 0 when the failure belongs to the document rather than to
	// a page.
	Page int

	// Part is which component failed, from the closed vocabulary above.
	Part Part

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
	if e.Part != PartUnknown {
		b.WriteString(", ")
		b.WriteString(e.Part.String())
	}
	if e.Page > 0 {
		b.WriteString(", page ")
		b.WriteString(strconv.Itoa(e.Page))
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Unwrap returns the sentinel, so every failure answers one errors.Is.
func (e *Error) Unwrap() error { return e.Err }

// malformed reports a structural failure in part at op.
func malformed(op string, part Part, detail string) error {
	return &Error{Op: op, Part: part, Detail: detail, Err: ErrMalformed}
}

// malformedPage reports a structural failure while reading a page.
func malformedPage(op string, part Part, page int, detail string) error {
	return &Error{Op: op, Part: part, Page: page, Detail: detail, Err: ErrMalformed}
}

// unsupported refuses a feature this package will not read.
func unsupported(op string, part Part, detail string) error {
	return &Error{Op: op, Part: part, Detail: detail, Err: ErrUnsupported}
}

// encrypted refuses a container whose data needs a credential.
func encrypted(detail string) error {
	return &Error{Op: "container", Part: PartContainer, Detail: detail, Err: ErrEncrypted}
}
