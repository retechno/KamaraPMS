package payables

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

func toPayment(r payablesdb.ListSupplierPaymentsRow) SupplierPayment {
	return SupplierPayment{
		ID: r.ID, Number: r.PaymentNumber, SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName, PaymentDate: r.PaymentDate, Amount: r.Amount,
		PaymentMethod: r.PaymentMethod, ReferenceNumber: deref(r.ReferenceNumber), Remarks: deref(r.Remarks), Status: r.Status, JournalID: r.JournalID, JournalNumber: r.JournalNumber,
		VoidJournalID: r.VoidJournalID, VoidedAt: r.VoidedAt, VoidReason: deref(r.VoidReason), CreatedAt: r.CreatedAt,
	}
}

// Payments lists payments to suppliers, newest first (payables.view).
func (s *Service) Payments(ctx context.Context, propertyID int64, f PaymentFilter) ([]SupplierPayment, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return nil, err
	}
	return s.listPayments(ctx, p.TenantID, propertyID, nil, f)
}

func (s *Service) listPayments(ctx context.Context, tenantID, propertyID int64, id *int64, f PaymentFilter) ([]SupplierPayment, error) {
	rows, err := s.q(ctx).ListSupplierPayments(ctx, payablesdb.ListSupplierPaymentsParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, SupplierID: f.SupplierID, Status: nullable(f.Status), FromDate: f.From, ToDate: f.To, RowLimit: limitOf(f.Limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]SupplierPayment, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPayment(r))
	}
	return out, nil
}

// GetPayment is one payment with the bills it settles (payables.view).
func (s *Service) GetPayment(ctx context.Context, propertyID, id int64) (SupplierPayment, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesView)
	if err != nil {
		return SupplierPayment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}

func (s *Service) loadPayment(ctx context.Context, tenantID, propertyID, id int64) (SupplierPayment, error) {
	list, err := s.listPayments(ctx, tenantID, propertyID, &id, PaymentFilter{Limit: 1})
	if err != nil {
		return SupplierPayment{}, err
	}
	if len(list) == 0 {
		return SupplierPayment{}, errPaymentNotFound()
	}
	pay := list[0]
	rows, err := s.q(ctx).ListAllocations(ctx, payablesdb.ListAllocationsParams{TenantID: tenantID, PropertyID: propertyID, PaymentID: id})
	if err != nil {
		return SupplierPayment{}, err
	}
	pay.Allocations = make([]Allocation, 0, len(rows))
	for _, r := range rows {
		pay.Allocations = append(pay.Allocations, Allocation{BillID: r.BillID, BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber, Amount: r.Amount})
	}
	return pay, nil
}

