package inventory

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The routes of purchasing (35-inventory.md 10): requests with their approval, orders, receipts, draft bills, returns, the notices and the chains. They are mounted by Register next to the masters.
func (h *Handler) registerPurchasing(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/inventory"
	mux.Handle("GET "+p+"/suppliers", httpx.HandlerFunc(h.listSuppliers))
	mux.Handle("GET "+p+"/supplier-items", httpx.HandlerFunc(h.listSupplierItems))

	mux.Handle("GET "+p+"/requests", httpx.HandlerFunc(h.listRequests))
	mux.Handle("POST "+p+"/requests", httpx.HandlerFunc(h.createRequest))
	mux.Handle("GET "+p+"/requests/{id}", httpx.HandlerFunc(h.getRequest))
	mux.Handle("PUT "+p+"/requests/{id}", httpx.HandlerFunc(h.updateRequest))
	mux.Handle("POST "+p+"/requests/{id}/submit", httpx.HandlerFunc(h.submitRequest))
	mux.Handle("POST "+p+"/requests/{id}/recall", httpx.HandlerFunc(h.recallRequest))
	mux.Handle("POST "+p+"/requests/{id}/cancel", httpx.HandlerFunc(h.cancelRequest))
	mux.Handle("POST "+p+"/requests/{id}/approve", httpx.HandlerFunc(h.approveRequest))
	mux.Handle("POST "+p+"/requests/{id}/return", httpx.HandlerFunc(h.returnRequest))
	mux.Handle("POST "+p+"/requests/{id}/purchase-orders", httpx.HandlerFunc(h.ordersFromRequest))

	mux.Handle("GET "+p+"/approvals/inbox", httpx.HandlerFunc(h.inbox))
	mux.Handle("GET "+p+"/notices", httpx.HandlerFunc(h.notices))
	mux.Handle("POST "+p+"/notices/{id}/seen", httpx.HandlerFunc(h.noticeSeen))
	mux.Handle("POST "+p+"/notices/seen-all", httpx.HandlerFunc(h.noticesSeenAll))
	mux.Handle("GET "+p+"/approval-chains/{doc}", httpx.HandlerFunc(h.getChain))
	mux.Handle("PUT "+p+"/approval-chains/{doc}", httpx.HandlerFunc(h.putChain))

	mux.Handle("GET "+p+"/purchase-orders", httpx.HandlerFunc(h.listPOs))
	mux.Handle("POST "+p+"/purchase-orders", httpx.HandlerFunc(h.createPO))
	mux.Handle("GET "+p+"/purchase-orders/{id}", httpx.HandlerFunc(h.getPO))
	mux.Handle("POST "+p+"/purchase-orders/{id}/send", httpx.HandlerFunc(h.sendPO))
	mux.Handle("POST "+p+"/purchase-orders/{id}/cancel", httpx.HandlerFunc(h.cancelPO))
	mux.Handle("POST "+p+"/purchase-orders/{id}/close", httpx.HandlerFunc(h.closePO))

	mux.Handle("GET "+p+"/receipts", httpx.HandlerFunc(h.listReceipts))
	mux.Handle("POST "+p+"/receipts", httpx.HandlerFunc(h.postReceipt))
	mux.Handle("GET "+p+"/receipts/{id}", httpx.HandlerFunc(h.getReceipt))
	mux.Handle("POST "+p+"/receipts/{id}/reverse", httpx.HandlerFunc(h.reverseReceipt))

	mux.Handle("GET "+p+"/bill-drafts", httpx.HandlerFunc(h.listDrafts))
	mux.Handle("GET "+p+"/bill-drafts/{id}", httpx.HandlerFunc(h.getDraft))
	mux.Handle("POST "+p+"/bill-drafts/preview", httpx.HandlerFunc(h.previewBill))
	mux.Handle("POST "+p+"/bill-drafts/confirm", httpx.HandlerFunc(h.confirmBill))
	mux.Handle("POST "+p+"/bill-drafts/{id}/discard", httpx.HandlerFunc(h.discardDraft))
	mux.Handle("POST "+p+"/bills/{id}/unmatch", httpx.HandlerFunc(h.unmatchBill))

	mux.Handle("GET "+p+"/returns", httpx.HandlerFunc(h.listReturns))
	mux.Handle("POST "+p+"/returns", httpx.HandlerFunc(h.postReturn))
	mux.Handle("GET "+p+"/returns/{id}", httpx.HandlerFunc(h.getReturn))
	mux.Handle("POST "+p+"/returns/{id}/link-credit-note", httpx.HandlerFunc(h.linkCredit))
}

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	return key, nil
}

