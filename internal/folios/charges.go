package folios

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
)

// replayLoop runs an idempotent request: a stored result for the key is returned as is, and a request that
// loses the race to a concurrent one with the same key (DUPLICATE_REQUEST) replays the winner.
func replayLoop[T any](key string, lookup func() (T, bool, error), run func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < 2; attempt++ {
		if key != "" {
			if v, ok, err := lookup(); err != nil || ok {
				return v, err
			}
		}
		v, err := run()
		if key != "" && apperr.IsCode(err, "DUPLICATE_REQUEST") {
			continue
		}
		return v, err
	}
	return zero, apperr.Busy("REQUEST_IN_PROGRESS", "the same request is still being processed")
}

func errKeyReused() *apperr.Error {
	return apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another request")
}

func checkKey(key string) []apperr.FieldError {
	if len(key) > 100 {
		return []apperr.FieldError{fieldErr("Idempotency-Key", "TOO_LONG", "at most 100 characters")}
	}
	return nil
}

// itemReplay looks up the stored result of an idempotency key: the item and the folio's balance now.
func (s *Service) itemReplay(ctx context.Context, tenantID, propertyID, folioID int64, key string) (ItemResult, bool, error) {
	it, err := s.q(ctx).GetFolioItemByKey(ctx, foliosdb.GetFolioItemByKeyParams{TenantID: tenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ItemResult{}, false, nil
		}
		return ItemResult{}, false, err
	}
	if it.FolioID != folioID {
		return ItemResult{}, false, errKeyReused()
	}
	res, err := s.itemResult(ctx, tenantID, propertyID, it.FolioID, it.ID)
	return res, true, err
}

func (s *Service) itemResult(ctx context.Context, tenantID, propertyID, folioID, itemID int64) (ItemResult, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ItemResult{}, err
	}
	view, err := s.itemView(ctx, tenantID, propertyID, folioID, itemID, decimals)
	if err != nil {
		return ItemResult{}, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, folioID)
	return ItemResult{Item: view, FolioBalance: fixed(balance, decimals)}, err
}

// PostCharge posts a manual charge to an open folio (folio.post_charge). key is the request's
// Idempotency-Key. A ROOM charge code is refused (409 ROOM_CHARGE_REQUIRES_ROOM_POSTING).
func (s *Service) PostCharge(ctx context.Context, propertyID, folioID int64, key string, in ChargeInput) (ItemResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioPostCharge)
	if err != nil {
		return ItemResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ItemResult{}, err
	}
	cmd, fields := in.parse(decimals)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return ItemResult{}, apperr.Invalid("the charge is invalid", fields...)
	}
	cmd.key = key
	return replayLoop(key,
		func() (ItemResult, bool, error) { return s.itemReplay(ctx, p.TenantID, propertyID, folioID, key) },
		func() (ItemResult, error) {
			var out ItemResult
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
				if err := refuseOutletFolios(folio); err != nil {
					return err
				}
				if in.StayID != nil {
					if folio.FolioType != FolioTypeMaster || folio.BookingGroupID == nil {
						return apperr.Invalid("the charge is invalid", fieldErr("stay_id", "ONLY_ON_MASTER", "only on the master folio of a group"))
					}
					if _, err := s.q(ctx).StayOfGroup(ctx, foliosdb.StayOfGroupParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: *in.StayID, GroupID: folio.BookingGroupID}); err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return apperr.Invalid("the charge is invalid", fieldErr("stay_id", "NOT_IN_GROUP", "a stay of a member of the group"))
						}
						return err
					}
					cmd.stayID = in.StayID
				}
				if cmd.serviceDate.IsZero() {
					cmd.serviceDate = day.BusinessDate
				} else if cmd.serviceDate.After(day.BusinessDate) {
					return apperr.Invalid("the charge is invalid", fieldErr("service_date", "IN_THE_FUTURE", "not after the business date"))
				}
				item, err := s.posting(p, propertyID, day.BusinessDate, folio).postCharge(ctx, cmd)
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.charge_posted", "folio", folioID, auditlabel.Folio(ctx, propertyID, folioID), nil, map[string]any{
					"folio_item_id": item.ID, "charge_code_id": cmd.chargeCodeID, "debit": item.Debit.String(), "description": item.Description,
				})); err != nil {
					return err
				}
				out, err = s.itemResult(ctx, p.TenantID, propertyID, folioID, item.ID)
				return err
			})
			return out, err
		})
}

