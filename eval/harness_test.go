package eval

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// echoModel returns a fixed JSON reply, ignoring the request.
//
// A hand-written fake rather than a mock, per rule §3.4. It stands in for a
// perfect extractor: given a document, it returns exactly what a careful
// person reading that document said the answer was.
type echoModel struct{ json []byte }

func (m echoModel) Generate(context.Context, ovrin.ModelRequest) (*ovrin.ModelResponse, error) {
	return &ovrin.ModelResponse{JSON: m.json}, nil
}

// TestHarnessScoresAPerfectExtractorPerfectly runs the whole harness offline.
//
// The fake model replies with each document's own ground truth, so a correct
// harness must report exact 1.00 and fabrication 0.00. Anything else is a bug
// in this package or an error in a label, and both are worth finding before a
// provider run rather than after paying for one.
//
// It is the only test that exercises Load → RunnerFor → Observe → Score → the
// renderer as one path, and it does it with no network and no credentials
// (rule §3.3).
//
// PDFs are skipped while the text-layer reader is unimplemented: this test
// measures the harness, and a document the pipeline declines to open measures
// the pipeline. The images cover every category anyway.
func TestHarnessScoresAPerfectExtractorPerfectly(t *testing.T) {
	docs, err := Load(corpusDir, "", "")
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}

	var cases []Case
	for _, d := range docs {
		if strings.EqualFold(filepath.Ext(d.Path), ".pdf") {
			continue
		}
		truth, err := os.ReadFile(filepath.Join(filepath.Dir(d.Path), d.Name+".expected.json"))
		if err != nil {
			t.Fatal(err)
		}
		run, err := RunnerFor(d.Category)
		if err != nil {
			t.Fatal(err)
		}
		client := ovrin.New(ovrin.WithModel(echoModel{json: truth}))

		obs, err := run(context.Background(), client, ovrin.File(d.Path))
		if err != nil {
			t.Errorf("%s: extraction failed: %v", d.ID(), err)
			continue
		}
		cases = append(cases, Case{Document: d, Observation: obs})
	}

	if len(cases) == 0 {
		t.Skip("no readable corpus documents; the pipeline handles no format this corpus uses")
	}

	r := Score(cases, Prices{})
	if r.Overall.Tally.Expected == 0 {
		t.Fatal("nothing was scored")
	}

	if r.Overall.Exact != 1 || r.Overall.Fabrication != 0 {
		t.Errorf("a perfect extractor scored exact %.4f fabrication %.4f, want 1.0000 and 0.0000\n%+v",
			r.Overall.Exact, r.Overall.Fabrication, r.Overall.Tally)
		for _, c := range cases {
			for _, j := range judge(c) {
				if j.excluded || (j.wanted && j.correct) || (!j.wanted && !j.produced) {
					continue
				}
				want := c.Document.Expected[j.key]
				got := c.Observation.Fields[j.key]
				t.Errorf("  %s %s: want %#v, got %#v (found=%v)",
					c.Document.ID(), j.key, want, got.Value, got.Found)
			}
		}
	}

	// The report must render without a NaN anywhere, on real data.
	text := r.Text()
	if strings.Contains(text, "NaN") {
		t.Errorf("the report contains NaN:\n%s", text)
	}
	if _, err := r.JSON(); err != nil {
		t.Errorf("the report does not marshal: %v", err)
	}

	cats := make([]string, 0, len(r.Categories))
	for _, c := range r.Categories {
		cats = append(cats, c.Name)
	}
	sort.Strings(cats)
	t.Logf("scored %d documents across %v: %d field instances, %d absences",
		r.Documents, cats, r.Overall.Tally.Expected, r.Overall.Tally.Absent)
}

// TestHarnessCatchesAFabrication runs the same path with a model that invents
// one value, and checks that the harness says so.
//
// The perfect-extractor test above proves the harness does not report false
// failures. This one proves it does not report false successes, which is the
// more dangerous direction: nobody investigates good news.
//
// identity/005 is the document used because it says in words that it does not
// expire, so an expiry date is unambiguously invented rather than arguably
// derived.
func TestHarnessCatchesAFabrication(t *testing.T) {
	docs, err := Load(corpusDir, "identity", "")
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	for _, d := range docs {
		if d.Name == "005" {
			doc = d
		}
	}
	if doc.Name == "" {
		t.Skip("identity/005 is not in the corpus")
	}
	if _, ok := doc.Expected["expires"]; ok {
		t.Fatal("identity/005 now has an expiry in ground truth; this test needs a document without one")
	}

	truth, err := os.ReadFile(filepath.Join(filepath.Dir(doc.Path), doc.Name+".expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	invented := strings.Replace(string(truth), "{\n", "{\n  \"expires\": \"2032-09-09\",\n", 1)

	run, err := RunnerFor(doc.Category)
	if err != nil {
		t.Fatal(err)
	}
	client := ovrin.New(ovrin.WithModel(echoModel{json: []byte(invented)}))
	obs, err := run(context.Background(), client, ovrin.File(doc.Path))
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	r := Score([]Case{{Document: doc, Observation: obs}}, Prices{})
	if r.Overall.Tally.Fabricated != 1 {
		t.Errorf("fabricated = %d, want 1; an invented expiry was not counted\n%+v",
			r.Overall.Tally.Fabricated, r.Overall.Tally)
	}
	if r.Overall.Fabrication == 0 {
		t.Error("fabrication rate is 0.00 on a document with an invented value")
	}
	if r.Overall.Exact != 1 {
		t.Errorf("exact = %.4f; inventing a value must not change the exact-match rate over the "+
			"values the document does contain", r.Overall.Exact)
	}
	if r.Overall.Precision >= 1 {
		t.Errorf("precision = %.4f, want below 1.00; a fabrication belongs in the precision denominator",
			r.Overall.Precision)
	}
}
