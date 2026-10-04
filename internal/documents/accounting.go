package documents

import (
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"

	"context"
)

// FinancialDoc is a report of the books as a table: the trial balance, a ledger, the income statement or the balance
// sheet. Bold rows are totals and subtotals.
type FinancialDoc struct {
	Hotel    Hotel
	Lang     Lang
	Printed  string
	Title    string
	Subtitle string
	Pairs    [][2]string
	Cols     []col
	Rows     [][]string
	Bold     map[int]bool
	Notes    []string
}

// RenderFinancial draws a report of the books.
func RenderFinancial(d FinancialDoc) ([]byte, error) {
	g := newPage(d.Hotel, d.Lang, d.Title, d.Printed)
	g.title(d.Title, d.Subtitle)
	g.pairs(d.Pairs)
	g.p.Ln(3)
	g.tableB(d.Cols, d.Rows, func(i int) bool { return d.Bold[i] })
	for _, n := range d.Notes {
		g.p.Ln(2)
		g.note(n, "I", 8.5)
	}
	return g.bytes()
}

func (s *Service) financial(ctx context.Context, propertyID int64, title, subtitle, period string, cols []col, rows [][]string, bold map[int]bool, notes ...string) (FinancialDoc, docContext, error) {
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return FinancialDoc{}, dc, err
	}
	return FinancialDoc{
		Hotel: dc.hotel, Lang: dc.lang, Printed: dc.printed, Title: title, Subtitle: subtitle, Pairs: [][2]string{{"Period", period}, {"Currency", dc.prop.CurrencyCode}},
		Cols: cols, Rows: rows, Bold: bold, Notes: notes,
	}, dc, nil
}

// blankZero prints an amount, or nothing for zero, so the columns of a statement stay readable.
func blankZero(l Lang, d decimal.Decimal, decimals int32) string {
	if d.IsZero() {
		return ""
	}
	return l.Money(d, decimals)
}

func statementRows(lang Lang, lines []accounting.StatementLine, decimals int32) ([][]string, map[int]bool) {
	var rows [][]string
	bold := map[int]bool{}
	for _, l := range lines {
		switch l.Kind {
		case "HEADING":
			bold[len(rows)] = true
			rows = append(rows, []string{"", l.Title, ""})
		case "GROUP":
			if len(l.Accounts) == 1 { // one account: its line is the group
				a := l.Accounts[0]
				rows = append(rows, []string{a.Code, l.Title + " - " + a.Name, blankZero(lang, a.Amount, decimals)})
				continue
			}
			rows = append(rows, []string{"", l.Title, ""})
			for _, a := range l.Accounts {
				rows = append(rows, []string{a.Code, "    " + a.Name, blankZero(lang, a.Amount, decimals)})
			}
			bold[len(rows)] = true
			rows = append(rows, []string{"", "Total " + l.Title, lang.Money(l.Amount, decimals)})
		default:
			bold[len(rows)] = true
			rows = append(rows, []string{"", l.Title, lang.Money(l.Amount, decimals)})
		}
	}
	return rows, bold
}

var statementCols = []col{{24, "Account", "L"}, {116, "", "L"}, {40, "Amount", "R"}}

// IncomeStatementPDF is the income statement of a range after USALI (accounting.view).
func (s *Service) IncomeStatementPDF(ctx context.Context, propertyID int64, from, to *civil.Date) (Document, error) {
	is, err := s.acct.IncomeStatement(ctx, propertyID, from, to)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	rows, bold := statementRows(dc.lang, is.Lines, dc.decimals)
	d, _, err := s.financial(ctx, propertyID, "INCOME STATEMENT", "USALI layout", dc.lang.Date(is.From)+" - "+dc.lang.Date(is.To), statementCols, rows, bold)
	if err != nil {
		return Document{}, err
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "income-statement-" + is.To.String() + ".pdf", PDF: pdf}, err
}

