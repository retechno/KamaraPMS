package reservations_test

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

// The structural guard of the sale paths (audit F-09, docs/architecture/18-architecture-decisions.md section 4.7).
//
// A sale is a use case that creates or changes the nights a guest is promised. Architecture 18 says every sale asks the one evaluator (availability.EvaluateStay, through requireSellable
// in this package and RequireSellableStay for the front desk). The behavioural test TestEverySalePathAsksTheSalesRestrictions proves it for the paths that are known; this test is
// what makes a NEW path fail until it either asks or says why it need not:
//
//   - the source of the two packages is read with go/parser (no list of the sale paths is kept: a path is found by what it writes);
//   - a use case (an exported method of Service) that, directly or through the helpers of its package, writes a room line or confirms a reservation must also reach a gate,
//     or be named in notASale with the reason (Architecture 18 section 4.7 is the source of the reasons);
//   - the front desk is read the same way: a method that lengthens a stay (ExtendNights) must reach RequireSellableStay.
//
// To add a sale path: call s.requireSellable(...) (or s.RequireSellableStay from another package) with what is NEW in the operation, and add a case to
// TestEverySalePathAsksTheSalesRestrictions. To add a path that writes lines but sells nothing (it cancels, assigns a room, changes a guest): add it to notASale below with the reason.
// There is nothing to do for a method that does not write a room line (a read, a report, a search).

type graph struct {
	calls map[string]map[string]bool // function name -> names it calls
	// exported is the set of exported methods of Service
	exported map[string]bool
}

// parseGraph reads the non-test files of a directory (or the replacement given for a file name) into a call graph by function name. A call is a selector or an identifier call: the
// graph knows names, not types, which is enough because each of the two packages has one Service and no two functions share a name.
func parseGraph(t *testing.T, dir string, replace map[string]string) graph {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	g := graph{calls: map[string]map[string]bool{}, exported: map[string]bool{}}
	for name := range replace { // a file that does not exist yet (the meta-test adds one)
		found := false
		for _, e := range entries {
			found = found || e.Name() == name
		}
		if !found {
			entries = append(entries, fakeEntry(name))
		}
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		var src any
		if text, ok := replace[name]; ok {
			src = text
		} else {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			src = string(b)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			set := g.calls[fd.Name.Name]
			if set == nil {
				set = map[string]bool{}
				g.calls[fd.Name.Name] = set
			}
			if fd.Recv != nil && fd.Name.IsExported() {
				g.exported[fd.Name.Name] = true
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					set[fun.Sel.Name] = true
				case *ast.Ident:
					set[fun.Name] = true
				}
				return true
			})
		}
	}
	return g
}

// reaches says whether fn, directly or through functions of the package, calls one of the targets.
func (g graph) reaches(fn string, targets map[string]bool) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		for callee := range g.calls[name] {
			if targets[callee] || walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(fn)
}

type fakeEntry string

func (f fakeEntry) Name() string               { return string(f) }
func (f fakeEntry) IsDir() bool                { return false }
func (f fakeEntry) Type() os.FileMode          { return 0 }
func (f fakeEntry) Info() (os.FileInfo, error) { return nil, nil }

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// guardConfig says, for one package, what writes the nights, what is the gate, and what is known not to sell.
type guardConfig struct {
	writers  map[string]bool
	gates    map[string]bool
	notASale map[string]string // exported method -> why it sells nothing
}

