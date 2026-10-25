package banquet_test

import (
	"context"
	"testing"

	"kamarapms/internal/banquet"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomstest"
)

// Transfer to the master folio of a group, the races of the posting and the close of the day (36 4.8, 4.9, 6 and tests 5 to 8 and 11 of section 11).

func (m *mfx) group(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, m.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date) VALUES ($1, $2, $3, 'Conference', '2026-09-30', '2026-10-03') RETURNING id`,
		m.tenantID, m.propID, code).Scan(&id))
	return id
}

// posted makes a confirmed event whose one session of the package is due now, and posts it.
func (m *mfx) posted(t *testing.T, name string) (banquet.Event, int64, banquet.PostResult) {
	t.Helper()
	ev, sid := m.eventOn(t, name, "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	ev = m.confirm(t, ev.ID)
	res, err := m.svc.PostSession(m.office, m.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	return ev, sid, res
}

func TestTheBillOfAnEventIsTransferredToTheMasterFolioOfAGroup(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, res := m.posted(t, "Gala")
	gid := m.group(t, "GRP1")
	master, err := f.Folios.OpenMaster(f.admin, f.propID, gid)
	must(t, err)
	ids := []int64{}
	for _, c := range res.Charges {
		ids = append(ids, c.FolioItemID)
	}
	_, approver := f.AdminAccount(t, f.tenantID)
	in := banquet.TransferInput{GroupID: gid, Mode: "ITEMS", ItemIDs: ids, Reason: "billed to the conference", Approval: &iam.ApprovalInput{Email: approver, Password: roomstest.Password}}
	// the person who manages the banquet but cannot reverse on a folio may not move the bill
	_, err = f.svc.TransferToGroup(f.office, f.propID, ev.ID, "tr-denied", in)
	_ = code(t, err, "PERMISSION_DENIED")
	r, err := f.svc.TransferToGroup(f.admin, f.propID, ev.ID, "tr-1", in)
	must(t, err)
	if r.Scope != folios.ScopeEventGroup || len(r.Moved) != len(ids) {
		t.Fatalf("transfer: %+v", r)
	}
	// the event is linked now, and a replay with the same key changes nothing
	got, err := f.svc.GetEvent(f.mgr, f.propID, ev.ID)
	must(t, err)
	if got.BookingGroupID == nil || *got.BookingGroupID != gid {
		t.Fatalf("the event is not linked to the group: %+v", got.BookingGroupID)
	}
	if bal := m.folioOf(t, ev.ID).Balance; !bal.IsZero() {
		t.Fatalf("the event folio keeps %s", bal)
	}
	var onMaster string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0)::text FROM folio_items WHERE folio_id = $1`, master.Master.ID).Scan(&onMaster))
	if !dec(onMaster).Equal(dec("36300000")) {
		t.Fatalf("the master folio carries %s", onMaster)
	}
	// another group is refused for a linked event
	other := m.group(t, "GRP2")
	_, err = f.svc.TransferToGroup(f.admin, f.propID, ev.ID, "tr-2", banquet.TransferInput{GroupID: other, Mode: "BALANCE", Reason: "x"})
	_ = code(t, err, "EVENT_GROUP_MISMATCH")
	m.closeDay(t)
	m.requireBalanced(t)
}

// ---------------------------------------------------------------- 5. the races

func TestTwoPostingsOfTheSameSessionHaveOneWinnerAndTheLockOrderHolds(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Race", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	ctxA, traceA := db.WithLockTrace(f.office)
	ctxB, traceB := db.WithLockTrace(f.office)
	errs := race(t,
		func() error { _, err := f.svc.PostSession(ctxA, f.propID, ev.ID, sid, banquet.PostInput{}); return err },
		func() error { _, err := f.svc.PostSession(ctxB, f.propID, ev.ID, sid, banquet.PostInput{}); return err })
	oneOK(t, errs, "SESSION_ALREADY_POSTED")
	assertLockOrder(t, traceA)
	assertLockOrder(t, traceB)
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&n))
	if n != 1 {
		t.Fatalf("%d postings", n)
	}
	// nothing was posted twice: the folio holds the 36,300,000 of the package once
	if bal := m.folioOf(t, ev.ID).Balance; !bal.Equal(dec("36300000")) {
		t.Fatalf("the folio holds %s", bal)
	}
}