// CashFlowPDF is the cash flow statement of a range (accounting.view).
func (s *Service) CashFlowPDF(ctx context.Context, propertyID int64, from, to *civil.Date) (Document, error) {
	cf, err := s.acct.CashFlow(ctx, propertyID, from, to)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	rows, bold := statementRows(dc.lang, cf.Lines, dc.decimals)
	var notes []string
	if !cf.Difference.IsZero() {
		notes = append(notes, "WARNING: the statement differs from the change of the cash accounts by "+dc.lang.Money(cf.Difference, dc.decimals)+".")
	}
	d, _, err := s.financial(ctx, propertyID, "CASH FLOW STATEMENT", dc.lang.T("Indirect method"), dc.lang.Date(cf.From)+" - "+dc.lang.Date(cf.To), statementCols, rows, bold, notes...)
	if err != nil {
		return Document{}, err
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "cash-flow-" + cf.To.String() + ".pdf", PDF: pdf}, err
}

// BalanceSheetPDF is the balance sheet as of a date (accounting.view).
func (s *Service) BalanceSheetPDF(ctx context.Context, propertyID int64, asOf *civil.Date) (Document, error) {
	bs, err := s.acct.BalanceSheet(ctx, propertyID, asOf)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	rows, bold := statementRows(dc.lang, bs.Lines, dc.decimals)
	notes := []string{"Equity includes the earnings of all periods to date: there is no year-end closing entry."}
	if !bs.Difference.IsZero() {
		notes = append(notes, "WARNING: the books are out of balance by "+dc.lang.Money(bs.Difference, dc.decimals)+".")
	}
	d, _, err := s.financial(ctx, propertyID, "BALANCE SHEET", "As of "+dc.lang.Date(bs.AsOf), "As of "+dc.lang.Date(bs.AsOf), statementCols, rows, bold, notes...)
	if err != nil {
		return Document{}, err
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "balance-sheet-" + bs.AsOf.String() + ".pdf", PDF: pdf}, err
}

// TrialBalancePDF is the trial balance of a range (accounting.view).
func (s *Service) TrialBalancePDF(ctx context.Context, propertyID int64, from, to *civil.Date) (Document, error) {
	tb, err := s.acct.TrialBalance(ctx, propertyID, from, to)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v decimal.Decimal) string { return blankZero(dc.lang, v, dc.decimals) }
	var rows [][]string
	for _, r := range tb.Rows {
		rows = append(rows, []string{r.Code + " " + r.Name, m(r.OpeningDebit), m(r.OpeningCredit), m(r.Debit), m(r.Credit), m(r.ClosingDebit), m(r.ClosingCredit)})
	}
	t := tb.Totals
	rows = append(rows, []string{"Total", m(t.OpeningDebit), m(t.OpeningCredit), m(t.Debit), m(t.Credit), m(t.ClosingDebit), m(t.ClosingCredit)})
	cols := []col{{48, "Account", "L"}, {22, "Open Dr", "R"}, {22, "Open Cr", "R"}, {22, "Debit", "R"}, {22, "Credit", "R"}, {22, "Close Dr", "R"}, {22, "Close Cr", "R"}}
	d, _, err := s.financial(ctx, propertyID, "TRIAL BALANCE", "", dc.lang.Date(tb.From)+" - "+dc.lang.Date(tb.To), cols, rows, map[int]bool{len(rows) - 1: true})
	if err != nil {
		return Document{}, err
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "trial-balance-" + tb.To.String() + ".pdf", PDF: pdf}, err
}

