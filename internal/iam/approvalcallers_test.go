package iam_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"kamarapms/internal/iam"
)

// The structural test of the rules of approval (docs/architecture/32-distinct-approver.md section 4), in the manner of the guard of the sale paths: it reads the source of every module with
// go/parser and fails when a use case asks for an approval without naming its kind, without the amount of a kind that has one, or without writing the marker in its audit entry; and when
// a kind of the table is asked for by no use case. A new use case that forgets any of that fails here, and a rule would otherwise silently not apply to it.

type callSite struct {
	file, fn string
	pos      token.Position
	kind     string // the name of the constant, "" when it is not a constant
	hasAmt   bool
}

// kindConstants reads the constants of the kinds from approvalkinds.go: the name of the constant and the kind it stands for.
func kindConstants(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "approvalkinds.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if bl, ok := vs.Values[i].(*ast.BasicLit); ok {
						v, _ := strconv.Unquote(bl.Value)
						out[n.Name] = v
					}
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no kind constants found")
	}
	return out
}

func kindSelector(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := s.X.(*ast.Ident); ok && x.Name == "iam" && strings.HasPrefix(s.Sel.Name, "Kind") {
			return s.Sel.Name
		}
	}
	return ""
}

func TestEveryUseCaseThatAsksForAnApprovalNamesItsKindAndAmountAndMarksTheEntry(t *testing.T) {
	consts := kindConstants(t)
	hasAmount := map[string]bool{}
	for _, k := range iam.ApprovalKinds() {
		hasAmount[string(k.Kind)] = k.HasAmount
	}
	for name, v := range consts {
		if _, ok := hasAmount[v]; !ok {
			t.Errorf("%s (%s) is a constant of a kind that is not in the table", name, v)
		}
	}
	files, err := filepath.Glob("../*/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no module sources: %v", err)
	}
	var sites []callSite
	marks := map[string]bool{} // the packages whose code writes the marker in an entry
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "iam" || strings.HasSuffix(filepath.Dir(path), "db") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Dir(path)
		if s := string(b); strings.Contains(s, ".AuditFields(") || strings.Contains(s, ".Marker(") || strings.Contains(s, "iam.SelfApprovedKey") {
			marks[pkg] = true
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, b, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					sel, ok := x.Type.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "ApprovalRequest" {
						return true
					}
					cs := callSite{file: pkg, fn: fd.Name.Name, pos: fset.Position(x.Pos())}
					for _, el := range x.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						switch kv.Key.(*ast.Ident).Name {
						case "Kind":
							cs.kind = kindSelector(kv.Value)
						case "Amount":
							cs.hasAmt = true
						}
					}
					sites = append(sites, cs)
				case *ast.CallExpr:
					// the helper of the reservations, which asks for the approval of a request line by line: its kind is the third argument
					if id, ok := x.Fun.(*ast.SelectorExpr); ok && id.Sel.Name == "approveRun" && len(x.Args) >= 7 {
						sites = append(sites, callSite{file: pkg, fn: fd.Name.Name, pos: fset.Position(x.Pos()), kind: kindSelector(x.Args[2]), hasAmt: true})
					}
				}
				return true
			})
		}
	}
	if len(sites) < 25 {
		t.Fatalf("only %d calls found: the scan is not looking at the modules", len(sites))
	}
	used := map[string]bool{}
	for _, s := range sites {
		where := s.pos.String()
		if s.fn == "approveRun" { // the helper itself passes the kind it was given
			continue
		}
		if s.kind == "" {
			t.Errorf("%s (%s): the request of an approval must name its kind with a constant of iam (iam.KindXxx)", where, s.fn)
			continue
		}
		v, ok := consts[s.kind]
		if !ok {
			t.Errorf("%s: %s is not a kind of the table", where, s.kind)
			continue
		}
		used[v] = true
		if hasAmount[v] && !s.hasAmt {
			t.Errorf("%s (%s): the kind %s has an amount and the request does not pass it", where, s.fn, v)
		}
		if !marks[s.file] {
			t.Errorf("%s: the package %s asks for an approval and never writes AuditFields or the marker in an audit entry", where, s.file)
		}
	}
	var unused []string
	for _, k := range iam.ApprovalKinds() {
		if !used[string(k.Kind)] {
			unused = append(unused, string(k.Kind))
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("kinds of the table that no use case asks for: %v", unused)
	}
}

// The scan fails on a use case that forgets what it must say, so that the test above is known to look at what it claims to.
func TestTheCallerScanSeesTheKindsOfTheTable(t *testing.T) {
	consts := kindConstants(t)
	seen := map[string]bool{}
	for _, v := range consts {
		seen[v] = true
	}
	for _, k := range iam.ApprovalKinds() {
		if !seen[string(k.Kind)] {
			t.Errorf("%s has no constant", k.Kind)
		}
	}
	if len(consts) != len(iam.ApprovalKinds()) {
		t.Errorf("%d constants for %d kinds", len(consts), len(iam.ApprovalKinds()))
	}
}
