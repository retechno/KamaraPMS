package frontdesk_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
)

// The in-house read model: every figure comes from the ledger (the folio balances) or from the snapshot of the booking (the price of the night), never from the rate master.

func TestInHouseShowsGuestRoomRateAndBalance(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	out, err := f.checkIn(t, f.admin, res, &f.r101, "")
	must(t, err)
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.ID != out.Stay.ID || r.Guest.ID != f.guest || r.Guest.Name != "Guest" || r.Room.Number != "101" || r.Room.RoomTypeCode != "DLX" || r.ConfirmationNumber != res.ConfirmationNumber {
		t.Fatalf("identity: %+v", r)
	}
	if r.Company != nil || len(r.Billing) != 0 {
		t.Fatalf("a stay without an instruction has no company: %+v", r)
	}
	if r.Rate.RatePlanCode != "BAR" || r.Rate.Amount != "1000000" || r.Rate.PriceMode == "" {
		t.Fatalf("rate: %+v", r.Rate)
	}
	if r.Stay.Nights != 2 || r.Stay.Adults != 2 || r.Stay.Children != 0 || r.Stay.Arrival != d("2026-09-30") || r.Stay.Departure != d("2026-10-02") {
		t.Fatalf("stay: %+v", r.Stay)
	}
	if r.Balance.Amount != "0" || r.Balance.Status != "SETTLED" || len(r.Balance.Folios) != 1 || r.Balance.Folios[0].ID != out.Folio.ID {
		t.Fatalf("balance: %+v", r.Balance)
	}
}

func TestInHouseBalanceIsTheFolioBalancesAndKeepsTheCompanyFolioApart(t *testing.T) {
	s := withCompanyFolio(t)
	s.pay(t, s.stay.Folio.ID, "p-guest", "300000")
	s.pay(t, s.companyFol, "p-company", "100000")
	rows, err := s.Front.ListInHouse(s.admin, s.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.Company == nil || r.Company.ID != s.company || r.Company.Name != "Acme Corp" {
		t.Fatalf("company: %+v", r.Company)
	}
	if len(r.Billing) != 1 || r.Billing[0].Scope != "ROOM" || r.Billing[0].CompanyName != "Acme Corp" {
		t.Fatalf("billing: %+v", r.Billing)
	}
	if len(r.Balance.Folios) != 2 || r.Balance.Folios[0].FolioType != "GUEST" || r.Balance.Folios[1].FolioType != "COMPANY" {
		t.Fatalf("folios: %+v", r.Balance.Folios)
	}
	for _, fo := range r.Balance.Folios {
		got, want := decimal.RequireFromString(fo.Balance), decimal.RequireFromString(s.balance(t, fo.ID))
		if !got.Equal(want) {
			t.Fatalf("folio %d: balance %s, ledger %s", fo.ID, fo.Balance, want)
		}
	}
	if r.Balance.Amount != "-400000" || r.Balance.Status != "CREDIT" {
		t.Fatalf("balance: %+v", r.Balance)
	}
}

func TestInHouseCompanyOfAnEmptyInstructionSetComesFromItsCompanyFolio(t *testing.T) {
	s := withCompanyFolio(t)
	must(t, s.Exec(t, `DELETE FROM folio_billing_instructions WHERE reservation_room_id = $1`, s.res.Rooms[0].ID))
	rows, err := s.Front.ListInHouse(s.admin, s.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Company == nil || rows[0].Company.Name != "Acme Corp" || len(rows[0].Billing) != 0 {
		t.Fatalf("company folio: %+v", rows)
	}
}

func TestInHouseRateIsTheSnapshotNotTheRateMaster(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err := f.checkIn(t, f.admin, res, &f.r101, "")
	must(t, err)
	_, err = f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "2500000"})
	must(t, err)
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Rate.Amount != "1000000" {
		t.Fatalf("the price of the booking must not follow the master: %+v", rows)
	}
}

func TestInHouseListsOnlyOpenStaysAndPages(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err := f.checkIn(t, f.admin, a, &f.r101, "")
	must(t, err)
	b := f.book(t, f.std, "2026-09-30", "2026-10-02")
	second, err := f.checkIn(t, f.admin, b, &f.r201, "")
	must(t, err)
	first, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 1)
	must(t, err)
	if len(first) != 1 || first[0].ID != second.Stay.ID || first[0].Rate.Amount != "500000" || first[0].Room.RoomTypeCode != "STD" {
		t.Fatalf("first page (newest first): %+v", first)
	}
	next, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, first[0].ID, 10)
	must(t, err)
	if len(next) != 1 || next[0].Room.Number != "101" {
		t.Fatalf("next page: %+v", next)
	}
	must(t, f.Exec(t, `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, second.Stay.ID))
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Room.Number != "101" {
		t.Fatalf("a stay that left is not in house: %+v", rows)
	}
}

func TestInHouseIsScopedAndAuthorized(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err := f.checkIn(t, f.admin, res, &f.r101, "")
	must(t, err)
	// another property of the same tenant does not show it, and another tenant cannot ask for this property
	ubud := f.Property(t, f.tenantID, "UBUD")
	rows, err := f.Front.ListInHouse(f.admin, ubud.ID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 0 {
		t.Fatalf("another property: %+v", rows)
	}
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	if _, err := f.Front.ListInHouse(stranger, f.propID, frontdesk.InHouseFilter{}, 0, 50); err == nil {
		t.Fatal("another tenant must not read the in-house list")
	}
	noRead := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Front.ListInHouse(noRead, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	wantCode(t, err, "PERMISSION_DENIED")
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	if rows, err := f.Front.ListInHouse(reader, f.propID, frontdesk.InHouseFilter{}, 0, 50); err != nil || len(rows) != 1 {
		t.Fatalf("a reader sees the list: %v %+v", err, rows)
	}
}
