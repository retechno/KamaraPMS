package documents

import (
	"context"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/tenancy"
)

func rateLabel(r decimal.Decimal) string { return r.StringFixed(2) + "%" }

// taxDoc draws the worksheet of a month of a tax: the base and the tax by charge code and rate, with the figures of the
// filing and the payments when it has been filed.
func (s *Service) taxDoc(ctx context.Context, propertyID int64, title string, prof taxfiling.Profile, start, end, due civil.Date, lines []taxfiling.WorksheetLine, base, tax decimal.Decimal, off taxfiling.VATOffset, input []taxfiling.InputClaim, ret *taxfiling.Return, filename string) (Document, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v decimal.Decimal) string { return dc.lang.Money(v, dc.decimals) }
	var rows [][]string
	for _, l := range lines {
		rows = append(rows, []string{l.ChargeCode, l.ChargeName, rateLabel(l.Rate), strconv.Itoa(l.Items), m(l.Base), m(l.Tax)})
	}
	rows = append(rows, []string{"", "Total", "", "", m(base), m(tax)})
	pairs := [][2]string{
		{"Tax", prof.TaxName}, {"Authority", prof.Authority}, {"Registration number", prof.RegistrationNumber}, {"Currency", dc.prop.CurrencyCode},
		{"Period", dc.lang.Date(start) + " - " + dc.lang.Date(end)}, {"Due", dc.lang.Date(due)},
	}
	subtitle := "Worksheet (not filed)"
	var notes []string
	// the input VAT claimed against the tax collected, and the credit carried from month to month (a return of the VAT tax)
	if len(input) > 0 || !off.CreditBroughtForward.IsZero() || !off.CreditCarriedForward.IsZero() {
		for _, c := range input {
			label := "Input VAT: " + c.BillNumber + " " + c.SupplierName + " (" + c.SupplierInvoiceNumber + ")"
			switch {
			case c.Source == taxfiling.ClaimSettlement:
				label = "Input VAT on card commission: " + c.BillNumber + " " + c.SupplierName + " (" + c.SupplierInvoiceNumber + ")"
			case c.Source == taxfiling.ClaimCreditNote && c.Reversal:
				label = "Input VAT of a credit note given back: " + c.BillNumber + " " + c.SupplierName + " (" + c.SupplierInvoiceNumber + ")"
			case c.Source == taxfiling.ClaimCreditNote:
				label = "Input VAT taken back by a credit note: " + c.BillNumber + " " + c.SupplierName + " (" + c.SupplierInvoiceNumber + ")"
			case c.Reversal:
				label = "Input VAT taken back: " + c.BillNumber + " " + c.SupplierName + " (" + c.SupplierInvoiceNumber + ")"
			}
			notes = append(notes, label+" "+m(c.Amount))
		}
		notes = append(notes, "Input VAT claimed: "+m(off.InputClaimed), "Credit brought forward: "+m(off.CreditBroughtForward), "Offset against the tax: "+m(off.Offset),
			"Payable: "+m(off.Payable), "Credit carried forward: "+m(off.CreditCarriedForward))
	}
	if ret != nil {
		subtitle = ret.Number
		pairs = append(pairs, [2]string{"Filed on", dc.lang.Date(ret.FiledOn)}, [2]string{"Filing reference", ret.FilingReference})
		if ret.Status == taxfiling.ReturnVoided {
			notes = append(notes, "VOIDED: "+ret.VoidReason)
		}
		for _, p := range ret.Payments {
			if p.Status != "POSTED" {
				continue
			}
			line := "Paid " + m(p.Amount) + " on " + dc.lang.Date(p.PaymentDate) + " (" + p.PaymentMethod + ")"
			if p.ReferenceNumber != "" {
				line += ", reference " + p.ReferenceNumber
			}
			if p.Penalty.IsPositive() {
				line += ", penalty " + m(p.Penalty)
			}
			notes = append(notes, line)
		}
		notes = append(notes, "Outstanding: "+m(ret.Outstanding))
	}
	d := FinancialDoc{
		Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Title: title, Subtitle: subtitle, Pairs: pairs,
		Cols: []col{{24, "Charge code", "L"}, {56, "Charged as", "L"}, {20, "Rate", "R"}, {16, "Items", "R"}, {32, "Taxable base", "R"}, {32, "Tax", "R"}},
		Rows: rows, Bold: map[int]bool{len(rows) - 1: true}, Notes: notes,
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: filename, PDF: pdf}, err
}

// TaxReturnPDF is a tax return as it was filed, with its payments (tax.view).
func (s *Service) TaxReturnPDF(ctx context.Context, propertyID, returnID int64) (Document, error) {
	ret, err := s.tax.GetReturn(ctx, propertyID, returnID)
	if err != nil {
		return Document{}, err
	}
	profiles, err := s.tax.Profiles(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	var prof taxfiling.Profile
	for _, p := range profiles {
		if p.TaxID == ret.TaxID {
			prof = p
		}
	}
	return s.taxDoc(ctx, propertyID, "TAX RETURN", prof, ret.PeriodStart, ret.PeriodEnd, ret.DueDate, ret.Lines, ret.Base, ret.Tax, ret.VATOffset, ret.Input, &ret, "tax-return-"+ret.Number+".pdf")
}

// TaxWorksheetPDF is the worksheet of a month of a tax, filed or not (tax.view).
func (s *Service) TaxWorksheetPDF(ctx context.Context, propertyID, taxID int64, start civil.Date) (Document, error) {
	ws, err := s.tax.Worksheet(ctx, propertyID, taxID, start)
	if err != nil {
		return Document{}, err
	}
	if ws.Return != nil {
		return s.TaxReturnPDF(ctx, propertyID, ws.Return.ID)
	}
	return s.taxDoc(ctx, propertyID, "TAX WORKSHEET", ws.Profile, ws.PeriodStart, ws.PeriodEnd, ws.DueDate, ws.Lines, ws.Base, ws.Tax, ws.VATOffset, ws.Input, nil, "tax-worksheet-"+ws.Profile.TaxCode+"-"+monthKey(start)+".pdf")
}

func monthKey(d civil.Date) string {
	return strconv.Itoa(d.Year()) + "-" + pad2(strconv.Itoa(int(d.Month())))
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func (h *Handler) registerTax(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/tax"
	mux.Handle("GET "+p+"/returns/{id}/return.pdf", httpx.HandlerFunc(h.serve(h.svc.TaxReturnPDF)))
	mux.Handle("GET "+p+"/worksheet.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		taxID, err := strconv.ParseInt(r.URL.Query().Get("tax_id"), 10, 64)
		if err != nil || taxID < 1 {
			return apperr.Invalid("the request is invalid", apperr.FieldError{Field: "tax_id", Code: "REQUIRED", Message: "the tax"})
		}
		period, err := civil.ParseDate(r.URL.Query().Get("period"))
		if err != nil {
			return apperr.Invalid("the request is invalid", apperr.FieldError{Field: "period", Code: "INVALID_DATE", Message: "the first day of the month, as YYYY-MM-DD"})
		}
		doc, err := h.svc.TaxWorksheetPDF(langCtx(r), pid, taxID, period)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
}
