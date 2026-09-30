package reservations

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the availability and reservations API (docs/architecture/06-api.md §11-12).
type Handler struct{ svc *Service }

// NewHandler returns the reservations HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/availability", httpx.HandlerFunc(h.search))
	mux.Handle("GET "+p+"/availability/rooms", httpx.HandlerFunc(h.freeRooms))
	mux.Handle("POST "+p+"/reservations", httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/reservations", httpx.HandlerFunc(h.list))
	mux.Handle("GET "+p+"/reservations/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/reservations/{id}", httpx.HandlerFunc(h.patch))
	mux.Handle("POST "+p+"/reservations/{id}/confirm", httpx.HandlerFunc(h.confirm))
	mux.Handle("POST "+p+"/reservations/{id}/cancel", httpx.HandlerFunc(h.cancel))
	mux.Handle("POST "+p+"/reservations/{id}/reinstate", httpx.HandlerFunc(h.reinstate))
	mux.Handle("POST "+p+"/reservations/{id}/rooms", httpx.HandlerFunc(h.addRoom))
	mux.Handle("PATCH "+p+"/reservations/{id}/rooms/{lineId}", httpx.HandlerFunc(h.patchRoom))
	mux.Handle("POST "+p+"/reservations/{id}/rooms/{lineId}/cancel", httpx.HandlerFunc(h.cancelRoom))
	mux.Handle("POST "+p+"/reservations/{id}/rooms/{lineId}/no-show", httpx.HandlerFunc(h.noShow))
	mux.Handle("POST "+p+"/reservations/{id}/rooms/{lineId}/assign-room", httpx.HandlerFunc(h.assign))
	mux.Handle("POST "+p+"/reservations/{id}/rooms/{lineId}/unassign-room", httpx.HandlerFunc(h.unassign))
}

func queryDate(r *http.Request, name string, errs *[]apperr.FieldError, required bool) *civil.Date {
	s := r.URL.Query().Get(name)
	if s == "" {
		if required {
			*errs = append(*errs, fieldErr(name, "REQUIRED", "YYYY-MM-DD"))
		}
		return nil
	}
	d, err := civil.ParseDate(s)
	if err != nil {
		*errs = append(*errs, fieldErr(name, "INVALID_FORMAT", "YYYY-MM-DD"))
		return nil
	}
	return &d
}

func queryInt(r *http.Request, name string, def int, errs *[]apperr.FieldError) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 1000 {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "a small non-negative integer"))
		return def
	}
	return n
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return id, nil
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	arrival, departure := queryDate(r, "arrival", &errs, true), queryDate(r, "departure", &errs, true)
	adults, children := queryInt(r, "adults", 1, &errs), queryInt(r, "children", 0, &errs)
	if len(errs) > 0 {
		return apperr.Invalid("the search is invalid", errs...)
	}
	res, err := h.svc.SearchAvailability(r.Context(), pid, *arrival, *departure, adults, children)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) freeRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	arrival, departure := queryDate(r, "arrival", &errs, true), queryDate(r, "departure", &errs, true)
	typeID, perr := strconv.ParseInt(r.URL.Query().Get("room_type_id"), 10, 64)
	if perr != nil || typeID < 1 {
		errs = append(errs, fieldErr("room_type_id", "REQUIRED", "a room type id"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the search is invalid", errs...)
	}
	rooms, err := h.svc.FreeRooms(r.Context(), pid, typeID, *arrival, *departure)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rooms})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Create(r.Context(), pid, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

type listCursor struct {
	BeforeID int64 `json:"b"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := ListFilter{ArrivalFrom: queryDate(r, "arrival_from", &errs, false), ArrivalTo: queryDate(r, "arrival_to", &errs, false),
		Status: r.URL.Query().Get("status"), Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if f.Status != "" && f.Status != StatusDraft && f.Status != StatusConfirmed && f.Status != StatusCancelled {
		errs = append(errs, fieldErr("status", "INVALID_VALUE", "DRAFT, CONFIRMED or CANCELLED"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	var cur listCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.List(r.Context(), pid, f, cur.BeforeID, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Summary]{Data: items}
	if out.Data == nil {
		out.Data = []Summary{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(listCursor{BeforeID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type patchRequest struct {
	Version        int32   `json:"version"`
	GuestID        *int64  `json:"guest_id"`
	Source         *string `json:"source"`
	Market         *string `json:"market"`
	SpecialRequest *string `json:"special_request"`
	Remarks        *string `json:"remarks"`
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req patchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.UpdateHeader(r.Context(), pid, id, HeaderPatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = pathID(r, "id")
	return propertyID, id, err
}

func lineIDs(r *http.Request) (propertyID, id, lineID int64, err error) {
	if propertyID, id, err = ids(r); err != nil {
		return 0, 0, 0, err
	}
	lineID, err = pathID(r, "lineId")
	return propertyID, id, lineID, err
}

type versionRequest struct {
	Version int32 `json:"version"`
}

type reasonRequest struct {
	Version int32  `json:"version"`
	Reason  string `json:"reason"`
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req versionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.Confirm(r.Context(), pid, id, req.Version)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) reinstate(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req versionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.Reinstate(r.Context(), pid, id, req.Version)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.Cancel(r.Context(), pid, id, req.Version, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type addRoomRequest struct {
	Version int32 `json:"version"`
	LineInput
}

func (h *Handler) addRoom(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req addRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.AddLine(r.Context(), pid, id, req.Version, req.LineInput)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

type patchRoomRequest struct {
	Version    int32           `json:"version"`
	Arrival    *civil.Date     `json:"arrival_date"`
	Departure  *civil.Date     `json:"departure_date"`
	RoomTypeID *int64          `json:"room_type_id"`
	RatePlanID *int64          `json:"rate_plan_id"`
	Adults     *int            `json:"adult_count"`
	Children   *int            `json:"child_count"`
	Overrides  []NightOverride `json:"nightly_overrides"`
}

func (h *Handler) patchRoom(w http.ResponseWriter, r *http.Request) error {
	pid, id, lineID, err := lineIDs(r)
	if err != nil {
		return err
	}
	var req patchRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.AmendLine(r.Context(), pid, id, lineID, LinePatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) cancelRoom(w http.ResponseWriter, r *http.Request) error {
	pid, id, lineID, err := lineIDs(r)
	if err != nil {
		return err
	}
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.CancelLine(r.Context(), pid, id, lineID, req.Version, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) noShow(w http.ResponseWriter, r *http.Request) error {
	pid, id, lineID, err := lineIDs(r)
	if err != nil {
		return err
	}
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.NoShow(r.Context(), pid, id, lineID, req.Version, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

type assignRequest struct {
	Version int32 `json:"version"`
	RoomID  int64 `json:"room_id"`
	Upgrade bool  `json:"upgrade"`
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) error {
	pid, id, lineID, err := lineIDs(r)
	if err != nil {
		return err
	}
	var req assignRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.AssignRoom(r.Context(), pid, id, lineID, req.Version, req.RoomID, req.Upgrade)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) unassign(w http.ResponseWriter, r *http.Request) error {
	pid, id, lineID, err := lineIDs(r)
	if err != nil {
		return err
	}
	var req versionRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := h.svc.UnassignRoom(r.Context(), pid, id, lineID, req.Version)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
