package banquet_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"kamarapms/internal/banquet"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/db"
)

// Cancellation, forfeit, transfer to the master folio of a group, the races and the close of the day (36 10.3, 4.7, 4.8, 4.9 and section 11 tests 5 to 8 and 11).

// policy makes the template of the examples: 30% at the signature, 50% 30 days before, 20% 7 days before; the fee is 10% from 60 days before, 50% from 30 to 59, 75% from 7 to 29, all of it after.
func (m *mfx) policy(t *testing.T, base string) banquet.PolicyTemplate {
	t.Helper()
	d30, d7 := 30, 7
	z, hi := 6, 29
	mid, far := 59, 60
	_ = far
	tpl, err := m.svc.CreatePolicyTemplate(m.mgr, m.propID, banquet.PolicyTemplateInput{
		Code: "STD" + base, Name: "Standard " + base, IsDefault: true, Base: base,
		Deposits: []banquet.TemplateDeposit{{Label: "Signature", Percent: dec("30")}, {Label: "Month before", DaysBefore: &d30, Percent: dec("50")}, {Label: "Week before", DaysBefore: &d7, Percent: dec("20")}},
		Tiers: []banquet.TierInput{
			{DaysFrom: 60, Percent: dec("10"), Action: banquet.ActionFee},
			{DaysFrom: 30, DaysTo: &mid, Percent: dec("50"), Action: banquet.ActionFee},
			{DaysFrom: 7, DaysTo: &hi, Percent: dec("75"), Action: banquet.ActionFee},
			{DaysFrom: 0, DaysTo: &z, Percent: dec("100"), Action: banquet.ActionFee},
		}})
	must(t, err)
	return tpl
}

// contract is the example of 10.3: 100 guests of the package at 363,000 nett (36,300,000 with the service and the tax), 46 days before the date, confirmed on the template, with 30% and 50% paid.
func (m *mfx) contract(t *testing.T, base string) (banquet.Event, int64, banquet.FolioRef) {
	t.Helper()
	m.policy(t, base)
	ev, sid := m.eventOn(t, "Wedding "+base, "2026-11-15", 100)
	m.packageLine(t, ev.ID, sid, banquet.PriceInclusive)
	ev = m.confirm(t, ev.ID)
	fo := m.folioOf(t, ev.ID)
	m.pay(t, fo.ID, "10890000", "dp1-"+base)
	m.pay(t, fo.ID, "18150000", "dp2-"+base)
	return ev, sid, m.folioOf(t, ev.ID)
}

func TestTheScheduleAndThePolicyAreCopiedFromTheTemplateOnConfirm(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, _ := m.contract(t, banquet.BaseNet)
	v, err := f.svc.Money(f.mgr, f.propID, ev.ID)
	must(t, err)
	if len(v.Schedule) != 3 || !v.Schedule[0].Amount.Equal(dec("10890000")) || !v.Schedule[1].Amount.Equal(dec("18150000")) || !v.Schedule[2].Amount.Equal(dec("7260000")) || !v.ScheduleTotal.Equal(dec("36300000")) {
		t.Fatalf("schedule: %+v", v.Schedule)
	}
	if v.Policy.Base != banquet.BaseNet || len(v.Policy.Tiers) != 4 || v.Policy.Current == nil || v.Policy.DaysLeft != 46 {
		t.Fatalf("policy: %+v", v.Policy)
	}
	if !v.DepositReceived.Equal(dec("29040000")) {
		t.Fatalf("deposits received: %s", v.DepositReceived)
	}
	// changing the template later changes no event
	tpls, err := f.svc.ListPolicyTemplates(f.mgr, f.propID, nil)
	must(t, err)
	_, err = f.svc.UpdatePolicyTemplate(f.mgr, f.propID, tpls[0].ID, banquet.PolicyTemplateInput{Code: tpls[0].Code, Name: tpls[0].Name, IsDefault: true, Base: banquet.BaseGross,
		Deposits: []banquet.TemplateDeposit{{Label: "All", Percent: dec("100")}}, Tiers: []banquet.TierInput{{DaysFrom: 0, Percent: dec("100"), Action: banquet.ActionFee}}})
	must(t, err)
	again, err := f.svc.Money(f.mgr, f.propID, ev.ID)
	must(t, err)
	if again.Policy.Base != banquet.BaseNet || len(again.Policy.Tiers) != 4 || len(again.Schedule) != 3 {
		t.Fatalf("the event followed the template: %+v", again.Policy)
	}
}

func TestCancellingADefiniteEventOnTheNetBaseMatchesSection10_3(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, fo := m.contract(t, banquet.BaseNet)
	q, err := f.svc.CancelQuote(f.mgr, f.propID, ev.ID)
	must(t, err)
	if q.DaysBefore != 46 || q.Tier == nil || !q.Tier.Percent.Equal(dec("50")) || !q.BaseAmount.Equal(dec("30000000")) || !q.Fee.Equal(dec("15000000")) {
		t.Fatalf("quote: %+v", q)
	}
	if !q.DepositsHeld.Equal(dec("29040000")) || !q.Applied.Equal(dec("15000000")) || !q.Refundable.Equal(dec("14040000")) || !q.StillOwed.IsZero() {
		t.Fatalf("quote money: %+v", q)
	}
	res, err := f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	must(t, err)
	if res.Event.Status != banquet.StatusCancelled || !res.Posted.Equal(dec("15000000")) || res.FolioItem == nil || !res.Refundable.Equal(dec("14040000")) || res.FolioID == nil || *res.FolioID != fo.ID {
		t.Fatalf("result: %+v", res)
	}
	after := m.folioOf(t, ev.ID)
	if !after.Balance.Equal(dec("-14040000")) {
		t.Fatalf("the folio keeps the deposit that is left: %s", after.Balance)
	}
	// the fee is posted once and the event is closed
	_, err = f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	_ = code(t, err, "EVENT_STATUS_INVALID")
	m.closeDay(t)
	m.requireBalanced(t)
}

