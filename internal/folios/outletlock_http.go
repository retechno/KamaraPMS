package folios

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerOutletLock mounts the routes of the lock of a folio for outlet charges (docs/architecture/34-pos.md section 6). They belong to the POS module of the plan.
func (h *Handler) registerOutletLock(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/folios/{id}/outlet-lock", httpx.HandlerFunc(h.getOutletLock))
	mux.Handle("PUT "+p+"/folios/{id}/outlet-lock", httpx.HandlerFunc(h.putOutletLock))
	mux.Handle("POST "+p+"/folios/{id}/outlet-lock/lock", httpx.HandlerFunc(h.lockOutletFolio))
	mux.Handle("GET "+p+"/outlet-charges", httpx.HandlerFunc(h.listOutletCharges))
}

func (h *Handler) getOutletLock(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetOutletLock(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) putOutletLock(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in OutletLockInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.SetOutletLock(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) lockOutletFolio(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in OutletLockPatch
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	v, err := h.svc.LockOutletFolio(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listOutletCharges(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var date *civil.Date
	if v := strings.TrimSpace(r.URL.Query().Get("date")); v != "" {
		d, perr := civil.ParseDate(v)
		if perr != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("date", "INVALID_VALUE", "YYYY-MM-DD"))
		}
		date = &d
	}
	list, err := h.svc.ListOutletCharges(r.Context(), pid, date, strings.TrimSpace(r.URL.Query().Get("result")))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}
