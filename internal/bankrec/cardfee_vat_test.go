package bankrec_test

import (
	"testing"

	"kamarapms/internal/bankrec"
)

func (g *guestFx) ruleVAT(t *testing.T, method, rate, vat string, days int, from string) bankrec.FeeRule {
	t.Helper()
	r, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: method, MDRRate: rate, VATRate: vat, SettlementDays: days, EffectiveFrom: d(from)})
	must(t, err)
	return r
}

func TestAFeeRuleTakesTheVATRateOfTheCommission(t *testing.T) {
	g := setupGuests(t)
	for name, vat := range map[string]string{"above 100": "101", "negative": "-1", "five decimals": "11.12345", "not a number": "x"} {
		_, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: "CARD", MDRRate: "2", VATRate: vat, SettlementDays: 1, EffectiveFrom: d("2026-09-01")})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	none := g.rule(t, "CARD", "2", 1, "2026-09-01") // no VAT rate given: 0
	if none.VATRate != "0" {
		t.Fatalf("the default VAT rate: %q", none.VATRate)
	}
	with := g.ruleVAT(t, "CARD", "2", "11", 1, "2026-10-01")
	if with.VATRate != "11" {
		t.Fatalf("the VAT rate: %q", with.VATRate)
	}
	list, err := g.BankRec.CardFeeRules(g.admin, g.propID)
	must(t, err)
	if len(list) != 2 || list[0].VATRate != "11" || list[1].VATRate != "0" {
		t.Fatalf("rules: %+v", list)
	}
	if err := g.Exec(t, `UPDATE card_fee_rules SET vat_rate = 5`); err == nil {
		t.Error("a rule was changed")
	}
}

func TestAPaymentKeepsTheVATItExpectsOnTheMDR(t *testing.T) {
	g := setupGuests(t)
	g.ruleVAT(t, "CARD", "2.5", "11", 2, "2026-09-01")
	card := g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	p := card.Payment
	if p.MDRFee == nil || *p.MDRFee != "10000" || p.MDRVATRate == nil || *p.MDRVATRate != "11" || p.MDRVAT == nil || *p.MDRVAT != "1100" {
		t.Fatalf("the VAT is expected on the MDR: %+v", p)
	}
	// the VAT is rounded from the rounded MDR: 2.5% of 333 is 8.325 -> 8; 11% of 8 is 0.88 -> 1
	odd := g.pay(t, "k2", "CARD", "333", "AUTH-2")
	if *odd.Payment.MDRFee != "8" || *odd.Payment.MDRVAT != "1" {
		t.Fatalf("rounded: fee %s, VAT %s", *odd.Payment.MDRFee, *odd.Payment.MDRVAT)
	}
	// a rule whose VAT rate is 0 keeps the rate 0 and VAT 0: that is not "without rate"
	g.ruleVAT(t, "OTHER", "0.7", "0", 1, "2026-09-01")
	wallet := g.pay(t, "k3", "OTHER", "100000", "QR")
	if wallet.Payment.MDRVATRate == nil || *wallet.Payment.MDRVATRate != "0" || *wallet.Payment.MDRVAT != "0" {
		t.Fatalf("a VAT rate of 0 is kept: %+v", wallet.Payment)
	}
	// cash has no snapshot at all
	cash := g.pay(t, "k4", "CASH", "1000", "")
	if cash.Payment.MDRVATRate != nil || cash.Payment.MDRVAT != nil {
		t.Fatalf("cash: %+v", cash.Payment)
	}
	// a new rule applies to the payments taken from its date; what was taken keeps its own
	g.closeDay(t)
	g.ruleVAT(t, "CARD", "2.5", "12", 2, "2026-10-01")
	next := g.pay(t, "k5", "CARD", "400000", "AUTH-3")
	if *next.Payment.MDRVATRate != "12" || *next.Payment.MDRVAT != "1200" {
		t.Fatalf("the new VAT rate: %+v", next.Payment)
	}
	got, err := g.Folios.GetPayment(g.admin, g.propID, card.Payment.ID)
	must(t, err)
	if *got.MDRVATRate != "11" || *got.MDRVAT != "1100" {
		t.Fatalf("the first payment did not move: %+v", got)
	}
	if err := g.Exec(t, `UPDATE payments SET mdr_vat = 1 WHERE id = $1`, card.Payment.ID); err == nil {
		t.Error("a snapshot was changed")
	}
	// the database keeps the snapshot whole: a VAT rate needs its VAT and an MDR
	if err := g.Exec(t, `INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, vat_rate, settlement_days, effective_from) VALUES ($1, $2, 'CARD', 1, 101, 1, '2026-12-01')`, g.tenantID, g.propID); err == nil {
		t.Error("a VAT rate above 100")
	}
}

