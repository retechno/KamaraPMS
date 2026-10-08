package frontdesk_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// Audit F-06: reversing a check-in with a company folio. A cancelled stay has no OPEN folio; an empty company folio is closed with the stay; a company folio that holds money blocks the
// reversal (the caller voids or refunds first); a charge on any folio blocks it as before. The guest folio keeps its payments (the deposit behaviour).

type scene struct {
	*fx
	res        reservations.Reservation
	stay       frontdesk.CheckInResult
	company    int64
	companyFol int64 // the company folio opened at check-in by the billing instruction
}

func (f *fx) approval(t *testing.T) *iam.ApprovalInput {
	t.Helper()
	var email string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT email FROM users WHERE tenant_id = $1 AND is_tenant_admin ORDER BY id LIMIT 1`, f.tenantID).Scan(&email))
	return &iam.ApprovalInput{Email: email, Password: roomstest.Password}
}

// withCompanyFolio: a reservation checked in, whose line is billed to a company, so that the stay has a guest folio and a company folio.
func withCompanyFolio(t *testing.T) *scene {
	t.Helper()
	f := setup(t)
	s := &scene{fx: f}
	s.res = f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	var err error
	s.stay, err = f.checkIn(t, f.admin, s.res, &f.r101, "")
	must(t, err)
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme Corp', 1000000) RETURNING id`, f.tenantID, f.propID).Scan(&s.company))
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, s.res.ID, s.res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: s.company}})
	must(t, err)
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY'`, s.stay.Stay.ID).Scan(&s.companyFol))
	return s
}

func (s *scene) reverse(t *testing.T) (frontdesk.ReverseResult, error) {
	t.Helper()
	return s.Front.ReverseCheckIn(s.admin, s.propID, s.stay.Stay.ID, frontdesk.ReverseInput{Version: s.stay.Stay.Version, Reason: "wrong guest"})
}

