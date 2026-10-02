package bankrec_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec"
	"kamarapms/internal/platform/auth"
)

func TestAJournalLineIsClearedOnceAndOnlyAgainstItsOwnAccount(t *testing.T) {
	f := setup(t)
	f.books(t)
	first := f.importFirst(t)
	dep := lineOf(t, first, "guest")
	pay := lineOf(t, first, "PLN")
	lines := f.uncleared(t, first.ID)
	var depJL, payJL int64
	for _, l := range lines {
		if l.Amount.Equal(dec("1000000")) {
			depJL = l.JournalLineID
		} else {
			payJL = l.JournalLineID
		}
	}
	// a clearing against the wrong amount leaves the line unmatched
	st, err := f.BankRec.Clear(f.admin, f.propID, first.ID, bankrec.ClearInput{StatementLineID: &pay.ID, JournalLineIDs: []int64{depJL}})
	must(t, err)
	if lineOf(t, st, "PLN").Matched || st.Summary.CanReconcile {
		t.Fatalf("a wrong amount does not match: %+v", lineOf(t, st, "PLN"))
	}
	// the same journal line again, for another statement line or in another statement, is refused
	_, err = f.BankRec.Clear(f.admin, f.propID, first.ID, bankrec.ClearInput{StatementLineID: &dep.ID, JournalLineIDs: []int64{depJL}})
	wantCode(t, err, "ALREADY_CLEARED")
	st, err = f.BankRec.Unclear(f.admin, f.propID, first.ID, st.Clearings[0].ID)
	must(t, err)
	if len(st.Clearings) != 0 || len(f.uncleared(t, first.ID)) != 2 {
		t.Fatalf("unmatched: %+v", st.Clearings)
	}
	_, err = f.BankRec.Unclear(f.admin, f.propID, first.ID, 999999)
	wantCode(t, err, "CLEARING_NOT_FOUND")
	st, err = f.BankRec.Clear(f.admin, f.propID, first.ID, bankrec.ClearInput{StatementLineID: &dep.ID, JournalLineIDs: []int64{depJL}})
	must(t, err)
	if !lineOf(t, st, "guest").Matched {
		t.Fatalf("matched: %+v", st.Lines)
	}
	// what is not allowed
	bad := func(name string, in bankrec.ClearInput) {
		_, err := f.BankRec.Clear(f.admin, f.propID, first.ID, in)
		e := roomstest_code(t, err)
		if len(e) == 0 {
			t.Errorf("%s: no field error", name)
		}
	}
	bad("no lines", bankrec.ClearInput{})
	bad("unknown journal line", bankrec.ClearInput{StatementLineID: &pay.ID, JournalLineIDs: []int64{999999}})
	bad("a line of another statement", bankrec.ClearInput{StatementLineID: ptr(int64(999999)), JournalLineIDs: []int64{payJL}})
	bad("twice in one request", bankrec.ClearInput{StatementLineID: &pay.ID, JournalLineIDs: []int64{payJL, payJL}})
	bad("without a statement line and not offsetting", bankrec.ClearInput{JournalLineIDs: []int64{payJL}})
	// a journal line of another account is not the bank's
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: "cash", Lines: []accounting.LineInput{
		{AccountID: f.acc["1110"], Debit: dec("5")}, {AccountID: f.acc["4110"], Credit: dec("5")}}}, "cash")
	must(t, err)
	var cashJL int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.id FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE a.code = '1110' LIMIT 1`).Scan(&cashJL))
	bad("another account", bankrec.ClearInput{StatementLineID: &pay.ID, JournalLineIDs: []int64{cashJL}})
}

func roomstest_code(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		t.Fatal("an error was expected")
	}
	var fields []string
	if e := asApp(err); e != nil {
		for _, f := range e.Fields {
			fields = append(fields, f.Field)
		}
		if len(fields) == 0 {
			fields = append(fields, e.Code)
		}
	}
	return fields
}

func TestOffsettingLinesAndTheOpeningLinesAreClearedWithoutAStatementLine(t *testing.T) {
	f := setup(t)
	// a booking and its reversal cancel each other, a third item is from before the first statement
	f.book(t, "wrong", "4110", "777", true)
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_journals WHERE idempotency_key = 'wrong'`).Scan(&id))
	_, err := f.Accounting.Reverse(f.admin, f.propID, id, accounting.ReverseInput{Reason: "wrong account", Approval: f.approval()})
	must(t, err)
	f.book(t, "old", "4110", "5000", true)
	st, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-10-01"), PeriodTo: d("2026-10-31"), OpeningBalance: dec("5000"), ClosingBalance: dec("5000"), CSV: "date,amount\n"})
	must(t, err)
	// statement end must cover the journal dates for the lines to be listed
	lines := f.uncleared(t, st.ID)
	if len(lines) != 3 {
		t.Fatalf("%d uncleared lines", len(lines))
	}
	var pair, old []int64
	for _, l := range lines {
		if l.Amount.Abs().Equal(dec("777")) {
			pair = append(pair, l.JournalLineID)
		} else {
			old = append(old, l.JournalLineID)
		}
	}
	// the old item is from before the first statement, so it is the opening balance
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: pair})
	must(t, err)
	done, err := f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: old})
	must(t, err)
	if !done.Summary.CanReconcile || len(done.Clearings) != 3 {
		t.Fatalf("ready: %+v", done.Summary)
	}
	eq(t, "cleared as opening", done.Summary.ClearedTotal, "5000")
	if _, err := f.BankRec.Reconcile(f.admin, f.propID, st.ID); err != nil {
		t.Fatal(err)
	}
	// a later statement cannot clear things on its own as opening: a first statement exists now
	f.book(t, "new", "4110", "9", true)
	next, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-11-01"), PeriodTo: d("2026-11-30"), OpeningBalance: dec("5000"), ClosingBalance: dec("5000"), CSV: "date,amount\n"})
	must(t, err)
	l2 := f.uncleared(t, next.ID)
	if len(l2) != 1 {
		t.Fatalf("uncleared: %+v", l2)
	}
	_, err = f.BankRec.Clear(f.admin, f.propID, next.ID, bankrec.ClearInput{JournalLineIDs: []int64{l2[0].JournalLineID}})
	if err == nil {
		t.Fatal("only the first statement of an account has an opening balance")
	}
}

