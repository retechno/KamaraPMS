package cityledger

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/cityledger/cityledgerdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/tenancy"
)

// Credit notes and write-offs of the city ledger (design: docs/architecture/10-credit-notes-writeoffs.md). Both lower what a company owes and are
// journaled when they are made (Dr an allowance of revenue and the tax payable, or the bad debt expense, Cr CITY_LEDGER), on the business date,
// with an approval; nothing of the folio or of an invoice is rewritten.

// Kinds and statuses of an adjustment.
const (
	KindCreditNote = "CREDIT_NOTE"
	KindWriteOff   = "WRITE_OFF"

	AdjustmentPosted = "POSTED"
	AdjustmentVoided = "VOIDED"

	maxCreditNoteLines = 20
)

// CreditNoteLine is a line of a credit note: the allowance of revenue it is booked on, the net amount and the tax on it.
type CreditNoteLine struct {
	LineNo      int32   `json:"line_no"`
	Description string  `json:"description"`
	AccountID   int64   `json:"account_id"`
	AccountCode string  `json:"account_code"`
	AccountName string  `json:"account_name"`
	NetAmount   string  `json:"net_amount"`
	TaxID       *int64  `json:"tax_id"`
	TaxCode     string  `json:"tax_code,omitempty"`
	TaxRate     *string `json:"tax_rate"`
	TaxAmount   string  `json:"tax_amount"`
	Total       string  `json:"total"`
}

// Adjustment is a credit note or a write-off.
type Adjustment struct {
	ID            int64      `json:"id"`
	Number        string     `json:"number"`
	Kind          string     `json:"kind"`
	CompanyID     int64      `json:"company_id"`
	InvoiceID     *int64     `json:"invoice_id"`
	InvoiceNumber string     `json:"invoice_number,omitempty"`
	PaymentID     *int64     `json:"payment_id"`
	PaymentNumber string     `json:"payment_number,omitempty"`
	Amount        string     `json:"amount"`
	BusinessDate  civil.Date `json:"business_date"`
	Reason        string     `json:"reason"`
	// DebitAccountCode is the account a write-off is charged to.
	DebitAccountCode string `json:"debit_account_code,omitempty"`
	Status           string `json:"status"`
	JournalID        int64  `json:"journal_id"`
	JournalNumber    string `json:"journal_number"`
	// AttachedInvoiceID is the invoice made from the transfer a credit note was made against; the invoice asks the net amount.
	AttachedInvoiceID *int64           `json:"attached_invoice_id"`
	VoidedAt          *time.Time       `json:"voided_at"`
	VoidReason        string           `json:"void_reason,omitempty"`
	ApprovedBy        *int64           `json:"approved_by"`
	CreatedBy         *int64           `json:"created_by"`
	Lines             []CreditNoteLine `json:"lines,omitempty"`
}

// CreditNoteLineInput is a line of a new credit note. A tax is taken at the rate it has now, on the net amount.
type CreditNoteLineInput struct {
	Description string `json:"description"`
	AccountID   int64  `json:"account_id"`
	NetAmount   string `json:"net_amount"`
	TaxID       *int64 `json:"tax_id"`
}

// CreditNoteInput makes a credit note against an invoice or against a transfer that is not on an invoice yet.
type CreditNoteInput struct {
	InvoiceID *int64                `json:"invoice_id"`
	PaymentID *int64                `json:"payment_id"`
	Reason    string                `json:"reason"`
	Lines     []CreditNoteLineInput `json:"lines"`
	Approval  *iam.ApprovalInput    `json:"approval"`
}

// WriteOffInput writes off all or part of what an invoice still owes, charged to an expense account (or the allowance for doubtful accounts).
type WriteOffInput struct {
	InvoiceID int64              `json:"invoice_id"`
	Amount    string             `json:"amount"`
	AccountID int64              `json:"account_id"`
	Reason    string             `json:"reason"`
	Approval  *iam.ApprovalInput `json:"approval"`
}

func adjustmentAudit(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "city_ledger_adjustment", EntityID: id, Old: old, New: updated}
}

