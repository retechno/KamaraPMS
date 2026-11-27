package folios

import (
	"context"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The return of an outlet charge after check-out (docs/architecture/42-pos-charge-return-after-checkout.md, KD-26). The folio that holds the
// charge is CLOSED. The return pair is written to it anyway, with the rows that settle it again in the same transaction: a refund of one of its
// payments, or the balance pair from the master its balance went to. The folio stays CLOSED, at zero, and its close is not touched. The rule
// of the database (folios_closed_balanced, 00103) checks it at commit. This is the one exception to "a closed folio is never the source or the
// target of a transfer" (30 §4 item 7).

// The cases of a return after check-out.
const (
	ClosedReturnRefund       = "REFUND"        // the folio that settled the charge is closed: the payer is refunded (§3.a, §3.c c2)
	ClosedReturnMasterCredit = "MASTER_CREDIT" // the guest's balance went to the open master of its group: the credit stays on it (§3.c c1)
)

// ClosedReturnFolio is a folio of the return, with its balance.
type ClosedReturnFolio struct {
	ID      int64
	Number  string
	Type    string
	Status  string
	Balance decimal.Decimal
}

// ClosedReturnPayment is a payment of the settling folio that a refund could go against.
type ClosedReturnPayment struct {
	ID           int64
	Number       string
	Method       string
	Amount       decimal.Decimal
	Refundable   decimal.Decimal // the amount less its refunds
	IsDeposit    bool            // paid before the check-in (folio_item_gl): refused
	BusinessDate civil.Date
}

// ClosedReturnPlan is what a return after check-out would do, read from the ledger. Refusal is set when it cannot be done; nothing is written
// by a plan.
type ClosedReturnPlan struct {
	ChargeID      int64
	Amount        decimal.Decimal
	BillFolioID   int64
	BillID        *int64
	StayID        *int64
	Holder        ClosedReturnFolio
	Master        *ClosedReturnFolio // the master the holder's balance went to (one hop)
	Case          string
	Payments      []ClosedReturnPayment // of the settling folio, when it is a refund
	Proposed      *int64                // the last payment that is not a deposit and covers the amount
	RefundMethods []string
	Refusal       *apperr.Error
	batchID       int64 // the batch of the charge to the room, whose transfer is returned
}

// FolioIDs are the folios a return locks: the holder, the bill and the master.
func (pl ClosedReturnPlan) FolioIDs() []int64 {
	ids := []int64{pl.Holder.ID, pl.BillFolioID}
	if pl.Master != nil {
		ids = append(ids, pl.Master.ID)
	}
	return ids
}

// ClosedRefund names the payment refunded, and how the money goes back.
type ClosedRefund struct {
	PaymentID       int64
	PaymentMethod   string // empty: the payment's own when the property refunds by it, else the first method it allows
	ReferenceNumber string
}

// ClosedReturned is a return after check-out that was made.
type ClosedReturned struct {
	ChargeID, TransferID int64
	Amount               decimal.Decimal
	Case                 string
	Holder               ClosedReturnFolio
	Master               *ClosedReturnFolio
	Refund               *Payment
	RefundOf             *int64
	StayID, BillID       *int64
}

func errReturnHolderOpen() *apperr.Error {
	return apperr.Conflict("RETURN_HOLDER_OPEN", "the folio that holds the charge is open: use the return of the bill")
}

func errReturnChain() *apperr.Error {
	return apperr.Conflict("RETURN_CHAIN_UNSUPPORTED", "the balance of the folio moved on more than once: a return after check-out follows one move to the master of the group")
}

func errReturnCityLedger() *apperr.Error {
	return apperr.Conflict("RETURN_AFTER_CHECKOUT_CITY_LEDGER", "the folio was settled by a transfer to the city ledger: the accountant corrects it on the company's account")
}

func errRefundOfDeposit() *apperr.Error {
	return apperr.Conflict("REFUND_OF_DEPOSIT_AFTER_CLOSE", "a payment made as a deposit is not refunded after its folio closed: refund another payment of the folio")
}

func errRefundExceeds(refundable decimal.Decimal, decimals int32) *apperr.Error {
	return apperr.Conflict("REFUND_EXCEEDS_PAYMENT", "the refund is more than what is left of the payment").WithContext("refundable", fixed(refundable, decimals))
}

func (s *Service) returnFolio(ctx context.Context, propertyID int64, f foliosdb.Folio) (ClosedReturnFolio, error) {
	bal, _, err := s.balanceOf(ctx, propertyID, f.ID)
	return ClosedReturnFolio{ID: f.ID, Number: f.FolioNumber, Type: f.FolioType, Status: f.Status, Balance: bal}, err
}

// PlanClosedReturn reads what a return after check-out of the outlet charge would do (§2, §3), for the preview and, under the locks, for the
// return itself. A charge that cannot be returned this way answers a plan with its Refusal.
func (s *Service) PlanClosedReturn(ctx context.Context, p auth.Principal, propertyID, chargeID int64) (ClosedReturnPlan, error) {
	q := s.q(ctx)
	ch, err := q.GetRoomCharge(ctx, foliosdb.GetRoomChargeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID})
	if err != nil {
		return ClosedReturnPlan{}, orNotFound(err, apperr.NotFound("ROOM_CHARGE_NOT_FOUND", "the outlet charge does not exist in this property"))
	}
	if ch.FromFolioID == nil || ch.TransferBatchID == nil {
		return ClosedReturnPlan{}, apperr.Conflict("ROOM_CHARGE_NO_BILL", "the charge has no bill to go back to")
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ClosedReturnPlan{}, err
	}
	plan := ClosedReturnPlan{ChargeID: ch.ID, Amount: ch.Amount, BillFolioID: *ch.FromFolioID, BillID: ch.BillID, StayID: ch.StayID, batchID: *ch.TransferBatchID}
	refuse := func(e *apperr.Error) (ClosedReturnPlan, error) {
		plan.Refusal = e
		return plan, nil
	}
	holder, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ch.ToFolioID})
	if err != nil {
		return ClosedReturnPlan{}, orNotFound(err, errFolioNotFound())
	}
	if plan.Holder, err = s.returnFolio(ctx, propertyID, holder); err != nil {
		return ClosedReturnPlan{}, err
	}
	if ch.ReturnedAt != nil {
		return refuse(apperr.Conflict("ALREADY_RETURNED", "the charge has been returned already").WithContext("charge_id", chargeID))
	}
	if holder.Status == "OPEN" {
		return refuse(errReturnHolderOpen())
	}
	// The settling folio: the holder, or the master its balance went to (one hop, §3.c).
	outs, err := q.ListLiveBalanceTransfersFrom(ctx, foliosdb.ListLiveBalanceTransfersFromParams{PropertyID: propertyID, FolioID: holder.ID})
	if err != nil {
		return ClosedReturnPlan{}, err
	}
	settling := holder
	if len(outs) > 0 {
		to := outs[0].ToFolioID
		for _, o := range outs {
			if o.ToFolioID != to || o.ToFolioType != FolioTypeMaster {
				return refuse(errReturnChain())
			}
		}
		master, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: to})
		if err != nil {
			return ClosedReturnPlan{}, orNotFound(err, errFolioNotFound())
		}
		// the master's own balance moves: only the ones back to this holder (the earlier returns of this use case) are part of the hop
		mouts, err := q.ListLiveBalanceTransfersFrom(ctx, foliosdb.ListLiveBalanceTransfersFromParams{PropertyID: propertyID, FolioID: master.ID})
		if err != nil {
			return ClosedReturnPlan{}, err
		}
		for _, o := range mouts {
			if o.ToFolioID != holder.ID {
				return refuse(errReturnChain())
			}
		}
		mv, err := s.returnFolio(ctx, propertyID, master)
		if err != nil {
			return ClosedReturnPlan{}, err
		}
		plan.Master, settling = &mv, master
	}
	cl, err := q.HasLiveCityLedgerPayment(ctx, foliosdb.HasLiveCityLedgerPaymentParams{PropertyID: propertyID, FolioID: settling.ID})
	if err != nil {
		return ClosedReturnPlan{}, err
	}
	if cl {
		return refuse(errReturnCityLedger())
	}
	if settling.Status == "OPEN" { // an open master keeps the credit
		plan.Case = ClosedReturnMasterCredit
		return plan, nil
	}
	plan.Case = ClosedReturnRefund
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return ClosedReturnPlan{}, err
	}
	plan.RefundMethods = prop.RefundMethods
	rows, err := q.ListClosedReturnPayments(ctx, foliosdb.ListClosedReturnPaymentsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: settling.ID})
	if err != nil {
		return ClosedReturnPlan{}, err
	}
	plan.Payments = make([]ClosedReturnPayment, 0, len(rows))
	deposit, most := false, decimal.Zero
	for _, r := range rows {
		plan.Payments = append(plan.Payments, ClosedReturnPayment{ID: r.ID, Number: r.PaymentNumber, Method: r.PaymentMethod, Amount: r.Amount, Refundable: r.Refundable, IsDeposit: r.IsDeposit, BusinessDate: r.BusinessDate})
		covers := !r.Refundable.LessThan(plan.Amount)
		switch {
		case r.IsDeposit && covers:
			deposit = true
		case !r.IsDeposit:
			if covers {
				id := r.ID
				plan.Proposed = &id // the last one that covers it
			}
			most = decimal.Max(most, r.Refundable)
		}
	}
	if plan.Proposed == nil {
		if deposit {
			return refuse(errRefundOfDeposit())
		}
		return refuse(errRefundExceeds(most, decimals))
	}
	return plan, nil
}

