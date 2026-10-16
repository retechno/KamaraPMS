package inventory_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"kamarapms/internal/groups"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The test that holds the shared level of the stock (docs/architecture/35-inventory.md 5.1, D12).
//
// LevelStock is 45, the level of the booking groups: a booking never touches stock, and stock never touches a booking. That is true only as long as nobody writes a use case that
// does both, so the test has two parts:
//
//  (a) static: the source of groups, reservations, frontdesk and inventory is read with go/parser; a function that locks the groups, directly or through the functions of its own
//      package it calls, must not reach a lock on a stock table, and the other way round;
//  (b) dynamic: the use cases of the stock and a use case of the groups run once under a lock trace (db.WithLockTrace); no trace holds both a group and a stock table, no trace
//      descends in level, and inside the stock level the order is documents, warehouse, balances.

var stockTables = map[string]bool{"StockOpenings": true, "Warehouses": true, "StockBalances": true}

// lockKinds reads a package directory and returns, for each function (by name), whether it locks groups or stock directly and which functions of the package it calls.
type lockGraph struct {
	groups map[string]bool
	stock  map[string]bool
	calls  map[string]map[string]bool
}

func readLocks(t *testing.T, dir, pkg string, g *lockGraph) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := pkg + "." + fn.Name.Name
			if g.calls[name] == nil {
				g.calls[name] = map[string]bool{}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := v.X.(*ast.Ident); ok && x.Name == "db" {
						if v.Sel.Name == "Groups" {
							g.groups[name] = true
						}
						if stockTables[v.Sel.Name] {
							g.stock[name] = true
						}
					}
				case *ast.CallExpr:
					switch c := v.Fun.(type) {
					case *ast.Ident:
						g.calls[name][pkg+"."+c.Name] = true
					case *ast.SelectorExpr: // a method of the same package, called by its name
						g.calls[name][pkg+"."+c.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
}

// reaches reports the functions reachable from name, itself included.
func (g *lockGraph) reaches(name string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for c := range g.calls[n] {
			walk(c)
		}
	}
	walk(name)
	return seen
}

func (g *lockGraph) mixed() []string {
	var out []string
	for fn := range g.calls {
		var gr, st bool
		for r := range g.reaches(fn) {
			gr = gr || g.groups[r]
			st = st || g.stock[r]
		}
		if gr && st {
			out = append(out, fn)
		}
	}
	sort.Strings(out)
	return out
}

func TestNoUseCaseLocksBothAGroupAndStock(t *testing.T) {
	root := repoInternal(t)
	g := &lockGraph{groups: map[string]bool{}, stock: map[string]bool{}, calls: map[string]map[string]bool{}}
	for _, pkg := range []string{"groups", "reservations", "frontdesk", "inventory"} {
		readLocks(t, filepath.Join(root, pkg), pkg, g)
	}
	if len(g.groups) == 0 || len(g.stock) == 0 {
		t.Fatalf("the test sees no lock on a group (%d) or on stock (%d): it reads nothing", len(g.groups), len(g.stock))
	}
	if mixed := g.mixed(); len(mixed) > 0 {
		t.Fatalf("a use case locks both the booking groups and the stock (they share level 45, D12): %s\nGive the stock its own level, and renumber the levels above it", strings.Join(mixed, ", "))
	}
}

// The meta-test: a function that does both is found.
func TestTheStaticLockTestSeesAFunctionThatMixesBoth(t *testing.T) {
	dir := t.TempDir()
	src := `package x
import "kamarapms/internal/platform/db"
func A() { db.LockRows(nil, db.Groups, db.ForUpdate, 1, nil); helper() }
func helper() { db.LockRows(nil, db.StockBalances, db.ForUpdate, 1, nil) }
func B() { db.LockRows(nil, db.StockBalances, db.ForUpdate, 1, nil) }
`
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &lockGraph{groups: map[string]bool{}, stock: map[string]bool{}, calls: map[string]map[string]bool{}}
	readLocks(t, dir, "x", g)
	if mixed := g.mixed(); len(mixed) != 1 || mixed[0] != "x.A" {
		t.Fatalf("mixed: %v", mixed)
	}
}

// checkTrace holds the rules of 5.1 on the locks one transaction took.
func checkTrace(t *testing.T, label string, events []db.LockEvent) {
	t.Helper()
	var group, stock bool
	level, rank := db.LevelNone, 0
	ranks := map[string]int{"inv_openings": 1, "inv_warehouses": 2, "inv_balances": 3}
	for _, e := range events {
		if e.Level < level {
			t.Errorf("%s: the level fell from %d to %d (%s)", label, level, e.Level, e.Table)
		}
		if e.Level > level {
			rank = 0
		}
		level = e.Level
		if e.Table == "booking_groups" {
			group = true
		}
		if r, ok := ranks[e.Table]; ok {
			stock = true
			if r < rank {
				t.Errorf("%s: %s taken after a later table of the stock level", label, e.Table)
			}
			rank = r
		}
	}
	if group && stock {
		t.Errorf("%s: the transaction locked a group and stock", label)
	}
}

func TestEveryUseCaseTakesItsLocksInOrderAndNeverAGroupWithStock(t *testing.T) {
	f := newFx(t, "FIFO", 0)
	// the opening stock: business day, warehouse, balances, then the document number
	ctx, trace := db.WithLockTrace(f.admin)
	_, err := f.svc.PostOpening(ctx, f.propID, f.opening("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", line(f.item["BERAS"], f.unit["kg"], "10", "1000"), line(f.item["SIRUP"], f.unit["botol"], "5", "2000")))
	must(t, err)
	ev := trace.Events()
	checkTrace(t, "opening", ev)
	var tables []string
	for _, e := range ev {
		if e.Table != "" {
			tables = append(tables, e.Table)
		}
	}
	if strings.Join(tables, ",") != "inv_warehouses,inv_balances" {
		t.Errorf("the opening locks %v, want the warehouse and then the balances", tables)
	}
	if last := ev[len(ev)-1]; last.Level != db.LevelSequences {
		t.Errorf("the document number comes last: %+v", last)
	}

	// a movement of two pairs, taken in one call, in ascending order whichever way they are named
	for _, pairs := range [][]inventory.Pair{{f.pair("MAIN", "SIRUP"), f.pair("BAR", "BERAS")}, {f.pair("BAR", "BERAS"), f.pair("MAIN", "SIRUP")}} {
		ctx, trace = db.WithLockTrace(f.admin)
		must(t, f.TxM.WithinTx(ctx, func(ctx context.Context) error {
			day, err := f.Tenancy.RequireOpenBusinessDay(ctx, f.propID, db.ForShare, nil)
			if err != nil {
				return err
			}
			_, err = f.ledger.Lock(ctx, inventorydbOf(f, ctx), inventory.Book{TenantID: f.tenantID, PropertyID: f.propID, Date: day.BusinessDate, Decimals: 0, Method: "FIFO"}, pairs, inventory.LockOptions{})
			return err
		}))
		checkTrace(t, "lock", trace.Events())
	}

	// a use case of the groups
	ctx, trace = db.WithLockTrace(f.admin)
	g, err := f.Groups.Create(ctx, f.propID, groups.Input{Code: "CONF", Name: "Conference", ArrivalDate: civil.MustParseDate("2026-10-10"), DepartureDate: civil.MustParseDate("2026-10-12"), IsActive: true})
	must(t, err)
	name := "Conference 2"
	_, err = f.Groups.Update(ctx, f.propID, g.ID, groups.Patch{Name: &name})
	must(t, err)
	checkTrace(t, "groups", trace.Events())
	var sawGroup bool
	for _, e := range trace.Events() {
		sawGroup = sawGroup || e.Table == "booking_groups"
	}
	if !sawGroup {
		t.Error("the trace of the group use case shows no lock on a group: the recorder reads nothing")
	}

	// the close of the day takes the stock level before the accounting settings
	ctx, trace = db.WithLockTrace(f.admin)
	must(t, f.TxM.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := f.Tenancy.RequireOpenBusinessDay(ctx, f.propID, db.ForUpdate, nil); err != nil {
			return err
		}
		if err := f.svc.CloseStock(ctx, adminPrincipal(f), f.propID, civil.MustParseDate("2026-09-30")); err != nil {
			return err
		}
		return db.EnterLockLevel(ctx, db.LevelAccounting)
	}))
	checkTrace(t, "close", trace.Events())
}

