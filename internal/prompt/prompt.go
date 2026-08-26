// Package prompt builds the model request, and is the boundary between text
// recovered from an attacker-controlled file and a system that follows
// instructions.
//
// # The security property
//
// A request has three parts and they never mix. The instruction is assembled
// by ovrin from the caller's schema and contains no byte of document content.
// The document travels in [Request.Content], one item per page, wrapped in
// markers that identify it as untrusted material to be read and not obeyed.
// The JSON Schema is passed through, so the shape of the reply is fixed before
// the document is read.
//
// The separation is structural rather than a matter of wording. [Instruction]
// takes a schema and nothing else: there is no parameter through which
// document text could reach the string it returns, which is a property a
// reviewer can check from the signature rather than by tracing the body. See
// docs/adr/0017-untrusted-document-content.md and docs/rules.md §7.2.
//
// # Delimiter forgery
//
// Any fixed delimiter can be written into a document, so this package does not
// use one. Each request draws a random identifier from crypto/rand and embeds
// it in both markers, and the instruction says that only a marker carrying
// that identifier bounds a block. A document is written before the request
// exists, so its author cannot know the identifier; marker-shaped text in a
// document is inert. The identifier is verified absent from every page before
// the request is built, so the guarantee is checked rather than assumed.
//
// The identifier appears only in the content, never in the instruction, so the
// instruction stays byte-identical for a given schema — which is what tests
// and prompt caching depend on.
//
// # What this package does not do
//
// It does not sanitise. Zero-width characters, direction overrides and
// instruction-shaped text are passed through verbatim, because silently
// stripping them means the operator never learns they are under attack
// (ADR-0017, mitigation 4). Detection and reporting belong to normalisation
// and scoring, not here.
//
// It does not promise that prompt injection is prevented. It makes the obvious
// attacks structurally impossible and leaves the rest to schema-constrained
// output, grounding and review. See docs/threat-model.md, T1.
//
// # Types
//
// [Request], [Content] and [Reading] mirror the root package's ModelRequest,
// Content and Reading field for field, and the root converts between them at
// the seam. [Table] and [Cell] mirror the part of its Table and Cell a
// rendering needs and no more. They are declared here rather than imported
// because the root package imports the pipeline, and a package the root imports
// cannot import the root back.
package prompt

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// DefaultTemperature is the sampling temperature ovrin asks for.
//
// Extraction wants the same answer every time, not a creative one, so ovrin
// sets this rather than leaving the provider's default in place — defaults
// vary by vendor and several of them are high enough to change a value between
// two identical calls.
const DefaultTemperature = 0.0

// The conditions building a request can fail on.
//
// They are unprefixed and untyped on purpose: the root package classifies them
// onto its own sentinels and attaches the stage, so that a caller reads one
// error vocabulary rather than one per internal package (docs/rules.md §2.2,
// docs/adr/0027-twelve-sentinels-and-one-op-vocabulary.md).
var (
	// ErrSchema means the schema cannot be turned into an instruction: it
	// describes no fields, or arrived without its JSON Schema bytes.
	ErrSchema = errors.New("invalid schema")

	// ErrNoContent means there is nothing to send. A request with no content
	// is a paid call that can only produce invented values, so it is refused
	// rather than made.
	ErrNoContent = errors.New("no readable content")

	// ErrAmbiguousContent means a page could not be represented faithfully:
	// it carried both text and an image, both a table and an image, or an
	// image with no media type.
	//
	// One request is one reading, so a page carrying both leaves no honest
	// choice — sending the text means ignoring the image, sending the image
	// means ignoring text that was already recovered, and nothing downstream
	// could tell which happened. Refusing names the problem instead
	// (docs/rules.md §6.1).
	ErrAmbiguousContent = errors.New("page cannot be represented as one content item")

	// ErrBoundary means no content boundary could be generated: the entropy
	// source could not be read, or every identifier it produced already
	// appeared in the content.
	//
	// It is separate from the others because it describes a failure of this
	// process, not of the document, the schema or the provider. Reporting it
	// as one of those would send an operator to inspect something that is not
	// at fault.
	ErrBoundary = errors.New("could not generate a content boundary")
)

// Reading is how a page was read.
//
// It mirrors the root package's Reading, including its values, so the
// conversion at the seam is a type conversion and nothing more.
type Reading string

