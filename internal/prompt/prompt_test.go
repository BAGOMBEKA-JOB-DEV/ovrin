package prompt

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// testJSONSchema stands in for the bytes stage 4 produces. Its content is
// irrelevant here: this package passes it through and never reads it.
var testJSONSchema = []byte(`{"type":"object","additionalProperties":false}`)

// markerParts splits a delimited content item into the boundary identifier and
// the document text it wraps.
//
// It is deliberately literal rather than a regexp: a test that parses the
// markers the same way the model is told to parse them is a test of the
// scheme, not of a pattern.
func markerParts(t *testing.T, text string) (id, body string) {
	t.Helper()

	nl := strings.Index(text, "\n")
	if nl < 0 {
		t.Fatalf("content has no opening marker line")
	}
	open, rest := text[:nl], text[nl+1:]

	last := strings.LastIndex(rest, "\n")
	if last < 0 {
		t.Fatalf("content has no closing marker line")
	}
	body, closing := rest[:last], rest[last+1:]

	if !strings.HasPrefix(open, "["+beginMarker+" id=") {
		t.Fatalf("opening marker is malformed: %q", open)
	}
	if !strings.HasPrefix(closing, "["+endMarker+" id=") || !strings.HasSuffix(closing, "]") {
		t.Fatalf("closing marker is malformed: %q", closing)
	}

	id = strings.TrimPrefix(open, "["+beginMarker+" id=")
	if sp := strings.Index(id, " "); sp >= 0 {
		id = id[:sp]
	}
	if id == "" {
		t.Fatalf("opening marker carries no identifier: %q", open)
	}
	return id, body
}

func TestBuildRejectsInputItCannotTurnIntoARequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     schema.Schema
		js     []byte
		pages  []PageContent
		want   error
		reason string
	}{
		{
			name:   "schema with no fields",
			in:     schema.Schema{Name: "Empty"},
			js:     testJSONSchema,
			pages:  []PageContent{{Number: 1, Text: "hello"}},
			want:   ErrSchema,
			reason: "schema describes no fields",
		},
		{
			name:   "empty json schema",
			in:     testSchema(),
			js:     nil,
			pages:  []PageContent{{Number: 1, Text: "hello"}},
			want:   ErrSchema,
			reason: "json schema is empty",
		},
		{
			name:   "no pages",
			in:     testSchema(),
			js:     testJSONSchema,
			pages:  nil,
			want:   ErrNoContent,
			reason: "no page content to send",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := Build(tc.in, tc.js, tc.pages)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error is %v, want it to be %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not say why", err)
			}
			if req.Instruction != "" || req.Content != nil || req.Schema != nil {
				t.Errorf("a failed build returned a partly populated request: %+v", req)
			}
			// docs/rules.md §2.5: an error is a log line in five systems.
			if strings.Contains(err.Error(), "hello") {
				t.Errorf("error carries document content: %q", err)
			}
		})
	}
}

func TestBuildProducesOneContentItemPerPage(t *testing.T) {
	t.Parallel()

	pages := []PageContent{
		{Number: 1, Reading: ReadingText, Text: "first page"},
		{Number: 2, Reading: ReadingOCR, Text: "second page"},
		{Number: 3, Reading: ReadingText, Text: ""},
	}

	req, err := Build(testSchema(), testJSONSchema, pages)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(req.Content) != len(pages) {
		t.Fatalf("got %d content items, want %d", len(req.Content), len(pages))
	}

	var id string
	for i, c := range req.Content {
		if c.Page != pages[i].Number {
			t.Errorf("item %d has page %d, want %d", i, c.Page, pages[i].Number)
		}
		if c.Reading != pages[i].Reading {
			t.Errorf("item %d has reading %q, want %q", i, c.Reading, pages[i].Reading)
		}
		gotID, body := markerParts(t, c.Text)
		if body != pages[i].Text {
			t.Errorf("item %d body is %q, want %q", i, body, pages[i].Text)
		}
		if !strings.Contains(c.Text, fmt.Sprintf("page=%d reading=%s]", pages[i].Number, pages[i].Reading.String())) {
			t.Errorf("item %d opening marker does not carry page and reading: %q", i, c.Text)
		}
		if i == 0 {
			id = gotID
		} else if gotID != id {
			t.Errorf("item %d uses identifier %q, want the same %q for the whole request", i, gotID, id)
		}
	}
}