func TestAPostingAndACancellationOfTheSameEventAreSerialised(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	m.policy(t, banquet.BaseNet)
	ev, sid := m.eventOn(t, "Post and cancel", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	ctxA, traceA := db.WithLockTrace(f.office)
	ctxB, traceB := db.WithLockTrace(f.mgr)
	errs := race(t,
		func() error { _, err := f.svc.PostSession(ctxA, f.propID, ev.ID, sid, banquet.PostInput{}); return err },
		func() error {
			_, err := f.svc.CancelEvent(ctxB, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason})
			return err
		})
	assertLockOrder(t, traceA)
	assertLockOrder(t, traceB)
	var posted, fees int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&posted))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'BANQUET'`, m.folioOf(t, ev.ID).ID).Scan(&fees))
	// either the posting came first (and the cancel then refuses the posted session? no: it cancels and posts the fee) or the cancel closed the event first and the posting is refused
	if posted == 0 && errs[0] == nil {
		t.Fatalf("a posting that succeeded left no row: %v", errs)
	}
	if posted == 1 && fees < 4 {
		t.Fatalf("a posted session has %d charges", fees)
	}
}

// ---------------------------------------------------------------- 6. the close of the day

func TestTheCloseOfTheDayPostsDueSessionsInAutoMode(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Auto", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	before, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	m.closeDay(t)
	var pax int
	var auto bool
	var bd string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT pax_billed, auto, business_date::text FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&pax, &auto, &bd))
	if pax != 100 || !auto || bd != before.BusinessDate.String() {
		t.Fatalf("posting: %d %v %s", pax, auto, bd)
	}
	if bal := m.folioOf(t, ev.ID).Balance; !bal.Equal(dec("36300000")) {
		t.Fatalf("folio %s", bal)
	}
	m.requireBalanced(t)
	// the books keep the revenue on the day of the session
	if got := m.gl(t, "4240"); !got.Equal(dec("-23000000")) {
		t.Fatalf("food revenue %s", got)
	}
}

func TestAManualEventIsLeftForAPersonAndShowsInTheReportOfSessionsNotPosted(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Manual", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	_, err := f.svc.SetPostingMode(f.mgr, f.propID, ev.ID, banquet.PostingModeInput{PostingMode: "MANUAL"})
	must(t, err)
	_, warns, err := f.svc.NightAuditChecks(context.Background(), f.tenantID, f.propID)
	must(t, err)
	found := false
	for _, w := range warns {
		found = found || w.Code == "BANQUET_SESSION_NOT_POSTED"
	}
	if !found {
		t.Fatalf("warnings: %+v", warns)
	}
	m.closeDay(t)
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&n))
	if n != 0 {
		t.Fatal("the close posted a manual event")
	}
	list, err := f.svc.UnpostedReport(f.mgr, f.propID)
	must(t, err)
	if len(list) != 1 || list[0].SessionID != sid || list[0].DaysLate != 1 || list[0].PostingMode != "MANUAL" {
		t.Fatalf("report: %+v", list)
	}
	// a late posting is dated the open business day, not the day of the session
	r, err := f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	must(t, err)
	today, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	if r.Posting.BusinessDate != today.BusinessDate {
		t.Fatalf("posted on %s, the open day is %s", r.Posting.BusinessDate, today.BusinessDate)
	}
	list, err = f.svc.UnpostedReport(f.mgr, f.propID)
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("report after the posting: %+v", list)
	}
}

func TestAnAutoSessionWithNoGuestsIsSkippedWithAWarning(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "No guests", "2026-09-30", 0)
	_, err := f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.lunch.ID})
	must(t, err)
	m.confirm(t, ev.ID)
	_, warns, err := f.svc.NightAuditChecks(context.Background(), f.tenantID, f.propID)
	must(t, err)
	found := false
	for _, w := range warns {
		found = found || w.Code == "BANQUET_SESSION_NO_GUESTS"
	}
	if !found {
		t.Fatalf("warnings: %+v", warns)
	}
	m.closeDay(t)
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&n))
	if n != 0 {
		t.Fatal("a session with no guests was posted")
	}
}

func TestAChargeCodeThatIsInactiveBlocksTheCloseAndNothingIsPosted(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Inactive", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	_, err := f.Pool.Exec(context.Background(), `UPDATE charge_codes SET is_active = false WHERE property_id = $1 AND code = 'BQT_ROOM'`, f.propID)
	must(t, err)
	blockers, _, err := f.svc.NightAuditChecks(context.Background(), f.tenantID, f.propID)
	must(t, err)
	found := false
	for _, b := range blockers {
		found = found || b.Code == "BANQUET_CHARGE_CODE_INACTIVE"
	}
	if !found {
		t.Fatalf("blockers: %+v", blockers)
	}
	// a manual posting meets the same code and rolls back whole: the charges before it are not left
	_, err = f.svc.PostSession(f.office, f.propID, ev.ID, sid, banquet.PostInput{})
	if err == nil {
		t.Fatal("the posting went through an inactive charge code")
	}
	var n, items int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM banquet_session_postings WHERE session_id = $1`, sid).Scan(&n))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'BANQUET'`, m.folioOf(t, ev.ID).ID).Scan(&items))
	if n != 0 || items != 0 {
		t.Fatalf("left behind: %d postings, %d charges", n, items)
	}
}

func TestTheDatabaseRefusesAPostingOnAClosedDay(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, sid := m.eventOn(t, "Closed day", "2026-09-30", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	fo := m.folioOf(t, ev.ID)
	m.closeDay(t) // posts the session on 30 September and closes that day
	other, osid := m.eventOn(t, "Other", "2026-10-01", 10)
	_ = other
	err := f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
		tx, err := db.Tx(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO banquet_session_postings (tenant_id, property_id, session_id, folio_id, business_date, pax_billed, pax_basis, auto) VALUES ($1, $2, $3, $4, '2026-09-30', 1, 'ACTUAL', true)`,
			f.tenantID, f.propID, osid, fo.ID)
		return err
	})
	_ = code(t, err, "BUSINESS_DAY_CLOSED")
	_ = sid
}

