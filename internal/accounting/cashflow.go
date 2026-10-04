package accounting

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// CashFlow is the cash flow statement of a range by the indirect method: from the net income of the range, the non-cash items added back, then what the
// balance sheet accounts moved, in operating, investing and financing activities. Nothing is stored: it is read from the journals, and it is proved against
// the change of the cash accounts (the CASH group).
type CashFlow struct {
	Range
	Lines        []StatementLine `json:"lines"`
	NetIncome    decimal.Decimal `json:"net_income"`
	Operating    decimal.Decimal `json:"operating"`
	Investing    decimal.Decimal `json:"investing"`
	Financing    decimal.Decimal `json:"financing"`
	Unclassified decimal.Decimal `json:"unclassified"`
	NetChange    decimal.Decimal `json:"net_change"`
	OpeningCash  decimal.Decimal `json:"opening_cash"`
	ClosingCash  decimal.Decimal `json:"closing_cash"`
	Difference   decimal.Decimal `json:"difference"`
	Reconciled   bool            `json:"reconciled"`
}

var (
	operatingGroups = []groupSpec{
		{"RECEIVABLES", "Receivables"}, {"INVENTORIES", "Inventories"}, {"PREPAID", "Prepaid and other current"}, {"OTHER_ASSETS", "Other assets"},
		{"PAYABLES", "Payables"}, {"ACCRUED", "Accrued expenses"}, {"DEPOSITS", "Guest deposits and unearned revenue"},
		{"TAXES_PAYABLE", "Taxes and service charges payable"}, {"OTHER_CURRENT_LIABILITIES", "Other current liabilities"}, {"SUSPENSE", "Suspense"},
	}
	investingGroups = []groupSpec{{"FIXED_ASSETS", "Purchase of property and equipment"}}
	financingGroups = []groupSpec{{"LONG_TERM_DEBT", "Long-term liabilities"}, {"EQUITY", "Owner's equity (capital and drawings)"}}
)

