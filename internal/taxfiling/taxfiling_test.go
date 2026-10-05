package taxfiling_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/departments"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxfiling"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func asApp(err error) *apperr.Error {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	email            string
	acc              map[string]int64
	pb1              billingconfig.Tax
	folioID          int64
}

// setup: a property on 30 Sep 2026 whose room, restaurant and minibar charges carry a hotel tax (PB1, 10%) that is owed
// on the tax payable account 2410, and a guest in house who is charged.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, email: email, acc: map[string]int64{}}
	list, err := e.Accounting.Accounts(admin, p.ID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		f.acc[a.Code] = a.ID
	}
	f.pb1, err = e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "PB1", Name: "Hotel tax (PB1)", Rate: "10", GLAccountCode: "2410", IsActive: true})
	must(t, err)
	for _, code := range []string{"ROOM", "RESTAURANT", "MINIBAR"} {
		var id int64
		must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, p.ID, code).Scan(&id))
		_, err := e.Billing.ReplaceRules(admin, p.ID, id, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: f.pb1.ID, Sequence: 1}}})
		must(t, err)
	}
	typ := e.RoomType(t, admin, p.ID, "DLX")
	room := e.Room(t, admin, p.ID, typ.ID, "101", housekeeping.Clean)
	var chargeID, plan, guest int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&guest))
	_, err = e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: plan, RoomTypeIDs: []int64{typ.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	res, err := e.Res.Create(admin, p.ID, "", reservations.CreateInput{GuestID: &guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: typ.ID, RatePlanID: plan, Arrival: d("2026-09-30"), Departure: d("2026-10-05"), Adults: 2},
	}})
	must(t, err)
	st, err := e.Front.CheckIn(admin, p.ID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: guest, AdultCount: 2})
	must(t, err)
	f.folioID = st.Folio.ID
	return f
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.email, Password: roomstest.Password}
}

func (f *fx) charge(t *testing.T, code, unit, key string) folios.ItemResult {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	out, err := f.Folios.PostCharge(f.admin, f.propID, f.folioID, key, folios.ChargeInput{ChargeCodeID: id, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	return out
}

// closeDay runs the night audit of the current business date.
func (f *fx) closeDay(t *testing.T) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

func (f *fx) profile(t *testing.T) taxfiling.Profile {
	t.Helper()
	pr, err := f.Tax.CreateProfile(f.admin, f.propID, taxfiling.ProfileInput{TaxID: f.pb1.ID, Authority: "Bapenda Kabupaten Badung", RegistrationNumber: "P.2.0123456"})
	must(t, err)
	return pr
}

func (f *fx) balance(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, f.propID, code).Scan(&s))
	return dec(s)
}

// busyMonth charges the guest, reverses a charge, and closes 30 Sep: September is over and journaled.
func (f *fx) busyMonth(t *testing.T) {
	t.Helper()
	f.charge(t, "RESTAURANT", "1000000", "r1")
	mb := f.charge(t, "MINIBAR", "500000", "m1")
	f.charge(t, "MINIBAR", "200000", "m2")
	_, err := f.Folios.Reverse(f.admin, f.propID, mb.Item.ID, folios.CorrectionInput{Reason: "posted twice", Approval: f.approval()})
	must(t, err)
	f.closeDay(t)
}

