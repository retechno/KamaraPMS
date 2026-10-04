package documents_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/budget"
	"kamarapms/internal/documents"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

func TestBudgetAgainstActualAsPDF(t *testing.T) {
	f := setup(t)
	byCode := map[string]int64{}
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		byCode[a.Code] = a.ID
	}
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-09-30"), Description: "sale", Lines: []accounting.LineInput{
		{AccountID: byCode["1110"], Debit: decimal.RequireFromString("1500000")}, {AccountID: byCode["4110"], Credit: decimal.RequireFromString("1500000")},
	}}, "a")
	must(t, err)

	year := civil.MustParseDate("2026-01-01")
	b, err := f.Budget.Create(f.admin, f.propID, budget.CreateInput{YearStart: &year, Name: "Plan 2026"})
	must(t, err)
	rev := budget.RowInput{AccountID: byCode["4110"], Amounts: make([]string, 12)}
	for i := range rev.Amounts {
		rev.Amounts[i] = "0"
	}
	rev.Amounts[8] = "1200000"
	_, err = f.Budget.SaveGrid(f.admin, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{rev}})
	must(t, err)

	// Without an active budget there is nothing to print.
	_, err = f.Docs.BudgetVsActualPDF(f.admin, f.propID, budget.VsActualQuery{})
	roomstest.Want(t, err, "NO_ACTIVE_BUDGET")
	_, err = f.Budget.Activate(f.admin, f.propID, b.ID, budget.ActivateInput{Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}})
	must(t, err)

	doc, err := f.Docs.BudgetVsActualPDF(f.admin, f.propID, budget.VsActualQuery{})
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"BUDGET AGAINST ACTUAL", "USALI layout", "FY2026", "Plan 2026", "Room revenue - transient", "1,500,000", "1,200,000", "300,000", "Total revenue", "Net income"} {
		if !strings.Contains(s, want) {
			t.Errorf("the PDF lacks %q", want)
		}
	}
	if doc.Filename != "budget-vs-actual-2026-09-30.pdf" {
		t.Errorf("file name %q", doc.Filename)
	}

	id, err := f.Docs.BudgetVsActualPDF(documents.WithLang(f.admin, documents.LangID), f.propID, budget.VsActualQuery{})
	must(t, err)
	s = pdfText(t, id)
	for _, want := range []string{"ANGGARAN TERHADAP REALISASI", "Tahun fiskal", "Realisasi", "Selisih", "Format USALI"} {
		if !strings.Contains(s, want) {
			t.Errorf("the Indonesian PDF lacks %q", want)
		}
	}
}
