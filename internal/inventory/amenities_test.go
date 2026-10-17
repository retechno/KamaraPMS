package inventory_test

import (
	"context"
	"testing"
	"time"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomstest"
)

// The automatic amenities of the day close (35-inventory.md 5.9, 8.11, D24). The fixture is a small hotel: three standard rooms and a suite slept in by two guests each, and a plan that gives every room a soap
// and every guest a toothbrush, and the suite a shampoo and slippers for each guest. The close is run the way the night audit runs it: the open day FOR UPDATE, then CloseStock.

// occupancy is the front desk's answer, as internal/app wires it.
type occupancy struct{ fd *frontdesk.Service }

func (o occupancy) NightOccupancy(ctx context.Context, tenantID, propertyID int64, date civil.Date) ([]inventory.NightRoom, error) {
	rooms, err := o.fd.NightOccupancy(ctx, tenantID, propertyID, date)
	out := make([]inventory.NightRoom, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, inventory.NightRoom{RoomID: r.RoomID, RoomNumber: r.RoomNumber, RoomTypeID: r.RoomTypeID, StayID: r.StayID, Guests: r.Guests, Kind: r.Kind})
	}
	return out, err
}

// afx is the movements fixture with the hotel of the amenities.
type afx struct {
	*mfx
	std, ste int64 // the room types
	rooms    map[string]int64
	stays    map[string]int64
	night    string // the business date of the fixture, as text
}

const (
	soapCost    = "2960"
	brushCost   = "2100"
	shampooCost = "3250"
	slipperCost = "6500"
)

func newAmenities(t *testing.T, method string) *afx {
	t.Helper()
	a := &afx{mfx: newMovements(t, method), rooms: map[string]int64{}, stays: map[string]int64{}, night: roomstest.BD.String()}
	a.svc.SetOccupancy(occupancy{fd: a.Front})
	a.newItem(t, "TBRUSH", "Toothbrush", "SUP", "pcs")
	a.newItem(t, "SLIPPER", "Hotel slippers", "SUP", "pcs")
	std := a.RoomType(t, a.admin, a.propID, "STD")
	ste := a.RoomType(t, a.admin, a.propID, "STE")
	a.std, a.ste = std.ID, ste.ID
	for _, r := range []struct {
		no   string
		typ  int64
		from string
		to   string
	}{{"101", a.std, a.night, "2026-10-02"}, {"102", a.std, a.night, "2026-10-02"}, {"103", a.std, a.night, "2026-10-02"}, {"201", a.ste, a.night, "2026-10-02"}} {
		room := a.Room(t, a.admin, a.propID, r.typ, r.no)
		a.rooms[r.no] = room.ID
		a.stays[r.no] = a.Stay(t, a.tenantID, a.propID, r.typ, room.ID, r.from, r.to)
	}
	a.open(t, "HK", "SOAP", "pcs", "3", soapCost) // short by one tonight
	a.open(t, "HK", "TBRUSH", "pcs", "20", brushCost)
	a.open(t, "HK", "SHAMPOO", "pcs", "10", shampooCost)
	a.open(t, "HK", "SLIPPER", "pcs", "10", slipperCost)
	return a
}

func (a *afx) savePlan(t *testing.T, mut ...func(in *inventory.AmenityPlanInput)) inventory.AmenityPlan {
	t.Helper()
	in := inventory.AmenityPlanInput{IsEnabled: true, WarehouseID: ptr(a.hk), DepartmentID: &a.dept, ExpenseAccountID: ptr(a.accounts["5150"]), Lines: []inventory.AmenityPlanLineInput{
		{ItemID: a.item["SOAP"], UnitID: a.unit["pcs"], Qty: dec("1"), Basis: inventory.BasisRoom},
		{ItemID: a.item["TBRUSH"], UnitID: a.unit["pcs"], Qty: dec("1"), Basis: inventory.BasisGuest},
		{RoomTypeID: ptr(a.ste), ItemID: a.item["SHAMPOO"], UnitID: a.unit["pcs"], Qty: dec("1"), Basis: inventory.BasisRoom},
		{RoomTypeID: ptr(a.ste), ItemID: a.item["SLIPPER"], UnitID: a.unit["pcs"], Qty: dec("1"), Basis: inventory.BasisGuest},
	}}
	for _, f := range mut {
		f(&in)
	}
	p, err := a.svc.SaveAmenityPlan(a.admin, a.propID, in)
	must(t, err)
	return p
}

