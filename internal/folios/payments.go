package folios

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/tenancy"
)

// This file is PaymentService (docs/architecture/03-financial-engines.md step 11).

func (s *Service) paymentView(ctx context.Context, propertyID int64, pay foliosdb.Payment, decimals int32) (Payment, error) {
	v := Payment{
		ID: pay.ID, PaymentNumber: pay.PaymentNumber, FolioID: pay.FolioID, PaymentType: pay.PaymentType, PaymentMethod: pay.PaymentMethod, CompanyID: pay.CompanyID,
		Amount: fixed(pay.Amount, decimals), PaidAt: pay.PaidAt, BusinessDate: pay.BusinessDate, ReferenceNumber: deref(pay.ReferenceNumber),
		RefundOfPaymentID: pay.RefundOfPaymentID, Status: pay.Status, VoidedAt: pay.VoidedAt, VoidReason: deref(pay.VoidReason),
		Remarks: deref(pay.Remarks), CreatedBy: pay.CreatedBy, ApprovedBy: pay.ApprovedBy,
	}
	if pay.MdrRate != nil && pay.MdrFee != nil {
		rate, fee := pay.MdrRate.String(), fixed(*pay.MdrFee, decimals)
		v.MDRRate, v.MDRFee, v.ExpectedSettlementDate = &rate, &fee, pay.ExpectedSettlementDate
	}
	if pay.PaymentType == PaymentTypePayment && pay.Status == PaymentPosted {
		sum, err := s.q(ctx).SumRefundsOf(ctx, foliosdb.SumRefundsOfParams{PropertyID: propertyID, PaymentID: &pay.ID})
		if err != nil {
			return Payment{}, err
		}
		r := fixed(pay.Amount.Sub(sum.Refunded), decimals)
		v.Refundable = &r
	}
	return v, nil
}

// paymentResult loads a payment, its ledger entry and the folio's balance.
func (s *Service) paymentResult(ctx context.Context, tenantID, propertyID int64, pay foliosdb.Payment) (PaymentResult, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	pv, err := s.paymentView(ctx, propertyID, pay, decimals)
	if err != nil {
		return PaymentResult{}, err
	}
	entry, err := s.q(ctx).GetItemOfPayment(ctx, foliosdb.GetItemOfPaymentParams{TenantID: tenantID, PropertyID: propertyID, PaymentID: &pay.ID})
	if err != nil {
		return PaymentResult{}, err
	}
	iv, err := s.itemView(ctx, tenantID, propertyID, pay.FolioID, entry.ID, decimals)
	if err != nil {
		return PaymentResult{}, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, pay.FolioID)
	return PaymentResult{Payment: pv, FolioItem: iv, FolioBalance: fixed(balance, decimals)}, err
}

func (s *Service) paymentReplay(ctx context.Context, tenantID, propertyID int64, key string, folioID int64, ok func(foliosdb.Payment) bool) (PaymentResult, bool, error) {
	pay, err := s.q(ctx).GetPaymentByKey(ctx, foliosdb.GetPaymentByKeyParams{TenantID: tenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentResult{}, false, nil
		}
		return PaymentResult{}, false, err
	}
	if (folioID != 0 && pay.FolioID != folioID) || !ok(pay) {
		return PaymentResult{}, false, errKeyReused()
	}
	res, err := s.paymentResult(ctx, tenantID, propertyID, pay)
	return res, true, err
}