func (s *scene) pay(t *testing.T, folio int64, key, amount string) folios.PaymentResult {
	t.Helper()
	r, err := s.Folios.PostPayment(s.admin, s.propID, folio, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
	must(t, err)
	return r
}

// openFoliosOfCancelledStays is the invariant of migration 00064, asked of the service and not of the trigger.
func (s *scene) openFoliosOfCancelledStays(t *testing.T) int {
	t.Helper()
	return s.Count(t, `SELECT count(*) FROM folios f JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id WHERE st.status = 'CANCELLED' AND f.status = 'OPEN'`)
}

func (s *scene) folioStatus(t *testing.T, id int64) (status string, stay *int64) {
	t.Helper()
	must(t, s.Pool.QueryRow(context.Background(), `SELECT status, stay_id FROM folios WHERE id = $1`, id).Scan(&status, &stay))
	return
}

func (s *scene) balance(t *testing.T, folio int64) string {
	t.Helper()
	var b string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0)::text FROM folio_items WHERE folio_id = $1`, folio).Scan(&b))
	return b
}

func TestReverseClosesTheEmptyCompanyFolio(t *testing.T) { // A
	s := withCompanyFolio(t)
	audits := s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.closed'`)
	rev, err := s.reverse(t)
	must(t, err)
	if rev.Stay.Status != "CANCELLED" || rev.Folio.ID != s.stay.Folio.ID || rev.Folio.Status != "OPEN" {
		t.Fatalf("reversal: %+v", rev)
	}
	if len(rev.ClosedFolios) != 1 || rev.ClosedFolios[0].ID != s.companyFol || rev.ClosedFolios[0].Status != "CLOSED" {
		t.Fatalf("closed_folios: %+v", rev.ClosedFolios)
	}
	status, stay := s.folioStatus(t, s.companyFol)
	if status != "CLOSED" || stay == nil || *stay != s.stay.Stay.ID {
		t.Fatalf("the company folio is closed and stays linked to the cancelled stay as history: %s %v", status, stay)
	}
	if status, stay := s.folioStatus(t, s.stay.Folio.ID); status != "OPEN" || stay != nil {
		t.Fatalf("the guest folio is the open deposit folio again: %s %v", status, stay)
	}
	if n := s.openFoliosOfCancelledStays(t); n != 0 {
		t.Fatalf("%d open folios on a cancelled stay", n)
	}
	// the audit: one folio.closed with the reason, and the closed folios in the reversal entry
	if got := s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.closed'`) - audits; got != 1 {
		t.Fatalf("%d folio.closed entries", got)
	}
	var closedEntry, reversedEntry string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'folio.closed' AND entity_id = $1`, s.companyFol).Scan(&closedEntry))
	must(t, s.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'stay.check_in_reversed' AND entity_id = $1`, s.stay.Stay.ID).Scan(&reversedEntry))
	if !contains(closedEntry, "check-in reversed") || !contains(reversedEntry, "closed_folios") || !contains(reversedEntry, "FOL") {
		t.Fatalf("audit: %s | %s", closedEntry, reversedEntry)
	}
	// checking in again opens a new company folio for the new stay (the instruction is still on the line); the old one stays closed
	must(t, s.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, s.r101.ID))
	again, err := s.checkIn(t, s.admin, s.reload(t, s.res.ID), &s.r101, "")
	must(t, err)
	var newCompany int64
	must(t, s.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY'`, again.Stay.ID).Scan(&newCompany))
	if newCompany == s.companyFol {
		t.Fatal("a new company folio for the new stay")
	}
	if status, _ := s.folioStatus(t, s.companyFol); status != "CLOSED" {
		t.Fatalf("the old company folio stays closed: %s", status)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestReverseIsRefusedWhileACompanyFolioHoldsAPayment(t *testing.T) { // B
	s := withCompanyFolio(t)
	s.pay(t, s.companyFol, "p1", "50000")
	before := struct{ items, audits, journals int }{s.Count(t, `SELECT count(*) FROM folio_items`), s.Count(t, `SELECT count(*) FROM audit_logs`), s.Count(t, `SELECT count(*) FROM gl_journals`)}
	_, err := s.reverse(t)
	e := code(t, err, "CHECK_IN_HAS_PAYMENTS")
	rows, _ := e.Context["folios"].([]map[string]any)
	if len(rows) != 1 || rows[0]["folio_id"] != s.companyFol || rows[0]["folio_type"] != "COMPANY" || rows[0]["balance"] != "-50000" || rows[0]["folio_number"] == "" {
		t.Fatalf("the context names the folio and its balance: %+v", e.Context)
	}
	if status, _ := s.folioStatus(t, s.companyFol); status != "OPEN" || s.balance(t, s.companyFol) != "-50000.000" {
		t.Fatal("the company folio is untouched")
	}
	var stayStatus string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT status FROM stays WHERE id = $1`, s.stay.Stay.ID).Scan(&stayStatus))
	if stayStatus != "OPEN" {
		t.Fatalf("the stay stays open: %s", stayStatus)
	}
	if status, stay := s.folioStatus(t, s.stay.Folio.ID); status != "OPEN" || stay == nil {
		t.Fatal("the guest folio is still linked")
	}
	after := struct{ items, audits, journals int }{s.Count(t, `SELECT count(*) FROM folio_items`), s.Count(t, `SELECT count(*) FROM audit_logs`), s.Count(t, `SELECT count(*) FROM gl_journals`)}
	if after != before {
		t.Fatalf("a refused reversal leaves no trace: %+v -> %+v", before, after)
	}
	if s.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('stay.check_in_reversed', 'folio.closed')`) != 0 {
		t.Fatal("no audit entry for a refused reversal")
	}
}

func TestReverseSucceedsOnceThePaymentIsVoided(t *testing.T) { // B2
	s := withCompanyFolio(t)
	p := s.pay(t, s.companyFol, "p1", "50000")
	_, err := s.reverse(t)
	wantCode(t, err, "CHECK_IN_HAS_PAYMENTS")
	_, err = s.Folios.Void(s.admin, s.propID, p.Payment.ID, folios.CorrectionInput{Reason: "wrong folio", Approval: s.approval(t)})
	must(t, err)
	rev, err := s.reverse(t)
	must(t, err)
	if len(rev.ClosedFolios) != 1 || s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatalf("closed %+v", rev.ClosedFolios)
	}
	// the ledger kept both entries of the payment and its void: nothing was removed or rewritten
	if n := s.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, s.companyFol); n != 2 {
		t.Fatalf("payment and reversal both stay on the ledger: %d", n)
	}
}

