package bankrec_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

func (g *guestFx) rule(t *testing.T, method, rate string, days int, from string) bankrec.FeeRule {
	t.Helper()
	r, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: method, MDRRate: rate, SettlementDays: days, EffectiveFrom: d(from)})
	must(t, err)
	return r
}

func TestFeeRulesAreChecked(t *testing.T) {
	g := setupGuests(t)
	for name, in := range map[string]bankrec.FeeRuleInput{
		"a rate above 100": {PaymentMethod: "CARD", MDRRate: "101", SettlementDays: 1, EffectiveFrom: d("2026-09-01")},
		"a negative rate":  {PaymentMethod: "CARD", MDRRate: "-1", SettlementDays: 1, EffectiveFrom: d("2026-09-01")},
		"five decimals":    {PaymentMethod: "CARD", MDRRate: "1.23456", SettlementDays: 1, EffectiveFrom: d("2026-09-01")},
		"too many days":    {PaymentMethod: "CARD", MDRRate: "2", SettlementDays: 61, EffectiveFrom: d("2026-09-01")},
		"a method of cash": {PaymentMethod: "CASH", MDRRate: "2", SettlementDays: 1, EffectiveFrom: d("2026-09-01")},
		"no date":          {PaymentMethod: "CARD", MDRRate: "2", SettlementDays: 1},
	} {
		_, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	g.rule(t, "CARD", "2", 2, "2026-09-01")
	_, err := g.BankRec.CreateCardFeeRule(g.admin, g.propID, bankrec.FeeRuleInput{PaymentMethod: "CARD", MDRRate: "3", SettlementDays: 1, EffectiveFrom: d("2026-09-01")})
	wantCode(t, err, "FEE_RULE_EXISTS")
	g.rule(t, "CARD", "2.5", 2, "2026-10-15")
	g.rule(t, "OTHER", "0.7", 1, "2026-09-01")
	list, err := g.BankRec.CardFeeRules(g.admin, g.propID)
	must(t, err)
	if len(list) != 3 || list[0].PaymentMethod != "CARD" || list[0].MDRRate != "2.5" || list[1].MDRRate != "2" {
		t.Fatalf("rules, newest first within a method: %+v", list)
	}
	viewer := g.User(t, g.tenantID, g.propID, auth.PermBankView)
	if _, err := g.BankRec.CardFeeRules(viewer, g.propID); err != nil {
		t.Fatal(err)
	}
	_, err = g.BankRec.CreateCardFeeRule(viewer, g.propID, bankrec.FeeRuleInput{PaymentMethod: "CARD", MDRRate: "1", SettlementDays: 1, EffectiveFrom: d("2026-11-01")})
	wantCode(t, err, "PERMISSION_DENIED")
	if err := g.Exec(t, `UPDATE card_fee_rules SET mdr_rate = 1`); err == nil {
		t.Error("a rule was changed")
	}
	if g.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bank.card_fee_rule_added'`) != 3 {
		t.Error("rules are audited")
	}
}

