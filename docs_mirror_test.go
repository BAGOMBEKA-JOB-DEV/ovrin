// Drift caught here: a declaration written out in the documentation quietly
// ceasing to be the declaration in the code.
//
// This repository was written documentation-first, so the docs restate real Go
// declarations in prose — struct shapes, seam interfaces, constructor
// signatures. Those restatements are the first thing a reader believes and the
// last thing anybody remembers to change.
//
// Compiling them is not enough, and that is the point of this file. Extracting
// a doc's `type Event struct{…}` into a scratch package and building it proves
// only that the doc's own copy is well-formed; it stays green while the field
// it names is renamed in the real type, because the scratch package never
// mentions the real type. Roughly a fifth of the Go blocks in these docs are
// declaration listings, and a compile check passes all of them vacuously.
//
// So the comparison here is structural. A fence marked
//
//	```go mirror
//
// is parsed, and every top-level declaration in it is matched by name against
// the same declaration in the root package and compared:
//
//   - struct: field names in order, rendered types, struct tags — unexported
//     fields included, because a doc that omits them is describing a different
//     type from the one the compiler sees.
//   - interface: method names in order, and their rendered signatures.
//   - func and method: the rendered signature including parameter names. The
//     golden API file drops names deliberately; the docs show them, and a doc
//     saying `field` where the code says `name` is drift a reader trips over.
//   - const and var blocks: the same names, and where the value is a literal —
//     directly, or as the sole argument to errors.New — the identical literal.
//     That makes the error-string contract a checked thing for free.
//
// Mirroring is opt-in per fence: a declaration the docs do not mirror is not a
// failure, a name in a fence that the package does not declare is.
//
// Helpers shared with the other two drift tests (parseRootPackage, render,
// flattenFields, renderTypeParams) live in api_test.go.
package ovrin

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// mirrorFenceOpen is the fence marker. A plain ```go fence is illustrative and
// is not checked; adding " mirror" is a claim that the block restates real
// declarations.
const mirrorFenceOpen = "```go mirror"

func TestDocsMirror(t *testing.T) {
	t.Parallel()

	fences := mirrorFences(t, ".")
	if len(fences) == 0 {
		t.Fatalf("no %s fences found; either the marker changed or the docs stopped mirroring", mirrorFenceOpen)
	}

	_, files := parseRootPackage(t)
	idx := indexPackage(files)

	checked := 0
	for _, fence := range fences {
		// A fence is a fragment, so it is parsed as the body of a package.
		// Line 1 of the synthetic file is that package clause, so a node on
		// parsed line n is on doc line fence.line + n - 2.
		src := "package p\n" + fence.body
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, fence.file, src, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s:%d: the mirror fence does not parse as Go: %v", fence.file, fence.line, err)
			continue
		}
		at := func(pos token.Pos) string {
			return fmt.Sprintf("%s:%d", fence.file, fence.line+fset.Position(pos).Line-2)
		}
		for _, decl := range f.Decls {
			checked += checkDecl(t, idx, at, decl)
		}
	}
	t.Logf("compared %d mirrored declarations across %d fences", checked, len(fences))
}

// checkDecl compares one mirrored declaration against the package and returns
// how many names it checked.
func checkDecl(t *testing.T, idx *pkgIndex, at func(token.Pos) string, decl ast.Decl) int {
	t.Helper()

	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			checkFunc(t, idx, at, d)
		} else {
			checkMethod(t, idx, at, d)
		}
		return 1

	case *ast.GenDecl:
		n := 0
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				checkType(t, idx, at, s)
				n++
			case *ast.ValueSpec:
				n += checkValues(t, idx, at, d.Tok, s)
			}
		}
		return n
	}
	return 0
}

