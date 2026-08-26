// The panic conversions in this package's entry points.
//
// Gap closed here: three recover blocks — in Open, in Doc.Page and in
// Doc.Metadata — had no test at all. docs/threat-model.md T3 counts them as a
// mitigation: a bug in this parser, reached by a document an attacker chose,
// must cost the calling service an error rather than its process. That promise
// had never been read back, and the block in Doc.Metadata is the one worth
// worrying about, because it does not produce an error at all — it returns nil
// and the caller cannot tell a document with no metadata from a scan that fell
// over.
//
// How the panic is raised needs saying plainly. No document is known that makes
// this parser panic: hostile_test.go is a long list of documents that try, and
// every one of them is handled, which is the whole point of the package. A
// fixture that panicked would be a bug to fix rather than a fixture to keep. So
// the panic here is induced from inside the package, by clearing the object
// cache on an already-open Doc: the next object load writes to a nil map, which
// is an ordinary Go runtime panic raised from inside the parser, on the real
// call path, under the real defer. It is not a document an attacker could send;
// it stands in for the bug nobody has found yet, which is exactly what the
// recover blocks exist for.
//
// Each test also asserts by its own survival. If the conversion were removed,
// these tests would not fail — the binary would crash, taking the rest of the
// suite with it, which is the failure the mitigation prevents in a service.
package pdf

import (
	"errors"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/detect"
)

// panicking returns an open document whose next object load will panic.
//
// It returns the Doc rather than the corruption so that a test can read the
// document first: the before-and-after is what proves the failure came from the
// induced panic and not from a document that never had anything to give.
func panicking(t *testing.T, data []byte) *Doc {
	t.Helper()

	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The cache is written on every object load. A nil map is the cheapest
	// real runtime panic reachable on the actual parse path; nothing about
	// this test depends on which panic it is.
	doc.cache = nil
	return doc
}

// panicPage is a readable one-page document whose content and information
// dictionary both name things that must never reach an error message.
func panicPage() []byte {
	return buildPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << " + helvetica + " >> >> /Contents 4 0 R >>",
		streamObj("", "BT /F1 12 Tf 72 720 Td (JaneDoe) Tj ET"),
		"<< /Title (Statement for Jane Doe account 4471) >>",
	}, "/Info 5 0 R ")
}

// A panic while reading a page becomes an error the caller can act on.
func TestAPanicWhileReadingAPageBecomesAnError(t *testing.T) {
	t.Parallel()

	data := panicPage()

	// The same document, read normally. Without this the test below would
	// pass against a document that was never readable.
	if got := words(openPage(t, data)); got != "JaneDoe" {
		t.Fatalf("words = %q, want %q: the fixture is not readable, so the failure "+
			"below would prove nothing", got, "JaneDoe")
	}

	doc := panicking(t, data)
	page, err := doc.Page(1)

	if err == nil {
		t.Fatal("a panic inside the parser produced no error")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want ErrMalformed: a recovered panic is a document this "+
			"package could not read", err)
	}
	if len(page.Content.Words) != 0 || page.Stats.Chars != 0 {
		t.Errorf("a half-built page came back alongside the error: %d word(s), %d character(s). "+
			"A caller that checks the error will not see it, and one that does not must not "+
			"be handed the debris of a failed parse", len(page.Content.Words), page.Stats.Chars)
	}
	assertNoDocumentContent(t, err)
}

// The recovery is per call, not per document: a Doc that failed once must not
// be left in a state where every later call fails differently or crashes.
func TestEveryPageOfAPanickingDocumentFailsTheSameWay(t *testing.T) {
	t.Parallel()

	doc := panicking(t, panicPage())
	for i := 0; i < 3; i++ {
		if _, err := doc.Page(1); !errors.Is(err, ErrMalformed) {
			t.Fatalf("call %d: Page(1) = %v, want ErrMalformed every time", i, err)
		}
	}
}

// A panic while reading metadata costs the metadata and nothing else.
//
// This block returns nil rather than an error, which is a deliberate and
// uncomfortable choice: metadata is scanned for instruction-shaped language and
// is not part of the text, so losing it must not fail an extraction that could
// otherwise proceed. The cost is that the scan can silently not happen, and a
// caller has no way to tell that from a document that carried no metadata. The
// test records the behaviour precisely so that a change to it is a decision
// rather than an accident.
func TestAPanicWhileReadingMetadataReturnsNothingRatherThanCrashing(t *testing.T) {
	t.Parallel()

	data := panicPage()

	doc, err := Open(data, detect.Limits{}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := doc.Metadata(); len(got) != 1 || got[0].Key != "Title" {
		t.Fatalf("Metadata() = %v, want the one Title this document carries; "+
			"the fixture is wrong and the failure below would prove nothing", got)
	}

	// The same document, with the next object load poisoned. Reaching the
	// assertion at all is the first half of it.
	if got := panicking(t, data).Metadata(); got != nil {
		t.Errorf("Metadata() = %v, want nil after a recovered panic: a partial scan is "+
			"worse than none, because it reads as a document with nothing to hide", got)
	}
}

// assertNoDocumentContent fails when an error repeats anything the document
// said.
//
// A recovered panic is the easiest place in a parser to leak content, because
// the panic value is whatever the panicking code had to hand and that is very
// often a piece of the document (docs/rules.md §2.5, §7.5).
func assertNoDocumentContent(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{"JaneDoe", "Jane Doe", "4471", "Statement"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error repeats document content %q: %v", secret, err)
		}
	}
}
