package bankrec_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/taxfiling"
)

// settlementJournal is the lines of the journal of the newest settlement, by account code: debit less credit.
func (g *guestFx) settlementJournal(t *testing.T) map[string]decimal.Decimal {
	t.Helper()
	rows, err := g.Pool.Query(context.Background(), `SELECT a.code, sum(l.debit - l.credit)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.journal_id = (SELECT journal_id FROM card_settlements ORDER BY id DESC LIMIT 1) GROUP BY a.code`)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var code, amount string
		must(t, rows.Scan(&code, &amount))
		out[code] = decimal.RequireFromString(amount)
	}
	must(t, rows.Err())
	return out
}

func (g *guestFx) lastSettlement(t *testing.T) bankrec.SettlementRow {
	t.Helper()
	list, err := g.BankRec.Settlements(g.admin, g.propID, 0, 50)
	must(t, err)
	if len(list) == 0 {
		t.Fatal("no settlement")
	}
	return list[0]
}

// settleOne settles every payment line of the clearing account against a statement line of the amount the bank paid.
func (g *guestFx) settleOne(t *testing.T, paid string, vat *string) error {
	t.Helper()
	st := g.importStatement(t, "0", paid, "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,"+paid+"\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	ids := make([]int64, 0, len(exp.Lines))
	for _, l := range exp.Lines {
		ids = append(ids, l.JournalLineID)
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"], VATAmount: vat})
	return err
}

func ptrStr(s string) *string { return &s }

func (g *guestFx) pkpFrom(t *testing.T, date, treatment string) {
	t.Helper()
	_, err := g.Tax.ChangeSettings(g.admin, g.propID, taxfiling.SettingsInput{EffectiveFrom: d(date), IsPKP: true, NPWP: "01.234.567.8-901.000", InputVATTreatment: treatment})
	must(t, err)
}

func TestASettlementSplitsWhatTheBankKeptIntoTheMDRAndTheVAT(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1") // MDR 20,000, VAT 2,200
	g.closeDay(t)
	// not PKP: the VAT is part of the cost
	if err := g.settleOne(t, "977800", nil); err != nil {
		t.Fatal(err)
	}
	row := g.lastSettlement(t)
	eq(t, "the deduction", row.Fee, "22200")
	eq(t, "MDR", row.MDRAmount, "20000")
	eq(t, "VAT", row.VATAmount, "2200")
	eq(t, "proposed VAT", row.ProposedVAT, "2200")
	if row.VATTreatment == nil || *row.VATTreatment != "EXPENSE" || row.MDRRate == nil || *row.MDRRate != "2" || row.VATRate == nil || *row.VATRate != "11" {
		t.Fatalf("frozen: %+v", row)
	}
	eq(t, "expected MDR", *row.ExpectedMDR, "20000")
	eq(t, "expected VAT", *row.ExpectedVAT, "2200")
	eq(t, "MDR variance", *row.MDRVariance, "0")
	eq(t, "VAT variance", *row.VATVariance, "0")
	// fee = MDR + VAT, and it is what the bank kept
	if g.Count(t, `SELECT count(*) FROM card_settlements WHERE fee = mdr_amount + vat_amount AND fee = gross - net`) != 1 {
		t.Fatal("fee = MDR + VAT = gross - net")
	}
	j := g.settlementJournal(t)
	eq(t, "bank", j["1130"], "977800")
	eq(t, "the commission carries the VAT as a cost", j["6130"], "22200")
	if _, ok := j["1425"]; ok {
		t.Fatalf("no input VAT line for an expense: %v", j)
	}
	eq(t, "clearing", j["1150"], "-1000000")
	g.requireBalanced(t)
}

func TestACreditableVATGoesToInputVATAndADeferredOneToo(t *testing.T) {
	for _, treatment := range []string{"CREDITABLE", "DEFERRED"} {
		g := setupGuests(t)
		g.pkpFrom(t, "2026-10-01", treatment)
		g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
		g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
		g.closeDay(t)
		if err := g.settleOne(t, "977800", nil); err != nil {
			t.Fatal(err)
		}
		row := g.lastSettlement(t)
		if row.VATTreatment == nil || *row.VATTreatment != treatment {
			t.Fatalf("%s: %+v", treatment, row)
		}
		j := g.settlementJournal(t)
		eq(t, treatment+": input VAT", j["1425"], "2200")
		eq(t, treatment+": the commission is the MDR", j["6130"], "20000")
		eq(t, treatment+": bank", j["1130"], "977800")
		g.requireBalanced(t)
	}
}

func TestTheUserGivesTheFinalVATAndTheMDRIsWhatIsLeft(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-10-01", "CREDITABLE")
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.pay(t, "k2", "CARD", "500000", "AUTH-2")
	g.closeDay(t)
	// the acquirer's slip shows VAT of 3,300 on the commission of 30,000 instead of the 3,300 proposed... it kept 33,000 in all
	st := g.importStatement(t, "0", "1467000", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,1467000\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	ids := []int64{exp.Lines[0].JournalLineID, exp.Lines[1].JournalLineID}
	pv, err := g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids})
	must(t, err)
	eq(t, "deduction", pv.Deduction, "33000")
	eq(t, "proposed VAT = 33,000 x 11 / 111", pv.ProposedVAT, "3270") // 3,270.27 rounded
	eq(t, "proposed MDR", pv.ProposedMDR, "29730")
	if pv.VATTreatment != "CREDITABLE" || pv.InputVATAccount == nil || pv.InputVATAccount.Code != "1425" || pv.VATRate == nil || *pv.VATRate != "11" || pv.MDRRate == nil || *pv.MDRRate != "2" {
		t.Fatalf("preview: %+v", pv)
	}
	if pv.ExpectedMDR == nil || !pv.ExpectedMDR.Equal(decimal.NewFromInt(30000)) || !pv.ExpectedVAT.Equal(decimal.NewFromInt(3300)) {
		t.Fatalf("expected: %+v", pv)
	}
	// the preview writes nothing
	if g.Count(t, `SELECT count(*) FROM card_settlements`) != 0 {
		t.Fatal("a preview posts nothing")
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"], VATAmount: ptrStr("3300")})
	must(t, err)
	row := g.lastSettlement(t)
	eq(t, "the user's VAT is final", row.VATAmount, "3300")
	eq(t, "the MDR is the rest", row.MDRAmount, "29700")
	eq(t, "the proposal is kept", row.ProposedVAT, "3270")
	eq(t, "MDR variance", *row.MDRVariance, "-300")
	eq(t, "VAT variance", *row.VATVariance, "0")
	j := g.settlementJournal(t)
	eq(t, "input VAT", j["1425"], "3300")
	eq(t, "commission", j["6130"], "29700")
	g.requireBalanced(t)
}

func TestAVATOfZeroPutsEverythingOnTheCommission(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-10-01", "CREDITABLE")
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	if err := g.settleOne(t, "977800", ptrStr("0")); err != nil {
		t.Fatal(err)
	}
	row := g.lastSettlement(t)
	eq(t, "VAT", row.VATAmount, "0")
	eq(t, "MDR", row.MDRAmount, "22200")
	if row.VATTreatment != nil {
		t.Fatalf("no VAT, no treatment: %v", *row.VATTreatment)
	}
	eq(t, "proposed VAT is still the proposal", row.ProposedVAT, "2200")
	j := g.settlementJournal(t)
	if _, ok := j["1425"]; ok {
		t.Fatalf("no input VAT: %v", j)
	}
	eq(t, "commission", j["6130"], "22200")
}

func TestADeductionThatIsAllVATNeedsNoCommissionAccount(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-10-01", "CREDITABLE")
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "997800", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,997800\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{exp.Lines[0].JournalLineID}, VATAmount: ptrStr("2200")})
	must(t, err)
	j := g.settlementJournal(t)
	eq(t, "input VAT", j["1425"], "2200")
	if _, ok := j["6130"]; ok {
		t.Fatalf("no commission line: %v", j)
	}
}

