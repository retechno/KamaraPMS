package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/pos"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

type catFx struct {
	*roomstest.Env
	svc *pos.Service
	ctx context.Context
	pid int64
}

func newCatFx(t *testing.T) *catFx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	return &catFx{Env: e, ctx: roomstest.Admin(tn.ID), pid: p.ID,
		svc: pos.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Departments)}
}

// classes reads the class of every category by code ("" is none).
func (f *catFx) classes(t *testing.T) map[string]string {
	t.Helper()
	cats, err := f.svc.ListCategories(f.ctx, f.pid)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range cats {
		out[c.Code] = ""
		if c.FbClass != nil {
			out[c.Code] = *c.FbClass
		}
	}
	return out
}

func (f *catFx) seed(t *testing.T, dryRun bool) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := seedPosCategories(f.ctx, f.svc, f.pid, dryRun, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func (f *catFx) updates(t *testing.T) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1 AND action = 'pos.category_updated'`, f.pid)
}

func (f *catFx) audits(t *testing.T) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1`, f.pid)
}

func wantClasses(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("categories %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("category %s has class %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

var demoClasses = map[string]string{"MAINS": "FOOD", "DRINKS": "BEVERAGE", "LAUNDRY": "OTHER"}

// A new database gets the categories with the class of what they sell, so the class follows the product in every outlet.
func TestANewDatabaseGetsTheDemoCategoriesWithTheirClass(t *testing.T) {
	f := newCatFx(t)
	f.seed(t, false)
	wantClasses(t, f.classes(t), demoClasses)
}

// A category of an older seed has no class: the seed gives it one, through the service, so the audit trail has the change.
func TestACategoryWithoutAClassGetsItAndIsAudited(t *testing.T) {
	f := newCatFx(t)
	for i, c := range posCategories {
		if _, err := f.svc.CreateCategory(f.ctx, f.pid, pos.CategoryInput{Code: c.code, Name: c.name, SortOrder: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	wantClasses(t, f.classes(t), map[string]string{"MAINS": "", "DRINKS": "", "LAUNDRY": ""})
	f.seed(t, false)
	wantClasses(t, f.classes(t), demoClasses)
	if n := f.updates(t); n != 3 {
		t.Fatalf("%d category updates in the audit trail, want 3", n)
	}
	cats, err := f.svc.ListCategories(f.ctx, f.pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats { // the name and the order stay as they were
		if c.Name == "" || !c.IsActive {
			t.Fatalf("category %+v lost its name or became inactive", c)
		}
	}
}

// A class that is already set is the choice of a person: the seed leaves it and says so.
func TestAClassThatIsAlreadySetIsNotOverwritten(t *testing.T) {
	f := newCatFx(t)
	beverage, other := "BEVERAGE", "OTHER"
	if _, err := f.svc.CreateCategory(f.ctx, f.pid, pos.CategoryInput{Code: "MAINS", Name: "Makanan utama", SortOrder: 1, FbClass: &beverage}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateCategory(f.ctx, f.pid, pos.CategoryInput{Code: "DRINKS", Name: "Minuman", SortOrder: 2, FbClass: &other}); err != nil {
		t.Fatal(err)
	}
	out := f.seed(t, false)
	wantClasses(t, f.classes(t), map[string]string{"MAINS": "BEVERAGE", "DRINKS": "OTHER", "LAUNDRY": "OTHER"})
	if !strings.Contains(out, "category MAINS exists: warning, class BEVERAGE kept") || !strings.Contains(out, "category DRINKS exists: warning, class OTHER kept") {
		t.Fatalf("no warning for the classes that were kept:\n%s", out)
	}
	if n := f.updates(t); n != 0 {
		t.Fatalf("%d category updates, want none", n)
	}
}

// Running the seed again changes nothing, not even the audit trail; a dry run shows what it would fill and writes nothing.
func TestRunningTheSeedTwiceChangesNothingAndADryRunWritesNothing(t *testing.T) {
	f := newCatFx(t)
	out := f.seed(t, true)
	if len(f.classes(t)) != 0 || !strings.Contains(out, "category MAINS: to create (class FOOD)") {
		t.Fatalf("a dry run on a new database: %v\n%s", f.classes(t), out)
	}

	f.seed(t, false)
	before := f.audits(t)

	out = f.seed(t, false)
	wantClasses(t, f.classes(t), demoClasses)
	if after := f.audits(t); after != before {
		t.Fatalf("the second run wrote %d audit rows", after-before)
	}
	if strings.Contains(out, "to create") || strings.Contains(out, "to set") || strings.Contains(out, "warning") {
		t.Fatalf("the second run says it changes something:\n%s", out)
	}

	// the dry run of a database with a category that has no class names it and still writes nothing
	f2 := newCatFx(t)
	if _, err := f2.svc.CreateCategory(f2.ctx, f2.pid, pos.CategoryInput{Code: "MAINS", Name: "Makanan utama", SortOrder: 1}); err != nil {
		t.Fatal(err)
	}
	out = f2.seed(t, true)
	if !strings.Contains(out, "category MAINS exists: class FOOD to set") {
		t.Fatalf("the dry run does not name the category to fill:\n%s", out)
	}
	wantClasses(t, f2.classes(t), map[string]string{"MAINS": ""})
}
