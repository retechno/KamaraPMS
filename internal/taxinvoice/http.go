package taxinvoice

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the tax invoice API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/tax/invoices"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.issue))
	mux.Handle("GET "+p+"/preview", httpx.HandlerFunc(h.preview))
	mux.Handle("GET "+p+"/coverage", httpx.HandlerFunc(h.coverage))
	mux.Handle("GET "+p+"/exports", httpx.HandlerFunc(h.exports))
	mux.Handle("POST "+p+"/exports", httpx.HandlerFunc(h.export))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("POST "+p+"/{id}/void", httpx.HandlerFunc(h.void))
	mux.Handle("PUT "+p+"/{id}/djp-number", httpx.HandlerFunc(h.djpNumber))
}

func ids(r *http.Request) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errInvoiceNotFound()
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := Filter{Status: strings.ToUpper(r.URL.Query().Get("status")), From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs), Q: strings.TrimSpace(r.URL.Query().Get("q"))}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fieldErr("limit", "INVALID_VALUE", "a number"))
		}
		f.Limit = n
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Invoices(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Invoice]{Data: list})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	inv, err := h.svc.GetInvoice(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h *Handler) issue(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in IssueInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	inv, err := h.svc.Issue(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	in := IssueInput{SourceType: strings.ToUpper(q.Get("source_type"))}
	if in.SourceType == SourceFolio {
		in.FolioID = id
		in.Buyer = &Party{Name: q.Get("buyer_name"), NPWP: q.Get("buyer_npwp"), Address: q.Get("buyer_address")}
	} else {
		in.CityLedgerInvoiceID = id
	}
	pv, err := h.svc.Preview(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pv)
}

func (h *Handler) void(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	inv, err := h.svc.VoidInvoice(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h *Handler) djpNumber(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in DJPNumberInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	inv, err := h.svc.SetDJPNumber(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h *Handler) coverage(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	from, to := dateQuery(r, "from", &errs), dateQuery(r, "to", &errs)
	if len(errs) == 0 && (from == nil || to == nil) {
		errs = append(errs, fieldErr("from", "REQUIRED", "from and to"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	cov, err := h.svc.CoverageOf(r.Context(), pid, *from, *to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, cov)
}

func (h *Handler) exports(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Exports(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Export]{Data: list})
}

// export answers with the file itself; the batch is in the headers.
func (h *Handler) export(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in ExportInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f, err := h.svc.ExportInvoices(r.Context(), pid, in)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.FileName+`"`)
	w.Header().Set("X-Export-Id", strconv.FormatInt(f.ID, 10))
	w.Header().Set("X-Export-Sha256", f.SHA256)
	w.Header().Set("X-Export-Invoices", strconv.Itoa(f.InvoiceCount))
	_, err = w.Write(f.Content)
	return err
}
