package rates

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerYield mounts the yield management routes (docs/architecture/06-api.md, yield management).
func (h *Handler) registerYield(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/yield-rules", httpx.HandlerFunc(h.listYield))
	mux.Handle("POST "+p+"/yield-rules", httpx.HandlerFunc(h.createYield))
	mux.Handle("GET "+p+"/yield-rules/{id}", httpx.HandlerFunc(h.getYield))
	mux.Handle("PUT "+p+"/yield-rules/{id}", httpx.HandlerFunc(h.updateYield))
	mux.Handle("DELETE "+p+"/yield-rules/{id}", httpx.HandlerFunc(h.deleteYield))
	mux.Handle("GET "+p+"/rate-quotes", httpx.HandlerFunc(h.quote))
}

type yieldRequest struct {
	Code            string      `json:"code"`
	Name            string      `json:"name"`
	RatePlanID      *int64      `json:"rate_plan_id"`
	RoomTypeID      *int64      `json:"room_type_id"`
	StayFrom        *civil.Date `json:"stay_from"`
	StayTo          *civil.Date `json:"stay_to"`
	Weekdays        []string    `json:"weekdays"`
	OccupancyFrom   *string     `json:"occupancy_from"`
	OccupancyTo     *string     `json:"occupancy_to"`
	LeadMin         *int        `json:"lead_days_min"`
	LeadMax         *int        `json:"lead_days_max"`
	StayMin         *int        `json:"stay_nights_min"`
	StayMax         *int        `json:"stay_nights_max"`
	AdjustmentType  string      `json:"adjustment_type"`
	AdjustmentValue string      `json:"adjustment_value"`
	FloorAmount     *string     `json:"floor_amount"`
	CapAmount       *string     `json:"cap_amount"`
	Priority        *int        `json:"priority"`
	IsActive        *bool       `json:"is_active"`
}

func ruleID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errYieldRuleNotFound()
	}
	return id, nil
}

func (h *Handler) listYield(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
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
	rules, err := h.svc.ListYieldRules(r.Context(), pid, active)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[YieldRule]{Data: rules})
}

func (h *Handler) getYield(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	rule, err := h.svc.GetYieldRule(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rule)
}

func (req yieldRequest) input() YieldRuleInput {
	return YieldRuleInput(req)
}

func (h *Handler) createYield(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req yieldRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	rule, err := h.svc.CreateYieldRule(r.Context(), pid, req.input())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, rule)
}

func (h *Handler) updateYield(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	var req yieldRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	rule, err := h.svc.UpdateYieldRule(r.Context(), pid, id, req.input())
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rule)
}

func (h *Handler) deleteYield(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := ruleID(r)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteYieldRule(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) quote(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	arrival, departure := queryDate(r, "arrival_date", &errs), queryDate(r, "departure_date", &errs)
	if arrival.IsZero() && len(errs) == 0 {
		errs = append(errs, fieldErr("arrival_date", "REQUIRED", "YYYY-MM-DD"))
	}
	if departure.IsZero() && len(errs) == 0 {
		errs = append(errs, fieldErr("departure_date", "REQUIRED", "YYYY-MM-DD"))
	}
	id := func(name string) int64 {
		n, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
		if err != nil || n < 1 {
			errs = append(errs, fieldErr(name, "REQUIRED", "an id"))
		}
		return n
	}
	planID, typeID := id("rate_plan_id"), id("room_type_id")
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	q, err := h.svc.Quote(r.Context(), pid, planID, typeID, arrival, departure)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, q)
}