// A dynamic check of the check: a trace that descends is reported.
func TestTheDynamicLockTestSeesADescentAndAMix(t *testing.T) {
	events := []db.LockEvent{{Table: "inv_balances", Level: db.LevelStock}, {Table: "inv_warehouses", Level: db.LevelStock}}
	if !traceBroken(events) {
		t.Error("the warehouse after the balances is not seen")
	}
	events = []db.LockEvent{{Table: "booking_groups", Level: db.LevelGroups}, {Table: "inv_balances", Level: db.LevelStock}}
	if !traceBroken(events) {
		t.Error("a group and stock in one transaction is not seen")
	}
	events = []db.LockEvent{{Level: db.LevelSequences}, {Table: "inv_balances", Level: db.LevelStock}}
	if !traceBroken(events) {
		t.Error("a descent from 50 to 45 is not seen")
	}
	events = []db.LockEvent{{Level: db.LevelBusinessDay}, {Table: "inv_warehouses", Level: db.LevelStock}, {Table: "inv_balances", Level: db.LevelStock}, {Level: db.LevelSequences}}
	if traceBroken(events) {
		t.Error("the order of an opening is reported as broken")
	}
}

// traceBroken is checkTrace without the testing.T: it says whether the trace breaks a rule.
func traceBroken(events []db.LockEvent) bool {
	var group, stock bool
	level, rank := db.LevelNone, 0
	ranks := map[string]int{"inv_openings": 1, "inv_warehouses": 2, "inv_balances": 3}
	for _, e := range events {
		if e.Level < level {
			return true
		}
		if e.Level > level {
			rank = 0
		}
		level = e.Level
		group = group || e.Table == "booking_groups"
		if r, ok := ranks[e.Table]; ok {
			stock = true
			if r < rank {
				return true
			}
			rank = r
		}
	}
	return group && stock
}
