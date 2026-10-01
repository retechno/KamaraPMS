package accounting

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

const (
	maxLedgerLines = 5000
	maxReportYears = 5
)

// Range is the period a report covers (both days included).
type Range struct {
	From civil.Date `json:"from"`
	To   civil.Date `json:"to"`
}

// resolveRange fills the defaults (the current month so far) and checks the range.
func (s *Service) resolveRange(ctx context.Context, propertyID int64, from, to *civil.Date) (Range, error) {
	today, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return Range{}, err
	}
	r := Range{From: periodStart(today), To: today}
	if from != nil {
		r.From = *from
	}
	if to != nil {
		r.To = *to
	}
	switch {
	case r.To.Before(r.From):
		return r, apperr.Invalid("the range is invalid", fieldErr("to", "BEFORE_FROM", "the end is before the start"))
	case r.From.AddDays(366 * maxReportYears).Before(r.To):
		return r, apperr.Invalid("the range is invalid", fieldErr("to", "TOO_LONG", "at most 5 years"))
	}
	return r, nil
}

func (s *Service) resolveAsOf(ctx context.Context, propertyID int64, asOf *civil.Date) (civil.Date, error) {
	today, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return today, err
	}
	if asOf == nil {
		return today, nil
	}
	if asOf.After(today) {
		return *asOf, apperr.Invalid("the date is invalid", fieldErr("as_of", "IN_THE_FUTURE", "not after the current business date"))
	}
	return *asOf, nil
}

// natural is a signed balance (debit minus credit) on the side an account type normally carries.
func natural(side string, balance decimal.Decimal) decimal.Decimal {
	if side == SideCredit {
		return balance.Neg()
	}
	return balance
}

// ---------------------------------------------------------------------------------------------------------------
// Trial balance

// TrialRow is an account with its opening balance, the movement of the range and its closing balance; each balance is
// shown on the side it falls on.
type TrialRow struct {
	AccountID     int64           `json:"account_id"`
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	AccountType   string          `json:"account_type"`
	OpeningDebit  decimal.Decimal `json:"opening_debit"`
	OpeningCredit decimal.Decimal `json:"opening_credit"`
	Debit         decimal.Decimal `json:"debit"`
	Credit        decimal.Decimal `json:"credit"`
	ClosingDebit  decimal.Decimal `json:"closing_debit"`
	ClosingCredit decimal.Decimal `json:"closing_credit"`
}

// TrialBalance lists the accounts with entries up to the end of the range.
type TrialBalance struct {
	Range
	Rows   []TrialRow `json:"rows"`
	Totals TrialRow   `json:"totals"`
}

func sides(n decimal.Decimal) (debit, credit decimal.Decimal) {
	if n.IsNegative() {
		return decimal.Zero, n.Neg()
	}
	return n, decimal.Zero
}

