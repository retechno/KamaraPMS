// Package taxinvoice is the tax invoice (faktur pajak) of a PKP property: issued for a city ledger invoice or a closed folio from the
// VAT components of the folios behind it, with the seller and the buyer copied onto it, voided or replaced, given the official number
// of the tax authority afterwards, exported in batches, and checked against the VAT collected. The VAT return does not read these
// invoices (design: docs/architecture/09-pkp-input-vat.md, step 4).
package taxinvoice

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

// Sources of a tax invoice.
const (
	SourceCityLedgerInvoice = "CITY_LEDGER_INVOICE"
	SourceFolio             = "FOLIO"
)

// Statuses of a tax invoice.
const (
	StatusIssued = "ISSUED"
	StatusVoided = "VOIDED"
)

// Party is the buyer of an invoice.
type Party struct {
	Name    string `json:"name"`
	NPWP    string `json:"npwp"`
	Address string `json:"address,omitempty"`
}

// Seller is the hotel as it was when the invoice was issued.
type Seller struct {
	Name        string `json:"name"`
	NPWP        string `json:"npwp"`
	PKPNumber   string `json:"pkp_number,omitempty"`
	Address     string `json:"address,omitempty"`
	SignerName  string `json:"signer_name,omitempty"`
	SignerTitle string `json:"signer_title,omitempty"`
}

// Line is the VAT of a charge at a rate: the taxable base (DPP) and the VAT on it.
type Line struct {
	LineNo      int             `json:"line_no"`
	ChargeCode  string          `json:"charge_code"`
	Description string          `json:"description"`
	Base        decimal.Decimal `json:"base_amount"`
	Rate        decimal.Decimal `json:"rate"`
	VAT         decimal.Decimal `json:"vat_amount"`
}

// Invoice is a tax invoice, frozen when it was issued.
type Invoice struct {
	ID                      int64           `json:"id"`
	Ref                     string          `json:"invoice_ref"`
	Status                  string          `json:"status"`
	IssueDate               civil.Date      `json:"issue_date"`
	SourceType              string          `json:"source_type"`
	CityLedgerInvoiceID     *int64          `json:"city_ledger_invoice_id"`
	CityLedgerInvoiceNumber string          `json:"city_ledger_invoice_number,omitempty"`
	FolioID                 *int64          `json:"folio_id"`
	FolioNumber             string          `json:"folio_number,omitempty"`
	Seller                  Seller          `json:"seller"`
	Buyer                   Party           `json:"buyer"`
	TaxableBase             decimal.Decimal `json:"taxable_base"`
	VATAmount               decimal.Decimal `json:"vat_amount"`
	DJPNumber               string          `json:"djp_number,omitempty"`
	ReplacesInvoiceID       *int64          `json:"replaces_invoice_id"`
	VoidedAt                *time.Time      `json:"voided_at"`
	VoidReason              string          `json:"void_reason,omitempty"`
	CreatedAt               time.Time       `json:"created_at"`
	Lines                   []Line          `json:"lines,omitempty"`
}

// IssueInput issues a tax invoice for a source. The buyer is given for a folio; for a city ledger invoice it is the company.
type IssueInput struct {
	SourceType          string `json:"source_type"`
	CityLedgerInvoiceID int64  `json:"city_ledger_invoice_id"`
	FolioID             int64  `json:"folio_id"`
	Buyer               *Party `json:"buyer"`
	ReplacesInvoiceID   *int64 `json:"replaces_invoice_id"`
}

// Blocker says why an invoice cannot be issued yet.
type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Preview is what an invoice for a source would say, with what stops it from being issued.
type Preview struct {
	SourceType  string          `json:"source_type"`
	SourceRef   string          `json:"source_ref"`
	IssueDate   civil.Date      `json:"issue_date"`
	Seller      Seller          `json:"seller"`
	Buyer       Party           `json:"buyer"`
	Lines       []Line          `json:"lines"`
	TaxableBase decimal.Decimal `json:"taxable_base"`
	VATAmount   decimal.Decimal `json:"vat_amount"`
	Blockers    []Blocker       `json:"blockers"`
	Ready       bool            `json:"ready"`
}

// Filter narrows the invoice list.
type Filter struct {
	Status string
	From   *civil.Date
	To     *civil.Date
	Q      string
	Limit  int
}

// VoidInput voids an invoice with a reason and an approval.
type VoidInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// DJPNumberInput records the official number the tax authority gave.
type DJPNumberInput struct {
	Number string `json:"number"`
}

// UncoveredFolio is a folio that carries VAT and is on no tax invoice.
type UncoveredFolio struct {
	FolioID     int64           `json:"folio_id"`
	FolioNumber string          `json:"folio_number"`
	Status      string          `json:"status"`
	VAT         decimal.Decimal `json:"vat"`
}

// Coverage compares the VAT collected in a range with the VAT on the tax invoices issued in it.
type Coverage struct {
	From         civil.Date       `json:"from"`
	To           civil.Date       `json:"to"`
	VATCollected decimal.Decimal  `json:"vat_collected"`
	VATInvoiced  decimal.Decimal  `json:"vat_invoiced"`
	Difference   decimal.Decimal  `json:"difference"`
	Uncovered    []UncoveredFolio `json:"uncovered"`
}

// Export is a batch of tax invoices written as a file.
type Export struct {
	ID           int64      `json:"id"`
	Format       string     `json:"format"`
	PeriodStart  civil.Date `json:"period_start"`
	PeriodEnd    civil.Date `json:"period_end"`
	InvoiceCount int        `json:"invoice_count"`
	FileName     string     `json:"file_name"`
	SHA256       string     `json:"sha256"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ExportFile is an export with its content.
type ExportFile struct {
	Export
	Content []byte `json:"-"`
}

// ExportInput asks for the invoices issued in a range.
type ExportInput struct {
	From civil.Date `json:"from"`
	To   civil.Date `json:"to"`
}