func TestCancellingOnTheGrossBasePostsTheFeeInclusive(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, _ := m.contract(t, banquet.BaseGross)
	q, err := f.svc.CancelQuote(f.mgr, f.propID, ev.ID)
	must(t, err)
	if !q.BaseAmount.Equal(dec("36300000")) || !q.Fee.Equal(dec("18150000")) || !q.Refundable.Equal(dec("10890000")) {
		t.Fatalf("quote: %+v", q)
	}
	res, err := f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	must(t, err)
	if !res.Posted.Equal(dec("18150000")) || !res.Refundable.Equal(dec("10890000")) {
		t.Fatalf("result: %+v", res)
	}
	var debit, mode string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT debit::text, price_mode FROM folio_items WHERE id = $1`, *res.FolioItem).Scan(&debit, &mode))
	if !dec(debit).Equal(dec("18150000")) || mode != "INCLUSIVE" {
		t.Fatalf("the fee on the folio: %s %s", debit, mode)
	}
}

func TestALowerFeeNeedsItsReasonAndAForfeitNeverExceedsTheDeposits(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _, _ := m.contract(t, banquet.BaseNet)
	_, err := f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason, Amount: ptr(dec("1000000"))})
	_ = code(t, err, "FEE_REASON_REQUIRED")
	_, err = f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason, Amount: ptr(dec("30000000")), Action: banquet.ActionForfeit})
	_ = code(t, err, "FORFEIT_EXCEEDS_DEPOSITS")
	res, err := f.svc.CancelEvent(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason, Amount: ptr(dec("1000000")), FeeReason: ptr("loyal customer")})
	must(t, err)
	if !res.Posted.Equal(dec("1000000")) {
		t.Fatalf("posted %s", res.Posted)
	}
}

// ---------------------------------------------------------------- Q2: a tentative event with a down payment

func TestADownPaymentOnATentativeEventOpensOneEventFolioAndCanBeForfeited(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _ := m.eventOn(t, "Option with DP", "2026-11-15", 50)
	a, err := f.svc.OpenFolio(f.mgr, f.propID, ev.ID)
	must(t, err)
	b, err := f.svc.OpenFolio(f.mgr, f.propID, ev.ID)
	must(t, err)
	if a.ID != b.ID {
		t.Fatalf("two folios for one event: %d and %d", a.ID, b.ID)
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folios WHERE property_id = $1 AND banquet_event_id = $2`, f.propID, ev.ID).Scan(&n))
	if n != 1 {
		t.Fatalf("%d event folios", n)
	}
	m.pay(t, a.ID, "5000000", "dp")
	mv, err := f.svc.Money(f.mgr, f.propID, ev.ID)
	must(t, err)
	if !mv.DepositReceived.Equal(dec("5000000")) {
		t.Fatalf("a payment before the first charge is a deposit: %s", mv.DepositReceived)
	}
	_, err = f.svc.MarkLost(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.lostReason, Amount: ptr(dec("6000000"))})
	_ = code(t, err, "FORFEIT_EXCEEDS_DEPOSITS")
	res, err := f.svc.MarkLost(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.lostReason, Amount: ptr(dec("2000000")), Action: banquet.ActionForfeit})
	must(t, err)
	if res.Event.Status != banquet.StatusLost || !res.Posted.Equal(dec("2000000")) || !res.Refundable.Equal(dec("3000000")) {
		t.Fatalf("result: %+v", res)
	}
}

func TestATentativeEventLostWithoutAForfeitKeepsTheDepositForARefund(t *testing.T) {
	f := setup(t)
	m := f.money(t)
	ev, _ := m.eventOn(t, "Option lost", "2026-11-15", 50)
	fo, err := f.svc.OpenFolio(f.mgr, f.propID, ev.ID)
	must(t, err)
	m.pay(t, fo.ID, "5000000", "dp")
	res, err := f.svc.MarkLost(f.mgr, f.propID, ev.ID, banquet.CloseInput{ReasonID: f.lostReason})
	must(t, err)
	if !res.Posted.IsZero() || res.FolioItem != nil || !res.Refundable.Equal(dec("5000000")) {
		t.Fatalf("result: %+v", res)
	}
}

// ---------------------------------------------------------------- the structural guard

// The banquet reaches the ledger only through the entry points of the folios for an event: it never posts with PostCharge or writes folio_items itself.
func TestTheBanquetPostsOnlyThroughTheEventEntryPoints(t *testing.T) {
	files, err := os.ReadDir(".")
	must(t, err)
	bad := regexp.MustCompile(`\.PostCharge\(|INSERT INTO folio_items|folio_items`)
	comments := regexp.MustCompile(`(?m)//.*$`)
	for _, e := range files {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		must(t, err)
		if bad.Match(comments.ReplaceAll(src, nil)) {
			t.Fatalf("%s reaches the ledger by itself", e.Name())
		}
	}
	sql, err := os.ReadFile("queries_money.sql")
	must(t, err)
	if regexp.MustCompile(`(?i)(INSERT INTO|UPDATE|DELETE FROM)\s+(folio_items|payments|gl_)`).Match(sql) {
		t.Fatal("a money query writes the ledger")
	}
	_ = folios.SourceBanquet
	_ = db.ForShare
}
