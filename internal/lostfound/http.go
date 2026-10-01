package lostfound

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the lost and found API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/lost-found"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("GET "+p+"/{id}/possible-owners", httpx.HandlerFunc(h.owners))
	mux.Handle("POST "+p+"/{id}/return", httpx.HandlerFunc(h.handBack))
	mux.Handle("POST "+p+"/{id}/dispose", httpx.HandlerFunc(h.dispose))
}

type idCursor struct {
	Before int64 `json:"b"`
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errNotFound()
	}
	return propertyID, id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var errs []apperr.FieldError
	f := Filter{Status: strings.ToUpper(q.Get("status")), Category: strings.ToUpper(q.Get("category")), Query: q.Get("q")}
	if v := q.Get("room_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("room_id", "INVALID_VALUE", "a positive integer"))
		}
		f.RoomID = &id
	}
	date := func(name string) *civil.Date {
		v := q.Get(name)
		if v == "" {
			return nil
		}
		d, err := civil.ParseDate(v)
		if err != nil {
			errs = append(errs, fieldErr(name, "INVALID_DATE", "a date as YYYY-MM-DD"))
			return nil
		}
		return &d
	}
	f.FoundFrom, f.FoundTo = date("found_from"), date("found_to")
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var before *int64
	if page.Cursor != "" {
		var cur idCursor
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
		before = &cur.Before
	}
	items, err := h.svc.List(r.Context(), pid, f, before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Item]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	it, err := h.svc.Create(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, it)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	it, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) owners(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	list, err := h.svc.PossibleOwners(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Owner]{Data: list})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var patch Patch
	if err := httpx.DecodeJSON(w, r, &patch); err != nil {
		return err
	}
	it, err := h.svc.Update(r.Context(), pid, id, patch)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) handBack(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in ReturnInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	it, err := h.svc.Return(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) dispose(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in DisposeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	it, err := h.svc.Dispose(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, it)
}
