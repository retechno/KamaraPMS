package taxinvoice

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/taxinvoice/taxinvoicedb"
	"kamarapms/internal/tenancy"
)

const maxPage = 200

// Service is the tax invoice application service. Reading needs tax.view; issuing, voiding (which also needs an approval), recording the
// official number and exporting need tax.invoice.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	iam   *iam.Service
	tax   *taxfiling.Service // the PKP status of the property on the date of an invoice
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, iamSvc *iam.Service, tax *taxfiling.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, iam: iamSvc, tax: tax}
}

func (s *Service) q(ctx context.Context) *taxinvoicedb.Queries {
	return taxinvoicedb.New(s.txm.DB(ctx))
}

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, Old: old, New: updated}
}

func errInvoiceNotFound() *apperr.Error {
	return apperr.NotFound("TAX_INVOICE_NOT_FOUND", "the tax invoice does not exist in this property")
}

// retryOnDuplicate runs a use case again once when a concurrent request with the same Idempotency-Key won the insert.
func retryOnDuplicate(key string, run func() error) error {
	err := run()
	var ae *apperr.Error
	if key != "" && errors.As(err, &ae) && ae.Code == "DUPLICATE_REQUEST" {
		return run()
	}
	return err
}

// ---------------------------------------------------------------------------------------------------------------
// The invoice a source would give

var nonDigit = regexp.MustCompile(`[^0-9]`)

// digits is a tax number without its dots, dashes and spaces.
func digits(s string) string { return nonDigit.ReplaceAllString(s, "") }

// validNPWP says whether a tax number has the 15 digits of an NPWP or the 16 of a new NPWP or a NIK.
func validNPWP(digitsOnly string) bool { return len(digitsOnly) == 15 || len(digitsOnly) == 16 }

type rawLine struct {
	code, name string
	rate       decimal.Decimal
	base, vat  decimal.Decimal
}

// scale takes the share of a folio a transfer covers: every line times the factor, rounded to the decimals of the currency, the rounding
// difference left on the last line so that the totals are the rounded totals of the folio.
func scale(lines []rawLine, factor decimal.Decimal, decimals int32) []rawLine {
	if factor.Equal(decimal.NewFromInt(1)) || len(lines) == 0 {
		return lines
	}
	out := make([]rawLine, len(lines))
	sumBase, sumVAT, wantBase, wantVAT := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	for i, l := range lines {
		out[i] = rawLine{code: l.code, name: l.name, rate: l.rate, base: l.base.Mul(factor).Round(decimals), vat: l.vat.Mul(factor).Round(decimals)}
		sumBase, sumVAT = sumBase.Add(out[i].base), sumVAT.Add(out[i].vat)
		wantBase, wantVAT = wantBase.Add(l.base), wantVAT.Add(l.vat)
	}
	wantBase, wantVAT = wantBase.Mul(factor).Round(decimals), wantVAT.Mul(factor).Round(decimals)
	last := &out[len(out)-1]
	last.base, last.vat = last.base.Add(wantBase.Sub(sumBase)), last.vat.Add(wantVAT.Sub(sumVAT))
	return out
}

// part is a folio behind a source and what of it the source covers (the whole folio, or the amount transferred onto a city ledger invoice).
type part struct {
	folioID int64
	amount  decimal.Decimal
	whole   bool
}

