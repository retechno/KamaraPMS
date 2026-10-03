package documents

import (
	"context"
	"net/http"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/taxinvoice"
)

// TaxInvoicePDF is a tax invoice (faktur pajak) as it was issued: the seller and the buyer with their tax numbers, the VAT by charge, the
// signer of the hotel and the official number once the tax authority has given one. A void invoice says so (tax.view).
func (s *Service) TaxInvoicePDF(ctx context.Context, propertyID, invoiceID int64) (Document, error) {
	inv, err := s.taxInv.GetInvoice(ctx, propertyID, invoiceID)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v decimal.Decimal) string { return dc.lang.Money(v, dc.decimals) }
	var rows [][]string
	for _, l := range inv.Lines {
		rows = append(rows, []string{l.ChargeCode, l.Description, rateLabel(l.Rate), m(l.Base), m(l.VAT)})
	}
	rows = append(rows, []string{"", "Total", "", m(inv.TaxableBase), m(inv.VATAmount)})
	source := inv.CityLedgerInvoiceNumber
	if inv.SourceType == taxinvoice.SourceFolio {
		source = inv.FolioNumber
	}
	pairs := [][2]string{
		{"Seller", inv.Seller.Name}, {"Seller NPWP", inv.Seller.NPWP}, {"PKP number", inv.Seller.PKPNumber}, {"Seller address", inv.Seller.Address},
		{"Buyer", inv.Buyer.Name}, {"Buyer NPWP / NIK", inv.Buyer.NPWP}, {"Buyer address", inv.Buyer.Address},
		{"Date", dc.lang.Date(inv.IssueDate)}, {"For", source}, {"Official number", inv.DJPNumber}, {"Currency", dc.prop.CurrencyCode},
	}
	var notes []string
	if inv.Status == taxinvoice.StatusVoided {
		notes = append(notes, "VOID: "+inv.VoidReason)
	}
	if inv.Seller.SignerName != "" {
		sign := "Signed for the seller: " + inv.Seller.SignerName
		if inv.Seller.SignerTitle != "" {
			sign += ", " + inv.Seller.SignerTitle
		}
		notes = append(notes, sign)
	}
	d := FinancialDoc{
		Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Title: "TAX INVOICE (FAKTUR PAJAK)", Subtitle: inv.Ref, Pairs: pairs,
		Cols: []col{{24, "Charge code", "L"}, {70, "Description", "L"}, {20, "Rate", "R"}, {38, "Taxable base (DPP)", "R"}, {38, "VAT", "R"}},
		Rows: rows, Bold: map[int]bool{len(rows) - 1: true}, Notes: notes,
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "tax-invoice-" + inv.Ref + ".pdf", PDF: pdf}, err
}

func (h *Handler) registerTaxInvoice(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/tax/invoices/{id}/invoice.pdf", httpx.HandlerFunc(h.serve(h.svc.TaxInvoicePDF)))
}
