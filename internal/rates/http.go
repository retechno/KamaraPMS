package rates

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the rates API (docs/architecture/06-api.md §10).
type Handler struct{ svc *Service }

// NewHandler returns the rates HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/rate-plans", httpx.HandlerFunc(h.listPlans))
	mux.Handle("POST "+p+"/rate-plans", httpx.HandlerFunc(h.createPlan))
	mux.Handle("PATCH "+p+"/rate-plans/{id}", httpx.HandlerFunc(h.updatePlan))
	mux.Handle("GET "+p+"/rates", httpx.HandlerFunc(h.getRates))
	mux.Handle("PUT "+p+"/rates", httpx.HandlerFunc(h.fillRates))
	h.registerYield(mux)
}

type idCursor struct {
	AfterID int64 `json:"a"`
}

func (h *Handler) listPlans(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var active *bool
	if s := r.URL.Query().Get("active"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return apperr.Invalid("invalid filter", fieldErr("active", "INVALID_VALUE", "true or false"))
		}
		active = &b
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.ListRatePlans(r.Context(), pid, cur.AfterID, active, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[RatePlan]{Data: items}
	if out.Data == nil {
		out.Data = []RatePlan{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{AfterID: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type createPlanRequest struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	MealPlan           string `json:"meal_plan"`
	CancellationPolicy string `json:"cancellation_policy"`
	IsRefundable       *bool  `json:"is_refundable"`
	RoomChargeCodeID   int64  `json:"room_charge_code_id"`
	OccupancyKind      string `json:"occupancy_kind"`
	IsActive           *bool  `json:"is_active"`
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req createPlanRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	in := RatePlanInput{
		Code: req.Code, Name: req.Name, Description: req.Description, MealPlan: req.MealPlan, CancellationPolicy: req.CancellationPolicy,
		IsRefundable: req.IsRefundable == nil || *req.IsRefundable, RoomChargeCodeID: req.RoomChargeCodeID, OccupancyKind: req.OccupancyKind, IsActive: req.IsActive == nil || *req.IsActive,
	}
	plan, err := h.svc.CreateRatePlan(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, plan)
}

type patchPlanRequest struct {
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	MealPlan           *string `json:"meal_plan"`
	CancellationPolicy *string `json:"cancellation_policy"`
	IsRefundable       *bool   `json:"is_refundable"`
	RoomChargeCodeID   *int64  `json:"room_charge_code_id"`
	IsActive           *bool   `json:"is_active"`
}

func (h *Handler) updatePlan(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return errRatePlanNotFound()
	}
	var req patchPlanRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	plan, err := h.svc.UpdateRatePlan(r.Context(), pid, id, RatePlanPatch(req))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, plan)
}

func queryDate(r *http.Request, name string, errs *[]apperr.FieldError) civil.Date {
	s := r.URL.Query().Get(name)
	if s == "" {
		return civil.Date{}
	}
	d, err := civil.ParseDate(s)
	if err != nil {
		*errs = append(*errs, fieldErr(name, "INVALID_FORMAT", "YYYY-MM-DD"))
	}
	return d
}

func (h *Handler) getRates(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	from, to := queryDate(r, "from", &errs), queryDate(r, "to", &errs)
	planID, err := strconv.ParseInt(r.URL.Query().Get("rate_plan_id"), 10, 64)
	if err != nil || planID < 1 {
		errs = append(errs, fieldErr("rate_plan_id", "REQUIRED", "a rate plan id"))
	}
	var typeID *int64
	if s := r.URL.Query().Get("room_type_id"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("room_type_id", "INVALID_VALUE", "a positive integer"))
		}
		typeID = &id
	}
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	grid, err := h.svc.Rates(r.Context(), pid, planID, typeID, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, grid)
}

type fillRequest struct {
	RatePlanID  int64    `json:"rate_plan_id"`
	RoomTypeIDs []int64  `json:"room_type_ids"`
	From        *string  `json:"from"`
	To          *string  `json:"to"`
	Weekdays    []string `json:"weekdays"`
	Amount      string   `json:"amount"`
}

func (h *Handler) fillRates(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req fillRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var errs []apperr.FieldError
	date := func(field string, s *string) civil.Date {
		if s == nil {
			errs = append(errs, fieldErr(field, "REQUIRED", "YYYY-MM-DD"))
			return civil.Date{}
		}
		d, err := civil.ParseDate(*s)
		if err != nil {
			errs = append(errs, fieldErr(field, "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		return d
	}
	in := FillInput{RatePlanID: req.RatePlanID, RoomTypeIDs: req.RoomTypeIDs, From: date("from", req.From), To: date("to", req.To), Weekdays: req.Weekdays, Amount: req.Amount}
	if len(errs) > 0 {
		return apperr.Invalid("the rates are invalid", errs...)
	}
	res, err := h.svc.FillRates(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
