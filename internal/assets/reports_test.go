package assets_test

import (
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/auth"
)

// Tests of PR 4, first part: the reports and the fiscal book (docs/architecture/37-fixed-assets.md 4.9, 4.12, 5.3 and the examples 10.12, 10.15).

func (f *fx) recap(t *testing.T, from, to string) assets.RecapReport {
	t.Helper()
	r, err := f.svc.RecapReport(f.admin, f.propID, ptr(d(from)), ptr(d(to)))
	must(t, err)
	return r
}

func recapRow(t *testing.T, r assets.RecapReport, cat string) assets.RecapRow {
	t.Helper()
	for _, x := range r.Rows {
		if x.CategoryCode == cat {
			return x
		}
	}
	t.Fatalf("no row for %s", cat)
	return assets.RecapRow{}
}

func causeOf(cs []assets.DifferenceCause, code string) (assets.DifferenceCause, bool) {
	for _, c := range cs {
		if c.Code == code {
			return c, true
		}
	}
	return assets.DifferenceCause{}, false
}

// The recap agrees with the ledger of the register and with the books; when the books hold what the register does not, the difference is named (5.3).
func TestRecapMatchesTheBooksAndNamesTheCauses(t *testing.T) {
	f := newFx(t)
	f.withAssets(t, chair("Chair A"), chair("Chair B"))

	rep := f.recap(t, "2026-08-01", "2026-09-30")
	ffe := recapRow(t, rep, "FFE")
	for what, got := range map[string]string{"cost opening": ffe.CostOpening.String(), "cost additions": ffe.CostAdditions.String(), "cost closing": ffe.CostClosing.String(),
		"accum additions": ffe.AccumAdditions.String(), "accum closing": ffe.AccumClosing.String(), "carrying": ffe.CarryingClosing.String(),
		"ledger cost": ffe.LedgerCostClosing.String(), "ledger accum": ffe.LedgerAccumClosing.String()} {
		want := map[string]string{"cost opening": "0", "cost additions": "72000000", "cost closing": "72000000", "accum additions": "42000000", "accum closing": "42000000",
			"carrying": "30000000", "ledger cost": "72000000", "ledger accum": "42000000"}[what]
		if got != want {
			t.Errorf("%s: %s, want %s", what, got, want)
		}
	}
	if !ffe.CostDifference.IsZero() || !ffe.AccumDifference.IsZero() || len(ffe.CostCauses) != 0 || len(ffe.AccumCauses) != 0 {
		t.Fatalf("the books and the register differ: %+v", ffe)
	}
	// a period after the opening shows the opening balance as the opening of the period
	later := recapRow(t, f.recap(t, "2026-09-01", "2026-09-30"), "FFE")
	if !later.CostOpening.Equal(dec("72000000")) || !later.CostAdditions.IsZero() || !later.AccumOpening.Equal(dec("42000000")) {
		t.Errorf("%+v", later)
	}

	// a manual journal in the period of the register waits in the list: the difference is its amount and that is its cause
	waiting := f.post(t, "2026-09-10", "1530", "3900", "5000000")
	ffe = recapRow(t, f.recap(t, "2026-08-01", "2026-09-30"), "FFE")
	c, ok := causeOf(ffe.CostCauses, "WAITING_LIST")
	if !ffe.CostDifference.Equal(dec("5000000")) || !ok || !c.Amount.Equal(dec("5000000")) || c.Lines != 1 || len(ffe.CostCauses) != 1 {
		t.Fatalf("waiting: %+v %+v", ffe.CostDifference, ffe.CostCauses)
	}
	// dismissed, it is another cause
	line := f.lineOf(t, waiting.Number, "1530")
	must(t, f.svc.DismissLine(f.admin, f.propID, assets.DismissInput{JournalLineID: line.LineID, Reason: "a repair, not an asset"}))
	ffe = recapRow(t, f.recap(t, "2026-08-01", "2026-09-30"), "FFE")
	if c, ok := causeOf(ffe.CostCauses, "DISMISSED"); !ok || !c.Amount.Equal(dec("5000000")) || len(ffe.CostCauses) != 1 {
		t.Fatalf("dismissed: %+v", ffe.CostCauses)
	}
	// a manual journal before the register started is outside the list: a manual journal on the account
	f.post(t, "2026-08-15", "1530", "3900", "3000000")
	ffe = recapRow(t, f.recap(t, "2026-08-01", "2026-09-30"), "FFE")
	if !ffe.CostDifference.Equal(dec("8000000")) {
		t.Fatalf("difference %s", ffe.CostDifference)
	}
	if c, ok := causeOf(ffe.CostCauses, "MANUAL_JOURNAL"); !ok || !c.Amount.Equal(dec("3000000")) {
		t.Fatalf("manual: %+v", ffe.CostCauses)
	}
	if _, other := causeOf(ffe.CostCauses, "OTHER"); other {
		t.Fatalf("something is unexplained: %+v", ffe.CostCauses)
	}
	// the same causes are in the setup check
	chk, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	found := false
	for _, r := range chk.Reconciliation {
		if r.CategoryCode == "FFE" {
			found = true
			if _, ok := causeOf(r.CostCauses, "MANUAL_JOURNAL"); !ok || !r.CostDifference.Equal(dec("8000000")) {
				t.Errorf("setup check: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("no FFE in the setup check")
	}
}

// The depreciation of a run and the disposal of an asset are columns of their own, and the books still agree.
func TestRecapShowsDepreciationAndDisposals(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"))
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.disposeAs(t, f.admin, ids[0], f.sale("18000000", "1110"))

	ffe := recapRow(t, f.recap(t, "2026-09-01", "2026-10-01"), "FFE")
	for what, p := range map[string][2]string{
		"opening":      {ffe.CostOpening.String(), "72000000"},
		"disposals":    {ffe.CostDisposals.String(), "-36000000"},
		"closing":      {ffe.CostClosing.String(), "36000000"},
		"accum open":   {ffe.AccumOpening.String(), "42000000"},
		"depreciation": {ffe.AccumDepreciation.String(), "2000000"},
		"accum disp":   {ffe.AccumDisposals.String(), "-22000000"},
		"accum close":  {ffe.AccumClosing.String(), "22000000"},
		"carrying":     {ffe.CarryingClosing.String(), "14000000"},
	} {
		if p[0] != p[1] {
			t.Errorf("%s: %s, want %s", what, p[0], p[1])
		}
	}
	if !ffe.CostDifference.IsZero() || !ffe.AccumDifference.IsZero() {
		t.Fatalf("the books and the register differ: %s %s", ffe.CostDifference, ffe.AccumDifference)
	}
}

// 10.12 and 10.15 through the service: the commercial depreciation comes from the ledger, the fiscal one from the rates, and the correction is the difference.
func TestFiscalReportOfAnOldAssetAndOfADisposal(t *testing.T) {
	f := newFx(t)
	f.exec(t, `UPDATE fa_categories SET fiscal_group = 'G1', fiscal_method = 'SL' WHERE property_id = $1 AND code = 'FFE'`, f.propID)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"))
	if a := f.asDetail(t, ids[1]); a.FiscalGroup != "G1" {
		t.Fatalf("fiscal group %s", a.FiscalGroup)
	}
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.disposeAs(t, f.admin, ids[0], f.sale("18000000", "1110"))

	rep, err := f.svc.FiscalReport(f.admin, f.propID, assets.FiscalFilter{TaxYear: ptr(2026)})
	must(t, err)
	if rep.TaxYear != 2026 || rep.YearStart.String() != "2026-01-01" || rep.YearEnd.String() != "2026-12-31" || !rep.RatesToConfirm || len(rep.Rates) != 6 {
		t.Fatalf("%+v", rep)
	}
	byNumber := map[string]assets.FiscalRow{}
	for _, r := range rep.Rows {
		byNumber[r.Number] = r
	}
	a0, b0 := f.asDetail(t, ids[0]), f.asDetail(t, ids[1])
	rowA, rowB := byNumber[a0.Number], byNumber[b0.Number]
	// the chair B: 750,000 a month by the rates, compared with the one month the register has depreciated (September), the same month
	wantAmount(t, "B fiscal", rowB.Fiscal, "750000")
	wantAmount(t, "B commercial", rowB.Commercial, "1000000")
	wantAmount(t, "B difference", rowB.Difference, "250000")
	if !rowB.Partial || rowB.FiscalGroup != "G1" || rowB.Months != 1 || rowB.FromMonth.String() != "2026-09-01" || rowB.ToMonth == nil || rowB.ToMonth.String() != "2026-09-01" || !rowB.UntilLastRun {
		t.Errorf("B: %+v", rowB)
	}
	// the chair A left in October: the month of the disposal is not depreciated, so its period is September as well
	wantAmount(t, "A fiscal", rowA.Fiscal, "750000")
	wantAmount(t, "A commercial", rowA.Commercial, "1000000")
	if len(rep.Disposals) != 1 {
		t.Fatalf("%d disposals", len(rep.Disposals))
	}
	dp := rep.Disposals[0]
	// fiscal accumulation up to September (33 months of 750,000 = 24,750,000): book value 11,250,000, so the fiscal gain is 6,750,000;
	// the commercial carrying value was 14,000,000 (the run of September took 1,000,000), so its gain is 4,000,000
	wantAmount(t, "fiscal book value", dp.FiscalBookValue, "11250000")
	wantAmount(t, "fiscal gain", dp.FiscalGain, "6750000")
	wantAmount(t, "commercial gain", dp.CommercialGain, "4000000")
	wantAmount(t, "disposal difference", dp.Difference, "2750000")
	wantAmount(t, "total fiscal", rep.Totals.Fiscal, "1500000")
	wantAmount(t, "total commercial", rep.Totals.Commercial, "2000000")
	wantAmount(t, "negative correction", rep.Totals.NegativeCorrection, "0")
	wantAmount(t, "positive correction", rep.Totals.PositiveCorrection, "500000")
	wantAmount(t, "net", rep.Totals.NetCorrection, "3250000")
	if rep.RegisterFrom == nil || rep.RegisterFrom.String() != "2026-09-01" || !rep.YearStartsBeforeRegister || rep.LastRunMonth == nil || rep.LastRunMonth.String() != "2026-09-01" || !rep.YearOpen {
		t.Errorf("period: %+v", rep)
	}
	if rep.Totals.PartialYearAssets != 2 {
		t.Errorf("%d partial", rep.Totals.PartialYearAssets)
	}
	// 2027: no run covers it yet, so there is nothing to compare
	next, err := f.svc.FiscalReport(f.admin, f.propID, assets.FiscalFilter{TaxYear: ptr(2027)})
	must(t, err)
	wantAmount(t, "2027 fiscal", next.Totals.Fiscal, "0")
	wantAmount(t, "2027 commercial", next.Totals.Commercial, "0")
	wantAmount(t, "2027 positive", next.Totals.PositiveCorrection, "0")
	// a filter by asset
	one, err := f.svc.FiscalReport(f.admin, f.propID, assets.FiscalFilter{TaxYear: ptr(2026), AssetID: &ids[1]})
	must(t, err)
	if len(one.Rows) != 1 || one.Rows[0].Number != b0.Number {
		t.Fatalf("%+v", one.Rows)
	}
	if _, err := f.svc.FiscalReport(f.admin, f.propID, assets.FiscalFilter{TaxYear: ptr(1800)}); err == nil {
		t.Fatal("a year of 1800 was accepted")
	}
}

// The schedule of an asset has the fiscal columns (4.9): the opening state, then month by month by the rates.
func TestTheScheduleOfAnAssetHasTheFiscalColumns(t *testing.T) {
	f := newFx(t)
	f.exec(t, `UPDATE fa_categories SET fiscal_group = 'G1', fiscal_method = 'SL' WHERE property_id = $1 AND code = 'FFE'`, f.propID)
	ids := f.withAssets(t, chair("Chair A"))
	sch, err := f.svc.AssetSchedule(f.admin, f.propID, ids[0])
	must(t, err)
	if sch.FiscalGroup == nil || *sch.FiscalGroup != "G1" {
		t.Fatalf("%+v", sch.FiscalGroup)
	}
	var opening, sep *assets.ScheduleRow
	for i := range sch.Rows {
		switch {
		case sch.Rows[i].Kind == assets.ScheduleOpening:
			opening = &sch.Rows[i]
		case sch.Rows[i].Month.String() == "2026-09-01":
			sep = &sch.Rows[i]
		}
	}
	if opening == nil || sep == nil || opening.FiscalAccumAfter == nil || sep.FiscalAmount == nil {
		t.Fatalf("rows %+v", sch.Rows)
	}
	wantAmount(t, "opening fiscal", *opening.FiscalAccumAfter, "24000000")
	wantAmount(t, "september fiscal", *sep.FiscalAmount, "750000")
	wantAmount(t, "september fiscal accumulated", *sep.FiscalAccumAfter, "24750000")
}

// The register as of a date, the locations and departments, the warranty and life, the schedules of a category.
func TestTheOtherReports(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"))
	f.exec(t, `UPDATE fa_assets SET warranty_until = '2026-11-15' WHERE id = $1`, ids[0])

	rep, err := f.svc.RegisterReport(f.admin, f.propID, assets.RegisterFilter{AsOf: ptr(d("2026-09-30"))})
	must(t, err)
	wantAmount(t, "register cost", rep.Cost, "72000000")
	wantAmount(t, "register carrying", rep.CarryingValue, "30000000")
	if rep.Count != 2 || len(rep.Rows) != 2 || rep.Rows[0].MonthsLeft == nil || *rep.Rows[0].MonthsLeft != 15 {
		t.Fatalf("%+v", rep.Rows)
	}
	// before the opening there was nothing
	before, err := f.svc.RegisterReport(f.admin, f.propID, assets.RegisterFilter{AsOf: ptr(d("2026-07-31"))})
	must(t, err)
	if before.Count != 0 {
		t.Fatalf("%d assets before the opening", before.Count)
	}
	// a disposed asset is in the register until its disposal
	f.disposeAs(t, f.admin, ids[0], f.writeOff(assets.ReasonLost))
	still, err := f.svc.RegisterReport(f.admin, f.propID, assets.RegisterFilter{AsOf: ptr(d("2026-09-29"))})
	must(t, err)
	gone, err := f.svc.RegisterReport(f.admin, f.propID, assets.RegisterFilter{AsOf: ptr(d("2026-09-30"))})
	must(t, err)
	if still.Count != 2 || gone.Count != 1 {
		t.Fatalf("%d and %d", still.Count, gone.Count)
	}

	loc := ensureLocation(t, f, "LOBBY")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{ids[1]}, ChangeLocation: true, LocationID: &loc, Reason: "to the lobby", IdempotencyKey: nextKey()})
	must(t, err)
	dep := f.dept(t, "ROOMS")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{ids[1]}, ChangeDepartment: true, DepartmentID: &dep, Reason: "rooms", IdempotencyKey: nextKey()})
	must(t, err)
	pl, err := f.svc.PlacementReport(f.admin, f.propID)
	must(t, err)
	var lobby, rooms *assets.PlacementRow
	for i := range pl.Locations {
		if pl.Locations[i].Code == "LOBBY" {
			lobby = &pl.Locations[i]
		}
	}
	for i := range pl.Departments {
		if pl.Departments[i].Code == "ROOMS" {
			rooms = &pl.Departments[i]
		}
	}
	if lobby == nil || lobby.Assets != 1 || !lobby.Cost.Equal(dec("36000000")) || !lobby.CarryingValue.Equal(dec("15000000")) || rooms == nil || rooms.TotalAssets != 1 {
		t.Fatalf("%+v %+v", lobby, rooms)
	}
	if pl.NoLocation.Assets != 0 || pl.NoDepartment.Assets != 0 {
		t.Errorf("unplaced %d %d", pl.NoLocation.Assets, pl.NoDepartment.Assets)
	}

	exp, err := f.svc.ExpiringReport(f.admin, f.propID, 90, 6)
	must(t, err)
	if len(exp) != 0 { // the chair with the warranty was written off; the other has 15 months and no warranty
		t.Fatalf("%+v", exp)
	}
	exp, err = f.svc.ExpiringReport(f.admin, f.propID, 90, 15)
	must(t, err)
	if len(exp) != 1 || !exp[0].LifeEnding {
		t.Fatalf("%+v", exp)
	}
	if _, err := f.svc.ExpiringReport(f.admin, f.propID, -1, 6); err == nil {
		t.Fatal("negative days were accepted")
	}

	list, err := f.svc.ScheduleReport(f.admin, f.propID, nil, ptr(f.categoryID(t, "FFE")))
	must(t, err)
	if len(list) != 1 || !list[0].Depreciable {
		t.Fatalf("%d schedules", len(list))
	}
	if _, err := f.svc.ScheduleReport(f.admin, f.propID, nil, nil); err == nil {
		t.Fatal("a schedule of nothing was accepted")
	}
}

