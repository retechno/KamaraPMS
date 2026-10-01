package cityledger_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// stayFolio adds an open folio with a balance of amount on a stay in a room of its own, and returns its folio id and
// stay id. The guest is still in house until checkOut is called.
func (f *fx) stayFolio(t *testing.T, room, amount string) (folioID, stayID int64) {
	t.Helper()
	typ := f.RoomType(t, f.admin, f.propID, "T"+room)
	r := f.Room(t, f.admin, f.propID, typ.ID, room)
	stayID = f.Stay(t, f.tenantID, f.propID, typ.ID, r.ID, "2026-09-28", "2026-09-30")
	var res int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.reservation_id FROM stays s JOIN reservation_rooms l ON l.id = s.reservation_room_id WHERE s.id = $1`, stayID).Scan(&res))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		f.tenantID, f.propID, "SF"+room, res, stayID).Scan(&folioID))
	unit := amount
	_, err := f.Folios.PostCharge(f.admin, f.propID, folioID, "sc-"+room, folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	return folioID, stayID
}

func (f *fx) checkOut(t *testing.T, stayID int64) {
	t.Helper()
	if err := f.Exec(t, `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, stayID); err != nil {
		t.Fatal(err)
	}
}

// transferred transfers the whole balance of a fresh stay folio and returns the payment id and the stay id.
func (f *fx) transferred(t *testing.T, company int64, room, amount string) (payment, stay int64) {
	t.Helper()
	folio, stay := f.stayFolio(t, room, amount)
	res, err := f.transfer(folio, company, "tr-"+room, amount)
	must(t, err)
	return res.Payment.ID, stay
}

func (f *fx) invoice(company int64, key string, ids ...int64) (cityledger.Invoice, error) {
	return f.CityLedger.CreateInvoice(f.admin, f.propID, company, key, cityledger.InvoiceInput{PaymentIDs: ids, Notes: "September stays"})
}

func TestInvoiceCombinesCheckedOutTransfersOfOneCompany(t *testing.T) {
	f := setup(t)
	p1, s1 := f.transferred(t, f.acme.ID, "101", "300000")
	p2, s2 := f.transferred(t, f.acme.ID, "102", "200000")
	p3, _ := f.transferred(t, f.acme.ID, "103", "50000") // still in house
	other := f.company(t, "OTHER", "")
	po, so := f.transferred(t, other.ID, "104", "70000")
	f.checkOut(t, s1)
	f.checkOut(t, s2)
	f.checkOut(t, so)

	cands, err := f.CityLedger.Candidates(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(cands) != 3 || !cands[0].Invoiceable || !cands[1].Invoiceable || cands[2].Invoiceable || cands[2].StayStatus != "OPEN" || cands[0].RoomNumbers != "101" {
		t.Fatalf("candidates: %+v", cands)
	}

	inv, err := f.invoice(f.acme.ID, "i1", p1, p2)
	must(t, err)
	if !strings.HasPrefix(inv.InvoiceNumber, "CINV") || inv.Total != "500000" || inv.Status != "ISSUED" || len(inv.Lines) != 2 || inv.InvoiceDate != roomstest.BD ||
		inv.DueDate != roomstest.BD.AddDays(30) || inv.Notes != "September stays" {
		t.Fatalf("invoice: %+v", inv)
	}
	if inv.Lines[0].RoomNumbers != "101" || inv.Lines[0].CheckedOutAt == nil || inv.Lines[0].FolioNumber != "SF101" || inv.Lines[0].Amount != "300000" {
		t.Fatalf("line: %+v", inv.Lines[0])
	}
	// the same key replays; the same transfers cannot be invoiced twice
	again, err := f.invoice(f.acme.ID, "i1", p1, p2)
	must(t, err)
	if again.ID != inv.ID || f.Count(t, `SELECT count(*) FROM city_ledger_invoices`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	_, err = f.invoice(f.acme.ID, "i2", p1)
	wantCode(t, err, "TRANSFER_NOT_AVAILABLE")
	_, err = f.invoice(f.acme.ID, "i1b", p3)
	wantCode(t, err, "STAY_NOT_CHECKED_OUT")
	_, err = f.invoice(f.acme.ID, "i3", po) // another company's transfer
	wantCode(t, err, "TRANSFER_NOT_AVAILABLE")
	_, err = f.invoice(f.acme.ID, "i4")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.invoice(other.ID, "i1", po)
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
	if acc := f.balance(t, f.acme.ID); acc.Balance != "550000" {
		t.Fatalf("an invoice does not change what is owed: %+v", acc)
	}

	// the late checkout joins the next invoice; the invoices are listed newest first
	cands, err = f.CityLedger.Candidates(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(cands) != 1 || cands[0].PaymentID != p3 {
		t.Fatalf("what is left: %+v", cands)
	}
	var st3 int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT f.stay_id FROM payments p JOIN folios f ON f.id = p.folio_id WHERE p.id = $1`, p3).Scan(&st3))
	f.checkOut(t, st3)
	second, err := f.invoice(f.acme.ID, "i5", p3)
	must(t, err)
	list, err := f.CityLedger.Invoices(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != inv.ID || len(list[0].Lines) != 0 {
		t.Fatalf("list: %+v", list)
	}
	got, err := f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	if len(got.Lines) != 2 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.invoice_issued'`) != 2 {
		t.Fatalf("detail: %+v", got)
	}
	_, err = f.CityLedger.GetInvoice(f.admin, f.propID, 9999)
	wantCode(t, err, "INVOICE_NOT_FOUND")
}

func TestVoidingAnInvoiceReleasesItsTransfers(t *testing.T) {
	f := setup(t)
	p1, s1 := f.transferred(t, f.acme.ID, "101", "300000")
	f.checkOut(t, s1)
	inv, err := f.invoice(f.acme.ID, "i1", p1)
	must(t, err)
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "wrong company"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	v, err := f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "wrong company", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.VoidReason != "wrong company" || v.ApprovedBy == nil {
		t.Fatalf("void: %+v", v)
	}
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "INVOICE_ALREADY_VOIDED")
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, 9999, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "INVOICE_NOT_FOUND")
	// the transfer can be invoiced again, and the voided invoice stays listed
	again, err := f.invoice(f.acme.ID, "i2", p1)
	must(t, err)
	list, err := f.CityLedger.Invoices(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if again.ID == inv.ID || len(list) != 2 || list[1].Status != "VOIDED" || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.invoice_voided'`) != 1 {
		t.Fatalf("after void: %+v %+v", again, list)
	}
}