// prepare reads the source and says what its tax invoice would be, with the blockers that stop it. It writes nothing.
func (s *Service) prepare(ctx context.Context, tenantID, propertyID int64, in IssueInput, issueDate civil.Date) (Preview, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	decimals := prop.CurrencyDecimals
	pv := Preview{SourceType: in.SourceType, IssueDate: issueDate, Lines: []Line{}, Blockers: []Blocker{}}
	block := func(code, msg string) { pv.Blockers = append(pv.Blockers, Blocker{Code: code, Message: msg}) }

	status, err := s.tax.SettingsOnDate(ctx, tenantID, propertyID, issueDate)
	if err != nil {
		return Preview{}, err
	}
	pv.Seller = Seller{Name: prop.Name, NPWP: status.NPWP, PKPNumber: status.PKPNumber, Address: joinAddress(prop.Address, prop.City), SignerName: status.SignerName, SignerTitle: status.SignerTitle}
	if !status.IsPKP {
		block("NOT_PKP", "the property is not PKP on the date of the invoice")
	}

	q := s.q(ctx)
	var transfers []part
	switch in.SourceType {
	case SourceCityLedgerInvoice:
		row, err := q.CityLedgerInvoiceSource(ctx, taxinvoicedb.CityLedgerInvoiceSourceParams{TenantID: tenantID, PropertyID: propertyID, ID: in.CityLedgerInvoiceID})
		if isNoRows(err) {
			return Preview{}, apperr.NotFound("CITY_LEDGER_INVOICE_NOT_FOUND", "the city ledger invoice does not exist in this property")
		}
		if err != nil {
			return Preview{}, err
		}
		pv.SourceRef = row.InvoiceNumber
		pv.Buyer = Party{Name: row.CompanyName, NPWP: deref(row.CompanyTaxID), Address: joinAddress(deref(row.CompanyAddress), deref(row.CompanyCity))}
		if row.Status != "ISSUED" {
			block("SOURCE_NOT_ISSUED", "the city ledger invoice is voided")
		}
		ts, err := q.CityLedgerTransfers(ctx, taxinvoicedb.CityLedgerTransfersParams{TenantID: tenantID, PropertyID: propertyID, InvoiceID: in.CityLedgerInvoiceID})
		if err != nil {
			return Preview{}, err
		}
		for _, t := range ts {
			transfers = append(transfers, part{t.FolioID, t.Amount, false})
		}
	case SourceFolio:
		row, err := q.FolioSource(ctx, taxinvoicedb.FolioSourceParams{TenantID: tenantID, PropertyID: propertyID, ID: in.FolioID})
		if isNoRows(err) {
			return Preview{}, apperr.NotFound("FOLIO_NOT_FOUND", "the folio does not exist in this property")
		}
		if err != nil {
			return Preview{}, err
		}
		pv.SourceRef = row.FolioNumber
		if in.Buyer != nil {
			pv.Buyer = *in.Buyer
		}
		if row.Status != "CLOSED" {
			block("SOURCE_NOT_CLOSED", "the folio is not closed")
		}
		transfers = append(transfers, part{in.FolioID, decimal.Zero, true})
	default:
		return Preview{}, apperr.Invalid("the tax invoice is invalid", fieldErr("source_type", "INVALID_VALUE", "CITY_LEDGER_INVOICE or FOLIO"))
	}
	pv.Buyer.Name, pv.Buyer.Address = strings.TrimSpace(pv.Buyer.Name), strings.TrimSpace(pv.Buyer.Address)
	pv.Buyer.NPWP = digits(pv.Buyer.NPWP)
	switch {
	case pv.Buyer.Name == "":
		block("BUYER_NAME_REQUIRED", "the name of the buyer")
	case len([]rune(pv.Buyer.Name)) > 150:
		block("BUYER_NAME_TOO_LONG", "the name of the buyer is at most 150 characters")
	}
	if !validNPWP(pv.Buyer.NPWP) {
		block("BUYER_NPWP_INVALID", "the buyer needs a tax number (NPWP or NIK) of 15 or 16 digits")
	}
	if len([]rune(pv.Buyer.Address)) > 400 {
		block("BUYER_ADDRESS_TOO_LONG", "the address of the buyer is at most 400 characters")
	}

	// the lines: the VAT of each folio, in the share the source covers
	merged := map[string]*rawLine{}
	var order []string
	for _, t := range transfers {
		lines, err := q.FolioVATLines(ctx, taxinvoicedb.FolioVATLinesParams{TenantID: tenantID, PropertyID: propertyID, FolioID: t.folioID})
		if err != nil {
			return Preview{}, err
		}
		raw := make([]rawLine, 0, len(lines))
		for _, l := range lines {
			raw = append(raw, rawLine{code: l.ChargeCode, name: l.ChargeName, rate: l.Rate, base: l.Base, vat: l.Vat})
		}
		factor := decimal.NewFromInt(1)
		if !t.whole {
			charged, err := q.FolioCharged(ctx, taxinvoicedb.FolioChargedParams{TenantID: tenantID, PropertyID: propertyID, FolioID: t.folioID})
			if err != nil {
				return Preview{}, err
			}
			factor = decimal.Zero
			if charged.IsPositive() {
				factor = decimal.Min(decimal.NewFromInt(1), t.amount.Div(charged))
			}
		}
		for _, l := range scale(raw, factor, decimals) {
			key := l.code + "|" + l.rate.String()
			if cur, ok := merged[key]; ok {
				cur.base, cur.vat = cur.base.Add(l.base), cur.vat.Add(l.vat)
				continue
			}
			c := l
			merged[key] = &c
			order = append(order, key)
		}
	}
	for i, key := range order {
		l := merged[key]
		if l.vat.IsZero() && l.base.IsZero() {
			continue
		}
		name := l.name
		if name == "" {
			name = l.code
		}
		pv.Lines = append(pv.Lines, Line{LineNo: i + 1, ChargeCode: l.code, Description: name, Base: l.base, Rate: l.rate, VAT: l.vat})
		pv.TaxableBase, pv.VATAmount = pv.TaxableBase.Add(l.base), pv.VATAmount.Add(l.vat)
	}
	for i := range pv.Lines {
		pv.Lines[i].LineNo = i + 1
	}
	if !pv.VATAmount.IsPositive() {
		block("NO_VAT", "there is no VAT to invoice")
	}
	if !pv.TaxableBase.IsPositive() {
		block("NO_BASE", "there is no taxable base to invoice")
	}
	pv.Ready = len(pv.Blockers) == 0
	return pv, nil
}

