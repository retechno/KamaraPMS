package documents_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/documents"
	"kamarapms/internal/platform/civil"
)

func TestCashFlowAsPDF(t *testing.T) {
	f := setup(t)
	byCode := map[string]int64{}
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		byCode[a.Code] = a.ID
	}
	post := func(key, debit, credit, amount string) {
		_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-09-30"), Description: key, Lines: []accounting.LineInput{
			{AccountID: byCode[debit], Debit: decimal.RequireFromString(amount)}, {AccountID: byCode[credit], Credit: decimal.RequireFromString(amount)},
		}}, key)
		must(t, err)
	}
	post("a", "1130", "2610", "8000000") // a bank loan
	post("b", "1530", "1130", "3000000") // equipment
	from, to := civil.MustParseDate("2026-09-01"), civil.MustParseDate("2026-09-30")
	doc, err := f.Docs.CashFlowPDF(f.admin, f.propID, &from, &to)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"CASH FLOW STATEMENT", "Indirect method", "Operating activities", "Investing activities", "Financing activities", "8,000,000", "Net change in cash", "5,000,000"} {
		if !strings.Contains(s, want) {
			t.Errorf("the PDF lacks %q", want)
		}
	}
	if strings.Contains(s, "WARNING") {
		t.Error("a reconciled statement carries no warning")
	}
	id, err := f.Docs.CashFlowPDF(documents.WithLang(f.admin, documents.LangID), f.propID, &from, &to)
	must(t, err)
	if s := pdfText(t, id); !strings.Contains(s, "LAPORAN ARUS KAS") || !strings.Contains(s, "Metode tidak langsung") {
		t.Error("the Indonesian PDF")
	}
}

func TestDirectCashFlowAsPDF(t *testing.T) {
	f := setup(t)
	byCode := map[string]int64{}
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		byCode[a.Code] = a.ID
	}
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: civil.MustParseDate("2026-09-30"), Description: "loan", Lines: []accounting.LineInput{
		{AccountID: byCode["1130"], Debit: decimal.RequireFromString("8000000")}, {AccountID: byCode["2610"], Credit: decimal.RequireFromString("8000000")},
	}}, "a")
	must(t, err)
	from, to := civil.MustParseDate("2026-09-01"), civil.MustParseDate("2026-09-30")
	doc, err := f.Docs.CashFlowDirectPDF(f.admin, f.propID, &from, &to)
	must(t, err)
	s := pdfText(t, doc)
	for _, want := range []string{"CASH FLOW STATEMENT", "Direct method", "Loans received and repaid", "8,000,000"} {
		if !strings.Contains(s, want) {
			t.Errorf("the PDF lacks %q", want)
		}
	}
	id, err := f.Docs.CashFlowDirectPDF(documents.WithLang(f.admin, documents.LangID), f.propID, &from, &to)
	must(t, err)
	if !strings.Contains(pdfText(t, id), "Metode langsung") {
		t.Error("the Indonesian PDF")
	}
}
