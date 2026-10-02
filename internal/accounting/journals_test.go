package accounting_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// hotel is a property on 30 Sep 2026 (IDR, no decimals) with two rooms of one type, a rate grid and a guest.
type hotel struct {
	*fx
	email       string
	typ         rooms.RoomType
	r101, r102  rooms.Room
	plan, guest int64
	cashAccount int64
	expenseAcct int64
	revenueAcct int64
}

func setupHotel(t *testing.T) *hotel {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	h := &hotel{fx: &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin}, email: email}
	h.typ = e.RoomType(t, admin, p.ID, "DLX")
	h.r101 = e.Room(t, admin, p.ID, h.typ.ID, "101", housekeeping.Clean)
	h.r102 = e.Room(t, admin, p.ID, h.typ.ID, "102", housekeeping.Clean)
	var chargeID int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&h.plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&h.guest))
	_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: h.plan, RoomTypeIDs: []int64{h.typ.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	h.cashAccount = h.byCode(t, "1110").ID
	h.expenseAcct = h.byCode(t, "7110").ID
	h.revenueAcct = h.byCode(t, "4110").ID
	return h
}

func (h *hotel) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: h.email, Password: roomstest.Password}
}

func (h *hotel) book(t *testing.T, arrival, departure string) reservations.Reservation {
	t.Helper()
	res, err := h.Res.Create(h.admin, h.propID, "", reservations.CreateInput{GuestID: &h.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: h.typ.ID, RatePlanID: h.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2},
	}})
	must(t, err)
	return res
}

func (h *hotel) checkIn(t *testing.T, res reservations.Reservation, room rooms.Room) frontdesk.CheckInResult {
	t.Helper()
	out, err := h.Front.CheckIn(h.admin, h.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: h.guest, AdultCount: 2})
	must(t, err)
	return out
}

// closeDay runs the night audit of the current business date.
func (h *hotel) closeDay(t *testing.T) {
	t.Helper()
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	h.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1)) // 00:30 local of the next day
	_, err = h.Audit.Run(h.admin, h.propID, day.BusinessDate)
	must(t, err)
}

// balance is debit minus credit of an account over all journals.
func (h *hotel) balance(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, h.propID, code).Scan(&s))
	return dec(s)
}

func (h *hotel) folioBalance(t *testing.T, folioID int64) decimal.Decimal {
	t.Helper()
	var s string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0)::text FROM folio_items WHERE folio_id = $1`, folioID).Scan(&s))
	return dec(s)
}

func (h *hotel) charge(t *testing.T, folioID int64, code, unit, key string) {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, h.propID, code).Scan(&id))
	_, err := h.Folios.PostCharge(h.admin, h.propID, folioID, key, folios.ChargeInput{ChargeCodeID: id, Quantity: "1", UnitPrice: &unit})
	must(t, err)
}

func (h *hotel) journals(t *testing.T, f accounting.JournalFilter) []accounting.Journal {
	t.Helper()
	list, err := h.Accounting.Journals(h.admin, h.propID, f)
	must(t, err)
	return list
}

// totals of every journal line of the property: debits and credits are equal.
func (h *hotel) requireBalanced(t *testing.T) {
	t.Helper()
	var dr, cr string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM gl_journal_lines WHERE property_id = $1`, h.propID).Scan(&dr, &cr))
	if dr != cr {
		t.Fatalf("the books do not balance: debit %s credit %s", dr, cr)
	}
}