// parse validates a manual charge; decimals is the property's currency precision.
func (in ChargeInput) parse(decimals int32) (chargeCmd, []apperr.FieldError) {
	var fields []apperr.FieldError
	cmd := chargeCmd{chargeCodeID: in.ChargeCodeID, description: strings.TrimSpace(in.Description)}
	if in.ChargeCodeID < 1 {
		fields = append(fields, fieldErr("charge_code_id", "REQUIRED", "a charge code"))
	}
	q, err := money.Parse(strings.TrimSpace(in.Quantity))
	if err != nil || !q.IsPositive() || !q.Equal(q.Round(maxQuantityScale)) {
		fields = append(fields, fieldErr("quantity", "INVALID_VALUE", "a positive number with at most 3 decimals"))
	}
	cmd.quantity = q
	if in.UnitPrice != nil {
		price, err := billingconfig.ParseUnitPrice(strings.TrimSpace(*in.UnitPrice), decimals)
		if err != nil {
			fields = append(fields, fieldErr("unit_price", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
		}
		cmd.unitPrice = &price
	}
	if d := strings.TrimSpace(in.Discount); d != "" {
		disc, err := billingconfig.ParseUnitPrice(d, decimals)
		if err != nil {
			fields = append(fields, fieldErr("discount_amount", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
		}
		cmd.discount = disc
	}
	mode, fe := priceModeOf(in.PriceMode)
	if fe != nil {
		fields = append(fields, *fe)
	}
	cmd.priceMode = mode
	if in.ServiceDate != nil {
		cmd.serviceDate = *in.ServiceDate
	}
	fields = append(fields, validateText("description", cmd.description, maxDescriptionLen)...)
	return cmd, fields
}

// PostAdjustment posts a signed correction on the current business date (folio.adjust) with an approval
// (correction.approve, entered as the `approval` block).
func (s *Service) PostAdjustment(ctx context.Context, propertyID, folioID int64, key string, in AdjustmentInput) (ItemResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioAdjust)
	if err != nil {
		return ItemResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ItemResult{}, err
	}
	var fields []apperr.FieldError
	if in.ChargeCodeID < 1 {
		fields = append(fields, fieldErr("charge_code_id", "REQUIRED", "a charge code"))
	}
	amount, perr := money.Parse(strings.TrimSpace(in.Amount))
	if perr != nil || amount.IsZero() || !amount.Equal(amount.Round(decimals)) {
		fields = append(fields, fieldErr("amount", "INVALID_AMOUNT", "a signed, non-zero amount with at most the currency's decimals"))
	}
	mode, fe := priceModeOf(in.PriceMode)
	if fe != nil {
		fields = append(fields, *fe)
	}
	reason, rf := requireReason(in.Reason)
	fields = append(fields, rf...)
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return ItemResult{}, apperr.Invalid("the adjustment is invalid", fields...)
	}
	return replayLoop(key,
		func() (ItemResult, bool, error) { return s.itemReplay(ctx, p.TenantID, propertyID, folioID, key) },
		func() (ItemResult, error) {
			approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindFolioAdjustment, Amount: amount}) // before the transaction: no lock is held while the password is hashed
			if err != nil {
				return ItemResult{}, err
			}
			var out ItemResult
			err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
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
				if err := refuseOutletFolios(folio); err != nil {
					return err
				}
				if in.RelatedItemID != nil {
					rel, err := s.q(ctx).GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.RelatedItemID})
					if err != nil || rel.FolioID != folioID {
						return apperr.Invalid("the adjustment is invalid", fieldErr("related_item_id", "NOT_FOUND", "an item of this folio"))
					}
				}
				item, err := s.posting(p, propertyID, day.BusinessDate, folio).postAdjustment(ctx, adjustmentCmd{
					chargeCodeID: in.ChargeCodeID, amount: amount, priceMode: mode, reason: reason, relatedItemID: in.RelatedItemID, key: key, approval: approval,
				})
				if err != nil {
					return err
				}
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.adjustment_posted", "folio", folioID, auditlabel.Folio(ctx, propertyID, folioID), nil, approval.AuditFields(map[string]any{
					"folio_item_id": item.ID, "charge_code_id": in.ChargeCodeID, "debit": item.Debit.String(), "credit": item.Credit.String(),
					"reason": reason, "actor": p.ActorID(),
				}))); err != nil {
					return err
				}
				out, err = s.itemResult(ctx, p.TenantID, propertyID, folioID, item.ID)
				return err
			})
			return out, err
		})
}

