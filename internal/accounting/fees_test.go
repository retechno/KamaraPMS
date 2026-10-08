package accounting_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// Audit F-08: the manual cancellation fee and no-show fee. A fee is an ordinary charge posted by the existing posting path to CANCEL_FEE or NO_SHOW_FEE; the event is never charged twice.

func (h *hotel) cancelled(t *testing.T) reservations.Reservation {
	t.Helper()
	res := h.book(t, "2026-10-02", "2026-10-04")
	out, err := h.Res.Cancel(h.admin, h.propID, res.ID, res.Version, "guest cancelled late")
	must(t, err)
	return out.Reservation
}

func (h *hotel) noShow(t *testing.T) (reservations.Reservation, int64) {
	t.Helper()
	res := h.book(t, "2026-09-30", "2026-10-02")
	out, err := h.Res.NoShow(h.admin, h.propID, res.ID, res.Rooms[0].ID, res.Version, "did not arrive")
	must(t, err)
	return out, res.Rooms[0].ID
}

func (h *hotel) fee(t *testing.T, resID int64, in folios.FeeInput) (folios.FeeResult, error) {
	t.Helper()
	return h.Folios.PostReservationFee(h.admin, h.propID, resID, in)
}

func cancelFee(amount string) folios.FeeInput {
	return folios.FeeInput{Type: "CANCEL_FEE", Amount: amount, Reason: "late cancellation"}
}

func noShowFee(line int64, amount string) folios.FeeInput {
	return folios.FeeInput{Type: "NO_SHOW_FEE", ReservationRoomID: &line, Amount: amount, Reason: "guest did not arrive"}
}

func appError(t *testing.T, err error, want string) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
	return ae
}

func appCode(t *testing.T, err error, want string) {
	t.Helper()
	_ = appError(t, err, want)
}

func (h *hotel) folioCount(t *testing.T) int {
	return h.Count(t, `SELECT count(*) FROM folios`)
}

func TestACancellationFeeIsAnOrdinaryChargeOnTheReservationFolio(t *testing.T) { // 1, 17-19, 22-27
	h := setupHotel(t)
	res := h.cancelled(t)
	if h.folioCount(t) != 0 {
		t.Fatal("a reservation without a deposit has no folio")
	}
	got, err := h.fee(t, res.ID, cancelFee("500000"))
	must(t, err)
	it := got.Item
	if it.TransactionType != "CHARGE" || it.ChargeCode != "CANCEL_FEE" || it.Debit != "500000" || it.Credit != "0" || it.GroupCode != "A" || it.RevenueAccountCode == nil || *it.RevenueAccountCode != "4510" ||
		it.BusinessDate.String() != "2026-09-30" || it.Reason != "late cancellation" {
		t.Fatalf("item: %+v", it)
	}
	if !got.FolioCreated || got.FolioBalance != "500000" || h.folioCount(t) != 1 {
		t.Fatalf("the folio of the reservation is opened by the fee: %+v", got)
	}
	var typ string
	var stay, company *int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_type, stay_id, bill_to_company_id FROM folios WHERE id = $1`, got.FolioID).Scan(&typ, &stay, &company))
	if typ != "GUEST" || stay != nil || company != nil {
		t.Fatalf("the folio is the one deposits go to: %s %v %v", typ, stay, company)
	}
	// the audit entry names the business context
	var entry string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'reservation.cancel_fee_posted' AND entity_id = $1`, res.ID).Scan(&entry))
	for _, want := range []string{"CANCEL_FEE", "500000", "late cancellation", "folio_item_id", "folio_id", "\"folio_created\": true"} {
		if !strings.Contains(entry, want) {
			t.Errorf("audit lacks %q: %s", want, entry)
		}
	}
	// the day close journals it through the normal path: revenue on 4510 once, the journal balances, the guest ledger agrees with the folio
	h.closeDay(t)
	if got := h.balance(t, "4510"); !got.Equal(dec("-500000")) {
		t.Fatalf("4510: %s", got)
	}
	h.requireBalanced(t)
	if g, want := h.balance(t, "1210"), h.folioBalance(t, got0(h, res.ID)); !g.Equal(want) {
		t.Fatalf("guest ledger %s, folio %s", g, want)
	}
}

