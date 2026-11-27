package pos

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos/posdb"
)

// The return of a charge to a room after that guest checked out (docs/architecture/42-pos-charge-return-after-checkout.md, KD-26). One act,
// one approval ROOM_CHARGE_RETURN_CLOSED and one transaction: the bill is reopened if it is closed, the charge goes back to it, and the
// folio that settled the charge is settled again (a refund to the payer, or the credit stays on an open master). The bill is left OPEN with
// the amount, for the cashier to settle with the existing flows (R6).

// The cases of the return after check-out.
const (
	ReturnCaseRefund       = folios.ClosedReturnRefund
	ReturnCaseMasterCredit = folios.ClosedReturnMasterCredit
)

// ReturnClosedInput is the request: the reason and the approval, and the refund when the settling folio is closed.
type ReturnClosedInput struct {
	Reason   string              `json:"reason"`
	Approval *iam.ApprovalInput  `json:"approval"`
	Refund   *ReturnClosedRefund `json:"refund"`
}

// ReturnClosedRefund names the payment refunded and how the money goes back (empty method: the payment's own when the property refunds by
// it).
type ReturnClosedRefund struct {
	PaymentID       int64  `json:"payment_id"`
	PaymentMethod   string `json:"payment_method"`
	ReferenceNumber string `json:"reference_number"`
}

// ReturnClosedFolio is a folio of the return.
type ReturnClosedFolio struct {
	FolioID     int64           `json:"folio_id"`
	FolioNumber string          `json:"folio_number"`
	FolioType   string          `json:"folio_type"`
	Status      string          `json:"status"`
	Balance     decimal.Decimal `json:"balance"`
}

// ReturnClosedPayment is a payment the refund could go against.
type ReturnClosedPayment struct {
	PaymentID     int64           `json:"payment_id"`
	PaymentNumber string          `json:"payment_number"`
	PaymentMethod string          `json:"payment_method"`
	Amount        decimal.Decimal `json:"amount"`
	Refundable    decimal.Decimal `json:"refundable"`
	IsDeposit     bool            `json:"is_deposit"`
	BusinessDate  civil.Date      `json:"business_date"`
}

// ReturnRefusal says why the charge cannot be returned this way.
type ReturnRefusal struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Context map[string]any `json:"context,omitempty"`
}

// ReturnClosedPreview is what the return would do, before the approval is asked (§8).
type ReturnClosedPreview struct {
	ChargeID          int64                 `json:"charge_id"`
	Amount            decimal.Decimal       `json:"amount"`
	BillID            int64                 `json:"bill_id"`
	BillNumber        string                `json:"bill_number"`
	BillStatus        string                `json:"bill_status"`
	Holder            ReturnClosedFolio     `json:"holder"`
	Master            *ReturnClosedFolio    `json:"master"`
	Case              string                `json:"case"`
	Payments          []ReturnClosedPayment `json:"payments"`
	ProposedPaymentID *int64                `json:"proposed_payment_id"`
	RefundMethods     []string              `json:"refund_methods"`
	Refusal           *ReturnRefusal        `json:"refusal"`
}

// ReturnClosedRefundDone is the refund that was recorded.
type ReturnClosedRefundDone struct {
	PaymentID     int64           `json:"payment_id"`
	PaymentNumber string          `json:"payment_number"`
	PaymentMethod string          `json:"payment_method"`
	Amount        decimal.Decimal `json:"amount"`
	RefundOf      int64           `json:"refund_of_payment_id"`
}

// ReturnClosedResult is the answer: the bill, open with the amount, the folios settled again and the refund.
type ReturnClosedResult struct {
	Bill   Bill                    `json:"bill"`
	Holder ReturnClosedFolio       `json:"holder"`
	Master *ReturnClosedFolio      `json:"master"`
	Refund *ReturnClosedRefundDone `json:"refund"`
}

