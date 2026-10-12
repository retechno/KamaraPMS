package bankrec_test

import (
	"context"
	"testing"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// QRIS is received into an account of its own (1155), has fee rules like a card, and is settled by the acquirer net of its MDR and the VAT on it.

func (g *guestFx) qrisLines(t *testing.T, statementID int64) []bankrec.UnclearedLine {
	t.Helper()
	lines, err := g.BankRec.SettlementLines(g.admin, g.propID, statementID, "QRIS")
	must(t, err)
	return lines
}

func TestAQRISPaymentIsReceivedIntoItsOwnClearingAccount(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "q", "QRIS", "1000000", "QR-1")
	g.pay(t, "c", "CARD", "500000", "AUTH-1")
	g.pay(t, "w", "OTHER", "50000", "WALLET-1")
	g.closeDay(t)
	// each method has its own clearing account: what is waiting for the acquirer of QRIS is not mixed with the cards and the wallets
	eq(t, "QRIS clearing", g.balanceOf(t, "1155"), "1000000")
	eq(t, "card clearing", g.balanceOf(t, "1150"), "500000")
	eq(t, "e-wallet and other clearing", g.balanceOf(t, "1160"), "50000")
	// the deposit is the guest's, so the other side is the advance deposits
	eq(t, "advance deposits", g.balanceOf(t, "2310"), "-1550000")
	g.requireBalanced(t)
}

func TestAFeeRuleCanBeForQRISAndItsPaymentsKeepTheSnapshot(t *testing.T) {
	g := setupGuests(t)
	rule := g.ruleVAT(t, "QRIS", "0.7", "11", 1, "2026-09-01")
	if rule.PaymentMethod != "QRIS" || rule.MDRRate != "0.7" || rule.VATRate != "11" {
		t.Fatalf("rule: %+v", rule)
	}
	// a QRIS payment before there was a rule has none; a card payment is not touched by the rule of QRIS
	card := g.pay(t, "c", "CARD", "400000", "AUTH-1")
	if card.Payment.MDRRate != nil {
		t.Fatalf("the rule of QRIS is not the rule of cards: %+v", card.Payment)
	}
	qr := g.pay(t, "q", "QRIS", "1000000", "QR-1")
	if qr.Payment.MDRRate == nil || *qr.Payment.MDRRate != "0.7" || qr.Payment.MDRFee == nil || *qr.Payment.MDRFee != "7000" ||
		qr.Payment.ExpectedSettlementDate == nil || qr.Payment.ExpectedSettlementDate.String() != "2026-10-01" {
		t.Fatalf("snapshot: %+v", qr.Payment)
	}
	// a method that is not paid out by an acquirer cannot have a rule
	_, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: "BANK_TRANSFER", MDRRate: "1", SettlementDays: 1, EffectiveFrom: d("2026-09-01")})
	wantCode(t, err, "VALIDATION_FAILED")
	// the rule is its own: a second one for the same day is refused, and it is audited
	_, err = g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: "QRIS", MDRRate: "0.3", SettlementDays: 1, EffectiveFrom: d("2026-09-01")})
	wantCode(t, err, "FEE_RULE_EXISTS")
	if g.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bank.card_fee_rule_added' AND new_data->>'payment_method' = 'QRIS'`) != 1 {
		t.Fatal("the rule is audited")
	}
}

