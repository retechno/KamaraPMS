package rooms

import (
	"net/http"
	"strconv"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the rooms API (docs/architecture/06-api.md §5-6).
type Handler struct{ svc *Service }

// NewHandler returns the rooms HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/room-types", httpx.HandlerFunc(h.listRoomTypes))
	mux.Handle("POST "+p+"/room-types", httpx.HandlerFunc(h.createRoomType))
	mux.Handle("GET "+p+"/room-types/{id}", httpx.HandlerFunc(h.getRoomType))
	mux.Handle("PATCH "+p+"/room-types/{id}", httpx.HandlerFunc(h.updateRoomType))

	mux.Handle("GET "+p+"/bed-types", httpx.HandlerFunc(h.listBedTypes))
	mux.Handle("POST "+p+"/bed-types", httpx.HandlerFunc(h.createBedType))
	mux.Handle("PATCH "+p+"/bed-types/{id}", httpx.HandlerFunc(h.updateBedType))

	mux.Handle("GET "+p+"/rooms", httpx.HandlerFunc(h.listRooms))
	mux.Handle("POST "+p+"/rooms", httpx.HandlerFunc(h.createRoom))
	mux.Handle("GET "+p+"/rooms/{id}", httpx.HandlerFunc(h.getRoom))
	mux.Handle("PATCH "+p+"/rooms/{id}", httpx.HandlerFunc(h.updateRoom))

	mux.Handle("GET "+p+"/room-blocks", httpx.HandlerFunc(h.listBlocks))
	mux.Handle("POST "+p+"/room-blocks", httpx.HandlerFunc(h.createBlock))
	mux.Handle("GET "+p+"/room-blocks/{id}", httpx.HandlerFunc(h.getBlock))
	mux.Handle("PATCH "+p+"/room-blocks/{id}", httpx.HandlerFunc(h.updateBlock))
	mux.Handle("POST "+p+"/room-blocks/{id}/cancel", httpx.HandlerFunc(h.cancelBlock))
}

// pathID parses the {id} path segment; a malformed id is reported as nf, like a missing one.
func pathID(r *http.Request, nf func() *apperr.Error) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, nf()
	}
	return id, nil
}

func queryBool(r *http.Request, name string) (*bool, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return nil, apperr.Invalid("invalid filter", apperr.FieldError{Field: name, Code: "INVALID_VALUE", Message: "true or false"})
	}
	return &b, nil
}

func queryInt(r *http.Request, name string) (*int64, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return nil, apperr.Invalid("invalid filter", apperr.FieldError{Field: name, Code: "INVALID_VALUE", Message: "a positive integer"})
	}
	return &n, nil
}

