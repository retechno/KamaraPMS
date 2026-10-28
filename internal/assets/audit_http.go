package assets

import (
	"net/http"

	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The stock-take and maintenance routes (docs/architecture/37-fixed-assets.md 4.10, 4.11, 8). A read route checks the permission before it reads the filters (the authz matrix of the module).

func (h *Handler) registerAudits(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/assets"
	mux.Handle("GET "+p+"/audits", httpx.HandlerFunc(h.listAudits))
	mux.Handle("POST "+p+"/audits", httpx.HandlerFunc(h.startAudit))
	mux.Handle("GET "+p+"/audits/{id}", httpx.HandlerFunc(h.getAudit))
	mux.Handle("PUT "+p+"/audits/{id}/lines", httpx.HandlerFunc(h.scanAsset))
	mux.Handle("POST "+p+"/audits/{id}/close", httpx.HandlerFunc(h.closeAudit))
	mux.Handle("GET "+p+"/proposals", httpx.HandlerFunc(h.listProposals))
	mux.Handle("POST "+p+"/proposals/{id}/apply", httpx.HandlerFunc(h.applyProposal))
	mux.Handle("POST "+p+"/proposals/{id}/reject", httpx.HandlerFunc(h.rejectProposal))
	mux.Handle("GET "+p+"/reports/audit-differences", httpx.HandlerFunc(h.reportAuditDifferences))
	mux.Handle("GET "+p+"/{id}/maintenance", httpx.HandlerFunc(h.assetMaintenance))
}

func (h *Handler) listAudits(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListAudits(r.Context(), pid, r.URL.Query().Get("status"))
	return list(w, v, err)
}

func (h *Handler) startAudit(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in StartAuditInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	d, err := h.svc.StartAudit(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h *Handler) getAudit(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	d, err := h.svc.GetAudit(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) scanAsset(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in ScanInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.ScanAsset(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) closeAudit(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.CloseAudit(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listProposals(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	audit, err := queryInt(r, "audit_id")
	if err != nil {
		return err
	}
	v, err := h.svc.ListProposals(r.Context(), pid, r.URL.Query().Get("status"), audit)
	return list(w, v, err)
}

func (h *Handler) applyProposal(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var in ApplyProposalInput
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	v, err := h.svc.ApplyProposal(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) rejectProposal(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		return err
	}
	v, err := h.svc.RejectProposal(r.Context(), pid, id, body.Note)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) reportAuditDifferences(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	asCSV, err := wantsCSV(r)
	if err != nil {
		return err
	}
	audit, err := queryInt(r, "audit_id")
	if err != nil {
		return err
	}
	from, err := queryDate(r, "date_from")
	if err != nil {
		return err
	}
	to, err := queryDate(r, "date_to")
	if err != nil {
		return err
	}
	rows, err := h.svc.AuditDifferences(r.Context(), pid, audit, r.URL.Query().Get("result"), from, to)
	if err != nil {
		return err
	}
	if !asCSV {
		return list(w, rows, nil)
	}
	out := make([][]string, 0, len(rows))
	for _, x := range rows {
		out = append(out, []string{x.AuditNumber, x.AuditStatus, x.StartedAt.UTC().Format("2006-01-02"), x.AssetNumber, x.AssetName, x.CategoryCode, x.Result, str(x.ExpectedLocationName),
			str(x.FoundLocationName), str(x.Note), x.CarryingValue.String(), x.AssetStatus, str(x.ProposalKind), str(x.ProposalStatus)})
	}
	return writeCSV(w, r, "asset-audit-differences.csv", []string{"audit", "audit_status", "started", "number", "name", "category", "result", "expected_location", "found_location", "note",
		"carrying_value", "asset_status", "proposal", "proposal_status"}, out)
}

func (h *Handler) assetMaintenance(w http.ResponseWriter, r *http.Request) error {
	pid, err := h.reportProperty(r)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.AssetRepairs(r.Context(), pid, id)
	return list(w, v, err)
}