// Reverse reverses an item posted on the current business date (folio.reverse) with an approval. A payment
// is voided (payment.void), not reversed here; an earlier day is corrected with an adjustment.
func (s *Service) Reverse(ctx context.Context, propertyID, itemID int64, in CorrectionInput) (ItemResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return ItemResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return ItemResult{}, apperr.Invalid("the reversal is invalid", fields...)
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindFolioReversal, Amount: s.itemAmountOf(ctx, p.TenantID, propertyID, itemID)})
	if err != nil {
		return ItemResult{}, err
	}
	var out ItemResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
		if err != nil {
			return orNotFound(err, errItemNotFound())
		}
		folio, err := s.lockFolio(ctx, p.TenantID, propertyID, pre.FolioID)
		if err != nil {
			return err
		}
		if err := requireOpen(folio); err != nil {
			return err
		}
		if err := refuseOutletFolios(folio); err != nil {
			return err
		}
		item, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
		if err != nil {
			return orNotFound(err, errItemNotFound())
		}
		switch item.TransactionType {
		case TypePayment, TypeRefund:
			return apperr.Conflict("USE_PAYMENT_CORRECTION", "a payment is voided or refunded, not reversed").WithContext("transaction_type", item.TransactionType)
		case TypeReversal:
			return apperr.Conflict("ITEM_NOT_REVERSIBLE", "a reversal cannot be reversed")
		}
		if err := s.refuseTransferLine(ctx, propertyID, item.ChargeCodeID); err != nil {
			return err
		}
		// the copy a transfer made is not reversed: it goes back with a return, which keeps the log, the first origin and the money in step
		if _, err := q.GetTransferByInItem(ctx, foliosdb.GetTransferByInItemParams{PropertyID: propertyID, ItemID: itemID}); err == nil {
			if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &itemID}); err == nil {
				return apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed")
			}
			return apperr.Conflict("TRANSFER_COPY_USE_RETURN", "a charge that came from another folio is sent back with a return, not reversed").WithContext("item_id", itemID)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !item.BusinessDate.Equal(day.BusinessDate) {
			return apperr.Conflict("CORRECTION_REQUIRES_ADJUSTMENT", "only an item posted on the current business date can be reversed: post an adjustment instead").
				WithContext("business_date", item.BusinessDate)
		}
		if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &itemID}); err == nil {
			return apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed")
		}
		rev, err := s.posting(p, propertyID, day.BusinessDate, folio).reverse(ctx, item, reason, approval)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.item_reversed", "folio_item", itemID, auditlabel.FolioOfItem(ctx, propertyID, itemID), nil, approval.AuditFields(map[string]any{
			"reversal_item_id": rev.ID, "folio_id": folio.ID, "reason": reason, "actor": p.ActorID(),
		}))); err != nil {
			return err
		}
		out, err = s.itemResult(ctx, p.TenantID, propertyID, folio.ID, rev.ID)
		return err
	})
	return out, err
}

// itemAmountOf is the amount of a folio item as the approval rule sees it (the amount of the charge reversed): read before the
// transaction, without a lock, only to know whether the person who acts may approve their own correction. An item that is not
// found is zero here; the transaction answers 404.
func (s *Service) itemAmountOf(ctx context.Context, tenantID, propertyID, itemID int64) decimal.Decimal {
	it, err := s.q(ctx).GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: tenantID, PropertyID: propertyID, ID: itemID})
	if err != nil {
		return decimal.Zero
	}
	return it.Debit.Add(it.Credit)
}
