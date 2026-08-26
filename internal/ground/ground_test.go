package ground

import (
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/normalise"
)

// page builds a one-page normalised document whose lines carry geometry, so
// that the page and box a Result reports can be asserted too.
func page(lines ...string) *normalise.Result {
	var ws []normalise.Word
	for i, l := range lines {
		ws = append(ws, normalise.Word{
			Text: l,
			Line: i,
			Box:  normalise.Rect{MinX: 40, MinY: float64(20 * i), MaxX: 500, MaxY: float64(20*i + 12)},
		})
	}
	return normalise.Normalise(normalise.Input{Pages: []normalise.Page{
		{Number: 1, Width: 595, Height: 842, Words: ws},
	}})
}

// matched returns the text a Result points at, which is what makes a span
// assertion readable in a failure message.
func matched(doc *normalise.Result, r Result) string {
	if r.Span == nil {
		return ""
	}
	return doc.Text[r.Span.Start:r.Span.End]
}

func TestGroundNumbers(t *testing.T) {
	t.Parallel()
	doc := page("Subtotal 25,000 and 1 234,50 and 1'250 and 25000", "Reference 255 for page 3 4")

	cases := []struct {
		name  string
		value any
		want  float64
		text  string
	}{
		{"an unformatted integer matches its own bytes", 25000, Verbatim, "25000"},
		{"a grouped figure matches by value", 24000 + 1000 - 0, Verbatim, "25000"},
		{"a European decimal matches by value", 1234.50, Normalised, "1 234,50"},
		{"an apostrophe group separator matches by value", 1250, Normalised, "1'250"},
		{"a float matches an integer literal", 25000.0, Verbatim, "25000"},
		{"a numeric string matches its own bytes", "25,000", Verbatim, "25,000"},
		{"a leading group does not match on its own", 25, NotFound, ""},
		{"a trailing group does not match on its own", 0, NotFound, ""},
		{"a digit run inside a longer number does not match", 5000, NotFound, ""},
		{"a number that is simply absent is not found", 987654, NotFound, ""},
		{"a number separated by a space is still one number", 1234.5, Normalised, "1 234,50"},
		{"a small number beside another is found", 4, Normalised, "4"},
		{"a number equal to a page number is found in the body", 3, Normalised, "3"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Ground(doc, c.value, KindNumber)
			if r.Grounding != c.want {
				t.Errorf("grounding = %v, want %v (matched %q)", r.Grounding, c.want, matched(doc, r))
			}
			if got := matched(doc, r); got != c.text {
				t.Errorf("matched %q, want %q", got, c.text)
			}
			if (r.Grounding == Verbatim) != r.Exact {
				t.Errorf("Exact = %v with grounding %v", r.Exact, r.Grounding)
			}
		})
	}
}

func TestNumberValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []float64
	}{
		{"plain integer", "42", []float64{42}},
		{"english grouped", "1,234,567", []float64{1234567}},
		{"english grouped with decimals", "1,234.56", []float64{1234.56}},
		{"european grouped with decimals", "1.234,56", []float64{1234.56}},
		{"european decimal", "25,50", []float64{25.5}},
		{"english decimal", "25.50", []float64{25.5}},
		{"single separator with three digits is ambiguous", "1,234", []float64{1234, 1.234}},
		{"single dot with three digits is ambiguous", "1.234", []float64{1234, 1.234}},
		{"three trailing zeros drop the decimal reading", "25,000", []float64{25000}},
		{"space grouping", "25 000", []float64{25000}},
		{"four digits after a separator is a decimal", "1,2345", []float64{1.2345}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := numberValues(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("numberValues(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if !nearlyEqual(got[i], c.want[i]) {
					t.Errorf("numberValues(%q)[%d] = %v, want %v", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestGroundCurrency(t *testing.T) {
	t.Parallel()
	doc := page("Amount due EUR 1.234,56", "Deposit $250.00 received", "Fee 900 GBP")

	cases := []struct {
		name  string
		value string
		want  float64
		text  string
	}{
		{"amount and code both present", "1234.56 EUR", Normalised, "EUR 1.234,56"},
		{"code after the amount", "900 GBP", Normalised, "900 GBP"},
		{"a symbol stands for its code", "250.00 USD", Normalised, "$250.00"},
		{"the same amount in another currency is not a match", "1234.56 GBP", NotFound, ""},
		{"a currency that is absent is not a match", "250.00 JPY", NotFound, ""},
		{"an amount with no currency falls back to a number", "900", Verbatim, "900"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Ground(doc, c.value, KindCurrency)
			if r.Grounding != c.want {
				t.Errorf("grounding = %v, want %v (matched %q)", r.Grounding, c.want, matched(doc, r))
			}
			if got := matched(doc, r); got != c.text {
				t.Errorf("matched %q, want %q", got, c.text)
			}
		})
	}
}

func TestGroundDates(t *testing.T) {
	t.Parallel()
	march3 := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		lines []string
		value any
		order DateOrder
		want  float64
		text  string
	}{
		{"iso form is verbatim", []string{"Issued 2026-03-03"}, march3, DateOrderUnknown, Verbatim, "2026-03-03"},
		{"slashed iso form", []string{"Issued 2026/03/03"}, march3, DateOrderUnknown, Normalised, "2026/03/03"},
		{"day first numeric", []string{"Issued 03/03/2026"}, march3, DayFirst, Normalised, "03/03/2026"},
		{"two digit year", []string{"Issued 3-3-26"}, march3, DateOrderUnknown, Normalised, "3-3-26"},
		{"prose with the month named", []string{"Issued 3 March 2026"}, march3, DateOrderUnknown, Normalised, "3 March 2026"},
		{"prose with an ordinal suffix", []string{"Issued 3rd March 2026"}, march3, DateOrderUnknown, Normalised, "3rd March 2026"},
		{"prose in american order", []string{"Issued March 3, 2026"}, march3, DateOrderUnknown, Normalised, "March 3, 2026"},
		{"prose written out in full", []string{"Issued the third of March 2026"}, march3, DateOrderUnknown, Normalised, "third of March 2026"},
		{"an ordinal above twenty", []string{"Issued the twenty-first of March 2026"}, time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), DateOrderUnknown, Normalised, "twenty-first of March 2026"},
		{"abbreviated month", []string{"Issued 3 Mar 2026"}, march3, DateOrderUnknown, Normalised, "3 Mar 2026"},
		{"an ambiguous date matches either reading when none is set", []string{"Issued 04/03/2026"}, march3, DateOrderUnknown, Normalised, "04/03/2026"},
		{"an ambiguous date does not match the other reading when one is set", []string{"Issued 04/03/2026"}, march3, DayFirst, NotFound, ""},
		{"a month-first reading is honoured", []string{"Issued 03/04/2026"}, march3, MonthFirst, Normalised, "03/04/2026"},
		{"a different day is not a match", []string{"Issued 2026-03-04"}, march3, DateOrderUnknown, NotFound, ""},
		{"an impossible day is not read as a date", []string{"Ref 31/02/2026 only"}, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), DateOrderUnknown, NotFound, ""},
		{"a date the document does not contain is not found", []string{"No dates here"}, march3, DateOrderUnknown, NotFound, ""},
		{"a date given as a string still grounds", []string{"Issued 3 March 2026"}, "2026-03-03", DateOrderUnknown, Normalised, "3 March 2026"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			doc := page(c.lines...)
			r := Ground(doc, c.value, KindDate, WithDateOrder(c.order))
			if r.Grounding != c.want {
				t.Errorf("grounding = %v, want %v (matched %q)", r.Grounding, c.want, matched(doc, r))
			}
			if got := matched(doc, r); got != c.text {
				t.Errorf("matched %q, want %q", got, c.text)
			}
		})
	}
}

