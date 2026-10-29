package assets_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
)

// The fiscal correction compares the two books for the same months of each asset (4.9): an old building and two chairs from the
// opening import, an asset bought inside the register and a chair that is sold later.
func TestFiscalCorrectionComparesTheSameMonths(t *testing.T) {
	f := newFx(t)
	f.exec(t, `UPDATE fa_categories SET fiscal_group = 'G1', fiscal_method = 'SL' WHERE property_id = $1 AND code = 'FFE'`, f.propID)
	building := row("Old building", "BUILDING", "1200000000", "100000000", 20)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"), building)
	bought := f.asset(t, "2026-09-15", "1530", "36000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.advanceTo(t, "2026-11-01")
	f.run(t, "2026-10")
	f.disposeAs(t, f.admin, ids[0], f.sale("18000000", "1110"))

	rep, err := f.svc.FiscalReport(f.admin, f.propID, assets.FiscalFilter{TaxYear: ptr(2026)})
	must(t, err)
	if rep.RegisterFrom == nil || rep.RegisterFrom.String() != "2026-09-01" || !rep.YearStartsBeforeRegister || rep.LastRunMonth == nil || rep.LastRunMonth.String() != "2026-10-01" || !rep.YearOpen {
		t.Fatalf("period %+v", rep)
	}
	byNumber := map[string]assets.FiscalRow{}
	sum := decimal.Zero
	for _, r := range rep.Rows {
		byNumber[r.Number] = r
		sum = sum.Add(r.Difference)
		if r.FromMonth.String() < "2026-09-01" || r.ToMonth == nil || r.ToMonth.String() > "2026-10-01" {
			t.Errorf("%s outside the months of the register: %s to %v", r.Number, r.FromMonth, r.ToMonth)
		}
	}
	// the old building: the same two months in both books, 5,000,000 a month, and the same figure in the fiscal book
	b := byNumber[f.asDetail(t, ids[2]).Number]
	if b.Months != 2 || b.FromMonth.String() != "2026-09-01" || b.ToMonth.String() != "2026-10-01" || !b.Partial || !b.UntilLastRun {
		t.Errorf("building %+v", b)
	}
	wantAmount(t, "building commercial", b.Commercial, "10000000")
	wantAmount(t, "building fiscal", b.Fiscal, "10000000")
	wantAmount(t, "building rate", b.FiscalRatePercent, "5")
	// the chair that was sold in November: both months, and its disposal uses the accumulation up to October
	a := byNumber[f.asDetail(t, ids[0]).Number]
	if a.Months != 2 || a.ToMonth.String() != "2026-10-01" {
		t.Errorf("chair A %+v", a)
	}
	wantAmount(t, "chair A fiscal", a.Fiscal, "1500000")
	wantAmount(t, "chair A commercial", a.Commercial, "2000000")
	if len(rep.Disposals) != 1 {
		t.Fatalf("%d disposals", len(rep.Disposals))
	}
	// 34 months of 750,000 = 25,500,000: book value 10,500,000, gain 7,500,000
	wantAmount(t, "fiscal book value", rep.Disposals[0].FiscalBookValue, "10500000")
	wantAmount(t, "fiscal gain", rep.Disposals[0].FiscalGain, "7500000")
	// the asset bought inside the register: from its first month of depreciation, never before
	p := byNumber[f.asDetail(t, bought.ID).Number]
	if p.Partial || p.Months < 1 || p.Months > 2 {
		t.Errorf("bought %+v", p)
	}
	wantAmount(t, "bought fiscal", p.Fiscal, decimal.NewFromInt(int64(p.Months)).Mul(dec("750000")).String())
	// the total correction is the sum of what is shown per asset and per disposal
	for _, dp := range rep.Disposals {
		sum = sum.Add(dp.Difference)
	}
	wantAmount(t, "net correction", rep.Totals.NetCorrection, sum.String())
}

// The setup check names the category in the issues and in the reconciliation, so the screen can translate the message.
func TestSetupCheckCarriesTheCategoryName(t *testing.T) {
	f := newFx(t)
	f.withAssets(t, chair("Chair A"))
	f.manual(t, "2026-09-10", "1530", "3900", "8000000")
	chk, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	for _, r := range chk.Reconciliation {
		if r.CategoryName == "" {
			t.Errorf("no category name in %+v", r)
		}
	}
	for _, i := range chk.Issues {
		if i.Code == "GL_DIFFERENCE" {
			if n, _ := i.Context["category_name"].(string); n == "" {
				t.Errorf("no category name in %+v", i)
			}
		}
	}
}