func got0(h *hotel, resID int64) int64 {
	var id int64
	_ = h.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE reservation_id = $1`, resID).Scan(&id)
	return id
}

func TestAFeeReusesTheFolioTheDepositWentTo(t *testing.T) { // 17
	h := setupHotel(t)
	res := h.book(t, "2026-10-02", "2026-10-04")
	dep, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "d1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	res = h.reload(t, res.ID)
	_, err = h.Res.Cancel(h.admin, h.propID, res.ID, res.Version, "guest cancelled")
	must(t, err)
	got, err := h.fee(t, res.ID, cancelFee("200000"))
	must(t, err)
	if got.FolioID != dep.Payment.FolioID || got.FolioCreated || h.folioCount(t) != 1 {
		t.Fatalf("the fee goes to the deposit folio: %+v", got)
	}
	if got.FolioBalance != "-100000" { // 200,000 fee against 300,000 deposit: 100,000 left to refund
		t.Fatalf("balance %s", got.FolioBalance)
	}
}

func (h *hotel) reload(t *testing.T, id int64) reservations.Reservation {
	t.Helper()
	r, err := h.Res.Get(h.admin, h.propID, id)
	must(t, err)
	return r
}

func TestACancellationFeeNeedsACancelledReservation(t *testing.T) { // 2
	h := setupHotel(t)
	res := h.book(t, "2026-10-02", "2026-10-04")
	_, err := h.fee(t, res.ID, cancelFee("100000"))
	appCode(t, err, "RESERVATION_NOT_CANCELLED")
	if h.Count(t, `SELECT count(*) FROM folio_items`)+h.folioCount(t) != 0 {
		t.Fatal("a refused fee leaves nothing, not even a folio")
	}
}

func TestFeeValidation(t *testing.T) { // 3
	h := setupHotel(t)
	res := h.cancelled(t)
	line := res.Rooms[0].ID
	for name, in := range map[string]folios.FeeInput{
		"zero":                 cancelFee("0"),
		"negative":             cancelFee("-100"),
		"not a number":         cancelFee("abc"),
		"empty":                cancelFee(""),
		"more decimals":        cancelFee("100.5"),
		"no reason":            {Type: "CANCEL_FEE", Amount: "100000"},
		"blank reason":         {Type: "CANCEL_FEE", Amount: "100000", Reason: "  "},
		"a room on a cancel":   {Type: "CANCEL_FEE", Amount: "100000", Reason: "x", ReservationRoomID: &line},
		"no room on a no-show": {Type: "NO_SHOW_FEE", Amount: "100000", Reason: "x"},
		"another type":         {Type: "MINIBAR", Amount: "100000", Reason: "x"},
		"no type":              {Amount: "100000", Reason: "x"},
	} {
		_, err := h.fee(t, res.ID, in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		appCode(t, err, "VALIDATION_FAILED")
	}
	if h.folioCount(t) != 0 {
		t.Fatal("no folio for a refused request")
	}
}

func TestTheSameCancellationIsNotChargedTwice(t *testing.T) { // 4, 20
	h := setupHotel(t)
	res := h.cancelled(t)
	first, err := h.fee(t, res.ID, cancelFee("500000"))
	must(t, err)
	for _, amount := range []string{"500000", "1", "999999"} { // the amount is not the key
		_, err = h.fee(t, res.ID, cancelFee(amount))
		e := appError(t, err, "FEE_ALREADY_POSTED")
		if e.Context["folio_item_id"] != first.Item.ID {
			t.Fatalf("the conflict names the fee that was posted: %v", e.Context)
		}
	}
	if h.folioCount(t) != 1 || h.Count(t, `SELECT count(*) FROM folio_items`) != 1 {
		t.Fatal("one folio, one fee")
	}
	// a fee that was reversed is still the fee of this cancellation: the replacement goes through the correction mechanism, not through the fee again
	_, err = h.Folios.Reverse(h.admin, h.propID, first.Item.ID, folios.CorrectionInput{Reason: "waived", Approval: h.approval()})
	must(t, err)
	_, err = h.fee(t, res.ID, cancelFee("500000"))
	appCode(t, err, "FEE_ALREADY_POSTED")
	// but a second cancellation (after a reinstatement) is another event
	res = h.reload(t, res.ID)
	res2, err := h.Res.Reinstate(h.admin, h.propID, res.ID, res.Version)
	must(t, err)
	h.Clock.Advance(time.Minute) // the fake clock of the tests stands still; a real one has moved on between two cancellations
	_, err = h.Res.Cancel(h.admin, h.propID, res2.ID, res2.Version, "cancelled again")
	must(t, err)
	again, err := h.fee(t, res.ID, cancelFee("250000"))
	must(t, err)
	if again.Item.ID == first.Item.ID || h.Count(t, `SELECT count(*) FROM folio_items WHERE charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'CANCEL_FEE')`, h.propID) != 3 {
		t.Fatal("the new cancellation is charged once, the old fee and its reversal stay in history")
	}
}

func TestTheTaxAndTheServiceChargeOfTheChargeCodeApply(t *testing.T) { // 5, 6
	h := setupHotel(t)
	svc, err := h.Billing.CreateServiceCharge(h.admin, h.propID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	vat, err := h.Billing.CreateTax(h.admin, h.propID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, IsActive: true})
	must(t, err)
	var code int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'NO_SHOW_FEE'`, h.propID).Scan(&code))
	_, err = h.Billing.ReplaceRules(h.admin, h.propID, code, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}}})
	must(t, err)
	// and the revenue account is the one the charge code names, not a fixed one
	must(t, h.Exec(t, `UPDATE charge_codes SET gl_account_code = '4590' WHERE id = $1`, code))
	res, line := h.noShow(t)
	got, err := h.fee(t, res.ID, noShowFee(line, "100000"))
	must(t, err)
	it := got.Item
	if it.ServiceChargeTotal != "10000" || it.TaxTotal != "12100" || it.Debit != "122100" || len(it.Components) != 2 || it.RevenueAccountCode == nil || *it.RevenueAccountCode != "4590" {
		t.Fatalf("100,000 + 10%% service + 11%% VAT on it, on account 4590: %+v", it)
	}
	h.closeDay(t)
	if g := h.balance(t, "4590"); !g.Equal(dec("-100000")) {
		t.Fatalf("4590: %s", g)
	}
	if g := h.balance(t, "4510"); !g.IsZero() {
		t.Fatalf("4510 must be untouched: %s", g)
	}
	h.requireBalanced(t)
}