func TestGroundStrings(t *testing.T) {
	t.Parallel()
	doc := page("Vendor ACME  Ltd of Ofﬁce Park", "Contact Ms Bäcker", "Beneficiary Smithson")

	cases := []struct {
		name  string
		value string
		want  float64
		text  string
	}{
		{"the same bytes are verbatim", "ACME", Verbatim, "ACME"},
		{"a different case is a normalised match", "acme ltd", Normalised, "ACME Ltd"},
		{"a collapsed whitespace run still matches", "ACME Ltd", Normalised, "ACME Ltd"},
		{"a ligature in the source matches its expansion", "Office Park", Normalised, "Ofﬁce Park"},
		{"a decomposed accent matches its composed form", "Bäcker", Normalised, "Bäcker"},
		{"an expanded name is a different string", "Acme Limited", NotFound, ""},
		{"a value inside a longer word does not match", "Smith", NotFound, ""},
		{"a value that is absent is not found", "Northwind", NotFound, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Ground(doc, c.value, KindString)
			if r.Grounding != c.want {
				t.Errorf("grounding = %v, want %v (matched %q)", r.Grounding, c.want, matched(doc, r))
			}
			if got := matched(doc, r); got != c.text {
				t.Errorf("matched %q, want %q", got, c.text)
			}
		})
	}
}

// TestGroundFindsThroughHiddenCharacters pins that a payload hidden inside a
// word does not also hide the word from the search. The text keeps the
// characters and normalisation still reports them.
func TestGroundFindsThroughHiddenCharacters(t *testing.T) {
	t.Parallel()
	doc := page("Vendor AC​ME Ltd")
	r := Ground(doc, "ACME", KindString)
	if r.Grounding != Normalised {
		t.Fatalf("grounding = %v, want %v", r.Grounding, Normalised)
	}
	if got := matched(doc, r); got != "AC​ME" {
		t.Errorf("matched %q, want the zero-width character to be inside the span", got)
	}
	if !strings.Contains(doc.Text, "​") {
		t.Error("the zero-width character was stripped from the text")
	}
}

func TestGroundBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		lines []string
		value bool
		want  float64
	}{
		{"a literal true is verbatim", []string{"approved: true"}, true, Verbatim},
		{"yes stands for true", []string{"Approved: Yes"}, true, Normalised},
		{"no stands for false", []string{"Approved: No"}, false, Normalised},
		{"a document that says neither grounds nothing", []string{"Approved"}, true, NotFound},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Ground(page(c.lines...), c.value, KindBool)
			if r.Grounding != c.want {
				t.Errorf("grounding = %v, want %v", r.Grounding, c.want)
			}
		})
	}
}

func TestGroundSlice(t *testing.T) {
	t.Parallel()
	doc := page("Items 100 200 and three hundred")

	all := Ground(doc, []int{100, 200}, KindSlice)
	if all.Grounding != Verbatim || !all.Exact {
		t.Errorf("a fully present slice = %v exact=%v, want %v true", all.Grounding, all.Exact, Verbatim)
	}
	if len(all.Elements) != 2 {
		t.Fatalf("got %d element results, want 2", len(all.Elements))
	}

	partial := Ground(doc, []int{100, 300}, KindSlice)
	if partial.Grounding != (Verbatim+NotFound)/2 {
		t.Errorf("a half-present slice = %v, want %v", partial.Grounding, (Verbatim+NotFound)/2)
	}
	if partial.Exact {
		t.Error("a slice with an ungrounded element is not exact")
	}

	none := Ground(doc, []int{7, 8}, KindSlice)
	if none.Grounding != NotFound || none.Reason != ReasonNotFound {
		t.Errorf("an absent slice = %v %q, want %v with a reason", none.Grounding, none.Reason, NotFound)
	}

	empty := Ground(doc, []int{}, KindSlice)
	if empty.Applicable {
		t.Error("an empty slice should produce no grounding signal")
	}
}

func TestGroundNotApplicable(t *testing.T) {
	t.Parallel()
	doc := page("Invoice 42")
	type nested struct{ A int }

	cases := []struct {
		name  string
		value any
		kind  Kind
	}{
		{"a nil value", nil, KindUnknown},
		{"an empty string", "", KindString},
		{"a blank string", "   ", KindString},
		{"a struct", nested{A: 1}, KindUnknown},
		{"a nil pointer", (*int)(nil), KindUnknown},
		{"a map", map[string]int{"a": 1}, KindUnknown},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := Ground(doc, c.value, c.kind)
			if r.Applicable {
				t.Errorf("Applicable = true, want false: a value with no groundable text has an absent signal, not a zero one")
			}
			if r.Grounding != 0 || r.Reason != "" || r.Span != nil {
				t.Errorf("got %+v, want the zero Result", r)
			}
		})
	}

	if r := Ground(nil, "x", KindString); r.Applicable {
		t.Error("grounding against no document should produce no signal")
	}
	if r := Ground(&normalise.Result{}, "x", KindString); r.Applicable {
		t.Error("grounding against empty text should produce no signal")
	}
}

