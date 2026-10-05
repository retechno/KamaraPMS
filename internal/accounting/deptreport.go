package accounting

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// DepartmentAccount is what one revenue or expense account posted directly to a department, on the normal side of the account.
type DepartmentAccount struct {
	AccountID   int64           `json:"account_id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	AccountType string          `json:"account_type"`
	Amount      decimal.Decimal `json:"amount"`
}

// DepartmentNode is a department or a sub-department with its revenue, its expenses and its departmental profit. A department adds up its own postings and those of its
// sub-departments (Revenue, Expense, Profit); OwnRevenue and OwnExpense are what was posted to it directly. Accounts are the direct postings by account.
type DepartmentNode struct {
	ID         int64               `json:"id"`
	ParentID   *int64              `json:"parent_id"`
	Code       string              `json:"code"`
	Name       string              `json:"name"`
	Level      int                 `json:"level"`
	Active     bool                `json:"is_active"`
	Revenue    decimal.Decimal     `json:"revenue"`
	Expense    decimal.Decimal     `json:"expense"`
	Profit     decimal.Decimal     `json:"profit"`
	OwnRevenue decimal.Decimal     `json:"own_revenue"`
	OwnExpense decimal.Decimal     `json:"own_expense"`
	Accounts   []DepartmentAccount `json:"accounts"`
	Children   []DepartmentNode    `json:"children"`
}

// DepartmentTotals adds up everything in the range, the unassigned too.
type DepartmentTotals struct {
	Revenue decimal.Decimal `json:"revenue"`
	Expense decimal.Decimal `json:"expense"`
	Profit  decimal.Decimal `json:"profit"`
}

// DepartmentReport is the revenue and the expenses of a range by department.
type DepartmentReport struct {
	Range
	Departments []DepartmentNode `json:"departments"`
	Unassigned  DepartmentNode   `json:"unassigned"`
	Totals      DepartmentTotals `json:"totals"`
}

// DepartmentReport adds up the revenue and expense accounts of the journals of a range (closing journals left out, the source of the income statement) by the department of the
// line (accounting.view). A department includes its sub-departments, the lines with no department are shown apart as unassigned, and the totals are those of the income
// statement. Only the departments in use, or with something posted, are listed; departmentID narrows the report to one department (or one sub-department).
func (s *Service) DepartmentReport(ctx context.Context, propertyID int64, from, to *civil.Date, departmentID *int64) (DepartmentReport, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return DepartmentReport{}, err
	}
	r, err := s.resolveRange(ctx, propertyID, from, to)
	if err != nil {
		return DepartmentReport{}, err
	}
	q := s.q(ctx)
	depts, err := q.ListDepartmentsFlat(ctx, accountingdb.ListDepartmentsFlatParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return DepartmentReport{}, err
	}
	known := map[int64]*DepartmentNode{}
	var order []int64
	for _, d := range depts {
		level := 1
		if d.ParentID != nil {
			level = 2
		}
		known[d.ID] = &DepartmentNode{ID: d.ID, ParentID: d.ParentID, Code: d.Code, Name: d.Name, Level: level, Active: d.IsActive, Accounts: []DepartmentAccount{}, Children: []DepartmentNode{}}
		order = append(order, d.ID)
	}
	if departmentID != nil {
		if _, ok := known[*departmentID]; !ok {
			return DepartmentReport{}, apperr.NotFound("DEPARTMENT_NOT_FOUND", "the department does not exist in this property")
		}
	}
	rows, err := q.DepartmentActivity(ctx, accountingdb.DepartmentActivityParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: r.From, ToDate: r.To})
	if err != nil {
		return DepartmentReport{}, err
	}
	unassigned := DepartmentNode{Code: "", Name: "", Level: 1, Active: true, Accounts: []DepartmentAccount{}, Children: []DepartmentNode{}}
	total := DepartmentTotals{}
	for _, x := range rows {
		amount := x.Balance // debit less credit
		node := &unassigned
		if x.DepartmentID != 0 {
			node = known[x.DepartmentID]
			if node == nil {
				continue // a department of no property of the tenant cannot be: the foreign key says so
			}
		}
		if x.AccountType == TypeRevenue {
			amount = amount.Neg() // revenue is a credit
		}
		node.Accounts = append(node.Accounts, DepartmentAccount{AccountID: x.ID, Code: x.Code, Name: x.Name, AccountType: x.AccountType, Amount: amount})
		if x.AccountType == TypeRevenue {
			node.OwnRevenue = node.OwnRevenue.Add(amount)
			total.Revenue = total.Revenue.Add(amount)
		} else {
			node.OwnExpense = node.OwnExpense.Add(amount)
			total.Expense = total.Expense.Add(amount)
		}
	}
	total.Profit = total.Revenue.Sub(total.Expense)

	// sub-departments go under their department, which adds them up
	for _, id := range order {
		n := known[id]
		n.Revenue, n.Expense = n.OwnRevenue, n.OwnExpense
	}
	var tops []DepartmentNode
	for _, id := range order {
		n := known[id]
		if n.ParentID == nil {
			continue
		}
		parent := known[*n.ParentID]
		if parent == nil {
			continue
		}
		parent.Revenue, parent.Expense = parent.Revenue.Add(n.Revenue), parent.Expense.Add(n.Expense)
		if shows(*n) {
			n.Profit = n.Revenue.Sub(n.Expense)
			parent.Children = append(parent.Children, *n)
		}
	}
	for _, id := range order {
		n := known[id]
		if n.ParentID != nil {
			continue
		}
		n.Profit = n.Revenue.Sub(n.Expense)
		if shows(*n) || len(n.Children) > 0 {
			tops = append(tops, *n)
		}
	}
	unassigned.Revenue, unassigned.Expense = unassigned.OwnRevenue, unassigned.OwnExpense
	unassigned.Profit = unassigned.Revenue.Sub(unassigned.Expense)
	if departmentID != nil {
		tops = narrow(tops, *departmentID)
		unassigned = DepartmentNode{Accounts: []DepartmentAccount{}, Children: []DepartmentNode{}} // a department asked for: no unassigned line
		total = DepartmentTotals{}
		for _, t := range tops {
			total.Revenue, total.Expense = total.Revenue.Add(t.Revenue), total.Expense.Add(t.Expense)
		}
		total.Profit = total.Revenue.Sub(total.Expense)
	}
	for i := range tops {
		sortAccounts(tops[i].Accounts)
		for j := range tops[i].Children {
			sortAccounts(tops[i].Children[j].Accounts)
		}
	}
	sortAccounts(unassigned.Accounts)
	if tops == nil {
		tops = []DepartmentNode{}
	}
	return DepartmentReport{Range: r, Departments: tops, Unassigned: unassigned, Totals: total}, nil
}

// shows says whether a department is listed: one in use, or with something posted to it.
func shows(n DepartmentNode) bool {
	return n.Active || len(n.Accounts) > 0 || !n.Revenue.IsZero() || !n.Expense.IsZero()
}

// narrow keeps the department asked for, or the department that holds the sub-department asked for, narrowed to that sub-department.
func narrow(tops []DepartmentNode, id int64) []DepartmentNode {
	for _, t := range tops {
		if t.ID == id {
			return []DepartmentNode{t}
		}
		for _, c := range t.Children {
			if c.ID == id {
				return []DepartmentNode{c}
			}
		}
	}
	return []DepartmentNode{}
}

func sortAccounts(list []DepartmentAccount) {
	sort.Slice(list, func(i, j int) bool { return list[i].Code < list[j].Code })
}
