package accounting

import (
	"net/http"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// RegisterReports mounts the report routes. Each answers CSV with `format=csv`.
func (h *Handler) RegisterReports(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/accounting"
	mux.Handle("GET "+p+"/trial-balance", httpx.HandlerFunc(h.trialBalance))
	mux.Handle("GET "+p+"/accounts/{id}/ledger", httpx.HandlerFunc(h.ledger))
	mux.Handle("GET "+p+"/income-statement", httpx.HandlerFunc(h.incomeStatement))
	mux.Handle("GET "+p+"/balance-sheet", httpx.HandlerFunc(h.balanceSheet))
	mux.Handle("GET "+p+"/cash-flow", httpx.HandlerFunc(h.cashFlow))
	mux.Handle("GET "+p+"/reconciliation", httpx.HandlerFunc(h.reconciliation))
}

// reportQuery reads the range (from, to) or the date (as_of) of a report request.
func reportQuery(r *http.Request) (from, to, asOf *civil.Date, err error) {
	var errs []apperr.FieldError
	from, to, asOf = dateQuery(r, "from", &errs), dateQuery(r, "to", &errs), dateQuery(r, "as_of", &errs)
	if len(errs) > 0 {
		return nil, nil, nil, apperr.Invalid("the report parameters are invalid", errs...)
	}
	return from, to, asOf, nil
}

func wantsCSV(r *http.Request) bool { return r.URL.Query().Get("format") == "csv" }

func (h *Handler) trialBalance(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, _, err := reportQuery(r)
	if err != nil {
		return err
	}
	tb, err := h.svc.TrialBalance(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if wantsCSV(r) {
		rows := [][]string{{"code", "name", "type", "opening_debit", "opening_credit", "debit", "credit", "closing_debit", "closing_credit"}}
		for _, x := range tb.Rows {
			rows = append(rows, []string{x.Code, x.Name, x.AccountType, x.OpeningDebit.String(), x.OpeningCredit.String(), x.Debit.String(), x.Credit.String(), x.ClosingDebit.String(), x.ClosingCredit.String()})
		}
		t := tb.Totals
		rows = append(rows, []string{"", "Total", "", t.OpeningDebit.String(), t.OpeningCredit.String(), t.Debit.String(), t.Credit.String(), t.ClosingDebit.String(), t.ClosingCredit.String()})
		rows[0] = csvlang.Header(r, rows[0])
		rows[len(rows)-1][1] = csvlang.Word(r, "Total")
		return writeCSV(w, "trial-balance-"+tb.To.String()+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, tb)
}

func (h *Handler) ledger(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	from, to, _, err := reportQuery(r)
	if err != nil {
		return err
	}
	gl, err := h.svc.GeneralLedger(r.Context(), pid, id, from, to)
	if err != nil {
		return err
	}
	if wantsCSV(r) {
		rows := [][]string{{"date", "journal", "type", "description", "source_type", "source_ref", "debit", "credit", "balance"}}
		rows = append(rows, []string{gl.From.String(), "", "", "Opening balance", "", "", "", "", gl.Opening.String()})
		for _, x := range gl.Lines {
			rows = append(rows, []string{x.Date.String(), x.JournalNumber, x.JournalType, x.Description, x.SourceType, x.SourceRef, x.Debit.String(), x.Credit.String(), x.Balance.String()})
		}
		rows[0] = csvlang.Header(r, rows[0])
		rows[1][3] = csvlang.Word(r, "Opening balance")
		return writeCSV(w, "ledger-"+gl.Account.Code+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, gl)
}

func statementCSV(lines []StatementLine) [][]string {
	rows := [][]string{{"line", "code", "name", "amount"}}
	for _, l := range lines {
		switch l.Kind {
		case "HEADING":
			rows = append(rows, []string{l.Title, "", "", ""})
		case "GROUP":
			for _, a := range l.Accounts {
				rows = append(rows, []string{l.Title, a.Code, a.Name, a.Amount.String()})
			}
			if len(l.Accounts) != 1 {
				rows = append(rows, []string{"Total " + l.Title, "", "", l.Amount.String()})
			}
		default:
			rows = append(rows, []string{l.Title, "", "", l.Amount.String()})
		}
	}
	return rows
}

func (h *Handler) incomeStatement(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, _, err := reportQuery(r)
	if err != nil {
		return err
	}
	is, err := h.svc.IncomeStatement(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if wantsCSV(r) {
		rows := statementCSV(is.Lines)
		rows[0] = csvlang.Header(r, rows[0])
		return writeCSV(w, "income-statement-"+is.To.String()+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, is)
}

func (h *Handler) balanceSheet(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	_, _, asOf, err := reportQuery(r)
	if err != nil {
		return err
	}
	bs, err := h.svc.BalanceSheet(r.Context(), pid, asOf)
	if err != nil {
		return err
	}
	if wantsCSV(r) {
		rows := statementCSV(bs.Lines)
		rows[0] = csvlang.Header(r, rows[0])
		return writeCSV(w, "balance-sheet-"+bs.AsOf.String()+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, bs)
}

func (h *Handler) reconciliation(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	_, _, asOf, err := reportQuery(r)
	if err != nil {
		return err
	}
	rec, err := h.svc.Reconciliation(r.Context(), pid, asOf)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handler) cashFlow(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	from, to, _, err := reportQuery(r)
	if err != nil {
		return err
	}
	cf, err := h.svc.CashFlow(r.Context(), pid, from, to)
	if err != nil {
		return err
	}
	if wantsCSV(r) {
		rows := statementCSV(cf.Lines)
		rows[0] = csvlang.Header(r, rows[0])
		return writeCSV(w, "cash-flow-"+cf.To.String()+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, cf)
}
