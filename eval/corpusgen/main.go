// Command corpusgen writes the seed evaluation corpus.
//
//	go run ./eval/corpusgen                 # rewrite eval/corpus in place
//	go run ./eval/corpusgen -out /tmp/x     # somewhere else, to diff first
//
// Every document it produces is synthetic. Nothing here is copied from a real
// document, nobody named exists, and every account number, tax identifier and
// telephone number was invented to have the right shape and no other property.
// That is not a nicety: rule §7.6 keeps real personal data out of the
// repository, and ADR-0023 makes redistributability a condition of entry, so a
// generator is the only way to seed a corpus that is unambiguously clear of
// both. `git rm` is not deletion and a repository is forever.
//
// # What this is not
//
// A generated corpus measures the generator as much as the extractor
// (ADR-0023 rejects run-time generation for exactly that reason, and rules
// §3.5 says a synthetic document proves only that we can read our own
// writing). This is a seed: it exists so the harness has something to run
// against on day one, and it should be displaced over time by public forms and
// donated documents, which are the ones that teach the corpus something.
// Its committed output is checked in so that a run is reproducible and so that
// nobody has to trust that the generator was ever run.
//
// # Difficulty is manufactured, not guessed
//
// Each document is drawn once and then put through a named chain of
// degradations — skew, sensor noise, low contrast, uneven lighting, dust,
// downsampling, JPEG ringing. The chain is recorded in the document's
// meta.yaml notes, so the difficulty label is a description of a process
// rather than somebody's impression of the result.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"image/png"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin/eval"
	intschema "github.com/BAGOMBEKA-JOB-DEV/ovrin/internal/schema"
)

// document is one corpus entry, before it is drawn.
type document struct {
	// Category is the corpus directory and Name is the stem.
	Category string
	Name     string

	// Body is the document's text, one line per line, shared by every
	// rendering. "@H " marks a heading, "@B " a bold line and "@R" a rule.
	Body []string

	// Expected is ground truth as JSON, written next to the body it describes
	// so that a labelling error is visible while the document is being
	// written rather than found six months later in a report.
	Expected string

	// Recipe says how the document is acquired.
	Recipe recipe

	// Difficulty is the label, Notes says what makes it hard, and Exclude
	// lists any field two careful readers would disagree about.
	Difficulty string
	Notes      string
	Exclude    []string

	// Seed fixes the pseudo-randomness so that regenerating the corpus
	// produces byte-identical files. A generator whose output churns turns
	// every corpus change into an unreviewable diff.
	Seed int64
}

func main() {
	out := flag.String("out", "eval/corpus", "directory to write the corpus into")
	flag.Parse()

	docs := documents()
	byCat := map[string]int{}
	for _, d := range docs {
		byCat[d.Category]++
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	for _, d := range docs {
		if err := write(*out, d); err != nil {
			log.Fatalf("%s/%s: %v", d.Category, d.Name, err)
		}
	}
	for _, c := range cats {
		log.Printf("%-11s %d documents", c, byCat[c])
	}
	log.Printf("wrote %d documents to %s", len(docs), *out)
}

// write draws one document and writes its three files.
func write(root string, d document) error {
	if err := validate(d); err != nil {
		return err
	}
	dir := filepath.Join(root, d.Category)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, ext, pages, err := draw(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, d.Name+ext), data, 0o644); err != nil {
		return err
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(d.Expected), "", "  "); err != nil {
		return fmt.Errorf("expected.json: %w", err)
	}
	pretty.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(dir, d.Name+".expected.json"), pretty.Bytes(), 0o644); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, d.Name+".meta.yaml"), []byte(metaYAML(d, pages)), 0o644)
}

// validate checks a document against the schema it will be scored with,
// before it is written.
//
// A ground-truth key that no schema field can produce would be a permanent
// hundred-percent miss on a field that does not exist, and it would be blamed
// on the extractor. Catching it here costs nothing; catching it in a report
// costs a provider run.
func validate(d document) error {
	exp, err := eval.ParseExpected([]byte(d.Expected))
	if err != nil {
		return err
	}
	t, err := eval.SchemaType(d.Category)
	if err != nil {
		return err
	}
	s, err := intschema.Reflect(t)
	if err != nil {
		return err
	}
	legal := map[string]bool{}
	var walk func(fs []intschema.Field)
	walk = func(fs []intschema.Field) {
		for _, f := range fs {
			legal[f.Key] = true
			walk(f.Fields)
			if f.Elem != nil {
				legal[f.Elem.Key] = true
				walk(f.Elem.Fields)
			}
		}
	}
	walk(s.Fields)

	keys := make([]string, 0, len(exp))
	for k := range exp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !legal[eval.CollapseIndices(k)] {
			return fmt.Errorf("ground truth key %q is not a field of the %s schema", k, d.Category)
		}
	}
	for _, k := range d.Exclude {
		if !legal[eval.CollapseIndices(k)] {
			return fmt.Errorf("excluded key %q is not a field of the %s schema", k, d.Category)
		}
	}
	return nil
}

// draw renders a document and returns its bytes, file extension and page
// count.
func draw(d document) (data []byte, ext string, pages int, err error) {
	r := d.Recipe
	if r.kind == kindPDF {
		b := writePDF(d.Body)
		return b, ".pdf", len(paginate(d.Body)), nil
	}

	rng := rand.New(rand.NewSource(d.Seed))
	img := render(d.Body, r.scale, r.paper, r.ink)
	img = apply(img, rng, r.steps(r.paper)...)

	var buf bytes.Buffer
	switch r.kind {
	case kindPNG:
		// Greyscale and maximum compression, because a scanner produces
		// greyscale and because ADR-0023 notes that documents in git are
		// binary blobs that inflate the repository permanently. Storing a
		// noisy scan as RGB costs three times the bytes to encode the same
		// information, forever.
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, greyscale(img)); err != nil {
			return nil, "", 0, err
		}
		return buf.Bytes(), ".png", 1, nil
	case kindJPEG:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: r.quality}); err != nil {
			return nil, "", 0, err
		}
		return buf.Bytes(), ".jpg", 1, nil
	}
	return nil, "", 0, fmt.Errorf("unknown recipe kind %q", r.kind)
}

// metaYAML renders one document's provenance record.
//
// Written by hand rather than marshalled so that the file reads like the
// example in docs/evaluation.md — the key order carries meaning, and a
// marshaller would sort it into something nobody wrote.
func metaYAML(d document, pages int) string {
	var b strings.Builder
	b.WriteString("source: synthetic\n")
	b.WriteString("licence: CC0-1.0\n")
	b.WriteString("redacted: nothing was redacted because nothing was ever real; every name,\n")
	b.WriteString("          address, tax identifier, account number and telephone number was\n")
	b.WriteString("          invented to have the right shape and no other property\n")
	fmt.Fprintf(&b, "difficulty: %s\n", d.Difficulty)
	fmt.Fprintf(&b, "pages: %d\n", pages)
	b.WriteString("language: en\n")
	if len(d.Exclude) > 0 {
		b.WriteString("exclude:\n")
		for _, k := range d.Exclude {
			fmt.Fprintf(&b, "  - %s\n", k)
		}
	}
	b.WriteString("notes: |\n")
	for _, line := range strings.Split(strings.TrimRight(d.Notes, "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	return b.String()
}
