package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	intschema "github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// corpusDir is the committed corpus, relative to this package.
const corpusDir = "corpus"

// TestCorpusLoads reads every committed document and checks the properties
// that make the corpus admissible and its figures interpretable.
//
// It runs in the ordinary offline suite, which is the point: the harness costs
// money and will be run rarely, so the things that can be checked for free —
// licensing, difficulty labels, ground truth that names fields the schema
// actually has — are checked on every commit instead.
func TestCorpusLoads(t *testing.T) {
	docs, err := Load(corpusDir, "", "")
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("the corpus is empty")
	}

	byCategory := map[string]int{}
	byDifficulty := map[string]int{}

	for _, d := range docs {
		d := d
		t.Run(d.ID(), func(t *testing.T) {
			byCategory[d.Category]++
			byDifficulty[d.Meta.Difficulty]++

			if _, err := os.Stat(d.Path); err != nil {
				t.Errorf("the document itself is missing: %v", err)
			}
			if d.Meta.Pages < 1 {
				t.Errorf("pages = %d, want at least 1", d.Meta.Pages)
			}
			if len(d.Expected) == 0 {
				t.Error("ground truth is empty; a document nothing is expected from scores nothing")
			}

			// Ground truth must name fields the schema can actually produce.
			// A typo here is a permanent hundred-percent miss on a field that
			// does not exist, and it would be blamed on the extractor.
			legal := legalKeys(t, d.Category)
			for k := range d.Expected {
				if !legal[CollapseIndices(k)] {
					t.Errorf("ground truth key %q is not a field of the %s schema", k, d.Category)
				}
			}
			for _, k := range d.Meta.Exclude {
				if !legal[CollapseIndices(k)] {
					t.Errorf("excluded key %q is not a field of the %s schema", k, d.Category)
				}
			}
		})
	}

	// ADR-0023: the corpus starts at five documents per category.
	for _, cat := range Categories {
		if byCategory[cat] < 5 {
			t.Errorf("category %s has %d documents, want at least 5 (ADR-0023)", cat, byCategory[cat])
		}
	}

	// An aggregate over an unbalanced corpus is meaningless, so a corpus that
	// is all one difficulty is a corpus whose headline figure means nothing.
	if len(byDifficulty) < 3 {
		t.Errorf("the corpus spans %d difficulties, want at least 3: %v", len(byDifficulty), byDifficulty)
	}
	t.Logf("corpus: %d documents, %v", len(docs), byDifficulty)
}

// TestCorpusIsRedistributable checks the constraint ADR-0023 calls
// non-negotiable, on every file, by reading the metadata rather than trusting
// that somebody looked.
func TestCorpusIsRedistributable(t *testing.T) {
	docs, err := Load(corpusDir, "", "")
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	allowed := map[string]bool{
		"CC0-1.0": true, "CC-BY-4.0": true, "Apache-2.0": true,
		"OGL-UK-3.0": true, "public-domain": true,
	}
	for _, d := range docs {
		if !allowed[d.Meta.Licence] {
			t.Errorf("%s: licence %q is not one this repository can redistribute",
				d.ID(), d.Meta.Licence)
		}
		switch d.Meta.Source {
		case "synthetic", "public-form", "donated":
		default:
			t.Errorf("%s: source %q is not one of synthetic, public-form, donated",
				d.ID(), d.Meta.Source)
		}
		if strings.TrimSpace(d.Meta.Redacted) == "" {
			t.Errorf("%s: no redaction note", d.ID())
		}
	}
}

// TestCorpusFilters covers the -category and -difficulty flags' loader.
func TestCorpusFilters(t *testing.T) {
	invoices, err := Load(corpusDir, "invoices", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range invoices {
		if d.Category != "invoices" {
			t.Errorf("category filter returned %s", d.ID())
		}
	}

	scans, err := Load(corpusDir, "", "poor-scan")
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) == 0 {
		t.Fatal("no poor scans in the corpus; the difficulty spread is not what it claims")
	}
	for _, d := range scans {
		if d.Meta.Difficulty != "poor-scan" {
			t.Errorf("difficulty filter returned %s at %s", d.ID(), d.Meta.Difficulty)
		}
	}

	if _, err := Load(corpusDir, "nonexistent", ""); err == nil {
		t.Error("a filter that matches nothing loaded silently")
	}
}

// TestCorpusRejectsAnIncompleteDocument checks that the loader is strict.
// A corpus that silently shrinks reports a better number than it earned.
func TestCorpusRejectsAnIncompleteDocument(t *testing.T) {
	dir := t.TempDir()
	cat := filepath.Join(dir, "invoices")
	if err := os.MkdirAll(cat, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cat, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("001.pdf", "not really a pdf")
	write("001.expected.json", `{"total": 1}`)
	write("001.meta.yaml", "source: synthetic\nlicence: CC0-1.0\nredacted: none\n")

	if _, err := Load(dir, "invoices", ""); err == nil {
		t.Error("a document with no difficulty label loaded")
	}

	write("001.meta.yaml", "source: synthetic\nlicence: CC0-1.0\nredacted: none\ndifficulty: clean-digital\npages: 1\n")
	docs, err := Load(dir, "invoices", "")
	if err != nil {
		t.Fatalf("a complete document did not load: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("loaded %d documents, want 1", len(docs))
	}

	write("001.expected.json", `{"total":`)
	if _, err := Load(dir, "invoices", ""); err == nil {
		t.Error("a document with malformed ground truth loaded")
	}
}

// legalKeys returns every field key a category's schema can produce.
func legalKeys(t *testing.T, category string) map[string]bool {
	t.Helper()
	typ, err := SchemaType(category)
	if err != nil {
		t.Fatal(err)
	}
	s, err := intschema.Reflect(typ)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	var walk func(fs []intschema.Field)
	walk = func(fs []intschema.Field) {
		for _, f := range fs {
			out[f.Key] = true
			walk(f.Fields)
			if f.Elem != nil {
				out[f.Elem.Key] = true
				walk(f.Elem.Fields)
			}
		}
	}
	walk(s.Fields)
	return out
}
