package office

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// isUnsupported reports whether an error answers [ErrUnsupported].
func isUnsupported(err error) bool { return errors.Is(err, ErrUnsupported) }

// tightLimits are far below the defaults, so that "bounded" and "unbounded"
// are distinguishable in the time a test gets. A ceiling of 512 MiB proves
// nothing in a second.
func tightLimits() detect.Limits {
	return detect.Limits{
		MaxSourceBytes:       1 << 20,
		MaxDecompressedBytes: 1 << 20,
		MaxStreamBytes:       1 << 18,
		MaxTextBytes:         1 << 16,
		MaxPages:             8,
		MaxDepth:             16,
		MaxObjects:           2000,
	}
}

// centralDirSignature begins each central directory record, which is where
// archive/zip reads an entry's declared sizes and method from.
var centralDirSignature = []byte{'P', 'K', 0x01, 0x02}

// patchCentral rewrites a field of the named entry's central directory record.
//
// It exists because archive/zip's writer will not produce the archives an
// attacker produces: it will not declare a size that disagrees with the
// content, and it will not write a compression method it cannot compress with.
// Those are the archives that have to be tested, so the bytes are edited
// directly.
func patchCentral(t *testing.T, data []byte, name string, offset int, value uint64, width int) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	for i := 0; i+46 <= len(out); i++ {
		if !bytes.Equal(out[i:i+4], centralDirSignature) {
			continue
		}
		nameLen := int(binary.LittleEndian.Uint16(out[i+28:]))
		if i+46+nameLen > len(out) {
			continue
		}
		if string(out[i+46:i+46+nameLen]) != name {
			continue
		}
		switch width {
		case 2:
			binary.LittleEndian.PutUint16(out[i+offset:], uint16(value))
		case 4:
			binary.LittleEndian.PutUint32(out[i+offset:], uint32(value))
		default:
			t.Fatalf("unsupported patch width %d", width)
		}
		return out
	}
	t.Fatalf("entry not found in the central directory")
	return nil
}

// Offsets within a central directory record.
const (
	centralFlags            = 8
	centralMethod           = 10
	centralUncompressedSize = 24
)

// TestZipBombIsRefused covers both shapes of the attack: one enormous entry,
// and many merely large ones that only exceed the budget together.
func TestZipBombIsRefused(t *testing.T) {
	t.Parallel()
	// Highly compressible: a megabyte of one byte deflates to a few hundred.
	filler := bytes.Repeat([]byte("A"), 1<<20)

	t.Run("one entry larger than the per-stream ceiling", func(t *testing.T) {
		t.Parallel()
		body := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>` +
			string(filler) + `</w:t></w:r></w:p></w:body></w:document>`
		data := zipOf(t,
			entry{name: contentTypesName, body: []byte(contentTypes)},
			entry{name: docxBody, body: []byte(body)},
		)
		if len(data) > 1<<16 {
			t.Fatalf("fixture is %d bytes; the point is that a tiny archive expands hugely", len(data))
		}
		_, err := ExtractDOCX(data, tightLimits(), nil)
		if !errors.Is(err, detect.ErrLimitExceeded) {
			t.Fatalf("err = %v, want a limit failure", err)
		}
	})

	t.Run("many entries that only exceed the budget together", func(t *testing.T) {
		t.Parallel()
		// Each header is well under the per-stream ceiling and they are only
		// a bomb cumulatively — a thousand entries of a mebibyte is the same
		// attack as one entry of a gibibyte (ADR-0020). All of them are read,
		// because docxSkipped opens every auxiliary part to find out whether
		// it held text.
		hdr := `<?xml version="1.0"?><w:hdr ` + wordNS + `><w:p><w:r><w:t>` +
			strings.Repeat("h", 200<<10) + `</w:t></w:r></w:p></w:hdr>`
		entries := []entry{
			{name: contentTypesName, body: []byte(contentTypes)},
			{name: docxBody, body: []byte(`<?xml version="1.0"?><w:document ` + wordNS + `><w:body/></w:document>`)},
		}
		for i := 1; i <= 40; i++ {
			entries = append(entries, entry{name: fmt.Sprintf("word/header%d.xml", i), body: []byte(hdr)})
		}
		lim := tightLimits()
		lim.MaxStreamBytes = 1 << 20       // each entry fits comfortably
		lim.MaxDecompressedBytes = 1 << 20 // together they do not
		lim.MaxTextBytes = 1 << 20
		_, err := ExtractDOCX(zipOf(t, entries...), lim, nil)
		if !errors.Is(err, detect.ErrLimitExceeded) {
			t.Fatalf("err = %v, want a cumulative limit failure", err)
		}
	})

	t.Run("the cumulative budget is shared with the caller's", func(t *testing.T) {
		t.Parallel()
		body := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>` +
			strings.Repeat("x", 300<<10) + `</w:t></w:r></w:p></w:body></w:document>`
		data := zipOf(t, entry{name: docxBody, body: []byte(body)})
		lim := tightLimits()
		lim.MaxStreamBytes = 1 << 20
		lim.MaxTextBytes = 1 << 20
		// A budget the caller has already mostly spent on another part of the
		// same document leaves nothing for this one.
		cum := detect.NewCounter(detect.LimitDecompressedBytes, 1<<20)
		if err := cum.Add(1<<20 - 1024); err != nil {
			t.Fatalf("priming the counter: %v", err)
		}
		_, err := ExtractDOCX(data, lim, cum)
		if !errors.Is(err, detect.ErrLimitExceeded) {
			t.Fatalf("err = %v, want a limit failure against the shared budget", err)
		}
	})
}

