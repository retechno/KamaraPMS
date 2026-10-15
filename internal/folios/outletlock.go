package folios

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The lock of a folio for outlet charges (docs/architecture/34-pos.md section 6).
//
// A folio (the guest folio or a company folio of an in-house stay, or the master folio of a group) is closed to outlets until the front desk opens it: with a limit (required), a validity
// (until check-out, or this business day only) and the outlets that may charge it. No row means locked. The amount used against the limit is never stored and is never the balance of the
// folio: it is the sum of the outlet charges of the lock period that were not returned (pos_room_charges), read under the folio's FOR UPDATE lock, which is the one that serialises
// every charge to that folio. Every open, change and lock is audited and is a row of folio_outlet_lock_events; every attempt of an outlet is a row of pos_room_charge_attempts.

const (
	// OutletLockOpen and OutletLockLocked are the two states of a lock.
	OutletLockOpen   = "OPEN"
	OutletLockLocked = "LOCKED"
	// ValidUntilClose lasts until the check-out (or until the master folio closes); ValidToday only until the end of the business day it was opened.
	ValidUntilClose = "UNTIL_CLOSE"
	ValidToday      = "TODAY"

	// The reasons a lock is locked.
	LockReasonNeverOpened = "NEVER_OPENED"
	LockReasonManual      = "MANUAL"
	LockReasonLimit       = "LIMIT_REACHED"
	LockReasonCheckout    = "CHECKOUT"
	LockReasonExpired     = "EXPIRED"
	LockReasonFolioClosed = "FOLIO_CLOSED"

	// ChargeSourceCharge and ChargeSourceMove are the two actions that originate in an outlet and are checked by the gate.
	ChargeSourceCharge = "CHARGE"
	ChargeSourceMove   = "MOVE"

	maxLockOutlets = 50
)

// OutletRef is an outlet as the lock names it.
type OutletRef struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
}

// OutletLockEvent is one change of a lock.
type OutletLockEvent struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Reason       *string         `json:"reason"`
	Old          json.RawMessage `json:"old"`
	New          json.RawMessage `json:"new"`
	BusinessDate civil.Date      `json:"business_date"`
	ActorName    string          `json:"actor_name"`
	At           time.Time       `json:"at"`
}