// close runs the stock step of the close of the open day, as the night audit does, in one transaction.
func (a *afx) closeDay() error {
	return a.TxM.WithinTx(a.admin, func(ctx context.Context) error {
		day, err := a.Tenancy.RequireOpenBusinessDay(ctx, a.propID, db.ForUpdate, nil)
		if err != nil {
			return err
		}
		p, err := auth.Require(ctx)
		if err != nil {
			return err
		}
		return a.svc.CloseStock(ctx, p, a.propID, day.BusinessDate)
	})
}

func (a *afx) run(t *testing.T, date string) inventory.AmenityRun {
	t.Helper()
	r, err := a.svc.GetAmenityRun(a.admin, a.propID, civil.MustParseDate(date))
	must(t, err)
	return r
}

func TestTheCloseMakesOneDocumentForTheRoomsThatWereSleptInAndAShortageDoesNotStopIt(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a := newAmenities(t, method)
			a.savePlan(t)
			// the preview of tonight says what the close will do, and warns about the soap
			pv, err := a.svc.AmenityPreview(a.admin, a.propID)
			must(t, err)
			if pv.Rooms != 4 || pv.Guests != 8 || len(pv.Short) != 1 || pv.Short[0].ItemCode != "SOAP" || !pv.Short[0].Short.Equal(dec("1")) {
				t.Fatalf("preview: rooms %d guests %d short %+v", pv.Rooms, pv.Guests, pv.Short)
			}
			if pv.Invalid == nil || pv.Types == nil || pv.Lines == nil {
				t.Fatalf("the lists of the preview are lists in the JSON, never null: invalid %v types %v lines %v", pv.Invalid, pv.Types, pv.Lines)
			}
			day, _ := a.Tenancy.CurrentBusinessDay(a.admin, a.propID)
			b, w, err := a.svc.NightAuditStock(a.admin, a.tenantID, a.propID, day.BusinessDate)
			must(t, err)
			if hasFinding(b, "AMENITY_STOCK_SHORT") || !hasFinding(w, "AMENITY_STOCK_SHORT") {
				t.Fatalf("a shortage is a warning, never a blocker: %v / %v", b, w)
			}
			must(t, a.closeDay())
			r := a.run(t, a.night)
			if r.Number != "AMN-20260930" || r.Rooms != 4 || r.Guests != 8 || r.Status != "POSTED" || r.HeldLines != 0 {
				t.Fatalf("run: %+v", r.AmenityRunBrief)
			}
			// 4 soaps (3 on the shelf and 1 at the estimate), 8 toothbrushes, 1 shampoo, 2 slippers
			want := dec("44890") // 4 x 2,960 + 8 x 2,100 + 3,250 + 2 x 6,500
			if !r.TotalValue.Equal(want) {
				t.Fatalf("value %s, want %s", r.TotalValue, want)
			}
			a.wantBal(t, a.pair("HK", "SOAP"), "-1", "-2960")
			a.wantBal(t, a.pair("HK", "TBRUSH"), "12", "25200")
			a.wantBal(t, a.pair("HK", "SHAMPOO"), "9", "29250")
			a.wantBal(t, a.pair("HK", "SLIPPER"), "8", "52000")
			// the shortfall keeps the account of the plan and the department
			var acc, dept int64
			var src string
			must(t, a.Pool.QueryRow(context.Background(), `SELECT gl_debit_account_id, COALESCE(gl_department_id, 0), source_type FROM inv_shortfalls WHERE warehouse_id = $1 AND status = 'OPEN'`, a.hk).Scan(&acc, &dept, &src))
			if acc != a.accounts["5150"] || dept != a.dept || src != "AMENITY" {
				t.Fatalf("shortfall: %d %d %s", acc, dept, src)
			}
			// the journal: Dr 5150 / Cr 1330 for the whole document, with the department
			if len(r.Journal) != 1 || r.Journal[0].DebitCode != "5150" || r.Journal[0].CreditCode != "1330" || !r.Journal[0].Amount.Equal(want) {
				t.Fatalf("journal: %+v", r.Journal)
			}
			// the soap line says what the shelf held and what was needed
			if len(r.Short) != 1 || !r.Short[0].StockBefore.Equal(dec("3")) || !r.Short[0].QtyStock.Equal(dec("4")) {
				t.Fatalf("short: %+v", r.Short)
			}
			if len(r.Types) != 2 || len(r.RoomList) != 4 {
				t.Fatalf("types %+v rooms %d", r.Types, len(r.RoomList))
			}
			// the document is in the list of issues, as an amenities document
			list, err := a.svc.ListIssues(a.admin, a.propID, inventory.IssueFilter{Origin: inventory.OriginAmenity})
			must(t, err)
			if len(list.Data) != 1 || list.Data[0].Number != "AMN-20260930" {
				t.Fatalf("issues: %+v", list.Data)
			}
			a.consistent(t)
		})
	}
}