// createPayment writes a payments row (number from the sequence, last in the lock order) and its ledger entry.
func (ps posting) createPayment(ctx context.Context, payType, method string, amount decimal.Decimal, in PaymentInput, refundOf *int64, key string, approvedBy *int64, reason string) (foliosdb.Payment, error) {
	var shiftID *int64
	if method == methodCash && ps.s.shifts != nil {
		var err error
		if shiftID, err = ps.s.shifts.CashShift(ctx, ps.propertyID); err != nil {
			return foliosdb.Payment{}, err
		}
	}
	var mdrRate, mdrFee *decimal.Decimal
	var settleOn *civil.Date
	if payType == PaymentTypePayment && (method == "CARD" || method == "OTHER") {
		rule, err := ps.s.q(ctx).CardFeeRule(ctx, foliosdb.CardFeeRuleParams{TenantID: ps.p.TenantID, PropertyID: ps.propertyID, PaymentMethod: method, OnDate: ps.bd})
		switch {
		case err == nil:
			decimals, derr := ps.s.decimals(ctx, ps.propertyID)
			if derr != nil {
				return foliosdb.Payment{}, derr
			}
			fee, on := money.Percent(amount, rule.MdrRate, decimals), ps.bd.AddDays(int(rule.SettlementDays))
			mdrRate, mdrFee, settleOn = &rule.MdrRate, &fee, &on // the rate that applies today is kept with the payment
		case !errors.Is(err, pgx.ErrNoRows):
			return foliosdb.Payment{}, err
		}
	}
	number, err := ps.s.days.NextDocumentNumber(ctx, ps.propertyID, tenancy.SeqPayment)
	if err != nil {
		return foliosdb.Payment{}, err
	}
	remarks := in.Remarks
	if reason != "" {
		remarks = reason
	}
	pay, err := ps.s.q(ctx).InsertPayment(ctx, foliosdb.InsertPaymentParams{
		TenantID: ps.p.TenantID, PropertyID: ps.propertyID, PaymentNumber: number, FolioID: ps.folio.ID, PaymentType: payType, PaymentMethod: method,
		Amount: amount, PaidAt: ps.at, BusinessDate: ps.bd, ReferenceNumber: nullable(in.ReferenceNumber), RefundOfPaymentID: refundOf,
		IdempotencyKey: nullable(key), Remarks: nullable(remarks), ActorID: ps.p.ActorID(), ApprovedBy: approvedBy, CompanyID: in.companyID, ShiftID: shiftID, MdrRate: mdrRate, MdrFee: mdrFee, ExpectedSettlementDate: settleOn,
	})
	if err != nil {
		return foliosdb.Payment{}, err
	}
	if payType == PaymentTypeRefund {
		_, err = ps.postRefundEntry(ctx, pay, reason)
	} else {
		_, err = ps.postPaymentEntry(ctx, pay)
	}
	return pay, err
}

func (in PaymentInput) parse(decimals int32) (decimal.Decimal, []apperr.FieldError) {
	var fields []apperr.FieldError
	amount, fe := parsePositive("amount", in.Amount, decimals)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if fe := validateMethod(in.PaymentMethod); fe != nil {
		fields = append(fields, *fe)
	}
	fields = append(fields, validateText("reference_number", in.ReferenceNumber, maxReferenceLen)...)
	fields = append(fields, validateText("remarks", in.Remarks, maxRemarksLen)...)
	return amount, fields
}

// PostPayment takes a payment on an open folio (payment.post). key is the request's Idempotency-Key.
func (s *Service) PostPayment(ctx context.Context, propertyID, folioID int64, key string, in PaymentInput) (PaymentResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermPaymentPost)
	if err != nil {
		return PaymentResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	amount, fields := in.parse(decimals)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return PaymentResult{}, apperr.Invalid("the payment is invalid", fields...)
	}
	return replayLoop(key,
		func() (PaymentResult, bool, error) {
			return s.paymentReplay(ctx, p.TenantID, propertyID, key, folioID, func(pay foliosdb.Payment) bool { return pay.PaymentType == PaymentTypePayment })
		},
		func() (PaymentResult, error) {
			var out PaymentResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				folio, err := s.lockFolio(ctx, p.TenantID, propertyID, folioID)
				if err != nil {
					return err
				}
				if err := requireOpen(folio); err != nil {
					return err
				}
				pay, err := s.posting(p, propertyID, day.BusinessDate, folio).createPayment(ctx, PaymentTypePayment, in.PaymentMethod, amount, in, nil, key, nil, "")
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "payment.posted", "payment", pay.ID, nil, map[string]any{
					"payment_number": pay.PaymentNumber, "folio_id": folioID, "amount": pay.Amount.String(), "method": pay.PaymentMethod,
				})); err != nil {
					return err
				}
				out, err = s.paymentResult(ctx, p.TenantID, propertyID, pay)
				return err
			})
			return out, err
		})
}

