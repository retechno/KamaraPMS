package reports

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// RegisterNap mounts the reports of docs/architecture/38-reports-night-audit-pack.md 7.2 to 7.5 (the daily flash report is RegisterFlash). Each handler asks for its permission before it reads the
// query.
func (h *Handler) RegisterNap(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/reports"
	query := func(r *http.Request) func(string) string {
		return func(k string) string { return r.URL.Query().Get(k) }
	}
	mux.Handle("GET "+p+"/cancellations", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		in, err := h.svc.CancellationParamsFrom(r.Context(), pid, query(r))
		if err != nil {
			return err
		}
		res, err := h.svc.Cancellations(r.Context(), pid, in)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
	mux.Handle("GET "+p+"/guest-ledger", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		at, err := h.svc.AsOfFrom(r.Context(), pid, query(r))
		if err != nil {
			return err
		}
		res, err := h.svc.GuestLedger(r.Context(), pid, at)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
	mux.Handle("GET "+p+"/deposit-ledger", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		at, err := h.svc.AsOfFrom(r.Context(), pid, query(r))
		if err != nil {
			return err
		}
		res, err := h.svc.DepositLedger(r.Context(), pid, at)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
	mux.Handle("GET "+p+"/corrections", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		in, err := h.svc.CorrectionParamsFrom(r.Context(), pid, query(r))
		if err != nil {
			return err
		}
		res, err := h.svc.Corrections(r.Context(), pid, in)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
	mux.Handle("GET "+p+"/z-reports", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		day, err := h.svc.ZDateFrom(r.Context(), pid, r)
		if err != nil {
			return err
		}
		res, err := h.svc.ZReports(r.Context(), pid, day)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}))
}

// businessDayErr is the answer for a date that is not a business day of the property.
func businessDayErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no business day on that date")
	}
	return err
}
