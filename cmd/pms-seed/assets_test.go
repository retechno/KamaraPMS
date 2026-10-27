package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/rooms/roomstest"
)

func TestSeedAssets(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.PropertyIn(t, tn.ID, "BALI", "IDR", 0)
	ctx := roomstest.Admin(tn.ID)
	svc := assets.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Accounting, e.Departments)
	e.Accounting.SetAssetSource(svc)
	standardDepartments(t, e, tn.ID, p.ID)
	deps := &seedDeps{pay: e.Payables, txm: e.TxM, days: e.Tenancy, depts: e.Departments}
	day, err := e.Tenancy.CurrentBusinessDay(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// the books began long before the business date, so that the opening balances can be dated their month end
	if _, err := e.Pool.Exec(ctx, `UPDATE accounting_settings SET start_date = $2 WHERE property_id = $1`, p.ID, day.BusinessDate.AddDays(-90)); err != nil {
		t.Fatal(err)
	}
	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedAssetsWith(ctx, svc, e.Accounting, deps, p.ID, day.BusinessDate, dry, &out)
		if err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		return n, out.String()
	}

	n, out := run(true)
	if n != len(assetAreas)+len(assetSpecs)+waitingRows || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %d %q", n, out)
	}
	if locs, _ := svc.ListLocations(ctx, p.ID, "", nil); len(locs) != 0 {
		t.Fatalf("a dry run made %d locations", len(locs))
	}
	if imps, _ := svc.ListOpeningImports(ctx, p.ID); len(imps) != 0 {
		t.Fatalf("a dry run made %d imports", len(imps))
	}

	created, _ := run(false)
	if created != n {
		t.Fatalf("created %d, the dry run said %d", created, n)
	}
	page, err := svc.ListAssets(ctx, p.ID, assets.AssetFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != len(assetSpecs) {
		t.Fatalf("%d assets, want %d", len(page.Data), len(assetSpecs))
	}
	settings, _ := svc.GetSettings(ctx, p.ID)
	if settings.RegisterFrom == nil {
		t.Fatal("the import did not start the register")
	}

	// every demo asset has a department but the laptop of the front office, whose month of depreciation therefore shows the blocker
	for _, a := range page.Data {
		if (a.DepartmentID == nil) != (a.Name == "Laptop front office" || a.CategoryCode == "LAND" || a.CategoryCode == "CIP") {
			t.Errorf("%s: department %v", a.Name, a.DepartmentID)
		}
	}

	// the waiting list: a line of each way a cost reaches the ledger
	wl, err := svc.ListWaiting(ctx, p.ID, assets.WaitingFilter{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, l := range wl.Data {
		seen[l.Source]++
	}
	if len(wl.Data) != waitingRows || seen[assets.WaitingBill] != 2 || seen[assets.WaitingCashier] != 1 || seen[assets.WaitingBank] != 1 || seen[assets.WaitingManual] != 1 {
		t.Fatalf("waiting list %d lines: %v", len(wl.Data), seen)
	}

	// the register-only demo: the plot that only the ledger holds ties out, any other file does not
	settingsNow, _ := svc.GetSettings(ctx, p.ID)
	asOf := settingsNow.RegisterFrom.AddDays(-1)
	inService := asOf.AddDays(-400)
	noJournal := false
	try := func(cost string) assets.OpeningResult {
		res, err := svc.ImportOpening(ctx, p.ID, assets.OpeningInput{AsOf: asOf, PostJournal: &noJournal, DryRun: true,
			Rows: []assets.OpeningRow{{Name: "Tanah parkir", Category: "LAND", Cost: decimal.RequireFromString(cost), InServiceDate: &inService}}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := try("750000000"); !res.Valid {
		t.Fatalf("the plot of the register-only demo does not tie out: %+v %+v asOf %s", res.Errors, res.GLCheck, asOf)
	}
	if res := try("750000001"); res.Valid {
		t.Fatal("a file that differs from the ledger tied out")
	}

	again, out := run(false)
	if again != 0 || !strings.Contains(out, "opening balances exist") {
		t.Fatalf("second run made %d: %s", again, out)
	}
	page, _ = svc.ListAssets(ctx, p.ID, assets.AssetFilter{Limit: 100})
	if len(page.Data) != len(assetSpecs) {
		t.Fatalf("after the second run %d assets", len(page.Data))
	}
}
