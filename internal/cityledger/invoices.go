package cityledger

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/cityledger/cityledgerdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Invoices group transfers (folios moved to a company) whose guests have checked out into one document for the
// company. The balance a company owes stays derived (transfers minus receipts): an invoice does not change it.

const (
	InvoiceIssued = "ISSUED"
	InvoiceVoided = "VOIDED"

	maxInvoiceLines = 200
)

// InvoiceLine is a transfer on an invoice (or a candidate for one).
type InvoiceLine struct {
	PaymentID          int64       `json:"payment_id"`
	PaymentNumber      string      `json:"payment_number"`
	BusinessDate       civil.Date  `json:"business_date"`
	FolioNumber        string      `json:"folio_number"`
	ConfirmationNumber string      `json:"confirmation_number"`
	StayNumber         string      `json:"stay_number,omitempty"`
	GuestName          string      `json:"guest_name,omitempty"`
	RoomNumbers        string      `json:"room_numbers,omitempty"`
	ArrivalDate        *civil.Date `json:"arrival_date"`
	DepartureDate      *civil.Date `json:"departure_date"`
	CheckedOutAt       *time.Time  `json:"checked_out_at"`
	Reference          string      `json:"reference_number,omitempty"`
	Amount             string      `json:"amount"`
}

// Candidate is a transfer that is not on a live invoice. Invoiceable is false while the guest is still in house.
type Candidate struct {
	InvoiceLine
	StayStatus  string `json:"stay_status"`
	Invoiceable bool   `json:"invoiceable"`
	// Credited is what credit notes made against the transfer took off; the invoice asks the amount less that.
	Credited string `json:"credited"`
	Net      string `json:"net"`
}

// Invoice is an invoice to a company; Lines is filled on the detail view only.
type Invoice struct {
	ID            int64      `json:"id"`
	InvoiceNumber string     `json:"invoice_number"`
	CompanyID     int64      `json:"company_id"`
	InvoiceDate   civil.Date `json:"invoice_date"`
	DueDate       civil.Date `json:"due_date"`
	Total         string     `json:"total"`
	Notes         string     `json:"notes,omitempty"`
	Status        string     `json:"status"`
	VoidedAt      *time.Time `json:"voided_at"`
	VoidReason    string     `json:"void_reason,omitempty"`
	CreatedBy     *int64     `json:"created_by"`
	ApprovedBy    *int64     `json:"approved_by"`
	// Paid is what posted receipts have allocated to the invoice; Outstanding is Total less Paid (0 once voided).
	Paid          string `json:"paid"`
	Outstanding   string `json:"outstanding"`
	PaymentStatus string `json:"payment_status"` // UNPAID, PARTIAL, PAID, CREDITED, WRITTEN_OFF or VOID
	// Subtotal is the transfers on the invoice; the Total is that less the credit notes of those transfers that were attached when the invoice was made
	// (AttachedCredits). Credited and WrittenOff are what credit notes and write-offs made against the invoice itself have taken off.
	Subtotal        string           `json:"subtotal"`
	Credited        string           `json:"credited"`
	WrittenOff      string           `json:"written_off"`
	AttachedCredits []AttachedCredit `json:"attached_credits,omitempty"`
	Lines           []InvoiceLine    `json:"lines,omitempty"`
}

// AttachedCredit is the credit note of a transfer that is on an invoice.
type AttachedCredit struct {
	ID            int64  `json:"id"`
	Number        string `json:"number"`
	PaymentNumber string `json:"payment_number"`
	Amount        string `json:"amount"`
}

// InvoiceInput asks for one invoice over the given transfers.
type InvoiceInput struct {
	PaymentIDs []int64 `json:"payment_ids"`
	Notes      string  `json:"notes"`
}

func invoiceAudit(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "city_ledger_invoice", EntityID: id, Old: old, New: updated}
}

func errInvoiceNotFound() *apperr.Error {
	return apperr.NotFound("INVOICE_NOT_FOUND", "the invoice does not exist in this property")
}