// violations are the use cases that write nights and neither reach a gate nor are named as not a sale.
func (c guardConfig) violations(g graph) []string {
	var out []string
	for name := range g.exported {
		if !g.reaches(name, c.writers) || g.reaches(name, c.gates) {
			continue
		}
		if _, ok := c.notASale[name]; ok {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Architecture 18 section 4.7: what is not a sale.
var reservationsGuard = guardConfig{
	writers: set("InsertLine", "UpdateLine", "ConfirmReservation", "InsertReservation"),
	gates:   set("requireSellable", "RequireSellableStay", "EvaluateStay"),
	notASale: map[string]string{
		"Cancel":              "a cancellation releases nights, it sells none",
		"CancelLine":          "a cancellation releases nights, it sells none",
		"NoShow":              "a no-show releases nothing new and sells none",
		"BulkNoShow":          "a no-show releases nothing new and sells none",
		"AssignRoom":          "operational: the product sold does not change (Architecture 18 section 4.7)",
		"UnassignRoom":        "operational: the product sold does not change (Architecture 18 section 4.7)",
		"MarkCheckedIn":       "the stay lifecycle: the night was sold when the reservation was",
		"MarkCheckInReversed": "the stay lifecycle: the night was sold when the reservation was",
		"MarkCompleted":       "the stay lifecycle: nothing is sold by the check-out",
		"Lock":                "locks the reservation for a caller; writes nothing",
	},
}

var frontdeskGuard = guardConfig{
	// lengthening the nights of a stay in house (ChangeDeparture, through extend)
	// (TrimNights shortens and OverrideNights reprices nights already sold: neither sells a night, which is why Move, CheckOut and the rate change need no gate)
	writers:  set("ExtendNights"),
	gates:    set("RequireSellableStay"),
	notASale: map[string]string{},
}

func TestEverySalePathReachesTheRestrictionGate(t *testing.T) {
	if v := reservationsGuard.violations(parseGraph(t, ".", nil)); len(v) != 0 {
		t.Fatalf("these use cases of internal/reservations write room lines and neither ask the sales restrictions nor are named as not a sale: %v. Call requireSellable with what is new in the operation, or add them to notASale with the reason.", v)
	}
	if v := frontdeskGuard.violations(parseGraph(t, "../frontdesk", nil)); len(v) != 0 {
		t.Fatalf("these use cases of internal/frontdesk change the nights of a stay and do not ask RequireSellableStay: %v", v)
	}
}

// The gate must still reach the evaluator: a gate that no longer asks anything would let every test above pass.
func TestTheGatesStillAskTheEvaluator(t *testing.T) {
	g := parseGraph(t, ".", nil)
	if !g.reaches("requireSellable", set("EvaluateStay")) {
		t.Fatal("requireSellable no longer calls availability.EvaluateStay")
	}
	if !g.reaches("RequireSellableStay", set("requireSellable")) {
		t.Fatal("RequireSellableStay no longer calls requireSellable")
	}
}

// The guard has teeth: take the real source, remove the gate from a known sale path, and the guard names the path. The paths that are checked here are the ones of Architecture 18
// section 4.7 table "Where it is called".
func TestTheGuardNamesASalePathThatBypassesTheGate(t *testing.T) {
	read := func(dir, file string) string {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct {
		name, dir, file, remove string
		want                    []string
		cfg                     guardConfig
	}{
		{"create and walk-in", ".", "create.go", "s.requireSellable(", []string{"Create", "CreateHeld"}, reservationsGuard},
		{"confirm", ".", "lifecycle.go", "s.requireSellable(", []string{"Confirm", "Reinstate"}, reservationsGuard},
		{"add a line", ".", "lines.go", "s.requireSellable(", []string{"AddLine", "AmendLine"}, reservationsGuard},
		{"extend a stay", "../frontdesk", "stayops.go", "s.res.RequireSellableStay(", []string{"ChangeDeparture"}, frontdeskGuard},
	}
	for _, c := range cases {
		src := read(c.dir, c.file)
		if !strings.Contains(src, c.remove) {
			t.Fatalf("%s: %s no longer contains %q: update the guard test", c.name, c.file, c.remove)
		}
		broken := strings.ReplaceAll(src, c.remove, "s.noGate(")
		got := c.cfg.violations(parseGraph(t, c.dir, map[string]string{c.file: broken}))
		for _, w := range c.want {
			found := false
			for _, g := range got {
				found = found || g == w
			}
			if !found {
				t.Errorf("%s: without the gate in %s the guard must name %s, it named %v", c.name, c.file, w, got)
			}
		}
	}
	// and a new use case that writes a line and asks nothing is named
	extra := `package reservations
func (s *Service) SellForFree() { s.q().InsertLine() }
`
	got := reservationsGuard.violations(parseGraph(t, ".", map[string]string{"zz_new.go": extra}))
	if len(got) != 1 || got[0] != "SellForFree" {
		t.Errorf("a new sale path without the gate must be named, got %v", got)
	}
}
