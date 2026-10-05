package budget

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/csvlang"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// Handler exposes the budget API.
type Handler struct{ svc *Service }

// NewHandler returns the HTTP handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/budgets"
	mux.Handle("GET "+p, httpx.HandlerFunc(h.list))
	mux.Handle("POST "+p, httpx.HandlerFunc(h.create))
	mux.Handle("GET "+p+"/vs-actual", httpx.HandlerFunc(h.vsActual))
	mux.Handle("GET "+p+"/statistics-vs-actual", httpx.HandlerFunc(h.statsVsActual))
	mux.Handle("GET "+p+"/department-vs-actual", httpx.HandlerFunc(h.deptVsActual))
	mux.Handle("GET "+p+"/{id}", httpx.HandlerFunc(h.get))
	mux.Handle("PATCH "+p+"/{id}", httpx.HandlerFunc(h.update))
	mux.Handle("DELETE "+p+"/{id}", httpx.HandlerFunc(h.delete))
	mux.Handle("PUT "+p+"/{id}/grid", httpx.HandlerFunc(h.saveGrid))
	mux.Handle("PUT "+p+"/{id}/statistics", httpx.HandlerFunc(h.saveStatistics))
	mux.Handle("POST "+p+"/{id}/spread", httpx.HandlerFunc(h.spread))
	mux.Handle("POST "+p+"/{id}/fill-from-actuals", httpx.HandlerFunc(h.fill))
	mux.Handle("POST "+p+"/{id}/activate", httpx.HandlerFunc(h.activate))
	mux.Handle("GET "+p+"/{id}/export", httpx.HandlerFunc(h.export))
	mux.Handle("POST "+p+"/{id}/import", httpx.HandlerFunc(h.importCSV))
}

func ids(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, errBudgetNotFound()
	}
	return propertyID, id, nil
}

func dateQuery(r *http.Request, name string, errs *[]apperr.FieldError) *civil.Date {
	v := strings.TrimSpace(r.URL.Query().Get(name))
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	f := ListFilter{YearStart: dateQuery(r, "year_start", &errs), Status: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))}
	if len(errs) > 0 {
		return apperr.Invalid("the filter is invalid", errs...)
	}
	list, err := h.svc.List(r.Context(), pid, f)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, httpx.Page[Budget]{Data: list})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.Create(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	b, err := h.svc.Get(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in UpdateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.Update(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(r.Context(), pid, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) saveGrid(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in GridInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.SaveGrid(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) spread(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in SpreadInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.Spread(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) fill(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in FillInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.FillFromActuals(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in ActivateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.Activate(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

// writeCSV answers rows as a download. Text that a spreadsheet could read as a formula is prefixed with an apostrophe (a number may start with a minus).
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

func (h *Handler) export(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	b, rows, err := h.svc.ExportCSV(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return writeCSV(w, "budget-"+b.YearLabel+"-v"+strconv.Itoa(b.Version)+".csv", rows)
}

func (h *Handler) importCSV(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
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
	res, err := h.svc.ImportCSV(r.Context(), pid, id, in.CSV, in.DryRun)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}

// ReportQuery reads the parameters of the budget against actual report from a request.
func ReportQuery(r *http.Request) (VsActualQuery, error) {
	var errs []apperr.FieldError
	q := VsActualQuery{YearStart: dateQuery(r, "year_start", &errs), From: dateQuery(r, "from", &errs), To: dateQuery(r, "to", &errs)}
	if v := strings.TrimSpace(r.URL.Query().Get("budget_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			errs = append(errs, fieldErr("budget_id", "INVALID_VALUE", "a budget id"))
		} else {
			q.BudgetID = &id
		}
	}
	if len(errs) > 0 {
		return q, apperr.Invalid("the report parameters are invalid", errs...)
	}
	return q, nil
}

func (h *Handler) vsActual(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q, err := ReportQuery(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.VsActual(r.Context(), pid, q)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("format") == "csv" {
		rows := [][]string{{"line", "code", "name", "period_actual", "period_budget", "period_variance", "period_variance_percent", "ytd_actual", "ytd_budget", "ytd_variance", "ytd_variance_percent"}}
		pct := func(c Cell) string {
			if c.VariancePercent == nil {
				return ""
			}
			return c.VariancePercent.String()
		}
		cells := func(a, b Cell) []string {
			return []string{a.Actual.String(), a.Budget.String(), a.Variance.String(), pct(a), b.Actual.String(), b.Budget.String(), b.Variance.String(), pct(b)}
		}
		for _, l := range rep.Lines {
			switch l.Kind {
			case "HEADING":
				rows = append(rows, []string{l.Title, "", "", "", "", "", "", "", "", "", ""})
			case "GROUP":
				for _, a := range l.Accounts {
					rows = append(rows, append([]string{l.Title, a.Code, a.Name}, cells(a.Period, a.YTD)...))
				}
				if len(l.Accounts) != 1 {
					rows = append(rows, append([]string{"Total " + l.Title, "", ""}, cells(l.Period, l.YTD)...))
				}
			default:
				rows = append(rows, append([]string{l.Title, "", ""}, cells(l.Period, l.YTD)...))
			}
		}
		rows[0] = csvlang.Header(r, rows[0])
		return writeCSV(w, "budget-vs-actual-"+rep.To.String()+".csv", rows)
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) saveStatistics(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := ids(r)
	if err != nil {
		return err
	}
	var in StatisticsInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	b, err := h.svc.SaveStatistics(r.Context(), pid, id, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *Handler) statsVsActual(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q, err := ReportQuery(r)
	if err != nil {
		return err
	}
	rep, err := h.svc.StatisticsVsActual(r.Context(), pid, q)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) deptVsActual(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	q, err := ReportQuery(r)
	if err != nil {
		return err
	}
	var dept *int64
	if v := strings.TrimSpace(r.URL.Query().Get("department_id")); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || id < 1 {
			return apperr.Invalid("the report parameters are invalid", fieldErr("department_id", "INVALID_VALUE", "a department id"))
		}
		dept = &id
	}
	rep, err := h.svc.DepartmentVsActual(r.Context(), pid, q, dept)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, rep)
}