func TestAPaymentOnTheGuestFolioStaysADeposit(t *testing.T) { // B3
	s := withCompanyFolio(t)
	s.pay(t, s.stay.Folio.ID, "pg", "70000")
	rev, err := s.reverse(t)
	must(t, err)
	if rev.Folio.ID != s.stay.Folio.ID || rev.Folio.Balance != "-70000" {
		t.Fatalf("the guest folio keeps the money: %+v", rev.Folio)
	}
	if status, stay := s.folioStatus(t, s.stay.Folio.ID); status != "OPEN" || stay != nil {
		t.Fatalf("it is the open deposit folio of the reservation: %s %v", status, stay)
	}
	if s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatal("open folio on a cancelled stay")
	}
}

func TestReverseSucceedsOnceARefundBringsTheBalanceBackToZero(t *testing.T) { // B4
	s := withCompanyFolio(t)
	p := s.pay(t, s.companyFol, "p1", "50000")
	_, err := s.Folios.Refund(s.admin, s.propID, p.Payment.ID, "r1", folios.RefundInput{Amount: "50000", PaymentMethod: "CASH", Reason: "returned", Approval: s.approval(t)})
	must(t, err)
	rev, err := s.reverse(t)
	must(t, err)
	if len(rev.ClosedFolios) != 1 || s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatalf("closed %+v", rev.ClosedFolios)
	}
}

func TestAChargeOnAnyFolioBlocksTheReversal(t *testing.T) { // C
	for _, onCompany := range []bool{false, true} {
		s := withCompanyFolio(t)
		folio := s.stay.Folio.ID
		if onCompany {
			folio = s.companyFol
		}
		it, err := s.Folios.PostCharge(s.admin, s.propID, folio, "c1", folios.ChargeInput{ChargeCodeID: s.laundry(t), Quantity: "1", UnitPrice: ptr("10000")})
		must(t, err)
		_, err = s.reverse(t)
		wantCode(t, err, "CHECK_IN_HAS_CHARGES")
		// a charge that was reversed is still a charge: the reversal of the check-in stays refused
		_, err = s.Folios.Reverse(s.admin, s.propID, it.Item.ID, folios.CorrectionInput{Reason: "posted twice", Approval: s.approval(t)})
		must(t, err)
		_, err = s.reverse(t)
		wantCode(t, err, "CHECK_IN_HAS_CHARGES")
		if status, _ := s.folioStatus(t, s.companyFol); status != "OPEN" {
			t.Fatal("nothing was closed")
		}
	}
}

func TestAClosedCompanyFolioIsLeftAlone(t *testing.T) { // D
	s := withCompanyFolio(t)
	must(t, s.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, s.companyFol))
	closedAt := ""
	must(t, s.Pool.QueryRow(context.Background(), `SELECT closed_at::text FROM folios WHERE id = $1`, s.companyFol).Scan(&closedAt))
	audits := s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.closed'`)
	rev, err := s.reverse(t)
	must(t, err)
	if len(rev.ClosedFolios) != 0 {
		t.Fatalf("nothing to close: %+v", rev.ClosedFolios)
	}
	var again string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT closed_at::text FROM folios WHERE id = $1`, s.companyFol).Scan(&again))
	if status, _ := s.folioStatus(t, s.companyFol); status != "CLOSED" || again != closedAt {
		t.Fatal("not reopened, not closed again")
	}
	if s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.closed'`) != audits {
		t.Fatal("no new folio.closed entry")
	}
	if s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatal("open folio on a cancelled stay")
	}
}

