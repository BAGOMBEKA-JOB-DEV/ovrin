// Drift caught here: a number in a documentation table ceasing to be the
// number in the code.
//
// This is a different failure from the other two. The API file catches a symbol
// changing shape and the mirror fences catch a declaration changing shape, but a
// default is a *value*, and a value can drift while every signature stays
// identical. It is also the drift with the worst consequences for a reader:
// somebody sizes their uploads against a documented 64 MiB limit, the constant
// moves to 32 MiB, and nothing anywhere is red. Worse still, several of these
// numbers are stated in two documents, so a change made in one place is a
// contradiction rather than an omission.
//
// The bridge between the two worlds is documentedDefaults below, and it is
// deliberately hand-written. Thirteen lines are cheap, and they are checked
// from three sides, which is what makes the map trustworthy rather than a
// fourth place to have to remember:
//
//   - the compiler proves every constant on the right-hand side exists;
//   - the documentation scan proves every table row has an entry, catching a
//     limit that was documented and never built;
//   - an AST scan for numeric With* options proves every entry has a table
//     row, catching a limit that was built and never documented.
//
// Set equality is asserted in both directions, so neither list can quietly grow
// past the other.
//
// Two further invariants come almost free and are worth having:
//
//   - the six confidence weights sum to exactly 1.00, since a weighted mean
//     whose weights do not is not a weighted mean;
//   - no limit is documented with two different values in two different places.
//
// Helpers shared with the other two drift tests (parseRootPackage, render)
// live in api_test.go.
package ovrin

