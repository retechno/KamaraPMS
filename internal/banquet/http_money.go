package banquet

import (
	"net/http"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of the money of an event (36 8, PR 2).

func (h *Handler) registerMoney(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet"
	mux.Handle("GET "+p+"/events/{id}/money", httpx.HandlerFunc(h.money))
	mux.Handle("GET "+p+"/events/{id}/folio", httpx.HandlerFunc(h.getFolio))
	mux.Handle("POST "+p+"/events/{id}/folio", httpx.HandlerFunc(h.openFolio))
	mux.Handle("GET "+p+"/events/{id}/lines", httpx.HandlerFunc(h.listLines))
	mux.Handle("POST "+p+"/events/{id}/lines", httpx.HandlerFunc(h.addLine))
	mux.Handle("PATCH "+p+"/events/{id}/lines/{lid}", httpx.HandlerFunc(h.updateLine))
	mux.Handle("DELETE "+p+"/events/{id}/lines/{lid}", httpx.HandlerFunc(h.removeLine))
	mux.Handle("GET "+p+"/events/{id}/estimate", httpx.HandlerFunc(h.estimate))
	mux.Handle("PUT "+p+"/events/{id}/deposit-schedule", httpx.HandlerFunc(h.putSchedule))
	mux.Handle("PUT "+p+"/events/{id}/policy", httpx.HandlerFunc(h.putPolicy))
	mux.Handle("GET "+p+"/events/{id}/cancellation-quote", httpx.HandlerFunc(h.cancelQuote))
	mux.Handle("PUT "+p+"/events/{id}/sessions/{sid}/actual", httpx.HandlerFunc(h.setActual))
	mux.Handle("POST "+p+"/events/{id}/sessions/{sid}/post", httpx.HandlerFunc(h.postSession))
	mux.Handle("PUT "+p+"/events/{id}/posting-mode", httpx.HandlerFunc(h.setPostingMode))
	mux.Handle("POST "+p+"/events/{id}/transfer-to-group", httpx.HandlerFunc(h.transferToGroup))
	mux.Handle("GET "+p+"/reports/unposted", httpx.HandlerFunc(h.unposted))
	mux.Handle("GET "+p+"/policy-templates", httpx.HandlerFunc(h.listTemplates))
	mux.Handle("POST "+p+"/policy-templates", httpx.HandlerFunc(h.createTemplate))
	mux.Handle("PATCH "+p+"/policy-templates/{tid}", httpx.HandlerFunc(h.updateTemplate))
}

// decodeOptional reads a JSON body that may be left out altogether.
func decodeOptional[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var in T
	if r.ContentLength == 0 {
		return in, nil
	}
	return decode[T](w, r)
}

func (h *Handler) money(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Money(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) getFolio(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.GetFolio(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) openFolio(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.OpenFolio(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) listLines(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.ListLines(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) addLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[LineInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.AddLine(r.Context(), pid, id, in)
	return created(w, v, err)
}

func (h *Handler) updateLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	lid, err := pathInt(r, "lid")
	if err != nil {
		return err
	}
	in, err := decode[LineUpdate](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdateLine(r.Context(), pid, id, lid, in)
	return ok(w, v, err)
}

func (h *Handler) removeLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	lid, err := pathInt(r, "lid")
	if err != nil {
		return err
	}
	v, err := h.svc.RemoveLine(r.Context(), pid, id, lid)
	return ok(w, v, err)
}

func (h *Handler) estimate(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Estimate(r.Context(), pid, id)
	return ok(w, v, err)
}

func (h *Handler) putSchedule(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ScheduleInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PutSchedule(r.Context(), pid, id, in)
	return page(w, v, err)
}

func (h *Handler) putPolicy(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PolicyInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PutPolicy(r.Context(), pid, id, in)
	return ok(w, v, err)
}

func (h *Handler) cancelQuote(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.CancelQuote(r.Context(), pid, id)
	return ok(w, v, err)
}

func sessionIDs(r *http.Request) (pid, id, sid int64, err error) {
	if pid, id, err = idsOf(r); err != nil {
		return
	}
	sid, err = pathInt(r, "sid")
	return
}

func (h *Handler) setActual(w http.ResponseWriter, r *http.Request) error {
	pid, id, sid, err := sessionIDs(r)
	if err != nil {
		return err
	}
	in, err := decode[ActualInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetActual(r.Context(), pid, id, sid, in)
	return ok(w, v, err)
}

func (h *Handler) postSession(w http.ResponseWriter, r *http.Request) error {
	pid, id, sid, err := sessionIDs(r)
	if err != nil {
		return err
	}
	in, err := decodeOptional[PostInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PostSession(r.Context(), pid, id, sid, in)
	return created(w, v, err)
}

func (h *Handler) transferToGroup(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[TransferInput](w, r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" && in.Mode == "BALANCE" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	v, err := h.svc.TransferToGroup(r.Context(), pid, id, key, in)
	return created(w, v, err)
}

func (h *Handler) unposted(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.UnpostedReport(r.Context(), pid)
	return page(w, v, err)
}

func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	act, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	v, err := h.svc.ListPolicyTemplates(r.Context(), pid, act)
	return page(w, v, err)
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[PolicyTemplateInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreatePolicyTemplate(r.Context(), pid, in)
	return created(w, v, err)
}

func (h *Handler) updateTemplate(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	tid, err := pathInt(r, "tid")
	if err != nil {
		return err
	}
	in, err := decode[PolicyTemplateInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.UpdatePolicyTemplate(r.Context(), pid, tid, in)
	return ok(w, v, err)
}

func (h *Handler) setPostingMode(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[PostingModeInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.SetPostingMode(r.Context(), pid, id, in)
	return ok(w, v, err)
}
