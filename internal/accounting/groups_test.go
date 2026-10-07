package accounting_test

import (
	"context"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/civil"
)

// Transaction groups (docs/architecture/20-transaction-group.md) are a presentation dimension of a folio. The general ledger and the night audit must not know about them.
func TestMovingLinesBetweenTransactionGroupsDoesNotTouchTheGeneralLedger(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.charge(t, st.Folio.ID, "LAUNDRY", "40000", "l1")
	_, err := h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "60000", PaymentMethod: "CASH"})
	must(t, err)
	h.closeDay(t) // day 1 is journaled, and its folio is still open

	gl := func() [3]string {
		var j, l, d string
		must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(md5(string_agg(j::text, '' ORDER BY j.id)), '-') FROM gl_journals j WHERE j.property_id = $1`, h.propID).Scan(&j))
		must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(md5(string_agg(l::text, '' ORDER BY l.id)), '-') FROM gl_journal_lines l WHERE l.property_id = $1`, h.propID).Scan(&l))
		must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(md5(string_agg(p::text, '' ORDER BY p.id)), '-') FROM gl_day_posts p WHERE p.property_id = $1`, h.propID).Scan(&d))
		return [3]string{j, l, d}
	}
	before := gl()
	journals := h.Count(t, `SELECT count(*) FROM gl_journals`)
	if journals != 1 {
		t.Fatalf("one journal for day 1: %d", journals)
	}

	// every line of the folio goes to another group
	fo, err := h.Folios.GetFolio(h.admin, h.propID, st.Folio.ID)
	must(t, err)
	to := []string{"B", "C", "D", "B", "C", "D"}
	moved := 0
	for i, it := range fo.Items {
		if it.TransactionType == "REVERSAL" {
			continue
		}
		_, err := h.Folios.SetItemGroup(h.admin, h.propID, it.ID, folios.GroupInput{GroupCode: to[i%len(to)]})
		must(t, err)
		moved++
	}
	if moved < 3 {
		t.Fatalf("moved %d lines", moved)
	}
	if after := gl(); after != before || h.Count(t, `SELECT count(*) FROM gl_journals`) != journals {
		t.Fatalf("moving lines changed the general ledger:\nbefore %v\nafter  %v", before, gl())
	}
	asOf := civil.MustParseDate("2026-09-30")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled || rec.PendingDays != 0 {
		t.Fatalf("the guest ledger no longer agrees with the folios: %+v", rec)
	}
	// the backfill finds nothing to do: no day lost or gained a journal
	n, err := h.Accounting.PostPending(h.admin, h.propID)
	must(t, err)
	if n != 0 {
		t.Fatalf("backfill posted %d days", n)
	}

	// the next night audit runs as it always does: its journal is made from the ledger of its own day, and day 1 is not touched
	h.charge(t, st.Folio.ID, "RESTAURANT", "25000", "r2")
	h.closeDay(t)
	if h.Count(t, `SELECT count(*) FROM gl_journals`) != 2 {
		t.Fatal("one journal for each closed day")
	}
	h.requireBalanced(t)
	if got, want := h.balance(t, "1210"), h.folioBalance(t, st.Folio.ID); !got.Equal(want) {
		t.Fatalf("the guest ledger %s is not the folio balance %s", got, want)
	}
	// and the groups survived the night audit
	fo, err = h.Folios.GetFolio(h.admin, h.propID, st.Folio.ID)
	must(t, err)
	groups := map[string]int{}
	for _, it := range fo.Items {
		groups[it.GroupCode]++
	}
	if groups["B"] == 0 || groups["C"] == 0 {
		t.Fatalf("the groups after the night audit: %v", groups)
	}
}