import (
	"go/ast"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// documentedDefaults maps the option named in a documentation table to the
// constant that actually implements it.
//
// A string on the right is a default that is not a number — the value is then
// compared as text, because "min(4, GOMAXPROCS)" is a real answer to "what is
// the default?" and pretending otherwise would mean either leaving the row
// unchecked or inventing a number nobody wrote.
//
// context.WithTimeout is the one entry that is not an ovrin option. It earns
// its place because the row exists: ADR-0020 documents the deliberate absence
// of a wall-clock default, and a future release quietly adding one should have
// to change this line.
var documentedDefaults = map[string]any{
	// docs/adr/0020-resource-limits.md
	"WithMaxSourceBytes":       DefaultMaxSourceBytes,
	"WithMaxDecompressedBytes": DefaultMaxDecompressedBytes,
	"WithMaxStreamBytes":       DefaultMaxStreamBytes,
	"WithMaxTextBytes":         DefaultMaxTextBytes,
	"WithMaxPages":             DefaultMaxPages,
	"WithMaxDepth":             DefaultMaxDepth,
	"WithMaxObjects":           DefaultMaxObjects,
	"WithMaxPagePixels":        DefaultMaxPagePixels,
	"WithConcurrency":          "min(4, GOMAXPROCS)",
	"context.WithTimeout":      "none — use the context",

	// docs/confidence.md
	"WithReviewThreshold": DefaultReviewThreshold,

	// docs/pipeline.md
	"WithMinTextDensity":      DefaultMinTextDensity,
	"WithMaxReplacementRatio": DefaultMaxReplacementRatio,
	"WithMinDecodableRatio":   DefaultMinDecodableRatio,
}

// confidenceWeights is the set of signals the weights table must name, keyed by
// the constant so that renaming a signal breaks here too.
var confidenceWeights = map[string]bool{
	SignalGrounding:  true,
	SignalAgreement:  true,
	SignalOCR:        true,
	SignalSchema:     true,
	SignalFormat:     true,
	SignalCrossField: true,
}

// hardFloorCount is the number of rows the hard-floor table must carry.
//
// There is no constant to compare the floors against yet: the default scorer is
// unimplemented, so the values live only in prose. Checking the shape of the
// table is what is available today, and when the scorer lands the floors should
// become constants and move into documentedDefaults.
const hardFloorCount = 5

func TestDocumentedDefaults(t *testing.T) {
	t.Parallel()

	rows := optionRows(t, ".")
	if len(rows) == 0 {
		t.Fatal("no documentation table rows naming a With… option were found; the table format must have changed")
	}

	t.Run("every documented row has a bridge entry", func(t *testing.T) {
		for _, r := range rows {
			if _, ok := documentedDefaults[r.option]; !ok {
				t.Errorf("%s:%d: %s is documented with a default of %q but has no entry in "+
					"documentedDefaults — either it was documented and never built, or add the entry",
					r.file, r.line, r.option, r.value)
			}
		}
	})

	t.Run("every bridge entry is documented", func(t *testing.T) {
		seen := map[string]bool{}
		for _, r := range rows {
			seen[r.option] = true
		}
		for _, name := range sortedKeys(documentedDefaults) {
			if !seen[name] {
				t.Errorf("documentedDefaults has an entry for %s but no documentation table mentions it", name)
			}
		}
	})

	t.Run("every numeric option is documented", func(t *testing.T) {
		_, files := parseRootPackage(t)
		options := numericOptions(files)

		for _, name := range sortedStrings(options) {
			if _, ok := documentedDefaults[name]; !ok {
				t.Errorf("%s is a numeric option but is in no documentation table — "+
					"we built a limit nobody documented", name)
			}
		}
		for _, name := range sortedKeys(documentedDefaults) {
			if strings.Contains(name, ".") {
				continue // qualified: not one of ours, e.g. context.WithTimeout
			}
			if !options[name] {
				t.Errorf("documentedDefaults names %s, which is not a func With…(number) Option "+
					"in this package — the option was renamed or removed", name)
			}
		}
	})

	t.Run("documented values equal the constants", func(t *testing.T) {
		for _, r := range rows {
			want, ok := documentedDefaults[r.option]
			if !ok {
				continue // already reported above
			}
			compareDocumentedValue(t, r, want)
		}
	})

	t.Run("no limit is documented twice with different values", func(t *testing.T) {
		byOption := map[string][]docRow{}
		for _, r := range rows {
			byOption[r.option] = append(byOption[r.option], r)
		}
		for _, name := range sortedRowKeys(byOption) {
			group := byOption[name]
			first := group[0]
			for _, r := range group[1:] {
				if sameDocumentedValue(first.value, r.value) {
					continue
				}
				t.Errorf("%s is documented as %q at %s:%d and as %q at %s:%d",
					name, first.value, first.file, first.line, r.value, r.file, r.line)
			}
		}
	})

	t.Run("the confidence weights sum to 1.00", func(t *testing.T) {
		weights := namedTable(t, "docs/confidence.md", []string{"Signal", "Weight"})
		if len(weights) != len(confidenceWeights) {
			t.Fatalf("docs/confidence.md: the weights table has %d rows, expected %d",
				len(weights), len(confidenceWeights))
		}

		// Summed in hundredths. Adding the float64 values would leave the
		// assertion at the mercy of binary rounding, and "sums to 1.00" is a
		// claim about the written decimals.
		total := 0
		for _, r := range weights {
			name := strings.Trim(cleanCell(r.cells[0]), "`")
			if !confidenceWeights[name] {
				t.Errorf("%s:%d: the weights table names the signal %q, which is not one of the "+
					"Signal* constants", r.file, r.line, name)
			}
			w, _, ok := documentedValue(r.cells[1])
			if !ok {
				t.Errorf("%s:%d: the weight for %q is %q, which is not a number",
					r.file, r.line, name, r.cells[1])
				continue
			}
			total += int(math.Round(w * 100))
		}
		if total != 100 {
			t.Errorf("docs/confidence.md: the confidence weights sum to %.2f, not 1.00 — "+
				"a weighted mean whose weights do not sum to one is not a weighted mean",
				float64(total)/100)
		}
	})

	t.Run("the hard floors are numbers", func(t *testing.T) {
		floors := namedTable(t, "docs/confidence.md", []string{"Condition", "Confidence capped at"})
		if len(floors) != hardFloorCount {
			t.Errorf("docs/confidence.md: the hard-floor table has %d rows, expected %d",
				len(floors), hardFloorCount)
		}
		for _, r := range floors {
			v, text, ok := documentedValue(r.cells[1])
			if !ok {
				t.Errorf("%s:%d: the hard floor for %q is %q, which is not a number",
					r.file, r.line, cleanCell(r.cells[0]), text)
				continue
			}
			if v < 0 || v > 1 {
				t.Errorf("%s:%d: the hard floor for %q is %v, which is not a confidence",
					r.file, r.line, cleanCell(r.cells[0]), v)
			}
		}
	})
}

// compareDocumentedValue checks one table row against its constant.
func compareDocumentedValue(t *testing.T, r docRow, want any) {
	t.Helper()

	got, text, isNum := documentedValue(r.value)

	switch w := want.(type) {
	case string:
		if isNum || text != w {
			t.Errorf("%s:%d: %s is documented as %q, the bridge says %q",
				r.file, r.line, r.option, text, w)
		}
	case int, int64, float64:
		wantNum := toFloat(w)
		if !isNum {
			t.Errorf("%s:%d: %s is documented as %q, which is not a number; the constant is %s",
				r.file, r.line, r.option, text, formatNumber(wantNum))
			return
		}
		if got != wantNum {
			t.Errorf("%s:%d: %s is documented as %q (%s) but the constant is %s",
				r.file, r.line, r.option, text, formatNumber(got), formatNumber(wantNum))
		}
	default:
		t.Errorf("documentedDefaults[%q] has type %T; add a case to compareDocumentedValue", r.option, want)
	}
}

func sameDocumentedValue(a, b string) bool {
	an, at, aIsNum := documentedValue(a)
	bn, bt, bIsNum := documentedValue(b)
	if aIsNum != bIsNum {
		return false
	}
	if aIsNum {
		return an == bn
	}
	return at == bt
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return math.NaN()
}

func formatNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ---------------------------------------------------------------------------
// Reading the numbers out of the prose
// ---------------------------------------------------------------------------

// suffixes are the magnitudes a table is allowed to write instead of digits.
// Longest first: "64 MiB" must not match "B".
var suffixes = []struct {
	text  string
	scale float64
}{
	{"MiB", 1 << 20},
	{"GiB", 1 << 30},
	{"KiB", 1 << 10},
	{"MB", 1e6},
	{"GB", 1e9},
	{"KB", 1e3},
	{"M", 1e6},
	{"K", 1e3},
	{"%", 0.01},
}

// documentedValue reads a table cell.
//
// It returns the number the cell states, the cell as cleaned text, and whether
// the cell stated a number at all. A cell that does not — "min(4, GOMAXPROCS)",
// "none — use the context", "always" — is a legitimate documented default and
// is compared as text.
func documentedValue(cell string) (num float64, text string, isNum bool) {
	text = cleanCell(cell)

	// A trailing gloss after an em or en dash is prose: "0.0 — the field is
	// absent" documents 0.0.
	head := text
	for _, dash := range []string{"—", "–"} {
		if i := strings.Index(head, dash); i >= 0 {
			head = head[:i]
		}
	}
	head = strings.TrimSpace(strings.Trim(head, "`"))
	head = strings.TrimLeft(head, "≥≤<>~≈± ")

	scale := 1.0
	for _, s := range suffixes {
		if strings.HasSuffix(head, s.text) {
			head = strings.TrimSpace(strings.TrimSuffix(head, s.text))
			scale = s.scale
			break
		}
	}
	// "500 000" is one number; the space is a thousands separator.
	head = strings.ReplaceAll(head, " ", "")
	if head == "" {
		return 0, text, false
	}

	v, err := strconv.ParseFloat(head, 64)
	if err != nil {
		return 0, text, false
	}
	if scale == 0.01 {
		// Divide rather than multiply by 0.01: a percentage written as a whole
		// number then compares bit-for-bit with the decimal constant.
		return v / 100, text, true
	}
	return v * scale, text, true
}

var nbsp = strings.NewReplacer(" ", " ", " ", " ", " ", " ")

var spaceRun = regexp.MustCompile(`\s+`)

// cleanCell strips backticks and normalises whitespace, so that a cell reads
// the way a person reads it.
func cleanCell(s string) string {
	s = nbsp.Replace(s)
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(spaceRun.ReplaceAllString(s, " "))
}

// ---------------------------------------------------------------------------
// Reading the tables
// ---------------------------------------------------------------------------

type mdRow struct {
	file  string
	line  int
	cells []string
	isSep bool
}

type docRow struct {
	file   string
	line   int
	option string
	value  string
}

// optionPattern matches a cell that names an option: `WithMaxPages`, or a
// qualified `context.WithTimeout`.
var optionPattern = regexp.MustCompile(`^(?:[a-z][a-z0-9]*\.)?With[A-Z][A-Za-z0-9]*$`)

// optionRows finds every table row in every Markdown file under root whose last
// cells are an option name preceded by its default.
func optionRows(t *testing.T, root string) []docRow {
	t.Helper()

	var out []docRow
	for _, path := range markdownFiles(t, root) {
		for _, r := range markdownRows(t, path) {
			if r.isSep {
				continue
			}
			for i, cell := range r.cells {
				if i == 0 || !optionPattern.MatchString(cleanCell(cell)) {
					continue
				}
				out = append(out, docRow{
					file:   r.file,
					line:   r.line,
					option: cleanCell(cell),
					value:  r.cells[i-1],
				})
				break
			}
		}
	}
	return out
}

// namedTable returns the rows of the table introduced by the given header, in
// the given file.
func namedTable(t *testing.T, path string, header []string) []mdRow {
	t.Helper()

	rows := markdownRows(t, path)
	start := -1
	for i, r := range rows {
		if equalCells(cleanCells(r.cells), header) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no table with the header | %s |", path, strings.Join(header, " | "))
	}

	var out []mdRow
	for i := start + 1; i < len(rows); i++ {
		// A table ends at the first line that is not the next line.
		if rows[i].line != rows[i-1].line+1 {
			break
		}
		if rows[i].isSep {
			continue
		}
		out = append(out, rows[i])
	}
	return out
}

var separatorCell = regexp.MustCompile(`^:?-+:?$`)

// markdownRows returns every table row in a Markdown file, in order.
//
// Rows inside a fenced code block are skipped: a Go example may perfectly well
// contain a line starting with a pipe, and it is not a table.
func markdownRows(t *testing.T, path string) []mdRow {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out []mdRow
	inFence := false
	for i, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := splitRow(trimmed)
		if len(cells) == 0 {
			continue
		}
		sep := true
		for _, c := range cells {
			if !separatorCell.MatchString(strings.TrimSpace(c)) {
				sep = false
				break
			}
		}
		out = append(out, mdRow{
			file:  filepath.ToSlash(path),
			line:  i + 1,
			cells: cells,
			isSep: sep,
		})
	}
	return out
}

func splitRow(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func markdownFiles(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s for Markdown: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Reading the options out of the code
// ---------------------------------------------------------------------------

// numericParam is the set of parameter types that make a With… function a
// documented default rather than a provider or a policy choice. WithModel takes
// a Model and belongs in prose; WithMaxPages takes an int and belongs in a
// table. Extending this set means extending documentedValue to match.
var numericParam = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "byte": true, "rune": true,
}

// numericOptions returns every exported func With…(number) Option in the
// package.
func numericOptions(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !strings.HasPrefix(fn.Name.Name, "With") {
				continue
			}
			if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 || render(fn.Type.Results.List[0].Type) != "Option" {
				continue
			}
			params := fn.Type.Params
			if params == nil || len(params.List) != 1 || len(params.List[0].Names) != 1 {
				continue
			}
			if !numericParam[render(params.List[0].Type)] {
				continue
			}
			out[fn.Name.Name] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func cleanCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = cleanCell(c)
	}
	return out
}

func equalCells(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRowKeys(m map[string][]docRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
