package bankrec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// Voiding a card settlement (docs/architecture/40-card-settlement-void.md, KD-05). The numbers of the tests are those of its section 9.

func (g *guestFx) void(t *testing.T, ctx context.Context, id int64, reason string) (bankrec.SettlementRow, error) {
	t.Helper()
	return g.BankRec.VoidSettlement(ctx, g.propID, id, bankrec.VoidSettlementInput{Reason: reason, Approval: g.approval()})
}

// voidPair is what the settlement journal and its reversal hold together, by account code (debit less credit), and the number of lines of each.
func (g *guestFx) voidPair(t *testing.T, settlementID int64) (map[string]decimal.Decimal, int, int) {
	t.Helper()
	rows, err := g.Pool.Query(context.Background(), `SELECT a.code, sum(l.debit - l.credit)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		JOIN card_settlements s ON l.journal_id IN (s.journal_id, s.void_journal_id) WHERE s.id = $1 GROUP BY a.code`, settlementID)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var code, amount string
		must(t, rows.Scan(&code, &amount))
		out[code] = dec(amount)
	}
	must(t, rows.Err())
	orig := g.Count(t, `SELECT count(*) FROM gl_journal_lines l JOIN card_settlements s ON l.journal_id = s.journal_id WHERE s.id = $1`, settlementID)
	mirror := g.Count(t, `SELECT count(*) FROM gl_journal_lines l JOIN card_settlements s ON l.journal_id = s.void_journal_id WHERE s.id = $1`, settlementID)
	return out, orig, mirror
}

// Test 1: the void of a creditable settlement in an open month.
func TestVoidingASettlementMirrorsItsJournalReleasesItsPaymentsAndUnmatchesItsLine(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	st := g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	if row.Status != "POSTED" || row.StatementID != st.ID || row.StatementLineNo != 1 {
		t.Fatalf("a live settlement names the line it is matched with: %+v", row)
	}
	eq(t, "bank after the settlement", g.balanceOf(t, "1130"), "977800")
	eq(t, "input VAT after the settlement", g.balanceOf(t, "1425"), "2200")

	got, err := g.void(t, g.admin, row.ID, "  wrong payments  ")
	must(t, err)
	if got.Status != "VOIDED" || got.VoidReason == nil || *got.VoidReason != "wrong payments" || got.VoidedAt == nil || got.VoidJournalNumber == nil || got.StatementID != 0 {
		t.Fatalf("the voided settlement: %+v", got)
	}
	// the mirror journal: a reversal dated the settlement, approved
	var typ, date string
	var reverses, approvedBy, settled int64
	must(t, g.Pool.QueryRow(context.Background(), `SELECT j.journal_type, j.journal_date::text, j.reverses_journal_id, j.approved_by, s.journal_id FROM gl_journals j
		JOIN card_settlements s ON s.void_journal_id = j.id WHERE s.id = $1`, row.ID).Scan(&typ, &date, &reverses, &approvedBy, &settled))
	var admin int64
	must(t, g.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, g.email).Scan(&admin))
	if typ != "REVERSAL" || date != "2026-09-30" || reverses != settled || approvedBy != admin {
		t.Fatalf("reversal: %s %s reverses %d of %d, approved by %d", typ, date, reverses, settled, approvedBy)
	}
	sums, orig, mirror := g.voidPair(t, row.ID)
	for code, v := range sums {
		if !v.IsZero() {
			t.Errorf("account %s: the settlement and its reversal leave %s", code, v)
		}
	}
	if orig != 4 || mirror != 4 {
		t.Fatalf("the mirror has every line: %d and %d", orig, mirror)
	}
	// the books are back where they were before the settlement
	eq(t, "bank", g.balanceOf(t, "1130"), "0")
	eq(t, "commission", g.balanceOf(t, "6130"), "0")
	eq(t, "input VAT", g.balanceOf(t, "1425"), "0")
	eq(t, "card clearing", g.balanceOf(t, "1150"), "1000000")
	// the payment line is released
	if g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE released_at IS NOT NULL`) != 1 || g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE released_at IS NULL`) != 0 {
		t.Fatal("the item of the settlement is released")
	}
	// the statement line has nothing matched; the bank line and its mirror are cleared together, on their own
	d, err := g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	if l := lineOf(t, d, "Card settlement"); l.Matched || len(l.Clearings) != 0 {
		t.Fatalf("the line is unmatched: %+v", l)
	}
	if len(d.Clearings) != 2 || d.Clearings[0].StatementLineID != nil || d.Clearings[1].StatementLineID != nil || !d.Clearings[0].Amount.Add(d.Clearings[1].Amount).IsZero() {
		t.Fatalf("the pair is cleared on its own: %+v", d.Clearings)
	}
	for _, c := range d.Clearings {
		if c.SettlementID != nil {
			t.Fatalf("a voided settlement has no live matching: %+v", c)
		}
	}
	eq(t, "cleared", d.Summary.ClearedTotal, "0")
	for _, u := range g.uncleared(t, st.ID) {
		if u.Amount.Abs().Equal(dec("977800")) {
			t.Fatalf("the bank lines of the settlement are not left uncleared: %+v", u)
		}
	}
	// the audit keeps what was unmatched (40 4.3)
	var data string
	must(t, g.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'bank.settlement_voided' AND entity_type = 'bank_statement' AND entity_id = $1`, st.ID).Scan(&data))
	for _, want := range []string{`"clearings_deleted"`, `"line": 1`, `"statement_id": ` + itoa(st.ID), `"amount": "977800"`, `"vat_treatment": "CREDITABLE"`, `"approved_by"`, `"reversal": "` + *got.VoidJournalNumber + `"`} {
		if !strings.Contains(data, want) {
			t.Errorf("the audit row has %s: %s", want, data)
		}
	}
	if g.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.payables_journal_reversed'`) != 1 {
		t.Error("the reversal journal is audited by the books")
	}
}

