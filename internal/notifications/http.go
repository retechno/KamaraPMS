package notifications

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the e-mail status of a reservation and the resend action.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/reservations/{id}/emails"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.resend))
}

func route(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property")
	}
	return propertyID, id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := route(r)
	if err != nil {
		return err
	}
	st, err := h.svc.List(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) resend(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := route(r)
	if err != nil {
		return err
	}
	e, err := h.svc.Resend(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusAccepted, e)
}