// CheckClosedRefund verifies the refund of a request against the plan, before the approval is asked: a refund is named exactly when the
// settling folio is closed, by a method the property refunds by.
func CheckClosedRefund(plan ClosedReturnPlan, refund *ClosedRefund) []apperr.FieldError {
	var fields []apperr.FieldError
	switch {
	case plan.Case == ClosedReturnRefund && (refund == nil || refund.PaymentID < 1):
		fields = append(fields, fieldErr("refund.payment_id", "REQUIRED", "the payment refunded to the payer"))
	case plan.Case != ClosedReturnRefund && refund != nil:
		fields = append(fields, fieldErr("refund", "NOT_ALLOWED", "the credit stays on the open master: nothing is refunded"))
	}
	if refund != nil {
		if m := strings.TrimSpace(refund.PaymentMethod); m != "" {
			if fe := validateMethod(m); fe != nil {
				fields = append(fields, fieldErr("refund.payment_method", fe.Code, fe.Message))
			} else if plan.RefundMethods != nil && !slices.Contains(plan.RefundMethods, m) {
				fields = append(fields, fieldErr("refund.payment_method", "NOT_ALLOWED", "this property refunds by "+strings.Join(plan.RefundMethods, ", ")+" only"))
			}
		}
		fields = append(fields, validateText("refund.reference_number", refund.ReferenceNumber, maxReferenceLen)...)
	}
	return fields
}