func TestTheCloseIsIdempotentByTheDayWhetherItRunsTwiceOrAtTheSameTime(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	a.savePlan(t)
	must(t, a.closeDay())
	must(t, a.closeDay()) // the second attempt of the same day finds the run and does nothing
	errs := race(a.closeDay, a.closeDay, a.closeDay)
	if succeeded(errs) != 3 {
		t.Fatalf("%v", errs)
	}
	if a.Count(t, `SELECT count(*) FROM inv_amenity_runs`) != 1 || a.Count(t, `SELECT count(*) FROM inv_issues WHERE origin = 'AMENITY'`) != 1 {
		t.Fatal("more than one document for the day")
	}
	if n := a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE' AND kind = 'ISSUE'`); n != 4 {
		t.Fatalf("%d movements for 4 items", n)
	}
	a.wantBal(t, a.pair("HK", "TBRUSH"), "12", "25200")
}

func TestACloseThatRollsBackLeavesNoDocument(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	a.savePlan(t)
	err := a.TxM.WithinTx(a.admin, func(ctx context.Context) error {
		day, err := a.Tenancy.RequireOpenBusinessDay(ctx, a.propID, db.ForUpdate, nil)
		if err != nil {
			return err
		}
		p, _ := auth.Require(ctx)
		if err := a.svc.CloseStock(ctx, p, a.propID, day.BusinessDate); err != nil {
			return err
		}
		return context.Canceled // the journal is not ready: the whole close is rolled back
	})
	if err == nil {
		t.Fatal("the transaction was to roll back")
	}
	if a.Count(t, `SELECT count(*) FROM inv_amenity_runs`)+a.Count(t, `SELECT count(*) FROM inv_issues`)+a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE'`) != 0 {
		t.Fatal("a rolled-back close left a document")
	}
	a.wantBal(t, a.pair("HK", "TBRUSH"), "20", "42000")
	must(t, a.closeDay()) // and the close that follows makes it
	if a.run(t, a.night).Rooms != 4 {
		t.Fatal("the run")
	}
}

// 5.9: a shortage settles with the next receipt, like a sale below zero (4.4).
func TestAShortageOfTheAmenitiesIsSettledByTheNextReceipt(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a := newAmenities(t, method)
			a.savePlan(t)
			must(t, a.closeDay())
			a.in(t, inventory.KindReceipt, a.pair("HK", "SOAP"), "500", "1500000") // 500 soaps at 3,000
			var settle string
			must(t, a.Pool.QueryRow(context.Background(), `SELECT value_delta::text FROM inv_movements WHERE kind = 'NEG_SETTLE' AND item_id = $1`, a.item["SOAP"]).Scan(&settle))
			if !dec(settle).Equal(dec("-40")) { // -1 x (3,000 - 2,960)
				t.Fatalf("settlement %s", settle)
			}
			a.wantBal(t, a.pair("HK", "SOAP"), "499", "1497000")
			a.consistent(t)
		})
	}
}