func errAdjustmentNotFound() *apperr.Error {
	return apperr.NotFound("ADJUSTMENT_NOT_FOUND", "the credit note or write-off does not exist in this property")
}

func adjustmentView(r cityledgerdb.ListAdjustmentsRow, decimals int32) Adjustment {
	a := Adjustment{
		ID: r.ID, Number: r.AdjustmentNumber, Kind: r.Kind, CompanyID: r.CompanyID, InvoiceID: r.InvoiceID, InvoiceNumber: deref(r.InvoiceNumber), PaymentID: r.PaymentID,
		PaymentNumber: deref(r.PaymentNumber), Amount: r.Amount.StringFixed(decimals), BusinessDate: r.BusinessDate, Reason: r.Reason, DebitAccountCode: deref(r.DebitAccountCode),
		Status: r.Status, JournalID: r.JournalID, JournalNumber: r.JournalNumber, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), ApprovedBy: r.ApprovedBy, CreatedBy: r.CreatedBy,
	}
	if r.AttachedInvoiceID != 0 {
		id := r.AttachedInvoiceID
		a.AttachedInvoiceID = &id
	}
	return a
}

func (s *Service) listAdjustments(ctx context.Context, tenantID, propertyID int64, id, companyID *int64, decimals int32) ([]Adjustment, error) {
	q := s.q(ctx)
	rows, err := q.ListAdjustments(ctx, cityledgerdb.ListAdjustmentsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, CompanyID: companyID})
	if err != nil {
		return nil, err
	}
	out := make([]Adjustment, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, adjustmentView(r, decimals))
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	lines, err := q.ListCreditNoteLines(ctx, cityledgerdb.ListCreditNoteLinesParams{TenantID: tenantID, PropertyID: propertyID, AdjustmentIds: ids})
	if err != nil {
		return nil, err
	}
	by := map[int64][]CreditNoteLine{}
	for _, l := range lines {
		cl := CreditNoteLine{
			LineNo: l.LineNo, Description: l.Description, AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, NetAmount: l.NetAmount.StringFixed(decimals),
			TaxID: l.TaxID, TaxCode: deref(l.TaxCode), TaxAmount: l.TaxAmount.StringFixed(decimals), Total: l.NetAmount.Add(l.TaxAmount).StringFixed(decimals),
		}
		if l.TaxRate != nil {
			rate := l.TaxRate.String()
			cl.TaxRate = &rate
		}
		by[l.AdjustmentID] = append(by[l.AdjustmentID], cl)
	}
	for i := range out {
		out[i].Lines = by[out[i].ID]
	}
	return out, nil
}

// Adjustments lists the credit notes and write-offs of a company, newest first, with the lines of the credit notes (cityledger.read).
func (s *Service) Adjustments(ctx context.Context, propertyID, companyID int64) ([]Adjustment, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if _, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals); err != nil {
		return nil, err
	}
	return s.listAdjustments(ctx, p.TenantID, propertyID, nil, &companyID, decimals)
}

// GetAdjustment is one credit note or write-off (cityledger.read).
func (s *Service) GetAdjustment(ctx context.Context, propertyID, id int64) (Adjustment, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Adjustment{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Adjustment{}, err
	}
	return s.loadAdjustment(ctx, p.TenantID, propertyID, id, decimals)
}

func (s *Service) loadAdjustment(ctx context.Context, tenantID, propertyID, id int64, decimals int32) (Adjustment, error) {
	list, err := s.listAdjustments(ctx, tenantID, propertyID, &id, nil, decimals)
	if err != nil {
		return Adjustment{}, err
	}
	if len(list) == 0 {
		return Adjustment{}, errAdjustmentNotFound()
	}
	return list[0], nil
}