func TestAFeeIsPostedOnTheOpenDayOnly(t *testing.T) { // 7, 8
	h := setupHotel(t)
	res := h.cancelled(t)
	h.closeDay(t) // the cancellation was on 30 Sep; today is 1 Oct, the business date of the fee
	got, err := h.fee(t, res.ID, cancelFee("500000"))
	must(t, err)
	if got.Item.BusinessDate.String() != "2026-10-01" {
		t.Fatalf("the fee is dated the open business day, not the day of the cancellation: %s", got.Item.BusinessDate)
	}
	// with no open day nothing is posted (the service gate; the database trigger of F-07 is behind it)
	must(t, h.Exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, h.propID))
	_, err = h.fee(t, res.ID, cancelFee("1"))
	appCode(t, err, "BUSINESS_DAY_NOT_FOUND")
}

func TestAFeeOfAnotherTenantOrPropertyIsNotFound(t *testing.T) { // 9, 10, 15, 16
	h := setupHotel(t)
	res := h.cancelled(t)
	other := h.Tenant(t, "XYZ")
	otherProp := h.Property(t, other.ID, "SG")
	otherAdmin, _ := h.AdminAccount(t, other.ID)
	_, err := h.Folios.PostReservationFee(otherAdmin, otherProp.ID, res.ID, cancelFee("1000"))
	appCode(t, err, "RESERVATION_NOT_FOUND")
	second := h.Property(t, h.tenantID, "JKT")
	_, err = h.Folios.PostReservationFee(h.admin, second.ID, res.ID, cancelFee("1000"))
	appCode(t, err, "RESERVATION_NOT_FOUND")
	nsRes, line := h.noShow(t)
	_, err = h.Folios.PostReservationFee(otherAdmin, otherProp.ID, nsRes.ID, noShowFee(line, "1000"))
	appCode(t, err, "RESERVATION_NOT_FOUND")
	_, err = h.Folios.PostReservationFee(h.admin, second.ID, nsRes.ID, noShowFee(line, "1000"))
	appCode(t, err, "RESERVATION_NOT_FOUND")
	// a room of another reservation is not this reservation's room
	_, err = h.fee(t, nsRes.ID, noShowFee(res.Rooms[0].ID, "1000"))
	appCode(t, err, "RESERVATION_ROOM_NOT_FOUND")
	// the permission that posts charges
	reader := h.User(t, h.tenantID, h.propID)
	_, err = h.Folios.PostReservationFee(reader, h.propID, res.ID, cancelFee("1000"))
	appCode(t, err, "PERMISSION_DENIED")
	if h.folioCount(t) != 0 {
		t.Fatal("no folio for a refused request")
	}
}

