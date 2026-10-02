package cityledger

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the city ledger API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/city-ledger"
	mux.Handle("GET "+p+"/accounts", httpx.HandlerFunc(h.accounts))
	mux.Handle("GET "+p+"/accounts/{id}", httpx.HandlerFunc(h.account))
	mux.Handle("GET "+p+"/accounts/{id}/statement", httpx.HandlerFunc(h.statement))
	mux.Handle("GET "+p+"/accounts/{id}/aging", httpx.HandlerFunc(h.aging))
	mux.Handle("GET "+p+"/accounts/{id}/receipts", httpx.HandlerFunc(h.receipts))
	mux.Handle("POST "+p+"/accounts/{id}/receipts", httpx.HandlerFunc(h.receive))
	mux.Handle("POST "+p+"/receipts/{id}/void", httpx.HandlerFunc(h.void))
	mux.Handle("GET "+p+"/accounts/{id}/invoice-candidates", httpx.HandlerFunc(h.candidates))
	mux.Handle("GET "+p+"/accounts/{id}/invoices", httpx.HandlerFunc(h.invoices))
	mux.Handle("POST "+p+"/accounts/{id}/invoices", httpx.HandlerFunc(h.createInvoice))
	mux.Handle("GET "+p+"/invoices/{id}", httpx.HandlerFunc(h.invoice))
	mux.Handle("POST "+p+"/invoices/{id}/void", httpx.HandlerFunc(h.voidInvoice))
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return propertyID, id, nil
}

type idCursor struct {
	After int64 `json:"a"`
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	owing := false
	if v := r.URL.Query().Get("owing"); v != "" {
		if owing, err = strconv.ParseBool(v); err != nil {
			return apperr.Invalid("the query is invalid", field("owing", "INVALID_VALUE", "true or false"))
		}
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.Accounts(r.Context(), pid, cur.After, owing, r.URL.Query().Get("q"), page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[Account]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{After: out.Data[page.Limit-1].CompanyID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	a, err := h.svc.GetAccount(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

// DateRange reads the optional from and to query dates.
func DateRange(r *http.Request) (from, to *civil.Date, err error) {
	var fields []apperr.FieldError
	read := func(name string) *civil.Date {
		v := strings.TrimSpace(r.URL.Query().Get(name))
		if v == "" {
			return nil
		}
		d, err := civil.ParseDate(v)
		if err != nil {
			fields = append(fields, field(name, "INVALID_DATE", "a date as YYYY-MM-DD"))
			return nil
		}
		return &d
	}
	from, to = read("from"), read("to")
	if from != nil && to != nil && to.Before(*from) {
		fields = append(fields, field("to", "INVALID_RANGE", "not before from"))
	}
	if len(fields) > 0 {
		return nil, nil, apperr.Invalid("the query is invalid", fields...)
	}
	return from, to, nil
}

func (h *Handler) statement(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	from, to, err := DateRange(r)
	if err != nil {
		return err
	}
	st, err := h.svc.Statement(r.Context(), pid, id, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) aging(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	a, err := h.svc.Aging(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) receipts(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Receipts(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Receipt]{Data: list})
}

func (h *Handler) receive(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in ReceiptInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Receive(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
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
	res, err := h.svc.VoidReceipt(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) candidates(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Candidates(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Candidate]{Data: list})
}

func (h *Handler) invoices(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Invoices(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Invoice]{Data: list})
}

func (h *Handler) createInvoice(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	var in InvoiceInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	inv, err := h.svc.CreateInvoice(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *Handler) invoice(w http.ResponseWriter, r *http.Request) error {
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

func (h *Handler) voidInvoice(w http.ResponseWriter, r *http.Request) error {
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
