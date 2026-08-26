package adaptertest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

const fakeOCRAPIKey = "ocr-adaptertest-secret-key"

// fakeOCRSuccess is one recognised page in the fake vendor's format.
//
// Three things about it are deliberate, and each corresponds to a
// normalisation the contract exists to enforce:
//
//   - the boxes are pixels at 100 DPI, so a box handed back unconverted lands
//     off a 612 × 792 point page;
//   - the confidences are on 0..100, so one handed back undivided is out of
//     range;
//   - the words are in block order rather than reading order, so an adapter
//     that does not sort returns them scrambled.
const fakeOCRSuccess = `{"pages":[{` +
	`"width":850,"height":1100,"language":"en","confidence":94,` +
	`"words":[` +
	`{"text":"Corporation","left":190,"top":200,"right":400,"bottom":225,"confidence":93},` +
	`{"text":"1,234.56","left":600,"top":900,"right":800,"bottom":925,"confidence":62},` +
	`{"text":"INVOICE","left":100,"top":100,"right":250,"bottom":125,"confidence":99},` +
	`{"text":"Total","left":100,"top":900,"right":180,"bottom":925,"confidence":95},` +
	`{"text":"Acme","left":100,"top":200,"right":175,"bottom":225,"confidence":87}` +
	`]}]}`

// fakeOCRNoWordConfidence is the same page with no per-word confidence, which
// is what a provider that scores only whole pages returns.
const fakeOCRNoWordConfidence = `{"pages":[{` +
	`"width":850,"height":1100,"language":"en","confidence":78,` +
	`"words":[` +
	`{"text":"Corporation","left":190,"top":200,"right":400,"bottom":225},` +
	`{"text":"1,234.56","left":600,"top":900,"right":800,"bottom":925},` +
	`{"text":"INVOICE","left":100,"top":100,"right":250,"bottom":125},` +
	`{"text":"Total","left":100,"top":900,"right":180,"bottom":925},` +
	`{"text":"Acme","left":100,"top":200,"right":175,"bottom":225}` +
	`]}]}`

// fakeOCRDocument is a two-page reply, as a provider that rasterises
// server-side returns. Its coordinates are already points, because the provider
// read the PDF's own page boxes rather than an image.
const fakeOCRDocument = `{"pages":[` +
	`{"width":612,"height":792,"language":"en","confidence":91,"words":[` +
	`{"text":"Bottom","left":100,"top":700,"right":200,"bottom":720,"confidence":70},` +
	`{"text":"ONE","left":210,"top":100,"right":280,"bottom":120,"confidence":80},` +
	`{"text":"PAGE","left":100,"top":100,"right":200,"bottom":120,"confidence":90}` +
	`]},` +
	`{"width":612,"height":792,"language":"en","confidence":85,"words":[` +
	`{"text":"Footer","left":100,"top":710,"right":190,"bottom":730,"confidence":66},` +
	`{"text":"PAGE2","left":230,"top":100,"right":320,"bottom":120,"confidence":77},` +
	`{"text":"SECOND","left":100,"top":100,"right":220,"bottom":120,"confidence":88}` +
	`]}]}`

// fakeOCRWant is fakeOCRSuccess normalised: points, 0..1, reading order.
//
// The pixel values are scaled by 612/850 — which is exactly 0.72, the ratio a
// 100 DPI raster of a US Letter page has.
var fakeOCRWant = OCRWant{
	Words: []ovrin.Word{
		{Text: "INVOICE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 180, MaxY: 90}, Confidence: 0.99, Line: 0},
		{Text: "Acme", Box: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 126, MaxY: 162}, Confidence: 0.87, Line: 1},
		{Text: "Corporation", Box: ovrin.Rect{MinX: 136.8, MinY: 144, MaxX: 288, MaxY: 162}, Confidence: 0.93, Line: 1},
		{Text: "Total", Box: ovrin.Rect{MinX: 72, MinY: 648, MaxX: 129.6, MaxY: 666}, Confidence: 0.95, Line: 2},
		{Text: "1,234.56", Box: ovrin.Rect{MinX: 432, MinY: 648, MaxX: 576, MaxY: 666}, Confidence: 0.62, Line: 2},
	},
	Lines: []ovrin.Line{
		{Text: "INVOICE", Box: ovrin.Rect{MinX: 72, MinY: 72, MaxX: 180, MaxY: 90}, Page: OCRPageNumber},
		{Text: "Acme Corporation", Box: ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 162}, Page: OCRPageNumber},
		{Text: "Total 1,234.56", Box: ovrin.Rect{MinX: 72, MinY: 648, MaxX: 576, MaxY: 666}, Page: OCRPageNumber},
	},
	Confidence: 0.94,
	Language:   "en",
}

