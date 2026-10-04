package documents

import (
	"context"
	"net/http"
	"strconv"

	"kamarapms/internal/budget"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// SetBudget gives the documents the budget service, for the budget against actual report as a PDF.
func (s *Service) SetBudget(b *budget.Service) { s.budget = b }

var budgetCols = []col{
	{48, "", "L"}, {22, "Actual", "R"}, {22, "Budget", "R"}, {22, "Variance", "R"}, {22, "YTD actual", "R"}, {22, "YTD budget", "R"}, {22, "YTD variance", "R"},
}

// BudgetVsActualPDF is the budget against actual report: the period and the year to date, laid out like the income statement (budget.view).
func (s *Service) BudgetVsActualPDF(ctx context.Context, propertyID int64, q budget.VsActualQuery) (Document, error) {
	rep, err := s.budget.VsActual(ctx, propertyID, q)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	m := func(c budget.Cell) []string {
		if c.Actual.IsZero() && c.Budget.IsZero() {
			return []string{"", "", ""}
		}
		return []string{dc.lang.Money(c.Actual, dc.decimals), dc.lang.Money(c.Budget, dc.decimals), dc.lang.Money(c.Variance, dc.decimals)}
	}
	cells := func(period, ytd budget.Cell) []string { return append(m(period), m(ytd)...) }
	var rows [][]string
	bold := map[int]bool{}
	for _, l := range rep.Lines {
		switch l.Kind {
		case "HEADING":
			bold[len(rows)] = true
			rows = append(rows, append([]string{l.Title}, "", "", "", "", "", ""))
		case "GROUP":
			if len(l.Accounts) == 1 { // one account: its line is the group
				a := l.Accounts[0]
				rows = append(rows, append([]string{a.Code + " " + a.Name}, cells(a.Period, a.YTD)...))
				continue
			}
			rows = append(rows, append([]string{l.Title}, "", "", "", "", "", ""))
			for _, a := range l.Accounts {
				rows = append(rows, append([]string{"  " + a.Code + " " + a.Name}, cells(a.Period, a.YTD)...))
			}
			bold[len(rows)] = true
			rows = append(rows, append([]string{"Total " + l.Title}, cells(l.Period, l.YTD)...))
		default:
			bold[len(rows)] = true
			rows = append(rows, append([]string{l.Title}, cells(l.Period, l.YTD)...))
		}
	}
	notes := []string{"The variance is the actual less the budget. The year to date runs from the first day of the fiscal year to the end of the period."}
	d, _, err := s.financial(ctx, propertyID, "BUDGET AGAINST ACTUAL", dc.lang.T("USALI layout"), dc.lang.Date(rep.From)+" - "+dc.lang.Date(rep.To), budgetCols, rows, bold, notes...)
	if err != nil {
		return Document{}, err
	}
	d.Pairs = append([][2]string{{"Fiscal year", rep.YearLabel}, {"Budget", rep.Budget.Name + " (v" + strconv.Itoa(rep.Budget.Version) + ", " + rep.Budget.Status + ")"}}, d.Pairs...)
	pdf, err := RenderFinancial(d)
	return Document{Filename: "budget-vs-actual-" + rep.To.String() + ".pdf", PDF: pdf}, err
}

func (h *Handler) registerBudget(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/properties/{propertyId}/budgets/vs-actual.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		q, err := budget.ReportQuery(r)
		if err != nil {
			return err
		}
		doc, err := h.svc.BudgetVsActualPDF(langCtx(r), pid, q)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
}