func TestManyEntriesRefused(t *testing.T) {
	t.Parallel()
	entries := []entry{
		{name: contentTypesName, body: []byte(contentTypes)},
		{name: docxBody, body: []byte(`<?xml version="1.0"?><w:document ` + wordNS + `><w:body/></w:document>`)},
	}
	for i := 0; i < 10000; i++ {
		entries = append(entries, entry{name: fmt.Sprintf("junk/%d.bin", i), body: []byte("x"), store: true})
	}
	data := zipOf(t, entries...)

	done := make(chan error, 1)
	go func() { _, err := ExtractDOCX(data, tightLimits(), nil); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a container with ten thousand entries was accepted")
		}
		if !isUnsupported(err) && !errors.Is(err, detect.ErrLimitExceeded) {
			t.Fatalf("err = %v, want a refusal naming the entry count or a limit", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("extraction did not terminate")
	}
}

func TestDeeplyNestedXMLIsBounded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "nesting the walker descends through spends the depth budget",
			body:    strings.Repeat(`<w:p>`, 5000) + strings.Repeat(`</w:p>`, 5000),
			wantErr: true,
		},
		{
			name: "nesting inside a skipped subtree is stepped over without recursion",
			// skipElement is iterative on purpose: encoding/xml's own
			// Decoder.Skip recurses once per level and would exhaust the
			// stack here.
			body:    `<w:del>` + strings.Repeat(`<w:x>`, 50000) + strings.Repeat(`</w:x>`, 50000) + `</w:del>`,
			wantErr: false,
		},
		{
			name:    "nesting inside run properties is counted rather than recursed",
			body:    `<w:p><w:r><w:rPr>` + strings.Repeat(`<w:x>`, 50000) + strings.Repeat(`</w:x>`, 50000) + `</w:rPr><w:t>ok</w:t></w:r></w:p>`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lim := tightLimits()
			lim.MaxStreamBytes = 4 << 20
			lim.MaxDecompressedBytes = 4 << 20
			_, err := ExtractDOCX(docxOf(t, tt.body), lim, nil)
			if tt.wantErr {
				if !errors.Is(err, detect.ErrLimitExceeded) {
					t.Fatalf("err = %v, want a depth limit failure", err)
				}
				return
			}
			if err != nil && !errors.Is(err, detect.ErrLimitExceeded) {
				t.Fatalf("err = %v, want success or a limit failure, never a crash", err)
			}
		})
	}
}