// fakeOCRWantDocument is fakeOCRDocument normalised, one entry per page.
var fakeOCRWantDocument = []OCRWant{
	{
		Words: []ovrin.Word{
			{Text: "PAGE", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 200, MaxY: 120}, Confidence: 0.90, Line: 0},
			{Text: "ONE", Box: ovrin.Rect{MinX: 210, MinY: 100, MaxX: 280, MaxY: 120}, Confidence: 0.80, Line: 0},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 100, MinY: 700, MaxX: 200, MaxY: 720}, Confidence: 0.70, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "PAGE ONE", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 280, MaxY: 120}, Page: 1},
			{Text: "Bottom", Box: ovrin.Rect{MinX: 100, MinY: 700, MaxX: 200, MaxY: 720}, Page: 1},
		},
		Confidence: 0.91,
		Language:   "en",
	},
	{
		Words: []ovrin.Word{
			{Text: "SECOND", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 220, MaxY: 120}, Confidence: 0.88, Line: 0},
			{Text: "PAGE2", Box: ovrin.Rect{MinX: 230, MinY: 100, MaxX: 320, MaxY: 120}, Confidence: 0.77, Line: 0},
			{Text: "Footer", Box: ovrin.Rect{MinX: 100, MinY: 710, MaxX: 190, MaxY: 730}, Confidence: 0.66, Line: 1},
		},
		Lines: []ovrin.Line{
			{Text: "SECOND PAGE2", Box: ovrin.Rect{MinX: 100, MinY: 100, MaxX: 320, MaxY: 120}, Page: 2},
			{Text: "Footer", Box: ovrin.Rect{MinX: 100, MinY: 710, MaxX: 190, MaxY: 730}, Page: 2},
		},
		Confidence: 0.85,
		Language:   "en",
	},
}

// The contract suite has to pass an adapter that obeys the contract, or its
// failures mean nothing. This is that adapter, and this is the whole suite.
func TestOCRSuiteAcceptsACompliantAdapter(t *testing.T) {
	OCR(t, OCRSuite{
		Name: "fakeocr",
		New: func(baseURL string) ovrin.OCR {
			return newFakeOCR(baseURL, fakeOCRAPIKey, nil)
		},
		NewDocument: func(baseURL string, content []byte) ovrin.DocumentOCR {
			return newFakeOCR(baseURL, fakeOCRAPIKey, content)
		},
		APIKey:       fakeOCRAPIKey,
		ProviderName: fakeOCRName,

		SuccessBody: fakeOCRSuccess,
		Want:        fakeOCRWant,

		PageConfidenceBody: fakeOCRNoWordConfidence,
		WantPageConfidence: 0.78,
		UsedPageConfidence: func(raw any) bool {
			r, ok := raw.(*fakeRaw)
			return ok && r.PageConfidenceUsed
		},

		DocumentBody:    fakeOCRDocument,
		WantDocument:    fakeOCRWantDocument,
		UnsupportedKind: ovrin.KindDOCX,

		ErrorBody: `{"error":{"code":9,"message":"something went wrong"}}`,
		EchoErrorBody: func(echo string) string {
			quoted, err := json.Marshal("Invalid input. Offending content: " + echo)
			if err != nil {
				return `{"error":{"message":"encode failed"}}`
			}
			return `{"error":{"code":3,"message":` + string(quoted) + `}}`
		},
	})
}

