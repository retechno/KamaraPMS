package reservations

import (
	"io"
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the bulk rooms and the rooming list of a group (docs/architecture/30-group-folio.md, part A).
func (h *Handler) registerGroupRooms(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/groups/{id}"
	mux.Handle("POST "+p+"/room-batches/preview", httpx.HandlerFunc(h.previewGroupRooms))
	mux.Handle("POST "+p+"/room-batches", httpx.HandlerFunc(h.createGroupRooms))
	mux.Handle("GET "+p+"/rooming-list", httpx.HandlerFunc(h.roomingList))
	mux.Handle("PATCH "+p+"/rooming-list", httpx.HandlerFunc(h.updateRoomingList))
	mux.Handle("POST "+p+"/rooming-list/import", httpx.HandlerFunc(h.importRoomingList))
	mux.Handle("POST "+p+"/cancellations", httpx.HandlerFunc(h.groupCancel))
	mux.Handle("POST "+p+"/reinstatements", httpx.HandlerFunc(h.groupReinstate))
	mux.Handle("GET "+p+"/holdings", httpx.HandlerFunc(h.groupHoldings))
}

// groupHoldings lists what the cancelled reservations of a group still hold: the follow-up of a cancellation.
func (h *Handler) groupHoldings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := h.svc.GroupHoldings(r.Context(), pid, gid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// groupCancel cancels the rooms of a group (docs/architecture/30-group-folio.md part F): 200 whatever the rows did, each row says what became of its reservation.
func (h *Handler) groupCancel(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in GroupCancelInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.GroupCancel(r.Context(), pid, gid, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// groupReinstate brings the cancelled reservations of a group back: 200 whatever the rows did.
func (h *Handler) groupReinstate(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in GroupReinstateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.GroupReinstate(r.Context(), pid, gid, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) previewGroupRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in BatchInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.PreviewGroupRooms(r.Context(), pid, gid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) createGroupRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in BatchInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.CreateGroupRooms(r.Context(), pid, gid, key, in)
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	return httpx.WriteJSON(w, status, out)
}

func (h *Handler) roomingList(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := h.svc.RoomingList(r.Context(), pid, gid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) updateRoomingList(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Rows []RoomingEdit `json:"rows"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.UpdateRoomingList(r.Context(), pid, gid, in.Rows)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// importRoomingList takes the CSV as the body of the request (text/csv). dry_run=true checks it and says what would change, and writes nothing.
func (h *Handler) importRoomingList(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	gid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return apperr.BadRequest("INVALID_BODY", "the file is too large or could not be read")
	}
	out, err := h.svc.ImportRoomingList(r.Context(), pid, gid, body, r.URL.Query().Get("dry_run") == "true")
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