func TestDeclaredSizeDisagreesWithContent(t *testing.T) {
	t.Parallel()
	good := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`

	t.Run("a declaration far above the ceiling is refused before opening", func(t *testing.T) {
		t.Parallel()
		data := zipOf(t, entry{name: docxBody, body: []byte(good)})
		// 0xFFFFFFFE rather than 0xFFFFFFFF, which would mean "see the ZIP64
		// extra field" instead of a very large number.
		data = patchCentral(t, data, docxBody, centralUncompressedSize, 0xFFFFFFFE, 4)
		_, err := ExtractDOCX(data, tightLimits(), nil)
		if !errors.Is(err, detect.ErrLimitExceeded) {
			t.Fatalf("err = %v, want the declared size to be refused", err)
		}
	})

	t.Run("a declaration far below the content does not raise the ceiling", func(t *testing.T) {
		t.Parallel()
		body := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>` +
			strings.Repeat("z", 512<<10) + `</w:t></w:r></w:p></w:body></w:document>`
		data := zipOf(t, entry{name: docxBody, body: []byte(body)})
		// The entry now claims to be one byte. If the declaration were
		// believed instead of the wrapped reader, half a mebibyte would sail
		// past a ceiling of 4 KiB.
		data = patchCentral(t, data, docxBody, centralUncompressedSize, 1, 4)
		lim := tightLimits()
		lim.MaxStreamBytes = 4 << 10
		doc, err := ExtractDOCX(data, lim, nil)
		if err == nil {
			t.Fatalf("a lying declaration was accepted, producing %d pages", len(doc.Pages))
		}
	})
}

func TestTruncatedArchive(t *testing.T) {
	t.Parallel()
	full := docxOf(t, `<w:p><w:r><w:t>hello</w:t></w:r></w:p>`)
	cuts := map[string][]byte{
		"empty":             nil,
		"header only":       full[:4],
		"half":              full[:len(full)/2],
		"missing end":       full[:len(full)-8],
		"central directory": full[:len(full)-1],
	}
	for name, data := range cuts {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ExtractDOCX(data, tightLimits(), nil)
			if err == nil {
				t.Fatal("a truncated archive was accepted")
			}
			assertNoDocumentContent(t, err)
		})
	}
}

func TestZipInZipIsNotFollowed(t *testing.T) {
	t.Parallel()

	t.Run("a nested archive entry is never opened", func(t *testing.T) {
		t.Parallel()
		// A bomb inside an entry this package has no name for costs nothing,
		// because parts are addressed by exact name and this one is not asked
		// for.
		bomb := zipOf(t, entry{name: "big.bin", body: bytes.Repeat([]byte("A"), 1<<20)})
		data := docxOf(t, `<w:p><w:r><w:t>outer</w:t></w:r></w:p>`,
			entry{name: "nested.zip", body: bomb, store: true})
		doc, err := ExtractDOCX(data, tightLimits(), nil)
		if err != nil {
			t.Fatalf("ExtractDOCX: %v", err)
		}
		if got := render(doc.Pages[0]); !equal(got, []string{"1:outer"}) {
			t.Errorf("words = %q, want the outer document only", got)
		}
	})

	t.Run("an archive standing in for the body part is not recursed into", func(t *testing.T) {
		t.Parallel()
		inner := zipOf(t, entry{name: docxBody, body: []byte(`<?xml version="1.0"?><w:document ` + wordNS + `><w:body/></w:document>`)})
		data := zipOf(t,
			entry{name: contentTypesName, body: []byte(contentTypes)},
			entry{name: docxBody, body: inner, store: true},
		)
		_, err := ExtractDOCX(data, tightLimits(), nil)
		if err == nil {
			t.Fatal("a zip presented as the body part was accepted as xml")
		}
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
		assertNoDocumentContent(t, err)
	})
}

