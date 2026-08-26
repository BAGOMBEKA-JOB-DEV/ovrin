package eval

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Categories are the corpus directories, in report order.
//
// Fixed rather than discovered from the filesystem so that a category nobody
// remembered to populate reports n=0 instead of vanishing from the report — a
// missing row is invisible and a zero row is not.
var Categories = []string{"invoices", "receipts", "forms", "statements", "identity"}

// Difficulties are the labels a document may carry, in increasing order of
// difficulty.
//
// The vocabulary is closed because an aggregate over an unbalanced corpus is
// meaningless, so every figure is broken down by these and a free-text label
// would silently create a bucket of one.
var Difficulties = []string{
	"clean-digital",
	"multi-column",
	"good-scan",
	"poor-scan",
	"photograph",
	"handwritten",
}

// Meta is one document's provenance record, read from NNN.meta.yaml.
//
// It is a struct rather than a map because the licensing constraint is
// absolute (rule §7.6, ADR-0023): a document with no Source and no Licence
// must fail to load rather than be scored, and a typed field is what lets the
// loader say which one is missing.
type Meta struct {
	// Source is where the document came from: public-form, synthetic or
	// donated.
	Source string

	// Licence is the SPDX identifier under which it may be redistributed.
	Licence string

	// Redacted says what was replaced and with what shape. It is required
	// even for synthetic documents, where the honest answer is that nothing
	// was redacted because nothing was ever real.
	Redacted string

	// Difficulty is one of [Difficulties]. Mandatory: every reported figure
	// is broken down by it.
	Difficulty string

	// Pages is the page count.
	Pages int

	// Language is the BCP 47 tag of the document's text.
	Language string

	// Notes is free prose about what makes the document interesting, and is
	// where ambiguity is recorded.
	Notes string

	// Exclude lists field keys that two careful readers would disagree about.
	// They are dropped from scoring entirely, because scoring a field whose
	// ground truth is contested measures the labeller, not the extractor.
	Exclude []string
}

// Document is one corpus entry: the file, its ground truth and its metadata.
type Document struct {
	// Category is the corpus directory: "invoices", "receipts" and so on.
	Category string

	// Name is the stem, "001". Category+"/"+Name identifies a document in a
	// report.
	Name string

	// Path is the absolute or working-directory-relative path to the document
	// itself, whatever extension it carries.
	Path string

	// Meta is the provenance record.
	Meta Meta

	// Expected is ground truth flattened to the field keys [ovrin.Result]
	// uses, so that scoring never has to walk two different shapes at once.
	// A field genuinely absent from the document is absent from this map.
	Expected map[string]any
}

// ID returns "category/name", the identifier a report uses for one document.
func (d Document) ID() string { return d.Category + "/" + d.Name }

// Load reads every corpus document under root, optionally filtered.
//
// Filters are empty for "everything". Loading is strict: a document whose
// metadata is incomplete or whose ground truth will not parse is an error
// rather than a skip, because a corpus that silently shrinks reports a better
// number than it earned.
func Load(root, category, difficulty string) ([]Document, error) {
	var out []Document
	for _, cat := range Categories {
		if category != "" && category != cat {
			continue
		}
		docs, err := loadCategory(root, cat)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if difficulty != "" && d.Meta.Difficulty != difficulty {
				continue
			}
			out = append(out, d)
		}
	}
	if category != "" && len(out) == 0 {
		return nil, fmt.Errorf("eval: no documents matched category %q difficulty %q", category, difficulty)
	}
	return out, nil
}

