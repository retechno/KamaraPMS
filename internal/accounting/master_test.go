package accounting_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// The master folio of a group in the night audit and in the books (docs/architecture/30-group-folio.md, part C).

// groupOf books and checks in one room for each room given, puts the reservations in a new group and returns the stays with their reservations.
type member struct {
	res reservations.Reservation
	st  frontdesk.CheckInResult
}

func (h *hotel) groupOfMembers(t *testing.T, arrival, departure string, n int) (int64, []member) {
	t.Helper()
	third := h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	var group int64
	must(t, h.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date) VALUES ($1, $2, 'GRP1', 'Conference', $3, $4) RETURNING id`,
		h.tenantID, h.propID, arrival, departure).Scan(&group))
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	var out []member
	for i := 0; i < n; i++ {
		res := h.book(t, arrival, departure)
		must(t, h.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, res.ID))
		room := h.r101
		switch i {
		case 1:
			room = h.r102
		case 2:
			room = third
		}
		out = append(out, member{res: res, st: h.checkIn(t, res, room)})
	}
	return group, out
}

func (h *hotel) route(t *testing.T, group int64, excluded ...int64) {
	t.Helper()
	_, err := h.Folios.SetGroupRouting(h.admin, h.propID, group, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeRoom}}, ExcludedLines: excluded})
	must(t, err)
}

func (h *hotel) masterOf(t *testing.T, group int64) int64 {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE booking_group_id = $1 AND folio_type = 'MASTER'`, group).Scan(&id))
	return id
}

func TestTheNightAuditPostsTheRoomsOfAGroupToItsMasterFolio(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-03", 3)
	h.route(t, group, ms[2].res.Rooms[0].ID) // the third member stays out of the routing
	master := h.masterOf(t, group)

	h.closeDay(t) // day 1: the three nights
	roomItems := func(folio int64) int {
		return h.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'ROOM_POSTING' AND transaction_type = 'CHARGE'`, folio)
	}
	if roomItems(master) != 2 || roomItems(ms[0].st.Folio.ID) != 0 || roomItems(ms[1].st.Folio.ID) != 0 || roomItems(ms[2].st.Folio.ID) != 1 {
		t.Fatalf("nights: master %d, members %d %d %d (the third is excluded: one night on its own folio)", roomItems(master), roomItems(ms[0].st.Folio.ID), roomItems(ms[1].st.Folio.ID), roomItems(ms[2].st.Folio.ID))
	}
	if h.folioBalance(t, master).String() != "2000000" || !h.folioBalance(t, ms[0].st.Folio.ID).IsZero() || h.folioBalance(t, ms[2].st.Folio.ID).String() != "1000000" {
		t.Fatalf("balances: master %s, first member %s, third member %s", h.folioBalance(t, master), h.folioBalance(t, ms[0].st.Folio.ID), h.folioBalance(t, ms[2].st.Folio.ID))
	}
	// the night of a stay routed to the master is still the night of that stay: the register is keyed by the stay and the line says whose night it is
	if n := h.Count(t, `SELECT count(*) FROM folio_items i JOIN stays s ON s.id = i.stay_id WHERE i.folio_id = $1 AND i.stay_room_id IS NOT NULL`, master); n != 2 {
		t.Fatalf("%d lines of the master carry their stay", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`); n != 3 {
		t.Fatalf("%d postings in the register, want 3", n)
	}
	// one set of entries in the journal: the revenue is posted once, whichever folio carries the night
	if got := h.balance(t, "4110"); !got.Equal(dec("-3000000")) {
		t.Fatalf("room revenue %s", got)
	}
	if got := h.balance(t, "1210"); !got.Equal(dec("3000000")) {
		t.Fatalf("guest ledger %s", got)
	}
	h.requireBalanced(t)

	// day 2: the register keeps the nights of day 1 from being posted again
	h.closeDay(t)
	if roomItems(master) != 4 || roomItems(ms[2].st.Folio.ID) != 2 || h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`) != 6 {
		t.Fatalf("day 2: master %d, third %d, register %d", roomItems(master), roomItems(ms[2].st.Folio.ID), h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`))
	}
	if got := h.balance(t, "4110"); !got.Equal(dec("-6000000")) {
		t.Fatalf("room revenue after day 2: %s", got)
	}
	asOf := civil.MustParseDate("2026-10-01")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("the reconciliation agrees with the master: %+v", rec)
	}
}

