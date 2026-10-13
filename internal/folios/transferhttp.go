package folios

import (
	"net/http"
	"strconv"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the transfers between folios (docs/architecture/30-group-folio.md, part B).
func (h *Handler) registerTransfers(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("POST "+p+"/folios/{id}/transfer-items", httpx.HandlerFunc(h.transferItems))
	mux.Handle("POST "+p+"/folios/{id}/transfer-balance", httpx.HandlerFunc(h.transferBalance))
	mux.Handle("POST "+p+"/folios/{id}/transfers/return", httpx.HandlerFunc(h.returnTransfers))
	mux.Handle("GET "+p+"/folios/{id}/transfers", httpx.HandlerFunc(h.listTransfers))
	mux.Handle("GET "+p+"/folios/{id}/transfer-targets", httpx.HandlerFunc(h.transferTargets))
}

func (h *Handler) transferItems(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in TransferItemsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.TransferItems(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) transferBalance(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in TransferBalanceInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.TransferBalance(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	return httpx.WriteJSON(w, status, res)
}

func (h *Handler) returnTransfers(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in ReturnInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.ReturnTransfers(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) listTransfers(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			return apperr.Invalid("the request is invalid", fieldErr("limit", "INVALID_VALUE", "a positive integer"))
		}
	}
	rows, err := h.svc.ListTransfers(r.Context(), pid, id, r.URL.Query().Get("direction"), limit)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) transferTargets(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	if _, err := tenancy.PropertyID(r); err != nil {
		return err
	}
	rows, err := h.svc.TransferTargets(r.Context(), pid, id, r.URL.Query().Get("q"), 25)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}
