package assets_test

import (
	"context"
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

func TestFiscalAccumulated(t *testing.T) {
	// 10.15: the group G1 (4 years, 25% a year) writes 36,000,000 off in 48 months
	rate := assets.FiscalRate{LifeYears: 4, SLRate: dec("25")}
	got, months := assets.FiscalAccumulated(dec("36000000"), rate, "SL", d("2020-01-15"), d("2026-09-30"), 1, 0)
	if !got.Equal(dec("36000000")) || months != 48 {
		t.Fatalf("got %s over %d months", got, months)
	}
	got, months = assets.FiscalAccumulated(dec("36000000"), rate, "SL", d("2026-01-15"), d("2026-03-31"), 1, 0)
	if months != 3 || !got.Equal(dec("2250000")) {
		t.Fatalf("got %s over %d months", got, months)
	}
}

func TestSLAccumulated(t *testing.T) {
	got := assets.SLAccumulated(dec("36000000"), dec("0"), 96, 81, 0)
	if !got.Equal(dec("30375000")) {
		t.Fatalf("got %s", got)
	}
}

func TestCategoriesAndLocations(t *testing.T) {
	f := newFx(t)
	cats, err := f.svc.ListCategories(f.admin, f.propID)
	must(t, err)
	if len(cats) != 7 {
		t.Fatalf("%d default categories, want 7", len(cats))
	}
	var ffe assets.Category
	for _, c := range cats {
		if c.Code == "FFE" {
			ffe = c
		}
	}
	// a used category cannot be switched off or lose its accounts
	_, err = f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	in := assets.CategoryInput{Code: ffe.Code, Name: ffe.Name, Kind: "OTHER", CostAccountID: ffe.CostAccountID, AccumulatedAccountID: ffe.AccumulatedAccountID, ExpenseAccountID: ffe.ExpenseAccountID,
		Method: ffe.Method, LifeMonths: ffe.LifeMonths, FiscalGroup: ffe.FiscalGroup, FiscalMethod: ffe.FiscalMethod, Version: ffe.Version}
	_, err = f.svc.UpdateCategory(f.admin, f.propID, ffe.ID, in)
	wantCode(t, err, "CATEGORY_IN_USE")

	loc, err := f.svc.CreateLocation(f.admin, f.propID, assets.LocationInput{Code: "LOBBY", Name: "Lobby"})
	must(t, err)
	if loc.Kind != "AREA" || loc.LabelCode == "" {
		t.Fatalf("%+v", loc)
	}
}

func TestSettingsRegisterFromIsLockedByAssets(t *testing.T) {
	f := newFx(t)
	s, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if s.RegisterFrom != nil {
		t.Fatalf("register_from set before any import: %v", s.RegisterFrom)
	}
	_, err = f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	s, err = f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if s.RegisterFrom == nil || s.RegisterFrom.String() != "2026-10-01" {
		t.Fatalf("register_from %v", s.RegisterFrom)
	}
}

func TestCancelAndRebuild(t *testing.T) {
	f := newFx(t)
	_, err := f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 12))
	must(t, err)
	page, err := f.svc.ListAssets(f.admin, f.propID, assets.AssetFilter{Limit: 10})
	must(t, err)
	if page.Total != 1 || !page.Summary.Cost.Equal(dec("10000000")) {
		t.Fatalf("%+v", page)
	}
	r, err := f.svc.RebuildState(f.admin, f.propID)
	must(t, err)
	if r.Checked != 1 || r.Repaired != 0 {
		t.Fatalf("%+v", r)
	}
	// drift: a hand edit of the state is found and repaired from the ledger
	_, err = f.Pool.Exec(context.Background(), `ALTER TABLE fa_assets DISABLE TRIGGER USER`)
	must(t, err)
	_, err = f.Pool.Exec(context.Background(), `UPDATE fa_assets SET accum_depr = 1 WHERE property_id = $1`, f.propID)
	_, _ = f.Pool.Exec(context.Background(), `ALTER TABLE fa_assets ENABLE TRIGGER USER`)
	must(t, err)
	r, err = f.svc.RebuildState(f.admin, f.propID)
	must(t, err)
	if r.Repaired != 1 {
		t.Fatalf("%+v", r)
	}
	// nothing has depreciated in the register yet, so the opening error can be cancelled once
	d1, err := f.svc.CancelAsset(f.admin, f.propID, page.Data[0].ID, assets.CancelInput{Reason: "entered twice"})
	must(t, err)
	if d1.Status != "CANCELLED" {
		t.Fatalf("status %s", d1.Status)
	}
	_, err = f.svc.CancelAsset(f.admin, f.propID, page.Data[0].ID, assets.CancelInput{Reason: "again"})
	wantCode(t, err, "ASSET_STATUS_INVALID")
}