// 5.9 correction: the reversing document is whole, once, dated the open day; it lowers the open shortfall and puts the goods back.
func TestTheReversalOfTheAmenitiesOfANightIsWholeAndOnce(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a := newAmenities(t, method)
			a.savePlan(t)
			must(t, a.closeDay())
			a.nextBusinessDay(t)
			_, err := a.svc.ReverseAmenityRun(a.admin, a.propID, roomstest.BD, "")
			wantCode(t, err, "VALIDATION_FAILED")
			r, err := a.svc.ReverseAmenityRun(a.admin, a.propID, roomstest.BD, "three rooms were not occupied")
			must(t, err)
			if r.Status != "REVERSED" || r.ReversalNumber == nil {
				t.Fatalf("%+v", r.AmenityRunBrief)
			}
			a.wantBal(t, a.pair("HK", "SOAP"), "3", "8880")
			a.wantBal(t, a.pair("HK", "TBRUSH"), "20", "42000")
			if n := a.Count(t, `SELECT count(*) FROM inv_shortfalls WHERE warehouse_id = $1 AND status = 'OPEN'`, a.hk); n != 0 {
				t.Fatalf("%d shortfalls open after the reversal", n)
			}
			// the reversing document is dated the open day, the original keeps its day
			if a.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'ISSUE_REVERSAL' AND business_date = '2026-10-01'`) != 4 {
				t.Fatal("the reversal is dated the open day")
			}
			_, err = a.svc.ReverseAmenityRun(a.admin, a.propID, roomstest.BD, "again")
			wantCode(t, err, "AMENITY_RUN_NOT_REVERSIBLE")
			a.consistent(t)
		})
	}
}

func TestThePlanIsSavedAsNewVersionsAndSwitchingItOffMakesNoDocument(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	p1 := a.savePlan(t)
	p2 := a.savePlan(t, func(in *inventory.AmenityPlanInput) { in.Lines = in.Lines[:2] })
	if p1.VersionNo != 1 || p2.VersionNo != 2 || len(p2.Versions) != 2 {
		t.Fatalf("versions %d %d (%d)", p1.VersionNo, p2.VersionNo, len(p2.Versions))
	}
	// a version is never edited
	if err := a.Exec(t, `UPDATE inv_amenity_plans SET is_enabled = false`); err == nil {
		t.Fatal("a plan was edited")
	}
	must(t, a.closeDay())
	run := a.run(t, a.night)
	if run.PlanVersion != 2 || len(run.Lines) != 2 { // the plan in force on the day: the newest version, saved the same day
		t.Fatalf("plan version %d with %d lines", run.PlanVersion, len(run.Lines))
	}
	// the day after, a plan that is switched off makes nothing
	a.nextBusinessDay(t)
	a.savePlan(t, func(in *inventory.AmenityPlanInput) { in.IsEnabled = false })
	must(t, a.closeDay())
	if a.Count(t, `SELECT count(*) FROM inv_amenity_runs`) != 1 {
		t.Fatal("a plan that is off made a document")
	}
	// the run of the first day keeps the version it used
	if got := a.run(t, a.night).PlanVersion; got != 2 {
		t.Fatalf("the run keeps its version: %d", got)
	}
}

func TestAPlanThatCannotRunIsRefusedWhenSavedAndABlockerWhenItBecomesInvalid(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	_, err := a.svc.SaveAmenityPlan(a.admin, a.propID, inventory.AmenityPlanInput{IsEnabled: true})
	wantCode(t, err, "AMENITY_PLAN_INVALID")
	_, err = a.svc.SaveAmenityPlan(a.admin, a.propID, inventory.AmenityPlanInput{IsEnabled: true, WarehouseID: ptr(a.hk), ExpenseAccountID: ptr(a.accounts["4110"]),
		Lines: []inventory.AmenityPlanLineInput{{ItemID: a.item["SOAP"], UnitID: a.unit["pcs"], Qty: dec("1"), Basis: inventory.BasisRoom}}}) // a revenue account
	wantCode(t, err, "AMENITY_PLAN_INVALID")
	_, err = a.svc.SaveAmenityPlan(a.admin, a.propID, inventory.AmenityPlanInput{IsEnabled: true, WarehouseID: ptr(a.hk), ExpenseAccountID: ptr(a.accounts["5150"]),
		Lines: []inventory.AmenityPlanLineInput{{ItemID: a.item["SOAP"], UnitID: a.unit["karton"], Qty: dec("1"), Basis: inventory.BasisRoom}}}) // a unit the item does not have
	wantCode(t, err, "AMENITY_PLAN_INVALID")
	a.savePlan(t)
	// the warehouse of the plan is switched off later: the close has a blocker with the cause
	must(t, a.Exec(t, `UPDATE inv_warehouses SET is_active = false WHERE id = $1`, a.hk))
	day, _ := a.Tenancy.CurrentBusinessDay(a.admin, a.propID)
	b, _, err := a.svc.NightAuditStock(a.admin, a.tenantID, a.propID, day.BusinessDate)
	must(t, err)
	if !hasFinding(b, "AMENITY_PLAN_INVALID") {
		t.Fatalf("blockers: %v", b)
	}
	wantCode(t, a.closeDay(), "AMENITY_PLAN_INVALID")
	if a.Count(t, `SELECT count(*) FROM inv_amenity_runs`) != 0 {
		t.Fatal("a run was made")
	}
	// a plan needs inventory.manage
	viewer := a.User(t, a.tenantID, a.propID, auth.PermInventoryView)
	_, err = a.svc.SaveAmenityPlan(viewer, a.propID, inventory.AmenityPlanInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = a.svc.ReverseAmenityRun(viewer, a.propID, roomstest.BD, "x")
	wantCode(t, err, "PERMISSION_DENIED")
}

// 5.9 which rooms: the arrival night counts and the departure day does not; a cancelled stay, a room that is vacant and a stay that has not arrived do not; a guest who moved room that day counts once, under the
// room of that night; a complimentary stay counts and is marked.
func TestTheRoomsOfTheNightAreTheRoomsThatWereSleptIn(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	type spec struct{ no, from, to string }
	for _, s := range []spec{{"301", "2026-09-29", a.night}, {"302", "2026-10-01", "2026-10-03"}, {"303", a.night, "2026-10-01"}, {"304", a.night, "2026-10-01"}, {"305", "2026-09-28", "2026-10-02"}} {
		room := a.Room(t, a.admin, a.propID, a.std, s.no)
		a.rooms[s.no] = room.ID
		a.stays[s.no] = a.Stay(t, a.tenantID, a.propID, a.std, room.ID, s.from, s.to)
	}
	// 303 is cancelled; 304 is complimentary; 305 moved from 305 to 306 on the night: its first segment ended that day
	must(t, a.Exec(t, `UPDATE stays SET status = 'CANCELLED' WHERE id = $1`, a.stays["303"]))
	must(t, a.Exec(t, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, occupancy_kind) SELECT tenant_id, property_id, 'FREE', 'Free', room_charge_code_id, 'COMPLIMENTARY' FROM rate_plans WHERE property_id = $1 AND code = 'BAR'`, a.propID))
	must(t, a.Exec(t, `UPDATE reservation_rooms SET rate_plan_id = (SELECT id FROM rate_plans WHERE property_id = $1 AND code = 'FREE') WHERE id = (SELECT reservation_room_id FROM stays WHERE id = $2)`, a.propID, a.stays["304"]))
	r306 := a.Room(t, a.admin, a.propID, a.std, "306")
	must(t, a.Exec(t, `UPDATE stay_rooms SET check_out_at = now(), end_business_date = $1::date WHERE stay_id = $2`, a.night, a.stays["305"]))
	must(t, a.Exec(t, `INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date) VALUES ($1, $2, $3, $4, now(), $5::date)`, a.tenantID, a.propID, a.stays["305"], r306.ID, a.night))
	rooms, err := a.Front.NightOccupancy(a.admin, a.tenantID, a.propID, roomstest.BD)
	must(t, err)
	got := map[string]string{}
	for _, r := range rooms {
		got[r.RoomNumber] = r.Kind
	}
	want := map[string]string{"101": "PAID", "102": "PAID", "103": "PAID", "201": "PAID", "304": "COMPLIMENTARY", "306": "PAID"}
	if len(got) != len(want) {
		t.Fatalf("rooms %v, want %v", got, want)
	}
	for no, kind := range want {
		if got[no] != kind {
			t.Fatalf("room %s: %q, want %q (all: %v)", no, got[no], kind, got)
		}
	}
	for _, no := range []string{"301", "302", "303", "305"} { // departure day, not yet arrived, cancelled, the room the guest left that day
		if _, ok := got[no]; ok {
			t.Fatalf("room %s was counted", no)
		}
	}
	a.savePlan(t)
	must(t, a.closeDay())
	run := a.run(t, a.night)
	if run.Rooms != 6 || run.CompRooms != 1 || run.Guests != 12 {
		t.Fatalf("run: %d rooms, %d complimentary, %d guests", run.Rooms, run.CompRooms, run.Guests)
	}
}

