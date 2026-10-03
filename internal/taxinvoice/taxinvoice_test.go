package taxinvoice_test

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/taxinvoice"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

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
	restaurant       int64 // charges VAT
	minibar          int64 // charges no VAT
	acme             companies.Company
	n                int
}

// setup: a property on 30 Sep 2026 (IDR) whose restaurant charges VAT (10%), a company ACME with a tax number, and, if pkp, the property is PKP.
func setup(t *testing.T, pkp bool) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, email: email}
	vat, err := e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "PPN", Name: "VAT", Rate: "10", TaxKind: "VAT", GLAccountCode: "2420", IsActive: true})
	must(t, err)
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'RESTAURANT'`, p.ID).Scan(&f.restaurant))
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, p.ID).Scan(&f.minibar))
	_, err = e.Billing.ReplaceRules(admin, p.ID, f.restaurant, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}})
	must(t, err)
	if pkp {
		_, err = e.Tax.ChangeSettings(admin, p.ID, taxfiling.SettingsInput{EffectiveFrom: roomstest.BD, IsPKP: true, NPWP: "01.234.567.8-901.000", PKPNumber: "PKP-77", SignerName: "A. Owner", SignerTitle: "Director"})
		must(t, err)
	}
	f.acme, err = e.Companies.Create(admin, p.ID, companies.Input{Code: "ACME", Name: "ACME Ltd", Address: "Jl. Mawar 2", City: "Jakarta", TaxID: "02.345.678.9-012.000", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	return f
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.email, Password: roomstest.Password}
}

// stayFolio adds a folio of a stay with one charge of cost (restaurant, with VAT, or minibar, without), in a room of its own.
func (f *fx) stayFolio(t *testing.T, charge int64, cost string) (folioID, stayID int64) {
	t.Helper()
	f.n++
	room := string(rune('0'+f.n)) + "01"
	typ := f.RoomType(t, f.admin, f.propID, "T"+room)
	r := f.Room(t, f.admin, f.propID, typ.ID, room)
	stayID = f.Stay(t, f.tenantID, f.propID, typ.ID, r.ID, "2026-09-28", "2026-09-30")
	var res int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.reservation_id FROM stays s JOIN reservation_rooms l ON l.id = s.reservation_room_id WHERE s.id = $1`, stayID).Scan(&res))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		f.tenantID, f.propID, "SF"+room, res, stayID).Scan(&folioID))
	_, err := f.Folios.PostCharge(f.admin, f.propID, folioID, "c-"+room, folios.ChargeInput{ChargeCodeID: charge, Quantity: "1", UnitPrice: &cost})
	must(t, err)
	return folioID, stayID
}

func (f *fx) checkOut(t *testing.T, stayID int64) {
	t.Helper()
	must(t, f.Exec(t, `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = $1`, stayID))
}

// cityLedgerInvoice transfers amount of a fresh folio (a restaurant charge of cost, VAT on top) to ACME and invoices the transfer.
func (f *fx) cityLedgerInvoice(t *testing.T, cost, amount string) (cityledger.Invoice, int64) {
	t.Helper()
	folio, stay := f.stayFolio(t, f.restaurant, cost)
	res, err := f.Folios.Transfer(f.admin, f.propID, folio, "tr-"+cost+amount, folios.TransferInput{CompanyID: f.acme.ID, Amount: amount})
	must(t, err)
	f.checkOut(t, stay)
	inv, err := f.CityLedger.CreateInvoice(f.admin, f.propID, f.acme.ID, "ci-"+cost+amount, cityledger.InvoiceInput{PaymentIDs: []int64{res.Payment.ID}})
	must(t, err)
	return inv, folio
}

func (f *fx) issueCL(t *testing.T, id int64, key string) (taxinvoice.Invoice, error) {
	t.Helper()
	return f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: id}, key)
}

func (f *fx) closeFolio(t *testing.T, folio int64) {
	t.Helper()
	must(t, f.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, folio))
}

func blockerCodes(t *testing.T, err error) []string {
	t.Helper()
	e := asApp(err)
	if e == nil || e.Code != "TAX_INVOICE_NOT_READY" {
		t.Fatalf("want TAX_INVOICE_NOT_READY: %v", err)
	}
	var out []string
	for _, b := range e.Context["blockers"].([]taxinvoice.Blocker) {
		out = append(out, b.Code)
	}
	return out
}

func has(list []string, code string) bool {
	for _, c := range list {
		if c == code {
			return true
		}
	}
	return false
}

