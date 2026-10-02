package housekeeping

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the housekeeping API (docs/architecture/06-api.md §7).
type Handler struct{ svc *Service }

// NewHandler returns the housekeeping HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/housekeeping", httpx.HandlerFunc(h.board))
	mux.Handle("POST /api/v1/properties/{propertyId}/rooms/{roomId}/housekeeping", httpx.HandlerFunc(h.setStatus))
	mux.Handle("GET /api/v1/properties/{propertyId}/rooms/{roomId}/housekeeping/logs", httpx.HandlerFunc(h.logs))
	h.RegisterTasks(mux)
}

func roomID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("roomId"), 10, 64)
	if err != nil || id < 1 {
		return 0, roomNotFound()
	}
	return id, nil
}

func (h *Handler) board(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var f BoardFilter
	if s := r.URL.Query().Get("status"); s != "" {
		st := Status(s)
		f.Status = &st
	}
	if s := r.URL.Query().Get("floor"); s != "" {
		f.Floor = &s
	}
	if s := r.URL.Query().Get("occupancy"); s != "" {
		o := Occupancy(s)
		f.Occupancy = &o
	}
	f.Flagged = r.URL.Query().Get("flagged") == "true"
	rooms, err := h.svc.Board(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[BoardRoom]{Data: rooms})
}

type setStatusRequest struct {
	Status Status `json:"status"`
	Notes  string `json:"notes"`
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := roomID(r)
	if err != nil {
		return err
	}
	var req setStatusRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	st, err := h.svc.SetStatus(r.Context(), pid, id, req.Status, req.Notes)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

type logCursor struct {
	BeforeID int64 `json:"b"`
}

func (h *Handler) logs(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := roomID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var before *int64
	if page.Cursor != "" {
		var cur logCursor
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
		before = &cur.BeforeID
	}
	logs, err := h.svc.Logs(r.Context(), pid, id, before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Log]{Data: logs}
	if len(logs) > page.Limit {
		out.Data = logs[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(logCursor{BeforeID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
