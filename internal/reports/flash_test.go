package reports_test

import (
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reports"
	"kamarapms/internal/rooms/roomstest"
)

func flashRow(t *testing.T, ls []reports.FlashLine, key string) reports.FlashLine {
	t.Helper()
	for _, l := range ls {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no row %q in %+v", key, ls)
	return reports.FlashLine{}
}

// The day of the fixture: the room night 1,000,000, a minibar of 1,000,000 (+10% service, +11% VAT on both = 100,000 and 121,000), a cash payment of 300,000, a typo voided, a refund of 50,000 and
// 200,000 moved to the city ledger. The control balance is hand-computed: GTR 3,221,000 - GTP 400,000 + refunds 50,000 - city ledger 200,000 = 2,671,000 = the change of the guest ledger.
func TestDailyFlashControlBalanceAndRevenueGroups(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	_, err := f.Folios.PostCharge(f.admin, f.propID, st.Folio.ID, "c1", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("1000000")})
	must(t, err)
	_, err = f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	typo, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p2", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.Void(f.admin, f.propID, typo.Payment.ID, folios.CorrectionInput{Reason: "typo", Approval: f.approval()})
	must(t, err)
	st2 := f.stay(t, f.r102, "2026-10-02")
	dep, err := f.Folios.PostPayment(f.admin, f.propID, st2.Folio.ID, "p3", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.Refund(f.admin, f.propID, dep.Payment.ID, "r1", folios.RefundInput{Amount: "50000", Reason: "goodwill", Approval: f.approval()})
	must(t, err)
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "Acme Ltd", CreditLimit: "5000000", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	_, err = f.Folios.Transfer(f.admin, f.propID, st.Folio.ID, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "200000"})
	must(t, err)
	f.audit(t)

	r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if r.Interim || r.Control == nil || !r.Control.Balanced {
		t.Fatalf("control: %+v", r.Control)
	}
	c := r.Control.Today
	if c.GTR != "3221000" || c.GTP != "400000" || c.Refunds != "50000" || c.CityLedger != "200000" || c.Expected != "2671000" || c.ActualChange != "2671000" || c.Difference != "0" {
		t.Fatalf("control today: %+v", c)
	}
	if got := r.Revenue.GTR.Today; got != "3221000" {
		t.Fatalf("gtr %s", got)
	}
	if r.Revenue.TotalNet.Today != "3000000" || r.Revenue.Service.Today != "100000" || r.Revenue.Tax.Today != "121000" {
		t.Fatalf("revenue: %+v %+v %+v", r.Revenue.TotalNet, r.Revenue.Service, r.Revenue.Tax)
	}
	if r.Payments.GTP.Today != "400000" || r.Payments.RefundsTotal.Today != "50000" || r.Payments.CityLedger.Today != "200000" {
		t.Fatalf("payments: %+v", r.Payments)
	}
	if r.Payments.GTP.MTD != "400000" || r.Revenue.GTR.YTD != "3221000" {
		t.Fatalf("mtd/ytd: %+v %+v", r.Payments.GTP, r.Revenue.GTR)
	}
	var a, rest string
	for _, g := range r.Revenue.Groups {
		if g.Code == "A" {
			a = g.Total.Today
		} else {
			rest += g.Code + "=" + g.Total.Today + " "
		}
	}
	if a != "2000000" {
		t.Fatalf("group A %s (%s)", a, rest)
	}
	// A + B + C is the income statement of the same day
	rev, err := f.Reports.Revenue(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
	must(t, err)
	if rev.Totals.Net != r.Revenue.TotalNet.Today {
		t.Fatalf("income statement %s vs %s", rev.Totals.Net, r.Revenue.TotalNet.Today)
	}
	if r.Rooms != nil && flashRow(t, r.Rooms, "rooms_sold").Today != "2" {
		t.Fatalf("rooms sold: %+v", flashRow(t, r.Rooms, "rooms_sold"))
	}
	if r.Signatures.PreparedBy == "" {
		t.Fatal("no prepared by")
	}
}

func TestDailyFlashOfTheOpenDayIsInterim(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1")
	r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if !r.Interim || r.Control != nil {
		t.Fatalf("interim %v control %v", r.Interim, r.Control)
	}
	// Nobody has left: the average length of stay is not zero, there is nothing to average, so the cells are blank and the screens print a dash.
	if a := flashRow(t, r.Rooms, "alos"); a.Today != "" || a.MTD != "" || a.LastMonth != "" || a.LastYear != "" || a.YTD != "" {
		t.Fatalf("alos without a departure: %+v", a)
	}
	_, err = f.Reports.DailyFlash(f.admin, f.propID, d("2030-01-01"))
	wantCode(t, err, "BUSINESS_DAY_NOT_FOUND")
}

func TestDailyFlashAccess(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Reports.DailyFlash(clerk, f.propID, d("2026-09-30"))
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	_, err = f.Reports.DailyFlash(roomstest.Admin(other.ID), f.propID, d("2026-09-30"))
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestDailyFlashCSV(t *testing.T) {
	f := setup(t)
	f.audit(t)
	r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	h, rows := r.CSV()
	if len(h) != 11 || len(rows) == 0 || len(rows[0]) != len(h) {
		t.Fatalf("csv %v %d", h, len(rows))
	}
}

// A payment whose row and ledger item disagree (money that did not follow the usual path) shows as a difference; the report is still returned.
func TestDailyFlashShowsADifferenceAndStillReturns(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1")
	_, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "50000", PaymentMethod: "CASH"})
	must(t, err)
	// the payment row says 55,000 while its ledger item says 50,000
	for _, q := range []string{`ALTER TABLE payments DISABLE TRIGGER USER`, `UPDATE payments SET amount = 55000`, `ALTER TABLE payments ENABLE TRIGGER USER`} {
		_, err = f.Pool.Exec(t.Context(), q)
		must(t, err)
	}
	must(t, err)
	f.audit(t)
	r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if r.Control == nil || r.Control.Balanced || r.Control.Today.Difference != "5000" {
		t.Fatalf("control: %+v", r.Control)
	}
	found := false
	for _, w := range r.Warnings {
		found = found || w.Code == "CONTROL_DIFFERENCE"
	}
	if !found {
		t.Fatalf("warnings %+v", r.Warnings)
	}
}

