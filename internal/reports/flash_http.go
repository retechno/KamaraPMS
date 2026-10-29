package reports

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/reports/reportsdb"
	"kamarapms/internal/tenancy"
)

// RegisterFlash mounts the daily flash report (docs/architecture/38-reports-night-audit-pack.md, 8.3).
func (h *Handler) RegisterFlash(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/reports/daily-flash", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		date, err := h.svc.FlashDateFrom(r.Context(), pid, r)
		if err != nil {
			return err
		}
		res, err := h.svc.DailyFlash(r.Context(), pid, date)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
}

// FlashDateFrom checks report.view and then reads the date of the request: the permission is answered before the query is looked at. Without a date it is the last closed business day,
// or the open day for a property that never closed one.
func (s *Service) FlashDateFrom(ctx context.Context, propertyID int64, r *http.Request) (civil.Date, error) {
	p, _, err := s.access(ctx, propertyID)
	if err != nil {
		return civil.Date{}, err
	}
	if v := r.URL.Query().Get("date"); v != "" {
		d, err := civil.ParseDate(v)
		if err != nil {
			return civil.Date{}, apperr.Invalid("the query is invalid", apperr.FieldError{Field: "date", Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
		}
		return d, nil
	}
	q := s.q(ctx)
	d, err := q.FlashLastClosedDay(ctx, reportsdb.FlashLastClosedDayParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		d, err = q.FlashOpenDay(ctx, reportsdb.FlashOpenDayParams{TenantID: p.TenantID, PropertyID: propertyID})
		if errors.Is(err, pgx.ErrNoRows) {
			return civil.Date{}, apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no business day")
		}
	}
	return d, err
}