// TestEntityExpansionIsImpossible is the confirmation behind the claim in the
// package comment. Each case is one of the routes the attack takes.
func TestEntityExpansionIsImpossible(t *testing.T) {
	t.Parallel()

	t.Run("the decoder this package builds cannot expand", func(t *testing.T) {
		t.Parallel()
		d := newDecoder(strings.NewReader("<a/>"))
		if d.Entity != nil {
			t.Error("Entity is not nil; a populated entity map is what makes expansion possible at all")
		}
		if !d.Strict {
			t.Error("Strict is false; a non-strict decoder passes an unknown entity through as text")
		}
	})

	t.Run("a doctype internal subset never populates the entity map", func(t *testing.T) {
		t.Parallel()
		// This is the property the whole defence rests on: encoding/xml
		// reports a DOCTYPE as one opaque Directive and never reads the
		// declarations inside it, so nine levels of nested entities leave
		// nothing behind to expand.
		d := xml.NewDecoder(strings.NewReader(billionLaughs("r")))
		for {
			if _, err := d.Token(); err != nil {
				break
			}
		}
		if len(d.Entity) != 0 {
			t.Fatalf("Entity = %v after parsing a dtd declaring entities; expansion would be possible", d.Entity)
		}
	})

	t.Run("a document type declaration is refused outright", func(t *testing.T) {
		t.Parallel()
		data := zipOf(t, entry{name: docxBody, body: []byte(billionLaughs("w:document"))})
		_, err := ExtractDOCX(data, tightLimits(), nil)
		if !isUnsupported(err) {
			t.Fatalf("err = %v, want a refusal of the document type declaration", err)
		}
	})

	t.Run("an entity reference with no declaration is an error, not text", func(t *testing.T) {
		t.Parallel()
		body := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r>` +
			`<w:t>&lol9;</w:t></w:r></w:p></w:body></w:document>`
		data := zipOf(t, entry{name: docxBody, body: []byte(body)})
		_, err := ExtractDOCX(data, tightLimits(), nil)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
		assertNoDocumentContent(t, err)
	})

	t.Run("an external entity is neither resolved nor fetched", func(t *testing.T) {
		t.Parallel()
		for _, target := range []string{"file:///etc/passwd", "http://127.0.0.1:1/x"} {
			doc := `<?xml version="1.0"?><!DOCTYPE w:document [<!ENTITY xxe SYSTEM "` + target + `">]>` +
				`<w:document ` + wordNS + `><w:body><w:p><w:r><w:t>&xxe;</w:t></w:r></w:p></w:body></w:document>`
			data := zipOf(t, entry{name: docxBody, body: []byte(doc)})
			got, err := ExtractDOCX(data, tightLimits(), nil)
			if err == nil {
				t.Fatalf("target %q: an external entity reference was accepted, yielding %d pages", target, len(got.Pages))
			}
			assertNoDocumentContent(t, err)
		}
	})

	t.Run("expansion produces no text even with a large budget", func(t *testing.T) {
		t.Parallel()
		// The belt-and-braces DOCTYPE refusal could mask a decoder that would
		// have expanded, so the same payload is driven through the decoder
		// directly and the character data it yields is counted.
		d := newDecoder(strings.NewReader(billionLaughs("r")))
		chars, tokens := 0, 0
		for tokens < 100000 {
			tok, err := d.Token()
			if err != nil {
				break
			}
			tokens++
			if c, ok := tok.(xml.CharData); ok {
				chars += len(c)
			}
		}
		if chars != 0 {
			t.Fatalf("the payload produced %d bytes of character data; nothing should have expanded", chars)
		}
	})
}

// billionLaughs returns the classic payload with root as the document element.
func billionLaughs(root string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><!DOCTYPE ` + root + ` [` + "\n")
	b.WriteString(` <!ENTITY lol "lol">` + "\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&b, ` <!ENTITY lol%d "`, i)
		for j := 0; j < 10; j++ {
			if i == 1 {
				b.WriteString(`&lol;`)
			} else {
				fmt.Fprintf(&b, `&lol%d;`, i-1)
			}
		}
		b.WriteString(`">` + "\n")
	}
	b.WriteString(`]><` + root + ` ` + wordNS + `>&lol9;</` + root + `>`)
	return b.String()
}

func TestEncryptedEntryIsNamed(t *testing.T) {
	t.Parallel()
	data := docxOf(t, `<w:p><w:r><w:t>hello</w:t></w:r></w:p>`)
	// Bit 0 of the general purpose flags marks the entry's data encrypted.
	data = patchCentral(t, data, docxBody, centralFlags, 0x0009, 2)
	_, err := ExtractDOCX(data, tightLimits(), nil)
	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("err = %v, want ErrEncrypted", err)
	}
	assertNoDocumentContent(t, err)
}

func TestUnsupportedCompressionMethodIsRefused(t *testing.T) {
	t.Parallel()
	data := zipOf(t, entry{name: docxBody, body: []byte(`<?xml version="1.0"?><w:document ` + wordNS + `><w:body/></w:document>`)})
	data = patchCentral(t, data, docxBody, centralMethod, 99, 2)
	_, err := ExtractDOCX(data, tightLimits(), nil)
	if !isUnsupported(err) {
		t.Fatalf("err = %v, want the method refused by name rather than attempted", err)
	}
	assertNoDocumentContent(t, err)
}