func returnFolio(f folios.ClosedReturnFolio) ReturnClosedFolio {
	return ReturnClosedFolio{FolioID: f.ID, FolioNumber: f.Number, FolioType: f.Type, Status: f.Status, Balance: f.Balance}
}

func (in *ReturnClosedRefund) ledger() *folios.ClosedRefund {
	if in == nil {
		return nil
	}
	return &folios.ClosedRefund{PaymentID: in.PaymentID, PaymentMethod: in.PaymentMethod, ReferenceNumber: in.ReferenceNumber}
}

// PreviewReturnClosed says which case applies to the charge, what would be refunded and what refuses it (pos.cashier and folio.reverse). It
// writes nothing.
func (s *Service) PreviewReturnClosed(ctx context.Context, propertyID, chargeID int64) (ReturnClosedPreview, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return ReturnClosedPreview{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermFolioReverse); err != nil {
		return ReturnClosedPreview{}, err
	}
	plan, err := s.ledger.PlanClosedReturn(ctx, p, propertyID, chargeID)
	if err != nil {
		return ReturnClosedPreview{}, err
	}
	if plan.BillID == nil {
		return ReturnClosedPreview{}, apperr.Conflict("ROOM_CHARGE_NO_BILL", "the charge has no bill to go back to")
	}
	b, err := s.billRow(ctx, p.TenantID, propertyID, *plan.BillID)
	if err != nil {
		return ReturnClosedPreview{}, err
	}
	out := ReturnClosedPreview{
		ChargeID: plan.ChargeID, Amount: plan.Amount, BillID: b.ID, BillNumber: deref(b.BillNumber), BillStatus: b.Status, Holder: returnFolio(plan.Holder), Case: plan.Case,
		Payments: []ReturnClosedPayment{}, ProposedPaymentID: plan.Proposed, RefundMethods: plan.RefundMethods,
	}
	if out.RefundMethods == nil {
		out.RefundMethods = []string{}
	}
	if plan.Master != nil {
		m := returnFolio(*plan.Master)
		out.Master = &m
	}
	for _, x := range plan.Payments {
		out.Payments = append(out.Payments, ReturnClosedPayment{PaymentID: x.ID, PaymentNumber: x.Number, PaymentMethod: x.Method, Amount: x.Amount, Refundable: x.Refundable, IsDeposit: x.IsDeposit, BusinessDate: x.BusinessDate})
	}
	if plan.Refusal != nil {
		out.Refusal = &ReturnRefusal{Code: plan.Refusal.Code, Message: plan.Refusal.Message, Context: plan.Refusal.Context}
	}
	return out, nil
}