func itoa(n int64) string { return decimal.NewFromInt(n).String() }

// Test 2: after a void the payments are settled again; neither the old credit line nor the debit of its mirror is a payment.
func TestAVoidedSettlementIsSettledAgain(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	st := g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	var payment, credit int64
	must(t, g.Pool.QueryRow(context.Background(), `SELECT settled_line_id, settling_line_id FROM card_settlement_items`).Scan(&payment, &credit))
	_, err := g.void(t, g.admin, row.ID, "wrong batch")
	must(t, err)
	var mirrorDebit int64
	must(t, g.Pool.QueryRow(context.Background(), `SELECT l.id FROM gl_journal_lines l JOIN card_settlements s ON s.void_journal_id = l.journal_id WHERE s.id = $1 AND l.account_id = $2`, row.ID, g.acc["1150"]).Scan(&mirrorDebit))

	lines, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CARD")
	must(t, err)
	if len(lines) != 1 || lines[0].JournalLineID != payment {
		t.Fatalf("only the released payment waits for its settlement: %+v", lines)
	}
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	if len(exp.Lines) != 1 || exp.Lines[0].JournalLineID != payment {
		t.Fatalf("the expected settlements list it again: %+v", exp.Lines)
	}
	line := lineOf(t, st, "Card settlement")
	for name, id := range map[string]int64{"the credit of the voided settlement": credit, "the debit of its reversal": mirrorDebit} {
		_, err := g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{id}, FeeAccountID: g.acc["6130"]})
		if e := asApp(err); e == nil || e.Code != "ALREADY_SETTLED" {
			t.Errorf("%s is not a payment: %v", name, err)
		}
	}
	again, err := g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{payment}, FeeAccountID: g.acc["6130"]})
	must(t, err)
	if !lineOf(t, again, "Card settlement").Matched {
		t.Fatal("the line is matched by the new settlement")
	}
	list, err := g.BankRec.Settlements(g.admin, g.propID, 0, 10)
	must(t, err)
	if len(list) != 2 || list[0].Status != "POSTED" || list[1].Status != "VOIDED" || list[0].ID == row.ID {
		t.Fatalf("a new settlement next to the voided one: %+v", list)
	}
	eq(t, "bank", g.balanceOf(t, "1130"), "977800")
	eq(t, "card clearing", g.balanceOf(t, "1150"), "0")
	if g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE settled_line_id = $1 AND released_at IS NULL`, payment) != 1 {
		t.Fatal("one live item")
	}
	// the statement reconciles: the pair of the void adds nothing, the new settlement matches the line
	_, err = g.BankRec.Reconcile(g.admin, g.propID, st.ID)
	must(t, err)
}

// settleWith pays 1,000,000 by card, closes the day and settles it from a line of what the bank paid.
func (g *guestFx) settleWith(t *testing.T, paid string, fee bool) bankrec.SettlementRow {
	t.Helper()
	g.pay(t, "k1", "CARD", "1000000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", paid, "date,description,reference,amount\n2026-09-30,Card settlement,SETT-1,"+paid+"\n")
	exp, err := g.BankRec.ExpectedSettlements(g.admin, g.propID, "CARD")
	must(t, err)
	in := bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{exp.Lines[0].JournalLineID}}
	if fee {
		in.FeeAccountID = g.acc["6130"]
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, lineOf(t, st, "Card settlement").ID, in)
	must(t, err)
	return g.lastSettlement(t)
}

// Test 3: every shape of a settlement journal is mirrored.
func TestEveryShapeOfASettlementIsMirrored(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(g *guestFx)
		paid  string
		fee   bool
		lines int
	}{
		"VAT as an expense": {func(g *guestFx) { g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01") }, "977800", true, 3},
		"VAT deferred": {func(g *guestFx) {
			g.pkpFrom(t, "2026-09-30", "DEFERRED")
			g.ruleVAT(t, "CARD", "2", "11", 1, "2026-09-01")
		}, "977800", true, 4},
		"no VAT":        {func(g *guestFx) { g.rule(t, "CARD", "2", 1, "2026-09-01") }, "980000", true, 3},
		"no commission": {func(g *guestFx) {}, "1000000", false, 2},
	} {
		t.Run(name, func(t *testing.T) {
			g := setupGuests(t)
			c.setup(g)
			row := g.settleWith(t, c.paid, c.fee)
			_, err := g.void(t, g.admin, row.ID, "wrong")
			must(t, err)
			sums, orig, mirror := g.voidPair(t, row.ID)
			if orig != c.lines || mirror != c.lines {
				t.Fatalf("lines: %d and %d, want %d", orig, mirror, c.lines)
			}
			for code, v := range sums {
				if !v.IsZero() {
					t.Errorf("account %s: %s left", code, v)
				}
			}
			eq(t, "card clearing", g.balanceOf(t, "1150"), "1000000")
		})
	}
}

// Test 4: a closed month refuses the void with its own message and writes nothing; once the month is reopened the void goes ahead.
func TestAVoidInAClosedMonthIsRefusedUntilTheMonthIsReopened(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	must(t, g.Exec(t, `INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES ($1, $2, '2026-09-01')`, g.tenantID, g.propID))
	_, err := g.void(t, g.admin, row.ID, "wrong")
	e := roomstest.Code(t, err, "PERIOD_CLOSED")
	if e.Context["month"] != "2026-09" || e.Context["period"] != "2026-09-01" || !strings.Contains(e.Message, "2026-09") || !strings.Contains(e.Message, "accountant") || !strings.Contains(e.Message, "manual journal") {
		t.Fatalf("the message names the month and who decides: %q %v", e.Message, e.Context)
	}
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`) != 0 || g.Count(t, `SELECT count(*) FROM card_settlements WHERE status = 'POSTED'`) != 1 ||
		g.Count(t, `SELECT count(*) FROM bank_clearings`) != 1 || g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE released_at IS NULL`) != 1 {
		t.Fatal("a refused void writes nothing")
	}
	_, err = g.Accounting.ReopenPeriod(g.admin, g.propID, d("2026-09-01"), "void a wrong settlement")
	must(t, err)
	_, err = g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
}

// Test 5: a reconciled statement refuses the void until it is reopened.
func TestAVoidNeedsTheStatementOpen(t *testing.T) {
	g := setupGuests(t)
	st := g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	_, err := g.BankRec.Reconcile(g.admin, g.propID, st.ID)
	must(t, err)
	_, err = g.void(t, g.admin, row.ID, "wrong")
	e := roomstest.Code(t, err, "STATEMENT_RECONCILED")
	if e.Context["statement_id"] != st.ID {
		t.Fatalf("it names the statement: %v", e.Context)
	}
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`) != 0 {
		t.Fatal("nothing is reversed")
	}
	_, err = g.BankRec.Reopen(g.admin, g.propID, st.ID, bankrec.ReopenInput{Reason: "void a settlement", Approval: g.approval()})
	must(t, err)
	_, err = g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
}

