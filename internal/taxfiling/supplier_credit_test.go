package taxfiling_test

import (
	"testing"

	"kamarapms/internal/payables"
	"kamarapms/internal/taxfiling"
)

func (v *vatFx) credit(t *testing.T, bill payables.Bill, number, date, amount, vat string) payables.CreditNote {
	t.Helper()
	if date == "" {
		day, err := v.Tenancy.CurrentBusinessDay(v.admin, v.propID)
		must(t, err)
		date = day.BusinessDate.String()
	}
	c, err := v.Payables.PostCreditNote(v.admin, v.propID, payables.CreditNoteInput{
		BillID: bill.ID, SupplierCreditNumber: number, CreditDate: d(date), Reason: "goods returned",
		Lines: []payables.CreditLineInput{{BillLineNo: 1, Amount: dec(amount), VATAmount: dec(vat)}},
	}, "")
	must(t, err)
	return c
}

// A credit note of a supplier takes back the input VAT of the bill it credits on the return of the month it is made in, like a bill claims it; voided after it was claimed, it gives the VAT back on the
// next return.
func TestACreditNoteOfASupplierTakesInputVATBackOnTheReturn(t *testing.T) {
	v := setupVAT(t)
	v.charge(t, "RESTAURANT", "100000", "r1") // output 10,000
	b := v.bill(t, "INV-A", "", "150000", "15000")
	c := v.credit(t, b, "CR-A", "", "40000", "4000")
	v.closeDay(t)

	sep := v.worksheet(t, "2026-09-01")
	if len(sep.Input) != 2 {
		t.Fatalf("the bill and the credit note: %+v", sep.Input)
	}
	var credits int
	for _, in := range sep.Input {
		if in.Source == taxfiling.ClaimCreditNote {
			credits++
			eq(t, "the credit note takes VAT back", in.Amount, "-4000")
			if in.BillNumber != c.Number || in.SupplierInvoiceNumber != "CR-A" || in.Reversal {
				t.Fatalf("claim: %+v", in)
			}
		}
	}
	if credits != 1 {
		t.Fatalf("%d credit note claims", credits)
	}
	eq(t, "September input", sep.InputClaimed, "11000")
	figures(t, "September worksheet", sep.VATOffset, "11000", "0", "10000", "0", "1000")

	ret := v.fileMonth(t, "2026-09-01", "sep")
	figures(t, "September return", ret.VATOffset, "11000", "0", "10000", "0", "1000")
	if len(ret.Input) != 2 {
		t.Fatalf("the claims are frozen on the return: %+v", ret.Input)
	}
	// the claims read back with their source
	got, err := v.Tax.GetReturn(v.admin, v.propID, ret.ID)
	must(t, err)
	var fromCredit int
	for _, in := range got.Input {
		if in.Source == taxfiling.ClaimCreditNote {
			fromCredit++
			eq(t, "frozen amount", in.Amount, "-4000")
		}
	}
	if fromCredit != 1 {
		t.Fatalf("claims read back: %+v", got.Input)
	}

	// a credit note made in October on the same bill takes its VAT off October's return
	v.closeThrough(t, "2026-10-01")
	c2 := v.credit(t, b, "CR-B", "2026-10-01", "10000", "1000")
	oct := v.worksheet(t, "2026-10-01")
	if len(oct.Input) != 1 || oct.Input[0].Source != taxfiling.ClaimCreditNote {
		t.Fatalf("October: %+v", oct.Input)
	}
	eq(t, "October input", oct.InputClaimed, "-1000")

	// the first credit note is voided after September claimed it: October gives the VAT back
	_, err = v.Payables.VoidCreditNote(v.admin, v.propID, c.ID, payables.VoidInput{Reason: "withdrawn", Approval: v.approval()})
	must(t, err)
	oct = v.worksheet(t, "2026-10-01")
	var gaveBack, tookBack int
	for _, in := range oct.Input {
		switch {
		case in.Source == taxfiling.ClaimCreditNote && in.Reversal:
			gaveBack++
			eq(t, "the reversal gives the VAT back", in.Amount, "4000")
		case in.Source == taxfiling.ClaimCreditNote:
			tookBack++
			eq(t, "the second credit note", in.Amount, "-1000")
		}
	}
	if gaveBack != 1 || tookBack != 1 {
		t.Fatalf("October: %+v", oct.Input)
	}
	eq(t, "October input", oct.InputClaimed, "3000")
	// a credit note voided before any return claimed it is not claimed at all
	_, err = v.Payables.VoidCreditNote(v.admin, v.propID, c2.ID, payables.VoidInput{Reason: "mistake", Approval: v.approval()})
	must(t, err)
	oct = v.worksheet(t, "2026-10-01")
	eq(t, "October input", oct.InputClaimed, "4000")
	if len(oct.Input) != 1 {
		t.Fatalf("only the reversal is left: %+v", oct.Input)
	}
}
