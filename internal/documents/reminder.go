package documents

import (
	"context"
	"strconv"

	"github.com/shopspring/decimal"
)

var reminderTitles = map[int]string{1: "First reminder", 2: "Second reminder", 3: "Final reminder"}

// ReminderPDF is the letter of a payment reminder to a company: the invoices it listed with what each owed the day it was recorded, how late it was and
// the interest if the property charges one (cityledger.read).
func (s *Service) ReminderPDF(ctx context.Context, propertyID, reminderID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	rm, err := s.ledger.GetReminder(ctx, propertyID, reminderID)
	if err != nil {
		return Document{}, err
	}
	co, err := s.cos.Get(ctx, propertyID, rm.CompanyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v string) string { return dc.lang.Money(dec(v), dc.decimals) }
	withInterest := decimal.RequireFromString(rm.TotalInterest).IsPositive()
	cols := []col{{34, "Invoice", "L"}, {30, "Invoice date", "L"}, {30, "Due date", "L"}, {24, "Days late", "R"}, {38, "Outstanding", "R"}}
	var rows [][]string
	for _, it := range rm.Items {
		row := []string{it.InvoiceNumber, dc.lang.Date(it.InvoiceDate), dc.lang.Date(it.DueDate), strconv.Itoa(it.DaysOverdue), m(it.Outstanding)}
		if withInterest {
			row = append(row, m(it.Interest))
		}
		rows = append(rows, row)
	}
	total := []string{"Total", "", "", "", m(rm.TotalOutstanding)}
	if withInterest {
		cols = []col{{30, "Invoice", "L"}, {26, "Invoice date", "L"}, {26, "Due date", "L"}, {20, "Days late", "R"}, {34, "Outstanding", "R"}, {30, "Interest", "R"}}
		total = append(total, m(rm.TotalInterest))
	}
	rows = append(rows, total)
	pairs := [][2]string{
		{"To", co.Name}, {"Address", co.Address + " " + co.City}, {"Tax ID", co.TaxID}, {"Date", dc.lang.Date(rm.Date)}, {"Currency", dc.prop.CurrencyCode},
	}
	notes := []string{"Our records show that the invoices above are past their due date. Please arrange payment, or contact us if you have already paid or if you have a question about an invoice."}
	if rm.Note != "" {
		notes = append(notes, rm.Note)
	}
	d := FinancialDoc{
		Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Title: "PAYMENT REMINDER", Subtitle: reminderTitles[rm.Level] + " " + rm.Number, Pairs: pairs,
		Cols: cols, Rows: rows, Bold: map[int]bool{len(rows) - 1: true}, Notes: notes,
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "reminder-" + rm.Number + ".pdf", PDF: pdf}, err
}
