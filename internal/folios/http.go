package folios

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
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
	mux.Handle("POST "+p+"/folio-items/{id}/transfer", httpx.HandlerFunc(h.transferItem))
	mux.Handle("PATCH "+p+"/folio-items/{id}/group", httpx.HandlerFunc(h.itemGroup))
	h.registerTransfers(mux)
	h.registerMaster(mux)
	mux.Handle("PATCH "+p+"/payments/{id}/group", httpx.HandlerFunc(h.paymentGroup))
	mux.Handle("GET "+p+"/payments", httpx.HandlerFunc(h.listPayments))
	mux.Handle("POST "+p+"/payments/{id}/void", httpx.HandlerFunc(h.void))
	mux.Handle("POST "+p+"/payments/{id}/refunds", httpx.HandlerFunc(h.refund))
	mux.Handle("POST "+p+"/reservations/{id}/deposits", httpx.HandlerFunc(h.deposit))
	mux.Handle("POST "+p+"/reservations/{id}/fees", httpx.HandlerFunc(h.fee))
	mux.Handle("GET "+p+"/reservations/{id}/rooms/{lineId}/billing-instructions", httpx.HandlerFunc(h.getInstructions))
	mux.Handle("PUT "+p+"/reservations/{id}/rooms/{lineId}/billing-instructions", httpx.HandlerFunc(h.putInstructions))
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
	f := FolioFilter{ReservationID: queryID(r, "reservation_id", &errs), StayID: queryID(r, "stay_id", &errs), BookingGroupID: queryID(r, "booking_group_id", &errs), FolioType: r.URL.Query().Get("folio_type"), Status: r.URL.Query().Get("status"), CancelledWithBalance: r.URL.Query().Get("cancelled_with_balance") == "true"}
	if f.Status != "" && f.Status != "OPEN" && f.Status != "CLOSED" {
		errs = append(errs, fieldErr("status", "INVALID_VALUE", "OPEN or CLOSED"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	order, err := FolioSortSpec.Parse(r)
	if err != nil {
		return err
	}
	pos, err := listsort.Decode(page.Cursor, order)
	if err != nil {
		return err
	}
	res, err := h.svc.ListFolios(r.Context(), pid, f, order, pos, page.Limit)
	if err != nil {
		return err
	}
	out := httpx.Page[FolioSummary]{Data: res.Data}
	if out.Data == nil {
		out.Data = []FolioSummary{}
	}
	if res.Next != nil {
		if out.NextCursor, err = listsort.Encode(order, *res.Next); err != nil {
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

func (h *Handler) transferItem(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in TransferItemInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.TransferItem(r.Context(), pid, id, in)
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
	order, err := PaymentSortSpec.Parse(r)
	if err != nil {
		return err
	}
	pos, err := listsort.Decode(page.Cursor, order)
	if err != nil {
		return err
	}
	res, err := h.svc.ListPayments(r.Context(), pid, f, order, pos, page.Limit)
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
	if res.Next != nil {
		if out.NextCursor, err = listsort.Encode(order, *res.Next); err != nil {
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

func lineIDs(r *http.Request) (propertyID, reservationID, lineID int64, err error) {
	if propertyID, reservationID, err = ids(r); err != nil {
		return 0, 0, 0, err
	}
	lineID, err = strconv.ParseInt(r.PathValue("lineId"), 10, 64)
	if err != nil || lineID < 1 {
		return 0, 0, 0, apperr.NotFound("NOT_FOUND", "no such resource")
	}
	return propertyID, reservationID, lineID, nil
}

func (h *Handler) getInstructions(w http.ResponseWriter, r *http.Request) error {
	pid, res, line, err := lineIDs(r)
	if err != nil {
		return err
	}
	out, err := h.svc.GetBillingInstructions(r.Context(), pid, res, line)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) putInstructions(w http.ResponseWriter, r *http.Request) error {
	pid, res, line, err := lineIDs(r)
	if err != nil {
		return err
	}
	var in setInstructionsRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	out, err := h.svc.SetBillingInstructions(r.Context(), pid, res, line, in.Instructions)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}

type setInstructionsRequest struct {
	Instructions []InstructionInput `json:"instructions"`
}

func (h *Handler) itemGroup(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in GroupInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.SetItemGroup(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) paymentGroup(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in GroupInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.SetPaymentGroup(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) fee(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in FeeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.PostReservationFee(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, res)
}
