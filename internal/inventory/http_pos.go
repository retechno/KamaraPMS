package inventory

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the POS deduction (35-inventory.md 5.6, 10): the recipe of a product, the queue of the rows and the report of the sales that left no deduction. The recipe routes sit under the POS (the module
// gate of the route is POS) and ask the entitlement of the stock module themselves.
func (h *Handler) registerPOS(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/pos/products/{id}/recipe", httpx.HandlerFunc(h.getRecipe))
	mux.Handle("PUT "+p+"/pos/products/{id}/recipe", httpx.HandlerFunc(h.putRecipe))
	mux.Handle("GET "+p+"/inventory/consumption-queue", httpx.HandlerFunc(h.listQueue))
	mux.Handle("POST "+p+"/inventory/consumption-queue/{id}/retry", httpx.HandlerFunc(h.retryQueueRow))
	mux.Handle("POST "+p+"/inventory/consumption-queue/{id}/cancel", httpx.HandlerFunc(h.cancelQueueRow))
	mux.Handle("GET "+p+"/inventory/reports/pos-without-deduction", httpx.HandlerFunc(h.salesWithoutDeduction))
}

func (h *Handler) getRecipe(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	outlet, err := queryInt(r, "outlet_id")
	if err != nil {
		return err
	}
	v, err := h.svc.GetRecipe(r.Context(), pid, id, outlet)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) putRecipe(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in RecipeInput) (Recipe, error) { return h.svc.SaveRecipe(r.Context(), pid, id, in) })
}

func (h *Handler) listQueue(w http.ResponseWriter, r *http.Request) error {
	var statuses []string
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		for _, s := range strings.Split(v, ",") {
			statuses = append(statuses, strings.ToUpper(strings.TrimSpace(s)))
		}
	}
	return jsonOf(w, r, http.StatusOK, func(pid int64) (QueueList, error) { return h.svc.ListQueue(r.Context(), pid, statuses, queryLimit(r)) })
}

func (h *Handler) retryQueueRow(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (QueueRow, error) { return h.svc.RetryQueueRow(r.Context(), pid, id) })
}

func (h *Handler) cancelQueueRow(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (QueueRow, error) { return h.svc.CancelQueueRow(r.Context(), pid, id) })
}

func (h *Handler) salesWithoutDeduction(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, err := queryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "to")
	if err != nil {
		return err
	}
	if from == nil || to == nil {
		return apperr.Invalid("the report is invalid", fieldErr("from", "REQUIRED", "from and to are dates (YYYY-MM-DD)"))
	}
	outlet, err := queryInt(r, "outlet_id")
	if err != nil {
		return err
	}
	list, err := h.svc.SalesWithoutDeductionReport(r.Context(), pid, *from, *to, outlet, queryLimit(r))
	return page(w, list, err)
}
