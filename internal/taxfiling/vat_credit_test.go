package taxfiling_test

import (
	"context"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/payables"
	"kamarapms/internal/taxfiling"
)

// vatFx is the setup of the VAT tests: the property is PKP from 30 Sep, the restaurant charges VAT (10%) instead of the hotel tax, and
// the profile of the VAT tax claims the input VAT of the supplier bills.
type vatFx struct {
	*fx
	vat      billingconfig.Tax
	profile  taxfiling.Profile
	supplier payables.Supplier
}

func setupVAT(t *testing.T) *vatFx {
	t.Helper()
	f := setup(t)
	v := &vatFx{fx: f}
	var err error
	v.vat, err = f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "PPN", Name: "VAT", Rate: "10", TaxKind: "VAT", GLAccountCode: "2420", IsActive: true})
	must(t, err)
	var restaurant int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'RESTAURANT'`, f.propID).Scan(&restaurant))
	_, err = f.Billing.ReplaceRules(f.admin, f.propID, restaurant, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: v.vat.ID, Sequence: 1}}})
	must(t, err)
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-09-30"), IsPKP: true, NPWP: "01.234.567.8-901.000"})
	must(t, err)
	v.profile, err = f.Tax.CreateProfile(f.admin, f.propID, taxfiling.ProfileInput{TaxID: v.vat.ID, Authority: "KPP Pratama", ClaimsInputVAT: ptr(true)})
	must(t, err)
	v.supplier, err = f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: "PLN", Name: "PLN Electricity"})
	must(t, err)
	return v
}

// bill enters a supplier bill of a cost and the VAT on it, dated on the business date unless date is given.
func (v *vatFx) bill(t *testing.T, invoice, date, cost, vat string) payables.Bill {
	t.Helper()
	if date == "" {
		day, err := v.Tenancy.CurrentBusinessDay(v.admin, v.propID)
		must(t, err)
		date = day.BusinessDate.String()
	}
	b, err := v.Payables.PostBill(v.admin, v.propID, payables.BillInput{
		SupplierID: v.supplier.ID, SupplierInvoiceNumber: invoice, BillDate: d(date),
		Lines: []payables.BillLineInput{{AccountID: v.acc["6510"], Description: "Electricity", Amount: dec(cost), VATAmount: dec(vat)}},
	}, "")
	must(t, err)
	return b
}

func (v *vatFx) fileMonth(t *testing.T, month, key string) taxfiling.Return {
	t.Helper()
	ret, err := v.Tax.FileReturn(v.admin, v.propID, taxfiling.FileInput{TaxID: v.vat.ID, PeriodStart: d(month)}, key)
	must(t, err)
	return ret
}

func (v *vatFx) worksheet(t *testing.T, month string) taxfiling.Worksheet {
	t.Helper()
	w, err := v.Tax.Worksheet(v.admin, v.propID, v.vat.ID, d(month))
	must(t, err)
	return w
}

// checkOutGuest settles the folio of the guest in house and checks them out (a stay that overstays blocks the night audit).
func (v *vatFx) checkOutGuest(t *testing.T) {
	t.Helper()
	var stayID int64
	var version int32
	must(t, v.Pool.QueryRow(context.Background(), `SELECT id, version FROM stays WHERE property_id = $1`, v.propID).Scan(&stayID, &version))
	in := frontdesk.CheckOutInput{Version: version, ConfirmEarlyDeparture: true}
	_, err := v.Front.CheckOut(v.admin, v.propID, stayID, in)
	e := asApp(err)
	if e == nil || e.Code != "FOLIO_NOT_BALANCED" {
		t.Fatalf("the folio is not balanced yet: %v", err)
	}
	balance := e.Context["folios"].([]map[string]any)[0]["balance"].(string)
	_, err = v.Folios.PostPayment(v.admin, v.propID, v.folioID, "settle", folios.PaymentInput{Amount: balance, PaymentMethod: "CASH"})
	must(t, err)
	_, err = v.Front.CheckOut(v.admin, v.propID, stayID, in)
	must(t, err)
}

// closeThrough runs the night audit until the business date is the given day.
func (v *vatFx) closeThrough(t *testing.T, until string) {
	t.Helper()
	for i := 0; i < 40; i++ {
		day, err := v.Tenancy.CurrentBusinessDay(v.admin, v.propID)
		must(t, err)
		if day.BusinessDate == d(until) {
			return
		}
		v.closeDay(t)
	}
	t.Fatalf("the business date did not reach %s", until)
}

func figures(t *testing.T, what string, o taxfiling.VATOffset, input, bf, offset, payable, carried string) {
	t.Helper()
	eq(t, what+": input claimed", o.InputClaimed, input)
	eq(t, what+": credit brought forward", o.CreditBroughtForward, bf)
	eq(t, what+": offset", o.Offset, offset)
	eq(t, what+": payable", o.Payable, payable)
	eq(t, what+": credit carried forward", o.CreditCarriedForward, carried)
}

// The example of the design: September has more input VAT than output VAT, the overpayment is carried to October and used there.
func TestAnOverpaymentOfVATIsCarriedForwardAndUsedByTheNextMonth(t *testing.T) {
	v := setupVAT(t)
	// September (a single day of the books): output 10,000, input 15,000.
	v.charge(t, "RESTAURANT", "100000", "r1")
	v.bill(t, "INV-A", "", "150000", "15000")
	v.closeDay(t)
	sep := v.worksheet(t, "2026-09-01")
	if !sep.Ready || !sep.ClaimsInputVAT || len(sep.Input) != 1 {
		t.Fatalf("September: %+v", sep)
	}
	eq(t, "September output", sep.Tax, "10000")
	figures(t, "September worksheet", sep.VATOffset, "15000", "0", "10000", "0", "5000")

	ret := v.fileMonth(t, "2026-09-01", "sep")
	figures(t, "September return", ret.VATOffset, "15000", "0", "10000", "0", "5000")
	eq(t, "September tax", ret.Tax, "10000")
	eq(t, "nothing to pay", ret.Outstanding, "0")
	if ret.PaymentStatus != "PAID" || ret.OffsetJournalID == nil || len(ret.Input) != 1 || ret.Input[0].Amount.String() != "15000" {
		t.Fatalf("September return: %+v", ret)
	}
	eq(t, "VAT payable after the offset", v.balance(t, "2420"), "0")
	eq(t, "the credit is the balance of input VAT", v.balance(t, "1425"), "5000")
	liab, err := v.Tax.Liability(v.admin, v.propID, nil)
	must(t, err)
	if len(liab.Taxes) != 1 || !liab.Taxes[0].Owed.IsZero() || liab.Taxes[0].CreditAvailable.String() != "5000" || liab.Taxes[0].Offset.String() != "10000" {
		t.Fatalf("liability: %+v", liab.Taxes)
	}
	if len(liab.Accounts) != 1 || !liab.Accounts[0].Difference.IsZero() {
		t.Fatalf("the books agree with what is owed: %+v", liab.Accounts)
	}

	// October: output 12,000, input 3,000, credit 5,000.
	v.charge(t, "RESTAURANT", "120000", "r2")
	v.bill(t, "INV-B", "", "30000", "3000")
	v.checkOutGuest(t)
	v.closeThrough(t, "2026-11-01")
	oct := v.worksheet(t, "2026-10-01")
	if !oct.Ready {
		t.Fatalf("October: %+v", oct.Blockers)
	}
	figures(t, "October worksheet", oct.VATOffset, "3000", "5000", "8000", "4000", "0")
	octRet := v.fileMonth(t, "2026-10-01", "oct")
	figures(t, "October return", octRet.VATOffset, "3000", "5000", "8000", "4000", "0")
	eq(t, "October tax", octRet.Tax, "12000")
	eq(t, "to pay", octRet.Outstanding, "4000")
	// the credit was used once: a month cannot be filed twice and the figures of September did not move
	got, err := v.Tax.GetReturn(v.admin, v.propID, ret.ID)
	must(t, err)
	figures(t, "September return, later", got.VATOffset, "15000", "0", "10000", "0", "5000")

	// the payment is of what is payable, not of the tax collected
	_, err = v.Tax.PayReturn(v.admin, v.propID, octRet.ID, taxfiling.PayInput{PaymentDate: d("2026-11-01"), Amount: dec("12000"), PaymentMethod: "CASH"}, "p-too-much")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = v.Tax.PayReturn(v.admin, v.propID, octRet.ID, taxfiling.PayInput{PaymentDate: d("2026-11-01"), Amount: dec("4000"), PaymentMethod: "CASH"}, "p1")
	must(t, err)
	eq(t, "VAT payable settled", v.balance(t, "2420"), "0")
	eq(t, "the credit is used up", v.balance(t, "1425"), "0")
	v.requireBalanced(t)

	// only the latest return can be voided, and the credit goes back with it
	_, err = v.Tax.VoidReturn(v.admin, v.propID, ret.ID, taxfiling.VoidInput{Reason: "wrong", Approval: v.approval()})
	wantCode(t, err, "TAX_RETURN_NOT_LATEST")
	pays, err := v.Tax.Payments(v.admin, v.propID, taxfiling.PaymentFilter{ReturnID: &octRet.ID})
	must(t, err)
	_, err = v.Tax.VoidPayment(v.admin, v.propID, pays[0].ID, taxfiling.VoidInput{Reason: "wrong", Approval: v.approval()})
	must(t, err)
	_, err = v.Tax.VoidReturn(v.admin, v.propID, octRet.ID, taxfiling.VoidInput{Reason: "wrong", Approval: v.approval()})
	must(t, err)
	eq(t, "the offset is reversed", v.balance(t, "1425"), "8000")
	eq(t, "the whole VAT collected is owed again", v.balance(t, "2420"), "-12000")
	again := v.worksheet(t, "2026-10-01")
	figures(t, "October again", again.VATOffset, "3000", "5000", "8000", "4000", "0")
	refiled := v.fileMonth(t, "2026-10-01", "oct2")
	figures(t, "October refiled", refiled.VATOffset, "3000", "5000", "8000", "4000", "0")
	if n := v.Count(t, `SELECT count(*) FROM tax_return_input_claims WHERE released_at IS NULL`); n != 2 {
		t.Fatalf("%d live claims, want 2 (one per bill)", n)
	}
	if n := v.Count(t, `SELECT count(*) FROM tax_return_input_claims WHERE released_at IS NOT NULL`); n != 1 {
		t.Fatalf("%d released claims, want 1 (the voided October return)", n)
	}
}

// Partial use of the credit: the credit is larger than what is collected, and what is left goes on.
func TestTheCreditIsUsedUpToTheTaxCollected(t *testing.T) {
	v := setupVAT(t)
	v.charge(t, "RESTAURANT", "60000", "r1") // output 6,000
	v.bill(t, "INV-A", "", "150000", "15000")
	v.closeDay(t)
	ret := v.fileMonth(t, "2026-09-01", "sep")
	figures(t, "September", ret.VATOffset, "15000", "0", "6000", "0", "9000")
}

func TestAMonthWithoutInputVATPaysAllItsTax(t *testing.T) {
	v := setupVAT(t)
	v.charge(t, "RESTAURANT", "100000", "r1")
	v.closeDay(t)
	ret := v.fileMonth(t, "2026-09-01", "sep")
	figures(t, "September", ret.VATOffset, "0", "0", "0", "10000", "0")
	if ret.OffsetJournalID != nil {
		t.Fatal("no offset, no journal")
	}
	eq(t, "to pay", ret.Outstanding, "10000")
}

func TestOtherTaxesAreNotOffset(t *testing.T) {
	f := setup(t)
	pr := f.profile(t) // the hotel tax PB1
	f.busyMonth(t)
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "k")
	must(t, err)
	figures(t, "PB1", ret.VATOffset, "0", "0", "0", "220000", "0")
	// only the profile of a VAT tax can claim input VAT
	_, err = f.Tax.UpdateProfile(f.admin, f.propID, pr.ID, taxfiling.ProfilePatch{ClaimsInputVAT: ptr(true)})
	e := wantFieldError(t, err, "claims_input_vat")
	_ = e
}

func wantFieldError(t *testing.T, err error, field string) bool {
	t.Helper()
	e := asApp(err)
	if e == nil || e.Code != "VALIDATION_FAILED" {
		t.Fatalf("want a validation error on %s: %v", field, err)
	}
	for _, fe := range e.Fields {
		if fe.Field == field {
			return true
		}
	}
	t.Fatalf("no error on %s: %+v", field, e.Fields)
	return false
}

// A bill voided before it is claimed is never claimed; one voided after is taken back by the next return; a bill that comes late is
// claimed by the next open month.
func TestVoidedAndLateBillsAreClaimedOnce(t *testing.T) {
	v := setupVAT(t)
	v.charge(t, "RESTAURANT", "100000", "r1")
	kept := v.bill(t, "INV-KEPT", "", "50000", "5000")
	gone := v.bill(t, "INV-GONE", "", "20000", "2000")
	_, err := v.Payables.VoidBill(v.admin, v.propID, gone.ID, payables.VoidInput{Reason: "duplicate", Approval: v.approval()})
	must(t, err)
	late := v.bill(t, "INV-LATE", "", "10000", "1000") // entered in September, claimed by it ...
	v.closeDay(t)
	sep := v.worksheet(t, "2026-09-01")
	if len(sep.Input) != 2 {
		t.Fatalf("the voided bill is not claimed: %+v", sep.Input)
	}
	eq(t, "September input", sep.InputClaimed, "6000")
	ret := v.fileMonth(t, "2026-09-01", "sep")
	if len(ret.Input) != 2 {
		t.Fatalf("claims: %+v", ret.Input)
	}
	// ... a bill dated in September but entered in October goes to October
	v.bill(t, "INV-LATER", "2026-09-30", "10000", "1000")
	// a bill voided after it was claimed is taken back in October
	_, err = v.Payables.VoidBill(v.admin, v.propID, kept.ID, payables.VoidInput{Reason: "returned", Approval: v.approval()})
	must(t, err)
	_ = late
	oct := v.worksheet(t, "2026-10-01")
	if len(oct.Input) != 2 {
		t.Fatalf("October claims the late bill and takes back the voided one: %+v", oct.Input)
	}
	var reversals int
	for _, c := range oct.Input {
		if c.Reversal {
			reversals++
			eq(t, "the reversal takes back what was claimed", c.Amount, "-5000")
		} else {
			eq(t, "the late bill", c.Amount, "1000")
		}
	}
	if reversals != 1 {
		t.Fatalf("%d reversals", reversals)
	}
	eq(t, "October input", oct.InputClaimed, "-4000")
	// the credit carried from September (6,000 - 6,000... = 0 after offsetting 10,000 of output) is unchanged by October's worksheet
	figures(t, "October worksheet", oct.VATOffset, "-4000", "0", "-4000", "4000", "0")
}

// The opening credit is the starting credit; only before the first return, once.
func TestAnOpeningCreditStartsTheChain(t *testing.T) {
	v := setupVAT(t)
	_, err := v.Tax.SetOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec("7000")})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = v.Tax.SetOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec("-1"), Approval: v.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	pr, err := v.Tax.SetOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec("7000"), Approval: v.approval()})
	must(t, err)
	eq(t, "opening credit", pr.OpeningCredit, "7000")
	eq(t, "input VAT holds it", v.balance(t, "1425"), "7000")
	eq(t, "opening balance equity", v.balance(t, "3900"), "-7000")
	_, err = v.Tax.SetOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec("1"), Approval: v.approval()})
	wantCode(t, err, "TAX_OPENING_CREDIT_EXISTS")
	// a credit that is voided and set again
	pr, err = v.Tax.VoidOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.VoidInput{Reason: "wrong amount", Approval: v.approval()})
	must(t, err)
	eq(t, "no credit", pr.OpeningCredit, "0")
	eq(t, "reversed", v.balance(t, "1425"), "0")
	_, err = v.Tax.VoidOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.VoidInput{Reason: "again", Approval: v.approval()})
	wantCode(t, err, "TAX_OPENING_CREDIT_NOT_FOUND")
	_, err = v.Tax.SetOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec("7000"), Approval: v.approval()})
	must(t, err)

	v.charge(t, "RESTAURANT", "100000", "r1") // output 10,000
	v.closeDay(t)
	ws := v.worksheet(t, "2026-09-01")
	figures(t, "September worksheet", ws.VATOffset, "0", "7000", "7000", "3000", "0")
	ret := v.fileMonth(t, "2026-09-01", "sep")
	figures(t, "September return", ret.VATOffset, "0", "7000", "7000", "3000", "0")
	eq(t, "the credit is used", v.balance(t, "1425"), "0")
	// the credit is not set once a return is filed
	_, err = v.Tax.VoidOpeningCredit(v.admin, v.propID, v.profile.ID, taxfiling.VoidInput{Reason: "late", Approval: v.approval()})
	wantCode(t, err, "TAX_OPENING_CREDIT_LOCKED")
	// the flag cannot be taken off a profile whose returns have claimed
	_, err = v.Tax.UpdateProfile(v.admin, v.propID, v.profile.ID, taxfiling.ProfilePatch{ClaimsInputVAT: ptr(false)})
	must(t, err) // no input VAT was claimed: it can
}

func TestOnlyOneProfileClaimsAndNotWhileClaimed(t *testing.T) {
	v := setupVAT(t)
	other, err := v.Billing.CreateTax(v.admin, v.propID, billingconfig.TaxInput{Code: "PPN2", Name: "VAT 2", Rate: "5", TaxKind: "VAT", IsActive: true})
	must(t, err)
	_, err = v.Tax.CreateProfile(v.admin, v.propID, taxfiling.ProfileInput{TaxID: other.ID, Authority: "KPP", ClaimsInputVAT: ptr(true)})
	wantCode(t, err, "TAX_CLAIMS_TAKEN")
	v.charge(t, "RESTAURANT", "100000", "r1")
	v.bill(t, "INV-A", "", "50000", "5000")
	v.closeDay(t)
	v.fileMonth(t, "2026-09-01", "sep")
	_, err = v.Tax.UpdateProfile(v.admin, v.propID, v.profile.ID, taxfiling.ProfilePatch{ClaimsInputVAT: ptr(false)})
	wantCode(t, err, "TAX_CLAIMS_IN_USE")
}

// The database holds the chain without the service.
func TestTheChainAndTheClaimsAreEnforcedByTheDatabase(t *testing.T) {
	v := setupVAT(t)
	v.charge(t, "RESTAURANT", "100000", "r1")
	b := v.bill(t, "INV-A", "", "50000", "5000")
	v.closeDay(t)
	ret := v.fileMonth(t, "2026-09-01", "sep")
	insert := `INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on, input_claimed, credit_brought_forward)
		VALUES ($1, $2, 'TXR9999', $3, '2026-10-01', '2026-10-31', '2026-11-15', 0, 0, '2026-11-02', 0, $4)`
	// the credit brought forward is what the month before carried (0 after offsetting 10,000 with 5,000 ... no: 5,000 of input, 10,000 of output)
	if _, err := v.Pool.Exec(context.Background(), insert, v.tenantID, v.propID, v.vat.ID, 123); err == nil {
		t.Error("a return that does not start with the credit carried was accepted")
	}
	// a bill line is claimed once among the live claims
	if _, err := v.Pool.Exec(context.Background(), `INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, bill_id, line_no, amount) VALUES ($1, $2, $3, $4, 1, 5000)`,
		v.tenantID, v.propID, ret.ID, b.ID); err == nil {
		t.Error("a bill line was claimed twice")
	}
	if _, err := v.Pool.Exec(context.Background(), `DELETE FROM tax_return_input_claims`); err == nil {
		t.Error("claims were deleted")
	}
	if _, err := v.Pool.Exec(context.Background(), `UPDATE tax_return_input_claims SET amount = 1`); err == nil {
		t.Error("a claim was changed")
	}
	if _, err := v.Pool.Exec(context.Background(), `UPDATE tax_returns SET input_claimed = 1`); err == nil {
		t.Error("a return was changed")
	}
}