func TestDayCloseJournalFollowsTheGuestLedger(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-03")
	dep, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	st := h.checkIn(t, res, h.r101)
	if st.Folio.ID != dep.Payment.FolioID {
		t.Fatalf("the deposit folio is the stay folio")
	}
	h.charge(t, st.Folio.ID, "MINIBAR", "1000000", "mb")
	_, err = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "card", folios.PaymentInput{Amount: "200000", PaymentMethod: "CARD"})
	must(t, err)
	co, err := h.Companies.Create(h.admin, h.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	_, err = h.Folios.Transfer(h.admin, h.propID, st.Folio.ID, "tr", folios.TransferInput{CompanyID: co.ID, Amount: "100000"})
	must(t, err)
	_, err = h.CityLedger.Receive(h.admin, h.propID, co.ID, "rc", cityledger.ReceiptInput{Amount: "50000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	h.closeDay(t)

	list := h.journals(t, accounting.JournalFilter{})
	if len(list) != 1 || list[0].Type != accounting.JournalDayClose || list[0].Number != "JV000001" || list[0].Date != d("2026-09-30") {
		t.Fatalf("one day close journal: %+v", list)
	}
	j, err := h.Accounting.GetJournal(h.admin, h.propID, list[0].ID)
	must(t, err)
	h.requireBalanced(t)
	// the deposit sits in the liability, what was taken after check-in clears the guest ledger
	for code, want := range map[string]string{"1110": "300000", "1150": "200000", "1130": "50000", "1220": "50000", "2310": "-300000", "4230": "-1000000"} {
		if got := h.balance(t, code); !got.Equal(dec(want)) {
			t.Errorf("account %s: %s, want %s", code, got, want)
		}
	}
	// the guest ledger is what folios owe plus the deposits they hold
	if got, want := h.balance(t, "1210"), h.folioBalance(t, st.Folio.ID).Add(dec("300000")); !got.Equal(want) {
		t.Fatalf("guest ledger %s, folios owe %s with deposits", got, want)
	}
	var minibar *accounting.JournalLine
	for i, l := range j.Lines {
		if l.AccountCode == "4230" {
			minibar = &j.Lines[i]
		}
	}
	if minibar == nil || minibar.SourceType != "CHARGE_CODE" || minibar.SourceRef != "MINIBAR" || !minibar.Credit.Equal(dec("1000000")) {
		t.Fatalf("minibar revenue line: %+v", minibar)
	}
	// the day is on record: running the posting again does nothing
	must(t, h.TxM.WithinTx(h.admin, func(ctx context.Context) error {
		p, _ := auth.Require(ctx)
		return h.Accounting.PostDay(ctx, p, h.propID, d("2026-09-30"))
	}))
	if n := h.Count(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, h.propID); n != 1 {
		t.Fatalf("%d journals", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.day_posted'`); n != 1 {
		t.Fatalf("%d day_posted audit rows", n)
	}
}

func TestDepositsAreReleasedWhenTheFolioClosesAtCheckout(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-01")
	_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	st := h.checkIn(t, res, h.r101)
	h.closeDay(t) // the night is charged; the deposit is still a liability
	if got := h.balance(t, "2310"); !got.Equal(dec("-300000")) {
		t.Fatalf("deposits held: %s", got)
	}
	owed := h.folioBalance(t, st.Folio.ID)
	if !owed.IsPositive() {
		t.Fatalf("the guest owes the rest: %s", owed)
	}
	_, err = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "rest", folios.PaymentInput{Amount: owed.String(), PaymentMethod: "CARD"})
	must(t, err)
	got, err := h.Front.CheckOut(h.admin, h.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
	must(t, err)
	if len(got.Folios) == 0 {
		t.Fatalf("checked out: %+v", got)
	}
	h.closeDay(t)
	if got := h.balance(t, "2310"); !got.IsZero() {
		t.Fatalf("deposits held after checkout: %s", got)
	}
	if got := h.balance(t, "1210"); !got.IsZero() {
		t.Fatalf("guest ledger after checkout: %s", got)
	}
	h.requireBalanced(t)
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines WHERE source_type = 'DEPOSIT_RELEASE'`); n != 2 {
		t.Fatalf("a release has two lines, got %d", n)
	}
}

func TestRefundAndVoidFollowTheirPayment(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-10-02", "2026-10-04")
	_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "400000", PaymentMethod: "CASH"})
	must(t, err)
	dep2, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep2", folios.PaymentInput{Amount: "100000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	_, err = h.Folios.Void(h.admin, h.propID, dep2.Payment.ID, folios.CorrectionInput{Reason: "wrong guest", Approval: h.approval()})
	must(t, err)
	first, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep3", folios.PaymentInput{Amount: "50000", PaymentMethod: "CASH"})
	must(t, err)
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	_, err = h.Folios.Refund(h.admin, h.propID, first.Payment.ID, "rf", folios.RefundInput{Amount: "20000", Reason: "changed plans", Approval: h.approval()})
	must(t, err)
	h.closeDay(t)
	// 400000 + 50000 - 20000 held; the voided transfer left nothing
	if got := h.balance(t, "2310"); !got.Equal(dec("-430000")) {
		t.Fatalf("deposits held: %s", got)
	}
	if got := h.balance(t, "1110"); !got.Equal(dec("430000")) {
		t.Fatalf("cash: %s", got)
	}
	if got := h.balance(t, "1130"); !got.IsZero() {
		t.Fatalf("bank: %s", got)
	}
	if got := h.balance(t, "1210"); !got.IsZero() {
		t.Fatalf("a deposit never reaches the guest ledger: %s", got)
	}
	h.requireBalanced(t)
}

func TestAChargeReversalAndTheTaxesAreJournaled(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-03"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.charge(t, st.Folio.ID, "LAUNDRY", "40000", "l1")
	var itemID int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND reference_id IS NULL AND description ILIKE '%laundry%' ORDER BY id LIMIT 1`, st.Folio.ID).Scan(&itemID))
	_, err := h.Folios.Reverse(h.admin, h.propID, itemID, folios.CorrectionInput{Reason: "posted twice", Approval: h.approval()})
	must(t, err)
	h.closeDay(t)
	h.requireBalanced(t)
	if got := h.balance(t, "4350"); !got.IsZero() {
		t.Fatalf("laundry revenue after its reversal: %s", got)
	}
	if got := h.balance(t, "4210"); !got.Equal(dec("-100000")) {
		t.Fatalf("restaurant revenue: %s", got)
	}
	// whatever tax the charges carry is posted to a payable account, and the guest ledger is what the folio owes
	if got, want := h.balance(t, "1210"), h.folioBalance(t, st.Folio.ID); !got.Equal(want) {
		t.Fatalf("guest ledger %s, folio %s", got, want)
	}
}

func TestAnItemWithoutAnAccountGoesToSuspense(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-03"), h.r101)
	must(t, h.Exec(t, `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'OTHER'`, h.propID))
	h.charge(t, st.Folio.ID, "OTHER", "10000", "o1")
	h.closeDay(t)
	if got := h.balance(t, "2990"); !got.Equal(dec("-10000")) {
		t.Fatalf("suspense: %s", got)
	}
	h.requireBalanced(t)
}

func TestWithoutAccountingTheDayClosesAndBackfillCatchesUp(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	must(t, h.Exec(t, `DELETE FROM accounting_settings WHERE property_id = $1`, h.propID))
	h.closeDay(t)
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM gl_journals`); n != 0 {
		t.Fatalf("%d journals without accounting", n)
	}
	must(t, h.Exec(t, `INSERT INTO accounting_settings (tenant_id, property_id, start_date) VALUES ($1, $2, '2026-09-30')`, h.tenantID, h.propID))
	n, err := h.Accounting.PostPending(h.admin, h.propID)
	must(t, err)
	if n != 2 {
		t.Fatalf("backfilled %d days", n)
	}
	again, err := h.Accounting.PostPending(h.admin, h.propID)
	must(t, err)
	if again != 0 {
		t.Fatalf("nothing left, got %d", again)
	}
	h.requireBalanced(t)
	if got := h.balance(t, "4210"); !got.Equal(dec("-100000")) {
		t.Fatalf("restaurant revenue: %s", got)
	}
	if got, want := h.balance(t, "1210"), h.folioBalance(t, st.Folio.ID); !got.Equal(want) {
		t.Fatalf("guest ledger %s, folio %s", got, want)
	}
}

func TestPostPendingNeedsPermissionAndRunsOnce(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	must(t, h.Exec(t, `DELETE FROM accounting_settings WHERE property_id = $1`, h.propID))
	h.closeDay(t)
	must(t, h.Exec(t, `INSERT INTO accounting_settings (tenant_id, property_id, start_date) VALUES ($1, $2, '2026-09-30')`, h.tenantID, h.propID))
	poster := h.User(t, h.tenantID, h.propID, auth.PermAccountingPost, auth.PermAccountingView)
	_, err := h.Accounting.PostPending(poster, h.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	closer := h.User(t, h.tenantID, h.propID, auth.PermAccountingClose)
	var wg sync.WaitGroup
	counts := make([]int, 4)
	errs := make([]error, 4)
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts[i], errs[i] = h.Accounting.PostPending(closer, h.propID)
		}()
	}
	wg.Wait()
	total := 0
	for i := range counts {
		must(t, errs[i])
		total += counts[i]
	}
	if total != 1 || h.Count(t, `SELECT count(*) FROM gl_journals`) != 1 {
		t.Fatalf("one day, one journal: posted %v", counts)
	}
}

func TestJournalTablesAreAppendOnlyAndBalanced(t *testing.T) {
	h := setupHotel(t)
	j, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "k1")
	must(t, err)
	for _, sql := range []string{
		`UPDATE gl_journals SET description = 'x'`, `DELETE FROM gl_journals`, `UPDATE gl_journal_lines SET debit = 1`, `DELETE FROM gl_journal_lines`,
		`TRUNCATE gl_journals CASCADE`, `TRUNCATE gl_journal_lines`,
	} {
		if err := h.Exec(t, sql); err == nil {
			t.Fatalf("%s must fail", sql)
		}
	}
	// a journal that does not balance cannot commit, and one without lines neither
	err = h.Exec(t, `WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
		VALUES ($1, $2, 'X1', 'MANUAL', '2026-09-30', 'x') RETURNING id)
		INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit) SELECT $1, $2, id, 1, $3, 5 FROM j`, h.tenantID, h.propID, h.cashAccount)
	if err == nil || !strings.Contains(err.Error(), "not balanced") {
		t.Fatalf("unbalanced journal: %v", err)
	}
	err = h.Exec(t, `INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES ($1, $2, 'X2', 'MANUAL', '2026-09-30', 'x')`, h.tenantID, h.propID)
	if err == nil || !strings.Contains(err.Error(), "not balanced") {
		t.Fatalf("journal without lines: %v", err)
	}
	if len(j.Lines) != 2 {
		t.Fatalf("lines: %+v", j.Lines)
	}
}

// simple is a balanced manual journal: cash against revenue.
func (h *hotel) simple(amount string) accounting.ManualInput {
	return accounting.ManualInput{Date: d("2026-09-30"), Description: "Cash sale", Reference: "INV-1", Lines: []accounting.LineInput{
		{AccountID: h.cashAccount, Debit: dec(amount)}, {AccountID: h.revenueAcct, Credit: dec(amount), Description: "rooms"},
	}}
}

func TestManualJournalRules(t *testing.T) {
	h := setupHotel(t)
	j, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "m1")
	must(t, err)
	if j.Type != accounting.JournalManual || !j.Total.Equal(dec("100000")) || j.Reference != "INV-1" || len(j.Lines) != 2 || j.Lines[0].LineNo != 1 {
		t.Fatalf("journal: %+v", j)
	}
	// a retry with the same key returns the same journal
	again, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "m1")
	must(t, err)
	if again.ID != j.ID || h.Count(t, `SELECT count(*) FROM gl_journals`) != 1 {
		t.Fatalf("a replay posts nothing: %+v", again)
	}
	// each rule has its own field error
	bad := func(name string, mut func(*accounting.ManualInput), field string) {
		in := h.simple("100000")
		mut(&in)
		_, err := h.Accounting.PostManual(h.admin, h.propID, in, "bad-"+name)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		for _, f := range e.Fields {
			if f.Field == field {
				return
			}
		}
		t.Errorf("%s: want a field error on %s, got %+v", name, field, e.Fields)
	}
	bad("unbalanced", func(in *accounting.ManualInput) { in.Lines[1].Credit = dec("90000") }, "lines")
	bad("one line", func(in *accounting.ManualInput) { in.Lines = in.Lines[:1] }, "lines")
	bad("both sides", func(in *accounting.ManualInput) { in.Lines[0].Credit = dec("5") }, "lines[0].debit")
	bad("negative", func(in *accounting.ManualInput) { in.Lines[0].Debit = dec("-1") }, "lines[0].debit")
	bad("decimals", func(in *accounting.ManualInput) {
		in.Lines[0].Debit = dec("100000.5")
		in.Lines[1].Credit = dec("100000.5")
	}, "lines[0].debit")
	bad("no description", func(in *accounting.ManualInput) { in.Description = " " }, "description")
	bad("no date", func(in *accounting.ManualInput) { in.Date = civil.Date{} }, "journal_date")
	bad("future", func(in *accounting.ManualInput) { in.Date = d("2026-10-02") }, "journal_date")
	bad("before start", func(in *accounting.ManualInput) { in.Date = d("2026-09-29") }, "journal_date")
	bad("header", func(in *accounting.ManualInput) { in.Lines[0].AccountID = h.byCode(t, "1100").ID }, "lines[0].account_id")
	bad("unknown", func(in *accounting.ManualInput) { in.Lines[0].AccountID = 999999 }, "lines[0].account_id")
	bad("control", func(in *accounting.ManualInput) { in.Lines[0].AccountID = h.byCode(t, "1210").ID }, "lines[0].account_id")
	bad("deposits", func(in *accounting.ManualInput) { in.Lines[0].AccountID = h.byCode(t, "2310").ID }, "lines[0].account_id")
	off := h.byCode(t, "7110")
	_, err = h.Accounting.UpdateAccount(h.admin, h.propID, off.ID, accounting.AccountPatch{IsActive: ptr(false)})
	must(t, err)
	bad("inactive", func(in *accounting.ManualInput) { in.Lines[0].AccountID = off.ID }, "lines[0].account_id")
	// permission and tenancy
	viewer := h.User(t, h.tenantID, h.propID, auth.PermAccountingView)
	_, err = h.Accounting.PostManual(viewer, h.propID, h.simple("1"), "v")
	wantCode(t, err, "PERMISSION_DENIED")
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.journal_posted'`); n != 1 {
		t.Fatalf("%d audit rows", n)
	}
	// the account that has entries cannot become a header or be deleted
	bank := h.byCode(t, "1140")
	in := h.simple("500")
	in.Lines[0].AccountID = bank.ID
	_, err = h.Accounting.PostManual(h.admin, h.propID, in, "bank")
	must(t, err)
	_, err = h.Accounting.UpdateAccount(h.admin, h.propID, bank.ID, accounting.AccountPatch{IsPostable: ptr(false), StatementGroup: ptr("")})
	wantCode(t, err, "ACCOUNT_HAS_ENTRIES")
	wantCode(t, h.Accounting.DeleteAccount(h.admin, h.propID, bank.ID), "ACCOUNT_HAS_ENTRIES")
}

