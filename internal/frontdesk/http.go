package frontdesk

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the front desk API (docs/architecture/06-api.md §13).
type Handler struct{ svc *Service }

// NewHandler returns the front desk HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("POST "+p+"/reservations/{id}/rooms/{lineId}/check-in", httpx.HandlerFunc(h.checkIn))
	mux.Handle("POST "+p+"/walk-ins", httpx.HandlerFunc(h.walkIn))
	mux.Handle("GET "+p+"/arrivals", httpx.HandlerFunc(h.arrivals))
	mux.Handle("GET "+p+"/stays", httpx.HandlerFunc(h.list))
	mux.Handle("GET "+p+"/stays/in-house", httpx.HandlerFunc(h.inHouse))
	mux.Handle("GET "+p+"/stays/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("POST "+p+"/stays/{id}/reverse-check-in", httpx.HandlerFunc(h.reverse))
	mux.Handle("POST "+p+"/stays/{id}/move", httpx.HandlerFunc(h.move))
	mux.Handle("POST "+p+"/stays/{id}/change-departure", httpx.HandlerFunc(h.changeDeparture))
	mux.Handle("POST "+p+"/stays/{id}/rates", httpx.HandlerFunc(h.changeRates))
	mux.Handle("POST "+p+"/stays/{id}/guests", httpx.HandlerFunc(h.addGuest))
	mux.Handle("POST "+p+"/stays/{id}/check-out", httpx.HandlerFunc(h.checkOut))
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return id, nil
}

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func (h *Handler) checkIn(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	resID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	lineID, err := pathID(r, "lineId")
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in CheckInInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.CheckIn(r.Context(), pid, resID, lineID, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) walkIn(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in WalkInInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.WalkIn(r.Context(), pid, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) arrivals(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var date *civil.Date
	if s := r.URL.Query().Get("date"); s != "" {
		d, err := civil.ParseDate(s)
		if err != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("date", "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		date = &d
	}
	f := ArrivalFilter{Status: r.URL.Query().Get("status"), Q: r.URL.Query().Get("q")}
	if s := r.URL.Query().Get("room_type_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("room_type_id", "INVALID_VALUE", "a positive integer"))
		}
		f.RoomTypeID = &id
	}
	rows, err := h.svc.Arrivals(r.Context(), pid, date, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

type listCursor struct {
	Before int64 `json:"b"`
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
	f := StayFilter{Status: r.URL.Query().Get("status")}
	if f.Status != "" && f.Status != "OPEN" && f.Status != "CHECKED_OUT" && f.Status != "CANCELLED" {
		errs = append(errs, fieldErr("status", "INVALID_VALUE", "OPEN, CHECKED_OUT or CANCELLED"))
	}
	if s := r.URL.Query().Get("departure_date"); s != "" {
		d, err := civil.ParseDate(s)
		if err != nil {
			errs = append(errs, fieldErr("departure_date", "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		f.DepartureDate = &d
	}
	if s := r.URL.Query().Get("departure_until"); s != "" {
		d, err := civil.ParseDate(s)
		if err != nil {
			errs = append(errs, fieldErr("departure_until", "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		f.DepartureUntil = &d
	}
	if s := r.URL.Query().Get("room_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("room_id", "INVALID_VALUE", "a positive integer"))
		}
		f.RoomID = &id
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
	items, err := h.svc.ListStays(r.Context(), pid, f, cur.Before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[StaySummary]{Data: items}
	if out.Data == nil {
		out.Data = []StaySummary{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(listCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) inHouse(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur listCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	var f InHouseFilter
	var errs []apperr.FieldError
	f.Q = r.URL.Query().Get("q")
	for _, d := range []struct {
		name string
		to   **civil.Date
	}{{"departure_date", &f.DepartureDate}, {"departure_until", &f.DepartureUntil}} {
		if s := r.URL.Query().Get(d.name); s != "" {
			v, err := civil.ParseDate(s)
			if err != nil {
				errs = append(errs, fieldErr(d.name, "INVALID_FORMAT", "YYYY-MM-DD"))
				continue
			}
			*d.to = &v
		}
	}
	if s := r.URL.Query().Get("room_type_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("room_type_id", "INVALID_VALUE", "a positive integer"))
		}
		f.RoomTypeID = &id
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	items, err := h.svc.ListInHouse(r.Context(), pid, f, cur.Before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[InHouseRow]{Data: items}
	if out.Data == nil {
		out.Data = []InHouseRow{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(listCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
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
	d, err := h.svc.GetStay(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) reverse(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in ReverseInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.ReverseCheckIn(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func stayRoute(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = pathID(r, "id")
	return propertyID, id, err
}

func (h *Handler) move(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := stayRoute(r)
	if err != nil {
		return err
	}
	var in MoveInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Move(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) changeDeparture(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := stayRoute(r)
	if err != nil {
		return err
	}
	var in ChangeDepartureInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.ChangeDeparture(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) addGuest(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := stayRoute(r)
	if err != nil {
		return err
	}
	var in AddGuestInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.AddGuest(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) checkOut(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := stayRoute(r)
	if err != nil {
		return err
	}
	if _, err := idempotencyKey(r); err != nil {
		return err
	}
	var in CheckOutInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.CheckOut(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) changeRates(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in ChangeRatesInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.ChangeRates(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
