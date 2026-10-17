package inventory

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the movements (35-inventory.md 10): requisitions with their approval, transfers, issues, counts and the amenities. Mounted by Register next to the purchasing.
func (h *Handler) registerMovements(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/requisitions", httpx.HandlerFunc(h.listRequisitions))
	mux.Handle("POST "+p+"/requisitions", httpx.HandlerFunc(h.createRequisition))
	mux.Handle("GET "+p+"/requisitions/{id}", httpx.HandlerFunc(h.getRequisition))
	mux.Handle("PUT "+p+"/requisitions/{id}", httpx.HandlerFunc(h.updateRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/submit", httpx.HandlerFunc(h.submitRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/recall", httpx.HandlerFunc(h.recallRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/cancel", httpx.HandlerFunc(h.cancelRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/approve", httpx.HandlerFunc(h.approveRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/return", httpx.HandlerFunc(h.returnRequisition))
	mux.Handle("POST "+p+"/requisitions/{id}/reject", httpx.HandlerFunc(h.rejectRequisition))

	mux.Handle("GET "+p+"/transfers", httpx.HandlerFunc(h.listTransfers))
	mux.Handle("POST "+p+"/transfers", httpx.HandlerFunc(h.postTransfer))
	mux.Handle("GET "+p+"/transfers/{id}", httpx.HandlerFunc(h.getTransfer))
	mux.Handle("POST "+p+"/transfers/{id}/reverse", httpx.HandlerFunc(h.reverseTransfer))

	mux.Handle("GET "+p+"/issues", httpx.HandlerFunc(h.listIssues))
	mux.Handle("POST "+p+"/issues", httpx.HandlerFunc(h.postIssue))
	mux.Handle("GET "+p+"/issues/{id}", httpx.HandlerFunc(h.getIssue))
	mux.Handle("POST "+p+"/issues/{id}/reverse", httpx.HandlerFunc(h.reverseIssue))

	mux.Handle("GET "+p+"/counts", httpx.HandlerFunc(h.listCounts))
	mux.Handle("POST "+p+"/counts", httpx.HandlerFunc(h.startCount))
	mux.Handle("GET "+p+"/counts/{id}", httpx.HandlerFunc(h.getCount))
	mux.Handle("PUT "+p+"/counts/{id}/lines", httpx.HandlerFunc(h.enterCountLines))
	mux.Handle("POST "+p+"/counts/{id}/submit", httpx.HandlerFunc(h.submitCount))
	mux.Handle("POST "+p+"/counts/{id}/approve", httpx.HandlerFunc(h.approveCount))
	mux.Handle("POST "+p+"/counts/{id}/cancel", httpx.HandlerFunc(h.cancelCount))

	mux.Handle("GET "+p+"/amenities/plan", httpx.HandlerFunc(h.getAmenityPlan))
	mux.Handle("PUT "+p+"/amenities/plan", httpx.HandlerFunc(h.putAmenityPlan))
	mux.Handle("GET "+p+"/amenities/preview", httpx.HandlerFunc(h.amenityPreview))
	mux.Handle("GET "+p+"/amenities/runs", httpx.HandlerFunc(h.listAmenityRuns))
	mux.Handle("GET "+p+"/amenities/runs/{date}", httpx.HandlerFunc(h.getAmenityRun))
	mux.Handle("POST "+p+"/amenities/runs/{date}/reverse", httpx.HandlerFunc(h.reverseAmenityRun))
}

// ReasonInput carries the reason of a reversal or a rejection.
type ReasonInput struct {
	Reason string `json:"reason"`
}

func dateOf(r *http.Request) (civil.Date, error) {
	d, err := civil.ParseDate(r.PathValue("date"))
	if err != nil {
		return civil.Date{}, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return d, nil
}

// ------------------------------------------------------------------------------------------------ requisitions

func (h *Handler) listRequisitions(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	mine, err := queryBool(r, "mine")
	if err != nil {
		return err
	}
	f := RequisitionFilter{Status: r.URL.Query().Get("status"), Q: strings.TrimSpace(r.URL.Query().Get("q")), Mine: mine != nil && *mine, Limit: queryLimit(r)}
	list, err := h.svc.ListRequisitions(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) createRequisition(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[RequisitionInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Requisition, error) { return h.svc.CreateRequisition(r.Context(), pid, in) })
}

func (h *Handler) getRequisition(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Requisition, error) { return h.svc.GetRequisition(r.Context(), pid, id) })
}

func (h *Handler) updateRequisition(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in RequisitionInput) (Requisition, error) {
		return h.svc.UpdateRequisition(r.Context(), pid, id, in)
	})
}

func (h *Handler) submitRequisition(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Requisition, error) { return h.svc.SubmitRequisition(r.Context(), pid, id) })
}

func (h *Handler) recallRequisition(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Requisition, error) { return h.svc.RecallRequisition(r.Context(), pid, id) })
}

func (h *Handler) cancelRequisition(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Requisition, error) { return h.svc.CancelRequisition(r.Context(), pid, id) })
}

func (h *Handler) approveRequisition(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ApproveInput) (Requisition, error) {
		return h.svc.ApproveRequisition(r.Context(), pid, id, in)
	})
}