// ---------------------------------------------------------------- the close inside a count (D24)

func (a *afx) countHK(t *testing.T) inventory.Count {
	t.Helper()
	return a.startCount(t, "HK", inventory.ScopeAll)
}

func (a *afx) closeHeld(t *testing.T) inventory.AmenityRun {
	t.Helper()
	a.settings(t, func(in *inventory.SettingsInput) { in.CountBlocksNightAudit = false })
	must(t, a.closeDay())
	return a.run(t, a.night)
}

// The close falls in a count that the property lets pass: the document is written with its lines held, nothing moves, nothing is journaled, and the close says AMENITY_HELD.
func TestTheCloseThatFallsInACountWritesTheDocumentAndHoldsItsLines(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	a.savePlan(t)
	c := a.countHK(t)
	a.settings(t, func(in *inventory.SettingsInput) { in.CountBlocksNightAudit = false })
	day, _ := a.Tenancy.CurrentBusinessDay(a.admin, a.propID)
	b, w, err := a.svc.NightAuditStock(a.admin, a.tenantID, a.propID, day.BusinessDate)
	must(t, err)
	if hasFinding(b, "AMENITY_PLAN_INVALID") || hasFinding(b, "INVENTORY_COUNT_OPEN") || !hasFinding(w, "AMENITY_HELD") || !hasFinding(w, "INVENTORY_COUNT_OPEN") {
		t.Fatalf("blockers %v warnings %v", b, w)
	}
	must(t, a.closeDay())
	run := a.run(t, a.night)
	if run.HeldLines != 4 || run.Rooms != 4 || !run.TotalValue.IsZero() || run.HeldByCount == nil || *run.HeldByCount != c.Number {
		t.Fatalf("run: held %d total %s by %v", run.HeldLines, run.TotalValue, run.HeldByCount)
	}
	if a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE'`) != 0 {
		t.Fatal("a held line moved stock")
	}
	a.wantBal(t, a.pair("HK", "TBRUSH"), "20", "42000")
	if len(run.Journal) != 0 {
		t.Fatalf("journal of a held document: %+v", run.Journal)
	}
	// a reversal is refused while lines are held
	_, err = a.svc.ReverseAmenityRun(a.admin, a.propID, roomstest.BD, "no")
	wantCode(t, err, "AMENITY_RUN_HELD")
	// the count sheet shows the document that waits
	cc, err := a.svc.GetCount(a.admin, a.propID, c.ID)
	must(t, err)
	if len(cc.HeldDocuments) != 1 || cc.HeldDocuments[0].Number != "AMN-20260930" || cc.HeldDocuments[0].HeldLines != 4 {
		t.Fatalf("held documents: %+v", cc.HeldDocuments)
	}
	// a count of another warehouse holds nothing: the main store's count does not touch the amenities
	_, err = a.svc.CancelCount(a.cnt, a.propID, c.ID)
	must(t, err)
	if a.run(t, a.night).HeldLines != 0 || a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE'`) != 4 {
		t.Fatal("a cancel applies the held lines")
	}
	a.wantBal(t, a.pair("HK", "TBRUSH"), "12", "25200")
	a.wantBal(t, a.pair("HK", "SOAP"), "-1", "-2960")
	a.consistent(t)
}

