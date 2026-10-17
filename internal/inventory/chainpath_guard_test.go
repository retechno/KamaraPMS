package inventory_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The structural guard of the approval chain (35-inventory.md 5.8, in the spirit of document 32 4).
//
// Only the engine (internal/inventory/requests.go) writes the states SUBMITTED, APPROVED and RETURNED of a purchase request, and every function of it that does so writes an audit entry; a function that moves the
// request along the chain (submit, approve, return) also writes the notices (directly, or through advance and finishRequest). A new path that changes the status of a request elsewhere, or without audit, fails here.

var guardedStates = map[string]bool{"ReqSubmitted": true, "ReqApproved": true, "ReqReturned": true, "SUBMITTED": true, "APPROVED": true, "RETURNED": true}

// noNotice are the functions that may write a guarded state without a notice: giving a request back to APPROVED when its last order was cancelled tells nobody.
var noNotice = map[string]bool{"releaseRequest": true}

func isGuardedStatus(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return guardedStates[v.Name]
	case *ast.BasicLit:
		return v.Kind == token.STRING && guardedStates[strings.Trim(v.Value, `"`)]
	}
	return false
}

// scanChainPaths reports, for one Go file, the functions that write a guarded state of a request and what they lack.
func scanChainPaths(rel string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		writes, audits, notifies := false, false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CompositeLit:
				if sel, ok := v.Type.(*ast.SelectorExpr); ok && (sel.Sel.Name == "SetRequestStateParams" || sel.Sel.Name == "SetRequisitionStateParams") {
					for _, el := range v.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Status" && isGuardedStatus(kv.Value) {
								writes = true
							}
						}
					}
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "Write":
						audits = true
					case "notify", "notifyDoc", "advance", "finishRequest":
						notifies = true
					}
				}
			}
			return true
		})
		if !writes {
			continue
		}
		name := fn.Name.Name
		if rel != "inventory/requests.go" && rel != "inventory/requisitions.go" {
			out = append(out, rel+": "+name+" writes a state of the chain, which only the engine does")
			continue
		}
		if !audits {
			out = append(out, rel+": "+name+" writes a state of the chain without an audit entry")
		}
		if !notifies && !noNotice[name] {
			out = append(out, rel+": "+name+" writes a state of the chain without notices")
		}
	}
	return out, nil
}

func TestOnlyTheEngineWritesTheStatesOfTheChain(t *testing.T) {
	root := repoInternal(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "inventory/inventorydb" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		v, err := scanChainPaths(rel, src)
		if err != nil {
			return err
		}
		violations = append(violations, v...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("the chain of approval is written outside its engine, or without audit and notices:\n  %s", strings.Join(violations, "\n  "))
	}
}

// The meta-test: the guard sees what it is there to see.
func TestTheGuardOfTheChainSeesAnotherWriter(t *testing.T) {
	other := `package pos; func f(q *Q) { q.SetRequestState(nil, inventorydb.SetRequestStateParams{Status: "APPROVED"}) }`
	if v, err := scanChainPaths("pos/other.go", other); err != nil || len(v) == 0 {
		t.Errorf("another package approves a request: %v %v", v, err)
	}
	noAudit := `package inventory; func ApproveX(q *Q) { q.SetRequestState(nil, inventorydb.SetRequestStateParams{Status: ReqApproved}); s.advance() }`
	if v, _ := scanChainPaths("inventory/requests.go", noAudit); len(v) != 1 || !strings.Contains(v[0], "without an audit entry") {
		t.Errorf("a path without audit: %v", v)
	}
	noNotices := `package inventory; func ApproveX(q *Q) { q.SetRequestState(nil, inventorydb.SetRequestStateParams{Status: ReqReturned}); s.audit.Write() }`
	if v, _ := scanChainPaths("inventory/requests.go", noNotices); len(v) != 1 || !strings.Contains(v[0], "without notices") {
		t.Errorf("a path without notices: %v", v)
	}
	whole := `package inventory; func ApproveX(q *Q) { q.SetRequestState(nil, inventorydb.SetRequestStateParams{Status: ReqApproved}); s.audit.Write(); s.notify() }`
	if v, _ := scanChainPaths("inventory/requests.go", whole); len(v) != 0 {
		t.Errorf("a whole path is reported: %v", v)
	}
	draft := `package inventory; func Recall(q *Q) { q.SetRequestState(nil, inventorydb.SetRequestStateParams{Status: ReqDraft}) }`
	if v, _ := scanChainPaths("inventory/requests.go", draft); len(v) != 0 {
		t.Errorf("a state that is not guarded: %v", v)
	}
}