func TestATaxInvoiceForACityLedgerInvoiceCopiesTheSellerAndTheBuyer(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	if !strings.HasPrefix(inv.Ref, "TXI") || inv.Status != "ISSUED" || inv.IssueDate != roomstest.BD || inv.SourceType != taxinvoice.SourceCityLedgerInvoice || inv.CityLedgerInvoiceNumber != cli.InvoiceNumber {
		t.Fatalf("invoice: %+v", inv)
	}
	if inv.Seller.Name == "" || inv.Seller.NPWP != "01.234.567.8-901.000" || inv.Seller.PKPNumber != "PKP-77" || inv.Seller.SignerName != "A. Owner" {
		t.Fatalf("seller: %+v", inv.Seller)
	}
	if inv.Buyer.Name != "ACME Ltd" || inv.Buyer.NPWP != "023456789012000" || inv.Buyer.Address != "Jl. Mawar 2, Jakarta" {
		t.Fatalf("buyer: %+v", inv.Buyer)
	}
	if len(inv.Lines) != 1 || inv.Lines[0].ChargeCode != "RESTAURANT" {
		t.Fatalf("lines: %+v", inv.Lines)
	}
	eq(t, "taxable base", inv.TaxableBase, "110000")
	eq(t, "VAT", inv.VATAmount, "11000")
	eq(t, "line rate", inv.Lines[0].Rate, "10")
	// a retry returns it; a second invoice for the same source is refused; the number is gapless
	again, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	if again.ID != inv.ID || f.Count(t, `SELECT count(*) FROM tax_invoices`) != 1 {
		t.Fatalf("a replay issues nothing: %+v", again)
	}
	_, err = f.issueCL(t, cli.ID, "k2")
	wantCode(t, err, "TAX_INVOICE_EXISTS")
	if f.Count(t, `SELECT count(*) FROM document_sequences WHERE sequence_type = 'TAX_INVOICE' AND next_value = 2`) != 1 {
		t.Fatal("a refused invoice uses no number")
	}
	// what was copied does not change with the company
	_, err = f.Companies.Update(f.admin, f.propID, f.acme.ID, companies.Patch{Name: ptr("ACME Renamed")})
	must(t, err)
	got, err := f.TaxInvoice.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	if got.Buyer.Name != "ACME Ltd" {
		t.Fatalf("the buyer is as it was issued: %+v", got.Buyer)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'tax.invoice_issued'`); n != 1 {
		t.Fatalf("%d audit rows", n)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAPartTransferTakesItsShareOfTheVAT(t *testing.T) {
	f := setup(t, true)
	// the folio is 121,000 with VAT; half of it is transferred
	cli, _ := f.cityLedgerInvoice(t, "110000", "60500")
	inv, err := f.issueCL(t, cli.ID, "half")
	must(t, err)
	eq(t, "taxable base", inv.TaxableBase, "55000")
	eq(t, "VAT", inv.VATAmount, "5500")
	// a share that does not divide: the rounded totals are the totals of the lines
	odd, _ := f.cityLedgerInvoice(t, "110000", "100000")
	oddInv, err := f.issueCL(t, odd.ID, "odd")
	must(t, err)
	var base, vat decimal.Decimal
	for _, l := range oddInv.Lines {
		base, vat = base.Add(l.Base), vat.Add(l.VAT)
	}
	if !base.Equal(oddInv.TaxableBase) || !vat.Equal(oddInv.VATAmount) {
		t.Fatalf("lines %s and %s, invoice %s and %s", base, vat, oddInv.TaxableBase, oddInv.VATAmount)
	}
	eq(t, "VAT of a share of 100,000 of 121,000", oddInv.VATAmount, "9091")
}

func TestATaxInvoiceForAClosedFolioNeedsTheBuyer(t *testing.T) {
	f := setup(t, true)
	folio, stay := f.stayFolio(t, f.restaurant, "200000")
	issue := func(buyer *taxinvoice.Party, key string) (taxinvoice.Invoice, error) {
		return f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceFolio, FolioID: folio, Buyer: buyer}, key)
	}
	good := &taxinvoice.Party{Name: "Siti Nurhaliza", NPWP: "3171.0123.4567.8901", Address: "Jl. Melati 5"}
	_, err := issue(good, "f0")
	if !has(blockerCodes(t, err), "SOURCE_NOT_CLOSED") {
		t.Fatalf("an open folio is not invoiced: %v", err)
	}
	f.checkOut(t, stay)
	f.closeFolio(t, folio)
	_, err = issue(nil, "f1")
	codes := blockerCodes(t, err)
	if !has(codes, "BUYER_NAME_REQUIRED") || !has(codes, "BUYER_NPWP_INVALID") {
		t.Fatalf("blockers: %v", codes)
	}
	_, err = issue(&taxinvoice.Party{Name: "X", NPWP: "12345"}, "f2")
	if !has(blockerCodes(t, err), "BUYER_NPWP_INVALID") {
		t.Fatalf("a short tax number: %v", err)
	}
	inv, err := issue(good, "f3")
	must(t, err)
	if inv.SourceType != taxinvoice.SourceFolio || inv.FolioNumber == "" || inv.Buyer.NPWP != "3171012345678901" || inv.Buyer.Name != "Siti Nurhaliza" {
		t.Fatalf("invoice: %+v", inv)
	}
	eq(t, "VAT", inv.VATAmount, "20000")
	_, err = issue(good, "f4")
	wantCode(t, err, "TAX_INVOICE_EXISTS")
}

func TestNothingIsInvoicedWithoutPKPOrVAT(t *testing.T) {
	f := setup(t, false)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	_, err := f.issueCL(t, cli.ID, "k1")
	if !has(blockerCodes(t, err), "NOT_PKP") {
		t.Fatalf("a property that is not PKP: %v", err)
	}
	pv, err := f.TaxInvoice.Preview(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID})
	must(t, err)
	if pv.Ready || len(pv.Lines) != 1 || pv.SourceRef != cli.InvoiceNumber || pv.Buyer.Name != "ACME Ltd" {
		t.Fatalf("the preview says what it would be: %+v", pv)
	}
	if f.Count(t, `SELECT count(*) FROM tax_invoices`) != 0 {
		t.Fatal("a preview issues nothing")
	}
	// PKP, but the charge carries no VAT
	g := setup(t, true)
	folio, stay := g.stayFolio(t, g.minibar, "100000")
	res, err := g.Folios.Transfer(g.admin, g.propID, folio, "tr", folios.TransferInput{CompanyID: g.acme.ID, Amount: "100000"})
	must(t, err)
	g.checkOut(t, stay)
	plain, err := g.CityLedger.CreateInvoice(g.admin, g.propID, g.acme.ID, "ci", cityledger.InvoiceInput{PaymentIDs: []int64{res.Payment.ID}})
	must(t, err)
	_, err = g.issueCL(t, plain.ID, "k")
	if !has(blockerCodes(t, err), "NO_VAT") {
		t.Fatalf("no VAT: %v", err)
	}
	// a company without a tax number
	h := setup(t, true)
	_, err = h.Companies.Update(h.admin, h.propID, h.acme.ID, companies.Patch{TaxID: ptr("")})
	must(t, err)
	hc, _ := h.cityLedgerInvoice(t, "110000", "121000")
	_, err = h.issueCL(t, hc.ID, "k")
	if !has(blockerCodes(t, err), "BUYER_NPWP_INVALID") {
		t.Fatalf("a buyer without a tax number: %v", err)
	}
}

func TestVoidingAndReplacingATaxInvoice(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	// the city ledger invoice cannot be voided before its tax invoice
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, cli.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	wantCode(t, err, "CITY_LEDGER_INVOICE_HAS_TAX_INVOICE")
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "wrong buyer"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, inv.ID, taxinvoice.VoidInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	// a replacement of an invoice that is not void is refused
	_, err = f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID, ReplacesInvoiceID: &inv.ID}, "r0")
	wantCode(t, err, "TAX_INVOICE_REPLACE_INVALID")
	v, err := f.TaxInvoice.VoidInvoice(f.admin, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "wrong buyer", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.VoidReason != "wrong buyer" || v.VoidedAt == nil {
		t.Fatalf("voided: %+v", v)
	}
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "TAX_INVOICE_ALREADY_VOIDED")
	// the replacement points to it, and an invoice is replaced once
	rep, err := f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID, ReplacesInvoiceID: &inv.ID}, "r1")
	must(t, err)
	if rep.ReplacesInvoiceID == nil || *rep.ReplacesInvoiceID != inv.ID || rep.Ref == inv.Ref {
		t.Fatalf("replacement: %+v", rep)
	}
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, rep.ID, taxinvoice.VoidInput{Reason: "again", Approval: f.approval()})
	must(t, err)
	_, err = f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID, ReplacesInvoiceID: &inv.ID}, "r2")
	wantCode(t, err, "TAX_INVOICE_ALREADY_REPLACED")
	// a replacement is of an invoice of the same source
	other, _ := f.cityLedgerInvoice(t, "50000", "55000")
	_, err = f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: other.ID, ReplacesInvoiceID: &inv.ID}, "r3")
	wantCode(t, err, "TAX_INVOICE_REPLACE_INVALID")
	// and once the tax invoice is void, so can the city ledger invoice be, when no other tax invoice is live
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, cli.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
}

func TestTheOfficialNumberIsRecordedOnce(t *testing.T) {
	f := setup(t, true)
	a, _ := f.cityLedgerInvoice(t, "110000", "121000")
	b, _ := f.cityLedgerInvoice(t, "50000", "55000")
	ia, err := f.issueCL(t, a.ID, "ka")
	must(t, err)
	ib, err := f.issueCL(t, b.ID, "kb")
	must(t, err)
	_, err = f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ia.ID, taxinvoice.DJPNumberInput{Number: "not valid!"})
	wantCode(t, err, "VALIDATION_FAILED")
	got, err := f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ia.ID, taxinvoice.DJPNumberInput{Number: "010.000-26.12345678"})
	must(t, err)
	if got.DJPNumber != "010.000-26.12345678" {
		t.Fatalf("number: %+v", got)
	}
	_, err = f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ia.ID, taxinvoice.DJPNumberInput{Number: "010.000-26.99999999"})
	wantCode(t, err, "TAX_INVOICE_NUMBER_SET")
	_, err = f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ib.ID, taxinvoice.DJPNumberInput{Number: "010.000-26.12345678"})
	wantCode(t, err, "TAX_INVOICE_NUMBER_TAKEN")
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, ib.ID, taxinvoice.VoidInput{Reason: "x", Approval: f.approval()})
	must(t, err)
	_, err = f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ib.ID, taxinvoice.DJPNumberInput{Number: "010.000-26.55555555"})
	wantCode(t, err, "TAX_INVOICE_ALREADY_VOIDED")
	// the database refuses any other change
	if err := f.Exec(t, `UPDATE tax_invoices SET vat_amount = 1 WHERE id = $1`, ia.ID); err == nil {
		t.Error("an invoice was changed")
	}
	if err := f.Exec(t, `UPDATE tax_invoices SET djp_number = 'x' WHERE id = $1`, ia.ID); err == nil {
		t.Error("the official number was changed")
	}
	if err := f.Exec(t, `DELETE FROM tax_invoices`); err == nil {
		t.Error("an invoice was deleted")
	}
	if err := f.Exec(t, `UPDATE tax_invoice_lines SET vat_amount = 1`); err == nil {
		t.Error("a line was changed")
	}
}

func TestExportingAPeriodRecordsABatchAndCanBeRepeated(t *testing.T) {
	f := setup(t, true)
	a, _ := f.cityLedgerInvoice(t, "110000", "121000")
	b, _ := f.cityLedgerInvoice(t, "50000", "55000")
	ia, err := f.issueCL(t, a.ID, "ka")
	must(t, err)
	ib, err := f.issueCL(t, b.ID, "kb")
	must(t, err)
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, ib.ID, taxinvoice.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	_, err = f.TaxInvoice.SetDJPNumber(f.admin, f.propID, ia.ID, taxinvoice.DJPNumberInput{Number: "010.000-26.12345678"})
	must(t, err)

	day := civil.MustParseDate("2026-09-30")
	_, err = f.TaxInvoice.ExportInvoices(f.admin, f.propID, taxinvoice.ExportInput{From: day.AddDays(10), To: day.AddDays(20)})
	wantCode(t, err, "TAX_INVOICE_EXPORT_EMPTY")
	_, err = f.TaxInvoice.ExportInvoices(f.admin, f.propID, taxinvoice.ExportInput{From: day, To: day.AddDays(-1)})
	wantCode(t, err, "VALIDATION_FAILED")
	file, err := f.TaxInvoice.ExportInvoices(f.admin, f.propID, taxinvoice.ExportInput{From: day, To: day})
	must(t, err)
	if file.InvoiceCount != 2 || file.Format != "CSV" || len(file.SHA256) != 64 || !strings.HasSuffix(file.FileName, ".csv") {
		t.Fatalf("export: %+v", file.Export)
	}
	if len(file.Content) < 3 || file.Content[0] != 0xEF {
		t.Fatal("the file starts with a byte order mark")
	}
	rows, err := csv.NewReader(strings.NewReader(string(file.Content[3:]))).ReadAll()
	must(t, err)
	if len(rows) != 3 || rows[0][0] != "invoice_ref" {
		t.Fatalf("rows: %v", rows)
	}
	first := rows[1] // oldest first
	if first[0] != ia.Ref || first[1] != "ISSUED" || first[3] != "010.000-26.12345678" || first[4] != "012345678901000" || first[6] != "023456789012000" || first[14] != "110000" || first[16] != "11000" {
		t.Fatalf("row: %v", first)
	}
	if rows[2][0] != ib.Ref || rows[2][1] != "VOIDED" {
		t.Fatalf("a void invoice is exported as void: %v", rows[2])
	}
	// the batch is kept with the invoices in it; a second export is a batch of its own with the same content
	again, err := f.TaxInvoice.ExportInvoices(f.admin, f.propID, taxinvoice.ExportInput{From: day, To: day})
	must(t, err)
	if again.ID == file.ID || again.SHA256 != file.SHA256 {
		t.Fatalf("again: %+v", again.Export)
	}
	list, err := f.TaxInvoice.Exports(f.admin, f.propID)
	must(t, err)
	if len(list) != 2 || list[0].ID != again.ID || f.Count(t, `SELECT count(*) FROM tax_invoice_export_items`) != 4 {
		t.Fatalf("exports: %+v", list)
	}
	if err := f.Exec(t, `DELETE FROM tax_invoice_exports`); err == nil {
		t.Error("an export was deleted")
	}
}

func TestCoverageShowsTheFoliosWithVATThatHaveNoTaxInvoice(t *testing.T) {
	f := setup(t, true)
	cli, folio := f.cityLedgerInvoice(t, "110000", "121000")
	other, _ := f.stayFolio(t, f.restaurant, "50000")
	day := civil.MustParseDate("2026-09-30")
	cov, err := f.TaxInvoice.CoverageOf(f.admin, f.propID, day, day)
	must(t, err)
	eq(t, "VAT collected", cov.VATCollected, "16000")
	eq(t, "VAT invoiced", cov.VATInvoiced, "0")
	if len(cov.Uncovered) != 2 {
		t.Fatalf("uncovered: %+v", cov.Uncovered)
	}
	_, err = f.issueCL(t, cli.ID, "k")
	must(t, err)
	cov, err = f.TaxInvoice.CoverageOf(f.admin, f.propID, day, day)
	must(t, err)
	eq(t, "VAT invoiced", cov.VATInvoiced, "11000")
	eq(t, "the difference", cov.Difference, "5000")
	if len(cov.Uncovered) != 1 || cov.Uncovered[0].FolioID != other {
		t.Fatalf("only the folio not invoiced is left (the other is covered through its city ledger invoice, %d): %+v", folio, cov.Uncovered)
	}
	_, err = f.TaxInvoice.CoverageOf(f.admin, f.propID, day, day.AddDays(-1))
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestOnlyOneTaxInvoiceIsIssuedForASourceUnderARace(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.issueCL(t, cli.ID, "race-"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if e := asApp(err); e == nil || e.Code != "TAX_INVOICE_EXISTS" {
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || f.Count(t, `SELECT count(*) FROM tax_invoices`) != 1 {
		t.Fatalf("%d invoices were issued: %v", ok, errs)
	}
	// the numbers of the refused ones were given back
	if f.Count(t, `SELECT count(*) FROM document_sequences WHERE sequence_type = 'TAX_INVOICE' AND next_value = 2`) != 1 {
		t.Fatal("the sequence has gaps")
	}
}

func TestPermissionsAndTenants(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k")
	must(t, err)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermTaxView)
	if list, err := f.TaxInvoice.Invoices(viewer, f.propID, taxinvoice.Filter{}); err != nil || len(list) != 1 {
		t.Fatalf("a viewer reads the invoices: %v", err)
	}
	if _, err := f.TaxInvoice.GetInvoice(viewer, f.propID, inv.ID); err != nil {
		t.Fatalf("a viewer reads an invoice: %v", err)
	}
	_, err = f.TaxInvoice.Issue(viewer, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID}, "v")
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.TaxInvoice.Preview(viewer, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.TaxInvoice.ExportInvoices(viewer, f.propID, taxinvoice.ExportInput{From: roomstest.BD, To: roomstest.BD})
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, other.ID)
	_, err = f.TaxInvoice.GetInvoice(otherAdmin, f.propID, inv.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestTheTaxInvoiceIsADocument(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k")
	must(t, err)
	doc, err := f.Docs.TaxInvoicePDF(f.admin, f.propID, inv.ID)
	must(t, err)
	if !strings.HasPrefix(string(doc.PDF), "%PDF") || doc.Filename != "tax-invoice-"+inv.Ref+".pdf" {
		t.Fatalf("document: %s %d", doc.Filename, len(doc.PDF))
	}
	_, err = f.Docs.TaxInvoicePDF(f.admin, f.propID, 999999)
	wantCode(t, err, "TAX_INVOICE_NOT_FOUND")
}

// creditNote makes a credit note with VAT (net 55,000, VAT 5,500: half of an invoice of 110,000 and 11,000) against an invoice or a transfer.
func (f *fx) creditNote(t *testing.T, invoiceID, paymentID *int64, key string) (cityledger.Adjustment, error) {
	t.Helper()
	var ppn, allowance int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM taxes WHERE property_id = $1 AND code = 'PPN'`, f.propID).Scan(&ppn))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4160'`, f.propID).Scan(&allowance))
	return f.CityLedger.CreateCreditNote(f.admin, f.propID, key, cityledger.CreditNoteInput{InvoiceID: invoiceID, PaymentID: paymentID, Reason: "dispute", Approval: f.approval(),
		Lines: []cityledger.CreditNoteLineInput{{Description: "Restaurant corrected", AccountID: allowance, NetAmount: "55000", TaxID: &ppn}}})
}

