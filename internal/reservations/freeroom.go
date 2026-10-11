package reservations

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations/reservationsdb"
	"kamarapms/internal/tenancy"
)

// freeAuth is what a request brings for the complimentary and house use rooms it books: the approval of a manager, and
// whether the request knowingly takes a month over its quota. Like the rate override it is verified once per request,
// and the nights of the lines already priced in the request count against the quota of the next ones.
type freeAuth struct {
	approval   *iam.ApprovalInput
	exceed     bool
	done       bool
	approvedBy int64
	exceeded   []string // the kinds and months that went over the quota, for the audit
	pending    map[string]int
}

type freeKey struct{}

// WithFreeApproval carries the approval of the free rooms of a request (the credentials of a user holding
// reservation.complimentary_approve, not needed when the caller holds it) and whether to go over the monthly quota.
func WithFreeApproval(ctx context.Context, approval *iam.ApprovalInput, exceedQuota bool) context.Context {
	return context.WithValue(ctx, freeKey{}, &freeAuth{approval: approval, exceed: exceedQuota, pending: map[string]int{}})
}

// FreeRoomAudit is what the audit entry of a request records about its free rooms: who approved, and the quotas it went over.
func FreeRoomAudit(ctx context.Context) map[string]any {
	fa, _ := ctx.Value(freeKey{}).(*freeAuth)
	if fa == nil || !fa.done {
		return nil
	}
	out := map[string]any{"free_room_approved_by": fa.approvedBy}
	if len(fa.exceeded) > 0 {
		out["free_quota_exceeded"] = fa.exceeded
	}
	return out
}

func withFreeRoomAudit(ctx context.Context, data map[string]any) map[string]any {
	for k, v := range FreeRoomAudit(ctx) {
		data[k] = v
	}
	return data
}

// requireFreeApproval is the approval of a free room: the caller's own when they hold
// reservation.complimentary_approve, otherwise the credentials of someone who does (422 APPROVAL_REQUIRED).
func (s *Service) requireFreeApproval(ctx context.Context, propertyID int64, fa *freeAuth) error {
	if fa.done {
		return nil
	}
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if s.authz.Require(ctx, propertyID, auth.PermReservationComplimentaryApprove) == nil {
		if id := p.ActorID(); id != nil {
			fa.approvedBy = *id
		}
	} else {
		if s.approver == nil {
			return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "a complimentary or house use room needs an approval: the approver's email and password").
				WithContext("permission", string(auth.PermReservationComplimentaryApprove))
		}
		ap, err := s.approver.VerifyApprovalFor(ctx, propertyID, fa.approval, auth.PermReservationComplimentaryApprove)
		if err != nil {
			return err
		}
		fa.approvedBy = ap.UserID()
	}
	fa.done = true
	return nil
}