// 8.11 variant 1: the item was counted BEFORE the close: its count is not shortened by the amenities, they are applied after the adjustment.
func TestAHeldLineOfAnItemCountedBeforeTheCloseIsAppliedAfterTheCount(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a := newAmenities(t, method)
			a.savePlan(t)
			c := a.countHK(t)
			a.Clock.Advance(10 * time.Minute)
			c = a.found(t, c, "SOAP", "2") // the shelf holds 2 of the 3 the book says
			a.Clock.Advance(10 * time.Minute)
			a.closeHeld(t)
			c = a.submitCount(t, c)
			if l := lineOf(t, c, a.item["SOAP"]); !l.VarianceQty.Equal(dec("-1")) || !l.HeldBeforeQty.IsZero() {
				t.Fatalf("preview: %+v", l)
			}
			a.Clock.Advance(time.Hour)
			c = a.approveCount(t, c)
			// -1 x 2,960 as a loss, then the soaps of the night: 4 needed, 2 on the shelf: 2 at the cost and 2 at the estimate
			a.wantBal(t, a.pair("HK", "SOAP"), "-2", "-5920")
			if l := lineOf(t, c, a.item["SOAP"]); !l.ExpectedQty.Equal(dec("3")) || !l.VarianceQty.Equal(dec("-1")) {
				t.Fatalf("line: %+v", l)
			}
			// the other lines were not counted: applied after the count, with the night as source date
			a.wantBal(t, a.pair("HK", "TBRUSH"), "12", "25200")
			if a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE' AND source_date = '2026-09-30'`) != 4 {
				t.Fatal("the held movements keep the night as source_date")
			}
			if after := a.run(t, a.night); after.HeldLines != 0 || !after.TotalValue.Equal(dec("44890")) {
				t.Fatalf("run after: %+v", a.run(t, a.night).AmenityRunBrief)
			}
			a.consistent(t)
		})
	}
}