// The readings, matching the root package's constants.
const (
	// ReadingUnknown is the zero value, for a page whose origin was not
	// recorded.
	ReadingUnknown Reading = ""

	// ReadingText is a PDF's own text layer.
	ReadingText Reading = "text"

	// ReadingOCR is optical character recognition of a rasterised page.
	ReadingOCR Reading = "ocr"

	// ReadingVision is a multimodal model reading a page image.
	ReadingVision Reading = "vision"
)

// String returns the reading name, or "unknown" for the zero value, so that a
// marker never reads "reading=".
func (r Reading) String() string {
	if r == ReadingUnknown {
		return "unknown"
	}
	return string(r)
}

// Request is one extraction call, ready for the root package to hand to a
// model adapter.
//
// The fields mirror the root package's ModelRequest exactly. Instruction and
// Content never mix: an adapter maps Instruction to the provider's system role
// and Content to the user role, and never concatenates them.
type Request struct {
	// Instruction is built from the schema. It never contains document
	// content.
	Instruction string

	// Content is the untrusted material, already delimited and labelled.
	Content []Content

	// Schema is the JSON Schema the reply must satisfy, passed through
	// unchanged.
	Schema []byte

	// Temperature is the sampling temperature. It is always set, and always
	// low: see [DefaultTemperature].
	Temperature *float64
}

// Content is one piece of material handed to a model. It is always untrusted.
//
// The fields mirror the root package's Content exactly.
type Content struct {
	// Reading is which reading produced this content.
	Reading Reading

	// Page is 1-based.
	Page int

	// Text is set when Reading is text or OCR, and carries the delimiting
	// markers around the document's own text.
	Text string

	// Image is set when Reading is vision. Raw bytes, never base64 —
	// encoding is the adapter's job, and doing it twice corrupts the image.
	Image []byte

	// MediaType is the IANA media type, required when Image is set.
	MediaType string
}

// PageContent is one page of normalised content, ready to be delimited.
//
// This is the prompt stage's own input type rather than a type borrowed from
// normalisation, because the only thing this stage needs to know about a page
// is what to send and how to label it. Everything normalisation carries for
// grounding — offsets, spans, word boxes — is deliberately absent: content
// that is not here cannot be leaked here.
type PageContent struct {
	// Number is the 1-based page number. A value below 1 is replaced by the
	// page's position in the slice, because a marker reading "page 0" would
	// attribute a value to a page that does not exist.
	Number int

	// Reading is how this page was read, and is reported in the marker so a
	// value can be attributed to a reading as well as to a page.
	Reading Reading

	// Text is the normalised text of the page. It is untrusted, and is copied
	// into the request verbatim: this package never edits document content.
	Text string

	// Tables are the tables a provider recognised on this page, and are
	// rendered as grids after Text, inside the same content markers.
	//
	// They are here because a table that crosses the OCR seam and is then not
	// shown to the model is no better than one that was discarded: the words
	// of a table reach the model either way, and only the grid says which
	// column a value came from. A page with no tables, or from a provider that
	// does not look for them, leaves this empty and its text is sent
	// unchanged.
	Tables []Table

	// Image is the rasterised page, for a vision reading. A page sets either
	// Text or Image, never both: a [Content] carries one or the other, and a
	// page that sets both is refused with [ErrAmbiguousContent] rather than
	// having one of them quietly dropped.
	Image []byte

	// MediaType is the IANA media type of Image, and is required whenever
	// Image is set. An image with no media type cannot be sent by any
	// adapter, so it is refused here rather than at the provider.
	MediaType string
}

// Build assembles one [Request] from the schema, the JSON Schema bytes and the
// document's pages.
//
// The instruction comes from s alone, by way of [Instruction]. The pages reach
// the request only through [Request.Content], one item per page, each wrapped
// in markers bearing a per-request random identifier. jsonSchema is passed
// through unchanged and uncopied, so a caller must not mutate it afterwards;
// it is produced once per type and shared, and copying it on every extraction
// would cost more than the aliasing it prevents.
//
// Errors never carry document content — not the text, not a substring of it,
// not its length (docs/rules.md §2.5).
func Build(s schema.Schema, jsonSchema []byte, pages []PageContent) (Request, error) {
	return build(nil, s, jsonSchema, pages)
}

