package auditpack

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/reports"
	"kamarapms/internal/tenancy"
)

// Handler serves the archive of the night audit packs (docs/architecture/38-reports-night-audit-pack.md 8.3).
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes. Each handler asks for its permission before it reads the path or the query.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/night-audit/packs"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("GET "+p+"/{date}", httpx.HandlerFunc(h.day)) // the manifest, or the PDF when the date ends in .pdf (a pattern cannot name a part of a segment)
	mux.Handle("GET "+p+"/{date}/sections/{section}", httpx.HandlerFunc(h.section))
	mux.Handle("POST "+p+"/{date}/rebuild", httpx.HandlerFunc(h.rebuild))
	mux.Handle("GET "+p+"/{date}/deliveries", httpx.HandlerFunc(h.deliveries))
	mux.Handle("POST "+p+"/{date}/resend", httpx.HandlerFunc(h.resend))
	mux.Handle("GET /api/v1/properties/{propertyId}/night-audit/pack-recipients", httpx.HandlerFunc(h.listRecipients))
	mux.Handle("POST /api/v1/properties/{propertyId}/night-audit/pack-recipients", httpx.HandlerFunc(h.createRecipient))
	mux.Handle("PATCH /api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}", httpx.HandlerFunc(h.updateRecipient))
	mux.Handle("DELETE /api/v1/properties/{propertyId}/night-audit/pack-recipients/{id}", httpx.HandlerFunc(h.deleteRecipient))
}

func dateParam(field, v string) (civil.Date, error) {
	d, err := civil.ParseDate(v)
	if err != nil {
		return d, apperr.Invalid("the request is invalid", apperr.FieldError{Field: field, Code: "INVALID_FORMAT", Message: "YYYY-MM-DD"})
	}
	return d, nil
}

func optDate(r *http.Request, field string) (*civil.Date, error) {
	v := r.URL.Query().Get(field)
	if v == "" {
		return nil, nil
	}
	d, err := dateParam(field, v)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackView); err != nil {
		return err
	}
	var in ListInput
	if in.From, err = optDate(r, "from"); err != nil {
		return err
	}
	if in.To, err = optDate(r, "to"); err != nil {
		return err
	}
	if in.Cursor, err = optDate(r, "cursor"); err != nil {
		return err
	}
	res, err := h.svc.List(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) day(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackView); err != nil {
		return err
	}
	v, isPDF := strings.CutSuffix(r.PathValue("date"), ".pdf")
	date, err := dateParam("date", v)
	if err != nil {
		return err
	}
	if !isPDF {
		res, err := h.svc.Manifest(r.Context(), pid, date)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, res)
	}
	var codes []string
	if s := r.URL.Query().Get("sections"); s != "" {
		codes = strings.Split(s, ",")
	}
	name, pdf, err := h.svc.PDF(r.Context(), pid, date, codes)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store") // financial documents are never cached
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(pdf) //nolint:gosec // G705: a PDF with nosniff, never HTML
	return err
}

func (h *Handler) section(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackView); err != nil {
		return err
	}
	date, err := dateParam("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	code := r.PathValue("section")
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "csv" {
		return apperr.Invalid("the query is invalid", apperr.FieldError{Field: "format", Code: "INVALID_VALUE", Message: "json or csv"})
	}
	data, err := h.svc.SectionData(r.Context(), pid, date, code)
	if err != nil {
		return err
	}
	if format != "csv" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(data) //nolint:gosec // G705: JSON built by the server with nosniff, never HTML
		return err
	}
	head, rows, ok, err := tabular(code, data)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Invalid("the query is invalid", apperr.FieldError{Field: "format", Code: "INVALID_VALUE", Message: "this section has no table: json"})
	}
	head = csvlang.Header(r, head)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="night-audit-`+strings.ToLower(strings.ReplaceAll(code, "_", "-"))+"-"+date.String()+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write(head)
	for _, row := range rows {
		for i := range row {
			row[i] = reports.SafeCell(row[i])
		}
		_ = cw.Write(row)
	}
	cw.Flush()
	return nil
}

// table is a report that can be written as CSV.
type table interface {
	CSV() ([]string, [][]string)
}

func decode[T table](data []byte) (table, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// tabular reads the stored JSON of a section back into its report and gives its rows. ok is false for a section that is not a table.
func tabular(code string, data []byte) (head []string, rows [][]string, ok bool, err error) {
	var t table
	switch code {
	case SecFlash:
		t, err = decode[reports.DailyFlash](data)
	case SecRevenue:
		t, err = decode[reports.Revenue](data)
	case SecTax:
		t, err = decode[reports.TaxReport](data)
	case SecPayments:
		t, err = decode[reports.Cashier](data)
	case SecZReports:
		t, err = decode[reports.ZReport](data)
	case SecGuestLedger:
		t, err = decode[reports.GuestLedgerReport](data)
	case SecDepositLedger:
		t, err = decode[reports.DepositLedgerReport](data)
	case SecCorrections:
		t, err = decode[reports.CorrectionReport](data)
	case SecCancellations:
		t, err = decode[reports.CancellationReport](data)
	case SecInHouse:
		t, err = decode[reports.StayList](data)
	default:
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	head, rows = t.CSV()
	return head, rows, true, nil
}

func (h *Handler) rebuild(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackManage); err != nil {
		return err
	}
	date, err := dateParam("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	res, err := h.svc.Rebuild(r.Context(), pid, date)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deliveries(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackView); err != nil {
		return err
	}
	date, err := dateParam("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	res, err := h.svc.Deliveries(r.Context(), pid, date)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) resend(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackResend); err != nil {
		return err
	}
	date, err := dateParam("date", r.PathValue("date"))
	if err != nil {
		return err
	}
	var in ResendInput
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(w, r, &in); err != nil {
			return err
		}
	}
	res, err := h.svc.Resend(r.Context(), pid, date, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func recipientID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "id", Code: "INVALID_FORMAT", Message: "a positive number"})
	}
	return id, nil
}

func (h *Handler) listRecipients(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	res, err := h.svc.ListRecipients(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createRecipient(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackManage); err != nil {
		return err
	}
	var in RecipientInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.CreateRecipient(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) updateRecipient(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackManage); err != nil {
		return err
	}
	id, err := recipientID(r)
	if err != nil {
		return err
	}
	var in RecipientPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.UpdateRecipient(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteRecipient(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if _, err := h.svc.access(r.Context(), pid, auth.PermNightAuditPackManage); err != nil {
		return err
	}
	id, err := recipientID(r)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteRecipient(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
