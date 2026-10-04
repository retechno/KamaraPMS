package accounting_test

import (
	"testing"

	"kamarapms/internal/platform/auth"
)

// books of 30 Sep 2026, worked out by hand:
//
//	sale on credit 1,000,000; payroll paid 200,000; 300,000 of the receivable collected; equipment bought for 5,000,000;
//	a bank loan of 8,000,000 and capital of 2,000,000 received; depreciation of 100,000 (accumulated depreciation, 1590).
//
// net income 700,000; cash: front desk -4,900,000, bank +10,000,000, so the cash grew by 5,100,000.
func cashFlowBooks(t *testing.T) *hotel {
	h := setupHotel(t)
	h.manual(t, "2026-09-30", "1230", "4110", "1000000", "sale")
	h.manual(t, "2026-09-30", "5110", "1110", "200000", "payroll")
	h.manual(t, "2026-09-30", "1110", "1230", "300000", "collect")
	h.manual(t, "2026-09-30", "1530", "1110", "5000000", "equipment")
	h.manual(t, "2026-09-30", "1130", "2610", "8000000", "loan")
	h.manual(t, "2026-09-30", "1130", "3100", "2000000", "capital")
	h.manual(t, "2026-09-30", "7520", "1590", "100000", "depreciation")
	return h
}

func TestCashFlowIsWorkedOutFromTheJournalsAndProvedAgainstTheCash(t *testing.T) {
	h := cashFlowBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	cf, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "net income", cf.NetIncome, "700000")
	eq(t, "operating: net income + depreciation - the receivable that grew", cf.Operating, "100000")
	eq(t, "investing: the equipment, not the depreciation", cf.Investing, "-5000000")
	eq(t, "financing: the loan and the capital", cf.Financing, "10000000")
	eq(t, "unclassified", cf.Unclassified, "0")
	eq(t, "net change", cf.NetChange, "5100000")
	eq(t, "opening cash", cf.OpeningCash, "0")
	eq(t, "closing cash", cf.ClosingCash, "5100000")
	eq(t, "difference", cf.Difference, "0")
	if !cf.Reconciled {
		t.Fatal("the statement is proved against the cash")
	}
	eq(t, "net income line", lineOf(t, cf.Lines, "NET_INCOME").Amount, "700000")
	eq(t, "depreciation added back", lineOf(t, cf.Lines, "DEPRECIATION").Amount, "100000")
	rec := lineOf(t, cf.Lines, "RECEIVABLES")
	eq(t, "the receivable took cash", rec.Amount, "-700000")
	if len(rec.Accounts) != 1 || rec.Accounts[0].Code != "1230" {
		t.Fatalf("the account that moved: %+v", rec.Accounts)
	}
	eq(t, "fixed assets net of depreciation", lineOf(t, cf.Lines, "FIXED_ASSETS").Amount, "-5000000")
	eq(t, "the loan", lineOf(t, cf.Lines, "LONG_TERM_DEBT").Amount, "8000000")
	eq(t, "the capital", lineOf(t, cf.Lines, "EQUITY").Amount, "2000000")
	eq(t, "operating subtotal", lineOf(t, cf.Lines, "OPERATING").Amount, "100000")
	eq(t, "closing line", lineOf(t, cf.Lines, "CLOSING_CASH").Amount, "5100000")
	for _, l := range cf.Lines {
		if l.Key == "PAYABLES" || l.Key == "INVENTORIES" {
			t.Errorf("a group that did not move is left out: %s", l.Key)
		}
	}
}

func TestCashFlowOfALaterPeriodStartsFromTheCashThatWasThere(t *testing.T) {
	h := cashFlowBooks(t)
	from, to := d("2026-10-01"), d("2026-10-15")
	cf, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "opening cash", cf.OpeningCash, "5100000")
	eq(t, "closing cash", cf.ClosingCash, "5100000")
	eq(t, "net change", cf.NetChange, "0")
	eq(t, "net income", cf.NetIncome, "0")
	if !cf.Reconciled {
		t.Fatal("a quiet period reconciles")
	}
	for _, l := range cf.Lines {
		if l.Kind == "GROUP" && l.Key != "NET_INCOME" && l.Key != "OPENING_CASH" {
			t.Errorf("nothing moved, yet %s is listed", l.Key)
		}
	}
}