// invoiceOwes is what an issued invoice still owes: its total less the receipts, credit notes and write-offs.
func (s *Service) invoiceOwes(ctx context.Context, propertyID int64, inv cityledgerdb.CityLedgerInvoice) (decimal.Decimal, error) {
	paid, err := s.paidOf(ctx, propertyID, []int64{inv.ID})
	if err != nil {
		return decimal.Zero, err
	}
	adj, err := s.adjustedOf(ctx, propertyID, []int64{inv.ID})
	if err != nil {
		return decimal.Zero, err
	}
	return inv.Total.Sub(paid[inv.ID]).Sub(adj[inv.ID].credited).Sub(adj[inv.ID].writtenOff), nil
}

func validateReason(reason string) (string, []apperr.FieldError) {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return reason, []apperr.FieldError{field("reason", "REQUIRED", "a reason is required")}
	case len([]rune(reason)) > maxReasonLen:
		return reason, []apperr.FieldError{field("reason", "TOO_LONG", "at most 500 characters")}
	}
	return reason, nil
}

// CreateCreditNote makes a credit note (cityledger.credit_note plus an approval): against an issued invoice, for at most what it still owes, or against
// a transfer of a company that is not on an invoice yet, for at most what the transfer says (the invoice that is made from it asks the net).
// The company row is locked (level 44), so two notes and a receipt cannot take off the same amount; the journal is Dr the allowance of revenue
// of each line (net) and the tax payable of each tax, Cr CITY_LEDGER. A tax is taken at the rate it has now.
func (s *Service) CreateCreditNote(ctx context.Context, propertyID int64, key string, in CreditNoteInput) (Adjustment, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerCreditNote)
	if err != nil {
		return Adjustment{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Adjustment{}, err
	}
	reason, fields := validateReason(in.Reason)
	if (in.InvoiceID == nil) == (in.PaymentID == nil) {
		fields = append(fields, field("invoice_id", "INVALID_TARGET", "an invoice or a transfer, not both"))
	}
	if len(in.Lines) < 1 || len(in.Lines) > maxCreditNoteLines {
		fields = append(fields, field("lines", "OUT_OF_RANGE", "between 1 and 20 lines"))
	}
	nets := make([]decimal.Decimal, len(in.Lines))
	for i, l := range in.Lines {
		at := func(f string) string { return "lines[" + strconv.Itoa(i) + "]." + f }
		d, perr := money.Parse(strings.TrimSpace(l.NetAmount))
		switch {
		case perr != nil || !d.IsPositive():
			fields = append(fields, field(at("net_amount"), "INVALID_AMOUNT", "a positive amount"))
		case !d.Equal(d.Round(decimals)):
			fields = append(fields, field(at("net_amount"), "INVALID_AMOUNT", "at most the currency's decimals"))
		default:
			nets[i] = d
		}
		if strings.TrimSpace(l.Description) == "" || len([]rune(l.Description)) > 200 {
			fields = append(fields, field(at("description"), "INVALID", "a description of up to 200 characters"))
		}
		if l.AccountID < 1 {
			fields = append(fields, field(at("account_id"), "REQUIRED", "the revenue account of the allowance"))
		}
	}
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Adjustment{}, apperr.Invalid("the credit note is invalid", fields...)
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Adjustment{}, err
	}
	return receiptReplay(key,
		func() (Adjustment, bool, error) {
			return s.replayAdjustment(ctx, p, propertyID, key, KindCreditNote, decimals)
		},
		func() (Adjustment, error) {
			var out Adjustment
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				// the target and its company, then the lock of the company
				var companyID int64
				var invoice cityledgerdb.CityLedgerInvoice
				var transfer cityledgerdb.GetTransferRow
				if in.InvoiceID != nil {
					invoice, err = q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.InvoiceID})
					if errors.Is(err, pgx.ErrNoRows) {
						return errInvoiceNotFound()
					}
					if err != nil {
						return err
					}
					companyID = invoice.CompanyID
				} else {
					transfer, err = q.GetTransfer(ctx, cityledgerdb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.PaymentID})
					if errors.Is(err, pgx.ErrNoRows) || (err == nil && transfer.CompanyID == nil) {
						return apperr.NotFound("TRANSFER_NOT_FOUND", "the transfer does not exist in this property")
					}
					if err != nil {
						return err
					}
					companyID = *transfer.CompanyID
				}
				if err := s.companies.Lock(ctx, propertyID, companyID); err != nil {
					return err
				}
				po, err := s.acct.BeginPosting(ctx, propertyID) // accounting settings (46) after the company (44)
				if err != nil {
					return err
				}
				po.AllowControl(accounting.KeyCityLedger)
				// the lines: accounts and taxes
				type booked struct {
					net, tax decimal.Decimal
					rate     *decimal.Decimal
					taxCode  string
					taxGL    string
				}
				bs := make([]booked, len(in.Lines))
				total := decimal.Zero
				var lineErrs []apperr.FieldError
				for i, l := range in.Lines {
					at := func(f string) string { return "lines[" + strconv.Itoa(i) + "]." + f }
					code, _, typ, ok, aerr := po.AccountInfo(ctx, l.AccountID)
					if aerr != nil {
						return aerr
					}
					switch {
					case !ok:
						lineErrs = append(lineErrs, field(at("account_id"), "NOT_FOUND", "no such account in this property"))
					case typ != accounting.TypeRevenue:
						lineErrs = append(lineErrs, field(at("account_id"), "NOT_REVENUE", "the allowance is booked on a revenue account, "+code+" is not"))
					default:
						if cerr := po.CheckAccount(ctx, l.AccountID, at("account_id")); cerr != nil {
							var ae *apperr.Error
							if errors.As(cerr, &ae) && len(ae.Fields) > 0 {
								lineErrs = append(lineErrs, ae.Fields...)
							} else {
								return cerr
							}
						}
					}
					b := booked{net: nets[i]}
					if l.TaxID != nil {
						t, terr := q.TaxForCreditNote(ctx, cityledgerdb.TaxForCreditNoteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *l.TaxID})
						switch {
						case errors.Is(terr, pgx.ErrNoRows):
							lineErrs = append(lineErrs, field(at("tax_id"), "NOT_FOUND", "no such tax in this property"))
						case terr != nil:
							return terr
						case !t.IsActive:
							lineErrs = append(lineErrs, field(at("tax_id"), "INACTIVE", "the tax is inactive"))
						default:
							rate := t.Rate
							b.rate, b.taxCode, b.taxGL = &rate, t.Code, deref(t.GlAccountCode)
							b.tax = nets[i].Mul(t.Rate).Div(decimal.NewFromInt(100)).Round(decimals)
						}
					}
					bs[i] = b
					total = total.Add(b.net).Add(b.tax)
				}
				if len(lineErrs) > 0 {
					return apperr.Invalid("the credit note is invalid", lineErrs...)
				}
				// what it may take off
				if in.InvoiceID != nil {
					if invoice.Status != InvoiceIssued {
						return apperr.Conflict("INVOICE_NOT_PAYABLE", "a voided invoice has no credit note")
					}
					owes, oerr := s.invoiceOwes(ctx, propertyID, invoice)
					if oerr != nil {
						return oerr
					}
					if total.GreaterThan(owes) {
						return apperr.Conflict("ADJUSTMENT_EXCEEDS_INVOICE", "the credit note is more than the invoice still owes").WithContext("outstanding", owes.StringFixed(decimals))
					}
				} else {
					if transfer.Status != "POSTED" || transfer.PaymentType != "PAYMENT" || transfer.PaymentMethod != "CITY_LEDGER" {
						return apperr.Conflict("TRANSFER_NOT_AVAILABLE", "a credit note is made against a posted transfer to a company")
					}
					if n, nerr := q.TransferOnLiveInvoice(ctx, cityledgerdb.TransferOnLiveInvoiceParams{PropertyID: propertyID, PaymentID: *in.PaymentID}); nerr != nil {
						return nerr
					} else if n > 0 {
						return apperr.Conflict("TRANSFER_ON_INVOICE", "the transfer is on an invoice: make the credit note against the invoice")
					}
					credited, cerr := q.TransferCredited(ctx, cityledgerdb.TransferCreditedParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentID: in.PaymentID})
					if cerr != nil {
						return cerr
					}
					if credited.Add(total).GreaterThan(transfer.Amount) {
						return apperr.Conflict("ADJUSTMENT_EXCEEDS_TRANSFER", "the credit notes would be more than the transfer says").
							WithContext("available", transfer.Amount.Sub(credited).StringFixed(decimals))
					}
				}
				// the journal
				number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqCreditNote)
				if err != nil {
					return err
				}
				cityLedger, err := po.SystemAccount(ctx, accounting.KeyCityLedger)
				if err != nil {
					return err
				}
				var jl []accounting.SystemLine
				taxByAccount := map[int64]*accounting.SystemLine{}
				var taxOrder []int64
				for i, l := range in.Lines {
					jl = append(jl, accounting.SystemLine{AccountID: l.AccountID, Debit: bs[i].net, Description: strings.TrimSpace(l.Description), SourceType: "CL_CREDIT_NOTE", SourceRef: number})
					if bs[i].tax.IsPositive() {
						acc, aerr := po.TaxPayableAccount(ctx, bs[i].taxGL)
						if aerr != nil {
							return aerr
						}
						if cur, ok := taxByAccount[acc]; ok && cur.SourceRef == bs[i].taxCode {
							cur.Debit = cur.Debit.Add(bs[i].tax)
							continue
						}
						line := accounting.SystemLine{AccountID: acc, Debit: bs[i].tax, Description: "Tax " + bs[i].taxCode + " on credit note " + number, SourceType: "TAX_CREDIT_NOTE", SourceRef: bs[i].taxCode}
						taxByAccount[acc] = &line
						taxOrder = append(taxOrder, acc)
						jl = append(jl, line)
					}
				}
				_ = taxOrder
				jl = append(jl, accounting.SystemLine{AccountID: cityLedger, Credit: total, Description: "Credit note " + number, SourceType: "CL_CREDIT_NOTE", SourceRef: number})
				jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalReceivables, Date: day.BusinessDate, Description: "Credit note " + number, Reference: number, Lines: jl})
				if err != nil {
					return err
				}
				by := approval.UserID()
				adj, err := q.InsertAdjustment(ctx, cityledgerdb.InsertAdjustmentParams{
					TenantID: p.TenantID, PropertyID: propertyID, AdjustmentNumber: number, Kind: KindCreditNote, CompanyID: companyID, InvoiceID: in.InvoiceID, PaymentID: in.PaymentID,
					Amount: total, BusinessDate: day.BusinessDate, Reason: reason, JournalID: jid, ApprovedBy: &by, IdempotencyKey: &key, ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				for i, l := range in.Lines {
					if err := q.InsertCreditNoteLine(ctx, cityledgerdb.InsertCreditNoteLineParams{
						TenantID: p.TenantID, PropertyID: propertyID, AdjustmentID: adj.ID, LineNo: int32(i + 1), Description: strings.TrimSpace(l.Description), AccountID: l.AccountID, //nolint:gosec // G115: at most 20 lines
						NetAmount: bs[i].net, TaxID: l.TaxID, TaxRate: bs[i].rate, TaxAmount: bs[i].tax,
					}); err != nil {
						return err
					}
				}
				if err := s.audit.Write(ctx, adjustmentAudit(p, propertyID, day.BusinessDate, "cityledger.credit_note_made", adj.ID, nil, map[string]any{
					"number": number, "company_id": companyID, "invoice_id": in.InvoiceID, "payment_id": in.PaymentID, "amount": total.String(), "reason": reason, "approved_by": by, "journal": jnum,
				})); err != nil {
					return err
				}
				out, err = s.loadAdjustment(ctx, p.TenantID, propertyID, adj.ID, decimals)
				return err
			})
			return out, err
		})
}