// ReturnRoomChargeClosed returns a charge to a room whose folio is closed (the guest checked out) to its bill, in one transaction with one
// approval (§2): the bill is reopened if it is closed, the return pair is written, the folio that settled the charge is settled again, and the
// charge stops counting against the limit. The actor needs pos.cashier and folio.reverse, plus folio.reopen when the bill is closed and
// payment.refund for a refund, before the approval is asked. A folio settled by the city ledger is refused (case b, KD-29).
func (s *Service) ReturnRoomChargeClosed(ctx context.Context, propertyID, chargeID int64, in ReturnClosedInput) (ReturnClosedResult, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return ReturnClosedResult{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermFolioReverse); err != nil {
		return ReturnClosedResult{}, err
	}
	plan, err := s.ledger.PlanClosedReturn(ctx, p, propertyID, chargeID)
	if err != nil {
		return ReturnClosedResult{}, err
	}
	if plan.BillID == nil {
		return ReturnClosedResult{}, apperr.Conflict("ROOM_CHARGE_NO_BILL", "the charge has no bill to go back to")
	}
	billID := *plan.BillID
	pre, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return ReturnClosedResult{}, err
	}
	needs := func(status, settle string) []auth.Permission {
		var perms []auth.Permission
		if status == BillClosed {
			perms = append(perms, auth.PermFolioReopen)
		}
		if settle == ReturnCaseRefund {
			perms = append(perms, auth.PermPaymentRefund)
		}
		return perms
	}
	for _, perm := range needs(pre.Status, plan.Case) {
		if err := s.authz.Require(ctx, propertyID, perm); err != nil {
			return ReturnClosedResult{}, err
		}
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return ReturnClosedResult{}, invalid("return", fields...)
	}
	if plan.Refusal != nil {
		return ReturnClosedResult{}, plan.Refusal
	}
	if fields := folios.CheckClosedRefund(plan, in.Refund.ledger()); len(fields) > 0 {
		return ReturnClosedResult{}, invalid("return", fields...)
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindRoomChargeReturnClosed, Amount: plan.Amount})
	if err != nil {
		return ReturnClosedResult{}, err
	}
	var done folios.ClosedReturned
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // level 10
		if err != nil {
			return err
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, plan.FolioIDs()...); err != nil { // level 42, one call, ascending
			return err
		}
		// Under the locks the plan is read again: another return may have been made, or the folios may have moved meanwhile.
		again, err := s.ledger.PlanClosedReturn(ctx, p, propertyID, chargeID)
		if err != nil {
			return err
		}
		if again.Refusal != nil {
			return again.Refusal
		}
		if again.Case != plan.Case || !slices.Equal(again.FolioIDs(), plan.FolioIDs()) {
			return apperr.Conflict("RETURN_CHANGED", "the folios of the charge changed meanwhile: open the return again")
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		reopened := false
		switch b.Status {
		case BillOpen:
		case BillClosed:
			if err := s.authz.Require(ctx, propertyID, auth.PermFolioReopen); err != nil {
				return err
			}
			if _, err := s.ledger.ReopenBillFolio(ctx, p, propertyID, *b.FolioID); err != nil {
				return err
			}
			n, err := s.q(ctx).ReopenBill(ctx, posdb.ReopenBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, ActorID: p.ActorID()})
			if err != nil {
				return err
			}
			if n != 1 {
				return apperr.Conflict("BILL_NOT_CLOSED", "only a closed bill is reopened")
			}
			reopened = true
		default:
			return errBillNotOpen().WithContext("status", b.Status)
		}
		done, err = s.ledger.ReturnOutletRoomChargeClosed(ctx, p, propertyID, day.BusinessDate, chargeID, reason, approval, in.Refund.ledger())
		if err != nil {
			return err
		}
		fields := map[string]any{
			"charge_id": chargeID, "amount": done.Amount.String(), "reason": reason, "case": done.Case, "bill_reopened": reopened,
			"holder_folio_id": done.Holder.ID, "holder_folio_number": done.Holder.Number,
		}
		if done.Master != nil {
			fields["master_folio_id"], fields["master_folio_number"] = done.Master.ID, done.Master.Number
		}
		if done.Refund != nil {
			fields["refund_payment_id"], fields["refund_payment_number"], fields["refund_of_payment_id"], fields["refund_method"] = done.Refund.ID, done.Refund.PaymentNumber, *done.RefundOf, done.Refund.PaymentMethod
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.room_charge_returned_after_checkout", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(fields)))
	})
	if err != nil {
		return ReturnClosedResult{}, err
	}
	bill, err := s.loadBill(ctx, p, propertyID, billID)
	if err != nil {
		return ReturnClosedResult{}, err
	}
	out := ReturnClosedResult{Bill: bill, Holder: returnFolio(done.Holder)}
	if done.Master != nil {
		m := returnFolio(*done.Master)
		out.Master = &m
	}
	if done.Refund != nil {
		amount, err := decimal.NewFromString(done.Refund.Amount)
		if err != nil {
			return ReturnClosedResult{}, err
		}
		out.Refund = &ReturnClosedRefundDone{PaymentID: done.Refund.ID, PaymentNumber: done.Refund.PaymentNumber, PaymentMethod: done.Refund.PaymentMethod, Amount: amount, RefundOf: *done.RefundOf}
	}
	return out, nil
}
