package tesseract

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// The geometry every assertion in this file is written against.
//
// It is US Letter at 100 DPI, so 612 × 792 points is exactly 850 × 1100
// pixels and a fixture's pixel values convert to points without rounding. A
// normalisation that is off by a scale factor is then an exact mismatch rather
// than a rounding argument. The numbers match internal/adaptertest
// deliberately, so a fixture written for one reads correctly against the
// other.
const (
	// testPage is the page number the fixtures are recognised as.
	//
	// Deliberately not 1: an adapter that stamps a constant on an
	// [ovrin.Line] passes every assertion when the fixture is the first page,
	// and fails the moment a real document has a second one.
	testPage = 3

	testPointsW = 612.0
	testPointsH = 792.0
	testPixelsW = 850
	testPixelsH = 1100
	testDPI     = 100
)

// testPixelOverflow is the smallest point coordinate whose pixel value at
// testDPI falls off the bottom of the page.
//
// A fixture has to place at least one word below it, so that a box handed back
// in Tesseract's own pixels lands outside the page and the bounds assertion
// catches it. Without such a word every box could be left unconverted and
// still look plausible.
const testPixelOverflow = testPointsH * 72.0 / testDPI

// scale is what a pixel coordinate becomes in points, which at this geometry
// is exact.
const scale = testPointsW / testPixelsW

// hocrPreamble is the XHTML Tesseract really emits, doctype and all.
//
// It is in the fixtures rather than stripped out of them because a strict XML
// decoder rejects most of it, and a parser that only ever saw the fragment
// would fail on the first real page it was given.
const hocrPreamble = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN"
 "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
<head>
<title></title>
<meta http-equiv="Content-Type" content="text/html;charset=utf-8" />
<meta name='ocr-system' content='tesseract 5.3.4' />
<meta name='ocr-capabilities' content='ocr_page ocr_carea ocr_par ocr_line ocrx_word ocrp_wconf'/>
</head>
<body>`

const hocrEpilogue = `
</body>
</html>`

// twoColumns is the main fixture: three content areas presented in an order
// that is not reading order, with a word near the foot of the page.
//
// The right column is emitted before the left, which is the case that
// separates an adapter that sorts from one that hands back whatever Tesseract
// produced. Sorting the *areas* rather than the finished lines is what keeps
// the columns apart; sorting lines by position alone would interleave them.
const twoColumns = hocrPreamble + `
<div class='ocr_page' id='page_1' title='image "page.png"; bbox 0 0 850 1100; ppageno 0'>
 <div class='ocr_carea' id='block_1_1' title="bbox 450 100 800 200">
  <p class='ocr_par' id='par_1_1' lang='eng' title="bbox 450 100 800 200">
   <span class='ocr_line' id='line_1_1' title="bbox 450 100 800 140; baseline 0 -9; x_size 40">
    <span class='ocrx_word' id='word_1_1_1' title='bbox 450 100 560 140; x_wconf 88'>Right</span>
    <span class='ocrx_word' id='word_1_1_2' title='bbox 570 100 800 140; x_wconf 72'>Column</span>
   </span>
  </p>
 </div>
 <div class='ocr_carea' id='block_1_2' title="bbox 50 100 400 200">
  <p class='ocr_par' id='par_1_2' lang='eng' title="bbox 50 100 400 200">
   <span class='ocr_line' id='line_1_2' title="bbox 50 100 400 140; baseline 0 -9; x_size 40">
    <span class='ocrx_word' id='word_1_2_1' title='bbox 50 100 150 140; x_wconf 96'>Left</span>
    <span class='ocrx_word' id='word_1_2_2' title='bbox 160 100 400 140; x_wconf 64'>Side</span>
   </span>
  </p>
 </div>
 <div class='ocr_carea' id='block_1_3' title="bbox 50 1000 400 1050">
  <p class='ocr_par' id='par_1_3' lang='eng' title="bbox 50 1000 400 1050">
   <span class='ocr_line' id='line_1_3' title="bbox 50 1000 400 1050; baseline 0 -9; x_size 40">
    <span class='ocrx_word' id='word_1_3_1' title='bbox 50 1000 200 1050; x_wconf 55'>Footer</span>
    <span class='ocrx_word' id='word_1_3_2' title='bbox 210 1000 400 1050; x_wconf 91'>Total</span>
   </span>
  </p>
 </div>
