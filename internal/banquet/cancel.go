package banquet

import (
	"context"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Cancelling or losing an event with its money (36 4.7): the fee of the tier in force, or the forfeit of a deposit, posted once on the EVENT folio; the rest of the deposits is refunded by the
// refund that exists (an approval of the kind REFUND), a separate step of the folio.

// quoteOf is what cancelling an event would cost on the business date bd. It reads, it does not lock; closeEvent calls it with the folio and the event locked.
func (s *Service) quoteOf(ctx context.Context, tenantID, propertyID, eventID int64, hdr banquetdb.EventMoneyRow, bd civil.Date) (CancelQuote, error) {
	_, est, err := s.eventEstimate(ctx, tenantID, propertyID, eventID)
	if err != nil {
		return CancelQuote{}, err
	}
	decimals, err := s.decimals(ctx, tenantID, propertyID)
	if err != nil {
		return CancelQuote{}, err
	}
	tiers, err := s.tiers(ctx, tenantID, propertyID, nil, &eventID)
	if err != nil {
		return CancelQuote{}, err
	}
	q := CancelQuote{Status: hdr.Status, Base: BaseNet, BaseAmount: est.Net, Action: ActionFee, PolicyDefined: len(tiers) > 0, FeeItemID: hdr.CancelledFeeItemID}
	if hdr.PolicyBase != nil && *hdr.PolicyBase == BaseGross {
		q.Base, q.BaseAmount = BaseGross, est.Total
	}
	first, err := s.q(ctx).FirstSessionDate(ctx, banquetdb.FirstSessionDateParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return CancelQuote{}, err
	}
	if !first.Equal(civilEpoch) {
		q.DaysBefore = DaysBefore(bd, first)
	}
	ref, err := s.folioRef(ctx, tenantID, propertyID, eventID)
	if err != nil {
		return CancelQuote{}, err
	}
	if ref != nil {
		q.FolioID = &ref.ID
		if q.DepositsHeld, err = s.folios.EventDeposits(ctx, propertyID, ref.ID); err != nil {
			return CancelQuote{}, err
		}
	}
	if hdr.Status == StatusDefinite {
		if t := TierFor(tiers, q.DaysBefore); t != nil {
			q.Tier, q.Action = t, t.Action
			q.Fee = FeeOf(q.BaseAmount, t.Percent, decimals)
			if t.Action == ActionForfeit && q.Fee.GreaterThan(q.DepositsHeld) {
				q.Fee = q.DepositsHeld
			}
		}
	}
	balance := decimal.Zero
	if ref != nil {
		balance = ref.Balance
	}
	after := balance.Add(q.Fee)
	q.Applied = decimal.Min(q.DepositsHeld, q.Fee)
	q.Refundable = decimal.Max(decimal.Zero, decimal.Min(after.Neg(), q.DepositsHeld))
	q.StillOwed = decimal.Max(decimal.Zero, after)
	return q, nil
}

// CancelQuote answers what cancelling the event would cost today (banquet.manage or confirm).
func (s *Service) CancelQuote(ctx context.Context, propertyID, eventID int64) (CancelQuote, error) {
	p, err := s.canRead(ctx, propertyID, auth.PermBanquetManage, auth.PermBanquetConfirm)
	if err != nil {
		return CancelQuote{}, err
	}
	hdr, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return CancelQuote{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	bd := s.today(ctx, propertyID)
	return s.quoteOf(ctx, p.TenantID, propertyID, eventID, hdr, bd)
}

// closeMoney decides the amount and the action of a close from the quote and the request. A definite event pays the fee of its tier unless the person changes it (a lower amount needs its reason); a
// forfeit never exceeds the deposits received; an event that is not definite has no tier, so only a forfeit can be asked of it.
func closeMoney(status string, q CancelQuote, in CloseInput) (decimal.Decimal, string, *apperr.Error) {
	amount, action := decimal.Zero, q.Action
	if status == StatusDefinite {
		amount = q.Fee
	} else {
		action = ActionForfeit
	}
	if in.Amount != nil {
		amount = *in.Amount
	}
	if in.Action != "" {
		action = in.Action
	}
	if !amount.IsPositive() {
		return decimal.Zero, action, nil
	}
	if status != StatusDefinite && action != ActionForfeit {
		return amount, action, invalidCode("ACTION_NOT_ALLOWED", "action", "an event that is not definite has no policy: only a forfeit of the deposit can be asked")
	}
	if in.Amount != nil && status == StatusDefinite && amount.LessThan(q.Fee) && (in.FeeReason == nil || strings.TrimSpace(*in.FeeReason) == "") {
		return amount, action, invalidCode("FEE_REASON_REQUIRED", "fee_reason", "an amount below the policy needs the reason")
	}
	if action == ActionForfeit && amount.GreaterThan(q.DepositsHeld) {
		return amount, action, apperr.Conflict("FORFEIT_EXCEEDS_DEPOSITS", "the forfeit is more than the deposits received").WithContext("deposits_held", q.DepositsHeld.String())
	}
	return amount, action, nil
}

// MarkLost closes an inquiry or a tentative event that the customer did not take (banquet.manage); the reason is required and the holds are released. A deposit on the folio can be forfeited.
func (s *Service) MarkLost(ctx context.Context, propertyID, eventID int64, in CloseInput) (CancelResult, error) {
	return s.closeEvent(ctx, propertyID, eventID, in, StatusLost)
}

// CancelEvent cancels an event (banquet.manage for an inquiry or a tentative one, banquet.confirm for a definite one); the reason is required and the holds are released. A definite event pays the fee
// of its policy, a tentative one may forfeit a deposit.
func (s *Service) CancelEvent(ctx context.Context, propertyID, eventID int64, in CloseInput) (CancelResult, error) {
	return s.closeEvent(ctx, propertyID, eventID, in, StatusCancelled)
}

func (s *Service) closeEvent(ctx context.Context, propertyID, eventID int64, in CloseInput, target string) (CancelResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return CancelResult{}, err
	}
	if in.ReasonID < 1 {
		return CancelResult{}, invalidCode("REASON_REQUIRED", "reason_id", "a lost or cancelled event has a reason")
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return CancelResult{}, err
	}
	if a := in.Amount; a != nil && (a.IsNegative() || a.Round(decimals).Cmp(*a) != 0) {
		return CancelResult{}, invalid("close", fieldErr("amount", "INVALID_VALUE", "zero or more, with the decimals of the currency"))
	}
	if in.Action != "" && in.Action != ActionFee && in.Action != ActionForfeit {
		return CancelResult{}, invalid("close", fieldErr("action", "INVALID_VALUE", "FEE or FORFEIT"))
	}
	note := blankToNil(in.ReasonNote)
	var out CancelResult
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		// the folio of the event first when it exists (level 42), then the rooms and the event (45)
		pre, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if pre != nil {
			if err := s.folios.LockFolios(ctx, propertyID, db.ForUpdate, pre.ID); err != nil {
				return err
			}
		}
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, nil)
		if err != nil {
			return err
		}
		now, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if (pre == nil) != (now == nil) || (pre != nil && pre.ID != now.ID) {
			return apperr.Conflict("VERSION_CONFLICT", "the folio of the event changed: ask again")
		}
		allowed := hdr.Status == StatusInquiry || hdr.Status == StatusTentative || (target == StatusCancelled && hdr.Status == StatusDefinite)
		if !allowed {
			return apperr.Conflict("EVENT_STATUS_INVALID", "the event cannot be "+strings.ToLower(target)+" from its status").WithContext("status", hdr.Status)
		}
		if hdr.Status == StatusDefinite {
			if err := s.authz.Require(ctx, propertyID, auth.PermBanquetConfirm); err != nil {
				return err
			}
		}
		q := s.q(ctx)
		reason, err := q.GetReason(ctx, banquetdb.GetReasonParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.ReasonID})
		if err != nil {
			return orNotFound(err, invalidCode("REASON_NOT_FOUND", "reason_id", "no such reason in this property"))
		}
		want := ReasonLost
		if target == StatusCancelled {
			want = ReasonCancel
		}
		if reason.Kind != want || !reason.IsActive {
			return invalidCode("REASON_NOT_FOUND", "reason_id", "an active reason of the kind "+want+" is needed")
		}
		old, err := s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		mh, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
		if err != nil {
			return err
		}
		quote, err := s.quoteOf(ctx, p.TenantID, propertyID, eventID, mh, bd)
		if err != nil {
			return err
		}
		amount, action, ce := closeMoney(hdr.Status, quote, in)
		if ce != nil {
			return ce
		}
		var feeItem *int64
		if amount.IsPositive() {
			if mh.CancelledFeeItemID != nil {
				return apperr.Conflict("FEE_ALREADY_POSTED", "the fee of this cancellation is posted already")
			}
			item, err := s.postClose(ctx, p, propertyID, bd, eventID, now, mh, amount, action, in.FeeReason)
			if err != nil {
				return err
			}
			feeItem = &item
			if err := q.SetCancelledFee(ctx, banquetdb.SetCancelledFeeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, FolioItemID: feeItem}); err != nil {
				return err
			}
		}
		if err := s.releaseHolds(ctx, p.TenantID, propertyID, eventID, nil, bd); err != nil {
			return err
		}
		closed := s.clock.Now()
		if err := q.SetEventStatus(ctx, banquetdb.SetEventStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, Status: target, ReasonID: &in.ReasonID, ReasonNote: note,
			ClosedAt: &closed, ClosedOn: &bd, ActorID: p.ActorID()}); err != nil {
			return err
		}
		ev, err := s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		out = CancelResult{Event: ev, Quote: quote, Posted: amount, FolioItem: feeItem}
		if ref, err := s.folioRef(ctx, p.TenantID, propertyID, eventID); err != nil {
			return err
		} else if ref != nil {
			out.FolioID = &ref.ID
			held, err := s.folios.EventDeposits(ctx, propertyID, ref.ID)
			if err != nil {
				return err
			}
			out.Refundable = decimal.Max(decimal.Zero, decimal.Min(ref.Balance.Neg(), held))
		}
		action2 := "banquet.event_lost"
		if target == StatusCancelled {
			action2 = "banquet.event_cancelled"
		}
		data := map[string]any{"status": ev.Status, "reason_id": in.ReasonID, "reason_note": note, "fee": amount.String(), "fee_action": action, "tier": quote.Tier, "base": quote.Base,
			"base_amount": quote.BaseAmount.String(), "fee_reason": in.FeeReason}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, action2, "banquet_event", eventID, ev.Number, map[string]any{"status": old.Status}, data))
	})
	return out, err
}