func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// jsonOf runs a use case of a property and writes its answer.
func jsonOf[T any](w http.ResponseWriter, r *http.Request, status int, run func(pid int64) (T, error)) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	v, err := run(pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, status, v)
}

// withID runs a use case of a property and a resource id.
func withID[T any](w http.ResponseWriter, r *http.Request, status int, run func(pid, id int64) (T, error)) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	v, err := run(pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, status, v)
}

// withBody runs a use case of a property, a resource id and a JSON body.
func withBody[B, T any](w http.ResponseWriter, r *http.Request, status int, run func(pid, id int64, in B) (T, error)) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[B](w, r)
	if err != nil {
		return err
	}
	v, err := run(pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, status, v)
}

func (h *Handler) listSuppliers(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	active, err := queryBool(r, "active")
	if err != nil {
		return err
	}
	list, err := h.svc.ListSuppliers(r.Context(), pid, active)
	return page(w, list, err)
}

func (h *Handler) listSupplierItems(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	sup, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	item, err := queryInt(r, "item_id")
	if err != nil {
		return err
	}
	list, err := h.svc.ListSupplierItems(r.Context(), pid, sup, item)
	return page(w, list, err)
}

// ------------------------------------------------------------------------------------------------ requests

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	mine, err := queryBool(r, "mine")
	if err != nil {
		return err
	}
	f := RequestFilter{Status: r.URL.Query().Get("status"), Q: strings.TrimSpace(r.URL.Query().Get("q")), Mine: mine != nil && *mine, Limit: queryLimit(r)}
	list, err := h.svc.ListRequests(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[RequestInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Request, error) { return h.svc.CreateRequest(r.Context(), pid, in) })
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Request, error) { return h.svc.GetRequest(r.Context(), pid, id) })
}

func (h *Handler) updateRequest(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in RequestInput) (Request, error) {
		return h.svc.UpdateRequest(r.Context(), pid, id, in)
	})
}

func (h *Handler) submitRequest(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Request, error) { return h.svc.SubmitRequest(r.Context(), pid, id) })
}

func (h *Handler) recallRequest(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Request, error) { return h.svc.RecallRequest(r.Context(), pid, id) })
}

func (h *Handler) cancelRequest(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Request, error) { return h.svc.CancelRequest(r.Context(), pid, id) })
}

func (h *Handler) approveRequest(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ApproveInput) (Request, error) {
		return h.svc.ApproveRequest(r.Context(), pid, id, in)
	})
}

func (h *Handler) returnRequest(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in ReturnRequestInput) (Request, error) {
		return h.svc.ReturnRequest(r.Context(), pid, id, in)
	})
}

func (h *Handler) ordersFromRequest(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	in, err := decode[POFromRequestInput](w, r)
	if err != nil {
		return err
	}
	list, err := h.svc.CreateOrdersFromRequest(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, httpx.Page[PurchaseOrder]{Data: list})
}

// ------------------------------------------------------------------------------------------------ the approvals

func (h *Handler) inbox(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.Inbox(r.Context(), pid)
	return page(w, list, err)
}

func (h *Handler) notices(w http.ResponseWriter, r *http.Request) error {
	return jsonOf(w, r, http.StatusOK, func(pid int64) (NoticeList, error) { return h.svc.Notices(r.Context(), pid) })
}

func (h *Handler) noticeSeen(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := idsOf(r)
	if err != nil {
		return err
	}
	if err := h.svc.MarkNoticeSeen(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) noticesSeenAll(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if err := h.svc.MarkNoticeSeen(r.Context(), pid, 0); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) getChain(w http.ResponseWriter, r *http.Request) error {
	doc := strings.ToUpper(r.PathValue("doc"))
	return jsonOf(w, r, http.StatusOK, func(pid int64) (ApprovalChain, error) { return h.svc.GetApprovalChain(r.Context(), pid, doc) })
}

func (h *Handler) putChain(w http.ResponseWriter, r *http.Request) error {
	doc := strings.ToUpper(r.PathValue("doc"))
	in, err := decode[ApprovalChainInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusOK, func(pid int64) (ApprovalChain, error) { return h.svc.SetApprovalChain(r.Context(), pid, doc, in) })
}

// ------------------------------------------------------------------------------------------------ orders

func (h *Handler) listPOs(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	sup, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	open, err := queryBool(r, "open")
	if err != nil {
		return err
	}
	f := POFilter{Status: r.URL.Query().Get("status"), SupplierID: sup, OpenOnly: open != nil && *open, Q: strings.TrimSpace(r.URL.Query().Get("q")), Limit: queryLimit(r)}
	list, err := h.svc.ListPurchaseOrders(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) createPO(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[POInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (PurchaseOrder, error) { return h.svc.CreatePurchaseOrder(r.Context(), pid, in) })
}

func (h *Handler) getPO(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (PurchaseOrder, error) { return h.svc.GetPurchaseOrder(r.Context(), pid, id) })
}

func (h *Handler) sendPO(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (PurchaseOrder, error) { return h.svc.SendPurchaseOrder(r.Context(), pid, id) })
}