func TestInvoicesAreAppendOnlyAndNeedTheirPermission(t *testing.T) {
	f := setup(t)
	p1, s1 := f.transferred(t, f.acme.ID, "101", "300000")
	f.checkOut(t, s1)
	inv, err := f.invoice(f.acme.ID, "i1", p1)
	must(t, err)
	if err := f.Exec(t, `UPDATE city_ledger_invoices SET total = 1 WHERE id = $1`, inv.ID); err == nil {
		t.Fatal("an invoice total cannot change")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_invoices WHERE id = $1`, inv.ID); err == nil {
		t.Fatal("an invoice cannot be deleted")
	}
	if err := f.Exec(t, `UPDATE city_ledger_invoice_lines SET amount = 1`); err == nil {
		t.Fatal("a line's amount cannot change")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_invoice_lines`); err == nil {
		t.Fatal("a line cannot be deleted")
	}
	if err := f.Exec(t, `TRUNCATE city_ledger_invoices CASCADE`); err == nil {
		t.Fatal("invoices cannot be truncated")
	}
	reader := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	_, err = f.CityLedger.CreateInvoice(reader, f.propID, f.acme.ID, "x", cityledger.InvoiceInput{PaymentIDs: []int64{p1}})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.CityLedger.VoidInvoice(reader, f.propID, inv.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.CityLedger.GetInvoice(reader, f.propID, inv.ID); err != nil {
		t.Fatal(err)
	}
	invoicer := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerInvoice)
	_, err = f.CityLedger.GetInvoice(invoicer, f.propID, inv.ID)
	wantCode(t, err, "PERMISSION_DENIED")
}

// Several invoices over the same transfers at once: exactly one wins, the others are told the transfers are taken.
func TestInvoicesCannotClaimTheSameTransferUnderRace(t *testing.T) {
	f := setup(t)
	p1, s1 := f.transferred(t, f.acme.ID, "101", "300000")
	p2, s2 := f.transferred(t, f.acme.ID, "102", "200000")
	f.checkOut(t, s1)
	f.checkOut(t, s2)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.invoice(f.acme.ID, "race-"+string(rune('a'+i)), p1, p2)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "TRANSFER_NOT_AVAILABLE")
		}
	}
	if ok != 1 || f.Count(t, `SELECT count(*) FROM city_ledger_invoice_lines WHERE released_at IS NULL`) != 2 {
		t.Fatalf("%d invoices won", ok)
	}
}