func TestAPaymentKeepsTheRateOfItsDayAndTheFeeComputedFromIt(t *testing.T) {
	g := setupGuests(t)
	g.rule(t, "CARD", "2.5", 2, "2026-09-01")
	card := g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	if card.Payment.MDRRate == nil || *card.Payment.MDRRate != "2.5" || card.Payment.MDRFee == nil || *card.Payment.MDRFee != "10000" ||
		card.Payment.ExpectedSettlementDate == nil || card.Payment.ExpectedSettlementDate.String() != "2026-10-02" {
		t.Fatalf("snapshot: %+v", card.Payment)
	}
	// no rule for the wallet, none for cash: no snapshot
	wallet := g.pay(t, "k2", "OTHER", "50000", "QR")
	cash := g.pay(t, "k3", "CASH", "1000", "")
	if wallet.Payment.MDRRate != nil || cash.Payment.MDRRate != nil {
		t.Fatalf("no rule, no snapshot: %+v %+v", wallet.Payment, cash.Payment)
	}
	// the rate changes: what was taken before keeps its own; a rule that has not started yet does not apply
	g.rule(t, "CARD", "3", 1, "2026-10-01")
	g.rule(t, "CARD", "4", 1, "2026-12-01")
	again := g.pay(t, "k4", "CARD", "100000", "AUTH-2")
	if *again.Payment.MDRRate != "2.5" {
		t.Fatalf("the rule of 1 Oct has not started on 30 Sep: %s", *again.Payment.MDRRate)
	}
	g.closeDay(t)
	next := g.pay(t, "k5", "CARD", "100000", "AUTH-3")
	if *next.Payment.MDRRate != "3" || *next.Payment.MDRFee != "3000" || next.Payment.ExpectedSettlementDate.String() != "2026-10-02" {
		t.Fatalf("the new rate: %+v", next.Payment)
	}
	got, err := g.Folios.GetPayment(g.admin, g.propID, card.Payment.ID)
	must(t, err)
	if *got.MDRRate != "2.5" || *got.MDRFee != "10000" {
		t.Fatalf("the first payment did not move: %+v", got)
	}
	// rounded to the currency: 2.5% of 333 is 8.325, rounded to 8
	odd := g.pay(t, "k6", "CARD", "333", "AUTH-4")
	if *odd.Payment.MDRFee != "10" { // the rule of 3% applies now: 9.99 rounds to 10
		t.Fatalf("rounded fee: %s", *odd.Payment.MDRFee)
	}
	if err := g.Exec(t, `UPDATE payments SET mdr_fee = 1 WHERE id = $1`, card.Payment.ID); err == nil {
		t.Error("a snapshot was changed")
	}
}

func TestTheExpectedSettlementsListWhatTheBankShouldPayAndWhatIsLate(t *testing.T) {
	g := setupGuests(t)
	g.rule(t, "CARD", "2", 0, "2026-09-01") // the acquirer pays the same day
	g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	g.pay(t, "k2", "CARD", "600000", "AUTH-2")
	g.closeDay(t) // the business date is now 1 Oct: both were due on 30 Sep
	g.rule(t, "CARD", "2", 5, "2026-10-01")
	g.pay(t, "k3", "CARD", "500000", "AUTH-3") // due on 6 Oct
	g.closeDay(t)                              // its journal is made with the day close
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	eq(t, "gross", exp.Gross, "1500000")
	eq(t, "expected fee", exp.ExpectedFee, "30000")
	eq(t, "expected net", exp.ExpectedNet, "1470000")
	if len(exp.Lines) != 3 || exp.LateCount != 2 || exp.NoRate != 0 {
		t.Fatalf("expected: %+v", exp)
	}
	eq(t, "late gross", exp.LateGross, "1000000")
	if !exp.Lines[0].Late || exp.Lines[2].Late || exp.Lines[2].ExpectedDate.String() != "2026-10-06" {
		t.Fatalf("lines: %+v", exp.Lines)
	}
	eq(t, "first line net", exp.Lines[0].ExpectedNet, "392000")
	w, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "OTHER_PAYMENT")
	must(t, err)
	if len(w.Lines) != 0 {
		t.Fatalf("no wallet payments: %+v", w)
	}
	_, err = g.BankRec.ExpectedSettlements(g.admin, g.propID, "CASH")
	wantCode(t, err, "VALIDATION_FAILED")
	nobody := g.User(t, g.tenantID, g.propID)
	_, err = g.BankRec.ExpectedSettlements(nobody, g.propID, "CARD")
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestAProposalPicksTheOldestPaymentsThatAddUpToTheBankLine(t *testing.T) {
	g := setupGuests(t)
	g.rule(t, "CARD", "2", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	g.pay(t, "k2", "CARD", "600000", "AUTH-2")
	g.closeDay(t)
	g.pay(t, "k3", "CARD", "300000", "AUTH-3") // made on 1 Oct
	g.closeDay(t)
	// 392,000 + 588,000 = 980,000 is what the acquirer should pay for the first two
	st := g.importStatement(t, "0", "1313333", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,980000\n2026-10-01,Odd payout,SETT-2,333333\n")
	line := lineOf(t, st, "Card settlement")
	pr, err := g.BankRec.SettlementProposal(g.admin, g.propID, st.ID, line.ID, "CARD")
	must(t, err)
	if !pr.Matched || len(pr.JournalLineIDs) != 2 {
		t.Fatalf("proposal: %+v", pr)
	}
	eq(t, "gross", pr.Gross, "1000000")
	eq(t, "expected fee", pr.ExpectedFee, "20000")
	eq(t, "difference", pr.Difference, "0")
	// the settlement itself is the usual call; the fee it cost is compared with the fee expected
	done, err := g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: pr.JournalLineIDs, FeeAccountID: g.acc["6130"]})
	must(t, err)
	if !lineOf(t, done, "Card settlement").Matched {
		t.Fatal("matched")
	}
	list, err := g.BankRec.Settlements(g.admin, g.propID, 0, 50)
	must(t, err)
	if len(list) != 1 || list[0].ExpectedFee == nil || list[0].FeeVariance == nil || list[0].Payments != 2 {
		t.Fatalf("settlements: %+v", list)
	}
	eq(t, "fee", list[0].Fee, "20000")
	eq(t, "expected fee", *list[0].ExpectedFee, "20000")
	eq(t, "variance", *list[0].FeeVariance, "0")
	// what is left is offered to the next line; an amount that fits nothing is not a match, but still the best choice
	other := lineOf(t, st, "Odd payout")
	pr2, err := g.BankRec.SettlementProposal(g.admin, g.propID, st.ID, other.ID, "CARD")
	must(t, err)
	if pr2.Matched || len(pr2.JournalLineIDs) != 1 {
		t.Fatalf("the third payment is all there is, and it is not a match: %+v", pr2)
	}
	eq(t, "difference", pr2.Difference, "39333") // 333,333 against the 294,000 it should come to
	_, err = g.BankRec.SettlementProposal(g.admin, g.propID, st.ID, 999999, "CARD")
	wantCode(t, err, "STATEMENT_LINE_NOT_FOUND")
}

