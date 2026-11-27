package cityledger

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/cityledger/cityledgerdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Allocating a receipt after it is recorded, and releasing a wrong allocation (docs/architecture/41-receipt-allocation.md, KD-27).
// An allocation is a matching inside the city ledger: it posts no journal, because the day close journals the receipt itself.

func errAllocationNotFound() *apperr.Error {
	return apperr.NotFound("ALLOCATION_NOT_FOUND", "the allocation does not exist in this property")
}

// Allocate takes what is left of a posted receipt, of any day, to issued invoices of its company (cityledger.receive). The
// allocations are dated the current business date. The company lock (level 44) serialises it with every receipt, invoice, credit
// note and write-off of the company, so neither the receipt nor an invoice is ever allocated above its amount.
func (s *Service) Allocate(ctx context.Context, propertyID, receiptID int64, key string, in AllocateInput) (ReceiptResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReceive)
	if err != nil {
		return ReceiptResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ReceiptResult{}, err
	}
	allocations, fields := parseAllocations(in.Allocations, decimal.Zero, decimals)
	if len(in.Allocations) == 0 {
		fields = append(fields, field("allocations", "REQUIRED", "at least one invoice"))
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 100 {
		fields = append(fields, field("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len(fields) > 0 {
		return ReceiptResult{}, apperr.Invalid("the allocation is invalid", fields...)
	}
	return receiptReplay(key,
		func() (ReceiptResult, bool, error) {
			return s.allocationReplay(ctx, p, propertyID, receiptID, key, decimals)
		},
		func() (ReceiptResult, error) {
			var out ReceiptResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				pre, err := q.GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID})
				if errors.Is(err, pgx.ErrNoRows) {
					return errReceiptNotFound()
				}
				if err != nil {
					return err
				}
				owes, err := s.companies.LockForReceipt(ctx, propertyID, pre.CompanyID)
				if err != nil {
					return err
				}
				// under the lock: a request with the same key that committed meanwhile is answered as it was
				if res, ok, err := s.allocationReplay(ctx, p, propertyID, receiptID, key, decimals); err != nil || ok {
					out = res
					if ok {
						return errReplayed
					}
					return err
				}
				r, err := q.GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID})
				if err != nil {
					return err
				}
				if r.Status != ReceiptPosted {
					return apperr.Conflict("RECEIPT_ALREADY_VOIDED", "a voided receipt pays no invoice")
				}
				allocated, err := q.ReceiptAllocated(ctx, cityledgerdb.ReceiptAllocatedParams{PropertyID: propertyID, ReceiptID: receiptID})
				if err != nil {
					return err
				}
				before := r.Amount.Sub(allocated)
				total := decimal.Zero
				for _, a := range allocations {
					total = total.Add(a.amount)
				}
				if total.GreaterThan(before) {
					return apperr.Conflict("ALLOCATION_EXCEEDS_UNALLOCATED", "the allocations are more than is left of the receipt").WithContext("unallocated", before.StringFixed(decimals))
				}
				if err := s.allocate(ctx, p, propertyID, r.CompanyID, receiptID, day.BusinessDate, &key, allocations, decimals); err != nil {
					return err
				}
				alloc, err := s.allocationsOf(ctx, p.TenantID, propertyID, r.CompanyID, decimals)
				if err != nil {
					return err
				}
				invoices := make([]map[string]any, 0, len(allocations))
				for _, a := range alloc[receiptID].list {
					for _, x := range allocations {
						if a.InvoiceID == x.invoiceID && a.ReleasedAt == nil && a.AllocatedOn.Equal(day.BusinessDate) {
							invoices = append(invoices, map[string]any{"invoice_number": a.InvoiceNumber, "amount": a.Amount})
							break
						}
					}
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cityledger.receipt_allocated", receiptID, auditlabel.CityLedgerReceipt(ctx, propertyID, receiptID),
					map[string]any{"unallocated": before.String()},
					map[string]any{"invoices": invoices, "total": total.String(), "unallocated": before.Sub(total).String(), "actor": p.ActorID()})); err != nil {
					return err
				}
				out = ReceiptResult{Receipt: receiptView(r, alloc[receiptID], decimals), Balance: owes.StringFixed(decimals)}
				return nil
			})
			if errors.Is(err, errReplayed) {
				return out, nil
			}
			return out, err
		})
}

// errReplayed ends a transaction whose request was answered by the replay under the lock (nothing was written).
var errReplayed = errors.New("replayed")