// TrialBalance is the trial balance of a range (accounting.view).
func (s *Service) TrialBalance(ctx context.Context, propertyID int64, from, to *civil.Date) (TrialBalance, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return TrialBalance{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return TrialBalance{}, err
	}
	rows, err := s.q(ctx).TrialBalanceRows(ctx, accountingdb.TrialBalanceRowsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: r.From, ToDate: r.To})
	if err != nil {
		return TrialBalance{}, err
	}
	out := TrialBalance{Range: r, Rows: []TrialRow{}}
	for _, x := range rows {
		row := TrialRow{AccountID: x.ID, Code: x.Code, Name: x.Name, AccountType: x.AccountType, Debit: x.Debit, Credit: x.Credit}
		row.OpeningDebit, row.OpeningCredit = sides(x.Opening)
		row.ClosingDebit, row.ClosingCredit = sides(x.Opening.Add(x.Debit).Sub(x.Credit))
		if row.OpeningDebit.IsZero() && row.OpeningCredit.IsZero() && row.Debit.IsZero() && row.Credit.IsZero() && row.ClosingDebit.IsZero() && row.ClosingCredit.IsZero() {
			continue
		}
		out.Rows = append(out.Rows, row)
		t := &out.Totals
		t.OpeningDebit, t.OpeningCredit = t.OpeningDebit.Add(row.OpeningDebit), t.OpeningCredit.Add(row.OpeningCredit)
		t.Debit, t.Credit = t.Debit.Add(row.Debit), t.Credit.Add(row.Credit)
		t.ClosingDebit, t.ClosingCredit = t.ClosingDebit.Add(row.ClosingDebit), t.ClosingCredit.Add(row.ClosingCredit)
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------
// General ledger

// LedgerLine is one entry of an account with the running balance on the account's normal side.
type LedgerLine struct {
	Date          civil.Date      `json:"journal_date"`
	JournalID     int64           `json:"journal_id"`
	JournalNumber string          `json:"journal_number"`
	JournalType   string          `json:"journal_type"`
	Description   string          `json:"description"`
	SourceType    string          `json:"source_type,omitempty"`
	SourceRef     string          `json:"source_ref,omitempty"`
	Debit         decimal.Decimal `json:"debit"`
	Credit        decimal.Decimal `json:"credit"`
	Balance       decimal.Decimal `json:"balance"`
}

// GeneralLedger is the entries of one account in a range.
type GeneralLedger struct {
	Range
	Account   Account         `json:"account"`
	Opening   decimal.Decimal `json:"opening_balance"`
	Lines     []LedgerLine    `json:"lines"`
	Debit     decimal.Decimal `json:"total_debit"`
	Credit    decimal.Decimal `json:"total_credit"`
	Closing   decimal.Decimal `json:"closing_balance"`
	Truncated bool            `json:"truncated"`
}

// GeneralLedger is the ledger of one account (accounting.view). Balances are on the account's normal side.
func (s *Service) GeneralLedger(ctx context.Context, propertyID, accountID int64, from, to *civil.Date) (GeneralLedger, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return GeneralLedger{}, err
	}
	acc, err := s.load(ctx, p.TenantID, propertyID, accountID)
	if err != nil {
		return GeneralLedger{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return GeneralLedger{}, err
	}
	q := s.q(ctx)
	opening, err := q.LedgerOpening(ctx, accountingdb.LedgerOpeningParams{TenantID: p.TenantID, PropertyID: propertyID, AccountID: accountID, FromDate: r.From})
	if err != nil {
		return GeneralLedger{}, err
	}
	rows, err := q.LedgerLines(ctx, accountingdb.LedgerLinesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountID: accountID, FromDate: r.From, ToDate: r.To, RowLimit: maxLedgerLines + 1})
	if err != nil {
		return GeneralLedger{}, err
	}
	out := GeneralLedger{Range: r, Account: acc, Opening: natural(acc.NormalSide, opening), Lines: []LedgerLine{}}
	if len(rows) > maxLedgerLines {
		rows, out.Truncated = rows[:maxLedgerLines], true
	}
	run := opening
	for _, x := range rows {
		run = run.Add(x.Debit).Sub(x.Credit)
		out.Debit, out.Credit = out.Debit.Add(x.Debit), out.Credit.Add(x.Credit)
		desc := deref(x.Description)
		if desc == "" {
			desc = x.JournalDescription
		}
		out.Lines = append(out.Lines, LedgerLine{
			Date: x.JournalDate, JournalID: x.JournalID, JournalNumber: x.JournalNumber, JournalType: x.JournalType, Description: desc,
			SourceType: deref(x.SourceType), SourceRef: deref(x.SourceRef), Debit: x.Debit, Credit: x.Credit, Balance: natural(acc.NormalSide, run),
		})
	}
	out.Closing = natural(acc.NormalSide, run)
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Statements

// StatementAccount is an account in a statement line.
type StatementAccount struct {
	AccountID int64           `json:"account_id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	Amount    decimal.Decimal `json:"amount"`
}

// StatementLine is a group of accounts (kind GROUP) or a subtotal (kind SUBTOTAL, TOTAL).
type StatementLine struct {
	Key      string             `json:"key"`
	Title    string             `json:"title"`
	Kind     string             `json:"kind"`
	Amount   decimal.Decimal    `json:"amount"`
	Accounts []StatementAccount `json:"accounts"`
}

// IncomeStatement is the income statement laid out after USALI.
type IncomeStatement struct {
	Range
	Lines     []StatementLine `json:"lines"`
	NetIncome decimal.Decimal `json:"net_income"`
}

type groupSpec struct{ key, title string }

var (
	revGroups   = []groupSpec{{"REV_ROOMS", "Rooms"}, {"REV_FB", "Food and beverage"}, {"REV_OOD", "Other operated departments"}, {"REV_RENTAL_OTHER", "Rentals and other income"}, {"REV_MISC", "Miscellaneous income"}}
	deptExp     = []groupSpec{{"EXP_ROOMS", "Rooms"}, {"EXP_FB", "Food and beverage"}, {"EXP_OOD", "Other operated departments"}}
	undistrib   = []groupSpec{{"UND_AG", "Administrative and general"}, {"UND_IT", "Information and telecommunications"}, {"UND_SM", "Sales and marketing"}, {"UND_POM", "Property operations and maintenance"}, {"UND_UTIL", "Utilities"}}
	assetGroups = []groupSpec{{"CASH", "Cash and equivalents"}, {"RECEIVABLES", "Receivables"}, {"INVENTORIES", "Inventories"}, {"PREPAID", "Prepaid and other current"}, {"FIXED_ASSETS", "Property and equipment"}, {"OTHER_ASSETS", "Other assets"}}
	liabGroups  = []groupSpec{{"PAYABLES", "Payables"}, {"ACCRUED", "Accrued expenses"}, {"DEPOSITS", "Guest deposits and unearned revenue"}, {"TAXES_PAYABLE", "Taxes and service charges payable"}, {"OTHER_CURRENT_LIABILITIES", "Other current liabilities"}, {"LONG_TERM_DEBT", "Long-term liabilities"}, {"SUSPENSE", "Suspense"}}
)

// builder collects statement lines and the amount of each group.
type builder struct {
	byGroup map[string][]StatementAccount
	lines   []StatementLine
	amounts map[string]decimal.Decimal
}

func newBuilder(rows []accountingdb.AccountBalancesRow) *builder {
	b := &builder{byGroup: map[string][]StatementAccount{}, amounts: map[string]decimal.Decimal{}}
	for _, r := range rows {
		g := deref(r.StatementGroup)
		// Assets and expenses are shown as debit balances, liabilities, equity and revenue as credit balances, so a contra
		// account (accumulated depreciation, allowances) reduces its group.
		a := StatementAccount{AccountID: r.ID, Code: r.Code, Name: r.Name, Amount: r.Balance}
		if r.AccountType == TypeLiability || r.AccountType == TypeEquity || r.AccountType == TypeRevenue {
			a.Amount = r.Balance.Neg()
		}
		b.byGroup[g] = append(b.byGroup[g], a)
		b.amounts[g] = b.amounts[g].Add(a.Amount)
	}
	return b
}

// group adds the groups of a section and returns their sum.
func (b *builder) group(specs []groupSpec) decimal.Decimal {
	sum := decimal.Zero
	for _, g := range specs {
		sum = sum.Add(b.amounts[g.key])
		if accs := b.byGroup[g.key]; len(accs) > 0 {
			b.lines = append(b.lines, StatementLine{Key: g.key, Title: g.title, Kind: "GROUP", Amount: b.amounts[g.key], Accounts: accs})
		}
	}
	return sum
}

func (b *builder) sub(key, title, kind string, amount decimal.Decimal) decimal.Decimal {
	b.lines = append(b.lines, StatementLine{Key: key, Title: title, Kind: kind, Amount: amount, Accounts: []StatementAccount{}})
	return amount
}

func (b *builder) heading(key, title string) { b.sub(key, title, "HEADING", decimal.Zero) }

// IncomeStatement is the income statement of a range after USALI: operated departments (revenue less departmental
// expenses), undistributed operating expenses, gross operating profit, management fees, non-operating expenses
// (EBITDA), depreciation, interest and income taxes down to net income (accounting.view).
func (s *Service) IncomeStatement(ctx context.Context, propertyID int64, from, to *civil.Date) (IncomeStatement, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return IncomeStatement{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return IncomeStatement{}, err
	}
	rows, err := s.q(ctx).AccountBalances(ctx, accountingdb.AccountBalancesParams{
		TenantID: p.TenantID, PropertyID: propertyID, FromDate: &r.From, ToDate: r.To, AccountTypes: []string{TypeRevenue, TypeExpense}, ExcludeClosing: true,
	})
	if err != nil {
		return IncomeStatement{}, err
	}
	b := newBuilder(rows)
	b.heading("H_REVENUE", "Operating revenue")
	revenue := b.group(revGroups)
	b.sub("TOTAL_REVENUE", "Total revenue", "SUBTOTAL", revenue)
	b.heading("H_DEPT", "Departmental expenses")
	dept := b.group(deptExp)
	deptProfit := b.sub("DEPT_PROFIT", "Total departmental profit", "SUBTOTAL", revenue.Sub(dept))
	b.heading("H_UND", "Undistributed operating expenses")
	und := b.group(undistrib)
	gop := b.sub("GOP", "Gross operating profit", "TOTAL", deptProfit.Sub(und))
	fees := b.group([]groupSpec{{"MGMT_FEES", "Management and franchise fees"}})
	afterFees := b.sub("INCOME_AFTER_FEES", "Income after management fees", "SUBTOTAL", gop.Sub(fees))
	nonop := b.group([]groupSpec{{"NONOP", "Non-operating expenses (rent, property taxes, insurance)"}})
	ebitda := b.sub("EBITDA", "EBITDA", "TOTAL", afterFees.Sub(nonop))
	dep := b.group([]groupSpec{{"DEPRECIATION", "Depreciation and amortization"}})
	ebit := b.sub("EBIT", "EBIT", "SUBTOTAL", ebitda.Sub(dep))
	interest := b.group([]groupSpec{{"INTEREST", "Interest"}})
	ebt := b.sub("EBT", "Income before income taxes", "SUBTOTAL", ebit.Sub(interest))
	tax := b.group([]groupSpec{{"INCOME_TAX", "Income taxes"}})
	net := b.sub("NET_INCOME", "Net income", "TOTAL", ebt.Sub(tax))
	return IncomeStatement{Range: r, Lines: b.lines, NetIncome: net}, nil
}

// BalanceSheet is the balance sheet as of a date. Equity includes the earnings of all periods to date, since there is no
// year-end closing entry.
type BalanceSheet struct {
	AsOf        civil.Date      `json:"as_of"`
	Lines       []StatementLine `json:"lines"`
	Assets      decimal.Decimal `json:"total_assets"`
	Liabilities decimal.Decimal `json:"total_liabilities"`
	Equity      decimal.Decimal `json:"total_equity"`
	Difference  decimal.Decimal `json:"difference"`
}

// BalanceSheet is the balance sheet as of a business date (accounting.view).
func (s *Service) BalanceSheet(ctx context.Context, propertyID int64, asOf *civil.Date) (BalanceSheet, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return BalanceSheet{}, err
	}
	at, err := s.resolveAsOf(ctx, propertyID, asOf)
	if err != nil {
		return BalanceSheet{}, err
	}
	rows, err := s.q(ctx).AccountBalances(ctx, accountingdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, ToDate: at})
	if err != nil {
		return BalanceSheet{}, err
	}
	var bs []accountingdb.AccountBalancesRow
	earnings := decimal.Zero
	for _, r := range rows {
		if r.AccountType == TypeRevenue || r.AccountType == TypeExpense {
			earnings = earnings.Sub(r.Balance)
		} else {
			bs = append(bs, r)
		}
	}
	b := newBuilder(bs)
	b.heading("H_ASSETS", "Assets")
	assets := b.group(assetGroups)
	b.sub("TOTAL_ASSETS", "Total assets", "TOTAL", assets)
	b.heading("H_LIAB", "Liabilities")
	liab := b.group(liabGroups)
	b.sub("TOTAL_LIAB", "Total liabilities", "SUBTOTAL", liab)
	b.heading("H_EQUITY", "Equity")
	equity := b.group([]groupSpec{{"EQUITY", "Owner's equity"}})
	if !earnings.IsZero() {
		equity = equity.Add(earnings)
		b.lines = append(b.lines, StatementLine{Key: "EARNINGS", Title: "Earnings to date", Kind: "GROUP", Amount: earnings, Accounts: []StatementAccount{}})
	}
	b.sub("TOTAL_EQUITY", "Total equity", "SUBTOTAL", equity)
	b.sub("TOTAL_LIAB_EQUITY", "Total liabilities and equity", "TOTAL", liab.Add(equity))
	return BalanceSheet{AsOf: at, Lines: b.lines, Assets: assets, Liabilities: liab, Equity: equity, Difference: assets.Sub(liab).Sub(equity)}, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Reconciliation

// Control compares a control account of the ledger with what the folios and the city ledger say.
type Control struct {
	Key        string          `json:"key"`
	Title      string          `json:"title"`
	Account    string          `json:"account"`
	Ledger     decimal.Decimal `json:"ledger"`
	Source     decimal.Decimal `json:"source"`
	Difference decimal.Decimal `json:"difference"`
	Basis      string          `json:"basis"`
}

// Reconciliation is the control accounts as of a date.
type Reconciliation struct {
	AsOf          civil.Date `json:"as_of"`
	StartDate     civil.Date `json:"start_date"`
	Controls      []Control  `json:"controls"`
	PendingDays   int        `json:"pending_days"`
	IncludesToday bool       `json:"includes_open_day"`
	Reconciled    bool       `json:"reconciled"`
}

// Reconciliation proves the guest ledger, advance deposits and city ledger accounts against the folios and the city
// ledger as of a business date (accounting.view). Days still without a journal, and the open business day, make the
// ledger fall behind the source, which is reported (pending_days, includes_open_day).
func (s *Service) Reconciliation(ctx context.Context, propertyID int64, asOf *civil.Date) (Reconciliation, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return Reconciliation{}, err
	}
	cfg, err := s.q(ctx).GetSettings(ctx, accountingdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Reconciliation{}, settingsErr(err)
	}
	at, err := s.resolveAsOf(ctx, propertyID, asOf)
	if err != nil {
		return Reconciliation{}, err
	}
	today, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return Reconciliation{}, err
	}
	q := s.q(ctx)
	src, err := q.ControlSources(ctx, accountingdb.ControlSourcesParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
	if err != nil {
		return Reconciliation{}, err
	}
	pending, err := q.CountPendingDays(ctx, accountingdb.CountPendingDaysParams{TenantID: p.TenantID, PropertyID: propertyID, StartDate: cfg.StartDate, AsOf: at})
	if err != nil {
		return Reconciliation{}, err
	}
	entries, err := s.accountMap(ctx, p.TenantID, propertyID)
	if err != nil {
		return Reconciliation{}, err
	}
	rows, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, ToDate: at})
	if err != nil {
		return Reconciliation{}, err
	}
	bal := map[int64]decimal.Decimal{}
	for _, r := range rows {
		bal[r.ID] = r.Balance
	}
	acct := map[string]MapEntry{}
	for _, e := range entries {
		acct[e.Key] = e
	}
	mk := func(key, title, basis string, ledgerSide string, source decimal.Decimal) Control {
		e := acct[key]
		l := bal[e.AccountID]
		if ledgerSide == SideCredit {
			l = l.Neg()
		}
		return Control{Key: key, Title: title, Account: e.AccountCode + " " + e.AccountName, Ledger: l, Source: source, Difference: l.Sub(source), Basis: basis}
	}
	out := Reconciliation{AsOf: at, StartDate: cfg.StartDate, PendingDays: int(pending), IncludesToday: !at.Before(today)}
	out.Controls = []Control{
		mk("GUEST_LEDGER", "Guest ledger", "What the folios owe (all items to the date) plus the deposits they hold", SideDebit, src.FolioBalance.Add(src.DepositsHeld)),
		mk("ADVANCE_DEPOSITS", "Advance deposits", "Deposits on folios that were not closed yet", SideCredit, src.DepositsHeld),
		mk("CITY_LEDGER", "City ledger", "Guest balances transferred to companies, less receipts taken", SideDebit, src.CityTransferred.Sub(src.CityReceived)),
		mk(KeyAccountsPayable, "Accounts payable", "Supplier bills entered, less supplier payments made", SideCredit, src.BillsEntered.Sub(src.PaymentsMade)),
	}
	out.Reconciled = out.PendingDays == 0
	for _, c := range out.Controls {
		if !c.Difference.IsZero() {
			out.Reconciled = false
		}
	}
	return out, nil
}