func TestDuplicatePartsTakeTheFirst(t *testing.T) {
	t.Parallel()
	// Two parts of one name is an attempt to have two readers disagree about
	// which is the document. Whichever this package picks, it must pick the
	// same one every time.
	doc := func(s string) []byte {
		return []byte(`<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>` + s + `</w:t></w:r></w:p></w:body></w:document>`)
	}
	data := zipOf(t,
		entry{name: docxBody, body: doc("first")},
		entry{name: docxBody, body: doc("second")},
	)
	for i := 0; i < 3; i++ {
		got, err := ExtractDOCX(data, tightLimits(), nil)
		if err != nil {
			t.Fatalf("ExtractDOCX: %v", err)
		}
		if words := render(got.Pages[0]); !equal(words, []string{"1:first"}) {
			t.Fatalf("run %d read %q, want the first part of that name", i, words)
		}
	}
}

func TestWordCeilingIsEnforced(t *testing.T) {
	t.Parallel()
	// Many tiny cells are how the word count runs away from the byte budget:
	// each normalise.Word costs far more than the byte it carries.
	var row strings.Builder
	row.WriteString(`<row r="1">`)
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&row, `<c r="A%d"><v>1</v></c>`, i+1)
	}
	row.WriteString(`</row>`)
	lim := tightLimits()
	lim.MaxStreamBytes = 4 << 20
	lim.MaxDecompressedBytes = 4 << 20
	lim.MaxTextBytes = 4 << 20
	_, err := ExtractXLSX(xlsxOf(t, nil, row.String()), lim, nil)
	if !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("err = %v, want the per-row cell ceiling to refuse it", err)
	}
}

func TestSheetCountIsBoundedBeforePagesAreBuilt(t *testing.T) {
	t.Parallel()
	sheets := make([]string, 32)
	for i := range sheets {
		sheets[i] = `<row r="1"><c r="A1"><v>1</v></c></row>`
	}
	lim := tightLimits() // MaxPages is 8
	_, err := ExtractXLSX(xlsxOf(t, nil, sheets...), lim, nil)
	if !errors.Is(err, detect.ErrLimitExceeded) {
		t.Fatalf("err = %v, want the page ceiling to refuse the workbook", err)
	}
}

func TestExternalRelationshipsAreNotFollowed(t *testing.T) {
	t.Parallel()
	wb := `<?xml version="1.0"?><workbook ` + sheetNS + `><sheets>` +
		`<sheet name="S" sheetId="1" r:id="rA"/></sheets></workbook>`
	rels := `<?xml version="1.0"?><Relationships ` + relsNS + `>` +
		`<Relationship Id="rA" TargetMode="External" Target="http://127.0.0.1:1/sheet.xml"/>` +
		`</Relationships>`
	data := zipOf(t,
		entry{name: xlsxWorkbook, body: []byte(wb)},
		entry{name: xlsxWorkbookRels, body: []byte(rels)},
	)
	doc, err := ExtractXLSX(data, tightLimits(), nil)
	if err != nil {
		t.Fatalf("ExtractXLSX: %v", err)
	}
	for _, p := range doc.Pages {
		if len(p.Words) != 0 {
			t.Fatalf("page %d has words; nothing a document references is fetched", p.Number)
		}
	}
}