func queryDate(r *http.Request, name string) (*civil.Date, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil, nil
	}
	d, err := civil.ParseDate(s)
	if err != nil {
		return nil, apperr.Invalid("invalid filter", apperr.FieldError{Field: name, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return &d, nil
}

// dateField parses an optional request date, collecting a field error.
func dateField(errs *[]apperr.FieldError, field string, s *string, required bool) (civil.Date, *civil.Date) {
	if s == nil {
		if required {
			*errs = append(*errs, apperr.FieldError{Field: field, Code: "REQUIRED", Message: "YYYY-MM-DD"})
		}
		return civil.Date{}, nil
	}
	d, err := civil.ParseDate(*s)
	if err != nil {
		*errs = append(*errs, apperr.FieldError{Field: field, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
		return civil.Date{}, nil
	}
	return d, &d
}

func requiredInt(errs *[]apperr.FieldError, field string, v *int32) int32 {
	if v == nil {
		*errs = append(*errs, apperr.FieldError{Field: field, Code: "REQUIRED"})
		return 0
	}
	return *v
}

// idCursor is the keyset position of ascending-id lists.
type idCursor struct {
	AfterID int64 `json:"a"`
}

// blockCursor is the keyset position of the newest-first block list.
type blockCursor struct {
	BeforeID int64 `json:"b"`
}

func decodeCursor[C any](page httpx.PageRequest) (c C, set bool, err error) {
	if page.Cursor == "" {
		return c, false, nil
	}
	return c, true, httpx.DecodeCursor(page.Cursor, &c)
}

// ---------------------------------------------------------------------------
// Room types

func (h *Handler) listRoomTypes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	cur, _, err := decodeCursor[idCursor](page)
	if err != nil {
		return err
	}
	items, err := h.svc.ListRoomTypes(r.Context(), pid, cur.AfterID, active, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[RoomType]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getRoomType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errRoomTypeNotFound)
	if err != nil {
		return err
	}
	t, err := h.svc.GetRoomType(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

type createRoomTypeRequest struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	MaxAdult      *int32 `json:"max_adult"`
	MaxChild      *int32 `json:"max_child"`
	MaxOccupancy  *int32 `json:"max_occupancy"`
	BaseOccupancy *int32 `json:"base_occupancy"`
	SortOrder     int32  `json:"sort_order"`
	IsActive      *bool  `json:"is_active"`
}

func (h *Handler) createRoomType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createRoomTypeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var missing []apperr.FieldError
	in := RoomTypeInput{
		Code: req.Code, Name: req.Name, Description: req.Description, SortOrder: req.SortOrder, IsActive: true,
		MaxAdult: requiredInt(&missing, "max_adult", req.MaxAdult), MaxChild: requiredInt(&missing, "max_child", req.MaxChild),
		MaxOccupancy: requiredInt(&missing, "max_occupancy", req.MaxOccupancy), BaseOccupancy: requiredInt(&missing, "base_occupancy", req.BaseOccupancy),
	}
	if req.IsActive != nil {
		in.IsActive = *req.IsActive
	}
	if len(missing) > 0 {
		return apperr.Invalid("the room type is invalid", missing...)
	}
	t, err := h.svc.CreateRoomType(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, t)
}

type patchRoomTypeRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	MaxAdult      *int32  `json:"max_adult"`
	MaxChild      *int32  `json:"max_child"`
	MaxOccupancy  *int32  `json:"max_occupancy"`
	BaseOccupancy *int32  `json:"base_occupancy"`
	SortOrder     *int32  `json:"sort_order"`
	IsActive      *bool   `json:"is_active"`
}

func (h *Handler) updateRoomType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errRoomTypeNotFound)
	if err != nil {
		return err
	}
	var req patchRoomTypeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	t, err := h.svc.UpdateRoomType(r.Context(), pid, id, RoomTypePatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

// ---------------------------------------------------------------------------
// Bed types

func (h *Handler) listBedTypes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	items, err := h.svc.ListBedTypes(r.Context(), pid, active)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[BedType]{Data: items})
}

type createBedTypeRequest struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	SortOrder int32  `json:"sort_order"`
	IsActive  *bool  `json:"is_active"`
}

