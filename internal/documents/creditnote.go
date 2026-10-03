package documents

import (
	"context"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/platform/apperr"
)

// CreditNotePDF is a credit note to a company: what was taken off, by line, with the tax, against the invoice or the transfer it corrects. A void
// credit note is stamped. A write-off is internal and has no document (cityledger.read).
func (s *Service) CreditNotePDF(ctx context.Context, propertyID, adjustmentID int64) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	adj, err := s.ledger.GetAdjustment(ctx, propertyID, adjustmentID)
	if err != nil {
		return Document{}, err
	}
	if adj.Kind != cityledger.KindCreditNote {
		return Document{}, apperr.Conflict("NOT_A_CREDIT_NOTE", "only a credit note has a document")
	}
	co, err := s.cos.Get(ctx, propertyID, adj.CompanyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v string) string { return dc.lang.Money(dec(v), dc.decimals) }
	var rows [][]string
	for _, l := range adj.Lines {
		rate := ""
		if l.TaxRate != nil {
			rate = rateLabel(dec(*l.TaxRate))
		}
		rows = append(rows, []string{l.Description, m(l.NetAmount), rate, m(l.TaxAmount), m(l.Total)})
	}
	rows = append(rows, []string{"Total", "", "", "", m(adj.Amount)})
	against := "Invoice " + adj.InvoiceNumber
	if adj.InvoiceNumber == "" {
		against = "Transfer " + adj.PaymentNumber
	}
	pairs := [][2]string{
		{"Credit to", co.Name}, {"Address", co.Address + " " + co.City}, {"Tax ID", co.TaxID},
		{"Date", dc.lang.Date(adj.BusinessDate)}, {"Against", against}, {"Reason", adj.Reason}, {"Currency", dc.prop.CurrencyCode},
	}
	var notes []string
	if adj.Status == cityledger.AdjustmentVoided {
		notes = append(notes, "VOID: "+adj.VoidReason)
	}
	d := FinancialDoc{
		Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Title: "CREDIT NOTE", Subtitle: adj.Number, Pairs: pairs,
		Cols: []col{{80, "Description", "L"}, {32, "Net amount", "R"}, {18, "Tax rate", "R"}, {28, "Tax", "R"}, {32, "Total", "R"}},
		Rows: rows, Bold: map[int]bool{len(rows) - 1: true}, Notes: notes,
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "credit-note-" + adj.Number + ".pdf", PDF: pdf}, err
}