// ReturnOutletRoomChargeClosed makes the return after check-out (§2): the return pair between the closed holder and the bill, the balance pair
// from the master when the holder's balance went to it, the refund when the settling folio is closed, the charge marked returned. The caller
// holds the business day, has locked the plan's folios (one call, level 42), has reopened the bill and has verified the approval
// ROOM_CHARGE_RETURN_CLOSED. The plan is read again here, under the locks.
func (s *Service) ReturnOutletRoomChargeClosed(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, chargeID int64, reason string, approval iam.Approval, refund *ClosedRefund) (ClosedReturned, error) {
	plan, err := s.PlanClosedReturn(ctx, p, propertyID, chargeID)
	if err != nil {
		return ClosedReturned{}, err
	}
	if plan.Refusal != nil {
		return ClosedReturned{}, plan.Refusal
	}
	if fields := CheckClosedRefund(plan, refund); len(fields) > 0 {
		return ClosedReturned{}, apperr.Invalid("the return is invalid", fields...)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ClosedReturned{}, err
	}
	q := s.q(ctx)
	get := func(id int64) (foliosdb.Folio, error) {
		f, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		return f, orNotFound(err, errFolioNotFound())
	}
	holder, err := get(plan.Holder.ID)
	if err != nil {
		return ClosedReturned{}, err
	}
	bill, err := get(plan.BillFolioID)
	if err != nil {
		return ClosedReturned{}, err
	}
	if err := requireOpen(bill); err != nil {
		return ClosedReturned{}, err
	}
	settling := holder
	var master foliosdb.Folio
	if plan.Master != nil {
		if master, err = get(plan.Master.ID); err != nil {
			return ClosedReturned{}, err
		}
		settling = master
	}

	// The payment refunded, under its lock (level 43): what is left of it is read again, so two returns never refund more than it.
	var refundOf int64
	var method string
	if plan.Case == ClosedReturnRefund {
		refundOf = refund.PaymentID
		if err := db.LockRows(ctx, db.Payments, db.ForUpdate, propertyID, []int64{refundOf}); err != nil {
			return ClosedReturned{}, mapNotFound(err, errPaymentNotFound())
		}
		rows, err := q.ListClosedReturnPayments(ctx, foliosdb.ListClosedReturnPaymentsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: settling.ID})
		if err != nil {
			return ClosedReturned{}, err
		}
		i := slices.IndexFunc(rows, func(r foliosdb.ListClosedReturnPaymentsRow) bool { return r.ID == refundOf })
		if i < 0 {
			return ClosedReturned{}, apperr.Conflict("PAYMENT_NOT_REFUNDABLE", "the payment is not one of the folio's that can be refunded").WithContext("folio_number", settling.FolioNumber)
		}
		pay := rows[i]
		if pay.IsDeposit {
			return ClosedReturned{}, errRefundOfDeposit()
		}
		if pay.Refundable.LessThan(plan.Amount) {
			return ClosedReturned{}, errRefundExceeds(pay.Refundable, decimals)
		}
		method = strings.TrimSpace(refund.PaymentMethod)
		if method == "" { // as any refund: the payment's own method when the property refunds by it, else the first it allows
			method = plan.RefundMethods[0]
			if slices.Contains(plan.RefundMethods, pay.PaymentMethod) {
				method = pay.PaymentMethod
			}
		}
	}

	// The return pair of 30 §4.5, from the closed holder to the bill.
	rows, err := q.ListBatchTransfers(ctx, foliosdb.ListBatchTransfersParams{PropertyID: propertyID, BatchID: plan.batchID})
	if err != nil {
		return ClosedReturned{}, err
	}
	if len(rows) != 1 {
		return ClosedReturned{}, errTransferInvalid("the outlet charge has no transfer")
	}
	t := rows[0]
	if _, err := q.GetTransferReturnedBy(ctx, foliosdb.GetTransferReturnedByParams{PropertyID: propertyID, TransferID: &t.ID}); err == nil {
		return ClosedReturned{}, apperr.Conflict("ALREADY_RETURNED", "the transfer has been returned already").WithContext("transfer_id", t.ID)
	}
	by := approval.UserID()
	batch, err := q.InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
		TenantID: p.TenantID, PropertyID: propertyID, Kind: KindBalance, IsReturn: true, FromFolioID: holder.ID, ToFolioID: bill.ID, Scope: ScopeReturn, Reason: reason,
		BusinessDate: bd, ApprovedBy: ptr(by), ActorID: p.ActorID(),
	})
	if err != nil {
		return ClosedReturned{}, err
	}
	tid := t.ID
	moved, err := s.moveBalance(ctx, movement{p: p, propertyID: propertyID, bd: bd, src: holder, tgt: bill, batchID: batch.ID, approval: approval, reason: reason, scope: ScopeReturn, allowCredit: true}, t.Amount, decimals, &tid)
	if err != nil {
		return ClosedReturned{}, err
	}
	n, err := q.MarkRoomChargeReturned(ctx, foliosdb.MarkRoomChargeReturnedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID, ReturnedAt: s.clock.Now(), TransferID: &moved.TransferID})
	if err != nil {
		return ClosedReturned{}, err
	}
	if n != 1 {
		return ClosedReturned{}, apperr.Conflict("ALREADY_RETURNED", "the charge has been returned already").WithContext("charge_id", chargeID)
	}

	// The holder's credit goes to the master its balance went to: a balance pair from the master back to the holder (§3.c).
	if plan.Master != nil {
		mb, err := q.InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
			TenantID: p.TenantID, PropertyID: propertyID, Kind: KindBalance, FromFolioID: master.ID, ToFolioID: holder.ID, Scope: ScopeGroupMaster, Reason: reason,
			BusinessDate: bd, ApprovedBy: ptr(by), ActorID: p.ActorID(),
		})
		if err != nil {
			return ClosedReturned{}, err
		}
		if _, err := s.moveBalance(ctx, movement{p: p, propertyID: propertyID, bd: bd, src: master, tgt: holder, batchID: mb.ID, approval: approval, reason: reason, scope: ScopeGroupMaster, allowCredit: true}, t.Amount, decimals, nil); err != nil {
			return ClosedReturned{}, err
		}
	}

	out := ClosedReturned{ChargeID: chargeID, TransferID: moved.TransferID, Amount: t.Amount, Case: plan.Case, StayID: plan.StayID, BillID: plan.BillID}
	// The settling folio is closed: the payer is refunded at once (R2), so it is back at zero.
	if plan.Case == ClosedReturnRefund {
		pay, err := s.posting(p, propertyID, bd, settling).createPayment(ctx, PaymentTypeRefund, method, t.Amount, PaymentInput{ReferenceNumber: strings.TrimSpace(refund.ReferenceNumber)}, &refundOf, "", &by, reason)
		if err != nil {
			return ClosedReturned{}, err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "payment.refunded", "payment", pay.ID, auditlabel.Payment(ctx, propertyID, pay.ID), nil, approval.AuditFields(map[string]any{
			"payment_number": pay.PaymentNumber, "refund_of_payment_id": refundOf, "amount": pay.Amount.String(), "reason": reason, "actor": p.ActorID(),
			"room_charge_id": chargeID,
		}))); err != nil {
			return ClosedReturned{}, err
		}
		res, err := s.paymentResult(ctx, p.TenantID, propertyID, pay)
		if err != nil {
			return ClosedReturned{}, err
		}
		out.Refund, out.RefundOf = &res.Payment, &refundOf
	}
	if out.Holder, err = s.returnFolio(ctx, propertyID, holder); err != nil {
		return ClosedReturned{}, err
	}
	if plan.Master != nil {
		mv, err := s.returnFolio(ctx, propertyID, master)
		if err != nil {
			return ClosedReturned{}, err
		}
		out.Master = &mv
	}
	return out, nil
}