func checkType(t *testing.T, idx *pkgIndex, at func(token.Pos) string, doc *ast.TypeSpec) {
	t.Helper()

	name := doc.Name.Name
	pkg, ok := idx.types[name]
	if !ok {
		t.Errorf("%s: type %s is mirrored here but is not declared in package ovrin", at(doc.Pos()), name)
		return
	}

	if dtp, ptp := renderTypeParams(doc.TypeParams), renderTypeParams(pkg.TypeParams); dtp != ptp {
		t.Errorf("%s: type %s: type parameters differ\n%s", at(doc.Pos()), name, twoSided(dtp, ptp))
	}

	switch docType := doc.Type.(type) {
	case *ast.StructType:
		pkgType, ok := pkg.Type.(*ast.StructType)
		if !ok {
			t.Errorf("%s: type %s is a struct in the docs and %s in the package\n%s",
				at(doc.Pos()), name, kindOf(pkg.Type), twoSided(render(doc.Type), render(pkg.Type)))
			return
		}
		compareFields(t, at, name, doc.Pos(), flattenFields(docType.Fields), flattenFields(pkgType.Fields))

	case *ast.InterfaceType:
		pkgType, ok := pkg.Type.(*ast.InterfaceType)
		if !ok {
			t.Errorf("%s: type %s is an interface in the docs and %s in the package\n%s",
				at(doc.Pos()), name, kindOf(pkg.Type), twoSided(render(doc.Type), render(pkg.Type)))
			return
		}
		compareMembers(t, at, "type "+name, "method", flattenInterface(docType), flattenInterface(pkgType), doc.Pos())

	default:
		if kindOf(pkg.Type) != kindOf(doc.Type) || render(doc.Type) != render(pkg.Type) {
			t.Errorf("%s: type %s: the declaration differs\n%s",
				at(doc.Pos()), name, twoSided(render(doc.Type), render(pkg.Type)))
		}
	}
}

// compareFields reports every field that differs, and any change in count.
func compareFields(t *testing.T, at func(token.Pos) string, name string, pos token.Pos, doc, pkg []fieldRec) {
	t.Helper()

	n := len(doc)
	if len(pkg) < n {
		n = len(pkg)
	}
	for i := 0; i < n; i++ {
		if doc[i].name == pkg[i].name && doc[i].typ == pkg[i].typ && doc[i].tag == pkg[i].tag {
			continue
		}
		t.Errorf("%s: type %s: field %d differs\n%s",
			at(doc[i].pos), name, i+1, twoSided(fieldString(doc[i]), fieldString(pkg[i])))
	}
	if len(doc) != len(pkg) {
		t.Errorf("%s: type %s: the docs show %d fields and the package declares %d\n%s",
			at(pos), name, len(doc), len(pkg),
			twoSided(fieldList(doc[n:]), fieldList(pkg[n:])))
	}
}

// compareMembers reports differences between two ordered lists of named
// members — interface methods, or the entries of a value block.
func compareMembers(t *testing.T, at func(token.Pos) string, subject, kind string, doc, pkg []methodRec, pos token.Pos) {
	t.Helper()

	n := len(doc)
	if len(pkg) < n {
		n = len(pkg)
	}
	for i := 0; i < n; i++ {
		if doc[i].name == pkg[i].name && doc[i].sig == pkg[i].sig {
			continue
		}
		t.Errorf("%s: %s: %s %d differs\n%s", at(pos), subject, kind, i+1,
			twoSided(doc[i].name+doc[i].sig, pkg[i].name+pkg[i].sig))
	}
	if len(doc) != len(pkg) {
		t.Errorf("%s: %s: the docs show %d %ss and the package declares %d",
			at(pos), subject, len(doc), kind, len(pkg))
	}
}

// checkValues compares each name in a mirrored const or var block.
//
// It is per-name rather than block-to-block because the package is free to
// split a block across files, and doing so is not drift. A block in the docs
// listing only some of a package's constants is not drift either: mirroring is
// opt-in.
func checkValues(t *testing.T, idx *pkgIndex, at func(token.Pos) string, tok token.Token, doc *ast.ValueSpec) int {
	t.Helper()

	kw := "const"
	if tok == token.VAR {
		kw = "var"
	}
	for i, n := range doc.Names {
		if n.Name == "_" {
			continue
		}
		pkg, ok := idx.values[n.Name]
		if !ok {
			t.Errorf("%s: %s %s is mirrored here but is not declared in package ovrin", at(n.Pos()), kw, n.Name)
			continue
		}
		if pkg.kind != tok {
			t.Errorf("%s: %s is a %s in the docs and a %s in the package",
				at(n.Pos()), n.Name, kw, strings.ToLower(pkg.kind.String()))
		}
		if doc.Type != nil || pkg.typ != nil {
			if dt, pt := render(doc.Type), render(pkg.typ); dt != pt {
				t.Errorf("%s: %s %s: the declared type differs\n%s", at(n.Pos()), kw, n.Name, twoSided(dt, pt))
			}
		}
		if i >= len(doc.Values) {
			continue
		}
		docLit, ok := comparableLiteral(doc.Values[i])
		if !ok {
			continue
		}
		pkgLit, ok := comparableLiteral(pkg.value)
		if !ok || docLit != pkgLit {
			t.Errorf("%s: %s %s: the value differs\n%s", at(n.Pos()), kw, n.Name,
				twoSided(render(doc.Values[i]), render(pkg.value)))
		}
	}
	return len(doc.Names)
}

