package bankrec

import (
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the bank reconciliation API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/bank"
	mux.Handle("GET "+p+"/accounts", httpx.HandlerFunc(h.accounts))
	mux.Handle("POST "+p+"/accounts", httpx.HandlerFunc(h.createAccount))
	mux.Handle("GET "+p+"/accounts/{id}", httpx.HandlerFunc(h.account))
	mux.Handle("PATCH "+p+"/accounts/{id}", httpx.HandlerFunc(h.updateAccount))
	mux.Handle("GET "+p+"/statements", httpx.HandlerFunc(h.statements))
	mux.Handle("POST "+p+"/statements", httpx.HandlerFunc(h.importStatement))
	mux.Handle("GET "+p+"/statements/{id}", httpx.HandlerFunc(h.statement))
	mux.Handle("DELETE "+p+"/statements/{id}", httpx.HandlerFunc(h.deleteStatement))
	mux.Handle("GET "+p+"/statements/{id}/uncleared", httpx.HandlerFunc(h.uncleared))
	mux.Handle("POST "+p+"/statements/{id}/clearings", httpx.HandlerFunc(h.clear))
	mux.Handle("DELETE "+p+"/statements/{id}/clearings/{clearingId}", httpx.HandlerFunc(h.unclear))
	mux.Handle("POST "+p+"/statements/{id}/auto-match", httpx.HandlerFunc(h.autoMatch))
	mux.Handle("POST "+p+"/statements/{id}/lines/{lineId}/adjust", httpx.HandlerFunc(h.adjust))
	mux.Handle("GET "+p+"/statements/{id}/settlement-lines", httpx.HandlerFunc(h.settlementLines))
	mux.Handle("GET "+p+"/statements/{id}/lines/{lineId}/settlement-proposal", httpx.HandlerFunc(h.settlementProposal))
	mux.Handle("POST "+p+"/statements/{id}/lines/{lineId}/settlement-preview", httpx.HandlerFunc(h.settlementPreview))
	mux.Handle("GET "+p+"/card-fee-rules", httpx.HandlerFunc(h.feeRules))
	mux.Handle("POST "+p+"/card-fee-rules", httpx.HandlerFunc(h.createFeeRule))
	mux.Handle("GET "+p+"/card-settlements/expected", httpx.HandlerFunc(h.expectedSettlements))
	mux.Handle("GET "+p+"/card-settlements", httpx.HandlerFunc(h.settlements))
	mux.Handle("POST "+p+"/statements/{id}/lines/{lineId}/settle", httpx.HandlerFunc(h.settle))
	mux.Handle("POST "+p+"/statements/{id}/reconcile", httpx.HandlerFunc(h.reconcile))
	mux.Handle("POST "+p+"/statements/{id}/reopen", httpx.HandlerFunc(h.reopen))
}

func pathID(r *http.Request, name string, notFound *apperr.Error) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound
	}
	return id, nil
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.BankAccounts(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[BankAccount]{Data: list})
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id", errBankAccountNotFound())
	if err != nil {
		return err
	}
	b, err := h.svc.GetBankAccount(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in BankAccountInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.CreateBankAccount(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id", errBankAccountNotFound())
	if err != nil {
		return err
	}
	var in BankAccountPatch
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.UpdateBankAccount(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) statements(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var bankID *int64
	if v := r.URL.Query().Get("bank_account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			return apperr.Invalid("the filter is invalid", fieldErr("bank_account_id", "INVALID_VALUE", "an id"))
		}
		bankID = &id
	}
	list, err := h.svc.Statements(r.Context(), pid, bankID, strings.ToUpper(r.URL.Query().Get("status")))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Statement]{Data: list})
}

func (h *Handler) importStatement(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in ImportInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.ImportStatement(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, d)
}

// statementPath reads the property and the statement of a route.
func statementPath(r *http.Request) (int64, int64, error) {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return 0, 0, err
	}
	id, err := pathID(r, "id", errStatementNotFound())
	return pid, id, err
}

func (h *Handler) statement(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	d, err := h.svc.GetStatement(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) deleteStatement(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteStatement(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) uncleared(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	list, err := h.svc.UnclearedLines(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[UnclearedLine]{Data: list})
}

func (h *Handler) clear(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	var in ClearInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.Clear(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) unclear(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	cid, err := pathID(r, "clearingId", apperr.NotFound("CLEARING_NOT_FOUND", "the matching does not exist in this statement"))
	if err != nil {
		return err
	}
	d, err := h.svc.Unclear(r.Context(), pid, id, cid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) autoMatch(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	res, err := h.svc.AutoMatch(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) adjust(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	lid, err := pathID(r, "lineId", apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement"))
	if err != nil {
		return err
	}
	var in AdjustInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.Adjust(r.Context(), pid, id, lid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) reconcile(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	d, err := h.svc.Reconcile(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) reopen(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	var in ReopenInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.Reopen(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) settlementLines(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	list, err := h.svc.SettlementLines(r.Context(), pid, id, strings.ToUpper(r.URL.Query().Get("account_key")))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[UnclearedLine]{Data: list})
}

func (h *Handler) settle(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	lid, err := pathID(r, "lineId", apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement"))
	if err != nil {
		return err
	}
	var in SettleInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	d, err := h.svc.Settle(r.Context(), pid, id, lid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) settlementPreview(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	lid, err := pathID(r, "lineId", apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement"))
	if err != nil {
		return err
	}
	var in SettleInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	pv, err := h.svc.SettlementPreview(r.Context(), pid, id, lid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pv)
}

func (h *Handler) settlementProposal(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := statementPath(r)
	if err != nil {
		return err
	}
	lid, err := pathID(r, "lineId", apperr.NotFound("STATEMENT_LINE_NOT_FOUND", "the line does not exist in this statement"))
	if err != nil {
		return err
	}
	pr, err := h.svc.SettlementProposal(r.Context(), pid, id, lid, strings.ToUpper(r.URL.Query().Get("account_key")))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, pr)
}

func (h *Handler) feeRules(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	list, err := h.svc.CardFeeRules(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[FeeRule]{Data: list})
}

func (h *Handler) createFeeRule(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in FeeRuleInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	rule, err := h.svc.CreateCardFeeRule(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, rule)
}

func (h *Handler) expectedSettlements(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	key := strings.ToUpper(r.URL.Query().Get("account_key"))
	if key == "" {
		key = KeyCard
	}
	e, err := h.svc.ExpectedSettlements(r.Context(), pid, key)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, e)
}

type settlementCursor struct {
	Before int64 `json:"b"`
}

func (h *Handler) settlements(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	var cur settlementCursor
	if page.Cursor != "" {
		if err := httpx.DecodeCursor(page.Cursor, &cur); err != nil {
			return err
		}
	}
	items, err := h.svc.Settlements(r.Context(), pid, cur.Before, page.Limit+1)
	if err != nil {
		return err
	}
	out := httpx.Page[SettlementRow]{Data: items}
	if len(items) > page.Limit {
		out.Data = items[:page.Limit]
		if out.NextCursor, err = httpx.EncodeCursor(settlementCursor{Before: out.Data[page.Limit-1].ID}); err != nil {
			return err
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, out)
}