func (s *Service) replayAdjustment(ctx context.Context, p auth.Principal, propertyID int64, key, kind string, decimals int32) (Adjustment, bool, error) {
	row, err := s.q(ctx).GetAdjustmentByKey(ctx, cityledgerdb.GetAdjustmentByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if errors.Is(err, pgx.ErrNoRows) {
		return Adjustment{}, false, nil
	}
	if err != nil {
		return Adjustment{}, false, err
	}
	if row.Kind != kind {
		return Adjustment{}, false, errKeyReused()
	}
	a, err := s.loadAdjustment(ctx, p.TenantID, propertyID, row.ID, decimals)
	return a, true, err
}

// CreateWriteOff writes off all or part of what an issued invoice still owes (cityledger.write_off plus an approval): Dr the account that bears
// it, Cr CITY_LEDGER. The account is an expense account (the bad debt expense) or the allowance for doubtful accounts when the hotel provisions.
// The tax is not touched: the relief of the VAT on a bad debt is not modeled.
func (s *Service) CreateWriteOff(ctx context.Context, propertyID int64, key string, in WriteOffInput) (Adjustment, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerWriteOff)
	if err != nil {
		return Adjustment{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Adjustment{}, err
	}
	reason, fields := validateReason(in.Reason)
	amount, perr := money.Parse(strings.TrimSpace(in.Amount))
	switch {
	case perr != nil || !amount.IsPositive():
		fields = append(fields, field("amount", "INVALID_AMOUNT", "a positive amount"))
	case !amount.Equal(amount.Round(decimals)):
		fields = append(fields, field("amount", "INVALID_AMOUNT", "at most the currency's decimals"))
	}
	if in.InvoiceID < 1 {
		fields = append(fields, field("invoice_id", "REQUIRED", "the invoice"))
	}
	if in.AccountID < 1 {
		fields = append(fields, field("account_id", "REQUIRED", "the account that bears the write-off"))
	}
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Adjustment{}, apperr.Invalid("the write-off is invalid", fields...)
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Adjustment{}, err
	}
	return receiptReplay(key,
		func() (Adjustment, bool, error) {
			return s.replayAdjustment(ctx, p, propertyID, key, KindWriteOff, decimals)
		},
		func() (Adjustment, error) {
			var out Adjustment
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				invoice, err := q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.InvoiceID})
				if errors.Is(err, pgx.ErrNoRows) {
					return errInvoiceNotFound()
				}
				if err != nil {
					return err
				}
				if err := s.companies.Lock(ctx, propertyID, invoice.CompanyID); err != nil {
					return err
				}
				po, err := s.acct.BeginPosting(ctx, propertyID)
				if err != nil {
					return err
				}
				po.AllowControl(accounting.KeyCityLedger)
				code, _, typ, ok, aerr := po.AccountInfo(ctx, in.AccountID)
				if aerr != nil {
					return aerr
				}
				switch {
				case !ok:
					return apperr.Invalid("the write-off is invalid", field("account_id", "NOT_FOUND", "no such account in this property"))
				case typ != accounting.TypeExpense && code != allowanceAccountCode:
					return apperr.Invalid("the write-off is invalid", field("account_id", "NOT_EXPENSE", "an expense account, or the allowance for doubtful accounts "+allowanceAccountCode))
				}
				if cerr := po.CheckAccount(ctx, in.AccountID, "account_id"); cerr != nil {
					return cerr
				}
				if invoice.Status != InvoiceIssued {
					return apperr.Conflict("INVOICE_NOT_PAYABLE", "a voided invoice has no write-off")
				}
				owes, err := s.invoiceOwes(ctx, propertyID, invoice)
				if err != nil {
					return err
				}
				if amount.GreaterThan(owes) {
					return apperr.Conflict("ADJUSTMENT_EXCEEDS_INVOICE", "the write-off is more than the invoice still owes").WithContext("outstanding", owes.StringFixed(decimals))
				}
				number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqWriteOff)
				if err != nil {
					return err
				}
				cityLedger, err := po.SystemAccount(ctx, accounting.KeyCityLedger)
				if err != nil {
					return err
				}
				desc := "Write-off " + number + " invoice " + invoice.InvoiceNumber
				jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalReceivables, Date: day.BusinessDate, Description: desc, Reference: number, Lines: []accounting.SystemLine{
					{AccountID: in.AccountID, Debit: amount, Description: desc, SourceType: "CL_WRITE_OFF", SourceRef: number},
					{AccountID: cityLedger, Credit: amount, Description: desc, SourceType: "CL_WRITE_OFF", SourceRef: number},
				}})
				if err != nil {
					return err
				}
				by := approval.UserID()
				adj, err := q.InsertAdjustment(ctx, cityledgerdb.InsertAdjustmentParams{
					TenantID: p.TenantID, PropertyID: propertyID, AdjustmentNumber: number, Kind: KindWriteOff, CompanyID: invoice.CompanyID, InvoiceID: &in.InvoiceID,
					Amount: amount, BusinessDate: day.BusinessDate, Reason: reason, DebitAccountID: &in.AccountID, JournalID: jid, ApprovedBy: &by, IdempotencyKey: &key, ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, adjustmentAudit(p, propertyID, day.BusinessDate, "cityledger.write_off_made", adj.ID, nil, map[string]any{
					"number": number, "company_id": invoice.CompanyID, "invoice": invoice.InvoiceNumber, "amount": amount.String(), "account": code, "reason": reason, "approved_by": by, "journal": jnum,
				})); err != nil {
					return err
				}
				out, err = s.loadAdjustment(ctx, p.TenantID, propertyID, adj.ID, decimals)
				return err
			})
			return out, err
		})
}