func TestBuildNumbersPagesByPositionWhenTheNumberIsMissing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		pages []PageContent
		want  []int
	}{
		{
			name:  "numbers supplied are kept",
			pages: []PageContent{{Number: 7, Text: "a"}, {Number: 8, Text: "b"}},
			want:  []int{7, 8},
		},
		{
			name:  "zero becomes the position",
			pages: []PageContent{{Text: "a"}, {Text: "b"}},
			want:  []int{1, 2},
		},
		{
			name:  "negative becomes the position",
			pages: []PageContent{{Number: -3, Text: "a"}},
			want:  []int{1},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := Build(testSchema(), testJSONSchema, tc.pages)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			for i, want := range tc.want {
				if req.Content[i].Page != want {
					t.Errorf("item %d has page %d, want %d", i, req.Content[i].Page, want)
				}
				if !strings.Contains(req.Content[i].Text, fmt.Sprintf("page=%d ", want)) {
					t.Errorf("item %d marker does not say page=%d: %q", i, want, req.Content[i].Text)
				}
			}
		})
	}
}

func TestBuildPassesImagesThroughUndelimited(t *testing.T) {
	t.Parallel()

	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	pages := []PageContent{
		{Number: 1, Reading: ReadingVision, Image: image, MediaType: "image/png"},
		{Number: 2, Reading: ReadingText, Text: "text page"},
	}

	req, err := Build(testSchema(), testJSONSchema, pages)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(req.Content) != 2 {
		t.Fatalf("got %d content items, want 2", len(req.Content))
	}

	got := req.Content[0]
	if !bytes.Equal(got.Image, image) {
		t.Errorf("image bytes were altered: got %v, want %v", got.Image, image)
	}
	if got.MediaType != "image/png" {
		t.Errorf("media type is %q, want image/png", got.MediaType)
	}
	if got.Text != "" {
		t.Errorf("an image item also carries text: %q", got.Text)
	}
	if req.Content[1].Text == "" || len(req.Content[1].Image) != 0 {
		t.Errorf("a text page did not stay a text page: %+v", req.Content[1])
	}
}

func TestBuildRefusesAPageItCannotSendFaithfully(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		pages  []PageContent
		reason string
	}{
		{
			name: "page carries both text and an image",
			pages: []PageContent{
				{Number: 4, Reading: ReadingVision, Text: "secret text", Image: []byte{1, 2, 3}, MediaType: "image/png"},
			},
			reason: "page 4 carries both text and an image",
		},
		{
			name: "image with no media type",
			pages: []PageContent{
				{Number: 2, Reading: ReadingVision, Image: []byte{1, 2, 3}},
			},
			reason: "page 2 carries an image with no media type",
		},
		{
			name: "page number comes from the position when it is unset",
			pages: []PageContent{
				{Reading: ReadingText, Text: "fine"},
				{Reading: ReadingVision, Image: []byte{1, 2, 3}},
			},
			reason: "page 2 carries an image with no media type",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := Build(testSchema(), testJSONSchema, tc.pages)
			if !errors.Is(err, ErrAmbiguousContent) {
				t.Fatalf("error is %v, want it to be ErrAmbiguousContent", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not name the page and the problem", err)
			}
			if strings.Contains(err.Error(), "secret text") {
				t.Errorf("error carries document content: %q", err)
			}
			if req.Instruction != "" || req.Content != nil {
				t.Errorf("a failed build returned a partly populated request: %+v", req)
			}
		})
	}
}