func TestStatementsAreReconciledInOrderAndReopenedLatestFirst(t *testing.T) {
	f := setup(t)
	f.books(t)
	first := f.importFirst(t)
	_, err := f.BankRec.AutoMatch(f.admin, f.propID, first.ID)
	must(t, err)
	fee := lineOf(t, first, "fee")
	_, err = f.BankRec.Adjust(f.admin, f.propID, first.ID, fee.ID, bankrec.AdjustInput{AccountID: f.acc["6130"]})
	must(t, err)
	second, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-10-01"), PeriodTo: d("2026-10-31"), OpeningBalance: dec("735000"), ClosingBalance: dec("735000"), CSV: "date,amount\n"})
	must(t, err)
	// the second cannot be reconciled before the first
	if second.Summary.CanReconcile {
		t.Fatalf("the first is not reconciled: %+v", second.Summary)
	}
	_, err = f.BankRec.Reconcile(f.admin, f.propID, second.ID)
	wantCode(t, err, "STATEMENT_NOT_READY")
	_, err = f.BankRec.Reconcile(f.admin, f.propID, first.ID)
	must(t, err)
	if _, err = f.BankRec.Reconcile(f.admin, f.propID, second.ID); err != nil {
		t.Fatal(err)
	}
	// reopening: the latest first, with a reason and an approval
	_, err = f.BankRec.Reopen(f.admin, f.propID, first.ID, bankrec.ReopenInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "STATEMENT_NOT_LATEST")
	_, err = f.BankRec.Reopen(f.admin, f.propID, second.ID, bankrec.ReopenInput{Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.BankRec.Reopen(f.admin, f.propID, second.ID, bankrec.ReopenInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	re, err := f.BankRec.Reopen(f.admin, f.propID, second.ID, bankrec.ReopenInput{Reason: "missed a fee", Approval: f.approval()})
	must(t, err)
	if re.Status != "OPEN" || re.ReopenReason != "missed a fee" || re.ReconciledAt != nil {
		t.Fatalf("reopened: %+v", re.Statement)
	}
	_, err = f.BankRec.Reopen(f.admin, f.propID, second.ID, bankrec.ReopenInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "STATEMENT_NOT_RECONCILED")
	// an open statement is deleted with its lines; the first, reconciled, is not
	must(t, f.BankRec.DeleteStatement(f.admin, f.propID, second.ID))
	wantCode(t, f.BankRec.DeleteStatement(f.admin, f.propID, first.ID), "STATEMENT_RECONCILED")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'bank.statement_%'`); n != 6 {
		t.Fatalf("%d statement audit rows", n)
	}
}

func TestBankPermissions(t *testing.T) {
	f := setup(t)
	f.books(t)
	st := f.importFirst(t)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermBankView)
	if _, err := f.BankRec.GetStatement(viewer, f.propID, st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.BankRec.UnclearedLines(viewer, f.propID, st.ID); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"import":    errOf(f.BankRec.ImportStatement(viewer, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID})),
		"clear":     errOf(f.BankRec.Clear(viewer, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: []int64{1}})),
		"adjust":    errOf(f.BankRec.Adjust(viewer, f.propID, st.ID, 1, bankrec.AdjustInput{AccountID: f.acc["6130"]})),
		"reconcile": errOf(f.BankRec.Reconcile(viewer, f.propID, st.ID)),
	} {
		if e := asApp(err); e == nil || e.Code != "PERMISSION_DENIED" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := func() (int, int, error) { _, err := f.BankRec.AutoMatch(viewer, f.propID, st.ID); return 0, 0, err }(); err == nil {
		t.Error("auto match needs bank.reconcile")
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermAccountingView)
	if _, err := f.BankRec.Statements(nobody, f.propID, nil, ""); err == nil {
		t.Error("statements need bank.view")
	}
}

func TestAdjustingIntoAClosedPeriodIsRefused(t *testing.T) {
	f := setup(t)
	st := f.importFirst(t)
	must(t, f.Exec(t, `INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES ($1, $2, '2026-09-01')`, f.tenantID, f.propID))
	_, err := f.BankRec.Adjust(f.admin, f.propID, st.ID, lineOf(t, st, "fee").ID, bankrec.AdjustInput{AccountID: f.acc["6130"]})
	wantCode(t, err, "PERIOD_CLOSED")
	if f.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'BANK'`) != 0 || f.Count(t, `SELECT count(*) FROM bank_clearings`) != 0 {
		t.Fatal("a refused adjustment leaves nothing behind")
	}
}

func TestConcurrentMatchingAndReconcilingCannotDoubleUp(t *testing.T) {
	f := setup(t)
	f.books(t)
	st := f.importFirst(t)
	dep, pay := lineOf(t, st, "guest"), lineOf(t, st, "PLN")
	var depJL int64
	for _, l := range f.uncleared(t, st.ID) {
		if l.Amount.Equal(dec("1000000")) {
			depJL = l.JournalLineID
		}
	}
	// two people clear the same journal line against different statement lines
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := dep.ID
			if i%2 == 1 {
				target = pay.ID
			}
			_, errs[i] = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{StatementLineID: &target, JournalLineIDs: []int64{depJL}})
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if e := asApp(err); e == nil || e.Code != "ALREADY_CLEARED" {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || f.Count(t, `SELECT count(*) FROM bank_clearings`) != 1 {
		t.Fatalf("one clearing wins: %d", won)
	}
	// make the statement ready and reconcile it from several callers: one wins
	_, err := f.BankRec.Unclear(f.admin, f.propID, st.ID, firstClearing(t, f, st.ID))
	must(t, err)
	_, err = f.BankRec.AutoMatch(f.admin, f.propID, st.ID)
	must(t, err)
	_, err = f.BankRec.Adjust(f.admin, f.propID, st.ID, lineOf(t, st, "fee").ID, bankrec.AdjustInput{AccountID: f.acc["6130"]})
	must(t, err)
	recErrs := make([]error, 5)
	for i := range recErrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, recErrs[i] = f.BankRec.Reconcile(f.admin, f.propID, st.ID)
		}()
	}
	wg.Wait()
	won = 0
	for _, err := range recErrs {
		if err == nil {
			won++
		} else if e := asApp(err); e == nil || e.Code != "STATEMENT_RECONCILED" {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("one reconcile wins: %d", won)
	}
}

func firstClearing(t *testing.T, f *fx, statementID int64) int64 {
	t.Helper()
	d, err := f.BankRec.GetStatement(f.admin, f.propID, statementID)
	must(t, err)
	if len(d.Clearings) == 0 {
		t.Fatal("no clearing")
	}
	return d.Clearings[0].ID
}

func TestOnlyOffsettingLinesAreClearedWithoutAStatementLineWithinThePeriod(t *testing.T) {
	f := setup(t)
	f.book(t, "wrong", "4110", "777", true)
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_journals WHERE idempotency_key = 'wrong'`).Scan(&id))
	_, err := f.Accounting.Reverse(f.admin, f.propID, id, accounting.ReverseInput{Reason: "wrong account", Approval: f.approval()})
	must(t, err)
	f.book(t, "other", "4110", "5000", true)
	st, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"), OpeningBalance: dec("0"), ClosingBalance: dec("0"), CSV: "date,amount\n"})
	must(t, err)
	var pair, other []int64
	for _, l := range f.uncleared(t, st.ID) {
		if l.Amount.Abs().Equal(dec("777")) {
			pair = append(pair, l.JournalLineID)
		} else {
			other = append(other, l.JournalLineID)
		}
	}
	if len(pair) != 2 || len(other) != 1 {
		t.Fatalf("lines: %v %v", pair, other)
	}
	if _, err := f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: pair[:1]}); err == nil {
		t.Fatal("one side of a pair does not offset")
	}
	if _, err := f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: other}); err == nil {
		t.Fatal("an item of the period needs its statement line")
	}
	done, err := f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: pair})
	must(t, err)
	eq(t, "the pair cleared nothing", done.Summary.ClearedTotal, "0")
	if len(done.Clearings) != 2 || len(f.uncleared(t, st.ID)) != 1 {
		t.Fatalf("clearings: %+v", done.Clearings)
	}
}
