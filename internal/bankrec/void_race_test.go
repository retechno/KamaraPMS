package bankrec_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/taxfiling"
)

// The races of a void (docs/architecture/40-card-settlement-void.md 7.1 and 5).

// Test 17: several voids of the same settlement at the same time: one wins, the others are told it is voided; one reversal, one audit row, the items released once.
func TestConcurrentVoidsOfASettlementVoidItOnce(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	errs := make([]error, 5)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = g.void(t, g.admin, row.ID, "wrong")
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if e := asApp(err); e == nil || e.Code != "SETTLEMENT_ALREADY_VOIDED" {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("one void wins: %d", won)
	}
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`) != 1 || g.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bank.settlement_voided'`) != 1 ||
		g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE released_at IS NOT NULL`) != 1 || g.Count(t, `SELECT count(*) FROM bank_clearings`) != 2 {
		t.Fatal("one reversal, one audit row, one release, one pair cleared")
	}
	eq(t, "input VAT", g.balanceOf(t, "1425"), "0")
	eq(t, "card clearing", g.balanceOf(t, "1150"), "1000000")
}

// Test 18: a void and a settlement of the same payment on the statement of another bank account at the same time: never two live items, and a loser has a clean error.
func TestAVoidAndANewSettlementOfItsPaymentsNeverSettleTwice(t *testing.T) {
	g := setupGuests(t)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)
	var payment int64
	must(t, g.Pool.QueryRow(context.Background(), `SELECT settled_line_id FROM card_settlement_items`).Scan(&payment))
	payroll, err := g.BankRec.CreateBankAccount(g.admin, g.propID, bankrec.BankAccountInput{AccountID: g.acc["1140"], Name: "BCA payroll", AccountNumber: "987-654"})
	must(t, err)
	other, err := g.BankRec.ImportStatement(g.admin, g.propID, bankrec.ImportInput{
		BankAccountID: payroll.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-10-01"), OpeningBalance: dec("0"), ClosingBalance: dec("977800"),
		CSV: "date,description,reference,amount\n2026-09-30,Card settlement,SETT-1,977800\n",
	})
	must(t, err)
	line := lineOf(t, other, "Card settlement")
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		var voidErr, settleErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			if round == 0 {
				_, voidErr = g.void(t, g.admin, row.ID, "wrong account")
			}
		}()
		go func() {
			defer wg.Done()
			_, settleErr = g.BankRec.Settle(g.admin, g.propID, other.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{payment}, FeeAccountID: g.acc["6130"]})
		}()
		wg.Wait()
		if voidErr != nil {
			t.Fatalf("the void: %v", voidErr)
		}
		if settleErr != nil {
			if e := asApp(settleErr); e == nil || (e.Code != "ALREADY_SETTLED" && e.Code != "LINE_ALREADY_MATCHED") {
				t.Fatalf("round %d, the loser: %v", round, settleErr)
			}
		}
		if n := g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE settled_line_id = $1 AND released_at IS NULL`, payment); n > 1 {
			t.Fatalf("round %d: %d live items", round, n)
		}
	}
	// in the end the payment is settled once more, on the other account
	if g.Count(t, `SELECT count(*) FROM card_settlement_items WHERE settled_line_id = $1 AND released_at IS NULL`, payment) != 1 ||
		g.Count(t, `SELECT count(*) FROM card_settlements WHERE status = 'POSTED' AND bank_account_id = $1`, payroll.ID) != 1 {
		t.Fatal("settled again on the other account")
	}
}

// waitForLockWait waits until another session waits on a lock while running a statement that contains `like` (a pattern of ILIKE).
func (g *guestFx) waitForLockWait(t *testing.T, like string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) //nolint:forbidigo // a test waits on the wall clock for another session
	for {
		if g.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query ILIKE $1`, like) > 0 {
			return
		}
		if time.Now().After(deadline) { //nolint:forbidigo // as above
			t.Fatal("the other transaction never waited")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Test 19: order 3 of section 5. The filing of September reads the settlement while it is still POSTED and is held (the test holds the row of the number sequence of the returns)
// while the void commits; then the filing commits. The return holds the claim of the voided settlement, and October takes it back.
func TestAFilingThatReadTheSettlementBeforeTheVoidCommittedIsReversedByTheNextReturn(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, true)
	g.settleCardOn30Sep(t)
	row := g.lastSettlement(t)

	ctx := context.Background()
	hold, err := g.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = hold.Rollback(ctx) }()
	_, err = hold.Exec(ctx, `SELECT 1 FROM document_sequences WHERE property_id = $1 AND sequence_type = 'TAX_RETURN' FOR UPDATE`, g.propID)
	must(t, err)

	var ret taxfiling.Return
	var fileErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ret, fileErr = g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep")
	}()
	g.waitForLockWait(t, "%document_sequences%") // the worksheet is read: the settlement was POSTED
	_, voidErr := g.void(t, g.admin, row.ID, "wrong")
	must(t, hold.Rollback(ctx)) // the filing goes on, whatever the void did
	<-done
	if voidErr != nil {
		t.Fatalf("the void does not wait for the filing: %v", voidErr)
	}
	must(t, fileErr)

	if len(ret.Input) != 1 || ret.Input[0].SettlementID != row.ID || ret.Input[0].Reversal {
		t.Fatalf("the return holds the claim it read: %+v", ret.Input)
	}
	if g.Count(t, `SELECT count(*) FROM card_settlements WHERE status = 'VOIDED'`) != 1 {
		t.Fatal("the settlement is voided")
	}
	oct, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-10-01"))
	must(t, err)
	if len(oct.Input) != 1 || !oct.Input[0].Reversal || oct.Input[0].SettlementID != row.ID || oct.Input[0].ReversesReturnNumber != ret.Number {
		t.Fatalf("October reverses it: %+v", oct.Input)
	}
	eq(t, "the reversal", oct.Input[0].Amount, "-2200")
	// the input VAT ends at zero: in the books the void took back what the settlement debited (September had no VAT collected to offset it), and the credit September
	// carried forward is used up by the reversal in October
	eq(t, "input VAT", g.balanceOf(t, "1425"), "0")
	eq(t, "credit brought into October", oct.CreditBroughtForward, "2200")
	eq(t, "credit carried out of October", oct.CreditCarriedForward, "0")
}
