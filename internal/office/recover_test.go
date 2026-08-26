// The panic conversion at this package's entry points.
//
// Gap closed here: recovered was half covered. Every test in the package
// returns normally, so the branch that runs when there is nothing to recover
// was exercised by all of them and the branch that runs when there is
// something was exercised by none. docs/threat-model.md calls this conversion a
// T3 mitigation — the promise is that a bug in a parser reading a hostile
// document costs the calling service an error and not its process — and an
// untested recovery is a promise nobody has read back.
//
// No document is known that makes this package panic; one would be a bug and
// would be fixed rather than kept as a fixture. What is tested here is
// therefore the conversion itself, driven by a panic this file raises, which is
// the only thing that can be tested without shipping the bug the recovery
// exists for.
package office

import (
	"errors"
	"strings"
	"testing"
)

// panicPayload is what a panic inside a parser could plausibly carry: a slice
// header, an index, and — because a panic value is whatever the panicking code
// had to hand — a piece of the document.
const panicPayload = "runtime error: index out of range, near \"Jane Doe, account 4471\""

// extractWithRecovery stands in for an entry point: it installs the same
// deferred conversion the exported Extract functions do and then does whatever
// body says.
//
// Calling recovered through a function with the same signature shape is the
// closest a test can get to the real call sites without a document that
// panics; the deferred call, the named results and the assignment it performs
// are the same ones.
func extractWithRecovery(body func() *Document) (doc *Document, err error) {
	defer recovered(&doc, &err)
	return body(), nil
}

// A panic must leave the caller with an error, not with a crashed process.
func TestAPanicInAParserBecomesAMalformedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		panic func()
	}{
		{"a runtime failure", func() { var p *Document; _ = p.Pages }},
		{"an explicit panic carrying a string", func() { panic(panicPayload) }},
		{"an explicit panic carrying an error", func() { panic(errors.New(panicPayload)) }},
		{"a panic carrying nothing useful", func() { panic(struct{}{}) }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Reaching the next line at all is half the assertion: if the
			// conversion were removed, this test would take the whole binary
			// down rather than fail.
			doc, err := extractWithRecovery(func() *Document {
				tt.panic()
				return &Document{}
			})

			if err == nil {
				t.Fatal("a panic inside the parser produced no error")
			}
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed: a recovered panic is a document "+
					"this package could not read", err)
			}
			if doc != nil {
				t.Errorf("a document came back alongside a recovered panic: %+v", doc)
			}
		})
	}
}

// The panic value is not the error message.
//
// A panic value is whatever the panicking code had to hand, and inside a
// parser that is very often a piece of the document — a run of text, a cell, a
// part name. Putting it in the error would put document content in a log line
// (docs/rules.md §2.5, §7.5), which is the one thing this package is careful
// about everywhere else.
func TestARecoveredPanicDoesNotQuoteThePanicValue(t *testing.T) {
	t.Parallel()

	_, err := extractWithRecovery(func() *Document { panic(panicPayload) })
	if err == nil {
		t.Fatal("a panic inside the parser produced no error")
	}
	for _, secret := range []string{"Jane Doe", "4471", panicPayload} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error repeats the panic value %q: %v", secret, err)
		}
	}
}

// An ordinary return must pass through untouched.
//
// The deferred call runs on every entry, successful or not, so a conversion
// that was careless about the no-panic case would blank out every document
// this package ever read.
func TestRecoveredLeavesAnOrdinaryReturnAlone(t *testing.T) {
	t.Parallel()

	want := &Document{HiddenRuns: 3}
	doc, err := extractWithRecovery(func() *Document { return want })
	if err != nil {
		t.Fatalf("err = %v, want nil on a call that did not panic", err)
	}
	if doc != want {
		t.Errorf("doc = %+v, want the document the parser returned", doc)
	}
}