func TestUpdateAssetChecksTheVersion(t *testing.T) {
	f := newFx(t)
	_, err := f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	page, err := f.svc.ListAssets(f.admin, f.propID, assets.AssetFilter{})
	must(t, err)
	a := page.Data[0]
	_, err = f.svc.UpdateAsset(f.admin, f.propID, a.ID, assets.AssetPatch{Name: "Sofa 2", Version: a.Version})
	must(t, err)
	_, err = f.svc.UpdateAsset(f.admin, f.propID, a.ID, assets.AssetPatch{Name: "Sofa 3", Version: a.Version})
	wantCode(t, err, "VERSION_CONFLICT")
}

func TestPermissionsAndTenantIsolation(t *testing.T) {
	f := newFx(t)
	none := f.user(t, auth.PermPayablesView)
	if _, err := f.svc.ListAssets(none, f.propID, assets.AssetFilter{}); err == nil {
		t.Error("the register was listed without a permission")
	}
	reader := f.user(t, auth.PermAssetRead)
	if _, err := f.svc.ListAssets(reader, f.propID, assets.AssetFilter{}); err != nil {
		t.Errorf("a reader cannot read: %v", err)
	}
	_, err := f.svc.CreateLocation(reader, f.propID, assets.LocationInput{Code: "X", Name: "X"})
	wantCode(t, err, "PERMISSION_DENIED")

	other := f.Tenant(t, "XYZ")
	op := f.PropertyIn(t, other.ID, "OTH", "IDR", 0)
	_, err = f.svc.ListAssets(f.admin, op.ID, assets.AssetFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestEveryUseCaseLocksInOrder(t *testing.T) {
	f := newFx(t)
	_, err := f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	page, err := f.svc.ListAssets(f.admin, f.propID, assets.AssetFilter{})
	must(t, err)
	a := page.Data[0]
	check := func(label string, run func(ctx context.Context) error) {
		ctx, trace := db.WithLockTrace(f.admin)
		must(t, run(ctx))
		level := db.LevelNone
		for _, e := range trace.Events() {
			if e.Begin {
				level = db.LevelNone
				continue
			}
			if e.Level < level {
				t.Errorf("%s: the level fell from %d to %d (%s)", label, level, e.Level, e.Table)
			}
			level = e.Level
		}
	}
	check("update", func(ctx context.Context) error {
		_, err := f.svc.UpdateAsset(ctx, f.propID, a.ID, assets.AssetPatch{Name: "Sofa 2", Version: a.Version})
		return err
	})
	check("settings", func(ctx context.Context) error {
		s, err := f.svc.GetSettings(ctx, f.propID)
		if err != nil {
			return err
		}
		_, err = f.svc.UpdateSettings(ctx, f.propID, assets.SettingsInput{RegisterFrom: s.RegisterFrom, CapitalizationThreshold: s.CapitalizationThreshold, StartRule: s.StartRule, OEPolicy: s.OEPolicy, OEDefaultMonths: s.OEDefaultMonths, TaxYearStartMonth: s.TaxYearStartMonth, Version: s.Version})
		return err
	})
	check("location", func(ctx context.Context) error {
		_, err := f.svc.CreateLocation(ctx, f.propID, assets.LocationInput{Code: "LOBBY", Name: "Lobby"})
		return err
	})
	check("rebuild", func(ctx context.Context) error { _, err := f.svc.RebuildState(ctx, f.propID); return err })
}