// The reading-order guard is the one that decides whether the fixture can tell
// a sorting adapter from a passthrough, so it has to be able to fail.
func TestAPIOrder(t *testing.T) {
	t.Parallel()

	words := []ovrin.Word{{Text: "alpha"}, {Text: "beta"}, {Text: "gamma"}}

	tests := []struct {
		name      string
		body      string
		want      []int
		wantFound bool
	}{
		{
			name:      "a body in reading order yields the identity",
			body:      `{"w":["alpha","beta","gamma"]}`,
			want:      []int{0, 1, 2},
			wantFound: true,
		},
		{
			name:      "a scrambled body yields the order it used",
			body:      `{"w":["gamma","alpha","beta"]}`,
			want:      []int{2, 0, 1},
			wantFound: true,
		},
		{
			name:      "a word the body never spells out is not found",
			body:      `{"w":["alpha","beta"]}`,
			wantFound: false,
		},
		{
			name:      "a body that splits words into characters is not found",
			body:      `{"symbols":["a","l","p","h","a"]}`,
			wantFound: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, found := apiOrder(tc.body, words)
			if found != tc.wantFound {
				t.Fatalf("apiOrder() found = %v, want %v", found, tc.wantFound)
			}
			if !found {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("apiOrder() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("apiOrder() = %v, want %v", got, tc.want)
				}
			}
			if isIdentity(got) != isIdentity(tc.want) {
				t.Errorf("isIdentity(%v) = %v", got, isIdentity(got))
			}
		})
	}
}

func TestIsIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order []int
		want  bool
	}{
		{"an empty order is the identity", nil, true},
		{"0 1 2 is the identity", []int{0, 1, 2}, true},
		{"a swap is not", []int{1, 0, 2}, false},
		{"a rotation is not", []int{2, 0, 1}, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isIdentity(tc.order); got != tc.want {
				t.Errorf("isIdentity(%v) = %v, want %v", tc.order, got, tc.want)
			}
		})
	}
}

// The privacy assertion is only as good as its ability to pick document content
// out of a request, so the predicate that does the picking is checked here.
func TestLongLeaves(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("A", ocrLeafFloor)
	short := strings.Repeat("B", ocrLeafFloor-1)

	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "an encoded page is content",
			body: `{"image":{"content":"` + long + `"}}`,
			want: []string{long},
		},
		{
			name: "a vendor's vocabulary is not",
			body: `{"features":[{"type":"DOCUMENT_TEXT_DETECTION"}],"mimeType":"application/pdf"}`,
		},
		{
			name: "a short value is not",
			body: `{"image":{"content":"` + short + `"}}`,
		},
		{
			name: "a body that is not json is content when it is long enough",
			body: long,
			want: []string{long},
		},
		{
			name: "a body that is not json and is short is not",
			body: short,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := longLeaves([]byte(tc.body))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("longLeaves() = %v, want %v", got, tc.want)
			}
			if len(tc.want) == 1 && longestLeaf([]byte(tc.body)) != tc.want[0] {
				t.Errorf("longestLeaf() = %q, want %q",
					longestLeaf([]byte(tc.body)), tc.want[0])
			}
		})
	}
}

func TestRectsEqual(t *testing.T) {
	t.Parallel()

	base := ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288, MaxY: 162}

	tests := []struct {
		name string
		got  ovrin.Rect
		want bool
	}{
		{"an identical rect is equal", base, true},
		{
			name: "a rect within the float tolerance is equal",
			got:  ovrin.Rect{MinX: 72, MinY: 144, MaxX: 288 - ocrEpsilon/2, MaxY: 162},
			want: true,
		},
		{
			name: "a rect left in pixels is not",
			got:  ovrin.Rect{MinX: 100, MinY: 200, MaxX: 400, MaxY: 225},
			want: false,
		},
		{
			name: "a rect flipped to a bottom-left origin is not",
			got:  ovrin.Rect{MinX: 72, MinY: 630, MaxX: 288, MaxY: 648},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := rectsEqual(tc.got, base); got != tc.want {
				t.Errorf("rectsEqual(%+v, %+v) = %v, want %v", tc.got, base, got, tc.want)
			}
		})
	}
}