func TestReversal(t *testing.T) {
	h := setupHotel(t)
	j, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "m1")
	must(t, err)
	_, err = h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "wrong account"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Approval: h.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	rev, err := h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "wrong account", Approval: h.approval()})
	must(t, err)
	if rev.Type != accounting.JournalReversal || rev.ReversesID == nil || *rev.ReversesID != j.ID || rev.ApprovedBy == nil || !rev.Lines[0].Credit.Equal(dec("100000")) {
		t.Fatalf("reversal: %+v", rev)
	}
	if got := h.balance(t, "1110"); !got.IsZero() {
		t.Fatalf("cash after the reversal: %s", got)
	}
	orig, err := h.Accounting.GetJournal(h.admin, h.propID, j.ID)
	must(t, err)
	if orig.ReversedByID == nil || *orig.ReversedByID != rev.ID {
		t.Fatalf("the journal knows its reversal: %+v", orig)
	}
	_, err = h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "again", Approval: h.approval()})
	wantCode(t, err, "JOURNAL_ALREADY_REVERSED")
	_, err = h.Accounting.Reverse(h.admin, h.propID, rev.ID, accounting.ReverseInput{Reason: "undo", Approval: h.approval()})
	wantCode(t, err, "JOURNAL_NOT_REVERSIBLE")
	// day close journals follow the folios
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-03"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.closeDay(t)
	dc := h.journals(t, accounting.JournalFilter{Type: accounting.JournalDayClose})
	_, err = h.Accounting.Reverse(h.admin, h.propID, dc[0].ID, accounting.ReverseInput{Reason: "x", Approval: h.approval()})
	wantCode(t, err, "JOURNAL_NOT_REVERSIBLE")
	_, err = h.Accounting.Reverse(h.admin, h.propID, 999999, accounting.ReverseInput{Reason: "x", Approval: h.approval()})
	wantCode(t, err, "JOURNAL_NOT_FOUND")
}