// ---------------------------------------------------------------- the deposits

func TestAnUnpaidDepositDueProducesOneNoticeAndTheScheduleKeepsItsTotal(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	m.policy(t, banquet.BaseNet)
	ev, sid := m.eventOn(t, "Deposits", "2026-11-15", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	m.confirm(t, ev.ID)
	// the first row is due at the signature, today
	n, err := f.svc.WriteDueNotices(context.Background(), f.tenantID, f.propID, day("2026-09-30"))
	must(t, err)
	if n < 1 || f.notices(t, f.office, banquet.NoticeDepositDue) != 1 {
		t.Fatalf("notices written %d, deposit notices %d", n, f.notices(t, f.office, banquet.NoticeDepositDue))
	}
	_, err = f.svc.WriteDueNotices(context.Background(), f.tenantID, f.propID, day("2026-09-30"))
	must(t, err)
	if got := f.notices(t, f.office, banquet.NoticeDepositDue); got != 1 {
		t.Fatalf("the notice came again: %d", got)
	}
	// a line added later moves the amounts of the percent rows and the total stays the contract
	_, err = f.svc.AddLine(f.office, f.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: &m.coffee.ID})
	must(t, err)
	v, err := f.svc.Money(f.mgr, f.propID, ev.ID)
	must(t, err)
	if !v.ScheduleTotal.Equal(v.Estimate.Total) {
		t.Fatalf("schedule %s, contract %s", v.ScheduleTotal, v.Estimate.Total)
	}
}

func TestATransferByBalanceMovesAnAmountAndAReplayChangesNothing(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, _ := m.posted(t, "Balance")
	gid := m.group(t, "GRP1")
	_, err := f.Folios.OpenMaster(f.admin, f.propID, gid)
	must(t, err)
	_, approver := f.AdminAccount(t, f.tenantID)
	in := banquet.TransferInput{GroupID: gid, Mode: "BALANCE", Amount: "10000000", Reason: "part to the group", Approval: &iam.ApprovalInput{Email: approver, Password: roomstest.Password}}
	r, err := f.svc.TransferToGroup(f.admin, f.propID, ev.ID, "bal-1", in)
	must(t, err)
	if r.Scope != folios.ScopeEventGroup || r.Replayed {
		t.Fatalf("transfer: %+v", r)
	}
	again, err := f.svc.TransferToGroup(f.admin, f.propID, ev.ID, "bal-1", in)
	must(t, err)
	if !again.Replayed {
		t.Fatalf("a replay: %+v", again)
	}
	if bal := m.folioOf(t, ev.ID).Balance; !bal.Equal(dec("26300000")) {
		t.Fatalf("the event folio keeps %s", bal)
	}
}
