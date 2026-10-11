package frontdesk

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk/frontdeskdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations"
	"kamarapms/internal/reservations/reservationsdb"
)

// Apply-to values of ChangeRates.
const (
	ApplyNight     = "NIGHT"
	ApplyRemaining = "REMAINING"
)

// ChangeRates changes the rate of nights of an in-house stay (frontdesk.rate_change), with a reason that is required and audited. The rate plan is not touched: only the snapshot of the booking
// (reservation_room_rates, the same rows a room move or an extension override) changes.
//
//   - A night that is not charged is changed in the snapshot; the room charge posting then charges the new rate (tax and service by the engine, as always).
//   - A night that is charged keeps its charge: the snapshot changes and an ADJUSTMENT for the difference is posted on the folio the night was charged to (the correction workflow of the
//     folio: folio.adjust and the approval of a correction), dated the open business day. This is also how a night charged on a day that has closed is corrected, since a closed day is never
//     written to. A charged night is only changed when it is asked for by its date (NIGHT); REMAINING never touches one.
//   - A stay that is not in house (checked out, cancelled) is refused: STAY_NOT_OPEN.
//
// Locks, in the global order: the open business day (shared), the stay (the posting run, the check-out and another rate change take it too, so they take turns; the charged nights are read
// only after it is held), then the folios of the charged nights in ascending id.
func (s *Service) ChangeRates(ctx context.Context, propertyID, stayID int64, in ChangeRatesInput) (ChangeRatesResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskRateChange)
	if err != nil {
		return ChangeRatesResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	fields := requireVersion(in.Version)
	if reason == "" {
		fields = append(fields, fieldErr("reason", "REQUIRED", "a reason is required"))
	} else if len([]rune(reason)) > maxReasonLen {
		fields = append(fields, fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	if in.ApplyTo != ApplyNight && in.ApplyTo != ApplyRemaining {
		fields = append(fields, fieldErr("apply_to", "INVALID_VALUE", "NIGHT or REMAINING"))
	}
	if in.Date.IsZero() {
		fields = append(fields, fieldErr("date", "REQUIRED", "the night to change, or the first of the remaining nights"))
	}
	amount, perr := decimal.NewFromString(strings.TrimSpace(in.Amount))
	if perr != nil || amount.IsNegative() {
		fields = append(fields, fieldErr("amount", "INVALID_AMOUNT", "a non-negative amount with at most the currency decimals"))
	}
	if len(fields) > 0 {
		return ChangeRatesResult{}, apperr.Invalid("the rate change is invalid", fields...)
	}
	// The approvals are checked before the transaction (no lock is held while a password is hashed) from a read of the nights that takes no lock; the transaction asks again with the locks held
	// and refuses (APPROVAL_REQUIRED) if a charge or a rate moved in between so that more is needed than was approved.
	var approval, rateApproval iam.Approval
	callerApproves := s.authz.Require(ctx, propertyID, auth.PermReservationOverrideApprove) == nil
	if in.Approval != nil {
		peek, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return ChangeRatesResult{}, err
		}
		nights, err := s.res.NightRates(ctx, p.TenantID, propertyID, peek.line.ID)
		if err != nil {
			return ChangeRatesResult{}, err
		}
		postings, err := s.q(ctx).ListStayNightPostings(ctx, frontdeskdb.ListStayNightPostingsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
		if err != nil {
			return ChangeRatesResult{}, err
		}
		corrected, lowered := approvalNeeds(rateTargetsOrNil(in, amount, nights, postings), amount)
		if corrected {
			if approval, err = s.folios.VerifyApproval(ctx, propertyID, in.Approval); err != nil {
				return ChangeRatesResult{}, err
			}
		}
		if lowered && !callerApproves {
			if rateApproval, err = s.folios.VerifyApprovalFor(ctx, propertyID, in.Approval, auth.PermReservationOverrideApprove); err != nil {
				return ChangeRatesResult{}, err
			}
		}
	}
	var out ChangeRatesResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		q := s.q(ctx)
		pre, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return err
		}
		st, err := s.lockStay(ctx, p, propertyID, stayID, in.Version) // L4
		if err != nil {
			return err
		}
		prop, err := s.days.GetProperty(ctx, propertyID)
		if err != nil {
			return err
		}
		places := prop.CurrencyDecimals
		amount := amount.Round(places)
		nights, err := s.res.NightRates(ctx, p.TenantID, propertyID, pre.line.ID)
		if err != nil {
			return err
		}
		postings, err := q.ListStayNightPostings(ctx, frontdeskdb.ListStayNightPostingsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
		if err != nil {
			return err
		}
		targets, found := rateTargets(in, amount, nights, postings)
		if in.ApplyTo == ApplyNight && !found {
			return apperr.Invalid("the rate change is invalid", fieldErr("date", "OUT_OF_RANGE", "a night of the stay"))
		}
		if len(targets) == 0 {
			return apperr.Invalid("the rate change is invalid", fieldErr("amount", "UNCHANGED", "no night would change"))
		}
		needsCorrection, lowered := approvalNeeds(targets, amount)
		var rateApprover *int64 // who approved a lower rate: the caller when they are a rate approver, else the person whose credentials came with the request
		if needsCorrection {
			if err := s.authz.Require(ctx, propertyID, auth.PermFolioAdjust); err != nil {
				return err
			}
			if approval.IsZero() {
				if _, err := s.folios.VerifyApproval(ctx, propertyID, nil); err != nil { // APPROVAL_REQUIRED
					return err
				}
			}
		}
		if lowered {
			switch {
			case callerApproves:
				rateApprover = p.ActorID()
			case !rateApproval.IsZero():
				id := rateApproval.UserID()
				rateApprover = &id
			default:
				_, err := s.folios.VerifyApprovalFor(ctx, propertyID, nil, auth.PermReservationOverrideApprove) // APPROVAL_REQUIRED
				return err
			}
		}
		overrides := make([]reservations.NightOverride, len(targets))
		for i, t := range targets {
			overrides[i] = reservations.NightOverride{Date: t.night.StayDate, Amount: amount.StringFixed(places)}
		}
		if err := s.res.OverrideNights(ctx, p, auth.PermFrontdeskRateChange, propertyID, pre.line.ID, overrides); err != nil {
			return err
		}
		after, err := s.res.NightRates(ctx, p.TenantID, propertyID, pre.line.ID)
		if err != nil {
			return err
		}
		newRow := map[civil.Date]reservationsdb.ReservationRoomRate{}
		for _, n := range after {
			newRow[n.StayDate] = n
		}
		adjusted := map[int64]folios.NightAdjusted{}
		if needsCorrection {
			var corrections []folios.NightAdjustment
			for _, t := range targets {
				if t.po != nil {
					nr := newRow[t.night.StayDate]
					corrections = append(corrections, folios.NightAdjustment{ItemID: t.po.FolioItemID, Delta: nr.Amount.Sub(t.night.Amount), PriceMode: nr.PriceMode})
				}
			}
			done, err := s.folios.AdjustChargedNights(ctx, p, propertyID, bd, reason, approval, corrections)
			if err != nil {
				return err
			}
			for _, d := range done {
				adjusted[d.ItemID] = d
			}
		}
		plan, err := q.GetRatePlanCode(ctx, frontdeskdb.GetRatePlanCodeParams{PropertyID: propertyID, ID: pre.line.RatePlanID})
		if err != nil {
			return err
		}
		bumped, err := q.BumpStay(ctx, frontdeskdb.BumpStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		changes := make([]RateChange, len(targets))
		for i, t := range targets {
			nr := newRow[t.night.StayDate]
			c := RateChange{Date: t.night.StayDate, OldAmount: t.night.Amount.StringFixed(places), NewAmount: nr.Amount.StringFixed(places), PriceMode: nr.PriceMode, Charged: t.po != nil}
			meta := map[string]any{
				"date": c.Date, "amount": c.NewAmount, "reason": reason, "rate_plan": plan.Code, "room_number": pre.seg.RoomNumber, "reservation_id": pre.line.ReservationID,
				"price_mode": nr.PriceMode, "financial_adjustment": c.Charged, "apply_to": in.ApplyTo, "lowered": nr.Amount.LessThan(t.night.Amount),
			}
			if rateApprover != nil && nr.Amount.LessThan(t.night.Amount) {
				meta["rate_approved_by"] = *rateApprover
			}
			if t.po != nil {
				a := adjusted[t.po.FolioItemID]
				c.FolioID, c.AdjustmentItemID = &a.FolioID, &a.AdjustmentItemID
				meta["adjustment_item_id"], meta["folio_id"], meta["charged_item_id"], meta["approved_by"] = a.AdjustmentItemID, a.FolioID, t.po.FolioItemID, approval.UserID()
			}
			if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "stay.rate_changed", stayID, auditlabel.Stay(ctx, propertyID, stayID), map[string]any{"date": c.Date, "amount": c.OldAmount}, meta)); err != nil {
				return err
			}
			changes[i] = c
		}
		out = ChangeRatesResult{Stay: toStay(bumped, pre.line.ReservationID), Changes: changes}
		return nil
	})
	return out, err
}