// requireFreeRoom is the rule of booking a complimentary or house use room: the permission, the approval of a manager,
// and the monthly quota of its kind (409 FREE_NIGHT_QUOTA_EXCEEDED unless the request says it goes over knowingly; the
// approval is always there by then). excludeLine leaves a line's own stored nights out of the count (amending it).
func (s *Service) requireFreeRoom(ctx context.Context, propertyID, tenantID int64, kind string, arrival, departure civil.Date, excludeLine *int64) error {
	fa, _ := ctx.Value(freeKey{}).(*freeAuth)
	if fa == nil {
		fa = &freeAuth{pending: map[string]int{}}
	}
	if err := s.requireFreeApproval(ctx, propertyID, fa); err != nil {
		return err
	}
	q := s.q(ctx)
	quota, err := q.GetFreeNightQuota(ctx, reservationsdb.GetFreeNightQuotaParams{TenantID: tenantID, PropertyID: propertyID, OccupancyKind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no quota for this kind
	}
	if err != nil {
		return err
	}
	// the nights of the new line, by month
	byMonth := map[civil.Date]int{}
	for d := arrival; d.Before(departure); d = d.AddDays(1) {
		byMonth[civil.NewDate(d.Year(), d.Month(), 1)]++
	}
	for month, nights := range byMonth {
		used, err := q.CountFreeNights(ctx, reservationsdb.CountFreeNightsParams{
			TenantID: tenantID, PropertyID: propertyID, OccupancyKind: kind, FromDate: month, ToDate: civil.NewDate(month.Year(), month.Month()+1, 1), ExcludeLineID: excludeLine,
		})
		if err != nil {
			return err
		}
		key := kind + "|" + month.String()
		total := int(used) + fa.pending[key] + nights
		fa.pending[key] += nights
		if total > int(quota) {
			if !fa.exceed {
				return apperr.Conflict("FREE_NIGHT_QUOTA_EXCEEDED", "this takes the month over its quota of free nights").
					WithContext("occupancy_kind", kind).WithContext("month", month.String()).WithContext("quota", int(quota)).WithContext("used", int(used)+fa.pending[key]-nights).WithContext("requested", nights)
			}
			fa.exceeded = append(fa.exceeded, kind+" "+month.String())
		}
	}
	return nil
}

// FreeNightQuota is the monthly limit of free nights of one kind at a property.
type FreeNightQuota struct {
	OccupancyKind string `json:"occupancy_kind"`
	MonthlyNights int    `json:"monthly_nights"`
}

// FreeNightQuotas lists the quotas of the property (reservation.read); a kind without one has no limit.
func (s *Service) FreeNightQuotas(ctx context.Context, propertyID int64) ([]FreeNightQuota, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListFreeNightQuotas(ctx, reservationsdb.ListFreeNightQuotasParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]FreeNightQuota, len(rows))
	for i, r := range rows {
		out[i] = FreeNightQuota{OccupancyKind: r.OccupancyKind, MonthlyNights: int(r.MonthlyNights)}
	}
	return out, nil
}

// SetFreeNightQuota sets the monthly limit of free nights of a kind, or removes it (rate.manage). It limits the rooms
// booked from now on; what is booked already stays.
func (s *Service) SetFreeNightQuota(ctx context.Context, propertyID int64, kind string, monthlyNights *int) error {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if kind != rates.KindComplimentary && kind != rates.KindHouseUse {
		return apperr.Invalid("the quota is invalid", fieldErr("occupancy_kind", "INVALID_VALUE", "COMPLIMENTARY or HOUSE_USE"))
	}
	if monthlyNights != nil && (*monthlyNights < 0 || *monthlyNights > 100000) {
		return apperr.Invalid("the quota is invalid", fieldErr("monthly_nights", "OUT_OF_RANGE", "between 0 and 100000, or empty for no limit"))
	}
	p, err := s.writer(ctx, propertyID, auth.PermRateManage)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if monthlyNights == nil {
			if err := q.DeleteFreeNightQuota(ctx, reservationsdb.DeleteFreeNightQuotaParams{TenantID: p.TenantID, PropertyID: propertyID, OccupancyKind: kind}); err != nil {
				return err
			}
		} else if err := q.UpsertFreeNightQuota(ctx, reservationsdb.UpsertFreeNightQuotaParams{
			TenantID: p.TenantID, PropertyID: propertyID, OccupancyKind: kind, MonthlyNights: int32(*monthlyNights), ActorID: p.ActorID(), //nolint:gosec // G115: bounded above
		}); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "free_night_quota.set", 0, "", nil, map[string]any{"occupancy_kind": kind, "monthly_nights": monthlyNights}))
	})
}

func (h *Handler) registerQuotas(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/free-night-quotas"
	mux.Handle("GET "+p, httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		out, err := h.svc.FreeNightQuotas(r.Context(), pid)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
	}))
	mux.Handle("PUT "+p+"/{kind}", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		var req struct {
			MonthlyNights *int `json:"monthly_nights"`
		}
		if err := httpx.DecodeJSON(w, r, &req); err != nil {
			return err
		}
		if err := h.svc.SetFreeNightQuota(r.Context(), pid, r.PathValue("kind"), req.MonthlyNights); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}
