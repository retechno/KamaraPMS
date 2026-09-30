package billingconfig

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the billing configuration API (docs/architecture/06-api.md §9).
type Handler struct{ svc *Service }

// NewHandler returns the billing configuration HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/taxes", httpx.HandlerFunc(h.listTaxes))
	mux.Handle("POST "+p+"/taxes", httpx.HandlerFunc(h.createTax))
	mux.Handle("PATCH "+p+"/taxes/{id}", httpx.HandlerFunc(h.updateTax))
	mux.Handle("GET "+p+"/service-charges", httpx.HandlerFunc(h.listServiceCharges))
	mux.Handle("POST "+p+"/service-charges", httpx.HandlerFunc(h.createServiceCharge))
	mux.Handle("PATCH "+p+"/service-charges/{id}", httpx.HandlerFunc(h.updateServiceCharge))
	mux.Handle("GET "+p+"/charge-codes", httpx.HandlerFunc(h.listChargeCodes))
	mux.Handle("POST "+p+"/charge-codes", httpx.HandlerFunc(h.createChargeCode))
	mux.Handle("GET "+p+"/charge-codes/{id}", httpx.HandlerFunc(h.getChargeCode))
	mux.Handle("PATCH "+p+"/charge-codes/{id}", httpx.HandlerFunc(h.updateChargeCode))
	mux.Handle("PUT "+p+"/charge-codes/{id}/rules", httpx.HandlerFunc(h.replaceRules))
}

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
		return nil, apperr.Invalid("invalid filter", fieldErr(name, "INVALID_VALUE", "true or false"))
	}
	return &b, nil
}

type idCursor struct {
	AfterID int64 `json:"a"`
}

// listPage runs an ascending-id list with keyset paging and writes the page envelope.
func listPage[T any](w http.ResponseWriter, r *http.Request, id func(T) int64, load func(afterID int64, limit int) ([]T, error)) error {
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
	items, err := load(cur.AfterID, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[T]{Data: items}
	if out.Data == nil {
		out.Data = []T{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{AfterID: id(out.Data[page.Limit-1])}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Taxes

func (h *Handler) listTaxes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	return listPage(w, r, func(t Tax) int64 { return t.ID }, func(after int64, limit int) ([]Tax, error) {
		return h.svc.ListTaxes(r.Context(), pid, after, active, limit)
	})
}

type createTaxRequest struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Rate         string `json:"rate"`
	TaxOnService bool   `json:"tax_on_service"`
	IsActive     *bool  `json:"is_active"`
}

func (h *Handler) createTax(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createTaxRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := TaxInput{Code: req.Code, Name: req.Name, Rate: req.Rate, TaxOnService: req.TaxOnService, IsActive: req.IsActive == nil || *req.IsActive}
	t, err := h.svc.CreateTax(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, t)
}

type patchTaxRequest struct {
	Name         *string `json:"name"`
	Rate         *string `json:"rate"`
	TaxOnService *bool   `json:"tax_on_service"`
	IsActive     *bool   `json:"is_active"`
}

func (h *Handler) updateTax(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errTaxNotFound)
	if err != nil {
		return err
	}
	var req patchTaxRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	t, err := h.svc.UpdateTax(r.Context(), pid, id, TaxPatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, t)
}

// ---------------------------------------------------------------------------
// Service charges

func (h *Handler) listServiceCharges(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	return listPage(w, r, func(s ServiceCharge) int64 { return s.ID }, func(after int64, limit int) ([]ServiceCharge, error) {
		return h.svc.ListServiceCharges(r.Context(), pid, after, active, limit)
	})
}

type createServiceChargeRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Rate     string `json:"rate"`
	IsActive *bool  `json:"is_active"`
}

func (h *Handler) createServiceCharge(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createServiceChargeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	s, err := h.svc.CreateServiceCharge(r.Context(), pid, ServiceChargeInput{Code: req.Code, Name: req.Name, Rate: req.Rate, IsActive: req.IsActive == nil || *req.IsActive})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, s)
}

type patchServiceChargeRequest struct {
	Name     *string `json:"name"`
	Rate     *string `json:"rate"`
	IsActive *bool   `json:"is_active"`
}

func (h *Handler) updateServiceCharge(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errServiceChargeNotFound)
	if err != nil {
		return err
	}
	var req patchServiceChargeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	s, err := h.svc.UpdateServiceCharge(r.Context(), pid, id, ServiceChargePatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, s)
}

// ---------------------------------------------------------------------------
// Charge codes

func (h *Handler) listChargeCodes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var f ChargeCodeFilter
	if f.Active, err = queryBool(r, "active"); err != nil {
		return err
	}
	if s := r.URL.Query().Get("charge_type"); s != "" {
		f.ChargeType = &s
	}
	return listPage(w, r, func(c ChargeCode) int64 { return c.ID }, func(after int64, limit int) ([]ChargeCode, error) {
		return h.svc.ListChargeCodes(r.Context(), pid, after, f, limit)
	})
}

func (h *Handler) getChargeCode(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errChargeCodeNotFound)
	if err != nil {
		return err
	}
	c, err := h.svc.GetChargeCode(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

type createChargeCodeRequest struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	ChargeType       string `json:"charge_type"`
	PriceMode        string `json:"price_mode"`
	DefaultUnitPrice string `json:"default_unit_price"`
	IsActive         *bool  `json:"is_active"`
}

func (h *Handler) createChargeCode(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createChargeCodeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := h.svc.CreateChargeCode(r.Context(), pid, ChargeCodeInput{
		Code: req.Code, Name: req.Name, ChargeType: req.ChargeType, PriceMode: req.PriceMode,
		DefaultUnitPrice: req.DefaultUnitPrice, IsActive: req.IsActive == nil || *req.IsActive,
	})
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

type patchChargeCodeRequest struct {
	Name             *string `json:"name"`
	ChargeType       *string `json:"charge_type"`
	PriceMode        *string `json:"price_mode"`
	DefaultUnitPrice *string `json:"default_unit_price"`
	IsActive         *bool   `json:"is_active"`
}

func (h *Handler) updateChargeCode(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errChargeCodeNotFound)
	if err != nil {
		return err
	}
	var req patchChargeCodeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := h.svc.UpdateChargeCode(r.Context(), pid, id, ChargeCodePatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

type rulesRequest struct {
	Taxes []struct {
		TaxID    int64 `json:"tax_id"`
		Sequence int32 `json:"sequence"`
	} `json:"taxes"`
	ServiceCharges []struct {
		ServiceChargeID int64 `json:"service_charge_id"`
		Sequence        int32 `json:"sequence"`
	} `json:"service_charges"`
}

func (h *Handler) replaceRules(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, errChargeCodeNotFound)
	if err != nil {
		return err
	}
	var req rulesRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var in RulesInput
	for _, t := range req.Taxes {
		in.Taxes = append(in.Taxes, TaxRuleInput{TaxID: t.TaxID, Sequence: t.Sequence})
	}
	for _, s := range req.ServiceCharges {
		in.ServiceCharges = append(in.ServiceCharges, ServiceRuleInput{ServiceChargeID: s.ServiceChargeID, Sequence: s.Sequence})
	}
	c, err := h.svc.ReplaceRules(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}