func invoiceView(i cityledgerdb.CityLedgerInvoice, paid decimal.Decimal, adj adjustedAmounts, decimals int32) Invoice {
	out := Invoice{
		ID: i.ID, InvoiceNumber: i.InvoiceNumber, CompanyID: i.CompanyID, InvoiceDate: i.InvoiceDate, DueDate: i.DueDate, Total: i.Total.StringFixed(decimals),
		Notes: deref(i.Notes), Status: i.Status, VoidedAt: i.VoidedAt, VoidReason: deref(i.VoidReason), CreatedBy: i.CreatedBy, ApprovedBy: i.ApprovedBy,
	}
	settled := paid.Add(adj.credited).Add(adj.writtenOff)
	switch {
	case i.Status == InvoiceVoided:
		out.PaymentStatus, paid = "VOID", decimal.Zero
	case settled.GreaterThanOrEqual(i.Total) && adj.writtenOff.IsPositive():
		out.PaymentStatus = "WRITTEN_OFF"
	case settled.GreaterThanOrEqual(i.Total) && !paid.IsPositive():
		out.PaymentStatus = "CREDITED"
	case settled.GreaterThanOrEqual(i.Total):
		out.PaymentStatus = "PAID"
	case settled.IsPositive():
		out.PaymentStatus = "PARTIAL"
	default:
		out.PaymentStatus = "UNPAID"
	}
	out.Paid = paid.StringFixed(decimals)
	out.Subtotal, out.Credited, out.WrittenOff = i.Total.StringFixed(decimals), adj.credited.StringFixed(decimals), adj.writtenOff.StringFixed(decimals)
	if i.Status == InvoiceIssued {
		out.Outstanding = decimal.Max(i.Total.Sub(settled), decimal.Zero).StringFixed(decimals)
	} else {
		out.Outstanding = decimal.Zero.StringFixed(decimals)
	}
	return out
}

// paidOf returns what posted receipts have allocated to each of the invoices.
func (s *Service) paidOf(ctx context.Context, propertyID int64, ids []int64) (map[int64]decimal.Decimal, error) {
	out := map[int64]decimal.Decimal{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).InvoicePaid(ctx, cityledgerdb.InvoicePaidParams{PropertyID: propertyID, InvoiceIds: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.InvoiceID] = r.Paid
	}
	return out, nil
}

type adjustedAmounts struct{ credited, writtenOff decimal.Decimal }

// adjustedOf returns what posted credit notes and write-offs made against each of the invoices have taken off.
func (s *Service) adjustedOf(ctx context.Context, propertyID int64, ids []int64) (map[int64]adjustedAmounts, error) {
	out := map[int64]adjustedAmounts{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).InvoiceAdjusted(ctx, cityledgerdb.InvoiceAdjustedParams{PropertyID: propertyID, InvoiceIds: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.InvoiceID != nil {
			out[*r.InvoiceID] = adjustedAmounts{r.Credited, r.WrittenOff}
		}
	}
	return out, nil
}

func candidate(r cityledgerdb.ListInvoiceCandidatesRow, decimals int32) Candidate {
	return Candidate{
		InvoiceLine: InvoiceLine{
			PaymentID: r.ID, PaymentNumber: r.PaymentNumber, BusinessDate: r.BusinessDate, FolioNumber: r.FolioNumber, ConfirmationNumber: r.ConfirmationNumber,
			StayNumber: r.StayNumber, GuestName: r.GuestName, RoomNumbers: r.RoomNumbers, ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate,
			CheckedOutAt: r.CheckedOutAt, Reference: deref(r.ReferenceNumber), Amount: r.Amount.StringFixed(decimals),
		},
		StayStatus: r.StayStatus, Invoiceable: r.StayStatus == "CHECKED_OUT" && r.Amount.GreaterThan(r.Credited),
		Credited: r.Credited.StringFixed(decimals), Net: r.Amount.Sub(r.Credited).StringFixed(decimals),
	}
}

// Candidates lists a company's transfers that are not on a live invoice, oldest first; those whose guest has not
// checked out yet are marked not invoiceable.
func (s *Service) Candidates(ctx context.Context, propertyID, companyID int64) ([]Candidate, error) {
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
	rows, err := s.q(ctx).ListInvoiceCandidates(ctx, cityledgerdb.ListInvoiceCandidatesParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: &companyID})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, len(rows))
	for i, r := range rows {
		out[i] = candidate(r, decimals)
	}
	return out, nil
}