// LedgerPDF is the general ledger of one account in a range (accounting.view).
func (s *Service) LedgerPDF(ctx context.Context, propertyID, accountID int64, from, to *civil.Date) (Document, error) {
	gl, err := s.acct.GeneralLedger(ctx, propertyID, accountID, from, to)
	if err != nil {
		return Document{}, err
	}
	dc, err := s.context(ctx, propertyID)
	if err != nil {
		return Document{}, err
	}
	m := func(v decimal.Decimal) string { return blankZero(dc.lang, v, dc.decimals) }
	rows := [][]string{{dc.lang.Date(gl.From), "", "Opening balance", "", "", dc.lang.Money(gl.Opening, dc.decimals)}}
	for _, l := range gl.Lines {
		rows = append(rows, []string{dc.lang.Date(l.Date), l.JournalNumber, l.Description, m(l.Debit), m(l.Credit), dc.lang.Money(l.Balance, dc.decimals)})
	}
	rows = append(rows, []string{"", "", "Closing balance", m(gl.Debit), m(gl.Credit), dc.lang.Money(gl.Closing, dc.decimals)})
	cols := []col{{22, "Date", "L"}, {24, "Journal", "L"}, {56, "Detail", "L"}, {26, "Debit", "R"}, {26, "Credit", "R"}, {26, "Balance", "R"}}
	var notes []string
	if gl.Truncated {
		notes = append(notes, "Only the first entries are listed: narrow the range.")
	}
	d, _, err := s.financial(ctx, propertyID, "GENERAL LEDGER", gl.Account.Code+" "+gl.Account.Name, dc.lang.Date(gl.From)+" - "+dc.lang.Date(gl.To), cols, rows, map[int]bool{len(rows) - 1: true}, notes...)
	if err != nil {
		return Document{}, err
	}
	pdf, err := RenderFinancial(d)
	return Document{Filename: "ledger-" + gl.Account.Code + ".pdf", PDF: pdf}, err
}

func (h *Handler) registerAccounting(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/accounting"
	rangeDoc := func(render func(ctx context.Context, propertyID int64, from, to *civil.Date) (Document, error)) httpx.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			pid, err := tenancy.PropertyID(r)
			if err != nil {
				return err
			}
			from, to, _, err := dateParams(r)
			if err != nil {
				return err
			}
			doc, err := render(langCtx(r), pid, from, to)
			if err != nil {
				return err
			}
			return writePDF(w, doc)
		}
	}
	mux.Handle("GET "+p+"/trial-balance.pdf", rangeDoc(h.svc.TrialBalancePDF))
	mux.Handle("GET "+p+"/income-statement.pdf", rangeDoc(h.svc.IncomeStatementPDF))
	mux.Handle("GET "+p+"/cash-flow.pdf", rangeDoc(h.svc.CashFlowPDF))
	mux.Handle("GET "+p+"/balance-sheet.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		_, _, asOf, err := dateParams(r)
		if err != nil {
			return err
		}
		doc, err := h.svc.BalanceSheetPDF(langCtx(r), pid, asOf)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
	mux.Handle("GET "+p+"/accounts/{id}/ledger.pdf", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		pid, err := tenancy.PropertyID(r)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			return apperr.NotFound("NOT_FOUND", "no such document")
		}
		from, to, _, err := dateParams(r)
		if err != nil {
			return err
		}
		doc, err := h.svc.LedgerPDF(langCtx(r), pid, id, from, to)
		if err != nil {
			return err
		}
		return writePDF(w, doc)
	}))
}

// dateParams reads from, to and as_of (each optional, YYYY-MM-DD).
func dateParams(r *http.Request) (from, to, asOf *civil.Date, err error) {
	var errs []apperr.FieldError
	get := func(name string) *civil.Date {
		v := r.URL.Query().Get(name)
		if v == "" {
			return nil
		}
		d, perr := civil.ParseDate(v)
		if perr != nil {
			errs = append(errs, apperr.FieldError{Field: name, Code: "INVALID_DATE", Message: "a date as YYYY-MM-DD"})
			return nil
		}
		return &d
	}
	from, to, asOf = get("from"), get("to"), get("as_of")
	if len(errs) > 0 {
		return nil, nil, nil, apperr.Invalid("the document parameters are invalid", errs...)
	}
	return from, to, asOf, nil
}