func TestReversalRace(t *testing.T) {
	h := setupHotel(t)
	j, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "m1")
	must(t, err)
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "race", Approval: h.approval()})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "JOURNAL_ALREADY_REVERSED")
		}
	}
	if ok != 1 || h.balance(t, "1110").Sign() != 0 {
		t.Fatalf("one reversal wins: %d", ok)
	}
}

func TestConcurrentManualJournalsGetGaplessNumbersAndKeysAreHonoured(t *testing.T) {
	h := setupHotel(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	ids := make([]int64, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%4) // two callers per key
			j, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("1000"), key)
			errs[i], ids[i] = err, j.ID
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM gl_journals`); n != 4 {
		t.Fatalf("four keys, four journals, got %d", n)
	}
	if n := h.Count(t, `SELECT count(DISTINCT journal_number) FROM gl_journals WHERE journal_number ~ '^JV00000[1-4]$'`); n != 4 {
		t.Fatalf("numbers are gapless: %d", n)
	}
	h.requireBalanced(t)
}

func TestPeriods(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-20"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	list, err := h.Accounting.Periods(h.admin, h.propID)
	must(t, err)
	if len(list) != 1 || list[0].Start != d("2026-09-01") || list[0].Status != "OPEN" || list[0].Closable || list[0].Days != 1 {
		t.Fatalf("periods before the first close: %+v", list)
	}
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	wantCode(t, err, "PERIOD_NOT_READY")
	h.closeDay(t) // 30 Sep is closed and journaled; today is 1 Oct
	list, err = h.Accounting.Periods(h.admin, h.propID)
	must(t, err)
	if len(list) != 2 || list[0].Start != d("2026-10-01") || list[1].Start != d("2026-09-01") || !list[1].Closable || list[0].Closable {
		t.Fatalf("periods: %+v", list)
	}
	closer := h.User(t, h.tenantID, h.propID, auth.PermAccountingView)
	_, err = h.Accounting.ClosePeriod(closer, h.propID, d("2026-09-01"))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-10-01"))
	wantCode(t, err, "PERIOD_NOT_READY")
	per, err := h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	must(t, err)
	if per.Status != "CLOSED" || per.ClosedAt == nil || !per.Reopenable {
		t.Fatalf("closed: %+v", per)
	}
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	wantCode(t, err, "PERIOD_ALREADY_CLOSED")
	// a closed month takes no journals, and no reversal dated in it
	_, err = h.Accounting.PostManual(h.admin, h.propID, h.simple("1000"), "late")
	wantCode(t, err, "PERIOD_CLOSED")
	in := h.simple("1000")
	in.Date = d("2026-10-01")
	j, err := h.Accounting.PostManual(h.admin, h.propID, in, "oct")
	must(t, err)
	if _, err := h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "x", Approval: h.approval()}); err != nil {
		t.Fatal(err)
	}
	// only the latest closed month reopens, with a reason
	_, err = h.Accounting.ReopenPeriod(h.admin, h.propID, d("2026-09-01"), " ")
	wantCode(t, err, "VALIDATION_FAILED")
	per, err = h.Accounting.ReopenPeriod(h.admin, h.propID, d("2026-09-01"), "an invoice was missing")
	must(t, err)
	if per.Status != "OPEN" || per.ReopenReason != "an invoice was missing" {
		t.Fatalf("reopened: %+v", per)
	}
	_, err = h.Accounting.ReopenPeriod(h.admin, h.propID, d("2026-09-01"), "again")
	wantCode(t, err, "PERIOD_NOT_CLOSED")
	if _, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("1000"), "late"); err != nil {
		t.Fatalf("the reopened month takes journals: %v", err)
	}
	_, err = h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-08-01"))
	wantCode(t, err, "PERIOD_NOT_FOUND")
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('accounting.period_closed', 'accounting.period_reopened')`); n != 2 {
		t.Fatalf("%d period audit rows", n)
	}
}