// Test 6: a settlement whose matching was removed before this change (the trap of KD-05) is voided; its two bank lines are then cleared together by hand.
func TestASettlementAlreadyUnmatchedIsVoidedAndItsBankLinesClearedByHand(t *testing.T) {
	g := setupGuests(t)
	st := g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	must(t, g.Exec(t, `DELETE FROM bank_clearings WHERE property_id = $1`, g.propID)) // what Unclear did before the change
	got, err := g.void(t, g.admin, row.ID, "stuck since the unmatching")
	must(t, err)
	if got.Status != "VOIDED" || g.Count(t, `SELECT count(*) FROM bank_clearings`) != 0 {
		t.Fatalf("voided, nothing cleared by itself: %+v", got)
	}
	var pair []int64
	for _, u := range g.uncleared(t, st.ID) {
		if u.Amount.Abs().Equal(dec("977800")) {
			pair = append(pair, u.JournalLineID)
		}
	}
	if len(pair) != 2 {
		t.Fatalf("the bank line and its mirror wait side by side: %v", pair)
	}
	_, err = g.BankRec.Clear(g.admin, g.propID, st.ID, bankrec.ClearInput{JournalLineIDs: pair})
	must(t, err)
	for _, u := range g.uncleared(t, st.ID) {
		if u.Amount.Abs().Equal(dec("977800")) {
			t.Fatalf("cleared together: %+v", u)
		}
	}
}