// build is Build with the entropy source injected, so tests can pin the
// boundary identifier and assert on exact bytes. A nil entropy means
// crypto/rand.
func build(entropy io.Reader, s schema.Schema, jsonSchema []byte, pages []PageContent) (Request, error) {
	if len(s.Fields) == 0 {
		return Request{}, fmt.Errorf("%w: schema describes no fields", ErrSchema)
	}
	if len(jsonSchema) == 0 {
		return Request{}, fmt.Errorf("%w: json schema is empty", ErrSchema)
	}
	if len(pages) == 0 {
		return Request{}, fmt.Errorf("%w: no page content to send", ErrNoContent)
	}
	// Checked before entropy is drawn, and before anything is copied: a page
	// that cannot be sent should cost nothing to reject. The page number is
	// the only detail an error carries (docs/rules.md §2.5).
	for i, p := range pages {
		if err := check(i, p); err != nil {
			return Request{}, err
		}
	}

	// The bodies are built before the identifier is drawn, so that the
	// identifier is checked against every byte the request will carry — a
	// table cell's text included — rather than against the page prose alone.
	bodies := make([]string, len(pages))
	for i, p := range pages {
		bodies[i] = pageBody(p)
	}

	id, err := boundary(entropy, bodies)
	if err != nil {
		return Request{}, err
	}

	content := make([]Content, 0, len(pages))
	for i, p := range pages {
		content = append(content, wrap(id, i, p, bodies[i]))
	}

	temperature := DefaultTemperature
	return Request{
		// The only assignment to Instruction in this package, and its right
		// hand side cannot see pages. docs/rules.md §7.2.
		Instruction: Instruction(s),
		Content:     content,
		Schema:      jsonSchema,
		Temperature: &temperature,
	}, nil
}

// check reports whether a page can be sent as one content item.
func check(index int, p PageContent) error {
	if len(p.Image) == 0 {
		return nil
	}
	if p.Text != "" {
		return fmt.Errorf("%w: page %d carries both text and an image", ErrAmbiguousContent, number(index, p))
	}
	if len(p.Tables) > 0 {
		// A table is text, and an image item carries none: sending the image
		// would silently drop the structure, and rendering the table would
		// mean not sending the page. Neither is honest, so the page is refused
		// (docs/rules.md §6.1).
		return fmt.Errorf("%w: page %d carries both a table and an image", ErrAmbiguousContent, number(index, p))
	}
	if p.MediaType == "" {
		return fmt.Errorf("%w: page %d carries an image with no media type", ErrAmbiguousContent, number(index, p))
	}
	return nil
}

// number is the page number a page will be reported under.
//
// A number below 1 is replaced by the page's position, because a marker
// reading "page 0" would attribute a value to a page that does not exist.
func number(index int, p PageContent) int {
	if p.Number < 1 {
		return index + 1
	}
	return p.Number
}

// wrap turns one page into one content item.
//
// An image page is passed through undelimited: markers are text, and text
// prepended to an image is either ignored or corrupts it. The instruction
// states that page images are document content too, so the labelling that
// markers provide for text is provided for images by the instruction instead.
// body is the page's document text with its tables already rendered, computed
// once by [build] so that the boundary check and the request see the same
// bytes.
func wrap(id string, index int, p PageContent, body string) Content {
	page := number(index, p)
	if len(p.Image) > 0 {
		return Content{
			Reading:   p.Reading,
			Page:      page,
			Image:     p.Image,
			MediaType: p.MediaType,
		}
	}
	return Content{
		Reading: p.Reading,
		Page:    page,
		Text:    delimit(id, page, p.Reading, body),
	}
}

// delimit wraps document text between the two markers.
//
// The text is written verbatim between them, with a newline on each side
// regardless of what the text already ends in, so that both markers always sit
// alone on a line. A stray blank line is a much smaller problem than an end
// marker that a document could push into the middle of one.
func delimit(id string, page int, reading Reading, text string) string {
	var b strings.Builder
	b.Grow(len(text) + 2*len(id) + 160)
	fmt.Fprintf(&b, "[%s id=%s page=%d reading=%s]\n", beginMarker, id, page, reading.String())
	b.WriteString(text)
	fmt.Fprintf(&b, "\n[%s id=%s page=%d]", endMarker, id, page)
	return b.String()
}
