package accounting

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the accounting API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/accounting"
	mux.Handle("GET "+p+"/accounts", httpx.HandlerFunc(h.accounts))
	mux.Handle("POST "+p+"/accounts", httpx.HandlerFunc(h.createAccount))
	mux.Handle("POST "+p+"/accounts/import", httpx.HandlerFunc(h.importAccounts))
	mux.Handle("GET "+p+"/accounts/{id}", httpx.HandlerFunc(h.account))
	mux.Handle("PATCH "+p+"/accounts/{id}", httpx.HandlerFunc(h.updateAccount))
	mux.Handle("DELETE "+p+"/accounts/{id}", httpx.HandlerFunc(h.deleteAccount))
	mux.Handle("GET "+p+"/account-map", httpx.HandlerFunc(h.accountMap))
	mux.Handle("PUT "+p+"/account-map", httpx.HandlerFunc(h.setAccountMap))
	mux.Handle("GET "+p+"/unmapped", httpx.HandlerFunc(h.unmapped))
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errAccountNotFound()
	}
	return propertyID, id, nil
}

func boolQuery(r *http.Request, name string, errs *[]apperr.FieldError) *bool {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "true or false"))
		return nil
	}
	return &b
}

// writeCSV answers rows as a download. Text that a spreadsheet could read as a formula is prefixed with an apostrophe.
func writeCSV(w http.ResponseWriter, filename string, rows [][]string) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	out := csv.NewWriter(w)
	for _, row := range rows {
		safe := make([]string, len(row))
		for i, c := range row {
			if c != "" && strings.ContainsRune("=+@\t\r", rune(c[0])) || strings.HasPrefix(c, "-") && !isNumber(c) {
				c = "'" + c
			}
			safe[i] = c
		}
		if err := out.Write(safe); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("format") == "csv" {
		rows, err := h.svc.ExportCSV(r.Context(), pid)
		if err != nil {
			return err
		}
		return writeCSV(w, "chart-of-accounts.csv", rows)
	}
	var errs []apperr.FieldError
	q := r.URL.Query()
	f := AccountFilter{
		AccountType: strings.ToUpper(q.Get("account_type")), StatementGroup: strings.ToUpper(q.Get("statement_group")), Query: q.Get("q"),
		Active: boolQuery(r, "active", &errs), Postable: boolQuery(r, "postable", &errs),
	}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.Accounts(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Account]{Data: list})
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	a, err := h.svc.Account(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in AccountInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	a, err := h.svc.CreateAccount(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var patch AccountPatch
	if err := httpx.DecodeJSON(w, r, &patch); err != nil {
		return err
	}
	a, err := h.svc.UpdateAccount(r.Context(), pid, id, patch)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteAccount(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) importAccounts(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in struct {
		CSV    string `json:"csv"`
		DryRun bool   `json:"dry_run"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.svc.ImportCSV(r.Context(), pid, in.CSV, in.DryRun)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) accountMap(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	m, err := h.svc.AccountMap(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[MapEntry]{Data: m})
}

func (h *Handler) setAccountMap(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in struct {
		Entries []MapInput `json:"entries"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	m, err := h.svc.SetAccountMap(r.Context(), pid, in.Entries)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[MapEntry]{Data: m})
}

func (h *Handler) unmapped(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.UnmappedCodes(r.Context(), pid)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}