func TestTheExpectedSettlementsCountTheVATAndMarkThePaymentsWithoutARate(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "k0", "CARD", "100000", "AUTH-0") // before any rule: no snapshot
	g.ruleVAT(t, "CARD", "2", "11", 0, "2026-09-01")
	g.pay(t, "k1", "CARD", "400000", "AUTH-1") // fee 8,000, VAT 880
	g.pay(t, "k2", "CARD", "600000", "AUTH-2") // fee 12,000, VAT 1,320
	// a payment that was taken when the VAT was not kept yet: an MDR snapshot with no VAT snapshot
	g.pay(t, "k3", "CARD", "200000", "AUTH-3")
	must(t, g.Exec(t, `ALTER TABLE payments DISABLE TRIGGER USER; UPDATE payments SET mdr_vat_rate = NULL, mdr_vat = NULL WHERE reference_number = 'AUTH-3'; ALTER TABLE payments ENABLE TRIGGER USER`))
	g.closeDay(t)

	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	eq(t, "gross", exp.Gross, "1300000")
	eq(t, "expected MDR", exp.ExpectedMDR, "24000")
	eq(t, "expected VAT: the payment without a rate counts 0", exp.ExpectedVAT, "2200")
	eq(t, "expected deduction", exp.ExpectedDeduction, "26200")
	eq(t, "expected net", exp.ExpectedNet, "1273800")
	if exp.NoRate != 1 || exp.WithoutVATRate != 1 || len(exp.Lines) != 4 {
		t.Fatalf("one without any rate, one with an MDR and no VAT rate: %+v", exp)
	}
	var withVAT, withoutVAT, noRate int
	for _, l := range exp.Lines {
		switch {
		case l.MDRRate == nil:
			noRate++
			if l.WithoutVATRate || !l.ExpectedVAT.IsZero() {
				t.Errorf("a payment with no snapshot at all is not marked as without a VAT rate: %+v", l)
			}
		case l.WithoutVATRate:
			withoutVAT++
			if l.VATRate != nil || !l.ExpectedVAT.IsZero() {
				t.Errorf("without a VAT rate: expected VAT 0: %+v", l)
			}
			eq(t, "its expected net is only the MDR off", l.ExpectedNet, "196000")
		default:
			withVAT++
			if l.VATRate == nil || *l.VATRate != "11" {
				t.Errorf("VAT rate: %+v", l)
			}
		}
	}
	if withVAT != 2 || withoutVAT != 1 || noRate != 1 {
		t.Fatalf("lines: %d with the VAT, %d without a VAT rate, %d without any rate", withVAT, withoutVAT, noRate)
	}
	for _, l := range exp.Lines {
		if l.Amount.String() == "400000" {
			eq(t, "VAT", l.ExpectedVAT, "880")
			eq(t, "deduction", l.ExpectedDeduction, "8880")
			eq(t, "net", l.ExpectedNet, "391120")
		}
	}

	// the proposal of payment lines is matched on the net with the VAT off: in due order (the payment without a rate first, its date is its own day) the nets add up to
	// 100,000 (no rate), 491,120, 1,077,800 and 1,273,800; 977,800 is closest to the third
	st := g.importStatement(t, "0", "977800", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,977800\n")
	line := lineOf(t, st, "Card settlement")
	pr, err := g.BankRec.SettlementProposal(g.admin, g.propID, st.ID, line.ID, "CARD")
	must(t, err)
	if len(pr.JournalLineIDs) != 3 || pr.Matched {
		t.Fatalf("proposal: %+v", pr)
	}
	eq(t, "expected net", pr.ExpectedNet, "1077800")
	eq(t, "difference", pr.Difference, "-100000")
	eq(t, "expected MDR", pr.ExpectedMDR, "20000")
	eq(t, "expected VAT", pr.ExpectedVAT, "2200")
	eq(t, "the deduction is the MDR and the VAT", pr.ExpectedDeduction, "22200")
	if pr.WithoutVATRate != 0 {
		t.Fatalf("the payment without a VAT rate is the fourth: %+v", pr)
	}
}