// The state after a reversal and a payment on the company folio ran at the same moment: it is one of the two orders, never a mix.
func (s *scene) requireConsistent(t *testing.T, reverseErr, payErr error) {
	t.Helper()
	status, _ := s.folioStatus(t, s.companyFol)
	var stayStatus string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT status FROM stays WHERE id = $1`, s.stay.Stay.ID).Scan(&stayStatus))
	bal := s.balance(t, s.companyFol)
	switch {
	case reverseErr == nil && payErr == nil:
		t.Fatalf("both cannot succeed: the stay is %s, the company folio %s with %s", stayStatus, status, bal)
	case reverseErr == nil:
		// the reversal won: the folio is closed and empty, and the payment found it closed
		var ae *apperr.Error
		if !errors.As(payErr, &ae) || ae.Code != "FOLIO_CLOSED" || status != "CLOSED" || bal != "0" || stayStatus != "CANCELLED" {
			t.Fatalf("reversal won: payment err %v, folio %s %s, stay %s", payErr, status, bal, stayStatus)
		}
	default:
		// the payment won: the reversal saw the money and refused, and nothing changed
		var ae *apperr.Error
		if payErr != nil || !errors.As(reverseErr, &ae) || ae.Code != "CHECK_IN_HAS_PAYMENTS" || status != "OPEN" || stayStatus != "OPEN" || bal == "0" {
			t.Fatalf("payment won: reverse err %v, payment err %v, folio %s %s, stay %s", reverseErr, payErr, status, bal, stayStatus)
		}
	}
	if s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatal("an open folio on a cancelled stay")
	}
}

func TestReverseAndPaymentRaceOnTheCompanyFolio(t *testing.T) { // E1
	for i := 0; i < 8; i++ {
		s := withCompanyFolio(t)
		var wg sync.WaitGroup
		var reverseErr, payErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, reverseErr = s.reverse(t)
		}()
		go func() {
			defer wg.Done()
			_, payErr = s.Folios.PostPayment(s.admin, s.propID, s.companyFol, "race", folios.PaymentInput{Amount: "50000", PaymentMethod: "CASH"})
		}()
		wg.Wait()
		s.requireConsistent(t, reverseErr, payErr)
	}
}

func TestReverseAndChargeRaceOnTheCompanyFolio(t *testing.T) { // E2
	for i := 0; i < 8; i++ {
		s := withCompanyFolio(t)
		var wg sync.WaitGroup
		var reverseErr, chargeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, reverseErr = s.reverse(t)
		}()
		go func() {
			defer wg.Done()
			_, chargeErr = s.Folios.PostCharge(s.admin, s.propID, s.companyFol, "race", folios.ChargeInput{ChargeCodeID: s.laundry(t), Quantity: "1", UnitPrice: ptr("10000")})
		}()
		wg.Wait()
		var stayStatus string
		must(t, s.Pool.QueryRow(context.Background(), `SELECT status FROM stays WHERE id = $1`, s.stay.Stay.ID).Scan(&stayStatus))
		status, _ := s.folioStatus(t, s.companyFol)
		charges := s.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE'`, s.companyFol)
		switch {
		case reverseErr == nil && chargeErr == nil:
			t.Fatalf("both cannot succeed (stay %s, folio %s, %d charges)", stayStatus, status, charges)
		case reverseErr == nil:
			wantCode(t, chargeErr, "FOLIO_CLOSED")
			if charges != 0 || status != "CLOSED" || stayStatus != "CANCELLED" {
				t.Fatalf("reversal won: %d charges, folio %s, stay %s", charges, status, stayStatus)
			}
		default:
			wantCode(t, reverseErr, "CHECK_IN_HAS_CHARGES")
			if chargeErr != nil || charges != 1 || status != "OPEN" || stayStatus != "OPEN" {
				t.Fatalf("charge won: err %v, %d charges, folio %s, stay %s", chargeErr, charges, status, stayStatus)
			}
		}
		if s.openFoliosOfCancelledStays(t) != 0 {
			t.Fatal("an open folio on a cancelled stay")
		}
	}
}

