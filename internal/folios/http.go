package folios

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the folio and payment API (docs/architecture/06-api.md §14).
type Handler struct{ svc *Service }

// NewHandler returns the folios HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/folios", httpx.HandlerFunc(h.list))
	mux.Handle("GET "+p+"/folios/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("POST "+p+"/folios/{id}/charges", httpx.HandlerFunc(h.charge))
	mux.Handle("POST "+p+"/folios/{id}/adjustments", httpx.HandlerFunc(h.adjust))
	mux.Handle("POST "+p+"/folios/{id}/payments", httpx.HandlerFunc(h.pay))
	mux.Handle("POST "+p+"/folios/{id}/city-ledger-transfers", httpx.HandlerFunc(h.transfer))
	mux.Handle("POST "+p+"/folios/{id}/close", httpx.HandlerFunc(h.closeFolio))
	mux.Handle("POST "+p+"/folio-items/{id}/reverse", httpx.HandlerFunc(h.reverse))
	mux.Handle("GET "+p+"/payments", httpx.HandlerFunc(h.listPayments))
	mux.Handle("POST "+p+"/payments/{id}/void", httpx.HandlerFunc(h.void))
	mux.Handle("POST "+p+"/payments/{id}/refunds", httpx.HandlerFunc(h.refund))
	mux.Handle("POST "+p+"/reservations/{id}/deposits", httpx.HandlerFunc(h.deposit))
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return id, nil
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = pathID(r)
	return propertyID, id, err
}

// idempotencyKey reads the required Idempotency-Key header.
func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func queryID(r *http.Request, name string, errs *[]apperr.FieldError) *int64 {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "a positive integer"))
		return nil
	}
	return &id
}

type idCursor struct {
	Last int64 `json:"l"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := FolioFilter{ReservationID: queryID(r, "reservation_id", &errs), StayID: queryID(r, "stay_id", &errs), Status: r.URL.Query().Get("status")}
	if f.Status != "" && f.Status != "OPEN" && f.Status != "CLOSED" {
		errs = append(errs, fieldErr("status", "INVALID_VALUE", "OPEN or CLOSED"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.ListFolios(r.Context(), pid, f, cur.Last, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[FolioSummary]{Data: items}
	if out.Data == nil {
		out.Data = []FolioSummary{}
	}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Last: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	f, err := h.svc.GetFolio(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *Handler) charge(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in ChargeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.PostCharge(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) adjust(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in AdjustmentInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.PostAdjustment(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) reverse(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in CorrectionInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Reverse(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) pay(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
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
	res, err := h.svc.PostPayment(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) deposit(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
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
	res, err := h.svc.Deposit(r.Context(), pid, id, key, in)
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
	var in CorrectionInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Void(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) refund(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in RefundInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Refund(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}

type closeRequest struct {
	Version int32 `json:"version"`
}

func (h *Handler) closeFolio(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in closeRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f, err := h.svc.CloseFolio(r.Context(), pid, id, in.Version)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	f := PaymentFilter{Method: r.URL.Query().Get("method")}
	if s := r.URL.Query().Get("business_date"); s != "" {
		d, err := civil.ParseDate(s)
		if err != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("business_date", "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		f.BusinessDate = &d
	}
	var cur idCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	res, err := h.svc.ListPayments(r.Context(), pid, f, cur.Last, page.Limit+1)
	if err != nil {
		return err
	}
	type listBody struct {
		Data       []Payment     `json:"data"`
		Totals     []MethodTotal `json:"totals"`
		NextCursor string        `json:"next_cursor,omitempty"`
	}
	out := listBody{Data: res.Data, Totals: res.Totals}
	if out.Totals == nil {
		out.Totals = []MethodTotal{}
	}
	if len(res.Data) > page.Limit {
		out.Data = res.Data[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(idCursor{Last: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	var in TransferInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.Transfer(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}
