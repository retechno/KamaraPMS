package accounting_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

// Transfers between folios in the books (docs/architecture/30-group-folio.md, part B): they net to zero in every account and department of the day journal, the account of the system key
// FOLIO_TRANSFER comes to zero, and the reconciliation of the control accounts still agrees.
func TestTransfersBetweenFoliosNetToZeroInTheJournalAndTheReconciliation(t *testing.T) {
	h := setupHotel(t)
	a := h.book(t, "2026-09-30", "2026-10-02")
	b := h.book(t, "2026-09-30", "2026-10-02")
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	sa, sb := h.checkIn(t, a, h.r101), h.checkIn(t, b, h.r102)
	if sa.Folio.ID == sb.Folio.ID {
		t.Fatalf("two stays, one folio: %d %d (stays %d %d)", sa.Folio.ID, sb.Folio.ID, sa.Stay.ID, sb.Stay.ID)
	}
	h.charge(t, sa.Folio.ID, "MINIBAR", "1000000", "k1")
	h.charge(t, sa.Folio.ID, "LAUNDRY", "200000", "k2")

	// the books as they would be with no transfer: the day closed on a copy is not possible, so what must not change is checked after
	var minibar, laundry int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $2 AND code = 'MINIBAR')`, sa.Folio.ID, h.propID).Scan(&minibar))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $2 AND code = 'LAUNDRY')`, sa.Folio.ID, h.propID).Scan(&laundry))
	revenueBefore := h.balance(t, "4230")

	// charges to the folio of another reservation (a folio of no relation: ANY), then a balance back, and a return
	m, err := h.Folios.TransferItems(h.admin, h.propID, sa.Folio.ID, folios.TransferItemsInput{FolioID: sb.Folio.ID, ItemIDs: []int64{minibar}, Reason: "the other guest pays the minibar", Approval: h.approval()})
	must(t, err)
	if m.Scope != "ANY" {
		t.Fatalf("scope: %s", m.Scope)
	}
	bal, err := h.Folios.TransferBalance(h.admin, h.propID, sb.Folio.ID, "tb", folios.TransferBalanceInput{FolioID: sa.Folio.ID, Amount: "400000", Reason: "the first guest pays part of it", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.ReturnTransfers(h.admin, h.propID, sa.Folio.ID, folios.ReturnInput{TransferIDs: []int64{bal.Moved[0].TransferID}, To: "PREVIOUS", Reason: "no: put it back", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.TransferItems(h.admin, h.propID, sa.Folio.ID, folios.TransferItemsInput{FolioID: sb.Folio.ID, ItemIDs: []int64{laundry}, Reason: "the laundry too", Approval: h.approval()})
	must(t, err)

	// the readiness of the night audit finds the account at zero
	rd, err := h.Accounting.Readiness(h.admin, h.propID)
	must(t, err)
	for _, bl := range rd.Blockers {
		if bl.Code == accounting.BlockTransferAccount {
			t.Fatalf("the account of the transfers is zero: %+v", bl)
		}
	}
	h.closeDay(t)

	// the journal: the account of the transfers has no line and a zero balance; the revenue is what was charged, once; the books balance
	if got := h.balance(t, "1215"); !got.IsZero() {
		t.Fatalf("1215 comes to %s at the day close", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE l.property_id = $1 AND a.code = '1215'`, h.propID); n != 0 {
		t.Fatalf("%d lines on 1215: the transfers net to zero and leave no line", n)
	}
	if got := h.balance(t, "4230"); !got.Equal(revenueBefore.Add(dec("-1000000"))) {
		t.Fatalf("the minibar revenue is posted once: %s (before %s)", got, revenueBefore)
	}
	if got := h.balance(t, "4290"); got.IsPositive() {
		t.Logf("other revenue %s", got)
	}
	h.requireBalanced(t)
	// per department too: the lines of the day, grouped by account and department, are those the charges alone would make
	var bad int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT a.code, l.department_id FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.account_type = 'REVENUE' GROUP BY a.code, l.department_id HAVING sum(l.credit - l.debit) < 0) x`, h.propID).Scan(&bad))
	if bad != 0 {
		t.Fatalf("%d revenue lines are negative", bad)
	}

	// the control accounts still agree with the folios
	asOf := civil.MustParseDate("2026-09-30")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("the reconciliation must agree after the transfers: %+v", rec)
	}
	// the transfers are not revenue in any report
	var net string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(net_amount), 0)::text FROM folio_items i JOIN charge_codes c ON c.id = i.charge_code_id WHERE c.charge_type = 'TRANSFER'`).Scan(&net))
	if net != "0.000" && net != "0.00" && net != "0" {
		t.Fatalf("the lines of the code TRF come to %s", net)
	}

	// a manual journal into the account breaks the rule, and the night audit is not ready until it is put right
	var cash, transfer int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT account_id FROM gl_account_map WHERE property_id = $1 AND map_key = 'CASH'`, h.propID).Scan(&cash))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT account_id FROM gl_account_map WHERE property_id = $1 AND map_key = 'FOLIO_TRANSFER'`, h.propID).Scan(&transfer))
	_, err = h.Accounting.PostManual(h.admin, h.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-10-01"), Description: "by hand", Reference: "X", Lines: []accounting.LineInput{
		{AccountID: transfer, Debit: dec("5000")}, {AccountID: cash, Credit: dec("5000")}}}, "mx")
	must(t, err)
	rd, err = h.Accounting.Readiness(h.admin, h.propID)
	must(t, err)
	found := false
	for _, bl := range rd.Blockers {
		if bl.Code == accounting.BlockTransferAccount && bl.Ref == accounting.KeyFolioTransfer {
			found = true
		}
	}
	if !found || rd.Ready {
		t.Fatalf("a balance on the account of the transfers stops the night audit: %+v", rd)
	}
}

// Review of part B, point 12: the charges are posted and closed on day 1; on day 2 they are transferred by item and by balance and a transfer is returned. The lines of the code TRF are
// really posted to the account of the key (1215), both ways and in the same amount, and the day-2 journal comes to nothing for them: no line on 1215, no revenue moved, every account and
// department as the one new charge of the day alone would make it, and the reconciliation of the control accounts agrees.
func TestTransfersOfADayAfterTheChargesWereClosedNetToZeroInTheSecondDayJournal(t *testing.T) {
	h := setupHotel(t)
	a := h.book(t, "2026-09-30", "2026-10-03")
	b := h.book(t, "2026-09-30", "2026-10-03")
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	sa, sb := h.checkIn(t, a, h.r101), h.checkIn(t, b, h.r102)
	h.charge(t, sa.Folio.ID, "MINIBAR", "1000000", "d1a")
	h.charge(t, sa.Folio.ID, "LAUNDRY", "200000", "d1b")
	h.closeDay(t) // day 1 (2026-09-30) is closed with the charges in the books
	revenue1 := h.balance(t, "4230")
	laundry1 := h.balance(t, "4350")
	if !revenue1.IsNegative() || !laundry1.IsNegative() {
		t.Fatalf("the charges of day 1 are in the books: minibar %s laundry %s", revenue1, laundry1)
	}
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	if day.BusinessDate.String() != "2026-10-01" {
		t.Fatalf("the open day is %s", day.BusinessDate)
	}

	// day 2: one charge of its own, then the transfers
	h.charge(t, sb.Folio.ID, "MINIBAR", "50000", "d2")
	var own string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT net_amount::text FROM folio_items WHERE folio_id = $1 AND business_date = '2026-10-01' AND description <> '' AND transaction_type = 'CHARGE'
		AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $2 AND code = 'MINIBAR')`, sb.Folio.ID, h.propID).Scan(&own))
	var minibar, laundry int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $2 AND code = 'MINIBAR')`, sa.Folio.ID, h.propID).Scan(&minibar))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $2 AND code = 'LAUNDRY')`, sa.Folio.ID, h.propID).Scan(&laundry))
	_, err = h.Folios.TransferItems(h.admin, h.propID, sa.Folio.ID, folios.TransferItemsInput{FolioID: sb.Folio.ID, ItemIDs: []int64{minibar}, Reason: "the other guest pays the minibar", Approval: h.approval()})
	must(t, err)
	bal, err := h.Folios.TransferBalance(h.admin, h.propID, sb.Folio.ID, "tb", folios.TransferBalanceInput{FolioID: sa.Folio.ID, Amount: "400000", Reason: "the first guest pays part of it", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.ReturnTransfers(h.admin, h.propID, sa.Folio.ID, folios.ReturnInput{TransferIDs: []int64{bal.Moved[0].TransferID}, To: "PREVIOUS", Reason: "no: put it back", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.TransferItems(h.admin, h.propID, sa.Folio.ID, folios.TransferItemsInput{FolioID: sb.Folio.ID, ItemIDs: []int64{laundry}, Reason: "the laundry too", Approval: h.approval()})
	must(t, err)

	// the lines of TRF are posted on day 2, to 1215, as many credits as debits, and they are more than nothing: they are in the ledger, not left out of it
	var lines int
	var dr, cr string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(i.debit), 0)::text, COALESCE(sum(i.credit), 0)::text FROM folio_items i JOIN charge_codes c ON c.id = i.charge_code_id
		WHERE i.property_id = $1 AND c.charge_type = 'TRANSFER' AND i.business_date = '2026-10-01' AND i.revenue_account_code = '1215'`, h.propID).Scan(&lines, &dr, &cr))
	if lines != 4 || dr != "800000.000" || cr != "800000.000" {
		t.Fatalf("TRF lines on 1215: %d lines, debit %s credit %s (two transfers of 400,000 and the return)", lines, dr, cr)
	}
	// the moved charges are in the ledger of day 2 as well: a reversal and a copy of the minibar and of the laundry, in the same amounts
	if n := h.Count(t, `SELECT count(*) FROM folio_items i JOIN folio_transfers x ON x.out_item_id = i.id OR x.in_item_id = i.id JOIN charge_codes c ON c.id = i.charge_code_id
		WHERE i.property_id = $1 AND i.business_date = '2026-10-01' AND c.charge_type <> 'TRANSFER'`, h.propID); n != 4 {
		t.Fatalf("%d reversals and copies of moved charges on day 2, want 4", n)
	}
	h.closeDay(t) // day 2

	// the day-2 journal: nothing on 1215, and only the one new charge in the revenue accounts
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE l.property_id = $1 AND a.code = '1215'`, h.propID); n != 0 {
		t.Fatalf("%d journal lines on 1215", n)
	}
	if got := h.balance(t, "1215"); !got.IsZero() {
		t.Fatalf("1215 comes to %s", got)
	}
	rows, err := h.Pool.Query(context.Background(), `SELECT a.code, COALESCE(dp.code, ''), sum(l.credit - l.debit)::text FROM gl_journal_lines l
		JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
		JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		LEFT JOIN departments dp ON dp.property_id = l.property_id AND dp.id = l.department_id
		WHERE l.property_id = $1 AND j.journal_type = 'DAY_CLOSE' AND j.journal_date = '2026-10-01' AND a.account_type = 'REVENUE' AND a.code <> '4110'
		GROUP BY 1, 2 HAVING sum(l.credit - l.debit) <> 0 ORDER BY 1, 2`, h.propID)
	must(t, err)
	var got []string
	for rows.Next() {
		var code, dept, net string
		must(t, rows.Scan(&code, &dept, &net))
		got = append(got, code+"|"+dept+"|"+decimal.RequireFromString(net).String())
	}
	rows.Close()
	want := "4230|FB|" + decimal.RequireFromString(own).String()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the revenue of day 2 other than rooms is the new charge alone: got %v, want [%s]", got, want)
	}
	// and over both days: the minibar and the laundry were charged once
	if got := h.balance(t, "4230"); !got.Equal(revenue1.Sub(decimal.RequireFromString(own))) {
		t.Fatalf("minibar revenue %s, after day 1 %s and a charge of %s", got, revenue1, own)
	}
	if got := h.balance(t, "4350"); !got.Equal(laundry1) {
		t.Fatalf("laundry revenue %s moved from %s", got, laundry1)
	}
	h.requireBalanced(t)
	asOf := civil.MustParseDate("2026-10-01")
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("the reconciliation agrees after the transfers of day 2: %+v", rec)
	}
}