func TestJournalListFilters(t *testing.T) {
	h := setupHotel(t)
	a, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("1000"), "a")
	must(t, err)
	in := h.simple("2000")
	in.Description, in.Lines[0].AccountID = "Petty cash top up", h.byCode(t, "1120").ID
	b, err := h.Accounting.PostManual(h.admin, h.propID, in, "b")
	must(t, err)
	all := h.journals(t, accounting.JournalFilter{})
	if len(all) != 2 || all[0].ID != b.ID {
		t.Fatalf("newest first: %+v", all)
	}
	if got := h.journals(t, accounting.JournalFilter{AccountID: &h.cashAccount}); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("by account: %+v", got)
	}
	if got := h.journals(t, accounting.JournalFilter{Q: "petty"}); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("by text: %+v", got)
	}
	from, to := d("2026-10-01"), d("2026-10-31")
	if got := h.journals(t, accounting.JournalFilter{From: &from, To: &to}); len(got) != 0 {
		t.Fatalf("by date: %+v", got)
	}
	if got := h.journals(t, accounting.JournalFilter{Type: accounting.JournalReversal}); len(got) != 0 {
		t.Fatalf("by type: %+v", got)
	}
	_, err = h.Accounting.GetJournal(h.admin, h.propID, 999999)
	wantCode(t, err, "JOURNAL_NOT_FOUND")
	// another tenant's property
	other := h.Tenant(t, "XYZ")
	op := h.Property(t, other.ID, "SG")
	_, err = h.Accounting.GetJournal(roomstest.Admin(other.ID), op.ID, a.ID)
	wantCode(t, err, "JOURNAL_NOT_FOUND")
	_, err = h.Accounting.Journals(h.admin, op.ID, accounting.JournalFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if strings.Contains(a.Number, " ") {
		t.Fatal(a.Number)
	}
}
