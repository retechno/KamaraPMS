package roomcharge_test

import (
	"context"
	"strings"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/iam"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

// stayBilledTo books a room, gives its line the instructions, then checks in: the way a corporate booking goes.
func (f *fx) stayBilledTo(t *testing.T, room rooms.Room, in ...folios.InstructionInput) (frontdesk.CheckInResult, int64) {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: roomstest.BD, Departure: d("2026-10-02"), Adults: 2},
	}})
	must(t, err)
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, in)
	must(t, err)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out, res.Rooms[0].ID
}

func (f *fx) company(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, $3, $3, 100000000) RETURNING id`, f.tenantID, f.propID, code).Scan(&id))
	return id
}

func (f *fx) companyFolioID(t *testing.T, stayID, company int64) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE stay_id = $1 AND bill_to_company_id = $2`, stayID, company).Scan(&id))
	return id
}

// The room goes to the company folio the check-in opened; the guest folio stays empty; the register still says one posting for the night.
func TestRoomNightGoesToTheCompanyFolio(t *testing.T) {
	f := setup(t)
	acme := f.company(t, "ACME")
	st, _ := f.stayBilledTo(t, f.r101, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: acme})
	cf := f.companyFolioID(t, st.Stay.ID, acme)

	if got := f.preview(t); len(got) != 1 {
		t.Fatalf("preview: %v", got)
	}
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(res.Results) != 1 || res.Results[0].Status != "POSTED" {
		t.Fatalf("post: %+v", res.Results)
	}
	if n := f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE'`, cf); n != 1 {
		t.Fatalf("charges on the company folio: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, st.Folio.ID); n != 0 {
		t.Fatalf("items on the guest folio: %d", n)
	}
	// a second run posts nothing: the night is registered once, whatever the routing
	res, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM folio_items`) != 1 || f.Count(t, `SELECT count(*) FROM stay_charge_postings`) != 1 {
		t.Fatalf("second run: %+v", res.Results)
	}
}

// A routed night whose company folio is closed is an error and is never posted to the guest folio instead.
func TestClosedCompanyFolioBlocksTheNight(t *testing.T) {
	f := setup(t)
	acme := f.company(t, "ACME")
	st, _ := f.stayBilledTo(t, f.r101, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: acme})
	cf := f.companyFolioID(t, st.Stay.ID, acme)
	must(t, f.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, cf))

	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if res.Results[0].Status != "ERROR" || res.Results[0].Reason != "ROUTING_TARGET_CLOSED" {
		t.Fatalf("result: %+v", res.Results[0])
	}
	if len(res.Revalidation.Errors) != 1 || res.Revalidation.Errors[0].Reason != "ROUTING_TARGET_CLOSED" {
		t.Fatalf("revalidation: %+v", res.Revalidation)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items`) != 0 {
		t.Fatal("nothing may be posted, not even to the guest folio")
	}
	// the preview says the same
	pv, err := f.Charges.Preview(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if pv.Items[0].Reason != "ROUTING_TARGET_CLOSED" {
		t.Fatalf("preview: %+v", pv.Items[0])
	}
}

// An instruction added after a night was posted applies from the next night: the posted night stays where it is.
func TestInstructionAppliesFromTheNextNight(t *testing.T) {
	f := setup(t)
	acme := f.company(t, "ACME")
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: roomstest.BD, Departure: d("2026-10-03"), Adults: 2},
	}})
	must(t, err)
	st, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &f.r101.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	_, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	f.nextDay(t)
	// the guest is in house: the instruction opens the company folio at once
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeAll, CompanyID: acme}})
	must(t, err)
	cf := f.companyFolioID(t, st.Stay.ID, acme)
	_, err = f.Charges.PostManual(f.admin, f.propID, f.currentBD(t), nil)
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, st.Folio.ID) != 1 || f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, cf) != 1 {
		t.Fatalf("guest folio %d items, company folio %d items", f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, st.Folio.ID), f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, cf))
	}
}

// A room night moved to the company folio stays posted once: the register keeps one POSTED row for it, so no run charges it again, and the ledger holds the copy.
func TestMovedRoomNightIsNotChargedAgain(t *testing.T) {
	f := setup(t)
	acme := f.company(t, "ACME")
	st := f.stay(t, f.r101, "2026-10-02")
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	item := *res.Results[0].FolioItemID
	var reservationID, lineID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT rr.reservation_id, rr.id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, st.Stay.ID).Scan(&reservationID, &lineID))
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, reservationID, lineID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: acme}})
	must(t, err)
	cf := f.companyFolioID(t, st.Stay.ID, acme)

	moved, err := f.Folios.TransferItem(f.admin, f.propID, item, folios.TransferItemInput{FolioID: cf, Reason: "the company pays the room", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}})
	must(t, err)
	if moved.Charge.FolioBalance != "1221000" || moved.Reversal.FolioBalance != "0" {
		t.Fatalf("balances: %+v %+v", moved.Reversal.FolioBalance, moved.Charge.FolioBalance)
	}
	// the night is still posted: the preview has nothing to charge and a run posts nothing
	for _, line := range f.preview(t) {
		if !strings.HasSuffix(line, ":ALREADY_POSTED") {
			t.Fatalf("preview: %v", f.preview(t))
		}
	}
	again, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if again.Revalidation.Ready != 0 || len(again.Revalidation.Errors) != 0 || len(again.Revalidation.Invalid) != 0 {
		t.Fatalf("revalidation: %+v", again.Revalidation)
	}
	if n := f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`); n != 2 {
		t.Fatalf("charges in the ledger (the original and the copy): %d", n)
	}
	// the register: one POSTED row, for the copy; the original's row is REVERSED
	if f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED' AND folio_item_id = $1`, moved.Charge.Item.ID) != 1 ||
		f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'REVERSED' AND folio_item_id = $1`, item) != 1 ||
		f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`) != 1 {
		t.Fatal("the register must hold one POSTED row, for the copy")
	}
	// the room nights of the day are counted once
	if n := f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED' AND service_date = $1::date`, roomstest.BD.String()); n != 1 {
		t.Fatalf("room nights of the day: %d", n)
	}
}