// 8.11 variant 2: the item was counted AFTER the close: the amenities are already missing from the shelf the counter looked at, they lower what the count expects and are applied first.
func TestAHeldLineOfAnItemCountedAfterTheCloseLowersWhatTheCountExpects(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			a := newAmenities(t, method)
			a.savePlan(t)
			c := a.countHK(t)
			a.Clock.Advance(10 * time.Minute)
			a.closeHeld(t)
			a.Clock.Advance(10 * time.Minute)
			c = a.found(t, c, "SOAP", "2") // the shelf holds 2 after the amenities were taken
			c = a.submitCount(t, c)
			l := lineOf(t, c, a.item["SOAP"])
			if !l.HeldBeforeQty.Equal(dec("-4")) || !l.ExpectedQty.Equal(dec("-1")) || !l.VarianceQty.Equal(dec("3")) {
				t.Fatalf("preview: held %s expected %s variance %s", l.HeldBeforeQty, l.ExpectedQty, l.VarianceQty)
			}
			a.approveCount(t, c)
			// the line is applied first (3 on the shelf, 1 at the estimate: -1), then the count finds 2: a gain of 3 that closes the shortfall; the balance is the quantity counted, at cost
			a.wantBal(t, a.pair("HK", "SOAP"), "2", "5920")
			if n := a.Count(t, `SELECT count(*) FROM inv_shortfalls WHERE warehouse_id = $1 AND status = 'CLOSED_BY_COUNT'`, a.hk); n != 1 {
				t.Fatalf("%d shortfalls closed by the count", n)
			}
			if a.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'NEG_SETTLE'`) != 0 {
				t.Fatal("a count closes a shortfall without a settlement")
			}
			if a.run(t, a.night).HeldLines != 0 {
				t.Fatal("the held lines are applied")
			}
			a.consistent(t)
		})
	}
}

// Nothing is applied twice, and a second close inside the same count makes a second document, applied in the order of the days.
func TestTwoClosesInsideOneCountMakeTwoDocumentsAppliedInOrder(t *testing.T) {
	a := newAmenities(t, inventory.MethodAverage)
	a.savePlan(t)
	c := a.countHK(t)
	a.closeHeld(t)
	a.nextBusinessDay(t)
	must(t, a.closeDay())
	if a.Count(t, `SELECT count(*) FROM inv_issues WHERE origin = 'AMENITY'`) != 2 {
		t.Fatal("one document for each close")
	}
	a.Clock.Advance(time.Hour)
	c = a.found(t, c, "TBRUSH", "4") // the shelf after both nights
	c = a.submitCount(t, c)
	a.nextBusinessDay(t)
	a.approveCount(t, c)
	// 8 toothbrushes for each night: the first document is applied first
	a.wantBal(t, a.pair("HK", "TBRUSH"), "4", "8400")
	rows, err := a.Pool.Query(context.Background(), `SELECT i.iss_number, m.business_date::text, m.source_date::text FROM inv_movements m JOIN inv_issues i ON i.id = m.source_id AND m.source_type = 'INV_ISSUE' WHERE m.item_id = $1 ORDER BY m.id`, a.item["TBRUSH"])
	must(t, err)
	defer rows.Close()
	var order []string
	for rows.Next() {
		var n, d, s string
		must(t, rows.Scan(&n, &d, &s))
		order = append(order, n+" on "+d+" for "+s)
	}
	if len(order) != 2 || order[0] != "AMN-20260930 on 2026-10-02 for 2026-09-30" || order[1] != "AMN-20261001 on 2026-10-02 for 2026-10-01" {
		t.Fatalf("order: %v", order)
	}
	a.consistent(t)
}

// A race with a barrier: the entry of the quantity of an item (which stamps counted_at) and the close at the same time. Whichever comes first, the amenities are applied once and the book equals the
// quantity counted less only what falls after counted_at.
func TestTheEntryOfACountAndTheCloseAtTheSameTimeApplyTheAmenitiesOnce(t *testing.T) {
	for i := 0; i < 3; i++ {
		a := newAmenities(t, inventory.MethodAverage)
		a.savePlan(t)
		a.settings(t, func(in *inventory.SettingsInput) { in.CountBlocksNightAudit = false })
		c := a.countHK(t)
		q := dec("20")
		errs := race(
			func() error {
				_, err := a.svc.EnterCountLines(a.cnt, a.propID, c.ID, []inventory.CountLineInput{{ItemID: a.item["TBRUSH"], Qty: &q}})
				return err
			},
			a.closeDay,
		)
		if succeeded(errs) != 2 {
			t.Fatalf("%v", errs)
		}
		c = a.submitCount(t, c)
		c = a.approveCount(t, c)
		// with the clock of the server standing still the close and the entry have the same instant: the line counts as after (4.7), so the book is 20 less the 8 of the night
		a.wantBal(t, a.pair("HK", "TBRUSH"), "12", "25200")
		if a.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'INV_ISSUE' AND item_id = $1`, a.item["TBRUSH"]) != 1 {
			t.Fatal("the amenities were applied more than once")
		}
		a.consistent(t)
	}
}