// Every report needs a permission of the register: a person without one gets PERMISSION_DENIED, whatever the filters say.
func TestTheReportsNeedAnAssetPermission(t *testing.T) {
	f := newFx(t)
	nobody := f.user(t)
	reader := f.user(t, auth.PermAssetRead)
	check := func(name string, deny func() error, allow func() error) {
		t.Helper()
		wantCode(t, deny(), "PERMISSION_DENIED")
		if err := allow(); err != nil {
			t.Errorf("%s: a reader was refused: %v", name, err)
		}
	}
	check("register", func() error { _, e := f.svc.RegisterReport(nobody, f.propID, assets.RegisterFilter{}); return e },
		func() error { _, e := f.svc.RegisterReport(reader, f.propID, assets.RegisterFilter{}); return e })
	check("recap", func() error { _, e := f.svc.RecapReport(nobody, f.propID, nil, nil); return e },
		func() error { _, e := f.svc.RecapReport(reader, f.propID, nil, nil); return e })
	check("fiscal", func() error { _, e := f.svc.FiscalReport(nobody, f.propID, assets.FiscalFilter{}); return e },
		func() error { _, e := f.svc.FiscalReport(reader, f.propID, assets.FiscalFilter{}); return e })
	check("placement", func() error { _, e := f.svc.PlacementReport(nobody, f.propID); return e },
		func() error { _, e := f.svc.PlacementReport(reader, f.propID); return e })
	check("expiring", func() error { _, e := f.svc.ExpiringReport(nobody, f.propID, 90, 6); return e },
		func() error { _, e := f.svc.ExpiringReport(reader, f.propID, 90, 6); return e })
	check("schedule", func() error { _, e := f.svc.ScheduleReport(nobody, f.propID, nil, ptr(int64(1))); return e },
		func() error { _, e := f.svc.ScheduleReport(reader, f.propID, nil, ptr(int64(1))); return e })
}
