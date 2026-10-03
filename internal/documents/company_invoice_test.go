package documents_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

func TestCompanyInvoice(t *testing.T) {
	f := setup(t)
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "Acme Corp", Address: "Jl. Bisnis 9", City: "Jakarta", TaxID: "99.888.777.6", PaymentTermsDays: 45, IsActive: true})
	must(t, err)
	tr, err := f.Folios.Transfer(f.admin, f.propID, f.stay.Folio.ID, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "72100", ReferenceNumber: "PO-77"})
	must(t, err)
	if _, err := f.Pool.Exec(context.Background(), `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, f.stay.Stay.ID); err != nil {
		t.Fatal(err)
	}
	inv, err := f.CityLedger.CreateInvoice(f.admin, f.propID, co.ID, "i1", cityledger.InvoiceInput{PaymentIDs: []int64{tr.Payment.ID}, Notes: "Thank you"})
	must(t, err)
	doc, err := f.Docs.CompanyInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"INVOICE", inv.InvoiceNumber, "Acme Corp", "Jl. Bisnis 9", "99.888.777.6", "45 days", "PO-77", "101", "IDR 72,100", "Thank you"} {
		if !strings.Contains(s, want) {
			t.Errorf("invoice lacks %q", want)
		}
	}
	if strings.Contains(s, "VOID") || doc.Filename != "invoice-"+inv.InvoiceNumber+".pdf" {
		t.Errorf("a valid invoice is not stamped: %q", doc.Filename)
	}
	// a voided invoice is stamped
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "wrong company", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}})
	must(t, err)
	doc, err = f.Docs.CompanyInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	if s := pdfText(t, doc); !strings.Contains(s, "VOID") || !strings.Contains(s, "wrong company") {
		t.Error("a voided invoice prints as void")
	}
	desk := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Docs.CompanyInvoice(desk, f.propID, inv.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Docs.CompanyInvoice(f.admin, f.propID, 99999)
	wantCode(t, err, "INVOICE_NOT_FOUND")
}

func TestCreditNoteDocumentAndTheInvoiceThatTakesItOff(t *testing.T) {
	f := setup(t)
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "Acme Corp", Address: "Jl. Bisnis 9", City: "Jakarta", TaxID: "99.888.777.6", PaymentTermsDays: 45, IsActive: true})
	must(t, err)
	tr, err := f.Folios.Transfer(f.admin, f.propID, f.stay.Folio.ID, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "72100"})
	must(t, err)
	if _, err := f.Pool.Exec(context.Background(), `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, f.stay.Stay.ID); err != nil {
		t.Fatal(err)
	}
	var allowance int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4160'`, f.propID).Scan(&allowance))
	approval := &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
	// a credit note of the transfer before it is invoiced: the invoice asks the net
	note, err := f.CityLedger.CreateCreditNote(f.admin, f.propID, "n1", cityledger.CreditNoteInput{PaymentID: &tr.Payment.ID, Reason: "charge corrected", Approval: approval,
		Lines: []cityledger.CreditNoteLineInput{{Description: "Minibar corrected", AccountID: allowance, NetAmount: "12100"}}})
	must(t, err)
	inv, err := f.CityLedger.CreateInvoice(f.admin, f.propID, co.ID, "i1", cityledger.InvoiceInput{PaymentIDs: []int64{tr.Payment.ID}})
	must(t, err)
	doc, err := f.Docs.CompanyInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"Transfers", "72,100", "Credit note " + note.Number, "12,100", "IDR 60,000"} {
		if !strings.Contains(s, want) {
			t.Errorf("the invoice lacks %q", want)
		}
	}
	// the credit note as a document
	cn, err := f.Docs.CreditNotePDF(f.admin, f.propID, note.ID)
	must(t, err)
	s = pdfText(t, cn)
	for _, want := range []string{"CREDIT NOTE", note.Number, "Acme Corp", "99.888.777.6", "Minibar corrected", "12,100", "charge corrected"} {
		if !strings.Contains(s, want) {
			t.Errorf("the credit note lacks %q", want)
		}
	}
	if cn.Filename != "credit-note-"+note.Number+".pdf" {
		t.Errorf("filename: %s", cn.Filename)
	}
	var badDebt int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '6140'`, f.propID).Scan(&badDebt))
	wo, err := f.CityLedger.CreateWriteOff(f.admin, f.propID, "w1", cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: "1000", AccountID: badDebt, Reason: "small", Approval: approval})
	must(t, err)
	_, err = f.Docs.CreditNotePDF(f.admin, f.propID, wo.ID)
	wantCode(t, err, "NOT_A_CREDIT_NOTE")
	doc, err = f.Docs.CompanyInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	if s := pdfText(t, doc); !strings.Contains(s, "Written off") || !strings.Contains(s, "IDR 59,000") {
		t.Error("the invoice shows what was written off and what is left")
	}
}

func TestReminderLetter(t *testing.T) {
	f := setup(t)
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "Acme Corp", Address: "Jl. Bisnis 9", City: "Jakarta", TaxID: "99.888.777.6", PaymentTermsDays: 0, IsActive: true})
	must(t, err)
	tr, err := f.Folios.Transfer(f.admin, f.propID, f.stay.Folio.ID, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "72100"})
	must(t, err)
	if _, err := f.Pool.Exec(context.Background(), `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, f.stay.Stay.ID); err != nil {
		t.Fatal(err)
	}
	inv, err := f.CityLedger.CreateInvoice(f.admin, f.propID, co.ID, "i1", cityledger.InvoiceInput{PaymentIDs: []int64{tr.Payment.ID}})
	must(t, err)
	_, err = f.CityLedger.SetLateFee(f.admin, f.propID, cityledger.LateFeeInput{MonthlyRate: "3", GraceDays: 0})
	must(t, err)
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
	rem, err := f.CityLedger.CreateReminder(f.admin, f.propID, co.ID, "r1", cityledger.ReminderInput{Level: 2, Note: "Please call us"})
	must(t, err)
	doc, err := f.Docs.ReminderPDF(f.admin, f.propID, rem.ID)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"PAYMENT REMINDER", "Second reminder", rem.Number, "Acme Corp", "99.888.777.6", inv.InvoiceNumber, "72,100", "Interest", "Please call us", "past their due date"} {
		if !strings.Contains(s, want) {
			t.Errorf("the reminder lacks %q", want)
		}
	}
	if doc.Filename != "reminder-"+rem.Number+".pdf" {
		t.Errorf("filename: %s", doc.Filename)
	}
	_, err = f.Docs.ReminderPDF(f.admin, f.propID, 99999)
	wantCode(t, err, "REMINDER_NOT_FOUND")
}