// A charge code is grouped by the statement group of its revenue account: F&B revenue is group B, spa (other operated departments) is group C, and a code with no usable account is
// printed as Unmapped in group C with a warning. A + B + C stays equal to the income statement.
func TestDailyFlashGroupsRevenueByTheAccount(t *testing.T) {
	for _, c := range []struct{ account, group, warn string }{{"4230", "B", ""}, {"4310", "C", ""}} {
		t.Run(c.account, func(t *testing.T) {
			f := setup(t)
			_, err := f.Billing.UpdateChargeCode(f.admin, f.propID, f.minibar, billingconfig.ChargeCodePatch{GLAccountCode: ptr(c.account)})
			must(t, err)
			st := f.stay(t, f.r101, "2026-10-03")
			f.minibarCharge(t, st.Folio.ID, "c1")
			f.audit(t)
			r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
			must(t, err)
			got := map[string]string{}
			for _, g := range r.Revenue.Groups {
				got[g.Code] = g.Total.Today
			}
			if got["A"] != "1000000" || got[c.group] != "100000" {
				t.Fatalf("groups %v", got)
			}
			rev, err := f.Reports.Revenue(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
			must(t, err)
			if rev.Totals.Net != r.Revenue.TotalNet.Today || r.Revenue.TotalNet.Today != "1100000" {
				t.Fatalf("income statement %s vs %s", rev.Totals.Net, r.Revenue.TotalNet.Today)
			}
			for _, w := range r.Warnings {
				if w.Code == "UNMAPPED_REVENUE" {
					t.Fatalf("unexpected warning %+v", w)
				}
			}
		})
	}
}