</div>` + hocrEpilogue

// twoColumnsPageConfidence is the page confidence twoColumns implies, which is
// the mean of its six word confidences.
//
// The mean is not this package's invention: it is what Tesseract's own
// MeanTextConf computes, and hOCR reports no page confidence of its own.
const twoColumnsPageConfidence = (88 + 72 + 96 + 64 + 55 + 91) / 600.0

// wantWord is one expected word, in ovrin's own shape and in points.
type wantWord struct {
	text string
	box  ovrin.Rect
	conf float64
	line int
}

// twoColumnsWant is twoColumns in reading order, with every number stated in
// points rather than derived, so an arithmetic mistake in the test cannot
// cancel out one in the code.
var twoColumnsWant = []wantWord{
	{"Left", ovrin.Rect{MinX: 36, MinY: 72, MaxX: 108, MaxY: 100.8}, 0.96, 0},
	{"Side", ovrin.Rect{MinX: 115.2, MinY: 72, MaxX: 288, MaxY: 100.8}, 0.64, 0},
	{"Right", ovrin.Rect{MinX: 324, MinY: 72, MaxX: 403.2, MaxY: 100.8}, 0.88, 1},
	{"Column", ovrin.Rect{MinX: 410.4, MinY: 72, MaxX: 576, MaxY: 100.8}, 0.72, 1},
	{"Footer", ovrin.Rect{MinX: 36, MinY: 720, MaxX: 144, MaxY: 756}, 0.55, 2},
	{"Total", ovrin.Rect{MinX: 151.2, MinY: 720, MaxX: 288, MaxY: 756}, 0.91, 2},
}

// epsilon is the tolerance for a normalised coordinate or confidence.
//
// Far tighter than any error these assertions hunt: a box left in pixels is
// out by a factor of 1/0.72, not by a rounding.
const epsilon = 1e-9

func floatsEqual(a, b float64) bool { return math.Abs(a-b) <= epsilon }

func rectsEqual(a, b ovrin.Rect) bool {
	return floatsEqual(a.MinX, b.MinX) && floatsEqual(a.MinY, b.MinY) &&
		floatsEqual(a.MaxX, b.MaxX) && floatsEqual(a.MaxY, b.MaxY)
}

// recognise runs a fixture through the whole read-and-normalise path, which is
// everything [Provider.Recognise] does either side of the engine call.
func recognise(t *testing.T, hocr string) *ovrin.Recognition {
	t.Helper()

	page, err := parseHOCR(strings.NewReader(hocr))
	if err != nil {
		t.Fatalf("parseHOCR() error = %v", err)
	}
	sp := newSpace(testPixelsW, testPixelsH, testPointsW, testPointsH)
	return normalise(page, sp, testPage, Recognised{HOCR: hocr, Language: "eng"})
}

// ---------------------------------------------------------------------------
// The four normalisations
// ---------------------------------------------------------------------------

// Reading order is the normalisation most often skipped, because Tesseract's
// own order is usually close enough to look right. Anything with two columns
// is where it stops being close enough, and by then the words are in a prompt.
func TestNormaliseReturnsWordsInReadingOrder(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)

	got := make([]string, len(rec.Words))
	for i, w := range rec.Words {
		got[i] = w.Text
	}
	want := make([]string, len(twoColumnsWant))
	for i, w := range twoColumnsWant {
		want[i] = w.text
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Words = %v, want %v in reading order", got, want)
	}

	// And the fixture is capable of failing: Tesseract emitted the right
	// column first, so an adapter that returned its order unchanged would not
	// produce the slice above.
	apiOrder := []string{"Right", "Column", "Left", "Side", "Footer", "Total"}
	if strings.Join(want, "|") == strings.Join(apiOrder, "|") {
		t.Fatal("the fixture presents its words already in reading order, so it " +
			"cannot tell an adapter that sorts from one that does not")
	}
}

// Coordinates are page points with a top-left origin — neither PDF's origin
// nor Tesseract's pixels. The confidence engine and every review interface are
// written against that one convention (ADR-0009, docs/providers.md).
func TestNormaliseConvertsPixelsToPagePoints(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)

	if len(rec.Words) != len(twoColumnsWant) {
		t.Fatalf("got %d words, want %d", len(rec.Words), len(twoColumnsWant))
	}
	var lowest float64
	for i, w := range rec.Words {
		want := twoColumnsWant[i]
		if w.Box.MinX < 0 || w.Box.MinY < 0 ||
			w.Box.MaxX > testPointsW || w.Box.MaxY > testPointsH {
			t.Errorf("word %d (%q) box = %+v, off a %g × %g point page; the box was "+
				"probably handed back in tesseract's own pixels",
				i, w.Text, w.Box, testPointsW, testPointsH)
			continue
		}
		if !rectsEqual(w.Box, want.box) {
			t.Errorf("word %d (%q) box = %+v, want %+v", i, w.Text, w.Box, want.box)
		}
		if w.Box.MinY > lowest {
			lowest = w.Box.MinY
		}
	}

	// The anti-vacuity check. Unless one word sits low enough that its pixel
	// value would fall off the page, an unconverted box would still land
	// somewhere plausible and the assertion above would pass on a broken
	// adapter.
	if lowest <= testPixelOverflow {
		t.Errorf("every word sits above y=%g points, so a box left in pixels at %d "+
			"DPI would still land on the page and this test could not catch it",
			testPixelOverflow, testDPI)
	}
}

// Confidence is on 0..1. Tesseract reports 0..100; a scale error here does not
// fail, it silently makes every field look certain (docs/confidence.md).
func TestNormaliseDividesConfidenceByOneHundred(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)

	if len(rec.Words) != len(twoColumnsWant) {
		t.Fatalf("got %d words, want %d", len(rec.Words), len(twoColumnsWant))
	}
	for i, w := range rec.Words {
		if w.Confidence < 0 || w.Confidence > 1 {
			t.Errorf("word %d (%q) confidence = %g, which is not on 0..1; tesseract "+
				"reports 0..100 and it must be divided", i, w.Text, w.Confidence)
			continue
		}
		if !floatsEqual(w.Confidence, twoColumnsWant[i].conf) {
			t.Errorf("word %d (%q) confidence = %g, want %g",
				i, w.Text, w.Confidence, twoColumnsWant[i].conf)
		}
	}

	if !floatsEqual(rec.Confidence, twoColumnsPageConfidence) {
		t.Errorf("Recognition.Confidence = %g, want %g (the mean of the word "+
			"confidences, which is what tesseract's own MeanTextConf reports)",
			rec.Confidence, twoColumnsPageConfidence)
	}
	// A page confidence at either end of the range could not tell a normalised
	// value from a fabricated one, so the fixture must not produce one.
	if rec.Confidence <= 0 || rec.Confidence >= 1 {
		t.Errorf("the fixture's page confidence is %g; a value at either end of "+
			"the range makes this test vacuous", rec.Confidence)
	}
}

// A word that does not index a line cannot be grouped, and grounding a value
// means finding the line it sits on.
func TestNormaliseGroupsWordsIntoLines(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)

	wantLines := []ovrin.Line{
		{Text: "Left Side", Box: ovrin.Rect{MinX: 36, MinY: 72, MaxX: 288, MaxY: 100.8}, Page: testPage},
		{Text: "Right Column", Box: ovrin.Rect{MinX: 324, MinY: 72, MaxX: 576, MaxY: 100.8}, Page: testPage},
		{Text: "Footer Total", Box: ovrin.Rect{MinX: 36, MinY: 720, MaxX: 288, MaxY: 756}, Page: testPage},
	}
	if len(rec.Lines) != len(wantLines) {
		t.Fatalf("got %d lines, want %d", len(rec.Lines), len(wantLines))
	}
	for i, got := range rec.Lines {
		want := wantLines[i]
		if got.Text != want.Text {
			t.Errorf("line %d text = %q, want %q", i, got.Text, want.Text)
		}
		if !rectsEqual(got.Box, want.Box) {
			t.Errorf("line %d box = %+v, want %+v", i, got.Box, want.Box)
		}
		if got.Page != want.Page {
			t.Errorf("line %d page = %d, want %d; the page number must come from "+
				"the page that was recognised, not from a constant", i, got.Page, want.Page)
		}
	}

	for i, w := range rec.Words {
		if w.Line < 0 || w.Line >= len(rec.Lines) {
			t.Errorf("word %d (%q) indexes line %d, which is not one of the %d lines",
				i, w.Text, w.Line, len(rec.Lines))
			continue
		}
		if w.Line != twoColumnsWant[i].line {
			t.Errorf("word %d (%q) indexes line %d, want %d",
				i, w.Text, w.Line, twoColumnsWant[i].line)
		}
		if !strings.Contains(rec.Lines[w.Line].Text, w.Text) {
			t.Errorf("word %d (%q) indexes line %d, whose text is %q and does not "+
				"contain it", i, w.Text, w.Line, rec.Lines[w.Line].Text)
		}
	}
}

// ---------------------------------------------------------------------------
// The page-confidence fallback
// ---------------------------------------------------------------------------

// A word Tesseract reported no confidence for takes the page's, and the
// Recognition records that it did. Fabricating 1.0 would tell the confidence
// engine the word was read perfectly, which is the most consequential lie an
// adapter can tell (docs/rules.md §6.1, ADR-0009).
func TestNormaliseUsesPageConfidenceForAWordWithoutOne(t *testing.T) {
	t.Parallel()

	// "Unmeasured" carries a bbox and no x_wconf, which is what hOCR looks
	// like when Tesseract emits a word its confidence pass did not reach.
	const missing = hocrPreamble + `