// comparableLiteral extracts the literal a value declaration pins down, and
// reports whether there was one.
//
// A bare literal is the obvious case. A single-literal call — errors.New("…"),
// Kind("pdf") — is the other, and it is the one that makes the error-string
// contract checkable: a doc quoting an error message is then quoting the
// message the package actually produces.
func comparableLiteral(x ast.Expr) (string, bool) {
	switch v := x.(type) {
	case *ast.BasicLit:
		return v.Value, true
	case *ast.CallExpr:
		if len(v.Args) != 1 {
			return "", false
		}
		lit, ok := v.Args[0].(*ast.BasicLit)
		if !ok {
			return "", false
		}
		return render(v.Fun) + "(" + lit.Value + ")", true
	}
	return "", false
}

func checkFunc(t *testing.T, idx *pkgIndex, at func(token.Pos) string, doc *ast.FuncDecl) {
	t.Helper()

	pkg, ok := idx.funcs[doc.Name.Name]
	if !ok {
		t.Errorf("%s: func %s is mirrored here but is not declared in package ovrin", at(doc.Pos()), doc.Name.Name)
		return
	}
	d, p := namedSignature(doc), namedSignature(pkg)
	if d != p {
		t.Errorf("%s: func %s: the signature differs\n%s", at(doc.Pos()), doc.Name.Name, twoSided(d, p))
	}
}

func checkMethod(t *testing.T, idx *pkgIndex, at func(token.Pos) string, doc *ast.FuncDecl) {
	t.Helper()

	recv := baseTypeName(doc.Recv.List[0].Type)
	key := recv + "." + doc.Name.Name
	pkg, ok := idx.methods[key]
	if !ok {
		t.Errorf("%s: method %s is mirrored here but is not declared in package ovrin", at(doc.Pos()), key)
		return
	}
	if d, p := render(doc.Recv.List[0].Type), render(pkg.Recv.List[0].Type); d != p {
		t.Errorf("%s: method %s: the receiver differs\n%s", at(doc.Pos()), key, twoSided(d, p))
	}
	d, p := namedSignature(doc), namedSignature(pkg)
	if d != p {
		t.Errorf("%s: method %s: the signature differs\n%s", at(doc.Pos()), key, twoSided(d, p))
	}
}

// ---------------------------------------------------------------------------
// Rendering with names
// ---------------------------------------------------------------------------

// namedSignature renders a function the way the docs write it: parameter and
// result names kept, grouped declarations flattened so that "page, dpi int"
// and "page int, dpi int" are the same signature.
func namedSignature(fn *ast.FuncDecl) string {
	return fn.Name.Name + renderTypeParams(fn.Type.TypeParams) + funcTypeWithNames(fn.Type)
}

func funcTypeWithNames(ft *ast.FuncType) string {
	sig := "(" + strings.Join(namedFields(ft.Params), ", ") + ")"
	rs := namedFields(ft.Results)
	switch {
	case len(rs) == 0:
		return sig
	case len(rs) == 1 && !strings.Contains(rs[0], " "):
		return sig + " " + rs[0]
	default:
		return sig + " (" + strings.Join(rs, ", ") + ")"
	}
}

// namedFields flattens a field list to one "name type" (or bare "type") entry
// per declared name.
func namedFields(fl *ast.FieldList) []string {
	var out []string
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		typ := render(f.Type)
		if len(f.Names) == 0 {
			out = append(out, typ)
			continue
		}
		for _, n := range f.Names {
			out = append(out, n.Name+" "+typ)
		}
	}
	return out
}

