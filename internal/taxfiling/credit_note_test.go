package taxfiling_test

import (
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/taxfiling"
)

// creditNote transfers part of the folio of the guest in house to a company and makes a credit note of 100,000 (and the tax on it) against that transfer.
func (f *fx) creditNote(t *testing.T, key string) cityledger.Adjustment {
	t.Helper()
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "ACME Ltd", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	tr, err := f.Folios.Transfer(f.admin, f.propID, f.folioID, "tr-"+key, folios.TransferInput{CompanyID: co.ID, Amount: "550000"})
	must(t, err)
	n, err := f.CityLedger.CreateCreditNote(f.admin, f.propID, key, cityledger.CreditNoteInput{
		PaymentID: &tr.Payment.ID, Reason: "charge corrected", Approval: f.approval(),
		Lines: []cityledger.CreditNoteLineInput{{Description: "Restaurant corrected", AccountID: f.acc["4160"], NetAmount: "100000", TaxID: &f.pb1.ID}},
	})
	must(t, err)
	return n
}

func creditLine(t *testing.T, lines []taxfiling.WorksheetLine) taxfiling.WorksheetLine {
	t.Helper()
	for _, l := range lines {
		if l.ChargeCode == taxfiling.CreditNotesCode {
			return l
		}
	}
	t.Fatalf("no line for the credit notes: %+v", lines)
	return taxfiling.WorksheetLine{}
}

func TestACreditNoteTakesItsTaxOffTheReturnOfItsMonth(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.charge(t, "RESTAURANT", "1000000", "r1")
	f.creditNote(t, "cn1")
	f.closeDay(t) // the night of the guest is charged too: the tax collected is 200,000
	ws, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-09-01"))
	must(t, err)
	if !ws.Ready || len(ws.Lines) != 3 {
		t.Fatalf("worksheet: ready %v, lines %+v, blockers %v", ws.Ready, ws.Lines, ws.Blockers)
	}
	cl := creditLine(t, ws.Lines)
	eq(t, "credit note rate", cl.Rate, "10")
	eq(t, "credit note base", cl.Base, "-100000")
	eq(t, "credit note tax", cl.Tax, "-10000")
	eq(t, "tax of the month", ws.Tax, "190000")
	eq(t, "the books agree: the credit note debited the tax payable", ws.GLCollected, "190000")
	eq(t, "no difference", ws.Difference, "0")
	// the periods show the same
	pers, err := f.Tax.Periods(f.admin, f.propID, pr.TaxID)
	must(t, err)
	var sep taxfiling.Period
	for _, p := range pers {
		if p.PeriodStart == d("2026-09-01") {
			sep = p
		}
	}
	eq(t, "period tax", sep.Tax, "190000")
	// filed: the lines are frozen with the credit note on them
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "k")
	must(t, err)
	eq(t, "return tax", ret.Tax, "190000")
	eq(t, "to pay", ret.Outstanding, "190000")
	eq(t, "frozen credit note tax", creditLine(t, ret.Lines).Tax, "-10000")
	liab, err := f.Tax.Liability(f.admin, f.propID, nil)
	must(t, err)
	eq(t, "collected", liab.Taxes[0].Collected, "190000")
	eq(t, "owed", liab.Taxes[0].Owed, "190000")
	if len(liab.Accounts) != 1 || !liab.Accounts[0].Difference.IsZero() {
		t.Fatalf("the books agree with what is owed: %+v", liab.Accounts)
	}
}

func TestACreditNoteVoidedInTheSameMonthLeavesTheReturnAsItWas(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.charge(t, "RESTAURANT", "1000000", "r1")
	n := f.creditNote(t, "cn1")
	_, err := f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	f.closeDay(t)
	ws, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-09-01"))
	must(t, err)
	if !ws.Ready || len(ws.Lines) != 2 {
		t.Fatalf("a credit note made and voided in the month leaves no line: %+v", ws.Lines)
	}
	eq(t, "tax of the month", ws.Tax, "200000")
	eq(t, "no difference", ws.Difference, "0")
}

// A credit note voided in a later month gives its tax back to that month; the return of the month it was made in does not change.
func TestACreditNoteVoidedInALaterMonthGivesItsTaxBackThen(t *testing.T) {
	f := setup(t)
	pr := f.profile(t)
	f.charge(t, "RESTAURANT", "1000000", "r1")
	n := f.creditNote(t, "cn1")
	f.closeDay(t) // 30 Sep is over; the business date is 1 Oct
	ret, err := f.Tax.FileReturn(f.admin, f.propID, taxfiling.FileInput{TaxID: pr.TaxID, PeriodStart: d("2026-09-01")}, "sep")
	must(t, err)
	eq(t, "September", ret.Tax, "190000")
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	got, err := f.Tax.GetReturn(f.admin, f.propID, ret.ID)
	must(t, err)
	eq(t, "the return filed does not move", got.Tax, "190000")
	oct, err := f.Tax.Worksheet(f.admin, f.propID, pr.TaxID, d("2026-10-01"))
	must(t, err)
	cl := creditLine(t, oct.Lines)
	eq(t, "October gets the tax back", cl.Tax, "10000")
	eq(t, "and the base", cl.Base, "100000")
	if cl.Items != -1 {
		t.Fatalf("items: %d", cl.Items)
	}
	pers, err := f.Tax.Periods(f.admin, f.propID, pr.TaxID)
	must(t, err)
	for _, p := range pers {
		if p.PeriodStart == d("2026-10-01") {
			eq(t, "October period tax", p.Tax, "10000")
		}
	}
}