func joinAddress(address, city string) string {
	address, city = strings.TrimSpace(address), strings.TrimSpace(city)
	switch {
	case address == "":
		return city
	case city == "":
		return address
	}
	return address + ", " + city
}

func validateSource(in IssueInput) []apperr.FieldError {
	var fields []apperr.FieldError
	switch in.SourceType {
	case SourceCityLedgerInvoice:
		if in.CityLedgerInvoiceID < 1 {
			fields = append(fields, fieldErr("city_ledger_invoice_id", "REQUIRED", "the city ledger invoice"))
		}
	case SourceFolio:
		if in.FolioID < 1 {
			fields = append(fields, fieldErr("folio_id", "REQUIRED", "the folio"))
		}
	default:
		fields = append(fields, fieldErr("source_type", "INVALID_VALUE", "CITY_LEDGER_INVOICE or FOLIO"))
	}
	return fields
}

// Preview is what the tax invoice of a source would be, with what stops it from being issued (tax.invoice).
func (s *Service) Preview(ctx context.Context, propertyID int64, in IssueInput) (Preview, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxInvoice)
	if err != nil {
		return Preview{}, err
	}
	if fields := validateSource(in); len(fields) > 0 {
		return Preview{}, apperr.Invalid("the tax invoice is invalid", fields...)
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Preview{}, err
	}
	var pv Preview
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		pv, err = s.prepare(ctx, p.TenantID, propertyID, in, day.BusinessDate)
		return err
	})
	return pv, err
}

// ---------------------------------------------------------------------------------------------------------------
// Reading

func toInvoice(r taxinvoicedb.ListInvoicesRow) Invoice {
	return Invoice{
		ID: r.ID, Ref: r.InvoiceRef, Status: r.Status, IssueDate: r.IssueDate, SourceType: r.SourceType, CityLedgerInvoiceID: r.CityLedgerInvoiceID,
		CityLedgerInvoiceNumber: deref(r.CityLedgerInvoiceNumber), FolioID: r.FolioID, FolioNumber: deref(r.FolioNumber),
		Seller:      Seller{Name: r.SellerName, NPWP: r.SellerNpwp, PKPNumber: deref(r.SellerPkpNumber), Address: deref(r.SellerAddress), SignerName: deref(r.SignerName), SignerTitle: deref(r.SignerTitle)},
		Buyer:       Party{Name: r.BuyerName, NPWP: r.BuyerNpwp, Address: deref(r.BuyerAddress)},
		TaxableBase: r.TaxableBase, VATAmount: r.VatAmount, DJPNumber: deref(r.DjpNumber), ReplacesInvoiceID: r.ReplacesInvoiceID, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason),
		CreatedAt: r.CreatedAt,
	}
}

func (s *Service) list(ctx context.Context, tenantID, propertyID int64, id *int64, f Filter, withLines bool) ([]Invoice, error) {
	limit := f.Limit
	if limit < 1 || limit > maxPage {
		limit = maxPage
	}
	rows, err := s.q(ctx).ListInvoices(ctx, taxinvoicedb.ListInvoicesParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, Status: nullable(f.Status), FromDate: f.From, ToDate: f.To, Q: nullable(f.Q), RowLimit: int32(limit), //nolint:gosec // G115: at most 200
	})
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, toInvoice(r))
		ids = append(ids, r.ID)
	}
	if !withLines || len(ids) == 0 {
		return out, nil
	}
	lines, err := s.q(ctx).ListInvoiceLines(ctx, taxinvoicedb.ListInvoiceLinesParams{TenantID: tenantID, PropertyID: propertyID, InvoiceIds: ids})
	if err != nil {
		return nil, err
	}
	byInvoice := map[int64][]Line{}
	for _, l := range lines {
		byInvoice[l.InvoiceID] = append(byInvoice[l.InvoiceID], Line{LineNo: int(l.LineNo), ChargeCode: l.ChargeCode, Description: l.Description, Base: l.BaseAmount, Rate: l.Rate, VAT: l.VatAmount})
	}
	for i := range out {
		out[i].Lines = byInvoice[out[i].ID]
	}
	return out, nil
}