<div class='ocr_page' id='page_1' title='image ""; bbox 0 0 850 1100'>
 <div class='ocr_carea' id='block_1_1' title="bbox 50 100 800 1050">
  <span class='ocr_line' id='line_1_1' title="bbox 50 100 800 140">
   <span class='ocrx_word' id='w1' title='bbox 50 100 200 140; x_wconf 80'>Measured</span>
   <span class='ocrx_word' id='w2' title='bbox 210 100 500 140'>Unmeasured</span>
  </span>
  <span class='ocr_line' id='line_1_2' title="bbox 50 1000 400 1050">
   <span class='ocrx_word' id='w3' title='bbox 50 1000 400 1050; x_wconf 60'>Footer</span>
  </span>
 </div>
</div>` + hocrEpilogue

	rec := recognise(t, missing)

	// Two words carry a confidence, so the page's is their mean: 0.70. It is
	// neither 0 nor 1, which are exactly the two values a fabricating adapter
	// produces.
	const wantPage = (80 + 60) / 200.0
	if !floatsEqual(rec.Confidence, wantPage) {
		t.Fatalf("Recognition.Confidence = %g, want %g", rec.Confidence, wantPage)
	}

	byText := map[string]float64{}
	for _, w := range rec.Words {
		byText[w.Text] = w.Confidence
	}
	if got := byText["Unmeasured"]; !floatsEqual(got, wantPage) {
		if floatsEqual(got, 1) {
			t.Fatalf("the word tesseract reported no confidence for came back at 1; " +
				"a fabricated certainty is worse than an honest page-level one")
		}
		t.Fatalf("the word with no x_wconf has confidence %g, want the page "+
			"confidence %g", got, wantPage)
	}
	if got := byText["Measured"]; !floatsEqual(got, 0.8) {
		t.Errorf("the word with x_wconf 80 has confidence %g, want 0.8", got)
	}

	raw, ok := rec.Raw.(*Recognised)
	if !ok {
		t.Fatalf("Recognition.Raw is %T, want *Recognised", rec.Raw)
	}
	if !raw.WordConfidenceFromPage {
		t.Error("the fallback happened without being recorded; a caller cannot " +
			"otherwise tell a page-wide confidence from a per-word one that " +
			"happens to be uniform")
	}
}

// The other half of the same rule: when every word carries a confidence, the
// flag must stay false, or it says nothing.
func TestNormaliseDoesNotRecordAFallbackThatDidNotHappen(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)
	raw, ok := rec.Raw.(*Recognised)
	if !ok {
		t.Fatalf("Recognition.Raw is %T, want *Recognised", rec.Raw)
	}
	if raw.WordConfidenceFromPage {
		t.Error("WordConfidenceFromPage is set although every word carried an " +
			"x_wconf; a flag that is always true records nothing")
	}
}

// ---------------------------------------------------------------------------
// Raw, language and the page-level fields
// ---------------------------------------------------------------------------

// Raw is the caller's escape hatch from ovrin's abstraction. Normalisation
// deliberately discards structure Tesseract reports — per-character geometry,
// baselines, block boundaries — and Raw is the only route back to it
// (ADR-0009).
func TestNormalisePopulatesRaw(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)
	raw, ok := rec.Raw.(*Recognised)
	if !ok {
		t.Fatalf("Recognition.Raw is %T, want *Recognised", rec.Raw)
	}
	if raw.HOCR != twoColumns {
		t.Error("Recognised.HOCR is not the hocr that was parsed; it is the only " +
			"route to the structure normalisation discarded")
	}
	if raw.Language != "eng" {
		t.Errorf("Recognised.Language = %q, want %q", raw.Language, "eng")
	}
}

// Tesseract does not detect a language, it is told one. Reporting the model it
// was handed as the language it found would be a fabrication (rule §8.5), so
// the seam's field stays empty and the request lands on Recognised instead.
func TestNormaliseLeavesTheDetectedLanguageEmpty(t *testing.T) {
	t.Parallel()

	rec := recognise(t, twoColumns)
	if rec.Language != "" {
		t.Errorf("Recognition.Language = %q, want empty: tesseract detects no "+
			"language, and reporting the configured one as detected is a "+
			"fabrication", rec.Language)
	}
}

// ---------------------------------------------------------------------------
// The parser
// ---------------------------------------------------------------------------

func TestParseHOCR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hocr      string
		wantWords []string
		wantErr   error
	}{
		{
			name: "markup inside a word is read as one word",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <div class='ocr_carea' title="bbox 0 0 850 1100">
  <span class='ocr_line' title="bbox 10 10 300 40">
   <span class='ocrx_word' title='bbox 10 10 300 40; x_wconf 90'><strong>In</strong>voice</span>
  </span>
 </div>
</div>` + hocrEpilogue,
			wantWords: []string{"Invoice"},
		},
		{
			name: "entities are decoded rather than rejected",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <div class='ocr_carea' title="bbox 0 0 850 1100">
  <span class='ocr_line' title="bbox 10 10 300 40">
   <span class='ocrx_word' title='bbox 10 10 150 40; x_wconf 90'>R&amp;D</span>
   <span class='ocrx_word' title='bbox 160 10 300 40; x_wconf 90'>&lt;total&gt;</span>
  </span>
 </div>
</div>` + hocrEpilogue,
			wantWords: []string{"R&D", "<total>"},
		},
		{
			name: "a heading is a line, not a dropped element",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <div class='ocr_carea' title="bbox 0 0 850 1100">
  <span class='ocr_header' title="bbox 10 10 300 40">
   <span class='ocrx_word' title='bbox 10 10 300 40; x_wconf 90'>Heading</span>
  </span>
  <span class='ocr_line' title="bbox 10 50 300 80">
   <span class='ocrx_word' title='bbox 10 50 300 80; x_wconf 90'>Body</span>
  </span>
 </div>
</div>` + hocrEpilogue,
			wantWords: []string{"Heading", "Body"},
		},
		{
			name: "a word outside any area is kept rather than dropped",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <span class='ocrx_word' title='bbox 10 10 300 40; x_wconf 90'>Orphan</span>
</div>` + hocrEpilogue,
			wantWords: []string{"Orphan"},
		},
		{
			name: "an empty word element is not a word",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <div class='ocr_carea' title="bbox 0 0 850 1100">
  <span class='ocr_line' title="bbox 10 10 300 40">
   <span class='ocrx_word' title='bbox 10 10 20 40; x_wconf 3'> </span>
   <span class='ocrx_word' title='bbox 30 10 300 40; x_wconf 90'>Real</span>
  </span>
 </div>
</div>` + hocrEpilogue,
			wantWords: []string{"Real"},
		},
		{
			name: "a page tesseract found nothing on reports no words",
			hocr: hocrPreamble + `
<div class='ocr_page' title='bbox 0 0 850 1100'>
 <div class='ocr_carea' title="bbox 0 0 850 1100"></div>
</div>` + hocrEpilogue,
			wantErr: errNoWords,
		},
		{
			name:      "html that is not hocr reports no words rather than failing",
			hocr:      `<html><body><p>nothing to see</p></body></html>`,
			wantWords: nil,
			wantErr:   errNoWords,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page, err := parseHOCR(strings.NewReader(tc.hocr))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("parseHOCR() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHOCR() error = %v", err)
			}

			var got []string
			for _, b := range page.blocks {
				for _, l := range b.lines {
					for _, w := range l.words {
						got = append(got, w.text)
					}
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.wantWords, "|") {
				t.Errorf("words = %v, want %v", got, tc.wantWords)
			}
		})
	}
}

