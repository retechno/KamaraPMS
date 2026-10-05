package payables

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the payables API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/payables"
	mux.Handle("GET "+p+"/suppliers", httpx.HandlerFunc(h.suppliers))
	mux.Handle("POST "+p+"/suppliers", httpx.HandlerFunc(h.createSupplier))
	mux.Handle("GET "+p+"/suppliers/{id}", httpx.HandlerFunc(h.supplier))
	mux.Handle("PATCH "+p+"/suppliers/{id}", httpx.HandlerFunc(h.updateSupplier))
	mux.Handle("GET "+p+"/suppliers/{id}/open-bills", httpx.HandlerFunc(h.openBills))
	mux.Handle("GET "+p+"/bills", httpx.HandlerFunc(h.bills))
	mux.Handle("POST "+p+"/bills", httpx.HandlerFunc(h.postBill))
	mux.Handle("GET "+p+"/bills/{id}", httpx.HandlerFunc(h.bill))
	mux.Handle("POST "+p+"/bills/{id}/void", httpx.HandlerFunc(h.voidBill))
	mux.Handle("GET "+p+"/payments", httpx.HandlerFunc(h.payments))
	mux.Handle("POST "+p+"/payments", httpx.HandlerFunc(h.postPayment))
	mux.Handle("GET "+p+"/payments/{id}", httpx.HandlerFunc(h.payment))
	mux.Handle("POST "+p+"/payments/{id}/void", httpx.HandlerFunc(h.voidPayment))
	mux.Handle("GET "+p+"/credit-notes", httpx.HandlerFunc(h.creditNotes))
	mux.Handle("POST "+p+"/credit-notes", httpx.HandlerFunc(h.postCreditNote))
	mux.Handle("GET "+p+"/credit-notes/{id}", httpx.HandlerFunc(h.creditNote))
	mux.Handle("POST "+p+"/credit-notes/{id}/apply", httpx.HandlerFunc(h.applyCredit))
	mux.Handle("POST "+p+"/credit-notes/{id}/void", httpx.HandlerFunc(h.voidCreditNote))
	mux.Handle("GET "+p+"/aging", httpx.HandlerFunc(h.aging))
}

func ids(r *http.Request, notFound *apperr.Error) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, notFound
	}
	return pid, id, nil
}

func idQuery(r *http.Request, name string, errs *[]apperr.FieldError) *int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 1 {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "an id"))
		return nil
	}
	return &id
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

func limitQuery(r *http.Request, errs *[]apperr.FieldError) int {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fieldErr("limit", "INVALID_VALUE", "a number"))
	}
	return n
}

func (h *Handler) suppliers(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	f := SupplierFilter{Q: r.URL.Query().Get("q")}
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("active", "INVALID_VALUE", "true or false"))
		}
		f.Active = &b
	}
	list, err := h.svc.Suppliers(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Supplier]{Data: list})
}

func (h *Handler) supplier(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errSupplierNotFound())
	if err != nil {
		return err
	}
	sup, err := h.svc.GetSupplier(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, sup)
}

func (h *Handler) createSupplier(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in SupplierInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	sup, err := h.svc.CreateSupplier(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, sup)
}

func (h *Handler) updateSupplier(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errSupplierNotFound())
	if err != nil {
		return err
	}
	var in SupplierPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	sup, err := h.svc.UpdateSupplier(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, sup)
}

func (h *Handler) openBills(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errSupplierNotFound())
	if err != nil {
		return err
	}
	list, err := h.svc.OpenBills(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[OpenBill]{Data: list})
}

func (h *Handler) bills(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	q := r.URL.Query()
	f := BillFilter{SupplierID: idQuery(r, "supplier_id", &errs), Status: strings.ToUpper(q.Get("status")), From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs), Q: q.Get("q"), Limit: limitQuery(r, &errs)}
	if v := q.Get("open_only"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fieldErr("open_only", "INVALID_VALUE", "true or false"))
		}
		f.OpenOnly = b
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Bills(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Bill]{Data: list})
}

func (h *Handler) bill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errBillNotFound())
	if err != nil {
		return err
	}
	b, err := h.svc.GetBill(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func (h *Handler) postBill(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in BillInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.PostBill(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) voidBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errBillNotFound())
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.VoidBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) payments(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := PaymentFilter{SupplierID: idQuery(r, "supplier_id", &errs), Status: strings.ToUpper(r.URL.Query().Get("status")), From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs), Limit: limitQuery(r, &errs)}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Payments(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[SupplierPayment]{Data: list})
}

func (h *Handler) payment(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errPaymentNotFound())
	if err != nil {
		return err
	}
	pay, err := h.svc.GetPayment(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pay)
}

func (h *Handler) postPayment(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in PaymentInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pay, err := h.svc.PostPayment(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, pay)
}

func (h *Handler) voidPayment(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errPaymentNotFound())
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pay, err := h.svc.VoidPayment(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pay)
}

func (h *Handler) aging(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	asOf := dateQuery(r, "as_of", &errs)
	if len(errs) > 0 {
		return apperr.Invalid("the report parameters are invalid", errs...)
	}
	a, err := h.svc.Aging(r.Context(), pid, asOf)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) creditNotes(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	q := r.URL.Query()
	f := CreditFilter{SupplierID: idQuery(r, "supplier_id", &errs), BillID: idQuery(r, "bill_id", &errs), Status: strings.ToUpper(q.Get("status")), From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs), Q: q.Get("q"), Limit: limitQuery(r, &errs)}
	if v := q.Get("unapplied_only"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fieldErr("unapplied_only", "INVALID_VALUE", "true or false"))
		}
		f.UnappliedOnly = b
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.CreditNotes(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[CreditNote]{Data: list})
}

func (h *Handler) creditNote(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errCreditNotFound())
	if err != nil {
		return err
	}
	c, err := h.svc.GetCreditNote(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) postCreditNote(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in CreditNoteInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	c, err := h.svc.PostCreditNote(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) applyCredit(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errCreditNotFound())
	if err != nil {
		return err
	}
	var in ApplyCreditInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	c, err := h.svc.ApplyCredit(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) voidCreditNote(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errCreditNotFound())
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	c, err := h.svc.VoidCreditNote(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, c)
}
