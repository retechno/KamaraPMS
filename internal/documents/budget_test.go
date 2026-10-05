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
	"kamarapms/internal/shifts"
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

func TestTheZReportOfAShiftIsAPDFInBothLanguages(t *testing.T) {
	f := setup(t)
	float := "100000"
	sh, err := f.Shifts.Open(f.admin, f.propID, shifts.OpenInput{Drawer: "MAIN", OpeningFloat: &float})
	must(t, err)
	_, err = f.Shifts.Move(f.admin, f.propID, sh.ID, "d1", shifts.MovementInput{Kind: "DROP", Amount: "30000", Reason: "to the safe"})
	must(t, err)

	x, err := f.Docs.ShiftReportPDF(f.admin, f.propID, sh.ID)
	must(t, err)
	if s := pdfText(t, x); !strings.Contains(s, "X REPORT") || !strings.Contains(s, "Expected cash") || !strings.Contains(s, sh.Number) {
		t.Errorf("the X report of an open shift: %q", s)
	}
	_, err = f.Shifts.Close(f.admin, f.propID, sh.ID, shifts.CloseInput{CountedCash: "70000", Counts: []shifts.Count{{Denomination: "10000", Quantity: 7}}})
	must(t, err)
	z, err := f.Docs.ShiftReportPDF(f.admin, f.propID, sh.ID)
	must(t, err)
	s := pdfText(t, z)
	for _, want := range []string{"Z REPORT", "Cashier shift " + sh.Number, "Opening float", "100,000", "Drops to the safe", "30,000", "Expected cash", "Counted cash", "70,000", "Count by denomination", "Movements of the drawer", "to the safe", "A Z report is final"} {
		if !strings.Contains(s, want) {
			t.Errorf("the Z report lacks %q", want)
		}
	}
	if z.Filename != "z-report-"+sh.Number+".pdf" {
		t.Errorf("file name %q", z.Filename)
	}
	id, err := f.Docs.ShiftReportPDF(documents.WithLang(f.admin, documents.LangID), f.propID, sh.ID)
	must(t, err)
	si := pdfText(t, id)
	for _, want := range []string{"LAPORAN Z", "Shift kasir", "Modal awal", "Kas seharusnya", "Kas dihitung", "Hitungan per pecahan", "Mutasi laci kas", "Laporan Z bersifat final"} {
		if !strings.Contains(si, want) {
			t.Errorf("the Indonesian Z report lacks %q", want)
		}
	}
	_, err = f.Docs.ShiftReportPDF(f.admin, f.propID, 999999)
	roomstest.Want(t, err, "SHIFT_NOT_FOUND")
}