func TestAClosedMasterStopsTheNightAuditBeforeAnythingIsPosted(t *testing.T) {
	h := setupHotel(t)
	group, _ := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
	h.route(t, group)
	master := h.masterOf(t, group)
	must(t, h.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, master)) // a mistaken close, repaired later by a reopen
	pre, err := h.Audit.Preview(h.admin, h.propID)
	must(t, err)
	found := false
	for _, e := range pre.Blockers.ChargeErrors {
		found = found || e.Reason == "ROUTING_TARGET_CLOSED"
	}
	if !found || pre.CanRun {
		t.Fatalf("the preview must name the blocker: %+v", pre.Blockers.ChargeErrors)
	}
	before := h.sideEffects(t)
	err = h.runDay(t)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "NIGHT_AUDIT_BLOCKED" {
		t.Fatalf("the night audit must be blocked: %v", err)
	}
	if after := h.sideEffects(t); after != before {
		t.Fatalf("nothing is posted: %v -> %v", before, after)
	}
	if n := h.Count(t, `SELECT count(*) FROM folio_items WHERE source = 'ROOM_POSTING'`); n != 0 {
		t.Fatalf("%d room nights posted to someone's folio", n)
	}
	// repaired: the master is open again and the night audit runs
	must(t, h.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, master))
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'ROOM_POSTING'`, master); n != 2 {
		t.Fatalf("%d nights on the master", n)
	}
}

// A payment on the master before its first charge is an advance deposit and after it a settlement; the books and the reconciliation agree through the close of the master and the release of the deposit.
func TestTheDepositAndTheSettlementOfTheMasterAgreeWithTheBooks(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-01", 2)
	h.route(t, group)
	master := h.masterOf(t, group)
	asOf := civil.MustParseDate("2026-10-01")
	reconciled := func(when string) {
		t.Helper()
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("%s: the reconciliation must agree: %+v", when, rec)
		}
	}
	_, err := h.Folios.PostPayment(h.admin, h.propID, master, "dep", folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"})
	must(t, err)
	h.closeDay(t) // the two nights go to the master, the deposit is held
	if got := h.balance(t, "2310"); !got.Equal(dec("-500000")) {
		t.Fatalf("advance deposits: %s", got)
	}
	if got := h.balance(t, "1210"); !got.Equal(dec("2000000")) {
		t.Fatalf("guest ledger carries the master: %s", got)
	}
	reconciled("after day 1")

	// the rest is paid after the first charge: a settlement, not a deposit
	_, err = h.Folios.PostPayment(h.admin, h.propID, master, "rest", folios.PaymentInput{Amount: "1500000", PaymentMethod: "CARD"})
	must(t, err)
	for _, m := range ms {
		st, err := h.Front.GetStay(h.admin, h.propID, m.st.Stay.ID)
		must(t, err)
		_, err = h.Front.CheckOut(h.admin, h.propID, m.st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
		must(t, err)
	}
	info, err := h.Folios.GetFolio(h.admin, h.propID, master)
	must(t, err)
	if info.Balance != "0" {
		t.Fatalf("master balance %s", info.Balance)
	}
	closed, err := h.Folios.CloseFolio(h.admin, h.propID, master, info.Version)
	must(t, err)
	if closed.Status != "CLOSED" {
		t.Fatalf("closed: %+v", closed)
	}
	h.closeDay(t) // the deposit of the closed master is released
	if got := h.balance(t, "2310"); !got.IsZero() {
		t.Fatalf("advance deposits after the close: %s", got)
	}
	if got := h.balance(t, "1210"); !got.IsZero() {
		t.Fatalf("guest ledger after the close: %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines WHERE source_type = 'DEPOSIT_RELEASE'`); n != 2 {
		t.Fatalf("one release of the deposit has two lines, got %d", n)
	}
	h.requireBalanced(t)
	asOf = civil.MustParseDate("2026-10-02")
	reconciled("after the master closed")
}

// The last member to check out is told the group still has an open bill; the others are not, and nothing is blocked.
func TestTheLastCheckOutOfAGroupSaysTheMasterIsOpen(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-01", 2)
	h.route(t, group)
	h.closeDay(t)
	out := func(m member) frontdesk.CheckOutResult {
		st, err := h.Front.GetStay(h.admin, h.propID, m.st.Stay.ID)
		must(t, err)
		r, err := h.Front.CheckOut(h.admin, h.propID, m.st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
		must(t, err)
		return r
	}
	first := out(ms[0])
	if first.MasterFolio != nil {
		t.Fatalf("a member is still in house: %+v", first.MasterFolio)
	}
	last := out(ms[1])
	if last.MasterFolio == nil || last.MasterFolio.FolioID != h.masterOf(t, group) || last.MasterFolio.Balance != "2000000" || last.MasterFolio.GroupCode != "GRP1" {
		t.Fatalf("the last check-out: %+v", last.MasterFolio)
	}
	if n := h.Count(t, `SELECT count(*) FROM folios WHERE folio_type = 'MASTER' AND status = 'OPEN'`); n != 1 {
		t.Fatal("the master is left open")
	}
}

// A change of the routing and a posting run take turns: the night audit holds the stays of the members and the change locks them too, so the nights are either all posted before the change (and stay on
// the guest folios) or all after it (and go to the master), never split, and neither of them fails on the lock order.
func TestAChangeOfTheRoutingAndTheNightAuditTakeTurns(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-03", 2)
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	h.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	var wg sync.WaitGroup
	var runErr, routeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, runErr = h.Audit.Run(h.admin, h.propID, day.BusinessDate)
	}()
	go func() {
		defer wg.Done()
		_, routeErr = h.Folios.SetGroupRouting(h.admin, h.propID, group, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeRoom}}})
	}()
	wg.Wait()
	if runErr != nil || routeErr != nil {
		t.Fatalf("both finish: the night audit %v, the routing %v", runErr, routeErr)
	}
	var onMaster, onGuests int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folio_items i JOIN folios f ON f.id = i.folio_id WHERE i.source = 'ROOM_POSTING' AND f.folio_type = 'MASTER'`).Scan(&onMaster))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folio_items i JOIN folios f ON f.id = i.folio_id WHERE i.source = 'ROOM_POSTING' AND f.folio_type = 'GUEST'`).Scan(&onGuests))
	if onMaster+onGuests != 2 || (onMaster != 0 && onMaster != 2) {
		t.Fatalf("the two nights of day 1 are posted whole: %d on the master, %d on the guest folios", onMaster, onGuests)
	}
	// whichever came first, the next night goes where the routing says
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM folio_items i JOIN folios f ON f.id = i.folio_id WHERE i.source = 'ROOM_POSTING' AND f.folio_type = 'MASTER'`); n != onMaster+2 {
		t.Fatalf("the nights after the change go to the master: %d, want %d", n, onMaster+2)
	}
	_ = ms
}