func TestTheVATOfASettlementIsChecked(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "977800", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,977800\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	ids := []int64{exp.Lines[0].JournalLineID}
	for name, vat := range map[string]string{"above the deduction": "22201", "negative": "-1", "too precise": "10.5", "not a number": "x"} {
		_, err := g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"], VATAmount: ptrStr(vat)})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	if g.Count(t, `SELECT count(*) FROM card_settlements`) != 0 {
		t.Fatal("nothing was settled")
	}
}

func TestDifferentVATRatesAreSplitInProportionToWhatWasExpected(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 0, "2026-09-01")
	g.pay(t, "k1", "CARD", "400000", "AUTH-1") // MDR 8,000, VAT 880
	g.closeDay(t)
	g.ruleVAT(t, "CARD", "2", "0", 0, "2026-10-01")
	g.pay(t, "k2", "CARD", "600000", "AUTH-2") // MDR 12,000, no VAT
	g.closeDay(t)
	st := g.importStatement(t, "0", "975000", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,975000\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	if len(exp.Lines) != 2 {
		t.Fatalf("lines: %+v", exp.Lines)
	}
	ids := []int64{exp.Lines[0].JournalLineID, exp.Lines[1].JournalLineID}
	pv, err := g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids})
	must(t, err)
	eq(t, "deduction", pv.Deduction, "25000")
	// 25,000 x 880 / (20,000 + 880) = 1,053.64 -> 1,054
	eq(t, "proportional VAT", pv.ProposedVAT, "1054")
	if pv.VATRate != nil {
		t.Fatalf("the rates differ: no single VAT rate: %v", *pv.VATRate)
	}
	if pv.MDRRate == nil || *pv.MDRRate != "2" {
		t.Fatalf("the MDR rate is shared: %+v", pv.MDRRate)
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	must(t, err)
	row := g.lastSettlement(t)
	eq(t, "VAT", row.VATAmount, "1054")
	eq(t, "MDR", row.MDRAmount, "23946")
	if row.VATRate != nil {
		t.Fatalf("no single rate to freeze: %v", *row.VATRate)
	}
}