// VAT on a credit note changes the faktur the buyer has: it waits for the tax invoice to be void, and the replacement is on the reduced invoice.
func TestACreditNoteWithVATAsksTheTaxInvoiceToBeVoidedFirst(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	eq(t, "the faktur as first issued", inv.VATAmount, "11000")
	_, err = f.creditNote(t, &cli.ID, nil, "cn1")
	wantCode(t, err, "CREDIT_NOTE_TAX_INVOICE_LIVE")
	if f.Count(t, `SELECT count(*) FROM city_ledger_adjustments`) != 0 {
		t.Fatal("nothing was made")
	}
	_, err = f.TaxInvoice.VoidInvoice(f.admin, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "credit note coming", Approval: f.approval()})
	must(t, err)
	note, err := f.creditNote(t, &cli.ID, nil, "cn2")
	must(t, err)
	eq(t, "credit note", dec(note.Amount), "60500")
	rep, err := f.TaxInvoice.Issue(f.admin, f.propID, taxinvoice.IssueInput{SourceType: taxinvoice.SourceCityLedgerInvoice, CityLedgerInvoiceID: cli.ID, ReplacesInvoiceID: &inv.ID}, "k2")
	must(t, err)
	eq(t, "replacement base", rep.TaxableBase, "55000")
	eq(t, "replacement VAT", rep.VATAmount, "5500")
}

