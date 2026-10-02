package accounting

import (
	"net/http"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

func (h *Handler) fiscalYears(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.FiscalYears(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[FiscalYear]{Data: list})
}

func (h *Handler) closeFiscalYear(w http.ResponseWriter, r *http.Request) error {
	pid, d, err := periodParam(r)
	if err != nil {
		return err
	}
	fy, err := h.svc.CloseFiscalYear(r.Context(), pid, d)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, fy)
}

func (h *Handler) reopenFiscalYear(w http.ResponseWriter, r *http.Request) error {
	pid, d, err := periodParam(r)
	if err != nil {
		return err
	}
	var in ReopenYearInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fy, err := h.svc.ReopenFiscalYear(r.Context(), pid, d, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, fy)
}
