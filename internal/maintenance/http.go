package maintenance

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the maintenance API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/maintenance-requests"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET /api/v1/properties/{propertyId}/maintenance-staff", httpx.HandlerFunc(h.staff))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("POST "+p+"/{id}/assign", httpx.HandlerFunc(h.assign))
	mux.Handle("POST "+p+"/{id}/start", httpx.HandlerFunc(h.start))
	mux.Handle("POST "+p+"/{id}/resolve", httpx.HandlerFunc(h.resolve))
	mux.Handle("POST "+p+"/{id}/cancel", httpx.HandlerFunc(h.cancel))
	mux.Handle("POST "+p+"/{id}/reopen", httpx.HandlerFunc(h.reopen))
	mux.Handle("POST "+p+"/{id}/block", httpx.HandlerFunc(h.block))
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

func queryID(r *http.Request, name string, errs *[]apperr.FieldError) *int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 1 {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "a positive integer"))
		return nil
	}
	return &id
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	q := r.URL.Query()
	f := Filter{
		Status: strings.ToUpper(q.Get("status")), Category: strings.ToUpper(q.Get("category")), Priority: strings.ToUpper(q.Get("priority")),
		RoomID: queryID(r, "room_id", &errs), AssignedTo: queryID(r, "assigned_to", &errs),
	}
	if v := q.Get("open"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fieldErr("open", "INVALID_VALUE", "true or false"))
		}
		f.OpenOnly = b
	}
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
	out := httpx.Page[Request]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) staff(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Staff(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Staff]{Data: list})
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
	req, err := h.svc.Create(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, req)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	req, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, req)
}

// respond runs a change on the request of the path and writes the result.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, do func(pid, id int64) (Request, error)) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	req, err := do(pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, req)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var patch Patch
	if err := httpx.DecodeJSON(w, r, &patch); err != nil {
		return err
	}
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Update(r.Context(), pid, id, patch) })
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		UserID *int64 `json:"user_id"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Assign(r.Context(), pid, id, in.UserID) })
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) error {
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Start(r.Context(), pid, id) })
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) error {
	var in CloseInput
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Resolve(r.Context(), pid, id, in) })
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	var in CloseInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Cancel(r.Context(), pid, id, in) })
}

func (h *Handler) reopen(w http.ResponseWriter, r *http.Request) error {
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Reopen(r.Context(), pid, id) })
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request) error {
	var in BlockInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	return h.respond(w, r, func(pid, id int64) (Request, error) { return h.svc.Block(r.Context(), pid, id, in) })
}