// postClose posts the fee or the forfeit of a close on the EVENT folio, once: the key is the event. With the base GROSS the fee is posted INCLUSIVE so the customer pays exactly the amount of the tier; a
// forfeit is the amount of a deposit, so it is inclusive too; with the base NET the fee is read in the mode of its charge code.
func (s *Service) postClose(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, eventID int64, f *folios.EventFolio, mh banquetdb.EventMoneyRow, amount decimal.Decimal, action string, reason *string) (int64, error) {
	code, key, desc := CodeCancelFee, "banquet:cancel:"+itoa(eventID), "Cancellation fee"
	if action == ActionForfeit {
		code, key, desc = CodeForfeit, "banquet:cancel:"+itoa(eventID)+":forfeit", "Deposit forfeited"
	}
	cc, err := s.q(ctx).ChargeCodeByCode(ctx, banquetdb.ChargeCodeByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: code})
	if err != nil {
		return 0, orNotFound(err, apperr.Conflict("CHARGE_CODE_NOT_FOUND", "the charge code "+code+" does not exist in this property").WithContext("charge_code", code))
	}
	if f == nil {
		made, err := s.folios.OpenEventFolio(ctx, p, propertyID, eventID, mh.BookerCompanyID)
		if err != nil {
			return 0, err
		}
		f = &made
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.folio_opened", "banquet_event", eventID, mh.Number, nil, map[string]any{"folio_id": made.ID, "folio_number": made.Number})); err != nil {
			return 0, err
		}
	}
	mode := chargecalc.PriceMode(cc.PriceMode)
	if action == ActionForfeit || (mh.PolicyBase != nil && *mh.PolicyBase == BaseGross) {
		mode = chargecalc.Inclusive
	}
	line, err := s.folios.PostEventCharge(ctx, p, propertyID, bd, f.ID, folios.EventCharge{ChargeCodeID: cc.ID, Quantity: decimal.NewFromInt(1), UnitPrice: amount, PriceMode: mode,
		Description: truncate(mh.Name+" · "+desc, 200), ServiceDate: bd, Key: key, Reason: blankToNil(reason), ReferenceType: folios.ReferenceBanquetCancel, ReferenceID: itoa(eventID)})
	if err != nil {
		return 0, err
	}
	return line.ItemID, nil
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
