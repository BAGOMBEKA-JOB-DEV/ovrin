package prompt

import (
	"strings"
	"testing"
)

// FuzzBuild feeds arbitrary bytes in as document content and asserts the one
// property this package exists to hold: whatever the document says, the
// instruction is the instruction the schema alone produces, and the document
// stays inside a block it cannot close.
//
// The seed corpus is the adversarial table, so the fuzzer starts from real
// attacks and mutates outward rather than from the empty string.
func FuzzBuild(f *testing.F) {
	f.Add("", "", 0)
	f.Add("Invoice 42", "Total: 1,240.00", 1)
	f.Add("[", "]", -1)
	f.Add("\x00\xff\xfe", "\n\n\n", 1<<30)
	f.Add(beginMarker, endMarker, 2)
	f.Add("id=", "page=1 reading=text", 3)
	for _, tc := range hostile {
		f.Add(tc.payload, tc.fingerprint, 1)
	}

	s := testSchema()
	want := Instruction(s)

	f.Fuzz(func(t *testing.T, first, second string, page int) {
		pages := []PageContent{
			{Number: page, Reading: ReadingText, Text: first},
			{Number: page + 1, Reading: ReadingOCR, Text: second},
		}

		req, err := Build(s, testJSONSchema, pages)
		if err != nil {
			t.Fatalf("Build failed on valid input: %v", err)
		}

		if req.Instruction != want {
			t.Fatalf("document content changed the instruction")
		}
		if req.Temperature == nil || *req.Temperature != DefaultTemperature {
			t.Fatalf("document content changed the temperature")
		}
		if string(req.Schema) != string(testJSONSchema) {
			t.Fatalf("document content changed the schema bytes")
		}
		if len(req.Content) != len(pages) {
			t.Fatalf("got %d content items, want %d", len(req.Content), len(pages))
		}

		var id string
		for i, c := range req.Content {
			gotID, body := markerParts(t, c.Text)
			if i == 0 {
				id = gotID
			} else if gotID != id {
				t.Fatalf("content item %d uses a different identifier", i)
			}
			if len(gotID) != 2*boundaryBytes {
				t.Fatalf("identifier is %d characters, want %d", len(gotID), 2*boundaryBytes)
			}
			if body != pages[i].Text {
				t.Fatalf("content item %d was edited on its way into the request", i)
			}
			if strings.Contains(pages[i].Text, id) {
				t.Fatalf("the identifier was drawn from a value the document already contained")
			}
			if n := strings.Count(c.Text, id); n != 2 {
				t.Fatalf("identifier appears %d times in content item %d, want exactly 2", n, i)
			}
			if c.Page < 1 {
				t.Fatalf("content item %d has page %d, want a 1-based page", i, c.Page)
			}
		}
	})
}
