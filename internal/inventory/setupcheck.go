package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/civil"
)

// Severities of a finding of the setup check.
const (
	SeverityError   = "ERROR"   // the close of the day would stop, or the books are wrong
	SeverityWarning = "WARNING" // nothing is refused, but the setup is not finished or something is odd
)

// Setup keys the stock needs in the account map (35-inventory.md F14).
var inventoryKeys = []string{"INV_GRNI", "INV_PPV", "INV_WASTE", "INV_COUNT_VARIANCE", "INV_COGS_FOOD", "INV_COGS_BEVERAGE", "INV_STAFF_MEAL", "INV_ENTERTAINMENT"}

// SetupIssue is one finding of the setup check.
type SetupIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Ref      string `json:"ref,omitempty"`
	// Params fill the translation of the code (the web translates a finding, 35-inventory.md 5.7); Message is the English sentence.
	Params map[string]string `json:"params,omitempty"`
}

// SetupReport is the answer of the setup check: the state of the module and what is left to do.
type SetupReport struct {
	OK              bool         `json:"ok"` // no ERROR
	CostingMethod   string       `json:"costing_method"`
	CostingLockedOn *civil.Date  `json:"costing_locked_on"`
	Warehouses      int          `json:"warehouses"`
	Categories      int          `json:"categories"`
	Items           int          `json:"items"`
	Issues          []SetupIssue `json:"issues"`
}

// SetupCheck says what is left to set up and whether the books agree with the stock (inventory.view): the checklist of the first steps, the keys of the account map, the drift check
// and the reconciliation of the general ledger.
func (s *Service) SetupCheck(ctx context.Context, propertyID int64) (SetupReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return SetupReport{}, err
	}
	q := s.q(ctx)
	rep := SetupReport{CostingMethod: MethodAverage, Issues: []SetupIssue{}}
	cfg, err := q.GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: "COSTING_METHOD_NOT_CHOSEN", Message: "the costing method has not been confirmed: it is average until you save it, and it locks at the first stock movement"})
	case err != nil:
		return rep, err
	default:
		rep.CostingMethod, rep.CostingLockedOn = cfg.CostingMethod, cfg.CostingLockedOn
	}
	n, err := q.CountActive(ctx, inventorydb.CountActiveParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return rep, err
	}
	rep.Warehouses, rep.Categories, rep.Items = int(n.Warehouses), int(n.Categories), int(n.Items)
	for _, c := range []struct {
		n    int
		code string
		what string
	}{{rep.Categories, "NO_CATEGORY", "category with its accounts"}, {rep.Warehouses, "NO_WAREHOUSE", "warehouse"}, {rep.Items, "NO_ITEM", "item"}} {
		if c.n == 0 {
			rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: c.code, Message: "there is no " + c.what + " yet"})
		}
	}
	mapped, err := q.MappedInventoryKeys(ctx, inventorydb.MappedInventoryKeysParams{TenantID: p.TenantID, PropertyID: propertyID, Keys: inventoryKeys})
	if err != nil {
		return rep, err
	}
	for _, k := range inventoryKeys {
		if !slices.Contains(mapped, k) {
			rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: "ACCOUNT_MAP_INCOMPLETE", Ref: k, Message: fmt.Sprintf("the system account %s is not mapped (Setup > Account mapping)", k), Params: map[string]string{"key": k}})
		}
	}
	leaders, err := q.InactiveDepartmentLeaders(ctx, inventorydb.InactiveDepartmentLeadersParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return rep, err
	}
	for _, l := range leaders {
		role := "deputy"
		if l.IsHead {
			role = "head"
		}
		rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: "DEPARTMENT_HEAD_INACTIVE", Ref: l.Code, Message: fmt.Sprintf("%s is the %s of the department %s and is not active: choose another in Departments", l.FullName, role, l.Code), Params: map[string]string{"name": l.FullName, "role": role, "department": l.Code}})
	}
	for _, doc := range []string{DocPR, DocSR} {
		if _, err := q.GetApprovalSettings(ctx, inventorydb.GetApprovalSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc}); err != nil {
			continue // the chain has not been made yet: it is the default
		}
		if n, err := q.CountApprovalLevels(ctx, inventorydb.CountApprovalLevelsParams{TenantID: p.TenantID, PropertyID: propertyID, DocType: doc}); err != nil {
			return rep, err
		} else if n == 0 {
			rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: "APPROVAL_CHAIN_EMPTY", Ref: doc, Message: "the chain of " + doc + " has no level: a document is approved when it is submitted", Params: map[string]string{"doc": doc}})
		}
	}
	mism, err := q.ListFbClassMismatches(ctx, inventorydb.ListFbClassMismatchesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return rep, err
	}
	for _, m := range mism {
		acct := ""
		if m.Account != nil {
			acct = *m.Account
		}
		rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: FindFbClassRevenue, Ref: m.Source + ":" + m.Ref,
			Message: fmt.Sprintf("%s %s (%s) is %s but its charge code %s posts the revenue to %s: the cost reports follow the class and the general ledger follows the account", m.Source, m.Ref, m.Name, m.Class, m.ChargeCode, acct),
			Params:  map[string]string{"source": m.Source, "ref": m.Ref, "name": m.Name, "class": m.Class, "charge_code": m.ChargeCode, "account": acct}})
	}
	blockers, warnings, err := s.NightAuditStock(ctx, p.TenantID, propertyID, civil.Date{})
	if err != nil {
		return rep, err
	}
	for _, f := range blockers {
		rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityError, Code: f.Code, Message: f.Message, Ref: f.Ref, Params: f.Params})
	}
	for _, f := range warnings {
		rep.Issues = append(rep.Issues, SetupIssue{Severity: SeverityWarning, Code: f.Code, Message: f.Message, Ref: f.Ref, Params: f.Params})
	}
	rep.OK = true
	for _, i := range rep.Issues {
		if i.Severity == SeverityError {
			rep.OK = false
		}
	}
	return rep, nil
}