// TestGroundNotFoundIsReported is the row that matters: a value appearing
// nowhere in the document may have been invented, and it is reported as such.
func TestGroundNotFoundIsReported(t *testing.T) {
	t.Parallel()
	doc := page("Invoice 42 for Acme Ltd")
	r := Ground(doc, 999999.0, KindNumber)

	if r.Grounding != NotFound {
		t.Errorf("grounding = %v, want %v", r.Grounding, NotFound)
	}
	if r.Exact {
		t.Error("Exact should be false")
	}
	if r.Span != nil || r.Box != nil || r.Page != 0 {
		t.Errorf("got span %v box %v page %d, want all unset", r.Span, r.Box, r.Page)
	}
	if !r.Applicable {
		t.Error("a value that was searched for and not found has a signal of zero, not an absent one")
	}
	if r.Reason != ReasonNotFound {
		t.Errorf("Reason = %q, want %q", r.Reason, ReasonNotFound)
	}
	if strings.Contains(r.Reason, "999999") {
		t.Error("the reason leaked the value")
	}
}

func TestGroundDerivable(t *testing.T) {
	t.Parallel()
	doc := page("Line one 100", "Line two 200")

	absent := Ground(doc, 300.0, KindNumber)
	if absent.Grounding != NotFound || absent.Reason == "" {
		t.Errorf("without the option a computed total = %v %q, want %v with a reason", absent.Grounding, absent.Reason, NotFound)
	}

	derived := Ground(doc, 300.0, KindNumber, WithDerivable())
	if derived.Grounding != Derived {
		t.Errorf("with the option = %v, want %v", derived.Grounding, Derived)
	}
	if derived.Reason != "" {
		t.Errorf("a derived value is not a suspicious one, so it carries no reason, got %q", derived.Reason)
	}
	if derived.Span != nil || derived.Exact {
		t.Error("a derived value has no span and is not exact")
	}
	if !derived.Applicable {
		t.Error("a derived value has a signal")
	}

	present := Ground(doc, 100.0, KindNumber, WithDerivable())
	if present.Grounding != Verbatim {
		t.Errorf("the option must not demote a value that is present: got %v", present.Grounding)
	}
}

// TestGroundNeverMatchesAPageMarker pins the exclusion. Without it, grounding
// the number two against any two-page document succeeds on the marker.
func TestGroundNeverMatchesAPageMarker(t *testing.T) {
	t.Parallel()
	doc := normalise.Normalise(normalise.Input{Pages: []normalise.Page{
		{Number: 1, Words: []normalise.Word{{Text: "alpha", Line: 0}}},
		{Number: 2, Words: []normalise.Word{{Text: "beta", Line: 0}}},
		{Number: 3, Words: []normalise.Word{{Text: "gamma 3", Line: 0}}},
	}})

	if r := Ground(doc, 2, KindNumber); r.Grounding != NotFound {
		t.Errorf("the number 2 grounded at %q, but only the page marker says two", matched(doc, r))
	}
	if r := Ground(doc, "page", KindString); r.Grounding != NotFound {
		t.Errorf("the word page grounded at %q, but only the marker says it", matched(doc, r))
	}
	r := Ground(doc, 3, KindNumber)
	if r.Grounding == NotFound {
		t.Fatal("the number 3 is in the body of page 3 and should ground")
	}
	if r.Page != 3 {
		t.Errorf("Page = %d, want 3", r.Page)
	}
}

