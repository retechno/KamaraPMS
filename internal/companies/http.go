package companies

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the companies of a property.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/companies"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
}

type idCursor struct {
	AfterID int64 `json:"a"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var active *bool
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return apperr.Invalid("the query is invalid", apperr.FieldError{Field: "active", Code: "INVALID_VALUE", Message: "true or false"})
		}
		active = &b
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
	items, err := h.svc.List(r.Context(), pid, cur.AfterID, active, r.URL.Query().Get("q"), page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Company]{Data: items}
	if out.Data == nil {
		out.Data = []Company{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	ContactName      string `json:"contact_name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	Address          string `json:"address"`
	City             string `json:"city"`
	TaxID            string `json:"tax_id"`
	CreditLimit      string `json:"credit_limit"`
	PaymentTermsDays *int   `json:"payment_terms_days"`
	Notes            string `json:"notes"`
	IsActive         *bool  `json:"is_active"`
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
	terms := 30
	if req.PaymentTermsDays != nil {
		terms = *req.PaymentTermsDays
	}
	c, err := h.svc.Create(r.Context(), pid, Input{
		Code: req.Code, Name: req.Name, ContactName: req.ContactName, Email: req.Email, Phone: req.Phone, Address: req.Address, City: req.City, TaxID: req.TaxID,
		CreditLimit: req.CreditLimit, PaymentTermsDays: terms, Notes: req.Notes, IsActive: req.IsActive == nil || *req.IsActive,
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

func route(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errNotFound()
	}
	return propertyID, id, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := route(r)
	if err != nil {
		return err
	}
	c, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

type patchRequest struct {
	Name             *string `json:"name"`
	ContactName      *string `json:"contact_name"`
	Email            *string `json:"email"`
	Phone            *string `json:"phone"`
	Address          *string `json:"address"`
	City             *string `json:"city"`
	TaxID            *string `json:"tax_id"`
	CreditLimit      *string `json:"credit_limit"`
	PaymentTermsDays *int    `json:"payment_terms_days"`
	Notes            *string `json:"notes"`
	IsActive         *bool   `json:"is_active"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := route(r)
	if err != nil {
		return err
	}
	var req patchRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := h.svc.Update(r.Context(), pid, id, Patch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}