func TestAPaymentWithoutAVATRateExpectsNoVATAndIsCounted(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 0, "2026-09-01")
	g.pay(t, "k1", "CARD", "400000", "AUTH-1") // MDR 8,000, VAT 880
	g.pay(t, "k2", "CARD", "600000", "AUTH-2")
	must(t, g.Exec(t, `ALTER TABLE payments DISABLE TRIGGER USER; UPDATE payments SET mdr_vat_rate = NULL, mdr_vat = NULL WHERE reference_number = 'AUTH-2'; ALTER TABLE payments ENABLE TRIGGER USER`))
	g.closeDay(t)
	st := g.importStatement(t, "0", "980000", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,980000\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	ids := []int64{exp.Lines[0].JournalLineID, exp.Lines[1].JournalLineID}
	pv, err := g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids})
	must(t, err)
	if pv.WithoutVATRate != 1 || pv.WithoutRate != 0 {
		t.Fatalf("preview: %+v", pv)
	}
	eq(t, "expected VAT: only the first payment", *pv.ExpectedVAT, "880")
	// D = 20,000: 20,000 x 880 / 20,880 = 842.91 -> 843
	eq(t, "proportional VAT", pv.ProposedVAT, "843")
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	must(t, err)
	row := g.lastSettlement(t)
	if row.PaymentsWithoutVATRate != 1 {
		t.Fatalf("the settlement counts it: %+v", row)
	}
}

func TestASavedSettlementNeverChanges(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	if err := g.settleOne(t, "977800", nil); err != nil {
		t.Fatal(err)
	}
	before := g.lastSettlement(t)
	// the PKP status, the rule and the VAT rate change afterwards
	g.pkpFrom(t, "2026-10-02", "CREDITABLE")
	g.ruleVAT(t, "CARD", "3", "12", 1, "2026-10-01")
	after := g.lastSettlement(t)
	if *after.VATTreatment != *before.VATTreatment || !after.VATAmount.Equal(before.VATAmount) || !after.MDRAmount.Equal(before.MDRAmount) || *after.VATRate != *before.VATRate || *after.MDRRate != *before.MDRRate {
		t.Fatalf("a settlement moved: %+v and %+v", before, after)
	}
	if err := g.Exec(t, `UPDATE card_settlements SET vat_amount = 0, vat_treatment = NULL`); err == nil {
		t.Error("a settlement was changed")
	}
	if err := g.Exec(t, `DELETE FROM card_settlements`); err == nil {
		t.Error("a settlement was deleted")
	}
}

func TestThePreviewOfASettlementIsForWhoCanSeeTheBank(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "2977800", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,977800\n2026-10-01,Too much,SETT-2,2000000\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	in := bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{exp.Lines[0].JournalLineID}}
	viewer := g.User(t, g.tenantID, g.propID, auth.PermBankView)
	if _, err := g.BankRec.SettlementPreview(viewer, g.propID, st.ID, line.ID, in); err != nil {
		t.Fatal(err)
	}
	_, err = g.BankRec.Settle(viewer, g.propID, st.ID, line.ID, in)
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := g.User(t, g.tenantID, g.propID)
	_, err = g.BankRec.SettlementPreview(nobody, g.propID, st.ID, line.ID, in)
	wantCode(t, err, "PERMISSION_DENIED")
	bad := in
	bad.AccountKey = "CASH"
	_, err = g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, line.ID, bad)
	wantCode(t, err, "VALIDATION_FAILED")
	bad = in
	bad.JournalLineIDs = nil
	_, err = g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, line.ID, bad)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, 999999, in)
	wantCode(t, err, "STATEMENT_LINE_NOT_FOUND")
	// the bank paid more than the payments: no preview
	over := lineOf(t, st, "Too much")
	_, err = g.BankRec.SettlementPreview(g.admin, g.propID, st.ID, over.ID, in)
	wantCode(t, err, "VALIDATION_FAILED")
}

func (g *guestFx) requireBalanced(t *testing.T) {
	t.Helper()
	var dr, cr string
	must(t, g.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM gl_journal_lines WHERE property_id = $1`, g.propID).Scan(&dr, &cr))
	if dr != cr {
		t.Fatalf("the books do not balance: %s %s", dr, cr)
	}
}