func validatePayment(in PaymentInput, decimals int32) (decimal.Decimal, []apperr.FieldError) {
	var fields []apperr.FieldError
	if in.SupplierID < 1 {
		fields = append(fields, fieldErr("supplier_id", "REQUIRED", "choose a supplier"))
	}
	if in.PaymentDate.IsZero() {
		fields = append(fields, fieldErr("payment_date", "REQUIRED", "the date of the payment"))
	}
	if _, ok := methodAccount[in.PaymentMethod]; !ok {
		fields = append(fields, fieldErr("payment_method", "INVALID_METHOD", "CASH, BANK_TRANSFER or OTHER"))
	}
	if len([]rune(in.ReferenceNumber)) > 100 {
		fields = append(fields, fieldErr("reference_number", "TOO_LONG", "at most 100 characters"))
	}
	if len([]rune(in.Remarks)) > 500 {
		fields = append(fields, fieldErr("remarks", "TOO_LONG", "at most 500 characters"))
	}
	if len(in.Allocations) < 1 || len(in.Allocations) > maxAllocations {
		fields = append(fields, fieldErr("allocations", "INVALID_COUNT", "a payment settles between 1 and 100 bills"))
	}
	total := decimal.Zero
	seen := map[int64]bool{}
	for i, a := range in.Allocations {
		at := func(f string) string { return fmt.Sprintf("allocations[%d].%s", i, f) }
		switch {
		case a.BillID < 1:
			fields = append(fields, fieldErr(at("bill_id"), "REQUIRED", "choose the bill"))
		case seen[a.BillID]:
			fields = append(fields, fieldErr(at("bill_id"), "DUPLICATE", "the bill is listed twice"))
		}
		seen[a.BillID] = true
		switch {
		case !a.Amount.IsPositive():
			fields = append(fields, fieldErr(at("amount"), "NOT_POSITIVE", "an amount above zero"))
		case !a.Amount.Equal(a.Amount.Round(decimals)):
			fields = append(fields, fieldErr(at("amount"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", decimals)))
		}
		total = total.Add(a.Amount)
	}
	return total, fields
}

// PostPayment pays a supplier (payables.post): the payment is the sum of what it settles on bills of the supplier (an
// amount above what is owed on a bill is refused). Its journal debits ACCOUNTS PAYABLE against the cash, bank or other
// payment account. The Idempotency-Key makes a retry return the first payment.
func (s *Service) PostPayment(ctx context.Context, propertyID int64, in PaymentInput, key string) (SupplierPayment, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return SupplierPayment{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return SupplierPayment{}, err
	}
	total, fields := validatePayment(in, prop.CurrencyDecimals)
	if len(fields) > 0 {
		return SupplierPayment{}, apperr.Invalid("the payment is invalid", fields...)
	}
	var id int64
	err = retryOnDuplicate(key, func() error { return s.postPayment(ctx, p, propertyID, in, total, key, &id) })
	if err != nil {
		return SupplierPayment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}

func (s *Service) postPayment(ctx context.Context, p auth.Principal, propertyID int64, in PaymentInput, total decimal.Decimal, key string, out *int64) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.SupplierActive(ctx, payablesdb.SupplierActiveParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.SupplierID}); isNoRows(err) {
			return apperr.Invalid("the payment is invalid", fieldErr("supplier_id", "NOT_FOUND", "no such supplier in this property"))
		} else if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{in.SupplierID}); err != nil {
			return err
		}
		if key != "" {
			if prev, err := q.FindSupplierPaymentByKey(ctx, payablesdb.FindSupplierPaymentByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				*out = prev
				return nil
			} else if !isNoRows(err) {
				return err
			}
		}
		sup, err := s.loadSupplier(ctx, p.TenantID, propertyID, in.SupplierID)
		if err != nil {
			return err
		}
		open, err := q.OpenBillsOfSupplier(ctx, payablesdb.OpenBillsOfSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: in.SupplierID})
		if err != nil {
			return err
		}
		left := map[int64]decimal.Decimal{}
		for _, b := range open {
			left[b.ID] = b.Outstanding
		}
		var allocErrs []apperr.FieldError
		for i, a := range in.Allocations {
			at := fmt.Sprintf("allocations[%d].amount", i)
			o, ok := left[a.BillID]
			switch {
			case !ok:
				allocErrs = append(allocErrs, fieldErr(fmt.Sprintf("allocations[%d].bill_id", i), "NOT_PAYABLE", "not an open bill of this supplier"))
			case a.Amount.GreaterThan(o):
				allocErrs = append(allocErrs, fieldErr(at, "EXCEEDS_OUTSTANDING", "more than the "+o.String()+" still owed on the bill"))
			}
		}
		if len(allocErrs) > 0 {
			e := apperr.Conflict("ALLOCATION_EXCEEDS_OUTSTANDING", "the payment settles more than is owed")
			e.Fields = allocErrs
			return e
		}
		if err := po.CheckDate(ctx, in.PaymentDate, "payment_date"); err != nil {
			return err
		}
		payable, err := po.SystemAccount(ctx, accounting.KeyAccountsPayable)
		if err != nil {
			return err
		}
		from, err := po.SystemAccount(ctx, methodAccount[in.PaymentMethod])
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqSupplierPayment)
		if err != nil {
			return err
		}
		desc := "Payment " + number + " to " + sup.Name
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{
			Date: in.PaymentDate, Description: desc, Reference: number, Lines: []accounting.SystemLine{
				{AccountID: payable, Debit: total, Description: desc, SourceType: "AP_PAYMENT", SourceRef: number},
				{AccountID: from, Credit: total, Description: desc, SourceType: "AP_PAYMENT", SourceRef: number},
			},
		})
		if err != nil {
			return err
		}
		id, err := q.InsertSupplierPayment(ctx, payablesdb.InsertSupplierPaymentParams{
			TenantID: p.TenantID, PropertyID: propertyID, PaymentNumber: number, SupplierID: in.SupplierID, PaymentDate: in.PaymentDate, Amount: total, PaymentMethod: in.PaymentMethod,
			ReferenceNumber: nullable(in.ReferenceNumber), Remarks: nullable(in.Remarks), JournalID: jid, IdempotencyKey: nullable(key), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for _, a := range in.Allocations {
			if err := q.InsertAllocation(ctx, payablesdb.InsertAllocationParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: in.SupplierID, PaymentID: id, BillID: a.BillID, Amount: a.Amount}); err != nil {
				return err
			}
		}
		*out = id
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.payment_posted", "supplier_payment", id, nil,
			map[string]any{"payment_number": number, "supplier": sup.Code, "amount": total.String(), "method": in.PaymentMethod, "bills": len(in.Allocations), "journal": jnum}))
	})
}

// VoidPayment voids a payment (payables.post plus an approval): its journal is reversed on the current business date
// and the bills it settled are open again.
func (s *Service) VoidPayment(ctx context.Context, propertyID, id int64, in VoidInput) (SupplierPayment, error) {
	p, err := s.need(ctx, propertyID, auth.PermPayablesPost)
	if err != nil {
		return SupplierPayment{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return SupplierPayment{}, apperr.Invalid("the void is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return SupplierPayment{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		po, err := s.acct.BeginPosting(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		supplierID, err := q.PaymentSupplier(ctx, payablesdb.PaymentSupplierParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errPaymentNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Suppliers, db.ForUpdate, propertyID, []int64{supplierID}); err != nil {
			return err
		}
		pay, err := s.loadPayment(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if pay.Status != "POSTED" {
			return apperr.Conflict("PAYMENT_ALREADY_VOIDED", "the payment is voided already")
		}
		by := approval.UserID()
		rj, err := po.Reverse(ctx, pay.JournalID, day.BusinessDate, reason, by)
		if err != nil {
			return err
		}
		if err := q.VoidSupplierPayment(ctx, payablesdb.VoidSupplierPaymentParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: ptr(s.clock.Now()), ActorID: p.ActorID(), Reason: &reason, VoidJournalID: &rj, ApprovedBy: &by,
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "payables.payment_voided", "supplier_payment", id,
			map[string]any{"status": "POSTED"}, map[string]any{"status": "VOIDED", "reason": reason, "approved_by": by, "payment_number": pay.Number}))
	})
	if err != nil {
		return SupplierPayment{}, err
	}
	return s.loadPayment(ctx, p.TenantID, propertyID, id)
}
