package shifts_test

import (
	"context"
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reports"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/shifts"
)

// The Z report of all drawers (docs/architecture/38-reports-night-audit-pack.md 7.5) over a day with two drawers, hand-computed:
//
//	FRONT, cashier A: float 100,000, cash 400,000, a card of 50,000, a drop of 300,000; counted 200,000 = expected 100,000 + 400,000 - 300,000; closed (Z)
//	BACK, cashier B:  float  50,000, cash 100,000, a pay-out of 20,000; still open (X): expected 50,000 + 100,000 - 20,000 = 130,000, nothing counted
//	cash of 30,000 taken by the front office with no shift (the rule is off for the moment)
//
// The property: 2 shifts, float 150,000, cash 500,000 on shifts, drops 300,000, pay-outs 20,000, expected 330,000, counted 200,000 (the closed shift only), over/short 0. Taken on the day: cash 400,000 +
// 100,000 + 30,000 = 530,000 (30,000 of it not on a shift), card 50,000; the cashier report says the same, so the report is reconciled.
func TestTheZReportOfAllDrawers(t *testing.T) {
	f := setup(t)
	colleague := f.User(t, f.tenantID, f.propID, auth.PermCashierShift, auth.PermPaymentPost, auth.PermFolioRead)
	expense := f.account(t, "6190")
	day := roomstest.BD

	a := f.open(t, f.cashier, "FRONT", "100000")
	b := f.open(t, colleague, "BACK", "50000")
	folio := f.folio(t)
	_, err := f.pay(f.cashier, folio, "a1", "CASH", "400000")
	must(t, err)
	_, err = f.pay(f.cashier, folio, "a2", "CARD", "50000")
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, a.ID, "ad", shifts.MovementInput{Kind: "DROP", Amount: "300000", Reason: "to the safe"})
	must(t, err)
	_, err = f.pay(colleague, folio, "b1", "CASH", "100000")
	must(t, err)
	_, err = f.Shifts.Move(colleague, f.propID, b.ID, "bo", shifts.MovementInput{Kind: "PAY_OUT", Amount: "20000", AccountID: expense, Reason: "taxi"})
	must(t, err)
	_, err = f.Shifts.Close(f.cashier, f.propID, a.ID, shifts.CloseInput{CountedCash: "200000"})
	must(t, err)
	_, err = f.Pool.Exec(context.Background(), `UPDATE property_cashier_settings SET require_shift_for_cash = false WHERE property_id = $1`, f.propID)
	must(t, err)
	_, err = f.pay(f.admin, folio, "off", "CASH", "30000")
	must(t, err)

	z, err := f.Reports.ZReports(f.admin, f.propID, day)
	must(t, err)
	if len(z.Blocks) != 2 || len(z.Drawers) != 2 {
		t.Fatalf("blocks: %+v", z.Blocks)
	}
	blocks := map[string]reports.ZBlock{}
	for _, x := range z.Blocks {
		blocks[x.Drawer] = x
	}
	front, back := blocks["FRONT"], blocks["BACK"]
	if front.Kind != "Z" || front.OpeningFloat != "100000" || front.CashPayments != "400000" || front.Drops != "300000" || front.ExpectedCash != "200000" || front.CountedCash == nil || *front.CountedCash != "200000" ||
		front.OverShort == nil || *front.OverShort != "0" || front.TotalTakings != "450000" || front.ClosedAt == nil {
		t.Fatalf("FRONT: %+v", front)
	}
	if len(front.Tenders) != 1 || front.Tenders[0].Method != "CARD" || front.Tenders[0].Amount != "50000" {
		t.Fatalf("the card is another tender: %+v", front.Tenders)
	}
	if back.Kind != "X" || back.OpeningFloat != "50000" || back.CashPayments != "100000" || back.PayOuts != "20000" || back.ExpectedCash != "130000" || back.CountedCash != nil || back.OverShort != nil || back.ClosedAt != nil || back.TotalTakings != "100000" {
		t.Fatalf("BACK is an X report with nothing counted: %+v", back)
	}
	p := z.Property
	if p.Shifts != 2 || p.OpeningFloat != "150000" || p.CashPayments != "500000" || p.Drops != "300000" || p.PayOuts != "20000" || p.ExpectedCash != "330000" || p.CountedCash == nil || *p.CountedCash != "200000" ||
		p.OverShort == nil || *p.OverShort != "0" || p.TotalTakings != "550000" {
		t.Fatalf("property: %+v", p)
	}
	if z.CashOffShift != "30000" || z.VoidedAfterClose != "0" || !z.Reconciled {
		t.Fatalf("off shift %s reconciled %v: %+v", z.CashOffShift, z.Reconciled, z.Takings)
	}
	by := map[string]reports.ZTaking{}
	for _, x := range z.Takings {
		by[x.Method] = x
	}
	if c := by["CASH"]; c.OnShift != "500000" || c.OffShift != "30000" || c.Total != "530000" || c.CashierReport != "530000" || c.Difference != "0" {
		t.Fatalf("cash: %+v", c)
	}
	if c := by["CARD"]; c.Total != "50000" || c.CashierReport != "50000" || c.Difference != "0" {
		t.Fatalf("card: %+v", c)
	}

	// the totals are the shift reports: the report of the drawer says the same as the Z report of the day
	one, err := f.Shifts.Report(f.admin, f.propID, a.ID)
	must(t, err)
	if one.Shift.Cash == nil || one.Shift.Cash.Expected != front.ExpectedCash {
		t.Fatalf("the shift report and the day report disagree: %+v vs %+v", one.Shift.Cash, front)
	}

	// the shift of BACK stays open over midnight (the night audit no longer waits for it): the next day shows it as an X, FRONT is not there any more
	_, err = f.Pool.Exec(context.Background(), `UPDATE property_cashier_settings SET block_night_audit = false WHERE property_id = $1`, f.propID)
	must(t, err)
	if _, err = f.Audit.Run(f.admin, f.propID, day); err != nil {
		t.Fatal(err)
	}
	next := day.AddDays(1)
	z2, err := f.Reports.ZReports(f.admin, f.propID, next)
	must(t, err)
	if len(z2.Blocks) != 1 || z2.Blocks[0].Drawer != "BACK" || z2.Blocks[0].Kind != "X" {
		t.Fatalf("the open shift is on the next day too: %+v", z2.Blocks)
	}
	old, err := f.Reports.ZReports(f.admin, f.propID, day)
	must(t, err)
	if len(old.Blocks) != 2 {
		t.Fatalf("the closed day still has both drawers: %+v", old.Blocks)
	}
}

func TestTheZReportNeedsBothPermissionsBeforeTheDateIsRead(t *testing.T) {
	f := setup(t)
	onlyView := f.User(t, f.tenantID, f.propID, auth.PermReportView)
	onlyShift := f.User(t, f.tenantID, f.propID, auth.PermCashierShiftManage)
	_, err := f.Reports.ZReports(onlyView, f.propID, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Reports.ZReports(onlyShift, f.propID, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Reports.ZReports(f.admin, f.propID, roomstest.BD.AddDays(40))
	wantCode(t, err, "BUSINESS_DAY_NOT_FOUND")
}