// Invoices lists the tax invoices, newest first (tax.view).
func (s *Service) Invoices(ctx context.Context, propertyID int64, f Filter) ([]Invoice, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, p.TenantID, propertyID, nil, f, false)
}

func (s *Service) load(ctx context.Context, tenantID, propertyID, id int64) (Invoice, error) {
	list, err := s.list(ctx, tenantID, propertyID, &id, Filter{Limit: 1}, true)
	if err != nil {
		return Invoice{}, err
	}
	if len(list) == 0 {
		return Invoice{}, errInvoiceNotFound()
	}
	return list[0], nil
}

// GetInvoice is one tax invoice with its lines (tax.view).
func (s *Service) GetInvoice(ctx context.Context, propertyID, id int64) (Invoice, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Invoice{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// ---------------------------------------------------------------------------------------------------------------
// Issuing, voiding and the official number

// Issue issues the tax invoice of a source (tax.invoice). It is dated the current business date: an invoice is not backdated. The
// property must be PKP on that date, the buyer needs a tax number, and a source has one live invoice. A replacement names the void
// invoice it replaces. The Idempotency-Key makes a retry return the first invoice.
func (s *Service) Issue(ctx context.Context, propertyID int64, in IssueInput, key string) (Invoice, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxInvoice)
	if err != nil {
		return Invoice{}, err
	}
	fields := validateSource(in)
	if key == "" || len(key) > 100 {
		fields = append(fields, fieldErr("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Invoice{}, apperr.Invalid("the tax invoice is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.issue(ctx, p, propertyID, in, key, &id) })
	if err != nil {
		return Invoice{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

func (s *Service) issue(ctx context.Context, p auth.Principal, propertyID int64, in IssueInput, key string, out *int64) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if prev, err := q.FindInvoiceByKey(ctx, taxinvoicedb.FindInvoiceByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			*out = prev
			return nil
		} else if !isNoRows(err) {
			return err
		}
		pv, err := s.prepare(ctx, p.TenantID, propertyID, in, day.BusinessDate)
		if err != nil {
			return err
		}
		if !pv.Ready {
			return apperr.Conflict("TAX_INVOICE_NOT_READY", "the tax invoice cannot be issued yet").WithContext("blockers", pv.Blockers)
		}
		if in.ReplacesInvoiceID != nil {
			old, err := s.load(ctx, p.TenantID, propertyID, *in.ReplacesInvoiceID)
			if err != nil {
				return err
			}
			sameSource := old.SourceType == in.SourceType && ((old.CityLedgerInvoiceID != nil && *old.CityLedgerInvoiceID == in.CityLedgerInvoiceID) || (old.FolioID != nil && *old.FolioID == in.FolioID))
			if old.Status != StatusVoided || !sameSource {
				return apperr.Conflict("TAX_INVOICE_REPLACE_INVALID", "a tax invoice replaces a void tax invoice of the same source")
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqTaxInvoice)
		if err != nil {
			return err
		}
		params := taxinvoicedb.InsertInvoiceParams{
			TenantID: p.TenantID, PropertyID: propertyID, InvoiceRef: number, IssueDate: day.BusinessDate, SourceType: in.SourceType,
			SellerName: pv.Seller.Name, SellerNpwp: pv.Seller.NPWP, SellerPkpNumber: nullable(pv.Seller.PKPNumber), SellerAddress: nullable(pv.Seller.Address),
			SignerName: nullable(pv.Seller.SignerName), SignerTitle: nullable(pv.Seller.SignerTitle),
			BuyerName: pv.Buyer.Name, BuyerNpwp: pv.Buyer.NPWP, BuyerAddress: nullable(pv.Buyer.Address),
			TaxableBase: pv.TaxableBase, VatAmount: pv.VATAmount, ReplacesInvoiceID: in.ReplacesInvoiceID, IdempotencyKey: nullable(key), ActorID: p.ActorID(),
		}
		if in.SourceType == SourceCityLedgerInvoice {
			params.CityLedgerInvoiceID = ptr(in.CityLedgerInvoiceID)
		} else {
			params.FolioID = ptr(in.FolioID)
		}
		id, err := q.InsertInvoice(ctx, params)
		if err != nil {
			return err
		}
		for _, l := range pv.Lines {
			if err := q.InsertInvoiceLine(ctx, taxinvoicedb.InsertInvoiceLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, InvoiceID: id, LineNo: int32(l.LineNo), ChargeCode: l.ChargeCode, Description: l.Description, //nolint:gosec // G115: a few lines
				BaseAmount: l.Base, Rate: l.Rate, VatAmount: l.VAT,
			}); err != nil {
				return err
			}
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.invoice_issued", "tax_invoice", id, nil,
			map[string]any{"invoice_ref": number, "source": pv.SourceType, "source_ref": pv.SourceRef, "buyer": pv.Buyer.Name, "taxable_base": pv.TaxableBase.String(), "vat": pv.VATAmount.String()}))
	})
}

// VoidInvoice voids a tax invoice (tax.invoice plus an approval); its source can then be invoiced again as a replacement.
func (s *Service) VoidInvoice(ctx context.Context, propertyID, id int64, in VoidInput) (Invoice, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxInvoice)
	if err != nil {
		return Invoice{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Invoice{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Invoice{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cur, err := s.load(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != StatusIssued {
			return apperr.Conflict("TAX_INVOICE_ALREADY_VOIDED", "the tax invoice is voided already")
		}
		by := approval.UserID()
		if err := s.q(ctx).VoidInvoice(ctx, taxinvoicedb.VoidInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.invoice_voided", "tax_invoice", id,
			map[string]any{"status": StatusIssued}, map[string]any{"status": StatusVoided, "reason": reason, "approved_by": by, "invoice_ref": cur.Ref}))
	})
	if err != nil {
		return Invoice{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

var djpNumberPattern = regexp.MustCompile(`^[0-9A-Za-z./-]{1,40}$`)

// SetDJPNumber records the official number the tax authority gave an invoice when it was uploaded (tax.invoice): once, on an invoice that is
// not void, and one number belongs to one invoice.
func (s *Service) SetDJPNumber(ctx context.Context, propertyID, id int64, in DJPNumberInput) (Invoice, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxInvoice)
	if err != nil {
		return Invoice{}, err
	}
	number := strings.ReplaceAll(strings.TrimSpace(in.Number), " ", "")
	if !djpNumberPattern.MatchString(number) {
		return Invoice{}, apperr.Invalid("the official number is invalid", fieldErr("number", "INVALID_VALUE", "up to 40 letters, digits, dots, dashes and slashes"))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cur, err := s.load(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Status != StatusIssued {
			return apperr.Conflict("TAX_INVOICE_ALREADY_VOIDED", "the tax invoice is voided")
		}
		if cur.DJPNumber != "" {
			return apperr.Conflict("TAX_INVOICE_NUMBER_SET", "the official number is recorded already")
		}
		if err := s.q(ctx).SetDJPNumber(ctx, taxinvoicedb.SetDJPNumberParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, DjpNumber: &number}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.invoice_number_recorded", "tax_invoice", id, nil, map[string]any{"invoice_ref": cur.Ref, "djp_number": number}))
	})
	if err != nil {
		return Invoice{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// ---------------------------------------------------------------------------------------------------------------
// Coverage

// CoverageOf compares the VAT collected in a range with the VAT on the invoices issued in it, and lists the folios that carry VAT and are
// on no live invoice (tax.view). Information only: nothing here stops a return.
func (s *Service) CoverageOf(ctx context.Context, propertyID int64, from, to civil.Date) (Coverage, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return Coverage{}, err
	}
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return Coverage{}, apperr.Invalid("the range is invalid", fieldErr("to", "INVALID_RANGE", "from and to, to not before from"))
	}
	q := s.q(ctx)
	collected, err := q.VATCollectedBetween(ctx, taxinvoicedb.VATCollectedBetweenParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Coverage{}, err
	}
	invoiced, err := q.VATInvoicedBetween(ctx, taxinvoicedb.VATInvoicedBetweenParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Coverage{}, err
	}
	rows, err := q.FoliosWithoutInvoice(ctx, taxinvoicedb.FoliosWithoutInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return Coverage{}, err
	}
	out := Coverage{From: from, To: to, VATCollected: collected, VATInvoiced: invoiced, Difference: collected.Sub(invoiced), Uncovered: []UncoveredFolio{}}
	for _, r := range rows {
		out.Uncovered = append(out.Uncovered, UncoveredFolio{FolioID: r.FolioID, FolioNumber: r.FolioNumber, Status: r.Status, VAT: r.Vat})
	}
	return out, nil
}
