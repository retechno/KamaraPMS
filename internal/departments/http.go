package departments

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the department API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/departments"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("DELETE "+p+"/{id}", httpx.HandlerFunc(h.delete))
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
	var f Filter
	if v := strings.TrimSpace(r.URL.Query().Get("active")); v != "" {
		b, perr := strconv.ParseBool(v)
		if perr != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("active", "INVALID_VALUE", "true or false"))
		}
		f.Active = &b
	}
	list, err := h.svc.List(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Department]{Data: list})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.Create(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
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
	d, err := h.svc.Update(r.Context(), pid, id, patch)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
