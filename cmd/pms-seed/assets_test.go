package main

import (
	"bytes"
	"strings"
	"testing"

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
	day, err := e.Tenancy.CurrentBusinessDay(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedAssetsWith(ctx, svc, p.ID, day.BusinessDate, dry, &out)
		if err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		return n, out.String()
	}

	n, out := run(true)
	if n != len(assetAreas)+len(assetSpecs) || !strings.Contains(out, "dry run") {
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

	again, out := run(false)
	if again != 0 || !strings.Contains(out, "opening balances exist") {
		t.Fatalf("second run made %d: %s", again, out)
	}
	page, _ = svc.ListAssets(ctx, p.ID, assets.AssetFilter{Limit: 100})
	if len(page.Data) != len(assetSpecs) {
		t.Fatalf("after the second run %d assets", len(page.Data))
	}
}