func TestTitleProperties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		title    string
		wantBox  ovrin.Rect
		hasBox   bool
		wantConf float64
		hasConf  bool
	}{
		{
			name:     "a word title carries both",
			title:    "bbox 10 20 30 40; x_wconf 96",
			wantBox:  ovrin.Rect{MinX: 10, MinY: 20, MaxX: 30, MaxY: 40},
			hasBox:   true,
			wantConf: 96,
			hasConf:  true,
		},
		{
			name:    "a line title carries a bbox and no confidence",
			title:   "bbox 10 20 30 40; baseline 0 -9; x_size 40; x_descenders 9",
			wantBox: ovrin.Rect{MinX: 10, MinY: 20, MaxX: 30, MaxY: 40},
			hasBox:  true,
		},
		{
			name:     "a page title with an image name before the bbox",
			title:    `image "in.png"; bbox 0 0 850 1100; ppageno 0`,
			wantBox:  ovrin.Rect{MinX: 0, MinY: 0, MaxX: 850, MaxY: 1100},
			hasBox:   true,
			wantConf: 0,
		},
		{
			name:  "no properties at all",
			title: "",
		},
		{
			name:  "a truncated bbox is not half a box",
			title: "bbox 10 20; x_wconf 50",
			// Deliberately no box: three quarters of a rectangle placed on a
			// page is worse than no rectangle, because nothing downstream can
			// tell it is wrong.
			wantConf: 50,
			hasConf:  true,
		},
		{
			name:  "a non-numeric bbox is not a box",
			title: "bbox a b c d",
		},
		{
			name:    "zero is a confidence, not a missing one",
			title:   "bbox 1 2 3 4; x_wconf 0",
			wantBox: ovrin.Rect{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4},
			hasBox:  true,
			hasConf: true,
		},
		{
			name:    "an inverted bbox is squared up rather than flipped",
			title:   "bbox 30 40 10 20",
			wantBox: ovrin.Rect{MinX: 10, MinY: 20, MaxX: 30, MaxY: 40},
			hasBox:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			box, ok := bboxOf(tc.title)
			if ok != tc.hasBox {
				t.Errorf("bboxOf(%q) ok = %v, want %v", tc.title, ok, tc.hasBox)
			}
			if ok && !rectsEqual(box, tc.wantBox) {
				t.Errorf("bboxOf(%q) = %+v, want %+v", tc.title, box, tc.wantBox)
			}

			conf, ok := wconfOf(tc.title)
			if ok != tc.hasConf {
				t.Errorf("wconfOf(%q) ok = %v, want %v", tc.title, ok, tc.hasConf)
			}
			if ok && !floatsEqual(conf, tc.wantConf) {
				t.Errorf("wconfOf(%q) = %g, want %g", tc.title, conf, tc.wantConf)
			}
		})
	}
}