// allocationReplay answers a key used before: the same receipt again, another receipt is a reuse of the key.
func (s *Service) allocationReplay(ctx context.Context, p auth.Principal, propertyID, receiptID int64, key string, decimals int32) (ReceiptResult, bool, error) {
	q := s.q(ctx)
	used, err := q.AllocationKeyReceipt(ctx, cityledgerdb.AllocationKeyReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceiptResult{}, false, nil
	}
	if err != nil {
		return ReceiptResult{}, false, err
	}
	if used != receiptID {
		return ReceiptResult{}, false, errKeyReused()
	}
	res, err := s.receiptResult(ctx, p, propertyID, receiptID, decimals)
	return res, err == nil, err
}

// receiptResult is a receipt with its allocations and the balance of its company.
func (s *Service) receiptResult(ctx context.Context, p auth.Principal, propertyID, receiptID int64, decimals int32) (ReceiptResult, error) {
	r, err := s.q(ctx).GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: receiptID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceiptResult{}, errReceiptNotFound()
	}
	if err != nil {
		return ReceiptResult{}, err
	}
	acc, err := s.loadAccount(ctx, p.TenantID, propertyID, r.CompanyID, decimals)
	if err != nil {
		return ReceiptResult{}, err
	}
	alloc, err := s.allocationsOf(ctx, p.TenantID, propertyID, r.CompanyID, decimals)
	if err != nil {
		return ReceiptResult{}, err
	}
	return ReceiptResult{Receipt: receiptView(r, alloc[r.ID], decimals), Balance: acc.Balance}, nil
}

// ReleaseAllocation ends one allocation (cityledger.receive plus an approval of kind CITY_LEDGER_VOID for its amount, and a
// reason), on any day, while its receipt is posted and its invoice issued: the invoice owes the amount again and it goes back to
// what is left of the receipt. No journal.
func (s *Service) ReleaseAllocation(ctx context.Context, propertyID, allocationID int64, in VoidInput) (ReceiptResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerReceive)
	if err != nil {
		return ReceiptResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return ReceiptResult{}, apperr.Invalid("the release is invalid", field("reason", "REQUIRED", "a reason is required"))
	case len([]rune(reason)) > maxReasonLen:
		return ReceiptResult{}, apperr.Invalid("the release is invalid", field("reason", "TOO_LONG", "too long"))
	}
	pre, err := s.q(ctx).GetAllocation(ctx, cityledgerdb.GetAllocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: allocationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceiptResult{}, errAllocationNotFound()
	}
	if err != nil {
		return ReceiptResult{}, err
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindCityLedgerVoid, Amount: pre.Amount})
	if err != nil {
		return ReceiptResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ReceiptResult{}, err
	}
	var out ReceiptResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		owes, err := s.companies.LockForReceipt(ctx, propertyID, pre.CompanyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		a, err := q.GetAllocation(ctx, cityledgerdb.GetAllocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: allocationID})
		if err != nil {
			return err
		}
		r, err := q.GetReceipt(ctx, cityledgerdb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: a.ReceiptID})
		if err != nil {
			return err
		}
		inv, err := q.GetInvoice(ctx, cityledgerdb.GetInvoiceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: a.InvoiceID})
		if err != nil {
			return err
		}
		switch {
		case a.ReleasedAt != nil:
			return apperr.Conflict("ALLOCATION_ALREADY_RELEASED", "the allocation is released already")
		case r.Status != ReceiptPosted:
			return apperr.Conflict("RECEIPT_ALREADY_VOIDED", "the receipt is voided: its allocations pay nothing already")
		case inv.Status != InvoiceIssued:
			return apperr.Conflict("INVOICE_NOT_PAYABLE", "the invoice is voided").WithContext("invoice_id", inv.ID)
		}
		by, now, on := approval.UserID(), s.clock.Now(), day.BusinessDate
		if _, err := q.ReleaseAllocation(ctx, cityledgerdb.ReleaseAllocationParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: allocationID, Now: &now, ReleasedOn: &on, ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by,
		}); errors.Is(err, pgx.ErrNoRows) {
			return apperr.Conflict("ALLOCATION_ALREADY_RELEASED", "the allocation is released already")
		} else if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cityledger.allocation_released", r.ID, auditlabel.CityLedgerReceipt(ctx, propertyID, r.ID),
			map[string]any{"allocation_id": allocationID, "invoice_number": inv.InvoiceNumber, "amount": a.Amount.String(), "allocated_on": a.AllocatedOn},
			approval.AuditFields(map[string]any{"released": true, "reason": reason, "actor": p.ActorID()}))); err != nil {
			return err
		}
		alloc, err := s.allocationsOf(ctx, p.TenantID, propertyID, r.CompanyID, decimals)
		if err != nil {
			return err
		}
		out = ReceiptResult{Receipt: receiptView(r, alloc[r.ID], decimals), Balance: owes.StringFixed(decimals)}
		return nil
	})
	return out, err
}