// OutletLock is the lock of a folio as the screens show it.
type OutletLock struct {
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
	FolioType   string `json:"folio_type"`
	FolioStatus string `json:"folio_status"`
	RoomNumber  string `json:"room_number,omitempty"`
	GuestName   string `json:"guest_name,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
	// CheckoutDate is the departure date of the stay (what "until check-out" means for the screen); empty for a master.
	CheckoutDate *civil.Date `json:"checkout_date"`
	// Status is OPEN or LOCKED; Reason says why a locked folio is (NEVER_OPENED when it never was open).
	Status string  `json:"status"`
	Reason *string `json:"reason"`
	// CanOpen says whether the folio can be opened for outlets at all; WhyNot says why not (FOLIO_CLOSED, STAY_NOT_IN_HOUSE, GROUP_INACTIVE, FOLIO_TYPE).
	CanOpen      bool              `json:"can_open"`
	WhyNot       *string           `json:"why_not"`
	Limit        *decimal.Decimal  `json:"limit_amount"`
	Used         *decimal.Decimal  `json:"used"`
	Remaining    *decimal.Decimal  `json:"remaining"`
	ValidMode    *string           `json:"valid_mode"`
	ValidOn      *civil.Date       `json:"valid_on"`
	PeriodNo     int32             `json:"period_no"`
	Outlets      []OutletRef       `json:"outlets"`
	OpenedAt     *time.Time        `json:"opened_at"`
	UpdatedAt    *time.Time        `json:"updated_at"`
	Version      int32             `json:"version"`
	FolioVersion int32             `json:"folio_version"`
	Events       []OutletLockEvent `json:"events,omitempty"`
	BusinessDate civil.Date        `json:"business_date"`
}

// OutletLockInput opens a lock or changes an open one. The limit is required; there is no unlimited.
type OutletLockInput struct {
	LimitAmount decimal.Decimal `json:"limit_amount"`
	ValidMode   string          `json:"valid_mode"`
	OutletIDs   []int64         `json:"outlet_ids"`
	// Version is the version of the lock the screen loaded; required to change an open lock.
	Version *int32 `json:"version"`
}

// OutletLockPatch locks a folio by hand.
type OutletLockPatch struct {
	Version *int32 `json:"version"`
}

func errOutletLockVersion(current int32) *apperr.Error {
	return apperr.Conflict("VERSION_CONFLICT", "the lock was changed by someone else").WithContext("current_version", current)
}

// lockEligibility says whether a folio can be opened for outlets and, when it cannot, why. A folio of an in-house stay (guest or company) and the master of an active group can.
func (s *Service) lockEligibility(ctx context.Context, tenantID, propertyID int64, f foliosdb.GetFolioForOutletLockRow) (bool, string, error) {
	if f.Status != "OPEN" {
		return false, "FOLIO_CLOSED", nil
	}
	switch f.FolioType {
	case "GUEST", "COMPANY":
		if f.StayID == nil || f.StayStatus != "OPEN" {
			return false, "STAY_NOT_IN_HOUSE", nil
		}
		return true, "", nil
	case FolioTypeMaster:
		if f.BookingGroupID == nil {
			return false, "FOLIO_TYPE", nil
		}
		g, err := s.q(ctx).GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: tenantID, PropertyID: propertyID, ID: *f.BookingGroupID})
		if err != nil {
			return false, "", err
		}
		if !g.IsActive {
			return false, "GROUP_INACTIVE", nil
		}
		return true, "", nil
	}
	return false, "FOLIO_TYPE", nil
}

func lockJSON(status string, limit decimal.Decimal, mode string, validOn *civil.Date, outlets []int64, reason *string) json.RawMessage {
	m := map[string]any{"status": status, "limit_amount": limit.String(), "valid_mode": mode, "valid_on": validOn, "outlet_ids": outlets}
	if reason != nil {
		m["reason"] = *reason
	}
	b, _ := json.Marshal(m)
	return b
}

// view builds the lock of a folio. The caller holds whatever locks it needs; a read takes none.
func (s *Service) outletLockView(ctx context.Context, tenantID, propertyID int64, f foliosdb.GetFolioForOutletLockRow, today civil.Date, withEvents bool) (OutletLock, error) {
	q := s.q(ctx)
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return OutletLock{}, err
	}
	ok, why, err := s.lockEligibility(ctx, tenantID, propertyID, f)
	if err != nil {
		return OutletLock{}, err
	}
	out := OutletLock{
		FolioID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, FolioStatus: f.Status, RoomNumber: f.RoomNumber, GroupName: f.GroupName, CheckoutDate: f.CheckoutDate,
		Status: OutletLockLocked, CanOpen: ok, Outlets: []OutletRef{}, FolioVersion: f.Version, BusinessDate: today,
	}
	if f.FirstName != nil || f.LastName != nil {
		out.GuestName = strings.TrimSpace(deref(f.FirstName) + " " + deref(f.LastName))
	}
	if why != "" {
		out.WhyNot = &why
	}
	row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: tenantID, PropertyID: propertyID, FolioID: f.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		r := LockReasonNeverOpened
		out.Reason = &r
	} else if err != nil {
		return OutletLock{}, err
	} else {
		out.Version, out.PeriodNo = row.Version, row.PeriodNo
		limit := row.LimitAmount.Round(decimals)
		out.Limit, out.ValidMode, out.ValidOn = &limit, &row.ValidMode, row.ValidOn
		out.OpenedAt, out.UpdatedAt = &row.OpenedAt, &row.UpdatedAt
		out.Status, out.Reason = row.Status, row.LockedReason
		// A lock of this business day only is over when the business day has moved on, even before anything wrote that down.
		if row.Status == OutletLockOpen && row.ValidMode == ValidToday && row.ValidOn != nil && row.ValidOn.Before(today) {
			out.Status = OutletLockLocked
			r := LockReasonExpired
			out.Reason = &r
		}
		used, err := q.OutletChargesUsed(ctx, foliosdb.OutletChargesUsedParams{TenantID: tenantID, PropertyID: propertyID, FolioID: f.ID, PeriodNo: row.PeriodNo})
		if err != nil {
			return OutletLock{}, err
		}
		u := used.Round(decimals)
		rem := decimal.Max(limit.Sub(u), decimal.Zero)
		out.Used, out.Remaining = &u, &rem
		outlets, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: tenantID, PropertyID: propertyID, FolioID: f.ID})
		if err != nil {
			return OutletLock{}, err
		}
		for _, o := range outlets {
			out.Outlets = append(out.Outlets, OutletRef{ID: o.ID, Code: o.Code, Name: o.Name, IsActive: o.IsActive})
		}
	}
	if withEvents {
		evs, err := q.ListOutletLockEvents(ctx, foliosdb.ListOutletLockEventsParams{TenantID: tenantID, PropertyID: propertyID, FolioID: f.ID})
		if err != nil {
			return OutletLock{}, err
		}
		out.Events = make([]OutletLockEvent, 0, len(evs))
		for _, e := range evs {
			out.Events = append(out.Events, OutletLockEvent{ID: e.ID, Kind: e.Kind, Reason: e.Reason, Old: e.OldValue, New: e.NewValue, BusinessDate: e.BusinessDate, ActorName: e.ActorName, At: e.CreatedAt})
		}
	}
	return out, nil
}

// GetOutletLock reads the lock of a folio: who may open it, or who may read folios.
func (s *Service) GetOutletLock(ctx context.Context, propertyID, folioID int64) (OutletLock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return OutletLock{}, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, auth.PermFolioOutletChargeOpen, auth.PermFolioRead); err != nil {
		return OutletLock{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return OutletLock{}, err
	}
	f, err := s.q(ctx).GetFolioForOutletLock(ctx, foliosdb.GetFolioForOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return OutletLock{}, orNotFound(err, errFolioNotFound())
	}
	return s.outletLockView(ctx, p.TenantID, propertyID, f, day.BusinessDate, true)
}

func validateLockInput(in OutletLockInput, decimals int32) []apperr.FieldError {
	var fields []apperr.FieldError
	switch {
	case !in.LimitAmount.IsPositive():
		fields = append(fields, fieldErr("limit_amount", "REQUIRED", "the limit of outlet charges is required and must be above zero"))
	case in.LimitAmount.Exponent() < -decimals:
		fields = append(fields, fieldErr("limit_amount", "INVALID_VALUE", "more decimals than the currency has"))
	}
	if in.ValidMode != ValidUntilClose && in.ValidMode != ValidToday {
		fields = append(fields, fieldErr("valid_mode", "INVALID_VALUE", "UNTIL_CLOSE or TODAY"))
	}
	if len(in.OutletIDs) == 0 {
		fields = append(fields, fieldErr("outlet_ids", "REQUIRED", "choose at least one outlet that may charge this folio"))
	}
	if len(in.OutletIDs) > maxLockOutlets {
		fields = append(fields, fieldErr("outlet_ids", "INVALID_VALUE", "too many outlets"))
	}
	return fields
}

func dedupe(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// SetOutletLock opens the folio for outlets, or changes the limit, the validity and the outlets of an open lock (folio.outlet_charge_open). Opening starts a new period: what an earlier
// period used never counts against the new limit. A limit below what the period has used is refused. The folio is locked FOR UPDATE first, so a change never interleaves with a charge.
func (s *Service) SetOutletLock(ctx context.Context, propertyID, folioID int64, in OutletLockInput) (OutletLock, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioOutletChargeOpen)
	if err != nil {
		return OutletLock{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return OutletLock{}, err
	}
	if fields := validateLockInput(in, decimals); len(fields) > 0 {
		return OutletLock{}, apperr.Invalid("the lock is invalid", fields...)
	}
	in.OutletIDs = dedupe(in.OutletIDs)
	var out OutletLock
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, []int64{folioID}); err != nil { // L42
			return mapNotFound(err, errFolioNotFound())
		}
		q := s.q(ctx)
		f, err := q.GetFolioForOutletLock(ctx, foliosdb.GetFolioForOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
		if err != nil {
			return orNotFound(err, errFolioNotFound())
		}
		ok, why, err := s.lockEligibility(ctx, p.TenantID, propertyID, f)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.Conflict("OUTLET_LOCK_NOT_ALLOWED", "this folio cannot be opened for outlets").WithContext("reason", why).WithContext("folio_id", folioID)
		}
		for i, id := range in.OutletIDs {
			o, err := q.OutletInfo(ctx, foliosdb.OutletInfoParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Invalid("the lock is invalid", fieldErr("outlet_ids", "OUTLET_NOT_FOUND", "no such outlet in this property"))
			}
			if err != nil {
				return err
			}
			if !o.IsActive {
				return apperr.Invalid("the lock is invalid", fieldErr("outlet_ids", "INACTIVE", "an outlet is switched off: "+o.Code))
			}
			_ = i
		}
		var validOn *civil.Date
		if in.ValidMode == ValidToday {
			d := day.BusinessDate
			validOn = &d
		}
		existing, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
		hasRow := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		label := auditlabel.Folio(ctx, propertyID, folioID)
		// a lock of an earlier day is over: opening it again is an opening
		isOpen := hasRow && existing.Status == OutletLockOpen && (existing.ValidMode != ValidToday || existing.ValidOn == nil || !existing.ValidOn.Before(day.BusinessDate))
		switch {
		case !isOpen:
			period := int32(1)
			var oldVal json.RawMessage
			var oldOutlets []int64
			if hasRow {
				period = existing.PeriodNo + 1
				outs, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
				if err != nil {
					return err
				}
				for _, o := range outs {
					oldOutlets = append(oldOutlets, o.ID)
				}
				reason := existing.LockedReason
				if existing.Status == OutletLockOpen {
					r := LockReasonExpired
					reason = &r
				}
				oldVal = lockJSON(OutletLockLocked, existing.LimitAmount, existing.ValidMode, existing.ValidOn, oldOutlets, reason)
				if in.Version != nil && *in.Version != existing.Version {
					return errOutletLockVersion(existing.Version)
				}
				if err := q.UpdateOutletLock(ctx, foliosdb.UpdateOutletLockParams{
					TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Status: OutletLockOpen, LimitAmount: in.LimitAmount, ValidMode: in.ValidMode, ValidOn: validOn,
					PeriodNo: period, LockedReason: nil, OpenedBy: p.ActorID(), OpenedAt: ptrTime(s.clock.Now()), ActorID: p.ActorID(),
				}); err != nil {
					return err
				}
			} else if err := q.InsertOutletLock(ctx, foliosdb.InsertOutletLockParams{
				TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Status: OutletLockOpen, LimitAmount: in.LimitAmount, ValidMode: in.ValidMode, ValidOn: validOn,
				PeriodNo: period, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
			if err := s.replaceLockOutlets(ctx, p.TenantID, propertyID, folioID, in.OutletIDs); err != nil {
				return err
			}
			newVal := lockJSON(OutletLockOpen, in.LimitAmount, in.ValidMode, validOn, in.OutletIDs, nil)
			if err := q.InsertOutletLockEvent(ctx, foliosdb.InsertOutletLockEventParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Kind: "OPENED",
				OldValue: oldVal, NewValue: newVal, BusinessDate: day.BusinessDate, ActorID: p.ActorID()}); err != nil {
				return err
			}
			if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.outlet_lock_opened", "folio", folioID, label, oldVal, newVal)); err != nil {
				return err
			}
		default:
			if in.Version == nil {
				return apperr.Invalid("the lock is invalid", fieldErr("version", "REQUIRED", "the version of the lock you loaded"))
			}
			if *in.Version != existing.Version {
				return errOutletLockVersion(existing.Version)
			}
			used, err := q.OutletChargesUsed(ctx, foliosdb.OutletChargesUsedParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, PeriodNo: existing.PeriodNo})
			if err != nil {
				return err
			}
			if in.LimitAmount.LessThan(used) {
				return apperr.Conflict("LIMIT_BELOW_USED", "the limit cannot be below what outlets have already charged").
					WithContext("used", used.StringFixed(decimals)).WithContext("limit", in.LimitAmount.StringFixed(decimals))
			}
			outs, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
			if err != nil {
				return err
			}
			oldOutlets := make([]int64, 0, len(outs))
			for _, o := range outs {
				oldOutlets = append(oldOutlets, o.ID)
			}
			oldVal := lockJSON(OutletLockOpen, existing.LimitAmount, existing.ValidMode, existing.ValidOn, oldOutlets, nil)
			newVal := lockJSON(OutletLockOpen, in.LimitAmount, in.ValidMode, validOn, in.OutletIDs, nil)
			if err := q.UpdateOutletLock(ctx, foliosdb.UpdateOutletLockParams{
				TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Status: OutletLockOpen, LimitAmount: in.LimitAmount, ValidMode: in.ValidMode, ValidOn: validOn,
				PeriodNo: existing.PeriodNo, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
			if err := s.replaceLockOutlets(ctx, p.TenantID, propertyID, folioID, in.OutletIDs); err != nil {
				return err
			}
			if err := q.InsertOutletLockEvent(ctx, foliosdb.InsertOutletLockEventParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Kind: "CHANGED",
				OldValue: oldVal, NewValue: newVal, BusinessDate: day.BusinessDate, ActorID: p.ActorID()}); err != nil {
				return err
			}
			if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.outlet_lock_changed", "folio", folioID, label, oldVal, newVal)); err != nil {
				return err
			}
		}
		f2, err := q.GetFolioForOutletLock(ctx, foliosdb.GetFolioForOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
		if err != nil {
			return err
		}
		out, err = s.outletLockView(ctx, p.TenantID, propertyID, f2, day.BusinessDate, true)
		return err
	})
	return out, err
}

func ptrTime(t time.Time) *time.Time { return &t }

func (s *Service) replaceLockOutlets(ctx context.Context, tenantID, propertyID, folioID int64, ids []int64) error {
	q := s.q(ctx)
	if err := q.DeleteLockOutlets(ctx, foliosdb.DeleteLockOutletsParams{TenantID: tenantID, PropertyID: propertyID, FolioID: folioID}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := q.InsertLockOutlet(ctx, foliosdb.InsertLockOutletParams{TenantID: tenantID, PropertyID: propertyID, FolioID: folioID, OutletID: id}); err != nil {
			return err
		}
	}
	return nil
}

// LockOutletFolio locks an open folio by hand (folio.outlet_charge_open). Locking a folio that is locked already answers it as it is.
func (s *Service) LockOutletFolio(ctx context.Context, propertyID, folioID int64, patch OutletLockPatch) (OutletLock, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioOutletChargeOpen)
	if err != nil {
		return OutletLock{}, err
	}
	var out OutletLock
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, []int64{folioID}); err != nil {
			return mapNotFound(err, errFolioNotFound())
		}
		q := s.q(ctx)
		row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
		if err == nil {
			if patch.Version != nil && *patch.Version != row.Version {
				return errOutletLockVersion(row.Version)
			}
			if row.Status == OutletLockOpen {
				if err := s.writeLocked(ctx, p, propertyID, folioID, row, LockReasonManual, day.BusinessDate, true); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		f, err := q.GetFolioForOutletLock(ctx, foliosdb.GetFolioForOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
		if err != nil {
			return orNotFound(err, errFolioNotFound())
		}
		out, err = s.outletLockView(ctx, p.TenantID, propertyID, f, day.BusinessDate, true)
		return err
	})
	return out, err
}

// writeLocked turns an open lock into a locked one: the row, the event and the audit entry. The folio is locked FOR UPDATE by the caller.
func (s *Service) writeLocked(ctx context.Context, p auth.Principal, propertyID, folioID int64, row foliosdb.GetOutletLockRow, reason string, bd civil.Date, byPerson bool) error {
	q := s.q(ctx)
	var actor *int64
	if byPerson {
		actor = p.ActorID()
	}
	outs, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(outs))
	for _, o := range outs {
		ids = append(ids, o.ID)
	}
	if err := q.UpdateOutletLock(ctx, foliosdb.UpdateOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Status: OutletLockLocked,
		LimitAmount: row.LimitAmount, ValidMode: row.ValidMode, ValidOn: row.ValidOn, PeriodNo: row.PeriodNo, LockedReason: &reason, ActorID: actor}); err != nil {
		return err
	}
	oldVal := lockJSON(OutletLockOpen, row.LimitAmount, row.ValidMode, row.ValidOn, ids, nil)
	newVal := lockJSON(OutletLockLocked, row.LimitAmount, row.ValidMode, row.ValidOn, ids, &reason)
	if err := q.InsertOutletLockEvent(ctx, foliosdb.InsertOutletLockEventParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Kind: "LOCKED",
		Reason: &reason, OldValue: oldVal, NewValue: newVal, BusinessDate: bd, ActorID: actor}); err != nil {
		return err
	}
	e := auditEntry(p, propertyID, bd, "folio.outlet_lock_locked", "folio", folioID, auditlabel.Folio(ctx, propertyID, folioID), oldVal, newVal)
	if !byPerson {
		e.UserID = nil // the system locked it: the entry says why in its new value
	}
	return s.audit.Write(ctx, e)
}

// lockOnClose locks the open locks of folios that are closing (check-out, close of the master): the folio cannot be charged any more, and the lock says why. The caller holds the folios.
func (s *Service) lockOnClose(ctx context.Context, p auth.Principal, propertyID int64, folioIDs []int64, reason string) error {
	if len(folioIDs) == 0 {
		return nil
	}
	// The business day is held in share mode by the use case that closes the folios (it took it first, before the folios); here it is only read: taking it again would be a lock
	// of level 10 after level 42.
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	for _, id := range folioIDs {
		row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: id})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Status != OutletLockOpen) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.writeLocked(ctx, p, propertyID, id, row, reason, day.BusinessDate, false); err != nil {
			return err
		}
	}
	return nil
}

// ExpireOutletLocks locks the locks of "this business day only" that are over at the close of the day (the night audit calls it before it moves the day on). The check at charge time does not
// depend on it: it only makes the lists and the audit right the next morning.
func (s *Service) ExpireOutletLocks(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (int, error) {
	q := s.q(ctx)
	ids, err := q.ListExpiredTodayLocks(ctx, foliosdb.ListExpiredTodayLocksParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: &bd})
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: id})
		if err != nil || row.Status != OutletLockOpen {
			continue
		}
		if err := s.writeLocked(ctx, p, propertyID, id, row, LockReasonExpired, bd, false); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// OutletChargeCheck is what an outlet asks the gate: may this amount be charged to this folio.
type OutletChargeCheck struct {
	PropertyID    int64
	BusinessDate  civil.Date
	TargetFolioID int64 // the folio the charge lands on: the guest folio, the company folio, or the master when routed
	OutletID      int64
	Amount        decimal.Decimal
	Source        string // ChargeSourceCharge or ChargeSourceMove
	BillID        *int64
	BillNumber    string
	StayID        *int64 // the stay the charge is for (a master has none of its own)
	FromFolioID   *int64
}

// OutletChargeDecision is the answer of the gate. When Refusal is set the attempt was written (REJECTED) and the caller commits that and answers the refusal: the attempt rows are the only
// writes of the transaction. When it is nil the charge may be posted; the caller then calls RecordOutletCharge.
type OutletChargeDecision struct {
	Refusal   *apperr.Error
	PeriodNo  int32
	Limit     decimal.Decimal
	Used      decimal.Decimal
	Remaining decimal.Decimal
}

// CheckOutletCharge decides steps 3 to 7 of the design (34-pos.md 6.3). The caller is in a transaction, holds the open business day in share mode and the target folio FOR UPDATE (level
// 42): the amount used is read under that lock, so two charges at once cannot pass the limit. It writes the attempt row of a refusal (and the expiry of a lock of an earlier day); it posts
// nothing. The lock applies to everyone who acts for an outlet, the holder of folio.transfer_any included: that permission widens where a front office transfer may go, never this.
func (s *Service) CheckOutletCharge(ctx context.Context, p auth.Principal, c OutletChargeCheck) (OutletChargeDecision, error) {
	q := s.q(ctx)
	refuse := func(code, msg string, ctxv map[string]any) (OutletChargeDecision, error) {
		e := apperr.Conflict(code, msg)
		for k, v := range ctxv {
			e = e.WithContext(k, v)
		}
		b, _ := json.Marshal(ctxv)
		codeS := code
		_, err := q.InsertChargeAttempt(ctx, foliosdb.InsertChargeAttemptParams{
			TenantID: p.TenantID, PropertyID: c.PropertyID, Source: c.Source, FolioID: c.TargetFolioID, RequestedStayID: c.StayID, OutletID: c.OutletID, BillID: c.BillID,
			BillNumber: nullable(c.BillNumber), Amount: c.Amount, Result: "REJECTED", Code: &codeS, Context: b, BusinessDate: c.BusinessDate, ActorID: p.ActorID(),
		})
		return OutletChargeDecision{Refusal: e}, err
	}
	decimals, err := s.decimals(ctx, c.PropertyID)
	if err != nil {
		return OutletChargeDecision{}, err
	}
	f, err := q.GetFolioForOutletLock(ctx, foliosdb.GetFolioForOutletLockParams{TenantID: p.TenantID, PropertyID: c.PropertyID, ID: c.TargetFolioID})
	if err != nil {
		return OutletChargeDecision{}, orNotFound(err, errFolioNotFound())
	}
	target := f.FolioType
	if f.Status != "OPEN" {
		return refuse("FOLIO_CLOSED", "the folio is closed", map[string]any{"folio_id": f.ID, "folio_number": f.FolioNumber, "target": target})
	}
	row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: c.PropertyID, FolioID: c.TargetFolioID})
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse("OUTLET_FOLIO_LOCKED", "the folio is locked for outlet charges: ask the front desk to open it",
			map[string]any{"target_folio_id": f.ID, "target": target, "reason": LockReasonNeverOpened})
	}
	if err != nil {
		return OutletChargeDecision{}, err
	}
	if row.Status != OutletLockOpen {
		reason := LockReasonManual
		if row.LockedReason != nil {
			reason = *row.LockedReason
		}
		return refuse("OUTLET_FOLIO_LOCKED", "the folio is locked for outlet charges: ask the front desk to open it",
			map[string]any{"target_folio_id": f.ID, "target": target, "reason": reason})
	}
	if row.ValidMode == ValidToday && row.ValidOn != nil && row.ValidOn.Before(c.BusinessDate) {
		if err := s.writeLocked(ctx, p, c.PropertyID, c.TargetFolioID, row, LockReasonExpired, c.BusinessDate, false); err != nil {
			return OutletChargeDecision{}, err
		}
		return refuse("OUTLET_FOLIO_LOCKED", "the lock of this folio was for one business day and is over: ask the front desk to open it again",
			map[string]any{"target_folio_id": f.ID, "target": target, "reason": LockReasonExpired})
	}
	allowed, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: p.TenantID, PropertyID: c.PropertyID, FolioID: c.TargetFolioID})
	if err != nil {
		return OutletChargeDecision{}, err
	}
	ok := false
	for _, o := range allowed {
		if o.ID == c.OutletID {
			ok = true
		}
	}
	if !ok {
		return refuse("OUTLET_NOT_ALLOWED", "this outlet may not charge this folio", map[string]any{"target_folio_id": f.ID, "target": target, "outlet_id": c.OutletID})
	}
	used, err := q.OutletChargesUsed(ctx, foliosdb.OutletChargesUsedParams{TenantID: p.TenantID, PropertyID: c.PropertyID, FolioID: c.TargetFolioID, PeriodNo: row.PeriodNo})
	if err != nil {
		return OutletChargeDecision{}, err
	}
	remaining := decimal.Max(row.LimitAmount.Sub(used), decimal.Zero)
	if used.Add(c.Amount).GreaterThan(row.LimitAmount) {
		return refuse("OUTLET_LIMIT_EXCEEDED", "the charge is above the limit of outlet charges of this folio", map[string]any{
			"target_folio_id": f.ID, "target": target, "limit": row.LimitAmount.StringFixed(decimals), "used": used.StringFixed(decimals),
			"remaining": remaining.StringFixed(decimals), "requested": c.Amount.StringFixed(decimals),
		})
	}
	return OutletChargeDecision{PeriodNo: row.PeriodNo, Limit: row.LimitAmount, Used: used, Remaining: remaining}, nil
}

// RecordOutletCharge writes an accepted charge: the row that counts against the limit, the attempt, and, when the limit is now reached exactly, the automatic lock (LIMIT_REACHED). The
// caller has posted the transfer (its batch id) in the same transaction and still holds the folio. It returns the id of the charge.
func (s *Service) RecordOutletCharge(ctx context.Context, p auth.Principal, c OutletChargeCheck, d OutletChargeDecision, transferBatchID *int64, idempotencyKey *string) (int64, error) {
	q := s.q(ctx)
	id, err := q.InsertRoomChargeAccepted(ctx, foliosdb.InsertRoomChargeAcceptedParams{
		TenantID: p.TenantID, PropertyID: c.PropertyID, OutletID: c.OutletID, BillID: c.BillID, BillNumber: nullable(c.BillNumber), FromFolioID: c.FromFolioID, ToFolioID: c.TargetFolioID,
		StayID: c.StayID, Amount: c.Amount, PeriodNo: d.PeriodNo, TransferBatchID: transferBatchID, BusinessDate: c.BusinessDate, IdempotencyKey: idempotencyKey, ActorID: p.ActorID(),
	})
	if err != nil {
		return 0, err
	}
	if _, err := q.InsertChargeAttempt(ctx, foliosdb.InsertChargeAttemptParams{
		TenantID: p.TenantID, PropertyID: c.PropertyID, Source: c.Source, FolioID: c.TargetFolioID, RequestedStayID: c.StayID, OutletID: c.OutletID, BillID: c.BillID,
		BillNumber: nullable(c.BillNumber), Amount: c.Amount, Result: "ACCEPTED", Context: []byte(`{}`), PosRoomChargeID: &id, BusinessDate: c.BusinessDate, ActorID: p.ActorID(),
	}); err != nil {
		return 0, err
	}
	if !d.Used.Add(c.Amount).LessThan(d.Limit) { // used + amount = limit: the next charge is refused, and the screens say why
		row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: c.PropertyID, FolioID: c.TargetFolioID})
		if err != nil {
			return 0, err
		}
		if err := s.writeLocked(ctx, p, c.PropertyID, c.TargetFolioID, row, LockReasonLimit, c.BusinessDate, false); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// OutletChargeRow is one line of the list of incoming outlet charges: an accepted charge or a refused attempt.
type OutletChargeRow struct {
	ID            int64           `json:"id"`
	Source        string          `json:"source"`
	FolioID       int64           `json:"folio_id"`
	FolioNumber   string          `json:"folio_number"`
	FolioType     string          `json:"folio_type"`
	RoomNumber    string          `json:"room_number,omitempty"`
	GuestName     string          `json:"guest_name,omitempty"`
	GroupName     string          `json:"group_name,omitempty"`
	OutletID      int64           `json:"outlet_id"`
	OutletCode    string          `json:"outlet_code"`
	OutletName    string          `json:"outlet_name"`
	BillNumber    string          `json:"bill_number,omitempty"`
	Amount        decimal.Decimal `json:"amount"`
	Result        string          `json:"result"`
	Code          *string         `json:"code"`
	Context       json.RawMessage `json:"context"`
	LockStatus    string          `json:"lock_status,omitempty"`
	CreatedByName string          `json:"created_by_name,omitempty"`
	At            time.Time       `json:"at"`
}

// OutletChargeList is the incoming outlet charges of a business date, every room.
type OutletChargeList struct {
	BusinessDate civil.Date        `json:"business_date"`
	Data         []OutletChargeRow `json:"data"`
	Accepted     int               `json:"accepted"`
	Rejected     int               `json:"rejected"`
}

// ListOutletCharges lists the outlet charges of a business date (the open one by default), accepted and refused, newest first (folio.outlet_charge_open or folio.read).
func (s *Service) ListOutletCharges(ctx context.Context, propertyID int64, date *civil.Date, result string) (OutletChargeList, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return OutletChargeList{}, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, auth.PermFolioOutletChargeOpen, auth.PermFolioRead); err != nil {
		return OutletChargeList{}, err
	}
	var bd civil.Date
	if date != nil {
		bd = *date
	} else {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return OutletChargeList{}, err
		}
		bd = day.BusinessDate
	}
	var res *string
	if result != "" {
		if result != "ACCEPTED" && result != "REJECTED" {
			return OutletChargeList{}, apperr.Invalid("the filter is invalid", fieldErr("result", "INVALID_VALUE", "ACCEPTED or REJECTED"))
		}
		res = &result
	}
	rows, err := s.q(ctx).ListChargeAttempts(ctx, foliosdb.ListChargeAttemptsParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, Result: res, RowLimit: 500})
	if err != nil {
		return OutletChargeList{}, err
	}
	out := OutletChargeList{BusinessDate: bd, Data: make([]OutletChargeRow, 0, len(rows))}
	for _, r := range rows {
		if r.Result == "ACCEPTED" {
			out.Accepted++
		} else {
			out.Rejected++
		}
		ctxv := json.RawMessage(r.Context)
		if len(ctxv) == 0 {
			ctxv = json.RawMessage(`{}`)
		}
		out.Data = append(out.Data, OutletChargeRow{
			ID: r.ID, Source: r.Source, FolioID: r.FolioID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, RoomNumber: r.RoomNumber, GuestName: r.GuestName, GroupName: r.GroupName,
			OutletID: r.OutletID, OutletCode: r.OutletCode, OutletName: r.OutletName, BillNumber: deref(r.BillNumber), Amount: r.Amount, Result: r.Result, Code: r.Code,
			Context: ctxv, LockStatus: r.LockStatus, CreatedByName: r.CreatedByName, At: r.CreatedAt,
		})
	}
	return out, nil
}

var _ = audit.Entry{}