// Deposit takes a payment against a reservation that is not checked in yet (payment.post): it goes on the
// reservation's open folio that is not linked to a stay, which is created on the first deposit.
func (s *Service) Deposit(ctx context.Context, propertyID, reservationID int64, key string, in PaymentInput) (PaymentResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermPaymentPost)
	if err != nil {
		return PaymentResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	amount, fields := in.parse(decimals)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return PaymentResult{}, apperr.Invalid("the payment is invalid", fields...)
	}
	return replayLoop(key,
		func() (PaymentResult, bool, error) {
			return s.paymentReplay(ctx, p.TenantID, propertyID, key, 0, func(pay foliosdb.Payment) bool { return pay.PaymentType == PaymentTypePayment })
		},
		func() (PaymentResult, error) {
			var out PaymentResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				if _, err := q.GetReservationStatus(ctx, foliosdb.GetReservationStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: reservationID}); err != nil {
					return orNotFound(err, apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property"))
				}
				// L4: the reservation serialises deposits, so two first deposits cannot both create the folio.
				if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, []int64{reservationID}); err != nil {
					return mapNotFound(err, apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property"))
				}
				status, err := q.GetReservationStatus(ctx, foliosdb.GetReservationStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: reservationID})
				if err != nil {
					return err
				}
				if status != "DRAFT" && status != "CONFIRMED" {
					return apperr.Conflict("RESERVATION_NOT_OPEN", "a deposit needs a draft or confirmed reservation").WithContext("status", status)
				}
				folio, err := s.depositFolio(ctx, p, propertyID, reservationID)
				if err != nil {
					return err
				}
				folioID := folio.ID
				pay, err := s.posting(p, propertyID, day.BusinessDate, folio).createPayment(ctx, PaymentTypePayment, in.PaymentMethod, amount, in, nil, key, nil, "")
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "payment.deposit_posted", "payment", pay.ID, nil, map[string]any{
					"payment_number": pay.PaymentNumber, "folio_id": folioID, "reservation_id": reservationID, "amount": pay.Amount.String(), "method": pay.PaymentMethod,
				})); err != nil {
					return err
				}
				out, err = s.paymentResult(ctx, p.TenantID, propertyID, pay)
				return err
			})
			return out, err
		})
}

// depositFolio returns the reservation's open folio that has no stay, locked, or creates it. A new folio is our
// own uncommitted row, so it is not locked again: its number comes from a sequence (L5), after which no lower
// lock level may be taken.
func (s *Service) depositFolio(ctx context.Context, p auth.Principal, propertyID, reservationID int64) (foliosdb.Folio, error) {
	q := s.q(ctx)
	id, err := q.FindUnlinkedOpenFolio(ctx, foliosdb.FindUnlinkedOpenFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID})
	if err == nil {
		return s.lockFolio(ctx, p.TenantID, propertyID, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return foliosdb.Folio{}, err
	}
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFolio)
	if err != nil {
		return foliosdb.Folio{}, err
	}
	return q.InsertFolio(ctx, foliosdb.InsertFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, ReservationID: reservationID, ActorID: p.ActorID()})
}