func TestSpaceConvertsAndClamps(t *testing.T) {
	t.Parallel()

	sp := newSpace(testPixelsW, testPixelsH, testPointsW, testPointsH)

	if !floatsEqual(sp.scaleX, scale) || !floatsEqual(sp.scaleY, scale) {
		t.Fatalf("scale = %g × %g, want %g; at %d DPI a pixel is exactly 72/DPI "+
			"points", sp.scaleX, sp.scaleY, scale, testDPI)
	}

	tests := []struct {
		name string
		in   ovrin.Rect
		want ovrin.Rect
	}{
		{
			name: "a box inside the image scales exactly",
			in:   ovrin.Rect{MinX: 100, MinY: 200, MaxX: 300, MaxY: 400},
			want: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 216, MaxY: 288},
		},
		{
			name: "the whole image is the whole page",
			in:   ovrin.Rect{MinX: 0, MinY: 0, MaxX: testPixelsW, MaxY: testPixelsH},
			want: ovrin.Rect{MinX: 0, MinY: 0, MaxX: testPointsW, MaxY: testPointsH},
		},
		{
			name: "a box past the edge is clamped onto the page",
			in:   ovrin.Rect{MinX: -10, MinY: -10, MaxX: testPixelsW + 50, MaxY: testPixelsH + 50},
			want: ovrin.Rect{MinX: 0, MinY: 0, MaxX: testPointsW, MaxY: testPointsH},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sp.rect(tc.in); !rectsEqual(got, tc.want) {
				t.Errorf("rect(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// The banding is what makes the comparison transitive, and a non-transitive
// less function makes sort produce an order that depends on the input's
// original order — which is exactly what reading order removes.
func TestReadingOrderIsBandedNotStrictlyVertical(t *testing.T) {
	t.Parallel()

	sp := newSpace(testPixelsW, testPixelsH, testPointsW, testPointsH)
	// The right column starts two points higher than the left. A strict
	// top-to-bottom sort would put it first; banding puts the left column
	// first, which is how the page reads.
	boxes := []ovrin.Rect{
		{MinX: 320, MinY: 70, MaxX: 570, MaxY: 300},
		{MinX: 40, MinY: 72, MaxX: 290, MaxY: 300},
		{MinX: 40, MinY: 700, MaxX: 570, MaxY: 740},
	}
	got := readingOrder(boxes, sp)
	want := []int{1, 0, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("readingOrder() = %v, want %v", got, want)
		}
	}
}
