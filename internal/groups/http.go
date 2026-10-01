package groups

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the booking groups of a property.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/groups"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("GET "+p+"/{id}/reservations", httpx.HandlerFunc(h.members))
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
		return 0, 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return propertyID, id, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	var active *bool
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fieldErr("active", "INVALID_VALUE", "true or false"))
		}
		active = &b
	}
	var company *int64
	if v := r.URL.Query().Get("company_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("company_id", "INVALID_VALUE", "a positive integer"))
		}
		company = &id
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.List(r.Context(), pid, cur.Before, active, company, strings.TrimSpace(r.URL.Query().Get("q")), page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Group]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	CompanyID     *int64     `json:"company_id"`
	ContactName   string     `json:"contact_name"`
	ContactEmail  string     `json:"contact_email"`
	ContactPhone  string     `json:"contact_phone"`
	ArrivalDate   civil.Date `json:"arrival_date"`
	DepartureDate civil.Date `json:"departure_date"`
	Notes         string     `json:"notes"`
	IsActive      *bool      `json:"is_active"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	g, err := h.svc.Create(r.Context(), pid, Input{
		Code: req.Code, Name: req.Name, CompanyID: req.CompanyID, ContactName: req.ContactName, ContactEmail: req.ContactEmail, ContactPhone: req.ContactPhone,
		ArrivalDate: req.ArrivalDate, DepartureDate: req.DepartureDate, Notes: req.Notes, IsActive: req.IsActive == nil || *req.IsActive,
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, g)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	g, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, g)
}

type patchRequest struct {
	Name          *string     `json:"name"`
	CompanyID     *int64      `json:"company_id"`
	ContactName   *string     `json:"contact_name"`
	ContactEmail  *string     `json:"contact_email"`
	ContactPhone  *string     `json:"contact_phone"`
	ArrivalDate   *civil.Date `json:"arrival_date"`
	DepartureDate *civil.Date `json:"departure_date"`
	Notes         *string     `json:"notes"`
	IsActive      *bool       `json:"is_active"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var req patchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	g, err := h.svc.Update(r.Context(), pid, id, Patch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, g)
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Members(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Member]{Data: list})
}