// Void voids a payment taken on the current business date (payment.void) with an approval: the payment
// becomes VOIDED and its ledger entry is reversed. A payment of an earlier date is refunded instead.
func (s *Service) Void(ctx context.Context, propertyID, paymentID int64, in CorrectionInput) (PaymentResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermPaymentVoid)
	if err != nil {
		return PaymentResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return PaymentResult{}, apperr.Invalid("the void is invalid", fields...)
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return PaymentResult{}, err
	}
	var out PaymentResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.GetPayment(ctx, foliosdb.GetPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: paymentID})
		if err != nil {
			return orNotFound(err, errPaymentNotFound())
		}
		folio, err := s.lockFolio(ctx, p.TenantID, propertyID, pre.FolioID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Payments, db.ForUpdate, propertyID, []int64{paymentID}); err != nil {
			return mapNotFound(err, errPaymentNotFound())
		}
		pay, err := q.GetPayment(ctx, foliosdb.GetPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: paymentID})
		if err != nil {
			return err
		}
		if err := requireOpen(folio); err != nil {
			return err
		}
		if pay.CompanyID != nil && pay.Status == PaymentPosted && pay.PaymentType == PaymentTypePayment {
			// L7: the company account, after the payment. Voiding must not leave receipts above what is owed.
			if err := s.gate.LockForVoid(ctx, propertyID, *pay.CompanyID, pay.Amount); err != nil {
				return err
			}
		}
		switch {
		case pay.PaymentType != PaymentTypePayment:
			return apperr.Conflict("PAYMENT_NOT_VOIDABLE", "only a payment can be voided")
		case pay.Status != PaymentPosted:
			return apperr.Conflict("PAYMENT_ALREADY_VOIDED", "the payment is already voided")
		case !pay.BusinessDate.Equal(day.BusinessDate):
			return apperr.Conflict("CORRECTION_REQUIRES_ADJUSTMENT", "only a payment taken on the current business date can be voided: refund it instead").
				WithContext("business_date", pay.BusinessDate)
		}
		sum, err := q.SumRefundsOf(ctx, foliosdb.SumRefundsOfParams{PropertyID: propertyID, PaymentID: &paymentID})
		if err != nil {
			return err
		}
		if sum.RefundCount > 0 {
			return apperr.Conflict("PAYMENT_HAS_REFUNDS", "a payment with refunds cannot be voided")
		}
		entry, err := q.GetItemOfPayment(ctx, foliosdb.GetItemOfPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentID: &paymentID})
		if err != nil {
			return err
		}
		if _, err := s.posting(p, propertyID, day.BusinessDate, folio).reverse(ctx, entry, reason, approval); err != nil {
			return err
		}
		by := approval.UserID()
		voided, err := q.VoidPayment(ctx, foliosdb.VoidPaymentParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: paymentID, Now: s.clock.Now(), ActorID: p.ActorID(), Reason: &reason, ApprovedBy: &by,
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "payment.voided", "payment", paymentID,
			map[string]any{"status": pay.Status}, map[string]any{"status": voided.Status, "reason": reason, "actor": p.ActorID(), "approved_by": by})); err != nil {
			return err
		}
		out, err = s.voidResult(ctx, p.TenantID, propertyID, voided, entry.ID)
		return err
	})
	return out, err
}

// voidResult is the voided payment with the original entry and the folio's balance.
func (s *Service) voidResult(ctx context.Context, tenantID, propertyID int64, pay foliosdb.Payment, entryID int64) (PaymentResult, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	pv, err := s.paymentView(ctx, propertyID, pay, decimals)
	if err != nil {
		return PaymentResult{}, err
	}
	iv, err := s.itemView(ctx, tenantID, propertyID, pay.FolioID, entryID, decimals)
	if err != nil {
		return PaymentResult{}, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, pay.FolioID)
	return PaymentResult{Payment: pv, FolioItem: iv, FolioBalance: fixed(balance, decimals)}, err
}