func TestAQRISSettlementKeepsTheMDRAndTheVATOfWhatTheAcquirerKept(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "QRIS", "0.7", "11", 1, "2026-09-01")
	g.pay(t, "q1", "QRIS", "1000000", "QR-1") // MDR 7,000 and VAT 770
	g.pay(t, "q2", "QRIS", "500000", "QR-2")  // MDR 3,500 and VAT 385
	g.pay(t, "c", "CARD", "400000", "AUTH-1")
	g.closeDay(t)
	// the acquirer of QRIS pays 1,500,000 less 10,500 of MDR and 1,155 of VAT
	st := g.importStatement(t, "0", "1488345", "date,description,reference,amount\n2026-10-01,QRIS settlement,QRIS-77,1488345\n")
	line := lineOf(t, st, "QRIS settlement")

	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "QRIS")
	must(t, err)
	if len(exp.Lines) != 2 {
		t.Fatalf("the QRIS payments wait for their settlement, and only they: %+v", exp.Lines)
	}
	eq(t, "gross", exp.Gross, "1500000")
	eq(t, "expected MDR", exp.ExpectedMDR, "10500")
	eq(t, "expected VAT", exp.ExpectedVAT, "1155")
	eq(t, "expected net", exp.ExpectedNet, "1488345")
	cards, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	if len(cards.Lines) != 1 {
		t.Fatalf("the card payment is not among the QRIS ones: %+v", cards.Lines)
	}
	if lines := g.qrisLines(t, st.ID); len(lines) != 2 {
		t.Fatalf("lines to settle: %+v", lines)
	}
	ids := []int64{exp.Lines[0].JournalLineID, exp.Lines[1].JournalLineID}

	// the settlement clears the QRIS account and nothing else: the card key does not take QRIS lines
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "VALIDATION_FAILED")
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'BANK'`) != 0 {
		t.Fatal("a refused settlement posts nothing")
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "QRIS", JournalLineIDs: ids, FeeAccountID: g.acc["6130"], Description: "QRIS settlement 1 Oct"})
	must(t, err)

	row := g.lastSettlement(t)
	if row.AccountKey != "QRIS" {
		t.Fatalf("the settlement clears QRIS: %+v", row)
	}
	eq(t, "the deduction", row.Fee, "11655")
	eq(t, "MDR", row.MDRAmount, "10500")
	eq(t, "VAT", row.VATAmount, "1155")
	eq(t, "expected MDR", *row.ExpectedMDR, "10500")
	eq(t, "MDR variance", *row.MDRVariance, "0")
	eq(t, "VAT variance", *row.VATVariance, "0")
	j := g.settlementJournal(t)
	eq(t, "bank", j["1130"], "1488345")
	eq(t, "the commission carries the MDR and, as the property is not PKP, the VAT", j["6130"], "11655")
	eq(t, "QRIS clearing", j["1155"], "-1500000")
	eq(t, "QRIS clearing is settled", g.balanceOf(t, "1155"), "0")
	eq(t, "the card clearing is untouched", g.balanceOf(t, "1150"), "400000")
	g.requireBalanced(t)
	// each payment is settled once
	rest, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "QRIS")
	must(t, err)
	if len(rest.Lines) != 0 {
		t.Fatalf("settled: %+v", rest.Lines)
	}
	final, err := g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	if !final.Summary.CanReconcile {
		t.Fatalf("the statement is complete: %+v", final.Summary)
	}
}

func TestAnUnknownClearingKeyIsStillRefused(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "q", "QRIS", "100000", "QR-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "99300", "date,description,reference,amount\n2026-10-01,Settlement,S-1,99300\n")
	_, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "PAYPAL")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = g.BankRec.ExpectedSettlements(g.admin, g.propID, "CASH")
	wantCode(t, err, "VALIDATION_FAILED")
	if lines := g.qrisLines(t, st.ID); len(lines) != 1 {
		t.Fatalf("QRIS is a key: %+v", lines)
	}
}

func TestQRISRulesAndSettlementsBelongToTheirTenantAndNeedThePermissions(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "QRIS", "0.7", "11", 1, "2026-09-01")
	g.pay(t, "q", "QRIS", "100000", "QR-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "99230", "date,description,reference,amount\n2026-10-01,QRIS settlement,S-1,99230\n")
	line := lineOf(t, st, "QRIS settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "QRIS")
	must(t, err)
	ids := []int64{exp.Lines[0].JournalLineID}

	// another tenant sees neither the rules, the settlements nor the lines of this property
	g.Clock.Set(roomstest.T0)
	other := g.Tenant(t, "XYZ")
	g.Property(t, other.ID, "SG")
	stranger := roomstest.Admin(other.ID)
	_, err = g.BankRec.CardFeeRules(stranger, g.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = g.BankRec.ExpectedSettlements(stranger, g.propID, "QRIS")
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = g.BankRec.Settle(stranger, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "QRIS", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "PROPERTY_NOT_FOUND")

	// whoever may only look cannot settle or add a rule
	viewer := g.User(t, g.tenantID, g.propID, auth.PermBankView)
	if _, err := g.BankRec.ExpectedSettlements(viewer, g.propID, "QRIS"); err != nil {
		t.Fatal(err)
	}
	_, err = g.BankRec.Settle(viewer, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "QRIS", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = g.BankRec.CreateCardFeeRule(viewer, g.propID, bankrec.FeeRuleInput{PaymentMethod: "QRIS", MDRRate: "0.3", SettlementDays: 1, EffectiveFrom: d("2026-11-01")})
	wantCode(t, err, "PERMISSION_DENIED")
	var n int
	must(t, g.Pool.QueryRow(context.Background(), `SELECT count(*) FROM card_settlements WHERE account_key = 'QRIS'`).Scan(&n))
	if n != 0 {
		t.Fatalf("nothing was settled: %d", n)
	}
}
