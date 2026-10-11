package taxinvoice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxinvoice/taxinvoicedb"
)

// FormatCSV is the only export format built so far: one row per invoice line, every field of the invoice, UTF-8. It is a neutral file an
// accountant can map to the layout the tax authority's system takes; that layout is not built yet.
const FormatCSV = "CSV"

var csvHeader = []string{
	"invoice_ref", "status", "issue_date", "official_number", "seller_npwp", "seller_name", "buyer_npwp", "buyer_name", "buyer_address",
	"source", "source_ref", "line_no", "charge_code", "description", "taxable_base", "rate", "vat_amount",
}

// csvOf writes the invoices as rows, one per line, amounts at the decimals of the currency.
func csvOf(invoices []Invoice, decimals int32) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // a byte order mark, so a spreadsheet reads the names as UTF-8
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for _, inv := range invoices {
		ref := inv.CityLedgerInvoiceNumber
		if inv.SourceType == SourceFolio {
			ref = inv.FolioNumber
		}
		for _, l := range inv.Lines {
			row := []string{
				inv.Ref, inv.Status, inv.IssueDate.String(), inv.DJPNumber, digits(inv.Seller.NPWP), inv.Seller.Name, inv.Buyer.NPWP, inv.Buyer.Name, inv.Buyer.Address,
				inv.SourceType, ref, strconv.Itoa(l.LineNo), l.ChargeCode, l.Description, l.Base.StringFixed(decimals), l.Rate.String(), l.VAT.StringFixed(decimals),
			}
			if err := w.Write(row); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ExportInvoices writes the tax invoices issued in a range (void ones too, so a cancellation is reported) as a file and records the batch with
// the invoices in it and a hash of the file (tax.invoice). A range can be exported again: each time is a batch of its own.
func (s *Service) ExportInvoices(ctx context.Context, propertyID int64, in ExportInput) (ExportFile, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxInvoice)
	if err != nil {
		return ExportFile{}, err
	}
	if in.From.IsZero() || in.To.IsZero() || in.To.Before(in.From) {
		return ExportFile{}, apperr.Invalid("the range is invalid", fieldErr("to", "INVALID_RANGE", "from and to, to not before from"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return ExportFile{}, err
	}
	var out ExportFile
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		invoices, err := s.list(ctx, p.TenantID, propertyID, nil, Filter{From: &in.From, To: &in.To, Limit: maxPage}, true)
		if err != nil {
			return err
		}
		if len(invoices) == 0 {
			return apperr.Conflict("TAX_INVOICE_EXPORT_EMPTY", "no tax invoice was issued in this range")
		}
		// oldest first, the order they were issued in
		for i, j := 0, len(invoices)-1; i < j; i, j = i+1, j-1 {
			invoices[i], invoices[j] = invoices[j], invoices[i]
		}
		content, err := csvOf(invoices, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		name := "tax-invoices-" + in.From.String() + "-" + in.To.String() + ".csv"
		q := s.q(ctx)
		id, err := q.InsertExport(ctx, taxinvoicedb.InsertExportParams{
			TenantID: p.TenantID, PropertyID: propertyID, Format: FormatCSV, PeriodStart: in.From, PeriodEnd: in.To, InvoiceCount: int32(len(invoices)), //nolint:gosec // G115: at most 200
			FileName: name, Sha256: hex.EncodeToString(sum[:]), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for _, inv := range invoices {
			if err := q.InsertExportItem(ctx, taxinvoicedb.InsertExportItemParams{TenantID: p.TenantID, PropertyID: propertyID, ExportID: id, InvoiceID: inv.ID}); err != nil {
				return err
			}
		}
		out = ExportFile{Export: Export{ID: id, Format: FormatCSV, PeriodStart: in.From, PeriodEnd: in.To, InvoiceCount: len(invoices), FileName: name, SHA256: hex.EncodeToString(sum[:]), CreatedAt: s.clock.Now()}, Content: content}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.invoices_exported", "tax_invoice_export", id, name, nil,
			map[string]any{"from": in.From.String(), "to": in.To.String(), "invoices": len(invoices), "sha256": out.SHA256}))
	})
	return out, err
}

// Exports lists the batches written, newest first (tax.view).
func (s *Service) Exports(ctx context.Context, propertyID int64) ([]Export, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListExports(ctx, taxinvoicedb.ListExportsParams{TenantID: p.TenantID, PropertyID: propertyID, RowLimit: maxPage})
	if err != nil {
		return nil, err
	}
	out := make([]Export, 0, len(rows))
	for _, r := range rows {
		out = append(out, Export{ID: r.ID, Format: r.Format, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, InvoiceCount: int(r.InvoiceCount), FileName: r.FileName, SHA256: r.Sha256, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