func TestANoShowFeeNeedsANoShowRoom(t *testing.T) { // 11-14
	h := setupHotel(t)
	res, line := h.noShow(t)
	got, err := h.fee(t, res.ID, noShowFee(line, "300000"))
	must(t, err)
	if got.Item.ChargeCode != "NO_SHOW_FEE" || got.Item.Debit != "300000" || !got.FolioCreated {
		t.Fatalf("%+v", got.Item)
	}
	var entry string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'reservation.no_show_fee_posted' AND entity_id = $1`, res.ID).Scan(&entry))
	if !strings.Contains(entry, "reservation_room_id") || !strings.Contains(entry, "NO_SHOW_FEE") {
		t.Fatalf("audit: %s", entry)
	}
	// twice: no
	_, err = h.fee(t, res.ID, noShowFee(line, "300000"))
	appCode(t, err, "FEE_ALREADY_POSTED")
	// a confirmed room is not a no-show, and neither is a cancelled one
	confirmed := h.book(t, "2026-10-05", "2026-10-06")
	_, err = h.fee(t, confirmed.ID, noShowFee(confirmed.Rooms[0].ID, "300000"))
	appCode(t, err, "RESERVATION_ROOM_NOT_NO_SHOW")
	gone := h.cancelled(t)
	_, err = h.fee(t, gone.ID, noShowFee(gone.Rooms[0].ID, "300000"))
	appCode(t, err, "RESERVATION_ROOM_NOT_NO_SHOW")
	// the bulk no-show does not post anything by itself
	third := h.book(t, "2026-09-30", "2026-10-02")
	_, err = h.Res.BulkNoShow(h.admin, h.propID, roomstest.BD, []int64{third.Rooms[0].ID}, "bulk")
	must(t, err)
	if h.Count(t, `SELECT count(*) FROM folio_items i JOIN charge_codes c ON c.id = i.charge_code_id WHERE c.code IN ('NO_SHOW_FEE', 'CANCEL_FEE')`) != 1 {
		t.Fatal("a no-show posts no fee by itself")
	}
}

func TestAClosedDepositFolioIsNotReused(t *testing.T) { // 21
	h := setupHotel(t)
	res := h.cancelled(t)
	first, err := h.fee(t, res.ID, cancelFee("500000"))
	must(t, err)
	_, err = h.Folios.PostPayment(h.admin, h.propID, first.FolioID, "pay", folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"})
	must(t, err)
	fo, err := h.Folios.GetFolio(h.admin, h.propID, first.FolioID)
	must(t, err)
	_, err = h.Folios.CloseFolio(h.admin, h.propID, first.FolioID, fo.Version)
	must(t, err)
	// reinstated and cancelled again: a new event, and the folio of the reservation is closed, so the fee opens a new one
	cur := h.reload(t, res.ID)
	r2, err := h.Res.Reinstate(h.admin, h.propID, cur.ID, cur.Version)
	must(t, err)
	h.Clock.Advance(time.Minute)
	_, err = h.Res.Cancel(h.admin, h.propID, r2.ID, r2.Version, "again")
	must(t, err)
	again, err := h.fee(t, res.ID, cancelFee("100000"))
	must(t, err)
	if again.FolioID == first.FolioID || !again.FolioCreated {
		t.Fatalf("a closed folio takes no posting: %+v", again)
	}
}

func TestACompanyBillingInstructionDoesNotMoveAFeeOfAReservationWithoutAStay(t *testing.T) { // 18
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-02")
	var company int64
	must(t, h.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme Corp', 1000000) RETURNING id`, h.tenantID, h.propID).Scan(&company))
	_, err := h.Folios.SetBillingInstructions(h.admin, h.propID, res.ID, res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeAll, CompanyID: company}})
	must(t, err)
	res = h.reload(t, res.ID)
	ns, err := h.Res.NoShow(h.admin, h.propID, res.ID, res.Rooms[0].ID, res.Version, "did not arrive")
	must(t, err)
	got, err := h.fee(t, ns.ID, noShowFee(res.Rooms[0].ID, "400000"))
	must(t, err)
	var typ string
	var payer *int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT folio_type, bill_to_company_id FROM folios WHERE id = $1`, got.FolioID).Scan(&typ, &payer))
	if typ != "GUEST" || payer != nil {
		t.Fatalf("the resolver of the existing rules has a company folio only for a stay in house: the fee goes to the folio of the reservation, got %s %v", typ, payer)
	}
}

func TestAFeeThatFailsLeavesNothingBehind(t *testing.T) { // 28
	h := setupHotel(t)
	res := h.cancelled(t)
	// the revenue account of the fee needs a department that nothing supplies: the posting fails after the folio was opened
	must(t, h.Exec(t, `UPDATE gl_accounts SET department_requirement = 'REQUIRED', default_department_id = NULL WHERE property_id = $1 AND code = '4510'`, h.propID))
	must(t, h.Exec(t, `UPDATE charge_codes SET department_id = NULL WHERE property_id = $1 AND code = 'CANCEL_FEE'`, h.propID))
	_, err := h.fee(t, res.ID, cancelFee("500000"))
	if err == nil {
		t.Fatal("the posting should fail")
	}
	if h.folioCount(t) != 0 || h.Count(t, `SELECT count(*) FROM folio_items`) != 0 || h.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'reservation.%fee_posted'`) != 0 {
		t.Fatal("the whole transaction rolled back: no folio, no item, no audit entry")
	}
}

func TestTwoConcurrentFeesOfOneEventPostOnce(t *testing.T) { // 29, 30
	for _, kind := range []string{FeeCancelKind, FeeNoShowKind} {
		h := setupHotel(t)
		var resID, line int64
		var mk func(amount string) folios.FeeInput
		if kind == FeeCancelKind {
			resID = h.cancelled(t).ID
			mk = cancelFee
		} else {
			r, l := h.noShow(t)
			resID, line = r.ID, l
			mk = func(amount string) folios.FeeInput { return noShowFee(line, amount) }
		}
		const n = 6
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = h.fee(t, resID, mk("100000"))
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
				continue
			}
			appCode(t, err, "FEE_ALREADY_POSTED")
		}
		if ok != 1 {
			t.Fatalf("%s: %d requests succeeded, want exactly one", kind, ok)
		}
		if h.Count(t, `SELECT count(*) FROM folio_items WHERE idempotency_key LIKE 'fee:%'`) != 1 || h.folioCount(t) != 1 {
			t.Fatalf("%s: one fee on one folio", kind)
		}
	}
}

// the two kinds, spelled once
const (
	FeeCancelKind = "CANCEL_FEE"
	FeeNoShowKind = "NO_SHOW_FEE"
)