func (h *Handler) returnRequisition(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ReturnRequestInput) (Requisition, error) {
		return h.svc.ReturnRequisition(r.Context(), pid, id, in)
	})
}

func (h *Handler) rejectRequisition(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ReasonInput) (Requisition, error) {
		return h.svc.RejectRequisition(r.Context(), pid, id, in.Reason)
	})
}

// ------------------------------------------------------------------------------------------------ transfers

func (h *Handler) listTransfers(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	req, err := queryInt(r, "requisition_id")
	if err != nil {
		return err
	}
	free, err := queryBool(r, "free")
	if err != nil {
		return err
	}
	f := TransferFilter{Status: r.URL.Query().Get("status"), Q: strings.TrimSpace(r.URL.Query().Get("q")), FreeOnly: free != nil && *free, Limit: queryLimit(r)}
	if req != nil {
		f.RequisitionID = *req
	}
	list, err := h.svc.ListTransfers(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) postTransfer(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[TransferInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Transfer, error) { return h.svc.PostTransfer(r.Context(), pid, in) })
}

func (h *Handler) getTransfer(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Transfer, error) { return h.svc.GetTransfer(r.Context(), pid, id) })
}

func (h *Handler) reverseTransfer(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ReasonInput) (Transfer, error) {
		return h.svc.ReverseTransfer(r.Context(), pid, id, in.Reason)
	})
}

// ------------------------------------------------------------------------------------------------ issues

func (h *Handler) listIssues(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	wh, err := queryInt(r, "warehouse_id")
	if err != nil {
		return err
	}
	dep, err := queryInt(r, "department_id")
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
	f := IssueFilter{Purpose: r.URL.Query().Get("purpose"), Origin: r.URL.Query().Get("origin"), Q: strings.TrimSpace(r.URL.Query().Get("q")), From: from, To: to, Limit: queryLimit(r)}
	if wh != nil {
		f.WarehouseID = *wh
	}
	if dep != nil {
		f.DepartmentID = *dep
	}
	list, err := h.svc.ListIssues(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) postIssue(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[IssueInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Issue, error) { return h.svc.PostIssue(r.Context(), pid, in) })
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Issue, error) { return h.svc.GetIssue(r.Context(), pid, id) })
}

func (h *Handler) reverseIssue(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ReasonInput) (Issue, error) {
		return h.svc.ReverseIssue(r.Context(), pid, id, in.Reason)
	})
}

// ------------------------------------------------------------------------------------------------ counts

func (h *Handler) listCounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	wh, err := queryInt(r, "warehouse_id")
	if err != nil {
		return err
	}
	f := CountFilter{Status: r.URL.Query().Get("status"), Limit: queryLimit(r)}
	if wh != nil {
		f.WarehouseID = *wh
	}
	list, err := h.svc.ListCounts(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) startCount(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[CountInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Count, error) { return h.svc.StartCount(r.Context(), pid, in) })
}

func (h *Handler) getCount(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Count, error) { return h.svc.GetCount(r.Context(), pid, id) })
}

// CountLinesInput is the body of the entry of a count.
type CountLinesInput struct {
	Lines []CountLineInput `json:"lines"`
}

func (h *Handler) enterCountLines(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in CountLinesInput) (Count, error) {
		return h.svc.EnterCountLines(r.Context(), pid, id, in.Lines)
	})
}

func (h *Handler) submitCount(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Count, error) { return h.svc.SubmitCount(r.Context(), pid, id) })
}

func (h *Handler) approveCount(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Count, error) { return h.svc.ApproveCount(r.Context(), pid, id) })
}

func (h *Handler) cancelCount(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Count, error) { return h.svc.CancelCount(r.Context(), pid, id) })
}

// ------------------------------------------------------------------------------------------------ amenities

func (h *Handler) getAmenityPlan(w http.ResponseWriter, r *http.Request) error {
	return jsonOf(w, r, http.StatusOK, func(pid int64) (AmenityPlan, error) { return h.svc.GetAmenityPlan(r.Context(), pid) })
}

func (h *Handler) putAmenityPlan(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[AmenityPlanInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusOK, func(pid int64) (AmenityPlan, error) { return h.svc.SaveAmenityPlan(r.Context(), pid, in) })
}

func (h *Handler) amenityPreview(w http.ResponseWriter, r *http.Request) error {
	return jsonOf(w, r, http.StatusOK, func(pid int64) (AmenityPreview, error) { return h.svc.AmenityPreview(r.Context(), pid) })
}

func (h *Handler) listAmenityRuns(w http.ResponseWriter, r *http.Request) error {
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
	list, err := h.svc.ListAmenityRuns(r.Context(), pid, from, to, queryLimit(r))
	return page(w, list, err)
}

func (h *Handler) getAmenityRun(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	d, err := dateOf(r)
	if err != nil {
		return err
	}
	run, err := h.svc.GetAmenityRun(r.Context(), pid, d)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *Handler) reverseAmenityRun(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	d, err := dateOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ReasonInput](w, r)
	if err != nil {
		return err
	}
	run, err := h.svc.ReverseAmenityRun(r.Context(), pid, d, in.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, run)
}