func TestASettlementShowsTheFeeThatWasNotExpected(t *testing.T) {
	g := setupGuests(t)
	g.rule(t, "CARD", "2", 1, "2026-09-01")
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	// the acquirer kept 2.4% instead of 2%
	st := g.importStatement(t, "0", "976000", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,976000\n")
	line := lineOf(t, st, "Card settlement")
	pr, err := g.BankRec.SettlementProposal(g.admin, g.propID, st.ID, line.ID, "CARD")
	must(t, err)
	// 980,000 expected against 976,000: within half a percent of the line (4,880), so it is proposed, with the difference shown
	if !pr.Matched || len(pr.JournalLineIDs) != 1 {
		t.Fatalf("proposal: %+v", pr)
	}
	eq(t, "difference", pr.Difference, "-4000")
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: pr.JournalLineIDs, FeeAccountID: g.acc["6130"]})
	must(t, err)
	list, err := g.BankRec.Settlements(g.admin, g.propID, 0, 50)
	must(t, err)
	eq(t, "fee taken", list[0].Fee, "24000")
	eq(t, "variance", *list[0].FeeVariance, "4000")
}

func TestASettlementOfPaymentsWithoutASnapshotHasNoExpectedFee(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "k1", "CARD", "400000", "AUTH-1") // no rule yet
	g.closeDay(t)
	st := g.importStatement(t, "0", "392000", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-1,392000\n")
	line := lineOf(t, st, "Card settlement")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	if exp.NoRate != 1 || exp.LateCount != 0 || exp.Lines[0].MDRRate != nil || exp.Lines[0].ExpectedDate != nil {
		t.Fatalf("a payment without a rate is not late and expects no fee: %+v", exp)
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{exp.Lines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	must(t, err)
	list, err := g.BankRec.Settlements(g.admin, g.propID, 0, 50)
	must(t, err)
	if list[0].ExpectedFee != nil || list[0].FeeVariance != nil {
		t.Fatalf("no snapshot, no expectation: %+v", list[0])
	}
}

func TestPickTakesTheNumberOfPaymentsClosestToTheBankLine(t *testing.T) {
	line := func(net string) bankrec.ExpectedLine {
		return bankrec.ExpectedLine{ExpectedNet: decimal.RequireFromString(net)}
	}
	lines := []bankrec.ExpectedLine{line("100"), line("200"), line("300")}
	for target, want := range map[string]int{"100": 1, "300": 2, "600": 3, "590": 3, "1": 1, "250": 2} {
		n, _ := bankrec.Pick(lines, decimal.RequireFromString(target))
		if n != want {
			t.Errorf("target %s: %d payments, want %d", target, n, want)
		}
	}
	_ = civil.Date{}
}
