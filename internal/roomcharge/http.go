package roomcharge

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the room charge preview and posting (docs/architecture/06-api.md §15).
type Handler struct{ svc *Service }

// NewHandler returns the room charge HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/night-audit/room-charges"
	mux.Handle("POST "+p+"/preview", httpx.HandlerFunc(h.preview))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.post))
}

type request struct {
	BusinessDate *string `json:"business_date"`
	StayIDs      []int64 `json:"stay_ids"`
}

func (r request) parse() (civil.Date, error) {
	if r.BusinessDate == nil {
		return civil.Date{}, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "business_date", Code: "REQUIRED", Message: "YYYY-MM-DD"})
	}
	d, err := civil.ParseDate(*r.BusinessDate)
	if err != nil {
		return d, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "business_date", Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	for _, id := range r.StayIDs {
		if id < 1 {
			return d, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "stay_ids", Code: "INVALID_VALUE", Message: "positive ids"})
		}
	}
	return d, nil
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req request
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	bd, err := req.parse()
	if err != nil {
		return err
	}
	res, err := h.svc.Preview(r.Context(), pid, bd, req.StayIDs)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var req request
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	bd, err := req.parse()
	if err != nil {
		return err
	}
	res, err := h.svc.PostManual(r.Context(), pid, bd, req.StayIDs)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
