package folios

import (
	"net/http"

	"kamarapms/internal/platform/httpx"
)

// The routes of the master folio of a group and of its routing (docs/architecture/30-group-folio.md, part C).
func (h *Handler) registerMaster(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/groups/{id}/routing", httpx.HandlerFunc(h.getGroupRouting))
	mux.Handle("PUT "+p+"/groups/{id}/routing", httpx.HandlerFunc(h.putGroupRouting))
	mux.Handle("GET "+p+"/groups/{id}/routing/posted", httpx.HandlerFunc(h.postedForMaster))
	mux.Handle("POST "+p+"/groups/{id}/master-folio", httpx.HandlerFunc(h.openMaster))
	mux.Handle("GET "+p+"/reservations/{id}/rooms/{lineId}/routing", httpx.HandlerFunc(h.getLineRouting))
}

func (h *Handler) getGroupRouting(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	out, err := h.svc.GetGroupRouting(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) putGroupRouting(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in GroupRoutingInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.SetGroupRouting(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) openMaster(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	out, err := h.svc.OpenMaster(r.Context(), pid, id)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	return httpx.WriteJSON(w, status, out)
}

func (h *Handler) getLineRouting(w http.ResponseWriter, r *http.Request) error {
	pid, res, line, err := lineIDs(r)
	if err != nil {
		return err
	}
	out, err := h.svc.GetLineRouting(r.Context(), pid, res, line)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) postedForMaster(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	out, err := h.svc.PostedForMaster(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}