func (h *Handler) cancelPO(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (PurchaseOrder, error) { return h.svc.CancelPurchaseOrder(r.Context(), pid, id) })
}

func (h *Handler) closePO(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (PurchaseOrder, error) { return h.svc.ClosePurchaseOrder(r.Context(), pid, id) })
}

// ------------------------------------------------------------------------------------------------ receipts

func (h *Handler) listReceipts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	sup, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	po, err := queryInt(r, "po_id")
	if err != nil {
		return err
	}
	f := ReceiptFilter{Status: r.URL.Query().Get("status"), SupplierID: sup, POID: po, Q: strings.TrimSpace(r.URL.Query().Get("q")), Limit: queryLimit(r)}
	list, err := h.svc.ListReceipts(r.Context(), pid, f)
	return page(w, list, err)
}

func (h *Handler) postReceipt(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[ReceiptInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Receipt, error) { return h.svc.PostReceipt(r.Context(), pid, in) })
}

func (h *Handler) getReceipt(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Receipt, error) { return h.svc.GetReceipt(r.Context(), pid, id) })
}

type reasonInput struct {
	Reason string `json:"reason"`
}

func (h *Handler) reverseReceipt(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in reasonInput) (Receipt, error) {
		return h.svc.ReverseReceipt(r.Context(), pid, id, in.Reason)
	})
}

// ------------------------------------------------------------------------------------------------ bills

func (h *Handler) listDrafts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	sup, err := queryInt(r, "supplier_id")
	if err != nil {
		return err
	}
	open, err := queryBool(r, "open")
	if err != nil {
		return err
	}
	list, err := h.svc.ListBillDrafts(r.Context(), pid, DraftFilter{Status: r.URL.Query().Get("status"), SupplierID: sup, OpenOnly: open != nil && *open, Limit: queryLimit(r)})
	return page(w, list, err)
}

func (h *Handler) getDraft(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (BillDraft, error) { return h.svc.GetBillDraft(r.Context(), pid, id) })
}

func (h *Handler) discardDraft(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (BillDraft, error) { return h.svc.DiscardBillDraft(r.Context(), pid, id) })
}

func (h *Handler) previewBill(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[ConfirmBillInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusOK, func(pid int64) (BillPreview, error) { return h.svc.PreviewBill(r.Context(), pid, in) })
}

func (h *Handler) confirmBill(w http.ResponseWriter, r *http.Request) error {
	key, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	in, err := decode[ConfirmBillInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (ConfirmResult, error) { return h.svc.ConfirmBill(r.Context(), pid, in, key) })
}

func (h *Handler) unmatchBill(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in UnmatchInput) (BillDraftBrief, error) {
		return h.svc.UnmatchBill(r.Context(), pid, id, in)
	})
}

// ------------------------------------------------------------------------------------------------ returns

func (h *Handler) listReturns(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	rc, err := queryInt(r, "receipt_id")
	if err != nil {
		return err
	}
	list, err := h.svc.ListReturns(r.Context(), pid, rc)
	return page(w, list, err)
}

func (h *Handler) postReturn(w http.ResponseWriter, r *http.Request) error {
	in, err := decode[ReturnInput](w, r)
	if err != nil {
		return err
	}
	return jsonOf(w, r, http.StatusCreated, func(pid int64) (Return, error) { return h.svc.PostReturn(r.Context(), pid, in) })
}

func (h *Handler) getReturn(w http.ResponseWriter, r *http.Request) error {
	return withID(w, r, http.StatusOK, func(pid, id int64) (Return, error) { return h.svc.GetReturn(r.Context(), pid, id) })
}

type linkInput struct {
	CreditNoteID int64 `json:"credit_note_id"`
}

func (h *Handler) linkCredit(w http.ResponseWriter, r *http.Request) error {
	return withBody(w, r, http.StatusOK, func(pid, id int64, in linkInput) (Return, error) {
		return h.svc.LinkCreditNote(r.Context(), pid, id, in.CreditNoteID)
	})
}
