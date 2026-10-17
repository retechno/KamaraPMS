package inventory_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The structural guard of the stock ledger (docs/architecture/35-inventory.md 5.1).
//
// The ledger (inv_movements) and the tables that follow it (inv_balances, inv_layers, inv_layer_uses, inv_shortfalls) are written by one piece of code, internal/inventory/ledger.go. This
// test reads the source of the whole repository with go/parser (and the .sql files of the queries) and fails any other writer:
//
//   - a call of one of the generated Ledger* queries that write those tables, outside ledger.go;
//   - a string that holds an INSERT, UPDATE, DELETE or TRUNCATE of one of those tables, outside the generated code of inventorydb and the migrations;
//   - a query of a queries.sql that writes one of those tables and is not named Ledger*, or any such query outside internal/inventory;
//   - a use of AllowShortfall (the right to take stock below zero, 4.4) outside the files listed in mayAllowShortfall: the POS deduction and the amenities of the close, which come with
//     their PRs. PR 1 has none.
//
// To add a writer: do not. Add a method to ledger.go. To add a caller of AllowShortfall: add its file to mayAllowShortfall with the reason.

// ledgerWriters are the generated queries that change the ledger tables.
var ledgerWriters = map[string]bool{
	"LedgerEnsureBalance": true, "LedgerUpdateBalance": true, "LedgerInsertMovement": true, "LedgerInsertLayer": true, "LedgerUpdateLayer": true,
	"LedgerInsertLayerUse": true, "LedgerInsertShortfall": true, "LedgerUpdateShortfall": true, "LedgerCloseShortfallsByCount": true,
}

// mayAllowShortfall are the files (relative to internal/inventory) that may call AllowShortfall: the amenities run of the close (PR 3, 5.9) and what it holds over a count; the POS queue (PR 4) adds
// its file here.
var mayAllowShortfall = map[string]string{"amenities.go": "the amenities of the close may take stock below zero (I16, 4.4)"}

var ledgerSQL = regexp.MustCompile(`(?is)\b(insert\s+into|update|delete\s+from|truncate(\s+table)?)\s+(only\s+)?(inv_movements|inv_balances|inv_layers|inv_layer_uses|inv_shortfalls)\b`)

func repoInternal(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// scanGo reports what a Go file does that only the ledger writer may do. rel is its path relative to internal/.
func scanGo(rel string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	inLedger := rel == "inventory/ledger.go"
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if ledgerWriters[v.Sel.Name] && !inLedger {
				out = append(out, rel+": calls "+v.Sel.Name+", which only ledger.go may")
			}
			if v.Sel.Name == "ReturnAt" && rel != "inventory/ledger.go" && rel != "inventory/returns.go" {
				out = append(out, rel+": uses ReturnAt, which only the return to the supplier may (the cost of a receipt line, 4.6)")
			}
			if v.Sel.Name == "AllowShortfall" && !strings.HasPrefix(rel, "inventory/ledger") {
				if _, ok := mayAllowShortfall[strings.TrimPrefix(rel, "inventory/")]; !ok {
					out = append(out, rel+": uses AllowShortfall, which only the POS deduction and the amenities of the close may")
				}
			}
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "ReturnAt" && rel != "inventory/ledger.go" && rel != "inventory/returns.go" {
				out = append(out, rel+": uses ReturnAt, which only the return to the supplier may (the cost of a receipt line, 4.6)")
			}
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "AllowShortfall" && rel != "inventory/ledger.go" {
				if _, ok := mayAllowShortfall[strings.TrimPrefix(rel, "inventory/")]; !ok {
					out = append(out, rel+": uses AllowShortfall, which only the POS deduction and the amenities of the close may")
				}
			}
		case *ast.BasicLit:
			if v.Kind == token.STRING && ledgerSQL.MatchString(v.Value) {
				out = append(out, rel+": writes a ledger table in a string")
			}
		}
		return true
	})
	return out, nil
}

// scanQueries reports a query of a queries.sql that writes a ledger table and is not one of the Ledger* queries of the inventory.
func scanQueries(rel, text string) []string {
	var out []string
	name := ""
	var body []string
	flush := func() {
		if name != "" && ledgerSQL.MatchString(strings.Join(body, "\n")) {
			if rel != "inventory/queries.sql" || !strings.HasPrefix(name, "Ledger") {
				out = append(out, rel+": the query "+name+" writes a ledger table")
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "-- name: "); ok {
			flush()
			name, body = strings.Fields(rest)[0], nil
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			body = append(body, line)
		}
	}
	flush()
	return out
}

func TestTheLedgerIsWrittenByLedgerGoOnly(t *testing.T) {
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
		switch {
		case strings.HasSuffix(path, "_test.go"):
		case strings.HasSuffix(path, ".go"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			v, err := scanGo(rel, src)
			if err != nil {
				return err
			}
			violations = append(violations, v...)
		case strings.HasSuffix(path, ".sql"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			violations = append(violations, scanQueries(rel, string(src))...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// and the commands
	cmd, _ := filepath.Abs("../../cmd")
	_ = filepath.WalkDir(cmd, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(cmd, path)
		v, _ := scanGo("cmd/"+filepath.ToSlash(rel), src)
		violations = append(violations, v...)
		return nil
	})
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("the ledger is written outside ledger.go:\n  %s", strings.Join(violations, "\n  "))
	}
}

// The meta-test: the guard sees what it is there to see.
func TestTheGuardOfTheLedgerSeesAnotherWriter(t *testing.T) {
	cases := map[string]string{
		"pos/other.go":       `package pos; func f(q *Q) { q.LedgerInsertMovement(nil, nil) }`,
		"pos/sql.go":         "package pos\nconst s = `UPDATE inv_balances SET qty = 0`",
		"reports/sql.go":     "package reports\nconst s = `delete from inv_layers`",
		"app/shortfall.go":   `package app; func f() { inventory.AllowShortfall(a, "POS_SALE") }`,
		"inventory/other.go": `package inventory; func f(q *Q) { q.LedgerUpdateBalance(nil, nil) }`,
		"inventory/bills.go": `package inventory; func f() { ReturnAt(a, 1, q, v) }`,
	}
	for rel, src := range cases {
		v, err := scanGo(rel, src)
		if err != nil || len(v) == 0 {
			t.Errorf("%s: the guard did not see the writer (%v, %v)", rel, v, err)
		}
	}
	if v, _ := scanGo("inventory/ledger.go", `package inventory; func f(q *Q) { q.LedgerInsertMovement(nil, nil); q.LedgerUpdateBalance(nil, nil) }`); len(v) != 0 {
		t.Errorf("ledger.go is the writer: %v", v)
	}
	if v, _ := scanGo("pos/read.go", "package pos\nconst s = `SELECT * FROM inv_balances`"); len(v) != 0 {
		t.Errorf("a read is not a write: %v", v)
	}
	if v := scanQueries("pos/queries.sql", "-- name: X :exec\nUPDATE inv_balances SET qty = 0;\n"); len(v) == 0 {
		t.Error("a query outside the inventory that writes the ledger")
	}
	if v := scanQueries("inventory/queries.sql", "-- name: ItemHasStock :one\nDELETE FROM inv_layers;\n"); len(v) == 0 {
		t.Error("a query of the inventory that writes the ledger and is not a Ledger* query")
	}
	if v := scanQueries("inventory/queries.sql", "-- name: LedgerUpdateLayer :exec\nUPDATE inv_layers SET qty_remaining = 0;\n"); len(v) != 0 {
		t.Errorf("a Ledger* query is the writer: %v", v)
	}
}
