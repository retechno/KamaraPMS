package reports

import (
	"encoding/csv"
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the reports (docs/architecture/06-api.md, reports). Each report answers JSON, or CSV with
// ?format=csv.
type Handler struct{ svc *Service }

// NewHandler returns the reports HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/reports"
	mux.Handle("GET "+p+"/daily-summary", httpx.HandlerFunc(h.dated(func(r *http.Request, pid int64, d civil.Date) (any, error) {
		return h.svc.DailySummary(r.Context(), pid, d)
	})))
	mux.Handle("GET "+p+"/revenue", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.Revenue(r.Context(), pid, f, t)
	})))
	mux.Handle("GET "+p+"/tax", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.Tax(r.Context(), pid, f, t)
	})))
	mux.Handle("GET "+p+"/cashier", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.Cashier(r.Context(), pid, f, t)
	})))
	mux.Handle("GET "+p+"/statistics", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.Statistics(r.Context(), pid, f, t)
	})))
	mux.Handle("GET "+p+"/arrivals", httpx.HandlerFunc(h.dated(func(r *http.Request, pid int64, d civil.Date) (any, error) {
		return h.svc.Arrivals(r.Context(), pid, d)
	})))
	mux.Handle("GET "+p+"/departures", httpx.HandlerFunc(h.dated(func(r *http.Request, pid int64, d civil.Date) (any, error) {
		return h.svc.Departures(r.Context(), pid, d)
	})))
	mux.Handle("GET /api/v1/properties/{propertyId}/dashboard", httpx.HandlerFunc(h.plain(func(r *http.Request, pid int64) (any, error) { return h.svc.Dashboard(r.Context(), pid) })))
	mux.Handle("GET "+p+"/in-house", httpx.HandlerFunc(h.plain(func(r *http.Request, pid int64) (any, error) { return h.svc.InHouse(r.Context(), pid) })))
	mux.Handle("GET "+p+"/housekeeping-productivity", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.HousekeepingProductivity(r.Context(), pid, f, t)
	})))
	mux.Handle("GET "+p+"/housekeeping-dirty-rooms", httpx.HandlerFunc(h.plain(func(r *http.Request, pid int64) (any, error) {
		hours := 0
		if v := r.URL.Query().Get("min_hours"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, apperr.Invalid("the query is invalid", apperr.FieldError{Field: "min_hours", Code: "INVALID_VALUE", Message: "a whole number of hours"})
			}
			hours = n
		}
		return h.svc.HousekeepingDirty(r.Context(), pid, hours)
	})))
	mux.Handle("GET "+p+"/free-rooms", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		res, err := h.svc.FreeRooms(r.Context(), pid, f, t)
		return FreeRoomsList(res), err
	})))
	mux.Handle("GET "+p+"/free-rooms-by-reason", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		res, err := h.svc.FreeRooms(r.Context(), pid, f, t)
		return FreeRoomsByReason(res), err
	})))
	mux.Handle("GET "+p+"/free-rooms-by-kind", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		res, err := h.svc.FreeRooms(r.Context(), pid, f, t)
		return FreeRoomsByKind(res), err
	})))
	mux.Handle("GET "+p+"/maintenance", httpx.HandlerFunc(h.ranged(func(r *http.Request, pid int64, f, t civil.Date) (any, error) {
		return h.svc.Maintenance(r.Context(), pid, f, t)
	})))
}

func dateParam(r *http.Request, name string, errs *[]apperr.FieldError) civil.Date {
	v := r.URL.Query().Get(name)
	if v == "" {
		*errs = append(*errs, apperr.FieldError{Field: name, Code: "REQUIRED", Message: "YYYY-MM-DD"})
		return civil.Date{}
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		*errs = append(*errs, apperr.FieldError{Field: name, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return d
}

func (h *Handler) plain(f func(*http.Request, int64) (any, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		res, err := f(r, pid)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}
}

func (h *Handler) dated(f func(*http.Request, int64, civil.Date) (any, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		var errs []apperr.FieldError
		d := dateParam(r, "date", &errs)
		if len(errs) > 0 {
			return apperr.Invalid("the query is invalid", errs...)
		}
		res, err := f(r, pid, d)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}
}

func (h *Handler) ranged(f func(*http.Request, int64, civil.Date, civil.Date) (any, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		var errs []apperr.FieldError
		from, to := dateParam(r, "from", &errs), dateParam(r, "to", &errs)
		if len(errs) > 0 {
			return apperr.Invalid("the query is invalid", errs...)
		}
		res, err := f(r, pid, from, to)
		if err != nil {
			return err
		}
		return respond(w, r, res)
	}
}

func respond(w http.ResponseWriter, r *http.Request, res any) error {
	switch r.URL.Query().Get("format") {
	case "", "json":
		return httpx.WriteJSON(w, http.StatusOK, res)
	case "csv":
		t, ok := res.(Table)
		if !ok {
			return apperr.Invalid("the query is invalid", apperr.FieldError{Field: "format", Code: "INVALID_VALUE", Message: "json"})
		}
		header, rows := t.CSV()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		cw := csv.NewWriter(w)
		_ = cw.Write(header)
		for _, row := range rows {
			for i := range row {
				row[i] = SafeCell(row[i])
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		return nil
	}
	return apperr.Invalid("the query is invalid", apperr.FieldError{Field: "format", Code: "INVALID_VALUE", Message: "json or csv"})
}
