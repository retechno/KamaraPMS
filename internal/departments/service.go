package departments

import (
	"context"
	"strings"

	"kamarapms/internal/audit"
	"kamarapms/internal/departments/departmentsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service is the department application service. Reading needs accounting.view; changing the master accounting.manage.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *departmentsdb.Queries {
	return departmentsdb.New(s.txm.DB(ctx))
}

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func errNotFound() *apperr.Error {
	return apperr.NotFound("DEPARTMENT_NOT_FOUND", "the department does not exist in this property")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "department", EntityID: id, Old: old, New: updated}
}

func toDepartment(r departmentsdb.ListDepartmentsRow) Department {
	d := Department{
		ID: r.ID, ParentID: r.ParentID, ParentCode: deref(r.ParentCode), Code: r.Code, Name: r.Name, SortOrder: int(r.SortOrder), Active: r.IsActive,
		Level: 1, Children: int(r.ChildCount), InUse: r.InUse,
	}
	if r.ParentID != nil {
		d.Level = 2
	}
	return d
}

func (s *Service) list(ctx context.Context, tenantID, propertyID int64, id *int64, active *bool) ([]Department, error) {
	rows, err := s.q(ctx).ListDepartments(ctx, departmentsdb.ListDepartmentsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: active})
	if err != nil {
		return nil, err
	}
	out := make([]Department, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDepartment(r))
	}
	return out, nil
}

// List answers the departments, each department followed by its sub-departments (accounting.view).
func (s *Service) List(ctx context.Context, propertyID int64, f Filter) ([]Department, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, p.TenantID, propertyID, nil, f.Active)
}

// Get answers one department (accounting.view).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Department, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return Department{}, err
	}
	list, err := s.list(ctx, p.TenantID, propertyID, &id, nil)
	if err != nil {
		return Department{}, err
	}
	if len(list) == 0 {
		return Department{}, errNotFound()
	}
	return list[0], nil
}

// Check says whether a department takes a new posting: it exists in the property and is in use. The field is where the caller reports the error. The
// caller is in its own transaction; nothing is locked, so a department switched off at the very moment can still take that one line.
func (s *Service) Check(ctx context.Context, tenantID, propertyID, id int64, field string) error {
	list, err := s.list(ctx, tenantID, propertyID, &id, nil)
	if err != nil {
		return err
	}
	switch {
	case len(list) == 0:
		return apperr.Invalid("the department is invalid", fieldErr(field, "DEPARTMENT_NOT_FOUND", "no such department in this property"))
	case !list[0].Active:
		return apperr.Invalid("the department is invalid", fieldErr(field, "INACTIVE", "the department is switched off"))
	}
	return nil
}

// Create adds a department or a sub-department (accounting.manage).
func (s *Service) Create(ctx context.Context, propertyID int64, in Input) (Department, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return Department{}, err
	}
	code, name := normalCode(in.Code), strings.TrimSpace(in.Name)
	var fields []apperr.FieldError
	if !codePattern.MatchString(code) {
		fields = append(fields, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, . _ -"))
	}
	if n := len([]rune(name)); n < 1 || n > maxName {
		fields = append(fields, fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
	}
	if in.SortOrder < 0 || in.SortOrder > maxSort {
		fields = append(fields, fieldErr("sort_order", "INVALID_VALUE", "0 to 100000"))
	}
	if len(fields) > 0 {
		return Department{}, apperr.Invalid("the department is invalid", fields...)
	}
	var out Department
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if in.ParentID != nil {
			parents, err := s.list(ctx, p.TenantID, propertyID, in.ParentID, nil)
			if err != nil {
				return err
			}
			switch {
			case len(parents) == 0:
				return apperr.Invalid("the department is invalid", fieldErr("parent_id", "DEPARTMENT_NOT_FOUND", "no such department in this property"))
			case parents[0].ParentID != nil:
				return apperr.Invalid("the department is invalid", fieldErr("parent_id", "TOO_DEEP", "a sub-department has no sub-departments: at most two levels"))
			case !parents[0].Active:
				return apperr.Invalid("the department is invalid", fieldErr("parent_id", "INACTIVE", "the department is switched off"))
			}
		}
		id, err := s.q(ctx).InsertDepartment(ctx, departmentsdb.InsertDepartmentParams{
			TenantID: p.TenantID, PropertyID: propertyID, ParentID: in.ParentID, Code: code, Name: name, SortOrder: int32(in.SortOrder), ActorID: p.ActorID(), //nolint:gosec // G115: bounded above
		})
		if err != nil {
			return err
		}
		list, err := s.list(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "department.created", id, nil, map[string]any{"code": code, "name": name, "parent_id": in.ParentID}))
	})
	return out, err
}

