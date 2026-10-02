package nightaudit

import (
	"net/http"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes night audit (docs/architecture/06-api.md §15).
type Handler struct{ svc *Service }

// NewHandler returns the night audit HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes. The room charge routes under the same prefix belong to roomcharge.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/night-audit"
	mux.Handle("GET "+p+"/preview", httpx.HandlerFunc(h.preview))
	mux.Handle("POST "+p+"/no-shows", httpx.HandlerFunc(h.noShows))
	mux.Handle("POST "+p+"/run", httpx.HandlerFunc(h.run))
}

func parseDate(s *string) (civil.Date, error) {
	if s == nil {
		return civil.Date{}, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "business_date", Code: "REQUIRED", Message: "YYYY-MM-DD"})
	}
	d, err := civil.ParseDate(*s)
	if err != nil {
		return d, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "business_date", Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return d, nil
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	res, err := h.svc.Preview(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type noShowRequest struct {
	BusinessDate       *string `json:"business_date"`
	ReservationRoomIDs []int64 `json:"reservation_room_ids"`
	Confirm            bool    `json:"confirm"`
	Reason             string  `json:"reason"`
}

func (h *Handler) noShows(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req noShowRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	bd, err := parseDate(req.BusinessDate)
	if err != nil {
		return err
	}
	res, err := h.svc.NoShows(r.Context(), pid, bd, req.ReservationRoomIDs, req.Confirm, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type runRequest struct {
	BusinessDate *string `json:"business_date"`
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req runRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	bd, err := parseDate(req.BusinessDate)
	if err != nil {
		return err
	}
	res, err := h.svc.Run(r.Context(), pid, bd)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