// The fixture guards are the difference between a suite that proves something
// and one that cannot, so each is checked against a fixture that should be
// refused. valid() reports only through *testing.T, so the check runs it
// against a throwaway T and looks at whether it failed.
func TestOCRFixtureGuards(t *testing.T) {
	t.Parallel()

	good := OCRSuite{
		Name:         "guard",
		New:          func(string) ovrin.OCR { return nil },
		ProviderName: fakeOCRName,
		SuccessBody:  fakeOCRSuccess,
		ErrorBody:    `{"error":{}}`,
		Want:         fakeOCRWant,
	}

	tests := []struct {
		name    string
		mutate  func(*OCRSuite)
		wantOK  bool
		because string
	}{
		{
			name:   "the compliant fixture is accepted",
			mutate: func(*OCRSuite) {},
			wantOK: true,
		},
		{
			name: "a fixture already in reading order is refused",
			mutate: func(s *OCRSuite) {
				s.APIOrder = []int{0, 1, 2, 3, 4}
			},
			because: "it cannot tell a sorting adapter from a passthrough",
		},
		{
			name: "a fixture whose boxes are all near the top is refused",
			mutate: func(s *OCRSuite) {
				words := append([]ovrin.Word(nil), s.Want.Words...)
				for i := range words {
					words[i].Box.MinY = 10
					words[i].Box.MaxY = 30
				}
				s.Want.Words = words
			},
			because: "a box left in pixels would still land on the page",
		},
		{
			name: "a fixture whose confidences are all the page confidence is refused",
			mutate: func(s *OCRSuite) {
				words := append([]ovrin.Word(nil), s.Want.Words...)
				for i := range words {
					words[i].Confidence = s.Want.Confidence
				}
				s.Want.Words = words
			},
			because: "an adapter copying the page value would still pass",
		},
		{
			name: "a fixture whose confidences are not normalised is refused",
			mutate: func(s *OCRSuite) {
				words := append([]ovrin.Word(nil), s.Want.Words...)
				words[0].Confidence = 87
				s.Want.Words = words
			},
			because: "the fixture itself is on 0..100",
		},
		{
			name: "a fixture with a repeated word is refused",
			mutate: func(s *OCRSuite) {
				words := append([]ovrin.Word(nil), s.Want.Words...)
				words[1].Text = words[0].Text
				s.Want.Words = words
			},
			because: "the suite locates words in a fixture by their text",
		},
		{
			name: "a fixture with one line is refused",
			mutate: func(s *OCRSuite) {
				s.Want.Lines = s.Want.Lines[:1]
			},
			because: "a single line cannot show that words were grouped",
		},
		{
			name: "a page confidence of 1 is refused",
			mutate: func(s *OCRSuite) {
				s.Want.Confidence = 1
			},
			because: "1 is what a fabricating adapter produces",
		},
		{
			name: "a fallback fixture whose page confidence is 1 is refused",
			mutate: func(s *OCRSuite) {
				s.PageConfidenceBody = fakeOCRNoWordConfidence
				s.WantPageConfidence = 1
			},
			because: "1 is what a fabricating adapter produces",
		},
		{
			name: "a one-page document fixture is refused",
			mutate: func(s *OCRSuite) {
				s.NewDocument = func(string, []byte) ovrin.DocumentOCR { return nil }
				s.DocumentBody = fakeOCRDocument
				s.WantDocument = fakeOCRWantDocument[:1]
			},
			because: "it cannot show that every page came back",
		},
		{
			name: "a document fixture that expects page 1 twice is refused",
			mutate: func(s *OCRSuite) {
				s.NewDocument = func(string, []byte) ovrin.DocumentOCR { return nil }
				s.DocumentBody = fakeOCRDocument
				pages := []OCRWant{fakeOCRWantDocument[0], fakeOCRWantDocument[0]}
				s.WantDocument = pages
			},
			because: "an adapter stamping every line with page 1 would pass",
		},
		{
			name: "an adapter with no name is refused",
			mutate: func(s *OCRSuite) {
				s.ProviderName = ""
			},
			because: "an error could not say which adapter produced it",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := good
			s.Want.Words = append([]ovrin.Word(nil), good.Want.Words...)
			s.Want.Lines = append([]ovrin.Line(nil), good.Want.Lines...)
			tc.mutate(&s)

			probe := &testing.T{}
			ok := s.valid(probe)
			if ok != tc.wantOK {
				t.Errorf("valid() = %v, want %v; %s", ok, tc.wantOK, tc.because)
			}
		})
	}
}
