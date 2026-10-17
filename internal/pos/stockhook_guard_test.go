package pos

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

// The structural guard of the stock hook (docs/architecture/35-inventory.md 5.6, 13 PR 4): a sale reaches the stock queue from every path of the POS that posts a line to the ledger, and a void from every path
// that reverses one. A function of the POS that calls PostOutletLine must call queueSale (sendDrafts is the one that does), and one that calls VoidOutletItem must call queueVoid; a new path that posts or
// voids a line without telling the stock fails here.

func scanStockHook(rel string, src any) ([]string, error) {
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
		calls := map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
					calls[sel.Sel.Name] = true
				}
			}
			return true
		})
		if calls["PostOutletLine"] && !calls["queueSale"] {
			out = append(out, rel+": "+fn.Name.Name+" posts a line to the ledger without telling the stock (queueSale)")
		}
		if calls["VoidOutletItem"] && !calls["queueVoid"] {
			out = append(out, rel+": "+fn.Name.Name+" voids a line without telling the stock (queueVoid)")
		}
	}
	return out, nil
}

func TestEveryPathThatPostsOrVoidsALineTellsTheStock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		v, err := scanStockHook(e.Name(), src)
		if err != nil {
			t.Fatal(err)
		}
		violations = append(violations, v...)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("the POS posts or voids lines without the hook of the stock:\n  %s", strings.Join(violations, "\n  "))
	}
}

// The meta-test: the guard sees a path that forgets the hook.
func TestTheGuardOfTheStockHookSeesAPathThatForgetsIt(t *testing.T) {
	post := `package pos; func (s *Service) quickSale() { s.ledger.PostOutletLine(nil) }`
	if v, err := scanStockHook("x.go", post); err != nil || len(v) != 1 {
		t.Errorf("a path that posts a line and forgets the stock: %v %v", v, err)
	}
	void := `package pos; func (s *Service) quickVoid() { s.ledger.VoidOutletItem(nil) }`
	if v, err := scanStockHook("x.go", void); err != nil || len(v) != 1 {
		t.Errorf("a path that voids a line and forgets the stock: %v %v", v, err)
	}
	ok := `package pos; func (s *Service) sendDrafts() { s.ledger.PostOutletLine(nil); s.queueSale() }`
	if v, err := scanStockHook("x.go", ok); err != nil || len(v) != 0 {
		t.Errorf("a path that tells the stock is fine: %v %v", v, err)
	}
}
