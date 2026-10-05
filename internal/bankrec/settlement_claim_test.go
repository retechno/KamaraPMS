package bankrec_test

import (
	"testing"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/taxfiling"
)

// vatReturn makes the VAT tax of the property and the filing profile that claims input VAT.
func (g *guestFx) vatReturn(t *testing.T, claims bool) billingconfig.Tax {
	t.Helper()
	vat, err := g.Billing.CreateTax(g.admin, g.propID, billingconfig.TaxInput{Code: "PPN", Name: "VAT", Rate: "10", TaxKind: "VAT", GLAccountCode: "2420", IsActive: true})
	must(t, err)
	_, err = g.Tax.CreateProfile(g.admin, g.propID, taxfiling.ProfileInput{TaxID: vat.ID, Authority: "KPP Pratama", ClaimsInputVAT: &claims})
	must(t, err)
	return vat
}

// settleOnDay pays 1,000,000 by card on the first day, closes it, and settles it from a statement line of 30 Sep (the bank kept 22,200 of which 2,200 VAT).
func (g *guestFx) settleCardOn30Sep(t *testing.T) {
	t.Helper()
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "977800", "date,description,reference,amount\n2026-09-30,Card settlement,SETT-1,977800\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{exp.Lines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	must(t, err)
}

func TestTheCreditableVATOfASettlementIsClaimedOnTheReturn(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, true)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	if row.VATTreatment == nil || *row.VATTreatment != "CREDITABLE" {
		t.Fatalf("settlement: %+v", row)
	}

	w, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-09-01"))
	must(t, err)
	if len(w.Input) != 1 || w.Input[0].Source != taxfiling.ClaimSettlement || w.Input[0].Reversal || w.Input[0].SettlementID != row.ID {
		t.Fatalf("the settlement is claimed: %+v", w.Input)
	}
	eq(t, "the claim", w.Input[0].Amount, "2200")
	eq(t, "input VAT claimed", w.InputClaimed, "2200")
	if w.Input[0].BillNumber == "" || w.Input[0].BillDate.String() != "2026-09-30" || w.Input[0].SupplierName == "" {
		t.Fatalf("it names the journal, the date and the bank: %+v", w.Input[0])
	}

	ret, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep")
	must(t, err)
	if len(ret.Input) != 1 || ret.Input[0].Source != taxfiling.ClaimSettlement {
		t.Fatalf("the claim is frozen on the return: %+v", ret.Input)
	}
	got, err := g.Tax.GetReturn(g.admin, g.propID, ret.ID)
	must(t, err)
	if len(got.Input) != 1 || got.Input[0].Source != taxfiling.ClaimSettlement || got.Input[0].SettlementID != row.ID || !got.Input[0].Amount.Equal(w.Input[0].Amount) {
		t.Fatalf("read back: %+v", got.Input)
	}
	// claimed once: the next month does not claim it again
	again, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-10-01"))
	must(t, err)
	if len(again.Input) != 0 {
		t.Fatalf("a settlement is claimed once: %+v", again.Input)
	}
	// voiding the return releases the claim: the month, filed again, claims the settlement again
	_, err = g.Tax.VoidReturn(g.admin, g.propID, ret.ID, taxfiling.VoidInput{Reason: "refile", Approval: g.approval()})
	must(t, err)
	released, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-09-01"))
	must(t, err)
	if len(released.Input) != 1 || released.Input[0].Source != taxfiling.ClaimSettlement {
		t.Fatalf("after the void it is claimable again: %+v", released.Input)
	}
	ret2, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep2")
	must(t, err)
	if len(ret2.Input) != 1 {
		t.Fatalf("refiled: %+v", ret2.Input)
	}
	// the settlement itself did not move
	after := g.lastSettlement(t)
	if *after.VATTreatment != "CREDITABLE" || !after.VATAmount.Equal(row.VATAmount) {
		t.Fatalf("a settlement is final: %+v", after)
	}
}

func TestOnlyACreditableSettlementOfAClaimingProfileIsClaimed(t *testing.T) {
	// an expense (not PKP) and a deferred VAT are not claimed
	for name, setup := range map[string]func(g *guestFx){
		"expense":  func(g *guestFx) {},
		"deferred": func(g *guestFx) { g.pkpFrom(t, "2026-09-30", "DEFERRED") },
	} {
		g := setupGuests(t)
		setup(g)
		vat := g.vatReturn(t, true)
		g.settleCardOn30Sep(t)
		w, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-09-01"))
		must(t, err)
		if len(w.Input) != 0 {
			t.Errorf("%s: no claim: %+v", name, w.Input)
		}
	}
	// a creditable settlement is claimed only by the profile that claims input VAT
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, false)
	g.settleCardOn30Sep(t)
	w, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-09-01"))
	must(t, err)
	if len(w.Input) != 0 {
		t.Fatalf("a profile that does not claim has no input: %+v", w.Input)
	}
}

func TestTheDatabaseKeepsASettlementClaimedOnce(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, true)
	g.settleCardOn30Sep(t)
	ret, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep")
	must(t, err)
	row := g.lastSettlement(t)
	// a second live claim of the same settlement, a claim with two sources and a reversal of a settlement claim are all refused
	if err := g.Exec(t, `INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, settlement_id, amount) VALUES ($1, $2, $3, $4, 5)`, g.tenantID, g.propID, ret.ID, row.ID); err == nil {
		t.Error("a settlement is claimed once")
	}
	if err := g.Exec(t, `INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, bill_id, line_no, settlement_id, amount) VALUES ($1, $2, $3, 1, 1, $4, 5)`, g.tenantID, g.propID, ret.ID, row.ID); err == nil {
		t.Error("a claim names one source")
	}
	if err := g.Exec(t, `INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, settlement_id, amount) VALUES ($1, $2, $3, $4, -5)`, g.tenantID, g.propID, ret.ID, row.ID); err == nil {
		t.Error("a settlement claim is positive")
	}
}
