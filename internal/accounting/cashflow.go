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
	Method       string          `json:"method"`
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

// The methods of the cash flow statement.
const (
	CashFlowIndirect = "INDIRECT"
	CashFlowDirect   = "DIRECT"
)

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
		Range: r, Method: CashFlowIndirect, Lines: lines, NetIncome: is.NetIncome, Operating: op, Investing: inv, Financing: fin, Unclassified: unc, NetChange: net,
		OpeningCash: cashOpen, ClosingCash: cashClose, Difference: diff, Reconciled: diff.IsZero(),
	}, nil
}

// directCategory is where the cash a counterpart account explains is shown in the direct method.
type directCategory struct{ key, title, activity string }

var (
	catReceipts   = directCategory{"RECEIPTS", "Cash received from guests and customers", "OPERATING"}
	catPayments   = directCategory{"PAYMENTS", "Cash paid to suppliers, employees and for operating expenses", "OPERATING"}
	catTaxes      = directCategory{"TAXES_PAID", "Taxes and service charges paid", "OPERATING"}
	catInterest   = directCategory{"INTEREST_PAID", "Interest paid", "OPERATING"}
	catFixed      = directCategory{"FIXED_ASSETS", "Purchase and sale of property and equipment", "INVESTING"}
	catDebt       = directCategory{"LONG_TERM_DEBT", "Loans received and repaid", "FINANCING"}
	catEquity     = directCategory{"EQUITY", "Capital contributed and drawings", "FINANCING"}
	catUnclassify = directCategory{"UNCLASSIFIED", "Unclassified", "UNCLASSIFIED"}
)

// categoryOf places an account of a journal that moved cash by its statement group.
func categoryOf(group string) directCategory {
	switch group {
	case "RECEIVABLES", "DEPOSITS", "REV_ROOMS", "REV_FB", "REV_OOD", "REV_RENTAL_OTHER", "REV_MISC":
		return catReceipts
	case "EXP_ROOMS", "EXP_FB", "EXP_OOD", "UND_AG", "UND_IT", "UND_SM", "UND_POM", "UND_UTIL", "MGMT_FEES", "NONOP", "DEPRECIATION",
		"PAYABLES", "INVENTORIES", "PREPAID", "ACCRUED", "OTHER_ASSETS", "OTHER_CURRENT_LIABILITIES", "SUSPENSE":
		return catPayments
	case "TAXES_PAYABLE", "INCOME_TAX":
		return catTaxes
	case "INTEREST":
		return catInterest
	case "FIXED_ASSETS":
		return catFixed
	case "LONG_TERM_DEBT":
		return catDebt
	case "EQUITY":
		return catEquity
	}
	return catUnclassify
}

// CashFlowDirect is the cash flow statement of a range by the direct method (accounting.view): the cash the hotel received and paid, in the categories of its
// operating, investing and financing activities. It is read from the journals that touch a cash account (group CASH): the other accounts on such a journal explain
// the cash it moved (a revenue or receivable credited against cash debited is cash received; an expense or a payable debited against cash credited is cash paid), and
// the accounts are listed under their category. A move between two cash accounts is not an inflow or an outflow and is left out. The result is proved against the
// change of the cash accounts like the indirect statement.
func (s *Service) CashFlowDirect(ctx context.Context, propertyID int64, from, to *civil.Date) (CashFlow, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return CashFlow{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return CashFlow{}, err
	}
	q := s.q(ctx)
	rows, err := q.CashEffectByCounterpart(ctx, accountingdb.CashEffectByCounterpartParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: r.From, ToDate: r.To})
	if err != nil {
		return CashFlow{}, err
	}
	cash := func(asOf civil.Date) (decimal.Decimal, error) {
		bal, err := q.AccountBalances(ctx, accountingdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, ToDate: asOf})
		if err != nil {
			return decimal.Zero, err
		}
		sum := decimal.Zero
		for _, x := range bal {
			if deref(x.StatementGroup) == "CASH" {
				sum = sum.Add(x.Balance)
			}
		}
		return sum, nil
	}
	opening, err := cash(r.From.AddDays(-1))
	if err != nil {
		return CashFlow{}, err
	}
	closing, err := cash(r.To)
	if err != nil {
		return CashFlow{}, err
	}
	accounts := map[string][]StatementAccount{}
	amounts := map[string]decimal.Decimal{}
	for _, x := range rows {
		c := categoryOf(x.StatementGroup)
		accounts[c.key] = append(accounts[c.key], StatementAccount{AccountID: x.ID, Code: x.Code, Name: x.Name, Amount: x.Effect})
		amounts[c.key] = amounts[c.key].Add(x.Effect)
	}
	var lines []StatementLine
	section := func(activity, headKey, headTitle, totalKey, totalTitle string, cats ...directCategory) decimal.Decimal {
		lines = append(lines, StatementLine{Key: headKey, Title: headTitle, Kind: "HEADING", Accounts: []StatementAccount{}})
		sum := decimal.Zero
		for _, c := range cats {
			if len(accounts[c.key]) == 0 {
				continue
			}
			lines = append(lines, StatementLine{Key: c.key, Title: c.title, Kind: "GROUP", Amount: amounts[c.key], Accounts: accounts[c.key]})
			sum = sum.Add(amounts[c.key])
		}
		lines = append(lines, StatementLine{Key: totalKey, Title: totalTitle, Kind: "SUBTOTAL", Amount: sum, Accounts: []StatementAccount{}})
		return sum
	}
	op := section("OPERATING", "H_OPERATING", "Operating activities", "OPERATING", "Net cash from operating activities", catReceipts, catPayments, catTaxes, catInterest)
	inv := section("INVESTING", "H_INVESTING", "Investing activities", "INVESTING", "Net cash from investing activities", catFixed)
	fin := section("FINANCING", "H_FINANCING", "Financing activities", "FINANCING", "Net cash from financing activities", catDebt, catEquity)
	unc := decimal.Zero
	if len(accounts[catUnclassify.key]) > 0 {
		lines = append(lines, StatementLine{Key: "H_UNCLASSIFIED", Title: "Unclassified (accounts without a statement group)", Kind: "HEADING", Accounts: []StatementAccount{}})
		unc = amounts[catUnclassify.key]
		lines = append(lines, StatementLine{Key: catUnclassify.key, Title: catUnclassify.title, Kind: "GROUP", Amount: unc, Accounts: accounts[catUnclassify.key]})
	}
	net := op.Add(inv).Add(fin).Add(unc)
	lines = append(lines,
		StatementLine{Key: "NET_CHANGE", Title: "Net change in cash", Kind: "TOTAL", Amount: net, Accounts: []StatementAccount{}},
		StatementLine{Key: "OPENING_CASH", Title: "Cash at the start of the period", Kind: "GROUP", Amount: opening, Accounts: []StatementAccount{}},
		StatementLine{Key: "CLOSING_CASH", Title: "Cash at the end of the period", Kind: "TOTAL", Amount: closing, Accounts: []StatementAccount{}},
	)
	diff := closing.Sub(opening).Sub(net)
	return CashFlow{
		Range: r, Method: CashFlowDirect, Lines: lines, Operating: op, Investing: inv, Financing: fin, Unclassified: unc, NetChange: net,
		OpeningCash: opening, ClosingCash: closing, Difference: diff, Reconciled: diff.IsZero(),
	}, nil
}
