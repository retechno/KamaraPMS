package bankrec_test

import (
	"context"
	"testing"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/taxfiling"
)

// The VAT of a voided settlement (docs/architecture/40-card-settlement-void.md 5).

// Test 11: a settlement voided before any return is never claimed.
func TestASettlementVoidedBeforeTheReturnIsNeverClaimed(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, true)
	g.settleCardOn30Sep(t)
	_, err := g.void(t, g.admin, g.lastSettlement(t).ID, "wrong")
	must(t, err)
	for _, month := range []string{"2026-09-01", "2026-10-01"} {
		w, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d(month))
		must(t, err)
		if len(w.Input) != 0 {
			t.Fatalf("%s: nothing to claim: %+v", month, w.Input)
		}
	}
	ret, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep")
	must(t, err)
	if len(ret.Input) != 0 {
		t.Fatalf("the return claims nothing: %+v", ret.Input)
	}
}

// Test 12: a settlement claimed by a filed return and then voided is taken back by the next return, which names the claim; the settlement made again is claimed as a new one.
func TestTheClaimOfAVoidedSettlementIsReversedByTheNextReturn(t *testing.T) {
	g := setupGuests(t)
	g.pkpFrom(t, "2026-09-30", "CREDITABLE")
	vat := g.vatReturn(t, true)
	st := g.settleCardOn30Sep(t)
	first := g.lastSettlement(t)
	sep, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep")
	must(t, err)
	if len(sep.Input) != 1 || sep.Input[0].SettlementID != first.ID {
		t.Fatalf("September claims the settlement: %+v", sep.Input)
	}
	_, err = g.void(t, g.admin, first.ID, "wrong batch")
	must(t, err)

	oct, err := g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-10-01"))
	must(t, err)
	if len(oct.Input) != 1 {
		t.Fatalf("October takes the claim back: %+v", oct.Input)
	}
	rev := oct.Input[0]
	if rev.Source != taxfiling.ClaimSettlement || !rev.Reversal || rev.SettlementID != first.ID || rev.ReversesPeriod == nil || rev.ReversesPeriod.String() != "2026-09-01" ||
		rev.ReversesReturnNumber != sep.Number || rev.BillNumber != first.JournalNumber {
		t.Fatalf("the reversal names the claim, its month and its return: %+v", rev)
	}
	eq(t, "the reversal", rev.Amount, "-2200")
	eq(t, "input VAT of October", oct.InputClaimed, "-2200")
	// the input VAT ends at zero: in the books the void took back what the settlement debited (September had no VAT collected to offset it), and the credit September
	// carried forward is used up by the reversal in October
	eq(t, "input VAT", g.balanceOf(t, "1425"), "0")
	eq(t, "credit brought into October", oct.CreditBroughtForward, "2200")
	eq(t, "credit carried out of October", oct.CreditCarriedForward, "0")

	// the payments settled again: the new settlement is claimed as a new one, next to the reversal
	line := lineOf(t, st, "Card settlement")
	lines, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CARD")
	must(t, err)
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, line.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{lines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	must(t, err)
	second := g.lastSettlement(t)
	oct, err = g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-10-01"))
	must(t, err)
	if len(oct.Input) != 2 {
		t.Fatalf("October: the reversal and the new claim: %+v", oct.Input)
	}
	eq(t, "input VAT of October", oct.InputClaimed, "0")

	// voiding September releases its claim: the reversal goes (nothing is left to reverse), and September, filed again, claims only the new settlement
	_, err = g.Tax.VoidReturn(g.admin, g.propID, sep.ID, taxfiling.VoidInput{Reason: "refile", Approval: g.approval()})
	must(t, err)
	oct, err = g.Tax.Worksheet(g.admin, g.propID, vat.ID, d("2026-10-01"))
	must(t, err)
	if len(oct.Input) != 1 || oct.Input[0].Reversal || oct.Input[0].SettlementID != second.ID {
		t.Fatalf("no reversal is left, only the new settlement waits for a return: %+v", oct.Input)
	}
	sep2, err := g.Tax.FileReturn(g.admin, g.propID, taxfiling.FileInput{TaxID: vat.ID, PeriodStart: d("2026-09-01")}, "sep2")
	must(t, err)
	if len(sep2.Input) != 1 || sep2.Input[0].SettlementID != second.ID || sep2.Input[0].Reversal {
		t.Fatalf("September claims the new settlement only: %+v", sep2.Input)
	}

	// the database takes the reversing claim of a settlement, and only of the same settlement for the same amount (a transaction that is rolled back)
	ctx := context.Background()
	tx, err := g.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var claimID int64
	must(t, tx.QueryRow(ctx, `SELECT id FROM tax_return_input_claims WHERE return_id = $1 AND settlement_id = $2 AND released_at IS NULL`, sep2.ID, second.ID).Scan(&claimID))
	insert := func(settlement int64, amount string) error {
		_, err := tx.Exec(ctx, `SAVEPOINT s`)
		must(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, settlement_id, amount, reverses_claim_id) VALUES ($1, $2, $3, $4, $5, $6)`,
			g.tenantID, g.propID, sep2.ID, settlement, amount, claimID)
		if err != nil {
			_, rerr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`)
			must(t, rerr)
		}
		return err
	}
	if err := insert(second.ID, "-1"); err == nil {
		t.Error("a reversal is for the amount of the claim")
	}
	if err := insert(first.ID, "-2200"); err == nil {
		t.Error("a reversal is of the same settlement")
	}
	if err := insert(second.ID, "2200"); err == nil {
		t.Error("a reversal is negative")
	}
	if err := insert(second.ID, "-2200"); err != nil {
		t.Errorf("the reversal of a settlement claim is a valid row: %v", err)
	}
}