// Invoices lists a company's invoices, newest first, without their lines.
func (s *Service) Invoices(ctx context.Context, propertyID, companyID int64) ([]Invoice, error) {
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
	rows, err := s.q(ctx).ListInvoices(ctx, cityledgerdb.ListInvoicesParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: companyID})
	if err != nil {
		return nil, err
	}
	invoiceIDs := make([]int64, len(rows))
	for i, r := range rows {
		invoiceIDs[i] = r.ID
	}
	paid, err := s.paidOf(ctx, propertyID, invoiceIDs)
	if err != nil {
		return nil, err
	}
	adjusted, err := s.adjustedOf(ctx, propertyID, invoiceIDs)
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, len(rows))
	for i, r := range rows {
		out[i] = invoiceView(r, paid[r.ID], adjusted[r.ID], decimals)
	}
	return out, nil
}

func (s *Service) loadInvoice(ctx context.Context, tenantID, propertyID, id int64, decimals int32) (Invoice, error) {
	q := s.q(ctx)
	row, err := q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, errInvoiceNotFound()
	}
	if err != nil {
		return Invoice{}, err
	}
	lines, err := q.ListInvoiceLines(ctx, cityledgerdb.ListInvoiceLinesParams{TenantID: tenantID, PropertyID: propertyID, InvoiceID: id})
	if err != nil {
		return Invoice{}, err
	}
	paid, err := s.paidOf(ctx, propertyID, []int64{id})
	if err != nil {
		return Invoice{}, err
	}
	adjusted, err := s.adjustedOf(ctx, propertyID, []int64{id})
	if err != nil {
		return Invoice{}, err
	}
	out := invoiceView(row, paid[id], adjusted[id], decimals)
	notes, err := q.AttachedNotes(ctx, cityledgerdb.AttachedNotesParams{TenantID: tenantID, PropertyID: propertyID, InvoiceID: id})
	if err != nil {
		return Invoice{}, err
	}
	subtotal := decimal.Zero
	for _, l := range lines {
		subtotal = subtotal.Add(l.Amount)
	}
	out.Subtotal = subtotal.StringFixed(decimals)
	for _, n := range notes {
		out.AttachedCredits = append(out.AttachedCredits, AttachedCredit{ID: n.ID, Number: n.AdjustmentNumber, PaymentNumber: n.PaymentNumber, Amount: n.Amount.StringFixed(decimals)})
	}
	out.Lines = make([]InvoiceLine, len(lines))
	for i, l := range lines {
		out.Lines[i] = InvoiceLine{
			PaymentID: l.PaymentID, PaymentNumber: l.PaymentNumber, BusinessDate: l.BusinessDate, FolioNumber: l.FolioNumber, ConfirmationNumber: l.ConfirmationNumber,
			StayNumber: l.StayNumber, GuestName: l.GuestName, RoomNumbers: l.RoomNumbers, ArrivalDate: l.ArrivalDate, DepartureDate: l.DepartureDate,
			CheckedOutAt: l.CheckedOutAt, Reference: deref(l.ReferenceNumber), Amount: l.Amount.StringFixed(decimals),
		}
	}
	return out, nil
}

// GetInvoice returns an invoice with its lines (cityledger.read).
func (s *Service) GetInvoice(ctx context.Context, propertyID, id int64) (Invoice, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerRead)
	if err != nil {
		return Invoice{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Invoice{}, err
	}
	return s.loadInvoice(ctx, p.TenantID, propertyID, id, decimals)
}