// CashFlow is the cash flow statement of a range (accounting.view). The movement of an account is the other side of what the cash did: an asset that grows took cash,
// a liability or equity that grows brought it. Closing journals are left out of the movements as of the income statement, so the result of a closed year is not counted twice.
// Depreciation is added back, and taken out of the movement of the fixed assets, which carry the accumulated depreciation.
func (s *Service) CashFlow(ctx context.Context, propertyID int64, from, to *civil.Date) (CashFlow, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return CashFlow{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return CashFlow{}, err
	}
	is, err := s.IncomeStatement(ctx, propertyID, &r.From, &r.To)
	if err != nil {
		return CashFlow{}, err
	}
	q := s.q(ctx)
	before := r.From.AddDays(-1)
	opening, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, ToDate: before, ExcludeClosing: true})
	if err != nil {
		return CashFlow{}, err
	}
	closing, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, ToDate: r.To, ExcludeClosing: true})
	if err != nil {
		return CashFlow{}, err
	}
	type acc struct {
		row          accountingdb.AccountBalancesRow
		open, closed decimal.Decimal
	}
	byID := map[int64]*acc{}
	for _, x := range opening {
		byID[x.ID] = &acc{row: x, open: x.Balance}
	}
	for _, x := range closing {
		a := byID[x.ID]
		if a == nil {
			a = &acc{row: x}
			byID[x.ID] = a
		}
		a.row, a.closed = x, x.Balance
	}
	known := map[string]bool{"CASH": true}
	for _, specs := range [][]groupSpec{operatingGroups, investingGroups, financingGroups} {
		for _, g := range specs {
			known[g.key] = true
		}
	}
	effects := map[string][]StatementAccount{} // by group: the cash effect of each account, which is minus its movement
	amounts := map[string]decimal.Decimal{}
	cashOpen, cashClose := decimal.Zero, decimal.Zero
	var unclassified []StatementAccount
	unclassifiedSum := decimal.Zero
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return byID[ids[i]].row.Code < byID[ids[j]].row.Code })
	for _, id := range ids {
		a := byID[id]
		if a.row.AccountType == TypeRevenue || a.row.AccountType == TypeExpense {
			continue
		}
		g := deref(a.row.StatementGroup)
		if g == "CASH" {
			cashOpen, cashClose = cashOpen.Add(a.open), cashClose.Add(a.closed)
			continue
		}
		effect := a.open.Sub(a.closed)
		if effect.IsZero() {
			continue
		}
		sa := StatementAccount{AccountID: a.row.ID, Code: a.row.Code, Name: a.row.Name, Amount: effect}
		if !known[g] {
			unclassified = append(unclassified, sa)
			unclassifiedSum = unclassifiedSum.Add(effect)
			continue
		}
		effects[g] = append(effects[g], sa)
		amounts[g] = amounts[g].Add(effect)
	}
	depreciation := decimal.Zero
	for _, x := range closing {
		if deref(x.StatementGroup) == "DEPRECIATION" {
			depreciation = depreciation.Add(x.Balance)
		}
	}
	for _, x := range opening {
		if deref(x.StatementGroup) == "DEPRECIATION" {
			depreciation = depreciation.Sub(x.Balance)
		}
	}

	var lines []StatementLine
	heading := func(key, title string) {
		lines = append(lines, StatementLine{Key: key, Title: title, Kind: "HEADING", Accounts: []StatementAccount{}})
	}
	item := func(key, title string, amount decimal.Decimal, accounts []StatementAccount) {
		if accounts == nil {
			accounts = []StatementAccount{}
		}
		lines = append(lines, StatementLine{Key: key, Title: title, Kind: "GROUP", Amount: amount, Accounts: accounts})
	}
	total := func(key, title, kind string, amount decimal.Decimal) decimal.Decimal {
		lines = append(lines, StatementLine{Key: key, Title: title, Kind: kind, Amount: amount, Accounts: []StatementAccount{}})
		return amount
	}
	section := func(specs []groupSpec, adjust map[string]decimal.Decimal) decimal.Decimal {
		sum := decimal.Zero
		for _, g := range specs {
			amount := amounts[g.key].Add(adjust[g.key])
			if len(effects[g.key]) == 0 && amount.IsZero() {
				continue
			}
			item(g.key, g.title, amount, effects[g.key])
			sum = sum.Add(amount)
		}
		return sum
	}

	heading("H_OPERATING", "Operating activities")
	item("NET_INCOME", "Net income", is.NetIncome, nil)
	op := is.NetIncome
	if !depreciation.IsZero() {
		item("DEPRECIATION", "Depreciation and amortization (not paid in cash)", depreciation, nil)
		op = op.Add(depreciation)
	}
	op = op.Add(section(operatingGroups, nil))
	op = total("OPERATING", "Net cash from operating activities", "SUBTOTAL", op)

	heading("H_INVESTING", "Investing activities")
	// The fixed assets carry the accumulated depreciation: what the depreciation took off them is not an outflow, it was added back above.
	inv := section(investingGroups, map[string]decimal.Decimal{"FIXED_ASSETS": depreciation.Neg()})
	inv = total("INVESTING", "Net cash from investing activities", "SUBTOTAL", inv)

	heading("H_FINANCING", "Financing activities")
	fin := section(financingGroups, nil)
	fin = total("FINANCING", "Net cash from financing activities", "SUBTOTAL", fin)

	unc := decimal.Zero
	if len(unclassified) > 0 {
		heading("H_UNCLASSIFIED", "Unclassified (accounts without a statement group)")
		item("UNCLASSIFIED", "Unclassified", unclassifiedSum, unclassified)
		unc = unclassifiedSum
	}

	net := op.Add(inv).Add(fin).Add(unc)
	total("NET_CHANGE", "Net change in cash", "TOTAL", net)
	item("OPENING_CASH", "Cash at the start of the period", cashOpen, nil)
	total("CLOSING_CASH", "Cash at the end of the period", "TOTAL", cashClose)
	diff := cashClose.Sub(cashOpen).Sub(net)
	return CashFlow{
		Range: r, Lines: lines, NetIncome: is.NetIncome, Operating: op, Investing: inv, Financing: fin, Unclassified: unc, NetChange: net,
		OpeningCash: cashOpen, ClosingCash: cashClose, Difference: diff, Reconciled: diff.IsZero(),
	}, nil
}