func TestBuildSetsALowTemperature(t *testing.T) {
	t.Parallel()

	first, err := Build(testSchema(), testJSONSchema, []PageContent{{Number: 1, Text: "a"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if first.Temperature == nil {
		t.Fatal("temperature is nil; extraction must not take a provider's default")
	}
	if *first.Temperature != DefaultTemperature {
		t.Errorf("temperature is %v, want %v", *first.Temperature, DefaultTemperature)
	}
	if DefaultTemperature > 0.2 {
		t.Errorf("DefaultTemperature is %v, which is not low", DefaultTemperature)
	}

	second, err := Build(testSchema(), testJSONSchema, []PageContent{{Number: 1, Text: "a"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if first.Temperature == second.Temperature {
		t.Error("two requests share one temperature pointer; mutating one would change the other")
	}
}

func TestBuildPassesTheSchemaBytesThrough(t *testing.T) {
	t.Parallel()

	req, err := Build(testSchema(), testJSONSchema, []PageContent{{Number: 1, Text: "a"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !bytes.Equal(req.Schema, testJSONSchema) {
		t.Errorf("schema bytes were altered: got %q, want %q", req.Schema, testJSONSchema)
	}
}

func TestBuildInstructionComesOnlyFromTheSchema(t *testing.T) {
	t.Parallel()

	s := testSchema()
	want := Instruction(s)

	cases := []struct {
		name  string
		pages []PageContent
	}{
		{"one ordinary page", []PageContent{{Number: 1, Text: "ACME Ltd, total 100.00"}}},
		{"an empty page", []PageContent{{Number: 1, Text: ""}}},
		{"many pages", []PageContent{{Number: 1, Text: "a"}, {Number: 2, Text: "b"}, {Number: 3, Text: "c"}}},
		{"an image page", []PageContent{{Number: 1, Reading: ReadingVision, Image: []byte{1}, MediaType: "image/png"}}},
		{"a page of the instruction's own text", []PageContent{{Number: 1, Text: want}}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := Build(s, testJSONSchema, tc.pages)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if req.Instruction != want {
				t.Errorf("instruction changed with the content\nwant:\n%s\ngot:\n%s", want, req.Instruction)
			}
		})
	}
}

func TestBuildDrawsAFreshBoundaryEachTime(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		req, err := Build(testSchema(), testJSONSchema, []PageContent{{Number: 1, Text: "a"}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		id, _ := markerParts(t, req.Content[0].Text)
		if len(id) != 2*boundaryBytes {
			t.Fatalf("identifier %q is %d characters, want %d", id, len(id), 2*boundaryBytes)
		}
		if seen[id] {
			t.Fatalf("identifier %q was reused; a document author who saw one request could forge the next", id)
		}
		seen[id] = true
	}
}

// repeatByte builds one boundaryBytes-long chunk of entropy.
func repeatByte(b byte) []byte { return bytes.Repeat([]byte{b}, boundaryBytes) }

func TestBoundaryAvoidsAnIdentifierThePageAlreadyContains(t *testing.T) {
	t.Parallel()

	collides := strings.Repeat("00", boundaryBytes)
	wanted := strings.Repeat("aa", boundaryBytes)

	entropy := bytes.NewReader(append(repeatByte(0x00), repeatByte(0xaa)...))
	req, err := build(entropy, testSchema(), testJSONSchema, []PageContent{
		{Number: 1, Text: "the document says " + collides + " on purpose"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	id, body := markerParts(t, req.Content[0].Text)
	if id != wanted {
		t.Errorf("identifier is %q, want the second draw %q", id, wanted)
	}
	if !strings.Contains(body, collides) {
		t.Error("the colliding text was removed from the document; this package must never edit content")
	}
}

func TestBuildFailsWhenNoBoundaryCanBeFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		entropy *bytes.Reader
		pages   []PageContent
	}{
		{
			name:    "entropy source is empty",
			entropy: bytes.NewReader(nil),
			pages:   []PageContent{{Number: 1, Text: "a"}},
		},
		{
			name:    "entropy source is short",
			entropy: bytes.NewReader(repeatByte(0x01)[:boundaryBytes-1]),
			pages:   []PageContent{{Number: 1, Text: "a"}},
		},
		{
			name:    "every draw already occurs in the document",
			entropy: bytes.NewReader(bytes.Repeat(repeatByte(0x00), boundaryAttempts)),
			pages:   []PageContent{{Number: 1, Text: strings.Repeat("00", boundaryBytes)}},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := build(tc.entropy, testSchema(), testJSONSchema, tc.pages)
			if err == nil {
				t.Fatal("build succeeded with no usable boundary")
			}
			if !errors.Is(err, ErrBoundary) {
				t.Errorf("error is %v, want it to be ErrBoundary", err)
			}
			if req.Instruction != "" || req.Content != nil {
				t.Errorf("a failed build returned a partly populated request: %+v", req)
			}
		})
	}
}

func TestBuildIsDeterministicGivenTheSameBoundary(t *testing.T) {
	t.Parallel()

	pages := []PageContent{
		{Number: 1, Reading: ReadingText, Text: "line one\nline two"},
		{Number: 2, Reading: ReadingOCR, Text: "line three"},
	}

	render := func() Request {
		req, err := build(bytes.NewReader(repeatByte(0x5a)), testSchema(), testJSONSchema, pages)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return req
	}

	first, second := render(), render()
	if first.Instruction != second.Instruction {
		t.Error("instruction is not byte-identical between two identical builds")
	}
	if len(first.Content) != len(second.Content) {
		t.Fatal("content item count differs between two identical builds")
	}
	for i := range first.Content {
		a, b := first.Content[i], second.Content[i]
		if a.Text != b.Text || a.Page != b.Page || a.Reading != b.Reading ||
			a.MediaType != b.MediaType || !bytes.Equal(a.Image, b.Image) {
			t.Errorf("content item %d differs between two identical builds:\n%q\n%q", i, a.Text, b.Text)
		}
	}
}