// A credit note of the transfer before it is invoiced: the invoice asks the net and so does its faktur.
func TestTheTaxInvoiceOfAnInvoiceMadeFromACreditedTransferCoversTheNet(t *testing.T) {
	f := setup(t, true)
	folio, stay := f.stayFolio(t, f.restaurant, "110000")
	res, err := f.Folios.Transfer(f.admin, f.propID, folio, "tr", folios.TransferInput{CompanyID: f.acme.ID, Amount: "121000"})
	must(t, err)
	f.checkOut(t, stay)
	_, err = f.creditNote(t, nil, &res.Payment.ID, "cn1")
	must(t, err)
	cli, err := f.CityLedger.CreateInvoice(f.admin, f.propID, f.acme.ID, "ci", cityledger.InvoiceInput{PaymentIDs: []int64{res.Payment.ID}})
	must(t, err)
	eq(t, "the invoice asks the net", dec(cli.Total), "60500")
	inv, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	eq(t, "base", inv.TaxableBase, "55000")
	eq(t, "VAT", inv.VATAmount, "5500")
}

// A credit note without tax, or with a tax that is not VAT, does not touch the tax invoice.
func TestACreditNoteWithoutVATLeavesTheTaxInvoiceBe(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	_, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	var allowance int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4160'`, f.propID).Scan(&allowance))
	_, err = f.CityLedger.CreateCreditNote(f.admin, f.propID, "cn", cityledger.CreditNoteInput{InvoiceID: &cli.ID, Reason: "goodwill", Approval: f.approval(),
		Lines: []cityledger.CreditNoteLineInput{{Description: "Goodwill", AccountID: allowance, NetAmount: "1000"}}})
	must(t, err)
}