// Refund refunds part or all of a payment (payment.refund) with an approval. The refundable amount is checked
// under the original payment's row lock, so two refunds cannot exceed it together.
func (s *Service) Refund(ctx context.Context, propertyID, paymentID int64, key string, in RefundInput) (PaymentResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermPaymentRefund)
	if err != nil {
		return PaymentResult{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	decimals := prop.CurrencyDecimals
	amount, fe := parsePositive("amount", in.Amount, decimals)
	var fields []apperr.FieldError
	if fe != nil {
		fields = append(fields, *fe)
	}
	method := strings.TrimSpace(in.PaymentMethod)
	if method != "" {
		if fe := validateMethod(method); fe != nil {
			fields = append(fields, *fe)
		} else if !slices.Contains(prop.RefundMethods, method) {
			fields = append(fields, fieldErr("payment_method", "NOT_ALLOWED", "this property refunds by "+strings.Join(prop.RefundMethods, ", ")+" only"))
		}
	}
	fields = append(fields, validateText("reference_number", in.ReferenceNumber, maxReferenceLen)...)
	reason, rf := requireReason(in.Reason)
	fields = append(fields, rf...)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return PaymentResult{}, apperr.Invalid("the refund is invalid", fields...)
	}
	return replayLoop(key,
		func() (PaymentResult, bool, error) {
			res, ok, err := s.paymentReplay(ctx, p.TenantID, propertyID, key, 0, func(pay foliosdb.Payment) bool {
				return pay.PaymentType == PaymentTypeRefund && pay.RefundOfPaymentID != nil && *pay.RefundOfPaymentID == paymentID
			})
			return res, ok, err
		},
		func() (PaymentResult, error) {
			approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
			if err != nil {
				return PaymentResult{}, err
			}
			var out PaymentResult
			err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				q := s.q(ctx)
				pre, err := q.GetPayment(ctx, foliosdb.GetPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: paymentID})
				if err != nil {
					return orNotFound(err, errPaymentNotFound())
				}
				folio, err := s.lockFolio(ctx, p.TenantID, propertyID, pre.FolioID)
				if err != nil {
					return err
				}
				if err := db.LockRows(ctx, db.Payments, db.ForUpdate, propertyID, []int64{paymentID}); err != nil {
					return mapNotFound(err, errPaymentNotFound())
				}
				orig, err := q.GetPayment(ctx, foliosdb.GetPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: paymentID})
				if err != nil {
					return err
				}
				if err := requireOpen(folio); err != nil {
					return err
				}
				if orig.PaymentType != PaymentTypePayment || orig.Status != PaymentPosted {
					return apperr.Conflict("PAYMENT_NOT_REFUNDABLE", "only a posted payment can be refunded").WithContext("status", orig.Status)
				}
				if orig.CompanyID != nil {
					return apperr.Conflict("PAYMENT_NOT_REFUNDABLE", "a city ledger transfer is not refunded: the company's receipt settles it")
				}
				sum, err := q.SumRefundsOf(ctx, foliosdb.SumRefundsOfParams{PropertyID: propertyID, PaymentID: &paymentID})
				if err != nil {
					return err
				}
				refundable := orig.Amount.Sub(sum.Refunded)
				if amount.GreaterThan(refundable) {
					return apperr.Conflict("REFUND_EXCEEDS_PAYMENT", "the refund is more than what is left of the payment").
						WithContext("refundable", fixed(refundable, decimals))
				}
				// Without a method the refund leaves by the payment's own when the property allows it, else by the first
				// method it allows (cash unless configured otherwise).
				if method == "" {
					method = prop.RefundMethods[0]
					if slices.Contains(prop.RefundMethods, orig.PaymentMethod) {
						method = orig.PaymentMethod
					}
				}
				by := approval.UserID()
				pay, err := s.posting(p, propertyID, day.BusinessDate, folio).createPayment(ctx, PaymentTypeRefund, method, amount,
					PaymentInput{ReferenceNumber: in.ReferenceNumber}, &paymentID, key, &by, reason)
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "payment.refunded", "payment", pay.ID, nil, map[string]any{
					"payment_number": pay.PaymentNumber, "refund_of_payment_id": paymentID, "amount": pay.Amount.String(), "reason": reason,
					"actor": p.ActorID(), "approved_by": by,
				})); err != nil {
					return err
				}
				out, err = s.paymentResult(ctx, p.TenantID, propertyID, pay)
				return err
			})
			return out, err
		})
}

// ListPayments is the cashier list, newest first, with the net total per method for the business date
// (folio.read). before is the id to continue after (0 for the first page).
func (s *Service) ListPayments(ctx context.Context, propertyID int64, f PaymentFilter, before int64, limit int) (PaymentList, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return PaymentList{}, err
	}
	if f.Method != "" {
		if fe := validateMethod(f.Method); fe != nil {
			return PaymentList{}, apperr.Invalid("the filter is invalid", *fe)
		}
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentList{}, err
	}
	if before == 0 {
		before = math.MaxInt64
	}
	q := s.q(ctx)
	rows, err := q.ListPayments(ctx, foliosdb.ListPaymentsParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeIDOrMax: before, BusinessDate: f.BusinessDate, PaymentMethod: nullable(f.Method), RowLimit: rowLimit(limit),
	})
	if err != nil {
		return PaymentList{}, err
	}
	out := PaymentList{Data: make([]Payment, len(rows))}
	for i, r := range rows {
		if out.Data[i], err = s.paymentView(ctx, propertyID, r, decimals); err != nil {
			return PaymentList{}, err
		}
	}
	if f.BusinessDate != nil {
		totals, err := q.PaymentTotals(ctx, foliosdb.PaymentTotalsParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: *f.BusinessDate})
		if err != nil {
			return PaymentList{}, err
		}
		for _, t := range totals {
			out.Totals = append(out.Totals, MethodTotal{PaymentMethod: t.PaymentMethod, Paid: fixed(t.Paid, decimals), Refunded: fixed(t.Refunded, decimals), Net: fixed(t.Paid.Sub(t.Refunded), decimals)})
		}
	}
	return out, nil
}