// CreateInvoice issues one invoice over the given transfers of a company (cityledger.invoice). Every transfer must
// belong to the company, be posted, not be on another live invoice, and its guest must have checked out: otherwise
// 409 `TRANSFER_NOT_AVAILABLE` or `STAY_NOT_CHECKED_OUT` (context.payment_ids). The company row is locked (level 44),
// so two invoices cannot claim the same transfer; the unique index on live lines is the backstop. The due date is
// the invoice date plus the company's payment terms.
func (s *Service) CreateInvoice(ctx context.Context, propertyID, companyID int64, key string, in InvoiceInput) (Invoice, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerInvoice)
	if err != nil {
		return Invoice{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Invoice{}, err
	}
	ids := slices.Clone(in.PaymentIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	notes := strings.TrimSpace(in.Notes)
	var fields []apperr.FieldError
	if len(ids) == 0 || len(ids) > maxInvoiceLines {
		fields = append(fields, field("payment_ids", "OUT_OF_RANGE", "between 1 and 200 transfers"))
	}
	if slices.ContainsFunc(ids, func(id int64) bool { return id < 1 }) {
		fields = append(fields, field("payment_ids", "INVALID_VALUE", "positive ids"))
	}
	if len([]rune(notes)) > maxRemarksLen {
		fields = append(fields, field("notes", "TOO_LONG", "too long"))
	}
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return Invoice{}, apperr.Invalid("the invoice is invalid", fields...)
	}
	return receiptReplay(key,
		func() (Invoice, bool, error) {
			row, err := s.q(ctx).GetInvoiceByKey(ctx, cityledgerdb.GetInvoiceByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
			if errors.Is(err, pgx.ErrNoRows) {
				return Invoice{}, false, nil
			}
			if err != nil {
				return Invoice{}, false, err
			}
			if row.CompanyID != companyID {
				return Invoice{}, false, errKeyReused()
			}
			inv, err := s.loadInvoice(ctx, p.TenantID, propertyID, row.ID, decimals)
			return inv, true, err
		},
		func() (Invoice, error) {
			var out Invoice
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				if err := s.companies.Lock(ctx, propertyID, companyID); err != nil {
					return err
				}
				acc, err := s.loadAccount(ctx, p.TenantID, propertyID, companyID, decimals)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				rows, err := q.ListInvoiceCandidates(ctx, cityledgerdb.ListInvoiceCandidatesParams{TenantID: p.TenantID, PropertyID: propertyID, CompanyID: &companyID, Ids: ids})
				if err != nil {
					return err
				}
				found := map[int64]cityledgerdb.ListInvoiceCandidatesRow{}
				for _, r := range rows {
					found[r.ID] = r
				}
				var missing, waiting []int64
				for _, id := range ids {
					r, ok := found[id]
					switch {
					case !ok:
						missing = append(missing, id)
					case r.StayStatus != "CHECKED_OUT":
						waiting = append(waiting, id)
					}
				}
				if len(missing) > 0 {
					return apperr.Conflict("TRANSFER_NOT_AVAILABLE", "a transfer does not belong to this company, is voided, or is already on an invoice").WithContext("payment_ids", missing)
				}
				if len(waiting) > 0 {
					return apperr.Conflict("STAY_NOT_CHECKED_OUT", "only transfers of guests who have checked out can be invoiced").WithContext("payment_ids", waiting)
				}
				// the invoice asks the net: what the transfers say less the credit notes made against them
				total := decimal.Zero
				for _, r := range rows {
					total = total.Add(r.Amount).Sub(r.Credited)
				}
				if !total.IsPositive() {
					return apperr.Conflict("NOTHING_TO_INVOICE", "the credit notes take off all that these transfers say: there is nothing to invoice")
				}
				number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqCityLedgerInvoice)
				if err != nil {
					return err
				}
				inv, err := q.InsertInvoice(ctx, cityledgerdb.InsertInvoiceParams{
					TenantID: p.TenantID, PropertyID: propertyID, InvoiceNumber: number, CompanyID: companyID, InvoiceDate: day.BusinessDate,
					DueDate: day.BusinessDate.AddDays(acc.PaymentTermsDays), Total: total, Notes: nullable(notes), IdempotencyKey: &key, ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				for _, r := range rows {
					if err := q.InsertInvoiceLine(ctx, cityledgerdb.InsertInvoiceLineParams{TenantID: p.TenantID, PropertyID: propertyID, InvoiceID: inv.ID, PaymentID: r.ID, Amount: r.Amount}); err != nil {
						return err
					}
				}
				notes, err := q.UnattachedTransferNotes(ctx, cityledgerdb.UnattachedTransferNotesParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentIds: ids})
				if err != nil {
					return err
				}
				for _, n := range notes {
					if err := q.InsertAttachment(ctx, cityledgerdb.InsertAttachmentParams{TenantID: p.TenantID, PropertyID: propertyID, AdjustmentID: n.ID, InvoiceID: inv.ID}); err != nil {
						return err
					}
				}
				if out, err = s.loadInvoice(ctx, p.TenantID, propertyID, inv.ID, decimals); err != nil {
					return err
				}
				return s.audit.Write(ctx, invoiceAudit(p, propertyID, day.BusinessDate, "cityledger.invoice_issued", inv.ID, nil, map[string]any{
					"invoice_number": number, "company_id": companyID, "total": total.String(), "transfers": len(rows),
				}))
			})
			return out, err
		})
}

