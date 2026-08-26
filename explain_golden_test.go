package ovrin

import (
	"os"
	"strings"
	"testing"
)

// docs/explainability.md shows exactly what Explanation.String prints, and a
// reader is entitled to expect the real thing to look like it. The rendering
// is not part of the compatibility promise, but "not promised" is not the same
// as "may quietly differ from its own documentation".
//
// This is a golden test against the document itself: change the rendering and
// the doc goes stale in the same commit, loudly.
func TestExplainMatchesTheDocumentedGolden(t *testing.T) {
	t.Parallel()

	ocr := 0.97
	e := &Explanation{
		Field:      "total",
		Value:      "2500.00",
		Found:      true,
		Confidence: 0.99,
		Signals: []Signal{
			{Name: SignalGrounding, Value: 1.00, Weight: 0.30, Note: "found verbatim, page 1"},
			{Name: SignalOCR, Value: ocr, Weight: 0.20, Note: "12 backing words, mean 0.97"},
			{Name: SignalSchema, Value: 1.00, Weight: 0.15, Note: "float64, min=0 satisfied"},
			{Name: SignalCrossField, Value: 1.00, Weight: 0.05, Note: "line items sum to total"},
			{Name: SignalFormat, Value: 1.00, Weight: 0.05, Note: "parsed as currency"},
			{Name: SignalAgreement, Note: "only one reading"},
		},
		Provenance: []Provenance{{
			Reading: ReadingOCR,
			Page:    1,
			Method:  "ocr:tesseract",
			Exact:   true,
			Box:     &Rect{MinX: 412, MinY: 688, MaxX: 486, MaxY: 702},
		}},
		Validation: []RuleResult{
			{Rule: "required", Passed: true},
			{Rule: "min=0", Passed: true},
		},
	}

	got := strings.TrimRight(e.String(), "\n")
	want := strings.TrimRight(documentedGolden(t), "\n")
	if got != want {
		t.Errorf("Explanation.String() does not match docs/explainability.md.\n\n"+
			"--- got ---\n%s\n\n--- docs/explainability.md ---\n%s", got, want)
	}
}

// documentedGolden extracts the rendering from the document, so the test reads
// the same bytes a reader does rather than a copy that could drift from it.
func documentedGolden(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("docs/explainability.md")
	if err != nil {
		t.Fatalf("reading the document: %v", err)
	}
	body := string(raw)
	const marker = "Field:       total"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("docs/explainability.md no longer contains the Explain rendering")
	}
	rest := body[i:]
	j := strings.Index(rest, "\n```")
	if j < 0 {
		t.Fatal("the Explain rendering in docs/explainability.md is not in a closed fence")
	}
	return rest[:j]
}