// loadCategory reads one corpus directory. A directory that does not exist is
// not an error: a category may legitimately be empty before anybody has
// contributed to it, and the report says n=0.
func loadCategory(root, cat string) ([]Document, error) {
	dir := filepath.Join(root, cat)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("eval: reading corpus category %s: %w", cat, err)
	}

	// Group by stem so that the document, its ground truth and its metadata
	// are found together whatever extension the document carries — a
	// photograph is a .jpg and a clean digital original is a .pdf.
	stems := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		switch {
		case strings.HasSuffix(n, ".expected.json"), strings.HasSuffix(n, ".meta.yaml"):
			continue
		case strings.HasPrefix(n, "."), n == "README.md":
			continue
		}
		stem := strings.TrimSuffix(n, path.Ext(n))
		if prev, ok := stems[stem]; ok {
			return nil, fmt.Errorf("eval: %s/%s has two documents, %s and %s", cat, stem, prev, n)
		}
		stems[stem] = n
	}

	names := make([]string, 0, len(stems))
	for stem := range stems {
		names = append(names, stem)
	}
	sort.Strings(names)

	out := make([]Document, 0, len(names))
	for _, stem := range names {
		d, err := loadDocument(dir, cat, stem, stems[stem])
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// loadDocument reads one stem's three files.
func loadDocument(dir, cat, stem, file string) (Document, error) {
	d := Document{Category: cat, Name: stem, Path: filepath.Join(dir, file)}

	raw, err := os.ReadFile(filepath.Join(dir, stem+".meta.yaml"))
	if err != nil {
		return d, fmt.Errorf("eval: %s/%s: %w", cat, stem, err)
	}
	m, err := ParseMeta(string(raw))
	if err != nil {
		return d, fmt.Errorf("eval: %s/%s: %w", cat, stem, err)
	}
	if err := validateMeta(m); err != nil {
		return d, fmt.Errorf("eval: %s/%s: %w", cat, stem, err)
	}
	d.Meta = m

	raw, err = os.ReadFile(filepath.Join(dir, stem+".expected.json"))
	if err != nil {
		return d, fmt.Errorf("eval: %s/%s: %w", cat, stem, err)
	}
	exp, err := ParseExpected(raw)
	if err != nil {
		return d, fmt.Errorf("eval: %s/%s: %w", cat, stem, err)
	}
	d.Expected = exp
	return d, nil
}

// validateMeta enforces the four fields that make a document redistributable
// and its score interpretable. The check is here rather than in ParseMeta so
// that ParseMeta stays a parser and can be tested as one.
func validateMeta(m Meta) error {
	switch {
	case m.Source == "":
		return errors.New("meta.yaml has no source; a document with no known provenance cannot be redistributed")
	case m.Licence == "":
		return errors.New("meta.yaml has no licence; rule §7.6 requires every corpus document to be redistributable")
	case m.Redacted == "":
		return errors.New("meta.yaml has no redacted note; say what was replaced, or that nothing was ever real")
	case m.Difficulty == "":
		return errors.New("meta.yaml has no difficulty; an aggregate over an unlabelled corpus is meaningless")
	}
	for _, known := range Difficulties {
		if m.Difficulty == known {
			return nil
		}
	}
	return fmt.Errorf("meta.yaml difficulty %q is not one of %s", m.Difficulty, strings.Join(Difficulties, ", "))
}

// ParseMeta reads the flat subset of YAML that NNN.meta.yaml uses.
//
// Hand-rolled rather than a dependency: the format is seven keys of scalars,
// one optional literal block and one optional list, which is thirty lines of
// standard library, and the core module has zero external dependencies by
// rule §4.1. Taking gopkg.in/yaml.v3 to read seven keys would put a parser
// with its own CVE history into the build of a library whose whole argument is
// that it has no supply chain.
//
// It deliberately does not implement YAML. Anchors, flow mappings, quoting
// rules and multi-document streams are all unsupported, and a file using them
// will be misread rather than rejected. That is acceptable for files this
// repository generates and reviews; it would not be for user input.
func ParseMeta(s string) (Meta, error) {
	var m Meta
	lines := strings.Split(s, "\n")

	// key is the field the current continuation or block belongs to, and
	// blockIndent is the indentation a literal block's body must exceed.
	var key string
	var block []string
	var inBlock bool
	var blockIndent int

	flush := func() error {
		if !inBlock {
			return nil
		}
		inBlock = false
		// Trim trailing blank lines: a literal block that ends with a blank
		// line means the same thing as one that does not.
		for len(block) > 0 && strings.TrimSpace(block[len(block)-1]) == "" {
			block = block[:len(block)-1]
		}
		v := strings.Join(block, "\n")
		block = nil
		return assign(&m, key, v)
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)

		if inBlock {
			if trimmed == "" {
				block = append(block, "")
				continue
			}
			if indent > blockIndent {
				block = append(block, line[blockIndent+1:])
				continue
			}
			if err := flush(); err != nil {
				return m, err
			}
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			if key == "" {
				return m, fmt.Errorf("line %d: list item with no key", i+1)
			}
			m.Exclude = append(m.Exclude, unquote(stripComment(strings.TrimPrefix(trimmed, "- "))))
			continue
		}

		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			// A more-indented line with no colon continues the previous
			// scalar. docs/evaluation.md's own example wraps `redacted:` this
			// way, so the parser has to.
			if key == "" || indent == 0 {
				return m, fmt.Errorf("line %d: expected key: value", i+1)
			}
			if err := appendTo(&m, key, trimmed); err != nil {
				return m, err
			}
			continue
		}

		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == "|" || v == ">" || v == "|-" || v == ">-" {
			inBlock, blockIndent, block = true, indent, nil
			continue
		}
		if v == "" {
			// A key introducing a list, or an empty scalar.
			if err := assign(&m, key, ""); err != nil {
				return m, err
			}
			continue
		}
		if err := assign(&m, key, unquote(stripComment(v))); err != nil {
			return m, err
		}
	}
	if err := flush(); err != nil {
		return m, err
	}
	return m, nil
}

// stripComment removes a trailing comment. It requires whitespace before the
// '#' so that a value containing one — a hash in a document reference — is not
// truncated. docs/evaluation.md's example annotates `difficulty:` this way.
func stripComment(v string) string {
	if i := strings.Index(v, " #"); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}

// unquote removes matched surrounding quotes, which is the only quoting this
// parser understands.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// assign sets one key. An unknown key is an error rather than a silent skip:
// a typo in `difficulty` would otherwise produce a document that loads, scores
// and is filed under the wrong bucket.
func assign(m *Meta, key, v string) error {
	switch key {
	case "source":
		m.Source = v
	case "licence", "license":
		m.Licence = v
	case "redacted":
		m.Redacted = v
	case "difficulty":
		m.Difficulty = v
	case "language":
		m.Language = v
	case "notes":
		m.Notes = v
	case "exclude":
		if v != "" {
			for _, f := range strings.Split(v, ",") {
				if f = strings.TrimSpace(f); f != "" {
					m.Exclude = append(m.Exclude, f)
				}
			}
		}
	case "pages":
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("pages: %q is not a number", v)
		}
		m.Pages = n
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// appendTo continues a wrapped scalar, joining with a single space because the
// line break was typesetting rather than content.
func appendTo(m *Meta, key, v string) error {
	switch key {
	case "source":
		m.Source += " " + v
	case "licence", "license":
		m.Licence += " " + v
	case "redacted":
		m.Redacted += " " + v
	case "difficulty":
		m.Difficulty += " " + v
	case "language":
		m.Language += " " + v
	case "notes":
		m.Notes += " " + v
	default:
		return fmt.Errorf("cannot continue key %q", key)
	}
	return nil
}
