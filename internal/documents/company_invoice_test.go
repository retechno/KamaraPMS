package documents_test

import (
	"context"
	"strings"
	"testing"

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