// Update changes the name, the order or the use of a department (accounting.manage). A department with active sub-departments is not switched off.
func (s *Service) Update(ctx context.Context, propertyID, id int64, patch Patch) (Department, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return Department{}, err
	}
	var out Department
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		list, err := s.list(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return errNotFound()
		}
		old := list[0]
		next := old
		if patch.Name != nil {
			next.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.SortOrder != nil {
			next.SortOrder = *patch.SortOrder
		}
		if patch.Active != nil {
			next.Active = *patch.Active
		}
		var fields []apperr.FieldError
		if n := len([]rune(next.Name)); n < 1 || n > maxName {
			fields = append(fields, fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
		}
		if next.SortOrder < 0 || next.SortOrder > maxSort {
			fields = append(fields, fieldErr("sort_order", "INVALID_VALUE", "0 to 100000"))
		}
		if len(fields) > 0 {
			return apperr.Invalid("the department is invalid", fields...)
		}
		if old.Active && !next.Active && old.Children > 0 {
			all, err := s.list(ctx, p.TenantID, propertyID, nil, nil)
			if err != nil {
				return err
			}
			for _, c := range all {
				if c.ParentID != nil && *c.ParentID == id && c.Active {
					return apperr.Conflict("DEPARTMENT_HAS_ACTIVE_CHILDREN", "switch off its sub-departments first")
				}
			}
		}
		if !old.Active && next.Active && old.ParentID != nil {
			parents, err := s.list(ctx, p.TenantID, propertyID, old.ParentID, nil)
			if err != nil {
				return err
			}
			if len(parents) == 1 && !parents[0].Active {
				return apperr.Conflict("DEPARTMENT_PARENT_INACTIVE", "switch on the department it belongs to first")
			}
		}
		if err := s.q(ctx).UpdateDepartment(ctx, departmentsdb.UpdateDepartmentParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: next.Name, SortOrder: int32(next.SortOrder), IsActive: next.Active, ActorID: p.ActorID(), //nolint:gosec // G115: bounded above
		}); err != nil {
			return err
		}
		after, err := s.list(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = after[0]
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "department.updated", id,
			map[string]any{"name": old.Name, "sort_order": old.SortOrder, "is_active": old.Active}, map[string]any{"name": out.Name, "sort_order": out.SortOrder, "is_active": out.Active}))
	})
	return out, err
}

// Delete removes a department that nothing refers to (accounting.manage); one that was used is switched off instead.
func (s *Service) Delete(ctx context.Context, propertyID, id int64) error {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		list, err := s.list(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return errNotFound()
		}
		d := list[0]
		if d.Children > 0 {
			return apperr.Conflict("DEPARTMENT_IN_USE", "the department has sub-departments").WithContext("sub_departments", d.Children)
		}
		if d.InUse {
			return apperr.Conflict("DEPARTMENT_IN_USE", "something was posted to this department or points to it: switch it off instead")
		}
		if err := s.q(ctx).DeleteDepartment(ctx, departmentsdb.DeleteDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "department.deleted", id, map[string]any{"code": d.Code, "name": d.Name}, nil))
	})
}

// SeedProperty gives a new property the standard departments (the hook of tenancy.CreateProperty), and the default department of its standard charge codes.
func (s *Service) SeedProperty(ctx context.Context, in tenancy.PropertyCreated) error {
	n, err := s.q(ctx).SeedDepartments(ctx, departmentsdb.SeedDepartmentsParams{TenantID: in.TenantID, PropertyID: in.PropertyID, ActorID: in.ActorID})
	if err != nil {
		return err
	}
	bd := in.BusinessDate
	return s.audit.Write(ctx, audit.Entry{
		TenantID: in.TenantID, PropertyID: &in.PropertyID, BusinessDate: &bd, UserID: in.ActorID,
		Action: "department.seeded", EntityType: "property", EntityID: in.PropertyID, New: map[string]any{"departments": n},
	})
}