type rateTarget struct {
	night reservationsdb.ReservationRoomRate
	po    *frontdeskdb.ListStayNightPostingsRow
}

// rateTargets are the nights a change would touch: the night of the date, or it and every later night that is not charged (a charged night is never changed in bulk). A night that already
// has the rate is left out. found says whether the date is a night of the stay.
func rateTargetsOrNil(in ChangeRatesInput, amount decimal.Decimal, nights []reservationsdb.ReservationRoomRate, postings []frontdeskdb.ListStayNightPostingsRow) []rateTarget {
	t, _ := rateTargets(in, amount, nights, postings)
	return t
}

func rateTargets(in ChangeRatesInput, amount decimal.Decimal, nights []reservationsdb.ReservationRoomRate, postings []frontdeskdb.ListStayNightPostingsRow) (targets []rateTarget, found bool) {
	charged := map[civil.Date]frontdeskdb.ListStayNightPostingsRow{}
	for _, po := range postings {
		charged[po.ServiceDate] = po
	}
	for _, n := range nights {
		if n.StayDate.Before(in.Date) || (in.ApplyTo == ApplyNight && !n.StayDate.Equal(in.Date)) {
			continue
		}
		found = found || n.StayDate.Equal(in.Date)
		po, isCharged := charged[n.StayDate]
		switch {
		case isCharged && in.ApplyTo == ApplyRemaining:
			continue
		case n.Amount.Equal(amount):
			continue
		case isCharged:
			targets = append(targets, rateTarget{night: n, po: &po})
		default:
			targets = append(targets, rateTarget{night: n})
		}
	}
	return targets, found
}

// approvalNeeds says which approvals a change needs: the correction of a charged night, and a rate that goes down (the rate approver, unless the caller is one).
func approvalNeeds(targets []rateTarget, amount decimal.Decimal) (corrected, lowered bool) {
	for _, t := range targets {
		corrected = corrected || t.po != nil
		lowered = lowered || amount.LessThan(t.night.Amount)
	}
	return
}
