package accounting

import (
	"context"
	"fmt"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// Severities of an issue of the department setup.
const (
	SetupError   = "ERROR"   // the posting would be refused: the day close or a charge would stop
	SetupWarning = "WARNING" // nothing is refused, but the setting has no effect
)

// SetupIssue is one thing the department setup of the property gets wrong: a source that posts to an account on its own (SourceType: CHARGE_CODE, TAX, SERVICE_CHARGE or SYSTEM) next
// to what the rule of the account says.
type SetupIssue struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"` // DEPARTMENT_MISSING, DEFAULT_SWITCHED_OFF or DEPARTMENT_IGNORED
	SourceType  string `json:"source_type"`
	SourceRef   string `json:"source_ref"`
	SourceName  string `json:"source_name"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	Message     string `json:"message"`
}

// DepartmentSetupReport is the check of the department setup (the configuration validation before a night audit meets it).
type DepartmentSetupReport struct {
	OK     bool         `json:"ok"` // no ERROR
	Issues []SetupIssue `json:"issues"`
}

func sourceNoun(t string) string {
	switch t {
	case "CHARGE_CODE":
		return "charge code"
	case "TAX":
		return "tax"
	case "SERVICE_CHARGE":
		return "service charge"
	}
	return "system account"
}

// setupIssues lists what the department setup gets wrong, for the whole property or for one account.
func (s *Service) setupIssues(ctx context.Context, tenantID, propertyID int64, account *int64) ([]SetupIssue, error) {
	rows, err := s.q(ctx).DepartmentSetupSources(ctx, accountingdb.DepartmentSetupSourcesParams{TenantID: tenantID, PropertyID: propertyID, AccountID: account})
	if err != nil {
		return nil, err
	}
	out := []SetupIssue{}
	for _, r := range rows {
		issue := SetupIssue{SourceType: r.SourceType, SourceRef: r.SourceRef, SourceName: r.SourceName, AccountID: r.AccountID, AccountCode: r.AccountCode, AccountName: r.AccountName}
		label := fmt.Sprintf("%s %s", sourceNoun(r.SourceType), r.SourceRef)
		switch {
		case r.Requirement == DeptRequired && !r.HasOwn && !r.DefaultOk && r.DefaultDepartmentID != 0:
			issue.Severity, issue.Code = SetupError, "DEFAULT_SWITCHED_OFF"
			issue.Message = fmt.Sprintf("account %s - %s requires a department and its default department is switched off, so %s has none", r.AccountCode, r.AccountName, label)
		case r.Requirement == DeptRequired && !r.HasOwn && !r.DefaultOk:
			issue.Severity, issue.Code = SetupError, "DEPARTMENT_MISSING"
			issue.Message = fmt.Sprintf("account %s - %s requires a department, but no department is configured for %s", r.AccountCode, r.AccountName, label)
		case r.Requirement == DeptNone && r.HasOwn:
			issue.Severity, issue.Code = SetupWarning, "DEPARTMENT_IGNORED"
			issue.Message = fmt.Sprintf("account %s - %s takes no department, so the department of %s is not used", r.AccountCode, r.AccountName, label)
		default:
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

// requireSetup refuses a change that would leave a required department without a source (409 DEPARTMENT_SETUP_INCOMPLETE, with the first issue as context). It runs after the change
// inside the transaction, so a refusal rolls it back.
func (s *Service) requireSetup(ctx context.Context, tenantID, propertyID int64, account *int64) error {
	issues, err := s.setupIssues(ctx, tenantID, propertyID, account)
	if err != nil {
		return err
	}
	for _, i := range issues {
		if i.Severity == SetupError {
			return apperr.Conflict("DEPARTMENT_SETUP_INCOMPLETE", i.Message).WithContext("source_type", i.SourceType).WithContext("source_ref", i.SourceRef).WithContext("account_code", i.AccountCode)
		}
	}
	return nil
}

// DepartmentSetup checks the department setup of the property (accounting.view): every account that requires a department can find one for each charge code, tax, service charge and
// system account that posts to it, so that the night audit and the postings never meet a line without one.
func (s *Service) DepartmentSetup(ctx context.Context, propertyID int64) (DepartmentSetupReport, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return DepartmentSetupReport{}, err
	}
	issues, err := s.setupIssues(ctx, p.TenantID, propertyID, nil)
	if err != nil {
		return DepartmentSetupReport{}, err
	}
	rep := DepartmentSetupReport{OK: true, Issues: issues}
	for _, i := range issues {
		if i.Severity == SetupError {
			rep.OK = false
		}
	}
	return rep, nil
}