// Test 7: the matching of a live settlement is not undone by Unclear; an ordinary clearing still is.
func TestUnclearOfASettlementIsRefused(t *testing.T) {
	g := setupGuests(t)
	st := g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	d, err := g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	c := lineOf(t, d, "Card settlement").Clearings[0]
	if c.SettlementID == nil || *c.SettlementID != row.ID {
		t.Fatalf("the clearing names its settlement: %+v", c)
	}
	_, err = g.BankRec.Unclear(g.admin, g.propID, st.ID, c.ID)
	e := roomstest.Code(t, err, "CLEARING_IS_SETTLEMENT")
	if e.Context["settlement_id"] != row.ID || !strings.Contains(e.Message, "Void settlement") {
		t.Fatalf("it sends the user to the void: %q %v", e.Message, e.Context)
	}
	if g.Count(t, `SELECT count(*) FROM bank_clearings`) != 1 {
		t.Fatal("the matching stays")
	}
	// after the void the pair is an ordinary clearing: Unclear takes it apart as any other
	_, err = g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
	d, err = g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	_, err = g.BankRec.Unclear(g.admin, g.propID, st.ID, d.Clearings[0].ID)
	must(t, err)
}

// Test 8: the void is a business-dated write: it needs the open business day.
func TestAVoidNeedsTheOpenBusinessDay(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	must(t, g.Exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, g.propID))
	_, err := g.void(t, g.admin, row.ID, "wrong")
	wantCode(t, err, "BUSINESS_DAY_NOT_FOUND")
	if g.Count(t, `SELECT count(*) FROM card_settlements WHERE status = 'POSTED'`) != 1 {
		t.Fatal("nothing voided")
	}
}

