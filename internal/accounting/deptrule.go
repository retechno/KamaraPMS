package accounting

import (
	"context"
	"fmt"
	"slices"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/platform/apperr"
)

// The department rule of an account (design: docs/architecture/12-departments.md). It is the final gate of every journal the books take: the manual journal, a journal another module posts
// (the Poster), the journal of the day close. A reversal copies the lines of what it reverses and a closing entry follows the balances, so neither goes through it.
const (
	DeptNone     = "NONE"     // a line of the account has no department
	DeptOptional = "OPTIONAL" // it may have one (the default)
	DeptRequired = "REQUIRED" // every new line names one, or the posting is refused

	// ErrDepartmentRequired is the field error code of a line that has no department where the account needs one.
	ErrDepartmentRequired = "DEPARTMENT_REQUIRED"
	// ErrDepartmentNotAllowed is that of a line that names a department on an account that takes none.
	ErrDepartmentNotAllowed = "DEPARTMENT_NOT_ALLOWED"
)

var deptRequirements = []string{DeptNone, DeptOptional, DeptRequired}

func validRequirement(r string) bool { return slices.Contains(deptRequirements, r) }

// lineDepartment applies the department rule of an account to one line and returns the department the line is posted with.
//
//   - NONE: the line has none. A department that is named (explicit) is refused, unless the caller did not choose it (derived: the day close), where it is dropped.
//   - OPTIONAL and REQUIRED: the department named, else the default of the account when it is in use.
//   - REQUIRED with neither: 422 DEPARTMENT_REQUIRED, never a line without a department.
//
// source says what the line comes from, for the message ("charge code ROOM").
func lineDepartment(a accountingdb.ListAccountsRow, explicit *int64, derived bool, field, source string) (*int64, error) {
	switch a.DepartmentRequirement {
	case DeptNone:
		if explicit != nil && !derived {
			return nil, apperr.Invalid("the department is not allowed", fieldErr(field, ErrDepartmentNotAllowed,
				fmt.Sprintf("account %s - %s takes no department", a.Code, a.Name)))
		}
		return nil, nil
	}
	dept := explicit
	if dept == nil && a.DefaultDepartmentID != nil && a.DefaultDepartmentActive {
		dept = a.DefaultDepartmentID
	}
	if dept == nil && a.DepartmentRequirement == DeptRequired {
		msg := fmt.Sprintf("account %s - %s requires a department", a.Code, a.Name)
		switch {
		case a.DefaultDepartmentID != nil:
			msg += ", and its default department is switched off"
		case source != "":
			msg += ", but no department is configured for " + source
		}
		return nil, apperr.Invalid("the department is required", fieldErr(field, ErrDepartmentRequired, msg)).WithContext("account_code", a.Code)
	}
	return dept, nil
}

// validRule checks the rule an account is given: a known requirement, and no default department on an account that takes none.
func validRule(requirement string, def *int64) []apperr.FieldError {
	switch {
	case !validRequirement(requirement):
		return []apperr.FieldError{fieldErr("department_requirement", "INVALID_VALUE", "NONE, OPTIONAL or REQUIRED")}
	case requirement == DeptNone && def != nil:
		return []apperr.FieldError{fieldErr("default_department_id", ErrDepartmentNotAllowed, "an account that takes no department has no default department")}
	}
	return nil
}

// checkDefaultDepartment says whether a department can be the default of an account: it exists in the property and is in use.
func (s *Service) checkDefaultDepartment(ctx context.Context, tenantID, propertyID int64, id *int64) error {
	if id == nil || s.depts == nil {
		return nil
	}
	return s.depts.Check(ctx, tenantID, propertyID, *id, "default_department_id")
}

func sameID(a, b *int64) bool { return a != nil && b != nil && *a == *b }

func sameOrBothNil(a, b *int64) bool { return (a == nil && b == nil) || sameID(a, b) }

// ruleOrDefault is the requirement of an account, OPTIONAL for one that is not known yet.
func ruleOrDefault(r string) string {
	if r == "" {
		return DeptOptional
	}
	return r
}
