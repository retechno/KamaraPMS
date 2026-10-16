package pos

import (
	"net/http"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerPrinting mounts what the staff of the property do with the print agents and the papers (docs/architecture/06-api.md 24.3).
func (h *Handler) registerPrinting(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/pos"
	mux.Handle("GET "+p+"/devices", httpx.HandlerFunc(h.listDevices))
	mux.Handle("POST "+p+"/devices/enrollments", httpx.HandlerFunc(h.createEnrollment))
	mux.Handle("POST "+p+"/devices/{id}/revoke", httpx.HandlerFunc(h.revokeDevice))
	mux.Handle("POST "+p+"/devices/{id}/rotate-token", httpx.HandlerFunc(h.rotateDeviceToken))
	mux.Handle("GET "+p+"/outlets/{id}/failed-print-jobs", httpx.HandlerFunc(h.failedJobs))
	mux.Handle("POST "+p+"/print-jobs/{id}/reprint", httpx.HandlerFunc(h.reprintJob))
	mux.Handle("POST "+p+"/bills/{id}/print", httpx.HandlerFunc(h.printBill))
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListDevices(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) createEnrollment(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateEnrollment(r.Context(), pid)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) revokeDevice(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.RevokeDevice(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) rotateDeviceToken(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.RotateDeviceToken(r.Context(), pid, id)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) failedJobs(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	list, err := h.svc.FailedJobs(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *Handler) reprintJob(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in ReprintInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.ReprintJob(r.Context(), pid, r.PathValue("id"), in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) printBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in PrintBillInput
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	v, err := h.svc.PrintBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}
