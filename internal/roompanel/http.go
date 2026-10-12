package roompanel

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the room panel.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the route.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/rooms/{roomId}/summary", httpx.HandlerFunc(h.get))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	roomID, err := strconv.ParseInt(r.PathValue("roomId"), 10, 64)
	if err != nil || roomID < 1 {
		return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
	}
	out, err := h.svc.Get(r.Context(), pid, roomID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