// allowanceAccountCode is the allowance for doubtful accounts of the standard chart, which a write-off may be charged to besides an expense account.
const allowanceAccountCode = "1240"

// VoidAdjustment voids a credit note or a write-off (the permission of its kind plus an approval): its journal is reversed on the current
// business date and what it took off is owed again. A credit note of a transfer that is on an invoice now is voided with the invoice.
func (s *Service) VoidAdjustment(ctx context.Context, propertyID, id int64, in VoidInput) (Adjustment, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Adjustment{}, err
	}
	reason, fields := validateReason(in.Reason)
	if len(fields) > 0 {
		return Adjustment{}, apperr.Invalid("the void is invalid", fields...)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Adjustment{}, err
	}
	pre, err := s.q(ctx).GetAdjustment(ctx, cityledgerdb.GetAdjustmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Adjustment{}, errAdjustmentNotFound()
	}
	if err != nil {
		return Adjustment{}, err
	}
	perm := auth.PermCityLedgerCreditNote
	if pre.Kind == KindWriteOff {
		perm = auth.PermCityLedgerWriteOff
	}
	if err := s.authz.Require(ctx, propertyID, perm); err != nil {
		return Adjustment{}, err
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Adjustment{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.companies.Lock(ctx, propertyID, pre.CompanyID); err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID)
		if err != nil {
			return err
		}
		po.AllowControl(accounting.KeyCityLedger)
		q := s.q(ctx)
		cur, err := q.GetAdjustment(ctx, cityledgerdb.GetAdjustmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return err
		}
		if cur.Status != AdjustmentPosted {
			return apperr.Conflict("ADJUSTMENT_ALREADY_VOIDED", "the credit note or write-off is voided already")
		}
		list, err := s.listAdjustments(ctx, p.TenantID, propertyID, &id, nil, decimals)
		if err != nil {
			return err
		}
		if len(list) == 1 && list[0].AttachedInvoiceID != nil {
			return apperr.Conflict("ADJUSTMENT_ON_INVOICE", "the invoice asks the net amount of this credit note: void the invoice first").WithContext("invoice_id", *list[0].AttachedInvoiceID)
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, cur.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidAdjustment(ctx, cityledgerdb.VoidAdjustmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: s.clock.Now(), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by}); err != nil {
			return err
		}
		return s.audit.Write(ctx, adjustmentAudit(p, propertyID, day.BusinessDate, "cityledger.adjustment_voided", id,
			map[string]any{"status": cur.Status}, map[string]any{"status": AdjustmentVoided, "reason": reason, "approved_by": by, "number": cur.AdjustmentNumber}))
	})
	if err != nil {
		return Adjustment{}, err
	}
	return s.loadAdjustment(ctx, p.TenantID, propertyID, id, decimals)
}
