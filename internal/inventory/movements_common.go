package inventory

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// What the movements of PR 3 share (35-inventory.md 5.4): who may read and who may write, the lines of a document with their units, and the account and the department of a line of an issue.

// movementReaders may read the requisitions, the transfers, the issues and the counts: the module's permissions and the approvers of the chain.
var movementReaders = []auth.Permission{auth.PermInventoryView, auth.PermInventoryManage, auth.PermInventoryRequest, auth.PermInventoryPurchase, auth.PermInventoryReceive, auth.PermInventoryMove,
	auth.PermInventoryCount, auth.PermInventoryApprove, auth.PermInventoryDeptApprove, auth.PermInventoryCostControl, auth.PermInventoryFinalApprove, auth.PermInventorySRFinalApprove}

func (s *Service) canReadMovements(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, movementReaders...)
}

func (s *Service) canMove(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryMove)
}

func (s *Service) canCount(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryCount)
}

// newUUID makes a version 4 UUID for a document that the server writes itself (a reversing document, the amenities of a close).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system source of randomness failing is not something to continue from
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// docLine is a line a person typed: an item, a quantity in one of its units (the stock unit when none is named).
type docLine struct {
	ItemID           int64
	UnitID           *int64
	Qty              decimal.Decimal
	ExpenseAccountID *int64 // a line of an issue for a use may name its expense account
}

// docLineOut is a line that was checked: the item with the accounts of its category and the quantity in stock units.
type docLineOut struct {
	in       docLine
	item     inventorydb.ItemCategoryAccountsRow
	stockQty decimal.Decimal
}

// checkDocLines validates the lines of a document before anything is written: the item exists and is switched on, the unit is one of the item, the quantity is above zero with six decimals, an item is on
// the document once. It answers the lines with the quantity in stock units.
func (s *Service) checkDocLines(ctx context.Context, p auth.Principal, propertyID int64, what string, lines []docLine) ([]docLineOut, error) {
	if len(lines) == 0 || len(lines) > maxDocLines {
		return nil, invalid(what, fieldErr("lines", "OUT_OF_RANGE", "1 to "+itoa(maxDocLines)+" lines"))
	}
	q := s.q(ctx)
	ids := make([]int64, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ItemID)
	}
	items := map[int64]inventorydb.ItemCategoryAccountsRow{}
	rows, err := q.ItemCategoryAccounts(ctx, inventorydb.ItemCategoryAccountsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		items[r.ItemID] = r
	}
	units := map[[2]int64]inventorydb.ListItemUnitsOfRow{}
	us, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	for _, u := range us {
		units[[2]int64{u.ItemID, u.UnitID}] = u
	}
	stockUnit := map[int64]int64{}
	for _, id := range ids {
		if it, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err == nil {
			stockUnit[id] = it.StockUnitID
		}
	}
	var f []apperr.FieldError
	seen := map[int64]bool{}
	out := make([]docLineOut, 0, len(lines))
	for i, l := range lines {
		at := fmt.Sprintf("lines[%d]", i)
		it, ok := items[l.ItemID]
		switch {
		case !ok:
			f = append(f, fieldErr(at+".item_id", "NOT_FOUND", "no such item in this property"))
			continue
		case !it.IsActive:
			f = append(f, fieldErr(at+".item_id", "INACTIVE", "the item is switched off"))
			continue
		case seen[l.ItemID]:
			f = append(f, fieldErr(at+".item_id", "DUPLICATE", "an item is on the document once"))
			continue
		}
		seen[l.ItemID] = true
		unit := stockUnit[l.ItemID]
		if l.UnitID != nil {
			unit = *l.UnitID
		}
		u, ok := units[[2]int64{l.ItemID, unit}]
		if !ok {
			f = append(f, fieldErr(at+".unit_id", "NOT_FOUND", "not a unit of the item"))
			continue
		}
		if !l.Qty.IsPositive() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !l.Qty.Equal(Round6(l.Qty)) {
			f = append(f, fieldErr(at+".qty", "INVALID_VALUE", "above zero, six decimals at most"))
			continue
		}
		qty := StockQty(l.Qty, u.StockNum, u.StockDen)
		if !qty.IsPositive() {
			f = append(f, fieldErr(at+".qty", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
			continue
		}
		out = append(out, docLineOut{in: l, item: it, stockQty: qty})
	}
	if len(f) > 0 {
		return nil, invalid(what, f...)
	}
	return out, nil
}

// resolveDepartment applies the department rule of an account (document 12) to a line of an issue, the way the payables do for a bill line, so that a department that is required and missing is refused when
// the document is posted and never when the day closes. A department that is named on an account that takes none is refused unless the department was derived (from the warehouse), which is then dropped.
func (s *Service) resolveDepartment(ctx context.Context, p auth.Principal, propertyID, accountID int64, explicit *int64, derived bool, field string) (*int64, error) {
	a, err := s.q(ctx).AccountRule(ctx, inventorydb.AccountRuleParams{TenantID: p.TenantID, PropertyID: propertyID, ID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.Invalid("the account is invalid", fieldErr(field, "ACCOUNT_NOT_FOUND", "no such account in this property"))
	}
	if err != nil {
		return nil, err
	}
	if !a.IsActive || !a.IsPostable || a.AccountType != "EXPENSE" {
		return nil, apperr.Invalid("the account is invalid", fieldErr(field, "WRONG_ACCOUNT_TYPE", fmt.Sprintf("account %s - %s is not an active postable expense account", a.Code, a.Name)))
	}
	switch a.DepartmentRequirement {
	case "NONE":
		if explicit != nil && !derived {
			return nil, apperr.Invalid("the department is not allowed", fieldErr(field, "DEPARTMENT_NOT_ALLOWED", fmt.Sprintf("account %s - %s takes no department", a.Code, a.Name)))
		}
		return nil, nil
	}
	dept := explicit
	if dept == nil && a.DefaultDepartmentID != nil && a.DefaultDepartmentActive {
		dept = a.DefaultDepartmentID
	}
	if dept == nil && a.DepartmentRequirement == "REQUIRED" {
		return nil, apperr.Invalid("the department is required", fieldErr(field, "DEPARTMENT_REQUIRED", fmt.Sprintf("account %s - %s requires a department", a.Code, a.Name))).WithContext("account_code", a.Code)
	}
	return dept, nil
}

// keyAccount reads a system account by its key (INV_WASTE, INV_STAFF_MEAL, ...), refusing a property that has none.
func (s *Service) keyAccount(ctx context.Context, p auth.Principal, propertyID int64, key string) (int64, error) {
	a, err := s.q(ctx).GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: key})
	if err != nil {
		return 0, apperr.Conflict("ACCOUNT_KEY_MISSING", "the account for "+key+" is not set in this property").WithContext("key", key)
	}
	return a.ID, nil
}

// checkDepartmentExists says whether a department is one of this property's and in use.
func (s *Service) checkDepartmentExists(ctx context.Context, p auth.Principal, propertyID int64, id *int64, field string) error {
	if id == nil || s.depts == nil {
		return nil
	}
	return s.depts.Check(ctx, p.TenantID, propertyID, *id, field)
}

func errDocState(what, status, want string) *apperr.Error {
	return apperr.Conflict(what+"_STATE_INVALID", "the document is "+status+": this is for one that is "+want).WithContext("status", status)
}
