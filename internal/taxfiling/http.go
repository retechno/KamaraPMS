package taxfiling

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the tax filing API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/tax"
	mux.Handle("GET "+p+"/profiles", httpx.HandlerFunc(h.profiles))
	mux.Handle("POST "+p+"/profiles", httpx.HandlerFunc(h.createProfile))
	mux.Handle("GET "+p+"/profiles/{id}", httpx.HandlerFunc(h.profile))
	mux.Handle("PATCH "+p+"/profiles/{id}", httpx.HandlerFunc(h.updateProfile))
	mux.Handle("GET "+p+"/settings", httpx.HandlerFunc(h.settings))
	mux.Handle("POST "+p+"/settings", httpx.HandlerFunc(h.changeSettings))
	mux.Handle("GET "+p+"/periods", httpx.HandlerFunc(h.periods))
	mux.Handle("GET "+p+"/worksheet", httpx.HandlerFunc(h.worksheet))
	mux.Handle("GET "+p+"/returns", httpx.HandlerFunc(h.returns))
	mux.Handle("POST "+p+"/returns", httpx.HandlerFunc(h.fileReturn))
	mux.Handle("GET "+p+"/returns/{id}", httpx.HandlerFunc(h.taxReturn))
	mux.Handle("POST "+p+"/returns/{id}/void", httpx.HandlerFunc(h.voidReturn))
	mux.Handle("POST "+p+"/returns/{id}/payments", httpx.HandlerFunc(h.pay))
	mux.Handle("GET "+p+"/payments", httpx.HandlerFunc(h.payments))
	mux.Handle("GET "+p+"/payments/{id}", httpx.HandlerFunc(h.payment))
	mux.Handle("POST "+p+"/payments/{id}/void", httpx.HandlerFunc(h.voidPayment))
	mux.Handle("GET "+p+"/liability", httpx.HandlerFunc(h.liability))
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

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func (h *Handler) profiles(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Profiles(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Profile]{Data: list})
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errProfileNotFound())
	if err != nil {
		return err
	}
	pr, err := h.svc.GetProfile(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pr)
}

func (h *Handler) createProfile(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in ProfileInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pr, err := h.svc.CreateProfile(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, pr)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errProfileNotFound())
	if err != nil {
		return err
	}
	var in ProfilePatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pr, err := h.svc.UpdateProfile(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pr)
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Settings(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) changeSettings(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in SettingsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := h.svc.ChangeSettings(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) periods(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	taxID := idQuery(r, "tax_id", &errs)
	if taxID == nil && len(errs) == 0 {
		errs = append(errs, fieldErr("tax_id", "REQUIRED", "the tax"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	list, err := h.svc.Periods(r.Context(), pid, *taxID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Period]{Data: list})
}

func (h *Handler) worksheet(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	taxID, period := idQuery(r, "tax_id", &errs), dateQuery(r, "period", &errs)
	if taxID == nil || period == nil {
		if len(errs) == 0 {
			errs = append(errs, fieldErr("period", "REQUIRED", "tax_id and period (the first day of the month)"))
		}
	}
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	ws, err := h.svc.Worksheet(r.Context(), pid, *taxID, *period)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, ws)
}

func (h *Handler) returns(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := ReturnFilter{TaxID: idQuery(r, "tax_id", &errs), Status: strings.ToUpper(r.URL.Query().Get("status"))}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Returns(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Return]{Data: list})
}

func (h *Handler) taxReturn(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errReturnNotFound())
	if err != nil {
		return err
	}
	ret, err := h.svc.GetReturn(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, ret)
}

func (h *Handler) fileReturn(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in FileInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ret, err := h.svc.FileReturn(r.Context(), pid, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, ret)
}

func (h *Handler) voidReturn(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errReturnNotFound())
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ret, err := h.svc.VoidReturn(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, ret)
}

func (h *Handler) pay(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r, errReturnNotFound())
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in PayInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pay, err := h.svc.PayReturn(r.Context(), pid, id, in, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, pay)
}

func (h *Handler) payments(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := PaymentFilter{ReturnID: idQuery(r, "return_id", &errs), Status: strings.ToUpper(r.URL.Query().Get("status"))}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Payments(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Payment]{Data: list})
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

func (h *Handler) liability(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	asOf := dateQuery(r, "as_of", &errs)
	if len(errs) > 0 {
		return apperr.Invalid("the report parameters are invalid", errs...)
	}
	l, err := h.svc.Liability(r.Context(), pid, asOf)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, l)
}