func TestTwoReversalsAtOnceHaveOneWinner(t *testing.T) { // E3
	s := withCompanyFolio(t)
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([]frontdesk.ReverseResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.reverse(t)
		}()
	}
	wg.Wait()
	wins := 0
	for i := range errs {
		if errs[i] == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(errs[i], &ae) || (ae.Code != "STAY_NOT_OPEN" && ae.Code != "VERSION_CONFLICT") {
			t.Fatalf("the losers conflict: %v", errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("one winner, got %d", wins)
	}
	if got := s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.closed' AND entity_id = $1`, s.companyFol); got != 1 {
		t.Fatalf("the company folio is closed once: %d entries", got)
	}
	if s.openFoliosOfCancelledStays(t) != 0 {
		t.Fatal("an open folio on a cancelled stay")
	}
}

func TestReverseIsIsolatedByTenantAndProperty(t *testing.T) {
	s := withCompanyFolio(t)
	other := s.Tenant(t, "XYZ")
	otherProp := s.Property(t, other.ID, "SG")
	otherAdmin, _ := s.AdminAccount(t, other.ID)
	_, err := s.Front.ReverseCheckIn(otherAdmin, otherProp.ID, s.stay.Stay.ID, frontdesk.ReverseInput{Version: s.stay.Stay.Version, Reason: "x"})
	wantCode(t, err, "STAY_NOT_FOUND")
	second := s.Property(t, s.tenantID, "JKT")
	_, err = s.Front.ReverseCheckIn(s.admin, second.ID, s.stay.Stay.ID, frontdesk.ReverseInput{Version: s.stay.Stay.Version, Reason: "x"})
	wantCode(t, err, "STAY_NOT_FOUND")
	if status, _ := s.folioStatus(t, s.companyFol); status != "OPEN" {
		t.Fatal("nothing happened to the folios")
	}
	if s.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('stay.check_in_reversed', 'folio.closed')`) != 0 {
		t.Fatal("no audit side effect")
	}
}

// The regression the finding left: checking out still closes the company folio of the stay, posting to a closed folio is refused, and a reversed stay's company folio is not in the way.
func TestCheckOutStillClosesTheCompanyFolio(t *testing.T) {
	s := withCompanyFolio(t)
	in := frontdesk.CheckOutInput{Version: s.stay.Stay.Version, ConfirmEarlyDeparture: true}
	_, err := s.Front.CheckOut(s.admin, s.propID, s.stay.Stay.ID, in) // posts the night; the guest folio is then not balanced and says by how much
	e := code(t, err, "FOLIO_NOT_BALANCED")
	owedFolio, owed := int64(0), ""
	for _, row := range e.Context["folios"].([]map[string]any) {
		owedFolio, _ = row["folio_id"].(int64)
		owed, _ = row["balance"].(string)
	}
	if owed == "" || owedFolio != s.companyFol {
		t.Fatalf("the night is billed to the company folio by the instruction, and it is the one not balanced: %v", e.Context)
	}
	s.pay(t, owedFolio, "settle", owed)
	in.Version = s.detail(t, s.stay.Stay.ID).Stay.Version
	out, err := s.Front.CheckOut(s.admin, s.propID, s.stay.Stay.ID, in)
	must(t, err)
	if len(out.Folios) != 2 {
		t.Fatalf("both folios close at check-out: %+v", out.Folios)
	}
	_, err = s.Folios.PostPayment(s.admin, s.propID, s.companyFol, "late", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	wantCode(t, err, "FOLIO_CLOSED")
}
