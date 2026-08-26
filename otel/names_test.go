package ovrinotel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/BAGOMBEKA-JOB-DEV/ovrin"
)

// TestSpanNamesMatchTheDocumentation pins the emitted names to
// docs/observability.md. They are API: renaming one breaks a dashboard, so it
// is a breaking change with a migration note rather than a tidy-up.
func TestSpanNamesMatchTheDocumentation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   ovrin.Op
		span string
	}{
		{ovrin.OpDetect, "ovrin.detect"},
		{ovrin.OpAcquire, "ovrin.acquire"},
		{ovrin.OpRender, "ovrin.render"},
		{ovrin.OpOCR, "ovrin.ocr"},
		{ovrin.OpNormalise, "ovrin.normalise"},
		{ovrin.OpSchema, "ovrin.schema"},
		{ovrin.OpPrompt, "ovrin.prompt"},
		{ovrin.OpGenerate, "ovrin.generate"},
		{ovrin.OpValidate, "ovrin.validate"},
		{ovrin.OpGround, "ovrin.ground"},
		{ovrin.OpScore, "ovrin.score"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.op), func(t *testing.T) {
			t.Parallel()
			if got := spanNames[tt.op]; got != tt.span {
				t.Errorf("spanNames[%q] = %q, want %q", tt.op, got, tt.span)
			}
		})
	}

	if spanExtract != "ovrin.extract" {
		t.Errorf("root span = %q, want ovrin.extract", spanExtract)
	}
	if len(spanNames) != len(tests) {
		t.Errorf("spanNames has %d entries, want %d; an entry the documentation does not list is drift too",
			len(spanNames), len(tests))
	}
}

// TestSpanNamesCoverEveryOp reads the Op constants out of the core's source
// and fails if one of them has no span name.
//
// Go cannot enumerate the values of a named string type, and a hand-copied
// list is exactly the thing that goes stale: somebody adds a stage, the stage
// runs, and no trace ever shows it. Parsing the declaration means the drift
// cannot survive a test run.
func TestSpanNamesCoverEveryOp(t *testing.T) {
	t.Parallel()

	for _, op := range declaredOps(t) {
		op := op
		t.Run(string(op), func(t *testing.T) {
			t.Parallel()
			if _, ok := spanNames[op]; !ok {
				t.Errorf("ovrin.Op %q has no span name; add one to spanNames and to docs/observability.md", op)
			}
		})
	}
}

// declaredOps returns every non-zero Op constant declared by the core.
//
// The zero value is excluded: an Event that does not know its stage is not a
// stage, and there is no span for it. Its metrics still carry op "unknown",
// which is [ovrin.Op.String]'s own rendering of it.
func declaredOps(t *testing.T) []ovrin.Op {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go tool on PATH, so the core's source cannot be located: %v", err)
	}
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", "github.com/BAGOMBEKA-JOB-DEV/ovrin").Output()
	if err != nil {
		t.Fatalf("locating the core package: %v", err)
	}
	dir := strings.TrimSpace(string(out))

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	pkg, ok := pkgs["ovrin"]
	if !ok {
		t.Fatalf("no package ovrin in %s", dir)
	}

	var ops []ovrin.Op
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				id, ok := vs.Type.(*ast.Ident)
				if !ok || id.Name != "Op" || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquoting %s: %v", lit.Value, err)
				}
				if v == "" {
					continue
				}
				ops = append(ops, ovrin.Op(v))
			}
		}
	}

	if len(ops) == 0 {
		t.Fatalf("no Op constants found in %s; the parse is broken, not the vocabulary", dir)
	}
	return ops
}

// TestErrorKindsCoverEverySentinel is the same guard for the error vocabulary:
// a new sentinel with no token lands every failure of that kind in
// "unclassified", where nobody alerts on it.
func TestErrorKindsCoverEverySentinel(t *testing.T) {
	t.Parallel()

	sentinels := []struct {
		err  error
		want string
	}{
		{ovrin.ErrUnsupportedFormat, "unsupported_format"},
		{ovrin.ErrNoContent, "no_content"},
		{ovrin.ErrNoProvider, "no_provider"},
		{ovrin.ErrSchema, "schema"},
		{ovrin.ErrLimitExceeded, "limit_exceeded"},
		{ovrin.ErrAuth, "auth"},
		{ovrin.ErrRateLimit, "rate_limit"},
		{ovrin.ErrUnavailable, "unavailable"},
		{ovrin.ErrBadResponse, "bad_response"},
		{ovrin.ErrUnsupported, "unsupported"},
		{ovrin.ErrEncrypted, "encrypted"},
		{ovrin.ErrInternal, "internal"},
		{ovrin.ErrBadRequest, "bad_request"},
	}

	for _, tt := range sentinels {
		tt := tt
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := errorKind(tt.err); got != tt.want {
				t.Errorf("errorKind(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}

	if len(errorKinds) != len(sentinels) {
		t.Errorf("errorKinds has %d entries, want %d", len(errorKinds), len(sentinels))
	}
}