func TestProfiles(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	if pr.TaxCode != "PB1" || pr.DueDay != 15 || !pr.IsActive || pr.Authority != "Bapenda Kabupaten Badung" || pr.GLAccountCode != "2410" {
		t.Fatalf("profile: %+v", pr)
	}
	_, err := f.Tax.CreateProfile(f.admin, f.propID, taxfiling.ProfileInput{TaxID: f.pb1.ID, Authority: "again"})
	wantCode(t, err, "TAX_PROFILE_EXISTS")
	for name, in := range map[string]taxfiling.ProfileInput{
		"authority": {TaxID: f.pb1.ID},
		"tax_id":    {TaxID: 999999, Authority: "x"},
		"due_day":   {TaxID: f.pb1.ID, Authority: "x", DueDay: ptr(31)},
	} {
		_, err := f.Tax.CreateProfile(f.admin, f.propID, in)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		found := false
		for _, fe := range e.Fields {
			found = found || fe.Field == name
		}
		if !found {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}
	up, err := f.Tax.UpdateProfile(f.admin, f.propID, pr.ID, taxfiling.ProfilePatch{DueDay: ptr(20), RegistrationNumber: ptr("")})
	must(t, err)
	if up.DueDay != 20 || up.RegistrationNumber != "" || up.Authority != pr.Authority {
		t.Fatalf("updated: %+v", up)
	}
	_, err = f.Tax.UpdateProfile(f.admin, f.propID, pr.ID, taxfiling.ProfilePatch{DueDay: ptr(0)})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Tax.GetProfile(f.admin, f.propID, 999999)
	wantCode(t, err, "TAX_PROFILE_NOT_FOUND")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermTaxView)
	if list, err := f.Tax.Profiles(viewer, f.propID); err != nil || len(list) != 1 {
		t.Fatalf("a viewer reads the profiles: %v", err)
	}
	_, err = f.Tax.CreateProfile(viewer, f.propID, taxfiling.ProfileInput{TaxID: f.pb1.ID, Authority: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	f.Clock.Set(roomstest.T0)
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	_, err = f.Tax.Profiles(roomstest.Admin(other.ID), f.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestTheWorksheetReadsTheTaxCollectedAndChecksItAgainstTheBooks(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	sep := d("2026-09-01")
	ws, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, sep)
	must(t, err)
	if ws.Ready || len(ws.Blockers) == 0 || ws.Return != nil {
		t.Fatalf("the month is not over: %+v", ws)
	}
	f.busyMonth(t)
	ws, err = f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, sep)
	must(t, err)
	if !ws.Ready || len(ws.Blockers) != 0 || ws.PostedDays != 1 || ws.Days != 1 || ws.DueDate != d("2026-10-15") || ws.PeriodEnd != d("2026-09-30") {
		t.Fatalf("worksheet: %+v", ws)
	}
	// room (night audit) 1,000,000, restaurant 1,000,000, minibar 200,000 after its twin was reversed: tax 10% of 2,200,000
	by := map[string]taxfiling.WorksheetLine{}
	for _, l := range ws.Lines {
		by[l.ChargeCode] = l
	}
	eq(t, "room tax", by["ROOM"].Tax, "100000")
	eq(t, "restaurant tax", by["RESTAURANT"].Tax, "100000")
	eq(t, "minibar tax after the reversal", by["MINIBAR"].Tax, "20000")
	eq(t, "minibar base after the reversal", by["MINIBAR"].Base, "200000")
	eq(t, "rate", by["MINIBAR"].Rate, "10")
	eq(t, "tax", ws.Tax, "220000")
	eq(t, "base", ws.Base, "2200000")
	eq(t, "the books agree", ws.GLCollected, "220000")
	eq(t, "difference", ws.Difference, "0")
	eq(t, "tax payable account", f.balance(t, "2410"), "-220000")
	if per, err := f.Tax.Periods(f.admin, f.propID, pr.TaxID); err != nil || len(per) != 2 || per[0].PeriodStart != d("2026-10-01") || per[0].Status != "OPEN" || per[1].Status != "READY" || !per[1].Tax.Equal(dec("220000")) {
		t.Fatalf("periods: %+v %v", per, err)
	}
	// a month that is not the first of a month, another property's tax, a tax without a profile
	_, err = f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-09-15"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Tax.Worksheet(f.admin, f.propID, 999999, sep)
	wantCode(t, err, "TAX_PROFILE_NOT_FOUND")
	// October is not over
	oct, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-10-01"))
	must(t, err)
	if oct.Ready {
		t.Fatalf("October is not over: %+v", oct.Blockers)
	}
}

func TestFilingAReturnFreezesTheWorksheet(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.busyMonth(t)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermTaxView)
	in := taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01"), FilingReference: "SPTPD-0926", Notes: "e-filing"}
	_, err := f.Tax.FileReturn(viewer, f.propID, in, "k0")
	wantCode(t, err, "PERMISSION_DENIED")
	for name, bad := range map[string]taxfiling.FileInput{
		"period_start": {TaxID: pr.TaxID, PeriodStart: d("2026-09-10")},
		"filed_on":     {TaxID: pr.TaxID, PeriodStart: d("2026-09-01"), FiledOn: d("2026-12-01")},
	} {
		_, err := f.Tax.FileReturn(f.admin, f.propID, bad, "bad-"+name)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		found := false
		for _, fe := range e.Fields {
			found = found || fe.Field == name
		}
		if !found {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}
	_, err = f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-10-01")}, "oct")
	wantCode(t, err, "TAX_MONTH_NOT_READY")
	ret, err := f.Tax.FileReturn(f.admin, f.propID, in, "k1")
	must(t, err)
	if ret.Number != "TXR000001" || ret.Status != "FILED" || ret.DueDate != d("2026-10-15") || ret.FilingReference != "SPTPD-0926" || ret.FiledOn != d("2026-10-01") || len(ret.Lines) != 3 {
		t.Fatalf("return: %+v", ret)
	}
	eq(t, "tax", ret.Tax, "220000")
	eq(t, "base", ret.Base, "2200000")
	eq(t, "outstanding", ret.Outstanding, "220000")
	if ret.PaymentStatus != "UNPAID" || ret.Overdue {
		t.Fatalf("payment status: %+v", ret)
	}
	// a retry returns it, a second return of the month is refused
	again, err := f.Tax.FileReturn(f.admin, f.propID, in, "k1")
	must(t, err)
	if again.ID != ret.ID || f.Count(t, `SELECT count(*) FROM tax_returns`) != 1 {
		t.Fatalf("a replay files nothing: %+v", again)
	}
	_, err = f.Tax.FileReturn(f.admin, f.propID, in, "k2")
	wantCode(t, err, "TAX_RETURN_EXISTS")
	// the worksheet of the month now carries the return, and the figures of the return do not move
	f.charge(t, "RESTAURANT", "300000", "late")
	ws, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-09-01"))
	must(t, err)
	if ws.Return == nil || ws.Return.ID != ret.ID || ws.Ready {
		t.Fatalf("worksheet after filing: %+v", ws.Return)
	}
	got, err := f.Tax.GetReturn(f.admin, f.propID, ret.ID)
	must(t, err)
	eq(t, "the frozen tax", got.Tax, "220000")
	// returns can be listed and voided with a reason and an approval
	list, err := f.Tax.Returns(f.admin, f.propID, taxfiling.ReturnFilter{TaxID: &pr.TaxID})
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("returns: %+v", list)
	}
	_, err = f.Tax.VoidReturn(f.admin, f.propID, ret.ID, taxfiling.VoidInput{Reason: "wrong reference"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.Tax.VoidReturn(f.admin, f.propID, ret.ID, taxfiling.VoidInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	v, err := f.Tax.VoidReturn(f.admin, f.propID, ret.ID, taxfiling.VoidInput{Reason: "wrong reference", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.PaymentStatus != "VOIDED" || v.VoidReason != "wrong reference" {
		t.Fatalf("voided: %+v", v)
	}
	_, err = f.Tax.VoidReturn(f.admin, f.propID, ret.ID, taxfiling.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "TAX_RETURN_ALREADY_VOIDED")
	refiled, err := f.Tax.FileReturn(f.admin, f.propID, in, "k3")
	must(t, err)
	if refiled.ID == ret.ID || refiled.Number != "TXR000002" {
		t.Fatalf("the month is filed again: %+v", refiled)
	}
	if err := f.Exec(t, `DELETE FROM tax_returns`); err == nil {
		t.Fatal("returns are not deleted")
	}
	if err := f.Exec(t, `UPDATE tax_return_lines SET tax_amount = 1`); err == nil {
		t.Fatal("the worksheet of a return does not change")
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'tax.return_%'`); n != 3 {
		t.Fatalf("%d return audit rows", n)
	}
}

func TestPayingTheTaxAuthorityPostsAJournalAgainstTheTaxPayableAccount(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.busyMonth(t)
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "k1")
	must(t, err)
	pay := func(amount, penalty, method, key string, penaltyAccount int64) (taxfiling.Payment, error) {
		return f.Tax.PayReturn(f.admin, f.propID, ret.ID, taxfiling.PayInput{
			PaymentDate: d("2026-10-01"), Amount: dec(amount), Penalty: dec(penalty), PenaltyAccountID: penaltyAccount, PaymentMethod: method, ReferenceNumber: "NTPN-77",
		}, key)
	}
	// fields and limits
	for name, in := range map[string]taxfiling.PayInput{
		"amount":             {PaymentDate: d("2026-10-01"), PaymentMethod: "CASH"},
		"payment_method":     {PaymentDate: d("2026-10-01"), Amount: dec("1"), PaymentMethod: "CARD"},
		"payment_date":       {Amount: dec("1"), PaymentMethod: "CASH"},
		"penalty_account_id": {PaymentDate: d("2026-10-01"), Amount: dec("1"), Penalty: dec("5"), PaymentMethod: "CASH"},
		"penalty":            {PaymentDate: d("2026-10-01"), Amount: dec("1"), Penalty: dec("-5"), PaymentMethod: "CASH"},
	} {
		_, err := f.Tax.PayReturn(f.admin, f.propID, ret.ID, in, "bad-"+name)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		found := false
		for _, fe := range e.Fields {
			found = found || fe.Field == name
		}
		if !found {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}
	_, err = pay("220001", "0", "BANK_TRANSFER", "over", 0)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Tax.PayReturn(f.admin, f.propID, ret.ID, taxfiling.PayInput{PaymentDate: d("2026-10-05"), Amount: dec("1"), PaymentMethod: "CASH"}, "future")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = pay("1", "5", "CASH", "same", f.acc["2410"])
	wantCode(t, err, "VALIDATION_FAILED")
	// part of it by bank transfer, with a penalty for paying late
	p1, err := pay("120000", "3000", "BANK_TRANSFER", "p1", f.acc["7220"])
	must(t, err)
	if p1.Number != "TXP000001" || p1.Status != "POSTED" || p1.PaymentMethod != "BANK_TRANSFER" || !p1.Total.Equal(dec("123000")) || p1.JournalNumber == "" {
		t.Fatalf("payment: %+v", p1)
	}
	eq(t, "tax payable after the payment", f.balance(t, "2410"), "-100000")
	eq(t, "bank", f.balance(t, "1130"), "-123000")
	eq(t, "penalty", f.balance(t, "7220"), "3000")
	j, err := f.Accounting.GetJournal(f.admin, f.propID, p1.JournalID)
	must(t, err)
	if j.Type != accounting.JournalTax || len(j.Lines) != 3 || j.Lines[0].SourceType != "TAX_PAYMENT" || j.Lines[0].SourceRef != p1.Number {
		t.Fatalf("journal: %+v", j)
	}
	got, err := f.Tax.GetReturn(f.admin, f.propID, ret.ID)
	must(t, err)
	if got.PaymentStatus != "PARTIAL" || len(got.Payments) != 1 {
		t.Fatalf("partial: %+v", got)
	}
	eq(t, "outstanding", got.Outstanding, "100000")
	// the rest, in cash; a retry pays once
	p2, err := pay("100000", "0", "CASH", "p2", 0)
	must(t, err)
	replay, err := pay("100000", "0", "CASH", "p2", 0)
	must(t, err)
	if replay.ID != p2.ID || f.Count(t, `SELECT count(*) FROM tax_payments`) != 2 {
		t.Fatalf("a replay pays once: %+v", replay)
	}
	eq(t, "tax payable paid in full", f.balance(t, "2410"), "0")
	got, err = f.Tax.GetReturn(f.admin, f.propID, ret.ID)
	must(t, err)
	if got.PaymentStatus != "PAID" || !got.Outstanding.IsZero() {
		t.Fatalf("paid: %+v", got)
	}
	_, err = pay("1", "0", "CASH", "more", 0)
	wantCode(t, err, "VALIDATION_FAILED")
	// the books say nothing is owed, and so do the returns and the folios
	l, err := f.Tax.Liability(f.admin, f.propID, nil)
	must(t, err)
	if len(l.Taxes) != 1 || len(l.Accounts) != 1 {
		t.Fatalf("liability: %+v", l)
	}
	eq(t, "collected", l.Taxes[0].Collected, "220000")
	eq(t, "paid", l.Taxes[0].Paid, "220000")
	eq(t, "owed", l.Owed, "0")
	eq(t, "the books against the taxes", l.Accounts[0].Difference, "0")
	// voiding: a return with live payments cannot be voided; a payment is voided with an approval and its journal reversed
	_, err = f.Tax.VoidReturn(f.admin, f.propID, ret.ID, taxfiling.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "TAX_RETURN_HAS_PAYMENTS")
	_, err = f.Tax.VoidPayment(f.admin, f.propID, p1.ID, taxfiling.VoidInput{Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermTaxView)
	_, err = f.Tax.VoidPayment(viewer, f.propID, p1.ID, taxfiling.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	v, err := f.Tax.VoidPayment(f.admin, f.propID, p1.ID, taxfiling.VoidInput{Reason: "paid twice", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.VoidJournalID == nil {
		t.Fatalf("voided: %+v", v)
	}
	eq(t, "tax payable owed again", f.balance(t, "2410"), "-120000")
	eq(t, "penalty reversed", f.balance(t, "7220"), "0")
	_, err = f.Tax.VoidPayment(f.admin, f.propID, p1.ID, taxfiling.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "TAX_PAYMENT_ALREADY_VOIDED")
	l, err = f.Tax.Liability(f.admin, f.propID, nil)
	must(t, err)
	eq(t, "owed after the void", l.Owed, "120000")
	eq(t, "still agrees with the books", l.Accounts[0].Difference, "0")
	past := d("2026-09-30")
	then, err := f.Tax.Liability(f.admin, f.propID, &past)
	must(t, err)
	eq(t, "owed at the end of the month", then.Owed, "220000")
	eq(t, "filed then", then.Taxes[0].Filed, "0")
	eq(t, "not on a return then", then.Taxes[0].Unfiled, "220000")
	future := d("2027-01-01")
	_, err = f.Tax.Liability(f.admin, f.propID, &future)
	wantCode(t, err, "VALIDATION_FAILED")
	// a tax that is not on a liability account is posted to the tax payable account of the system
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'tax.payment_%'`); n != 3 {
		t.Fatalf("%d payment audit rows", n)
	}
	f.requireBalanced(t)
}

func (f *fx) requireBalanced(t *testing.T) {
	t.Helper()
	var dr, cr string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM gl_journal_lines WHERE property_id = $1`, f.propID).Scan(&dr, &cr))
	if dr != cr {
		t.Fatalf("the books do not balance: %s %s", dr, cr)
	}
}

func TestConcurrentFilingAndPaying(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.busyMonth(t)
	var wg sync.WaitGroup
	errs := make([]error, 5)
	rets := make([]taxfiling.Return, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rets[i], errs[i] = f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "own-"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	var ret taxfiling.Return
	won := 0
	for i, err := range errs {
		if err == nil {
			won++
			ret = rets[i]
		} else if e := asApp(err); e == nil || e.Code != "TAX_RETURN_EXISTS" {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || f.Count(t, `SELECT count(*) FROM tax_returns`) != 1 {
		t.Fatalf("one return wins: %d", won)
	}
	// several payments, each for the whole tax: one wins
	pays := make([]error, 5)
	for i := range pays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, pays[i] = f.Tax.PayReturn(f.admin, f.propID, ret.ID, taxfiling.PayInput{PaymentDate: d("2026-10-01"), Amount: ret.Tax, PaymentMethod: "BANK_TRANSFER"}, "pay-"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	won = 0
	for _, err := range pays {
		if err == nil {
			won++
		} else if e := asApp(err); e == nil || e.Code != "VALIDATION_FAILED" {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || f.Count(t, `SELECT count(*) FROM tax_payments`) != 1 {
		t.Fatalf("one payment wins: %d", won)
	}
	eq(t, "tax payable", f.balance(t, "2410"), "0")
	f.requireBalanced(t)
}

func TestTheWorksheetAndTheReturnAreDocuments(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.busyMonth(t)
	sep := d("2026-09-01")
	ws, err := f.Docs.TaxWorksheetPDF(f.admin, f.propID, pr.TaxID, sep)
	must(t, err)
	if len(ws.PDF) < 500 || string(ws.PDF[:5]) != "%PDF-" || ws.Filename != "tax-worksheet-PB1-2026-09.pdf" {
		t.Fatalf("worksheet: %q %d", ws.Filename, len(ws.PDF))
	}
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: sep, FilingReference: "SPTPD-1"}, "k1")
	must(t, err)
	_, err = f.Tax.PayReturn(f.admin, f.propID, ret.ID, taxfiling.PayInput{PaymentDate: d("2026-10-01"), Amount: dec("100000"), PaymentMethod: "CASH", ReferenceNumber: "NTPN-1"}, "p1")
	must(t, err)
	doc, err := f.Docs.TaxReturnPDF(f.admin, f.propID, ret.ID)
	must(t, err)
	if string(doc.PDF[:5]) != "%PDF-" || doc.Filename != "tax-return-TXR000001.pdf" {
		t.Fatalf("return: %q", doc.Filename)
	}
	// the worksheet of a filed month is the return
	again, err := f.Docs.TaxWorksheetPDF(f.admin, f.propID, pr.TaxID, sep)
	must(t, err)
	if again.Filename != doc.Filename {
		t.Fatalf("filed month: %q", again.Filename)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Docs.TaxReturnPDF(nobody, f.propID, ret.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Docs.TaxReturnPDF(f.admin, f.propID, 999999)
	wantCode(t, err, "TAX_RETURN_NOT_FOUND")
}

func TestThePenaltyOfATaxPaymentCarriesItsDepartment(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.busyMonth(t)
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "k1")
	must(t, err)
	dep, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "XTRA", Name: "Extra"})
	must(t, err)
	missing := int64(999999)
	pay := func(key string, dept *int64) error {
		_, err := f.Tax.PayReturn(f.admin, f.propID, ret.ID, taxfiling.PayInput{
			PaymentDate: d("2026-10-01"), Amount: dec("1000"), Penalty: dec("50"), PenaltyAccountID: f.acc["6130"], DepartmentID: dept, PaymentMethod: "BANK_TRANSFER",
		}, key)
		return err
	}
	wantCode(t, pay("p0", &missing), "VALIDATION_FAILED")
	must(t, pay("p1", &dep.ID))
	var got *int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT department_id FROM gl_journal_lines WHERE property_id = $1 AND account_id = $2 ORDER BY id DESC LIMIT 1`, f.propID, f.acc["6130"]).Scan(&got))
	if got == nil || *got != dep.ID {
		t.Fatalf("department of the penalty line: %v", got)
	}
}