// flattenInterface renders an interface's members in declaration order.
// Embedded interfaces are kept as written rather than expanded: a doc that
// writes `OCR` is mirroring the declaration, and the expanded method set is
// what api/ovrin.txt records.
func flattenInterface(it *ast.InterfaceType) []methodRec {
	var out []methodRec
	if it.Methods == nil {
		return out
	}
	for _, f := range it.Methods.List {
		if len(f.Names) == 0 {
			out = append(out, methodRec{name: render(f.Type)})
			continue
		}
		ft, ok := f.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		for _, n := range f.Names {
			out = append(out, methodRec{name: n.Name, sig: funcTypeWithNames(ft)})
		}
	}
	return out
}

func kindOf(x ast.Expr) string {
	switch x.(type) {
	case *ast.StructType:
		return "a struct"
	case *ast.InterfaceType:
		return "an interface"
	case *ast.FuncType:
		return "a func type"
	}
	return "a " + render(x)
}

func fieldString(f fieldRec) string {
	s := strings.TrimSpace(f.name + " " + f.typ)
	if f.tag != "" {
		s += " " + f.tag
	}
	return s
}

func fieldList(fs []fieldRec) string {
	if len(fs) == 0 {
		return "(nothing more)"
	}
	var out []string
	for _, f := range fs {
		out = append(out, fieldString(f))
	}
	return strings.Join(out, "; ")
}

// twoSided formats the doc's version above the package's, which is the only
// form of this message anybody can act on without opening both files.
func twoSided(doc, pkg string) string {
	return "\tdocs: " + doc + "\n\tcode: " + pkg
}

// ---------------------------------------------------------------------------
// The package index
// ---------------------------------------------------------------------------

type valueRec struct {
	kind  token.Token
	typ   ast.Expr
	value ast.Expr
}

type pkgIndex struct {
	types   map[string]*ast.TypeSpec
	funcs   map[string]*ast.FuncDecl
	methods map[string]*ast.FuncDecl
	values  map[string]valueRec
}

func indexPackage(files []*ast.File) *pkgIndex {
	idx := &pkgIndex{
		types:   map[string]*ast.TypeSpec{},
		funcs:   map[string]*ast.FuncDecl{},
		methods: map[string]*ast.FuncDecl{},
		values:  map[string]valueRec{},
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					idx.funcs[d.Name.Name] = d
					continue
				}
				idx.methods[baseTypeName(d.Recv.List[0].Type)+"."+d.Name.Name] = d
			case *ast.GenDecl:
				var lastType ast.Expr
				var lastValues []ast.Expr
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						idx.types[s.Name.Name] = s
					case *ast.ValueSpec:
						typ, values := s.Type, s.Values
						if typ == nil && len(values) == 0 {
							typ, values = lastType, lastValues
						} else {
							lastType, lastValues = typ, values
						}
						for i, n := range s.Names {
							rec := valueRec{kind: d.Tok, typ: typ}
							if i < len(values) {
								rec.value = values[i]
							}
							idx.values[n.Name] = rec
						}
					}
				}
			}
		}
	}
	return idx
}

// ---------------------------------------------------------------------------
// Finding the fences
// ---------------------------------------------------------------------------

type mirrorFence struct {
	file string // path relative to the repository root
	line int    // 1-based line of the first line of content
	body string
}

// mirrorFences finds every mirror fence in every Markdown file under root.
func mirrorFences(t *testing.T, root string) []mirrorFence {
	t.Helper()

	var out []mirrorFence
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
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, fencesIn(filepath.ToSlash(path), string(b))...)
		return nil
	})
	if err != nil {
		t.Fatalf("scanning %s for Markdown: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out
}

func fencesIn(path, content string) []mirrorFence {
	var out []mirrorFence
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != mirrorFenceOpen {
			continue
		}
		start := i + 1
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		out = append(out, mirrorFence{
			file: strings.TrimPrefix(path, "./"),
			line: start + 1, // 1-based line number of lines[start]
			body: strings.Join(lines[start:end], "\n") + "\n",
		})
		i = end
	}
	return out
}
