package documents_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

func TestFinancialStatementsAsPDF(t *testing.T) {
	f := setup(t)
	byCode := map[string]int64{}
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		byCode[a.Code] = a.ID
	}
	post := func(key, debit, credit, amount string) {
		_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-09-30"), Description: "manual " + key, Lines: []accounting.LineInput{
			{AccountID: byCode[debit], Debit: decimal.RequireFromString(amount)}, {AccountID: byCode[credit], Credit: decimal.RequireFromString(amount)},
		}}, key)
		must(t, err)
	}
	post("a", "1110", "4110", "1500000") // cash sale of rooms
	post("b", "6110", "1110", "250000")  // payroll
	from, to := civil.MustParseDate("2026-09-01"), civil.MustParseDate("2026-09-30")

	is, err := f.Docs.IncomeStatementPDF(f.admin, f.propID, &from, &to)
	must(t, err)
	s := pdfText(t, is)
	for _, want := range []string{"INCOME STATEMENT", "USALI layout", "Room revenue - transient", "1,500,000", "Gross operating profit", "1,250,000", "Net income", "Administrative and general"} {
		if !strings.Contains(s, want) {
			t.Errorf("income statement lacks %q", want)
		}
	}
	if is.Filename != "income-statement-2026-09-30.pdf" {
		t.Errorf("file name %q", is.Filename)
	}

	bs, err := f.Docs.BalanceSheetPDF(f.admin, f.propID, &to)
	must(t, err)
	s = pdfText(t, bs)
	for _, want := range []string{"BALANCE SHEET", "Total assets", "1,250,000", "Earnings to date", "no year-end closing", "Total liabilities and equity"} {
		if !strings.Contains(s, want) {
			t.Errorf("balance sheet lacks %q", want)
		}
	}
	if strings.Contains(s, "out of balance") {
		t.Error("balanced books carry no warning")
	}

	tb, err := f.Docs.TrialBalancePDF(f.admin, f.propID, &from, &to)
	must(t, err)
	s = pdfText(t, tb)
	for _, want := range []string{"TRIAL BALANCE", "1110 Cash on hand", "1,750,000", "Total"} {
		if !strings.Contains(s, want) {
			t.Errorf("trial balance lacks %q", want)
		}
	}

	gl, err := f.Docs.LedgerPDF(f.admin, f.propID, byCode["1110"], &from, &to)
	must(t, err)
	s = pdfText(t, gl)
	for _, want := range []string{"GENERAL LEDGER", "1110 Cash on hand", "JV000001", "Opening balance", "Closing balance", "1,250,000"} {
		if !strings.Contains(s, want) {
			t.Errorf("ledger lacks %q", want)
		}
	}

	// the permission is the one of the report itself
	desk := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Docs.TrialBalancePDF(desk, f.propID, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Docs.LedgerPDF(f.admin, f.propID, 999999, nil, nil)
	wantCode(t, err, "ACCOUNT_NOT_FOUND")
	back := civil.MustParseDate("2026-09-01")
	_, err = f.Docs.IncomeStatementPDF(f.admin, f.propID, &to, &back)
	wantCode(t, err, "VALIDATION_FAILED")
}
