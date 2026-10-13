package accounting_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/listsort"
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

func TestAClosedMasterDoesNotStopTheNightAuditTheNightsGoToTheGuestFolios(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
	h.route(t, group)
	master := h.masterOf(t, group)
	must(t, h.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, master)) // a mistaken close, repaired later by a reopen
	pre, err := h.Audit.Preview(h.admin, h.propID)
	must(t, err)
	if len(pre.Blockers.ChargeErrors) != 0 || !pre.CanRun {
		t.Fatalf("a closed master is not a blocker: %+v", pre.Blockers.ChargeErrors)
	}
	if w := pre.Warnings.ClosedMasters; len(w) != 1 || w[0].GroupCode != "GRP1" || w[0].Stays != 2 || w[0].FolioNumber == "" {
		t.Fatalf("the preview warns: %+v", pre.Warnings.ClosedMasters)
	}
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'ROOM_POSTING'`, master); n != 0 {
		t.Fatalf("%d nights on a closed master", n)
	}
	for _, m := range ms {
		if h.folioBalance(t, m.st.Folio.ID).String() != "1000000" {
			t.Fatalf("the night is on the guest folio: %s", h.folioBalance(t, m.st.Folio.ID))
		}
	}
	if got := h.balance(t, "4110"); !got.Equal(dec("-2000000")) {
		t.Fatalf("room revenue %s", got)
	}
	h.requireBalanced(t)
	// repaired: the master is open again and the next night goes to it
	must(t, h.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, master))
	pre, err = h.Audit.Preview(h.admin, h.propID)
	must(t, err)
	if len(pre.Warnings.ClosedMasters) != 0 {
		t.Fatalf("no warning once it is open: %+v", pre.Warnings.ClosedMasters)
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

// The deposit rule of the master across days (docs/architecture/30-group-folio.md 5.2): a payment taken before the first charge of the master is an advance deposit and stays one when the charges come;
// one taken after it is a settlement; the deposit is released (2310 to 1210) when the master closes; the books and the reconciliation agree on every day.
func TestTheMasterDepositRuleAcrossDaysAndItsReleaseAtTheClose(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
	h.route(t, group)
	master := h.masterOf(t, group)
	reconcile := func(date, when string) {
		t.Helper()
		asOf := civil.MustParseDate(date)
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("%s: %+v", when, rec)
		}
	}
	pay := func(amount, method, key string) {
		t.Helper()
		_, err := h.Folios.PostPayment(h.admin, h.propID, master, key, folios.PaymentInput{Amount: amount, PaymentMethod: method})
		must(t, err)
	}
	held := func() string { return h.balance(t, "2310").String() }

	// day 1: the deposit comes before any charge; the nights of the day are charged at the close
	pay("400000", "CARD", "dep")
	h.closeDay(t)
	if held() != "-400000" || h.balance(t, "1210").String() != "2000000" {
		t.Fatalf("day 1: deposits %s, guest ledger %s", held(), h.balance(t, "1210"))
	}
	reconcile("2026-09-30", "day 1")

	// day 2: a payment after the first charge is a settlement, and the deposit of day 1 stays a deposit
	pay("600000", "CARD", "settle")
	h.closeDay(t)
	if held() != "-400000" {
		t.Fatalf("day 2 leaves the deposit alone: %s", held())
	}
	if got := h.balance(t, "1210"); !got.Equal(dec("3400000")) { // 4,000,000 of nights less the 600,000 settled (the deposit is not in the guest ledger)
		t.Fatalf("day 2: guest ledger %s, want 3400000", got)
	}
	reconcile("2026-10-01", "day 2")

	// day 3: the members leave, the rest is paid, the master closes and the deposit is released at the next close
	for _, m := range ms {
		st, err := h.Front.GetStay(h.admin, h.propID, m.st.Stay.ID)
		must(t, err)
		_, err = h.Front.CheckOut(h.admin, h.propID, m.st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
		must(t, err)
	}
	pay("3000000", "CARD", "rest")
	info, err := h.Folios.GetFolio(h.admin, h.propID, master)
	must(t, err)
	if info.Balance != "0" {
		t.Fatalf("the master balance %s", info.Balance)
	}
	_, err = h.Folios.CloseFolio(h.admin, h.propID, master, info.Version)
	must(t, err)
	h.closeDay(t)
	if held() != "0" || !h.balance(t, "1210").IsZero() {
		t.Fatalf("after the release: deposits %s, guest ledger %s", held(), h.balance(t, "1210"))
	}
	h.requireBalanced(t)
	reconcile("2026-10-02", "after the close of the master")
}

// A payment taken after a charge that is then reversed the same day was taken before the first charge that stands: a deposit. The view says so before the close and the close journals it so, and
// the reconciliation agrees.
func TestADepositWhoseFirstChargeIsReversedTheSameDay(t *testing.T) {
	h := setupHotel(t)
	group, _ := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 1)
	master := h.openMasterOf(t, group)
	var minibar int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, h.propID).Scan(&minibar))
	unit := "100000"
	res, err := h.Folios.PostCharge(h.admin, h.propID, master, "c1", folios.ChargeInput{ChargeCodeID: minibar, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	pay, err := h.Folios.PostPayment(h.admin, h.propID, master, "p1", folios.PaymentInput{Amount: "50000", PaymentMethod: "CARD"})
	must(t, err)
	isDeposit := func() bool {
		var b bool
		must(t, h.Pool.QueryRow(context.Background(), `SELECT is_deposit FROM folio_item_gl WHERE payment_id = $1 AND item_id = (SELECT min(id) FROM folio_items WHERE payment_id = $1)`, pay.Payment.ID).Scan(&b))
		return b
	}
	if isDeposit() {
		t.Fatal("a payment after a charge that stands is a settlement")
	}
	_, err = h.Folios.Reverse(h.admin, h.propID, res.Item.ID, folios.CorrectionInput{Reason: "wrong guest", Approval: h.approval()})
	must(t, err)
	if !isDeposit() {
		t.Fatal("with the only charge reversed the payment was taken before the first charge that stands: a deposit")
	}
	h.closeDay(t)
	if got := h.balance(t, "2310"); !got.Equal(dec("-50000")) {
		t.Fatalf("advance deposits %s", got)
	}
	asOf := civil.MustParseDate("2026-09-30")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("the reconciliation agrees: %+v", rec)
	}
}

// openMasterOf opens the master of a group with no instruction.
func (h *hotel) openMasterOf(t *testing.T, group int64) int64 {
	t.Helper()
	r, err := h.Folios.OpenMaster(h.admin, h.propID, group)
	must(t, err)
	return r.Master.ID
}

// Every read that touches folios has to survive a master folio: no reservation, no stay, a payment and a transfer to the city ledger on it. This is the test of the audit of the queries that join
// folios to reservations or stays (docs/architecture/30-group-folio.md, 13.11).
func TestEveryReadThatTouchesFoliosSurvivesAMasterFolio(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
	var company int64
	must(t, h.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme Corp', 100000000) RETURNING id`, h.tenantID, h.propID).Scan(&company))
	must(t, h.Exec(t, `UPDATE booking_groups SET company_id = $1 WHERE id = $2`, company, group))
	h.route(t, group)
	master := h.masterOf(t, group)
	h.closeDay(t) // the nights of day 1 go to the master
	pay, err := h.Folios.PostPayment(h.admin, h.propID, master, "pay", folios.PaymentInput{Amount: "500000", PaymentMethod: "CARD"})
	must(t, err)
	tr, err := h.Folios.Transfer(h.admin, h.propID, master, "tr", folios.TransferInput{CompanyID: company, Amount: "700000"})
	must(t, err)

	// the folio screens and lists
	list, err := h.Folios.ListFolios(h.admin, h.propID, folios.FolioFilter{}, listsort.Sort{}, nil, 50)
	must(t, err)
	seen := false
	for _, f := range list.Data {
		seen = seen || (f.ID == master && f.FolioType == "MASTER" && f.ReservationID == nil && f.BookingGroupCode == "GRP1")
	}
	if !seen {
		t.Fatalf("the master is in the folio list: %+v", list.Data)
	}
	if _, err = h.Folios.GetFolio(h.admin, h.propID, master); err != nil {
		t.Fatalf("GetFolio: %v", err)
	}
	if _, err = h.Folios.TransferTargets(h.admin, h.propID, master, "", 25); err != nil {
		t.Fatalf("TransferTargets from a master: %v", err)
	}
	if _, err = h.Folios.TransferTargets(h.admin, h.propID, ms[0].st.Folio.ID, "Conf", 25); err != nil {
		t.Fatalf("TransferTargets to a master, by the name of the group: %v", err)
	}
	if _, err = h.Folios.ListTransfers(h.admin, h.propID, master, "", 50); err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	// the city ledger: the transfer from the master is on the account of the company, named by the group
	if _, err = h.CityLedger.Statement(h.admin, h.propID, company, nil, nil); err != nil {
		t.Fatalf("Statement: %v", err)
	}
	cands, err := h.CityLedger.Candidates(h.admin, h.propID, company)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	found := false
	for _, c := range cands {
		found = found || (c.PaymentNumber == tr.Payment.PaymentNumber && c.ConfirmationNumber == "GRP1" && c.FolioNumber != "" && !c.Invoiceable) // the group is still in house
	}
	if !found {
		t.Fatalf("the transfer of the master is a candidate of the invoice: %+v", cands)
	}
	// the group leaves: the transfer of its master can be invoiced, as the transfer of a guest who checked out
	must(t, h.Exec(t, `UPDATE reservation_rooms SET status = 'COMPLETED' WHERE reservation_id IN (SELECT id FROM reservations WHERE booking_group_id = $1)`, group))
	if cands, err = h.CityLedger.Candidates(h.admin, h.propID, company); err != nil || len(cands) != 1 || !cands[0].Invoiceable {
		t.Fatalf("invoiceable once the group has left: %+v %v", cands, err)
	}
	must(t, h.Exec(t, `UPDATE reservation_rooms SET status = 'CHECKED_IN' WHERE reservation_id IN (SELECT id FROM reservations WHERE booking_group_id = $1)`, group))
	if _, err = h.CityLedger.Aging(h.admin, h.propID, company); err != nil {
		t.Fatalf("Aging: %v", err)
	}
	// documents
	if _, err = h.Docs.Invoice(h.admin, h.propID, master); err != nil {
		t.Fatalf("Invoice: %v", err)
	}
	if _, err = h.Docs.Receipt(h.admin, h.propID, pay.Payment.ID); err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	// the reports and the night audit
	bd := civil.MustParseDate("2026-10-01")
	if _, err = h.Reports.DailySummary(h.admin, h.propID, bd); err != nil {
		t.Fatalf("DailySummary: %v", err)
	}
	if _, err = h.Reports.Revenue(h.admin, h.propID, civil.MustParseDate("2026-09-30"), bd); err != nil {
		t.Fatalf("Revenue: %v", err)
	}
	if _, err = h.Reports.Cashier(h.admin, h.propID, civil.MustParseDate("2026-09-30"), bd); err != nil {
		t.Fatalf("Cashier: %v", err)
	}
	if _, err = h.Reports.InHouse(h.admin, h.propID); err != nil {
		t.Fatalf("InHouse: %v", err)
	}
	if _, err = h.Audit.Preview(h.admin, h.propID); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// the books agree with the folios, the master included
	asOf := civil.MustParseDate("2026-09-30")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("the reconciliation agrees with a master that holds a payment and a transfer: %+v", rec)
	}
}