func TestCashFlowRefusesTheWrongRangeAndTheWrongRole(t *testing.T) {
	h := cashFlowBooks(t)
	from, to := d("2026-09-30"), d("2026-09-01")
	_, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	wantCode(t, err, "VALIDATION_FAILED")
	nobody := h.User(t, h.tenantID, h.propID, auth.PermBankView)
	_, err = h.Accounting.CashFlow(nobody, h.propID, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestCashFlowDoesNotCountTheClosingOfAYearTwice(t *testing.T) {
	h := cashFlowBooks(t)
	// the year is closed by hand as the closing journal does it: the result moves to retained earnings (a closing journal is flagged)
	from, to := d("2026-09-01"), d("2026-09-30")
	before, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	must(t, err)
	var accID, reID int64
	must(t, h.Pool.QueryRow(t.Context(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4110'`, h.propID).Scan(&accID))
	must(t, h.Pool.QueryRow(t.Context(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '3200'`, h.propID).Scan(&reID))
	must(t, h.Exec(t, `WITH j AS (
		INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, posted_at, is_closing)
		VALUES ($1, $2, 'JVCLOSE1', 'CLOSING', '2026-09-30', 'closing', now(), true) RETURNING id)
		INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
		SELECT $1, $2, j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, $3::bigint, 1000000, 0), (2, $4::bigint, 0, 1000000)) AS v(n, acc, dr, cr)`,
		h.tenantID, h.propID, accID, reID))
	after, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "net income", after.NetIncome, before.NetIncome.String())
	eq(t, "financing", after.Financing, before.Financing.String())
	eq(t, "net change", after.NetChange, before.NetChange.String())
	if !after.Reconciled {
		t.Fatal("still proved against the cash")
	}
}

func TestDirectCashFlowGivesWhatWasReceivedAndPaidAndAgreesWithTheIndirectOne(t *testing.T) {
	h := cashFlowBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	cf, err := h.Accounting.CashFlowDirect(h.admin, h.propID, &from, &to)
	must(t, err)
	if cf.Method != "DIRECT" {
		t.Fatalf("method %s", cf.Method)
	}
	eq(t, "received from guests and customers: the receivable collected", lineOf(t, cf.Lines, "RECEIPTS").Amount, "300000")
	eq(t, "paid to suppliers and for expenses: the payroll", lineOf(t, cf.Lines, "PAYMENTS").Amount, "-200000")
	eq(t, "operating", cf.Operating, "100000")
	eq(t, "the equipment", lineOf(t, cf.Lines, "FIXED_ASSETS").Amount, "-5000000")
	eq(t, "investing", cf.Investing, "-5000000")
	eq(t, "the loan", lineOf(t, cf.Lines, "LONG_TERM_DEBT").Amount, "8000000")
	eq(t, "the capital", lineOf(t, cf.Lines, "EQUITY").Amount, "2000000")
	eq(t, "financing", cf.Financing, "10000000")
	eq(t, "net change", cf.NetChange, "5100000")
	eq(t, "closing cash", cf.ClosingCash, "5100000")
	if !cf.Reconciled {
		t.Fatal("proved against the cash")
	}
	if recv := lineOf(t, cf.Lines, "RECEIPTS"); len(recv.Accounts) != 1 || recv.Accounts[0].Code != "1230" {
		t.Fatalf("the account behind the receipt: %+v", recv.Accounts)
	}
	for _, l := range cf.Lines { // what did not move cash is not there: the sale on credit, the depreciation
		if l.Key == "DEPRECIATION" || l.Key == "NET_INCOME" {
			t.Errorf("the indirect line %s in the direct statement", l.Key)
		}
	}
	ind, err := h.Accounting.CashFlow(h.admin, h.propID, &from, &to)
	must(t, err)
	for what, pair := range map[string][2]string{
		"operating": {cf.Operating.String(), ind.Operating.String()}, "investing": {cf.Investing.String(), ind.Investing.String()},
		"financing": {cf.Financing.String(), ind.Financing.String()}, "net change": {cf.NetChange.String(), ind.NetChange.String()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: direct %s, indirect %s", what, pair[0], pair[1])
		}
	}
}

func TestDirectCashFlowLeavesOutMovesBetweenCashAccountsAndSplitsTaxesAndInterest(t *testing.T) {
	h := cashFlowBooks(t)
	h.manual(t, "2026-09-30", "1130", "1110", "1000000", "bank deposit of cash") // between two cash accounts
	h.manual(t, "2026-09-30", "2420", "1130", "40000", "vat paid")
	h.manual(t, "2026-09-30", "7610", "1130", "25000", "interest paid")
	from, to := d("2026-09-01"), d("2026-09-30")
	cf, err := h.Accounting.CashFlowDirect(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "taxes paid", lineOf(t, cf.Lines, "TAXES_PAID").Amount, "-40000")
	eq(t, "interest paid", lineOf(t, cf.Lines, "INTEREST_PAID").Amount, "-25000")
	eq(t, "the move between cash accounts is no flow: payroll only", lineOf(t, cf.Lines, "PAYMENTS").Amount, "-200000")
	eq(t, "operating", cf.Operating, "35000")
	eq(t, "net change", cf.NetChange, "5035000")
	eq(t, "difference", cf.Difference, "0")
	if !cf.Reconciled {
		t.Fatal("proved against the cash")
	}
	later, laterEnd := d("2026-10-01"), d("2026-10-15")
	quiet, err := h.Accounting.CashFlowDirect(h.admin, h.propID, &later, &laterEnd)
	must(t, err)
	eq(t, "opening", quiet.OpeningCash, "5035000")
	eq(t, "net change", quiet.NetChange, "0")
	if !quiet.Reconciled {
		t.Fatal("a quiet period reconciles")
	}
	nobody := h.User(t, h.tenantID, h.propID, auth.PermBankView)
	_, err = h.Accounting.CashFlowDirect(nobody, h.propID, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
}