// TestGroundReportsWhereItLooked asserts that a hit carries the position the
// normaliser kept for it, which is what a Provenance is filled from.
func TestGroundReportsWhereItLooked(t *testing.T) {
	t.Parallel()
	doc := page("Vendor Acme Ltd", "Total 1,250.00")
	r := Ground(doc, 1250.0, KindNumber)

	if r.Span == nil {
		t.Fatal("expected a span")
	}
	if got := matched(doc, r); got != "1,250.00" {
		t.Fatalf("matched %q", got)
	}
	if r.Page != 1 {
		t.Errorf("Page = %d, want 1", r.Page)
	}
	if r.Box == nil {
		t.Fatal("expected a box")
	}
	want := normalise.Rect{MinX: 40, MinY: 20, MaxX: 500, MaxY: 32}
	if *r.Box != want {
		t.Errorf("Box = %v, want %v", *r.Box, want)
	}
	if len(r.Regions) != 1 || r.Regions[0].Page != 1 {
		t.Errorf("Regions = %v, want one region on page 1", r.Regions)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	n := 4
	type nested struct{ A int }

	cases := []struct {
		name  string
		value any
		want  Kind
	}{
		{"string", "a", KindString},
		{"int", 1, KindNumber},
		{"int64", int64(1), KindNumber},
		{"uint8", uint8(1), KindNumber},
		{"float64", 1.5, KindNumber},
		{"bool", true, KindBool},
		{"time", time.Now(), KindDate},
		{"slice", []int{1}, KindSlice},
		{"array", [2]int{1, 2}, KindSlice},
		{"pointer to int", &n, KindNumber},
		{"nil", nil, KindUnknown},
		{"nil pointer", (*int)(nil), KindUnknown},
		{"struct", nested{}, KindUnknown},
		{"map", map[string]int{}, KindUnknown},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := KindOf(c.value); got != c.want {
				t.Errorf("KindOf(%T) = %s, want %s", c.value, got, c.want)
			}
		})
	}
	if got := Ground(page("Total 42"), 42, KindUnknown); got.Grounding != Verbatim {
		t.Errorf("an inferred kind should ground: got %v", got.Grounding)
	}
}

// TestFoldMapsBackToTheSource asserts the index the folded search relies on.
// A hit found in the folded text and reported at the wrong offset is worse
// than no hit at all: it highlights the wrong part of the page.
func TestFoldMapsBackToTheSource(t *testing.T) {
	t.Parallel()
	cases := []string{
		"", "plain", "ACME  Ltd", "Ofﬁce", "Bäcker", "STRASSE", "ß",
		"a​b", "２５，０００", "a\xffb", "中文", "É",
	}
	for _, src := range cases {
		src := src
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			folded, index := fold(src)
			if len(index) != len(folded)+1 {
				t.Fatalf("index has %d entries for %d bytes", len(index), len(folded))
			}
			if index[len(index)-1] != len(src) {
				t.Errorf("terminator = %d, want %d", index[len(index)-1], len(src))
			}
			for i := 1; i < len(index); i++ {
				if index[i] < index[i-1] {
					t.Fatalf("index is not monotonic at %d", i)
				}
				if index[i] > len(src) {
					t.Fatalf("index[%d] = %d, past the source", i, index[i])
				}
			}
		})
	}
}

func TestBounded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		text    string
		start   int
		length  int
		numeric bool
		want    bool
	}{
		{"a whole word", "Acme Ltd", 0, 4, false, true},
		{"a prefix of a word", "Smithson", 0, 5, false, false},
		{"a suffix of a word", "Smithson", 5, 3, false, false},
		{"the whole text", "Acme", 0, 4, false, true},
		{"a word beside punctuation", "(Acme)", 1, 4, false, true},
		{"a group of a formatted number", "25,000", 0, 2, true, false},
		{"the last group of a formatted number", "25,000", 3, 3, true, false},
		{"a whole formatted number", "at 25,000 now", 3, 6, true, true},
		{"a number beside a space and a letter", "25 USD", 0, 2, true, true},
		{"a number in a spaced group", "25 000", 0, 2, true, false},
		{"a number beside a single digit", "3 4", 0, 1, true, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sp := normalise.Span{Start: c.start, End: c.start + c.length}
			if got := bounded(c.text, sp, c.numeric); got != c.want {
				t.Errorf("bounded(%q, %q) = %v, want %v", c.text, c.text[sp.Start:sp.End], got, c.want)
			}
		})
	}
}