// VoidInvoice voids an invoice (cityledger.invoice plus an approval); its transfers can be invoiced again.
func (s *Service) VoidInvoice(ctx context.Context, propertyID, id int64, in VoidInput) (Invoice, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerInvoice)
	if err != nil {
		return Invoice{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return Invoice{}, apperr.Invalid("the void is invalid", field("reason", "REQUIRED", "a reason is required"))
	case len([]rune(reason)) > maxReasonLen:
		return Invoice{}, apperr.Invalid("the void is invalid", field("reason", "TOO_LONG", "too long"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Invoice{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvoiceNotFound()
		}
		if err != nil {
			return err
		}
		if err := s.companies.Lock(ctx, propertyID, pre.CompanyID); err != nil {
			return err
		}
		cur, err := q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return err
		}
		if cur.Status != InvoiceIssued {
			return apperr.Conflict("INVOICE_ALREADY_VOIDED", "the invoice is already voided")
		}
		if n, err := q.CountLiveAdjustmentsOfInvoice(ctx, cityledgerdb.CountLiveAdjustmentsOfInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, InvoiceID: &id}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("INVOICE_HAS_ADJUSTMENTS", "credit notes or write-offs are made against this invoice: void them first").WithContext("adjustments", n)
		}
		paid, err := s.paidOf(ctx, propertyID, []int64{id})
		if err != nil {
			return err
		}
		if paid[id].IsPositive() {
			return apperr.Conflict("INVOICE_HAS_PAYMENTS", "receipts have paid this invoice: void them first").WithContext("paid", paid[id].StringFixed(decimals))
		}
		by := approval.UserID()
		now := s.clock.Now()
		if _, err := q.VoidInvoice(ctx, cityledgerdb.VoidInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: now, ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by}); err != nil {
			return err
		}
		if err := q.ReleaseInvoiceLines(ctx, cityledgerdb.ReleaseInvoiceLinesParams{PropertyID: propertyID, InvoiceID: id, Now: now}); err != nil {
			return err
		}
		if err := q.ReleaseAttachments(ctx, cityledgerdb.ReleaseAttachmentsParams{PropertyID: propertyID, InvoiceID: id, Now: now}); err != nil {
			return err
		}
		if out, err = s.loadInvoice(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, invoiceAudit(p, propertyID, day.BusinessDate, "cityledger.invoice_voided", id,
			map[string]any{"status": cur.Status}, map[string]any{"status": InvoiceVoided, "reason": reason, "actor": p.ActorID(), "approved_by": by}))
	})
	return out, err
}