func (h *Handler) createBedType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createBedTypeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := BedTypeInput{Code: req.Code, Name: req.Name, SortOrder: req.SortOrder, IsActive: true}
	if req.IsActive != nil {
		in.IsActive = *req.IsActive
	}
	b, err := h.svc.CreateBedType(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

type patchBedTypeRequest struct {
	Name      *string `json:"name"`
	SortOrder *int32  `json:"sort_order"`
	IsActive  *bool   `json:"is_active"`
}

func (h *Handler) updateBedType(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errBedTypeNotFound)
	if err != nil {
		return err
	}
	var req patchBedTypeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	b, err := h.svc.UpdateBedType(r.Context(), pid, id, BedTypePatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

// ---------------------------------------------------------------------------
// Rooms

func (h *Handler) listRooms(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var f RoomFilter
	if f.Active, err = queryBool(r, "active"); err != nil {
		return err
	}
	if f.RoomTypeID, err = queryInt(r, "room_type_id"); err != nil {
		return err
	}
	cur, _, err := decodeCursor[idCursor](page)
	if err != nil {
		return err
	}
	items, err := h.svc.ListRooms(r.Context(), pid, cur.AfterID, f, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Room]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errRoomNotFound)
	if err != nil {
		return err
	}
	room, err := h.svc.GetRoom(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, room)
}

type createRoomRequest struct {
	RoomNumber                string `json:"room_number"`
	RoomTypeID                int64  `json:"room_type_id"`
	Floor                     string `json:"floor"`
	Building                  string `json:"building"`
	BedTypeID                 *int64 `json:"bed_type_id"`
	IsActive                  *bool  `json:"is_active"`
	InitialHousekeepingStatus string `json:"initial_housekeeping_status"`
}

func (h *Handler) createRoom(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := CreateRoomInput{
		RoomInput:           RoomInput{RoomTypeID: req.RoomTypeID, RoomNumber: req.RoomNumber, Floor: req.Floor, Building: req.Building, BedTypeID: req.BedTypeID, IsActive: true},
		InitialHousekeeping: housekeeping.Status(req.InitialHousekeepingStatus),
	}
	if req.IsActive != nil {
		in.IsActive = *req.IsActive
	}
	room, err := h.svc.CreateRoom(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, room)
}

type patchRoomRequest struct {
	RoomTypeID *int64  `json:"room_type_id"`
	RoomNumber *string `json:"room_number"`
	Floor      *string `json:"floor"`
	Building   *string `json:"building"`
	BedTypeID  *int64  `json:"bed_type_id"`
	IsActive   *bool   `json:"is_active"`
}

func (h *Handler) updateRoom(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errRoomNotFound)
	if err != nil {
		return err
	}
	var req patchRoomRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	room, err := h.svc.UpdateRoom(r.Context(), pid, id, RoomPatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, room)
}

// ---------------------------------------------------------------------------
// Room blocks

func (h *Handler) listBlocks(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var f BlockFilter
	if f.RoomID, err = queryInt(r, "room_id"); err != nil {
		return err
	}
	if f.From, err = queryDate(r, "from"); err != nil {
		return err
	}
	if f.To, err = queryDate(r, "to"); err != nil {
		return err
	}
	if s := r.URL.Query().Get("status"); s != "" {
		f.Status = &s
	}
	cur, set, err := decodeCursor[blockCursor](page)
	if err != nil {
		return err
	}
	var before *int64
	if set {
		before = &cur.BeforeID
	}
	items, err := h.svc.ListBlocks(r.Context(), pid, before, f, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[RoomBlock]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(blockCursor{BeforeID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getBlock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errBlockNotFound)
	if err != nil {
		return err
	}
	b, err := h.svc.GetBlock(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

type createBlockRequest struct {
	RoomID    int64   `json:"room_id"`
	BlockType string  `json:"block_type"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Reason    string  `json:"reason"`
}

func (h *Handler) createBlock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createBlockRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var errs []apperr.FieldError
	in := CreateBlockInput{RoomID: req.RoomID, BlockType: req.BlockType, Reason: req.Reason}
	in.StartDate, _ = dateField(&errs, "start_date", req.StartDate, true)
	in.EndDate, _ = dateField(&errs, "end_date", req.EndDate, true)
	if req.RoomID < 1 {
		errs = append(errs, apperr.FieldError{Field: "room_id", Code: "REQUIRED"})
	}
	if len(errs) > 0 {
		return apperr.Invalid("the room block is invalid", errs...)
	}
	b, err := h.svc.CreateBlock(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

type patchBlockRequest struct {
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Reason    *string `json:"reason"`
}

func (h *Handler) updateBlock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errBlockNotFound)
	if err != nil {
		return err
	}
	var req patchBlockRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var errs []apperr.FieldError
	patch := BlockPatch{Reason: req.Reason}
	_, patch.StartDate = dateField(&errs, "start_date", req.StartDate, false)
	_, patch.EndDate = dateField(&errs, "end_date", req.EndDate, false)
	if len(errs) > 0 {
		return apperr.Invalid("the room block is invalid", errs...)
	}
	b, err := h.svc.UpdateBlock(r.Context(), pid, id, patch)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

type cancelBlockRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) cancelBlock(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errBlockNotFound)
	if err != nil {
		return err
	}
	var req cancelBlockRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	b, err := h.svc.CancelBlock(r.Context(), pid, id, req.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}
