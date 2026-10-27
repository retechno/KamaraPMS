package accounting_test

import (
	"context"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// fakeAssets is the register as the accounting sees it: the accounts that only the register posts to.
type fakeAssets struct{ ids []int64 }

func (f fakeAssets) ControlAccountIDs(context.Context, int64, int64) ([]int64, error) {
	return f.ids, nil
}

func fieldCode(err error, field string) string {
	if ae, ok := apperr.As(err); ok {
		for _, fe := range ae.Fields {
			if fe.Field == field {
				return fe.Code
			}
		}
	}
	return ""
}

// The accumulated depreciation accounts of the categories are the register's own (docs/architecture/37-fixed-assets.md F5): a manual journal and a bill line are refused, the cost accounts stay open,
// and the register's own journals (type ASSET, a poster allowed the right) post to them and reverse.
func TestTheAccumulatedDepreciationAccountsBelongToTheRegister(t *testing.T) {
	h := setupHotel(t)
	cost, accum := h.byCode(t, "1530").ID, h.byCode(t, "1592").ID
	line := func(acct int64, debit, credit string) accounting.LineInput {
		return accounting.LineInput{AccountID: acct, Debit: dec(debit), Credit: dec(credit)}
	}
	manual := func(acct int64) accounting.ManualInput {
		return accounting.ManualInput{Date: d("2026-09-30"), Description: "by hand", Lines: []accounting.LineInput{line(h.cashAccount, "0", "100"), line(acct, "100", "0")}}
	}

	// without the register wired nothing is a control account of it
	if _, err := h.Accounting.PostManual(h.admin, h.propID, manual(accum), "a0"); err != nil {
		t.Fatalf("no register: %v", err)
	}

	h.Accounting.SetAssetSource(fakeAssets{ids: []int64{accum}})
	_, err := h.Accounting.PostManual(h.admin, h.propID, manual(accum), "a1")
	if got := fieldCode(err, "lines[1].account_id"); got != "CONTROL_ACCOUNT" {
		t.Fatalf("a manual journal to the accumulated account: %v (%q)", err, got)
	}
	if _, err := h.Accounting.PostManual(h.admin, h.propID, manual(cost), "a2"); err != nil {
		t.Fatalf("the cost account stays open to a manual journal: %v", err)
	}

	p, err := auth.Require(h.admin)
	if err != nil {
		t.Fatal(err)
	}
	must(t, h.TxM.WithinTx(h.admin, func(ctx context.Context) error {
		if _, err := h.Tenancy.RequireOpenBusinessDay(ctx, h.propID, db.ForShare, nil); err != nil {
			return err
		}
		po, err := h.Accounting.BeginPosting(ctx, h.propID)
		if err != nil {
			return err
		}
		// a bill line is refused, a poster that was not given the right is refused, the cost account is fine
		if got := fieldCode(po.CheckAccount(ctx, accum, "lines[0].account_id"), "lines[0].account_id"); got != "CONTROL_ACCOUNT" {
			t.Errorf("a bill line to the accumulated account: %q", got)
		}
		if err := po.CheckAccount(ctx, cost, "lines[0].account_id"); err != nil {
			t.Errorf("the cost account is open to a bill line: %v", err)
		}
		if _, _, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: d("2026-09-30"), Description: "depreciation", Lines: []accounting.SystemLine{
			{AccountID: h.expenseAcct, Debit: dec("100")}, {AccountID: accum, Credit: dec("100")}}}); fieldCode(err, "lines[1].account_id") != "CONTROL_ACCOUNT" {
			t.Errorf("a poster without the right: %v", err)
		}
		// the register's own journal
		po.AllowControl(accounting.KeyFixedAssets)
		id, number, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: d("2026-09-30"), Description: "depreciation", Lines: []accounting.SystemLine{
			{AccountID: h.expenseAcct, Debit: dec("100"), SourceType: "ASSET", SourceRef: "2026-09"}, {AccountID: accum, Credit: dec("100"), SourceType: "ASSET", SourceRef: "2026-09"}}})
		if err != nil {
			return err
		}
		if number == "" {
			t.Error("no journal number")
		}
		// and it reverses, as a reopened month does
		if _, err := po.Reverse(ctx, id, d("2026-09-30"), "month reopened", p.UserID); err != nil {
			return err
		}
		return nil
	}))
	var typ string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT journal_type FROM gl_journals WHERE property_id = $1 AND description = 'depreciation'`, h.propID).Scan(&typ))
	if typ != "ASSET" {
		t.Fatalf("journal type %q", typ)
	}
}