// GetPayment returns one payment or refund (folio.read).
func (s *Service) GetPayment(ctx context.Context, propertyID, id int64) (Payment, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return Payment{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Payment{}, err
	}
	pay, err := s.q(ctx).GetPayment(ctx, foliosdb.GetPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Payment{}, orNotFound(err, errPaymentNotFound())
	}
	return s.paymentView(ctx, propertyID, pay, decimals)
}

// Transfer moves part of an open folio's balance to a company's city ledger account (cityledger.transfer): a
// CITY_LEDGER payment that credits the folio and adds to what the company owes. The company row is locked
// (L6, after the folio) so the credit limit holds when two folios transfer at once.
func (s *Service) Transfer(ctx context.Context, propertyID, folioID int64, key string, in TransferInput) (PaymentResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermCityLedgerTransfer)
	if err != nil {
		return PaymentResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PaymentResult{}, err
	}
	amount, fe := parsePositive("amount", in.Amount, decimals)
	var fields []apperr.FieldError
	if fe != nil {
		fields = append(fields, *fe)
	}
	if in.CompanyID < 1 {
		fields = append(fields, fieldErr("company_id", "REQUIRED", "a company is required"))
	}
	fields = append(fields, validateText("reference_number", in.ReferenceNumber, maxReferenceLen)...)
	fields = append(fields, validateText("remarks", in.Remarks, maxRemarksLen)...)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return PaymentResult{}, apperr.Invalid("the transfer is invalid", fields...)
	}
	return replayLoop(key,
		func() (PaymentResult, bool, error) {
			return s.paymentReplay(ctx, p.TenantID, propertyID, key, folioID, func(pay foliosdb.Payment) bool {
				return pay.PaymentType == PaymentTypePayment && pay.CompanyID != nil && *pay.CompanyID == in.CompanyID
			})
		},
		func() (PaymentResult, error) {
			var out PaymentResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
				if err != nil {
					return err
				}
				folio, err := s.lockFolio(ctx, p.TenantID, propertyID, folioID)
				if err != nil {
					return err
				}
				if err := requireOpen(folio); err != nil {
					return err
				}
				balance, _, err := s.balanceOf(ctx, propertyID, folioID)
				if err != nil {
					return err
				}
				if amount.GreaterThan(balance) {
					return apperr.Conflict("TRANSFER_EXCEEDS_BALANCE", "the transfer is more than the folio's balance").WithContext("balance", fixed(balance, decimals))
				}
				if err := s.gate.LockForTransfer(ctx, propertyID, in.CompanyID, amount); err != nil {
					return err
				}
				cid := in.CompanyID
				pay, err := s.posting(p, propertyID, day.BusinessDate, folio).createPayment(ctx, PaymentTypePayment, MethodCityLedger, amount,
					PaymentInput{ReferenceNumber: in.ReferenceNumber, Remarks: in.Remarks, companyID: &cid}, nil, key, nil, "")
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "cityledger.transferred", "payment", pay.ID, nil, map[string]any{
					"payment_number": pay.PaymentNumber, "folio_id": folioID, "company_id": cid, "amount": pay.Amount.String(),
				})); err != nil {
					return err
				}
				out, err = s.paymentResult(ctx, p.TenantID, propertyID, pay)
				return err
			})
			return out, err
		})
}
