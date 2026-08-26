//go:build eval

// This file is the half of the harness that costs money.
//
// It is behind the eval build tag because it needs credentials and bills a
// provider per document, so it must not run in CI and must not run in an
// ordinary `go test ./...` (ADR-0022). Everything it depends on — the corpus
// loader, the comparison, the metric arithmetic, the report renderer — is in
// the untagged files and is tested offline, so the only thing this file adds
// is the provider call and the plumbing around it.
//
//	export OPENAI_API_KEY=…
//	go test -tags=eval ./eval/... -run TestCorpus
//	go test -tags=eval ./eval/... -run TestCorpus -category invoices -difficulty poor-scan
//	go test -tags=eval ./eval/... -run TestCorpus -baseline report/no-run.json
package eval

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

var (
	categoryFlag = flag.String("category", "",
		"score one corpus category only: invoices, receipts, forms, statements, identity")
	difficultyFlag = flag.String("difficulty", "",
		"score one difficulty only: clean-digital, good-scan, poor-scan, photograph, multi-column")
	baselineFlag = flag.String("baseline", "",
		"a committed report.json to report the delta against; this is the form worth running during development")
	corpusFlag = flag.String("corpus", corpusDir, "the corpus directory")
	reportFlag = flag.String("report", "report", "the directory to write the report into")

	docTimeout = flag.Duration("doc-timeout", 3*time.Minute,
		"how long one document may take before it is recorded as a failure")
	epsilon = flag.Float64("epsilon", 0.01,
		"hide baseline movements smaller than this; a corpus this size moves by thousandths on one field changing its mind")

	// Prices are flags rather than constants because they change, they differ
	// by account, and a cost computed from a price this repository guessed
	// would be a number nobody can reproduce. Unset means the report says so
	// rather than reporting a cost of nothing.
	priceIn = flag.Float64("price-input-token", 0,
		"USD per input token; leave unset to report usage without money")
	priceOut  = flag.Float64("price-output-token", 0, "USD per output token")
	pricePage = flag.Float64("price-page-unit", 0, "USD per OCR page unit")
)

// TestCorpus runs the whole corpus and writes a report.
//
// It skips rather than fails when there are no credentials. A developer who
// runs the tagged suite without a key wants to be told once; twenty-five
// identical authentication failures are not more informative than one skip.
func TestCorpus(t *testing.T) {
	docs, err := Load(*corpusFlag, *categoryFlag, *difficultyFlag)
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(docs) == 0 {
		t.Skip("no documents matched the filters")
	}

	model, modelName, err := modelFromEnv()
	if err != nil {
		t.Skipf("no provider configured (%v); this suite needs credentials and costs money", err)
	}

	client := ovrin.New(ovrin.WithModel(model))

	cases := make([]Case, 0, len(docs))
	for _, d := range docs {
		d := d
		run, err := RunnerFor(d.Category)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), *docTimeout)
		obs, err := run(ctx, client, ovrin.File(d.Path))
		cancel()
		if err != nil {
			// A failed extraction is a result, not a reason to stop. It stays
			// in every denominator, because an extractor that errors on the
			// hard documents and succeeds on the easy ones has not scored
			// well. The error is logged and never the document's content.
			t.Logf("%s: extraction failed: %v", d.ID(), err)
		}
		cases = append(cases, Case{Document: d, Observation: obs})
	}

	report := Score(cases, Prices{
		USDPerInputToken:  *priceIn,
		USDPerOutputToken: *priceOut,
		USDPerPageUnit:    *pricePage,
	})
	report.Generated = time.Now().UTC()
	report.Commit = commit()
	report.Model = modelName
	report.OCR = "none"
	report.Reading = string(ovrin.ReadingAuto)
	report.Corpus = *corpusFlag
	report.Filter = filterDescription(*categoryFlag, *difficultyFlag)

	t.Log("\n" + report.Text())

	if err := writeReport(report, *reportFlag); err != nil {
		t.Fatalf("writing the report: %v", err)
	}

	if *baselineFlag != "" {
		base, err := readReport(*baselineFlag)
		if err != nil {
			t.Fatalf("reading the baseline: %v", err)
		}
		t.Log("\n" + RenderDeltas(Compare(base, report), *baselineFlag, *epsilon))
	}
}

// writeReport writes the JSON and Markdown forms of a report.
//
// Both are committed. A regression then arrives as a diff somebody reviews
// rather than as a customer's complaint, and the JSON is what a later run
// compares against with -baseline.
func writeReport(r *Report, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stem := fmt.Sprintf("%s-%s-%s",
		r.Generated.Format("2006-01-02"), slug(r.Model), slug(r.OCR))

	b, err := r.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, stem+".json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stem+".md"), []byte(r.Markdown()), 0o644)
}

// readReport loads a committed report to compare against.
func readReport(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// commit returns the ovrin commit the run was made from, or "unknown".
//
// Without it a figure cannot be reproduced, and rule §3.8 forbids quoting a
// figure nobody can reproduce. A dirty tree is marked, because a number
// produced from uncommitted code is not attributable to any commit at all.
func commit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	rev := strings.TrimSpace(string(out))
	if dirty, err := exec.Command("git", "status", "--porcelain").Output(); err == nil &&
		len(strings.TrimSpace(string(dirty))) > 0 {
		return rev + "-dirty"
	}
	return rev
}

// filterDescription records any restriction on the run, because a filtered
// report is not comparable with an unfiltered one and the file has to say so.
func filterDescription(category, difficulty string) string {
	var parts []string
	if category != "" {
		parts = append(parts, "category="+category)
	}
	if difficulty != "" {
		parts = append(parts, "difficulty="+difficulty)
	}
	return strings.Join(parts, " ")
}

// slug makes a string safe for a filename.
func slug(s string) string {
	if s == "" {
		return "none"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
