package accounting

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// RegisterJournals mounts the journal and period routes.
func (h *Handler) RegisterJournals(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/accounting"
	mux.Handle("GET "+p+"/journals", httpx.HandlerFunc(h.journals))
	mux.Handle("POST "+p+"/journals", httpx.HandlerFunc(h.postJournal))
	mux.Handle("POST "+p+"/journals/post-pending", httpx.HandlerFunc(h.postPending))
	mux.Handle("GET "+p+"/journals/{id}", httpx.HandlerFunc(h.journal))
	mux.Handle("POST "+p+"/journals/{id}/reverse", httpx.HandlerFunc(h.reverseJournal))
	mux.Handle("GET "+p+"/periods", httpx.HandlerFunc(h.periods))
	mux.Handle("POST "+p+"/periods/{start}/close", httpx.HandlerFunc(h.closePeriod))
	mux.Handle("POST "+p+"/periods/{start}/reopen", httpx.HandlerFunc(h.reopenPeriod))
}

func journalID(r *http.Request) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errJournalNotFound()
	}
	return pid, id, nil
}

func dateQuery(r *http.Request, name string, errs *[]apperr.FieldError) *civil.Date {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	d, err := civil.ParseDate(v)
	if err != nil {
		*errs = append(*errs, fieldErr(name, "INVALID_DATE", "a date as YYYY-MM-DD"))
		return nil
	}
	return &d
}

func (h *Handler) journals(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	q := r.URL.Query()
	f := JournalFilter{From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs), Type: strings.ToUpper(q.Get("type")), Q: q.Get("q")}
	if v := q.Get("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("account_id", "INVALID_VALUE", "an account id"))
		} else {
			f.AccountID = &id
		}
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil {
			errs = append(errs, fieldErr("limit", "INVALID_VALUE", "a number"))
		}
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Journals(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Journal]{Data: list})
}

func (h *Handler) journal(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := journalID(r)
	if err != nil {
		return err
	}
	j, err := h.svc.GetJournal(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, j)
}

func (h *Handler) postJournal(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in ManualInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	j, err := h.svc.PostManual(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, j)
}

func (h *Handler) reverseJournal(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := journalID(r)
	if err != nil {
		return err
	}
	var in ReverseInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	j, err := h.svc.Reverse(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, j)
}

func (h *Handler) postPending(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	n, err := h.svc.PostPending(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]int{"posted": n})
}

func (h *Handler) periods(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Periods(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Period]{Data: list})
}

func periodParam(r *http.Request) (int64, civil.Date, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, civil.Date{}, err
	}
	d, err := civil.ParseDate(r.PathValue("start"))
	if err != nil || d.Day() != 1 {
		return 0, civil.Date{}, apperr.NotFound("PERIOD_NOT_FOUND", "a period is named by the first day of its month")
	}
	return pid, d, nil
}

func (h *Handler) closePeriod(w http.ResponseWriter, r *http.Request) error {
	pid, d, err := periodParam(r)
	if err != nil {
		return err
	}
	per, err := h.svc.ClosePeriod(r.Context(), pid, d)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, per)
}

func (h *Handler) reopenPeriod(w http.ResponseWriter, r *http.Request) error {
	pid, d, err := periodParam(r)
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	per, err := h.svc.ReopenPeriod(r.Context(), pid, d, in.Reason)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, per)
}