func TestRelationshipTargetsAreNotTraversed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target string
		want   string
		ok     bool
	}{
		{name: "relative", target: "worksheets/sheet1.xml", want: "xl/worksheets/sheet1.xml", ok: true},
		{name: "dot relative", target: "./worksheets/sheet1.xml", want: "xl/worksheets/sheet1.xml", ok: true},
		{name: "package absolute", target: "/xl/worksheets/sheet1.xml", want: "xl/worksheets/sheet1.xml", ok: true},
		{name: "parent reference", target: "../../etc/passwd", ok: false},
		{name: "buried parent reference", target: "worksheets/../../x", ok: false},
		{name: "empty", target: "", ok: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := resolveTarget(tt.target)
			if ok != tt.ok {
				t.Fatalf("resolveTarget(%q) ok = %v, want %v", tt.target, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("resolveTarget(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

// assertNoDocumentContent checks that an error string carries none of the
// document text the fixtures plant in the places an error is tempted to quote:
// an entry name, a cell value, a sheet name (docs/rules.md §2.5, §7.5).
func assertNoDocumentContent(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, leak := range []string{
		"word/", "xl/", ".xml", ".zip", "[Content_Types]",
		"hello", "outer", "Confidential", "lol", "passwd", "127.0.0.1",
	} {
		if strings.Contains(msg, leak) {
			t.Errorf("error message %q contains %q, which is derived from the document", msg, leak)
		}
	}
}

// TestErrorsCarryNoDocumentContent drives every refusal path and checks the
// message each one produces.
func TestErrorsCarryNoDocumentContent(t *testing.T) {
	t.Parallel()
	secret := "Confidential"
	cases := map[string]func() error{
		"not a container": func() error {
			_, err := ExtractDOCX([]byte("not a zip at all"), tightLimits(), nil)
			return err
		},
		"missing body part": func() error {
			_, err := ExtractDOCX(zipOf(t, entry{name: "word/other.xml", body: []byte("<a/>")}), tightLimits(), nil)
			return err
		},
		"malformed xml quoting content": func() error {
			body := `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>` + secret + `</w:t></w:p></w:body></w:document>`
			_, err := ExtractDOCX(zipOf(t, entry{name: docxBody, body: []byte(body)}), tightLimits(), nil)
			return err
		},
		"unsupported kind": func() error {
			_, err := Extract(nil, detect.KindPDF, tightLimits(), nil)
			return err
		},
		"malformed csv": func() error {
			_, err := ExtractCSV([]byte("a,\"unbalanced "+secret+"\nb,c\n"), tightLimits())
			return err
		},
	}
	for name, run := range cases {
		name, run := name, run
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := run()
			if err == nil {
				t.Fatal("want an error")
			}
			assertNoDocumentContent(t, err)
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error message %q quotes document content", err)
			}
		})
	}
}

// TestNoPanicOnMalformedParts drives shapes that a token loop written without
// care would run off the end of.
func TestNoPanicOnMalformedParts(t *testing.T) {
	t.Parallel()
	bodies := map[string]string{
		"unclosed paragraph":  `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>x`,
		"unclosed run props":  `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:rPr><w:vanish/>`,
		"unclosed alternate":  `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><mc:AlternateContent><mc:Choice>`,
		"unclosed skip":       `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:del><w:r>`,
		"stray end element":   `<?xml version="1.0"?><w:document ` + wordNS + `><w:body></w:p></w:body></w:document>`,
		"text with children":  `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:p><w:r><w:t>a<w:t>b</w:t>c</w:t></w:r></w:p></w:body></w:document>`,
		"no elements at all":  `<?xml version="1.0"?>`,
		"empty part":          ``,
		"table row no cells":  `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:tbl><w:tr/></w:tbl></w:body></w:document>`,
		"row outside a table": `<?xml version="1.0"?><w:document ` + wordNS + `><w:body><w:tr><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tr></w:body></w:document>`,
	}
	for name, body := range bodies {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Whatever the answer is, it must be a returned value and not a
			// crash: a malformed document must not take down the calling
			// service (docs/threat-model.md T3).
			_, err := ExtractDOCX(zipOf(t, entry{name: docxBody, body: []byte(body)}), tightLimits(), nil)
			assertNoDocumentContent(t, err)
		})
	}
}

func TestMalformedSheetsDoNotPanic(t *testing.T) {
	t.Parallel()
	sheets := map[string]string{
		"unclosed row":         `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1"><v>1`,
		"unclosed cell":        `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1">`,
		"cell outside a row":   `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><c r="A1"><v>1</v></c></sheetData></worksheet>`,
		"absurd reference":     `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="` + strings.Repeat("A", 5000) + `1"><v>1</v></c></row></sheetData></worksheet>`,
		"non numeric index":    `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1" t="s"><v>not a number</v></c></row></sheetData></worksheet>`,
		"negative index":       `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1" t="s"><v>-5</v></c></row></sheetData></worksheet>`,
		"inline string no is":  `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1" t="inlineStr"/></row></sheetData></worksheet>`,
		"boolean out of range": `<?xml version="1.0"?><worksheet ` + sheetNS + `><sheetData><row r="1"><c r="A1" t="b"><v>7</v></c></row></sheetData></worksheet>`,
	}
	for name, sheet := range sheets {
		name, sheet := name, sheet
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := zipOf(t,
				entry{name: "xl/worksheets/sheet1.xml", body: []byte(sheet)},
			)
			_, err := ExtractXLSX(data, tightLimits(), nil)
			assertNoDocumentContent(t, err)
		})
	}
}