// Test 9: the permission, the tenant, the reason and the approval of the kind BANK_SETTLEMENT_VOID, whose amount is the gross.
func TestAVoidNeedsItsPermissionItsTenantAReasonAndAnApproval(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	viewer := g.User(t, g.tenantID, g.propID, auth.PermBankView)
	_, err := g.void(t, viewer, row.ID, "wrong")
	wantCode(t, err, "PERMISSION_DENIED")
	g.Clock.Set(roomstest.T0) // a property is made on the day of the clock
	other := g.Tenant(t, "XYZ")
	otherProp := g.Property(t, other.ID, "SG")
	otherAdmin, otherEmail := g.AdminAccount(t, other.ID)
	_, err = g.BankRec.VoidSettlement(otherAdmin, otherProp.ID, row.ID, bankrec.VoidSettlementInput{Reason: "x", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}})
	wantCode(t, err, "SETTLEMENT_NOT_FOUND")
	_, err = g.BankRec.VoidSettlement(otherAdmin, g.propID, row.ID, bankrec.VoidSettlementInput{Reason: "x", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = g.BankRec.VoidSettlement(g.admin, g.propID, 999999, bankrec.VoidSettlementInput{Reason: "x", Approval: g.approval()})
	wantCode(t, err, "SETTLEMENT_NOT_FOUND")
	_, err = g.void(t, g.admin, row.ID, "   ")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = g.void(t, g.admin, row.ID, strings.Repeat("x", 501))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = g.BankRec.VoidSettlement(g.admin, g.propID, row.ID, bankrec.VoidSettlementInput{Reason: "wrong"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = g.BankRec.VoidSettlement(g.admin, g.propID, row.ID, bankrec.VoidSettlementInput{Reason: "wrong", Approval: &iam.ApprovalInput{Email: g.email, Password: "not the password"}})
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	// the rule of the property compares the gross (1,000,000) with its limit
	lead, leadEmail := g.Account(t, g.tenantID, g.propID, auth.PermBankReconcile, auth.PermBankView, auth.PermCorrectionApprove)
	own := &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}
	g.SetRule(t, g.tenantID, g.propID, "BANK_SETTLEMENT_VOID", "DISTINCT_ABOVE", "999999")
	_, err = g.BankRec.VoidSettlement(lead, g.propID, row.ID, bankrec.VoidSettlementInput{Reason: "wrong", Approval: own})
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "BANK_SETTLEMENT_VOID" || !dec(e.Context["amount"].(string)).Equal(dec("1000000")) {
		t.Fatalf("context: %v", e.Context)
	}
	if g.Count(t, `SELECT count(*) FROM card_settlements WHERE status = 'VOIDED'`) != 0 {
		t.Fatal("no refused call voids")
	}
	g.SetRule(t, g.tenantID, g.propID, "BANK_SETTLEMENT_VOID", "DISTINCT_ABOVE", "1000000")
	_, err = g.BankRec.VoidSettlement(lead, g.propID, row.ID, bankrec.VoidSettlementInput{Reason: "wrong", Approval: own})
	must(t, err)
}

// Test 10: a voided settlement is not voided again.
func TestASettlementIsVoidedOnce(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	_, err := g.void(t, g.admin, row.ID, "wrong")
	must(t, err)
	_, err = g.void(t, g.admin, row.ID, "again")
	wantCode(t, err, "SETTLEMENT_ALREADY_VOIDED")
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`) != 1 {
		t.Fatal("one reversal")
	}
}
