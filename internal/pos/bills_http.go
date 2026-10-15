package pos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// registerBills mounts the routes of the bills (docs/architecture/06-api.md 24). They belong to the POS module of the plan.
func (h *Handler) registerBills(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/pos"
	mux.Handle("GET "+p+"/outlets/{id}/floor", httpx.HandlerFunc(h.floor))
	mux.Handle("GET "+p+"/outlets/{id}/catalog", httpx.HandlerFunc(h.catalog))
	mux.Handle("GET "+p+"/outlets/{id}/bills", httpx.HandlerFunc(h.listBills))
	mux.Handle("POST "+p+"/outlets/{id}/bills", httpx.HandlerFunc(h.openBill))
	mux.Handle("POST "+p+"/outlets/{id}/holds", httpx.HandlerFunc(h.createHold))
	mux.Handle("POST "+p+"/holds/{id}/seat", httpx.HandlerFunc(h.seatHold))
	mux.Handle("POST "+p+"/holds/{id}/cancel", httpx.HandlerFunc(h.cancelHold))
	mux.Handle("POST "+p+"/outlets/{id}/drawer/open", httpx.HandlerFunc(h.openDrawer))
	mux.Handle("GET "+p+"/outlets/{id}/room-lookup", httpx.HandlerFunc(h.roomLookup))
	mux.Handle("GET "+p+"/outlets/{id}/report", httpx.HandlerFunc(h.report))
	mux.Handle("POST "+p+"/price-quote", httpx.HandlerFunc(h.priceQuote))

	mux.Handle("GET "+p+"/bills/{id}", httpx.HandlerFunc(h.getBill))
	mux.Handle("PUT "+p+"/bills/{id}", httpx.HandlerFunc(h.updateBill))
	mux.Handle("POST "+p+"/bills/{id}/lines", httpx.HandlerFunc(h.addLine))
	mux.Handle("PUT "+p+"/bills/{id}/lines/{lineId}", httpx.HandlerFunc(h.updateLine))
	mux.Handle("DELETE "+p+"/bills/{id}/lines/{lineId}", httpx.HandlerFunc(h.deleteLine))
	mux.Handle("POST "+p+"/bills/{id}/lines/{lineId}/void", httpx.HandlerFunc(h.voidLine))
	mux.Handle("POST "+p+"/bills/{id}/send", httpx.HandlerFunc(h.sendBill))
	mux.Handle("POST "+p+"/bills/{id}/discount", httpx.HandlerFunc(h.discount))
	mux.Handle("POST "+p+"/bills/{id}/comp", httpx.HandlerFunc(h.comp))
	mux.Handle("POST "+p+"/bills/{id}/request", httpx.HandlerFunc(h.requestBill))
	mux.Handle("POST "+p+"/bills/{id}/pay", httpx.HandlerFunc(h.payBill))
	mux.Handle("POST "+p+"/bills/{id}/split", httpx.HandlerFunc(h.splitBill))
	mux.Handle("POST "+p+"/bills/{id}/merge", httpx.HandlerFunc(h.mergeBill))
	mux.Handle("POST "+p+"/bills/{id}/void", httpx.HandlerFunc(h.voidBill))
	mux.Handle("POST "+p+"/bills/{id}/reopen", httpx.HandlerFunc(h.reopenBill))
	mux.Handle("POST "+p+"/room-charges/{id}/return", httpx.HandlerFunc(h.returnRoomCharge))
	mux.Handle("POST "+p+"/room-charges/{id}/move", httpx.HandlerFunc(h.moveRoomCharge))
}

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func (h *Handler) floor(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Floor(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

// catalog answers the catalogue of an outlet with an ETag: a client that holds the version asks If-None-Match and gets 304 (offline readiness, 34-pos.md 12).
func (h *Handler) catalog(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := h.svc.Catalog(r.Context(), pid, id)
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) listBills(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	f := BillFilter{Status: strings.TrimSpace(r.URL.Query().Get("status")), Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if v := strings.TrimSpace(r.URL.Query().Get("table_id")); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("table_id", "INVALID_VALUE", "a table id"))
		}
		f.TableID = &n
	}
	if v := strings.TrimSpace(r.URL.Query().Get("date")); v != "" {
		d, perr := civil.ParseDate(v)
		if perr != nil {
			return apperr.Invalid("the filter is invalid", fieldErr("date", "INVALID_VALUE", "YYYY-MM-DD"))
		}
		f.Day = &d
	}
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("limit", "INVALID_VALUE", "a number"))
		}
		f.Limit = n
	}
	list, err := h.svc.ListBills(r.Context(), pid, id, f)
	return page(w, list, err)
}

func (h *Handler) openBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[OpenBillInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.OpenBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) createHold(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[HoldInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.CreateHold(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (h *Handler) seatHold(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	if err := h.svc.ReleaseHold(r.Context(), pid, id, true); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) cancelHold(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	if err := h.svc.ReleaseHold(r.Context(), pid, id, false); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) openDrawer(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[struct {
		Reason string `json:"reason"`
	}](w, r)
	if err != nil {
		return err
	}
	if err := h.svc.OpenDrawer(r.Context(), pid, id, in.Reason); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) roomLookup(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	list, err := h.svc.RoomLookup(r.Context(), pid, id, r.URL.Query().Get("q"))
	return page(w, list, err)
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	from, err := civil.ParseDate(r.URL.Query().Get("from"))
	if err != nil {
		return apperr.Invalid("the range is invalid", fieldErr("from", "INVALID_VALUE", "YYYY-MM-DD"))
	}
	to, err := civil.ParseDate(r.URL.Query().Get("to"))
	if err != nil {
		return apperr.Invalid("the range is invalid", fieldErr("to", "INVALID_VALUE", "YYYY-MM-DD"))
	}
	v, err := h.svc.Report(r.Context(), pid, id, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) priceQuote(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	in, err := decode[QuoteInput](w, r)
	if err != nil {
		return err
	}
	v, err := h.svc.PriceQuote(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handler) getBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	b, err := h.svc.GetBill(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) updateBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[UpdateBillInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.UpdateBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) addLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[AddLineInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.AddLine(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) updateLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	lineID, err := pathInt(r, "lineId")
	if err != nil {
		return err
	}
	in, err := decode[UpdateLineInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.UpdateLine(r.Context(), pid, id, lineID, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) deleteLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	lineID, err := pathInt(r, "lineId")
	if err != nil {
		return err
	}
	b, err := h.svc.DeleteLine(r.Context(), pid, id, lineID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) voidLine(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	lineID, err := pathInt(r, "lineId")
	if err != nil {
		return err
	}
	in, err := decode[VoidLineInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.VoidLine(r.Context(), pid, id, lineID, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) sendBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[SendInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.SendBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) discount(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[DiscountInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.ApplyDiscount(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) comp(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[CompInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.CompBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) requestBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	b, err := h.svc.RequestBill(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) payBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	in, err := decode[PayInput](w, r)
	if err != nil {
		return err
	}
	res, err := h.svc.PayBill(r.Context(), pid, id, key, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) splitBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[SplitInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.SplitBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) mergeBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[MergeInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.MergeBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) voidBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	b, err := h.svc.VoidBill(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) reopenBill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[ReopenInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.ReopenBill(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) returnRoomCharge(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[RoomChargeCorrectionInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.ReturnRoomCharge(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) moveRoomCharge(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[RoomChargeCorrectionInput](w, r)
	if err != nil {
		return err
	}
	b, err := h.svc.MoveRoomCharge(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}
