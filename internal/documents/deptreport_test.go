package documents_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/departments"
	"kamarapms/internal/documents"
	"kamarapms/internal/platform/civil"
)

func TestTheDepartmentReportAsPDF(t *testing.T) {
	f := setup(t)
	byCode := map[string]int64{}
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		byCode[a.Code] = a.ID
	}
	var fb int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = 'FB'`, f.propID).Scan(&fb))
	rest, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "REST", Name: "Restaurant", ParentID: &fb})
	must(t, err)
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-09-30"), Description: "sale", Lines: []accounting.LineInput{
		{AccountID: byCode["1110"], Debit: decimal.RequireFromString("300000")},
		{AccountID: byCode["4210"], Credit: decimal.RequireFromString("250000"), DepartmentID: &rest.ID},
		{AccountID: byCode["4590"], Credit: decimal.RequireFromString("50000")},
	}}, "a")
	must(t, err)
	from, to := civil.MustParseDate("2026-09-01"), civil.MustParseDate("2026-09-30")
	doc, err := f.Docs.DepartmentReportPDF(f.admin, f.propID, &from, &to, nil)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"DEPARTMENT REPORT", "FB Food and beverage", "REST Restaurant", "250,000", "300,000", "Total"} {
		if !strings.Contains(s, want) {
			t.Errorf("the PDF lacks %q", want)
		}
	}
	if doc.Filename != "department-report-2026-09-30.pdf" {
		t.Errorf("file name %q", doc.Filename)
	}
	id, err := f.Docs.DepartmentReportPDF(documents.WithLang(f.admin, documents.LangID), f.propID, &from, &to, nil)
	must(t, err)
	s = pdfText(t, id)
	for _, want := range []string{"LAPORAN DEPARTEMEN", "Pendapatan", "Tanpa departemen"} {
		if !strings.Contains(s, want) {
			t.Errorf("the Indonesian PDF lacks %q", want)
		}
	}
}
