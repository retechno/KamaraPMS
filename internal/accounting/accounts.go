package accounting

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

const (
	maxAccounts  = 2000
	maxImportRow = 2000
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._:/-]{0,29}$`)

// Service is the accounting application service. Reading needs accounting.view; the chart of accounts and the system
// account map need accounting.manage; manual journals accounting.post; periods accounting.close.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	iam   *iam.Service
	depts *departments.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, iamSvc *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, iam: iamSvc}
}

// SetDepartments gives the service the departments, to check the department of a journal line.
func (s *Service) SetDepartments(d *departments.Service) { s.depts = d }

func (s *Service) q(ctx context.Context) *accountingdb.Queries {
	return accountingdb.New(s.txm.DB(ctx))
}

func errAccountNotFound() *apperr.Error {
	return apperr.NotFound("ACCOUNT_NOT_FOUND", "the account does not exist in this property")
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

// SeedProperty gives a new property the standard chart of accounts (the hook of tenancy.CreateProperty).
func (s *Service) SeedProperty(ctx context.Context, in tenancy.PropertyCreated) error {
	n, err := s.q(ctx).SeedChart(ctx, accountingdb.SeedChartParams{TenantID: in.TenantID, PropertyID: in.PropertyID, ActorID: in.ActorID, StartDate: in.BusinessDate})
	if err != nil {
		return err
	}
	if err := s.q(ctx).SeedRetainedEarningsMap(ctx, accountingdb.SeedRetainedEarningsMapParams{TenantID: in.TenantID, PropertyID: in.PropertyID, ActorID: in.ActorID}); err != nil {
		return err
	}
	bd := in.BusinessDate
	return s.audit.Write(ctx, audit.Entry{
		TenantID: in.TenantID, PropertyID: &in.PropertyID, BusinessDate: &bd, UserID: in.ActorID,
		Action: "accounting.chart_seeded", EntityType: "property", EntityID: in.PropertyID, EntityLabel: auditlabel.Property(ctx, in.PropertyID), New: map[string]any{"accounts": n},
	})
}

// lock takes the property's accounting settings row (lock level 46): FOR UPDATE to change the chart, the system accounts
// or a period, FOR SHARE to post a journal. It is the first lock these use cases take.
func (s *Service) lock(ctx context.Context, tenantID, propertyID int64, mode db.LockMode) (accountingdb.AccountingSetting, error) {
	cfg, err := s.q(ctx).GetSettings(ctx, accountingdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return cfg, settingsErr(err)
	}
	if err := db.LockRows(ctx, db.Accounting, mode, propertyID, []int64{cfg.ID}); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Service) businessDate(ctx context.Context, propertyID int64) (civil.Date, error) {
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	return day.BusinessDate, err
}

func toAccount(r accountingdb.ListAccountsRow) Account {
	return Account{
		ID: r.ID, Code: r.Code, Name: r.Name, AccountType: r.AccountType, NormalSide: r.NormalSide, ParentID: r.ParentID, ParentCode: deref(r.ParentCode),
		IsPostable: r.IsPostable, IsActive: r.IsActive, StatementGroup: deref(r.StatementGroup), Description: deref(r.Description), InUse: r.InUse, CreatedAt: r.CreatedAt,
		DepartmentRequirement: r.DepartmentRequirement, DefaultDepartmentID: r.DefaultDepartmentID, DefaultDepartmentCode: deref(r.DefaultDepartmentCode), DefaultDepartmentName: deref(r.DefaultDepartmentName),
	}
}

// Accounts lists the chart, by code (accounting.view). It is not paginated: a chart is a few hundred rows.
func (s *Service) Accounts(ctx context.Context, propertyID int64, f AccountFilter) ([]Account, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return nil, err
	}
	var fields []apperr.FieldError
	if f.AccountType != "" && !slices.Contains(accountTypes, f.AccountType) {
		fields = append(fields, fieldErr("account_type", "INVALID_VALUE", strings.Join(accountTypes, ", ")))
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the filter is invalid", fields...)
	}
	return s.list(ctx, p.TenantID, propertyID, f, nil)
}

func (s *Service) list(ctx context.Context, tenantID, propertyID int64, f AccountFilter, id *int64) ([]Account, error) {
	arg := accountingdb.ListAccountsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: f.Active, Postable: f.Postable, RowLimit: maxAccounts}
	if f.AccountType != "" {
		arg.AccountType = &f.AccountType
	}
	if f.StatementGroup != "" {
		arg.StatementGroup = &f.StatementGroup
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		arg.Q = &q
	}
	rows, err := s.q(ctx).ListAccounts(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(rows))
	for i, r := range rows {
		out[i] = toAccount(r)
	}
	return out, nil
}

func (s *Service) load(ctx context.Context, tenantID, propertyID, id int64) (Account, error) {
	rows, err := s.list(ctx, tenantID, propertyID, AccountFilter{}, &id)
	if err != nil {
		return Account{}, err
	}
	if len(rows) == 0 {
		return Account{}, errAccountNotFound()
	}
	return rows[0], nil
}

// Account returns one account (accounting.view).
func (s *Service) Account(ctx context.Context, propertyID, id int64) (Account, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return Account{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// validGroup checks a statement group against the account type; a postable account must have one, or the statements
// would leave it out.
func validGroup(accountType, group string, postable bool) []apperr.FieldError {
	switch {
	case group == "" && postable:
		return []apperr.FieldError{fieldErr("statement_group", "REQUIRED", "an account that takes postings needs a statement group")}
	case group != "" && !slices.Contains(groupsOf[accountType], group):
		return []apperr.FieldError{fieldErr("statement_group", "INVALID_VALUE", "one of "+strings.Join(groupsOf[accountType], ", "))}
	}
	return nil
}

func validateText(name, description string) []apperr.FieldError {
	var fields []apperr.FieldError
	if name == "" || len([]rune(name)) > 150 {
		fields = append(fields, fieldErr("name", "REQUIRED", "1-150 characters"))
	}
	if len([]rune(description)) > 300 {
		fields = append(fields, fieldErr("description", "TOO_LONG", "at most 300 characters"))
	}
	return fields
}

// checkParent applies the rules of the hierarchy to the parent an account is given: it exists, is a header (an account
// that takes postings has no children), is of the same type, and is not the account itself or one of its descendants.
func (s *Service) checkParent(ctx context.Context, tenantID, propertyID int64, self *int64, accountType string, parentID *int64) error {
	if parentID == nil {
		return nil
	}
	q := s.q(ctx)
	parent, err := q.GetAccountRow(ctx, accountingdb.GetAccountRowParams{TenantID: tenantID, PropertyID: propertyID, ID: *parentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Invalid("the account is invalid", fieldErr("parent_id", "NOT_FOUND", "an account of this property"))
	}
	if err != nil {
		return err
	}
	switch {
	case parent.IsPostable:
		return apperr.Invalid("the account is invalid", fieldErr("parent_id", "PARENT_IS_POSTABLE", "the parent must be a header account"))
	case parent.AccountType != accountType:
		return apperr.Invalid("the account is invalid", fieldErr("parent_id", "PARENT_TYPE_MISMATCH", "the parent is of another account type"))
	}
	if self != nil {
		up, err := q.AncestorIDs(ctx, accountingdb.AncestorIDsParams{PropertyID: propertyID, ID: *parentID})
		if err != nil {
			return err
		}
		if slices.Contains(up, *self) {
			return apperr.Invalid("the account is invalid", fieldErr("parent_id", "CIRCULAR", "an account cannot sit under itself"))
		}
	}
	return nil
}

// CreateAccount adds an account (accounting.manage). The code is upper-cased and unique per property (409 CODE_TAKEN).
func (s *Service) CreateAccount(ctx context.Context, propertyID int64, in AccountInput) (Account, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return Account{}, err
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.AccountType = strings.ToUpper(strings.TrimSpace(in.AccountType))
	in.NormalSide = strings.ToUpper(strings.TrimSpace(in.NormalSide))
	in.StatementGroup = strings.ToUpper(strings.TrimSpace(in.StatementGroup))
	in.Description = strings.TrimSpace(in.Description)
	postable, active := in.IsPostable == nil || *in.IsPostable, in.IsActive == nil || *in.IsActive
	var fields []apperr.FieldError
	if !codePattern.MatchString(in.Code) {
		fields = append(fields, fieldErr("code", "INVALID_FORMAT", "1-30 characters: A-Z, 0-9, . _ : / -, starting with a letter or digit"))
	}
	if !slices.Contains(accountTypes, in.AccountType) {
		fields = append(fields, fieldErr("account_type", "INVALID_VALUE", strings.Join(accountTypes, ", ")))
	} else {
		fields = append(fields, validGroup(in.AccountType, in.StatementGroup, postable)...)
		if in.NormalSide == "" {
			in.NormalSide = defaultSide(in.AccountType)
		}
	}
	if in.NormalSide != SideDebit && in.NormalSide != SideCredit {
		fields = append(fields, fieldErr("normal_side", "INVALID_VALUE", "DEBIT or CREDIT"))
	}
	fields = append(fields, validateText(in.Name, in.Description)...)
	in.DepartmentRequirement = strings.ToUpper(strings.TrimSpace(in.DepartmentRequirement))
	if in.DepartmentRequirement == "" {
		in.DepartmentRequirement = DeptOptional
	}
	fields = append(fields, validRule(in.DepartmentRequirement, in.DefaultDepartmentID)...)
	if len(fields) > 0 {
		return Account{}, apperr.Invalid("the account is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate); err != nil {
			return err
		}
		bd, err := s.businessDate(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := s.checkParent(ctx, p.TenantID, propertyID, nil, in.AccountType, in.ParentID); err != nil {
			return err
		}
		if err := s.checkDefaultDepartment(ctx, p.TenantID, propertyID, in.DefaultDepartmentID); err != nil {
			return err
		}
		id, err = s.q(ctx).CreateAccount(ctx, accountingdb.CreateAccountParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, AccountType: in.AccountType, NormalSide: in.NormalSide, ParentID: in.ParentID,
			IsPostable: postable, IsActive: active, StatementGroup: nullable(in.StatementGroup), Description: nullable(in.Description), ActorID: p.ActorID(),
			DepartmentRequirement: in.DepartmentRequirement, DefaultDepartmentID: in.DefaultDepartmentID,
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.account_created", "gl_account", id, in.Code, nil, map[string]any{"code": in.Code, "name": in.Name, "type": in.AccountType, "department_requirement": in.DepartmentRequirement}))
	})
	if err != nil {
		return Account{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// UpdateAccount changes an account (accounting.manage). It cannot be deactivated or turned into a header while the
// system posts to it, and cannot take postings while it has children.
func (s *Service) UpdateAccount(ctx context.Context, propertyID, id int64, patch AccountPatch) (Account, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return Account{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate); err != nil {
			return err
		}
		bd, err := s.businessDate(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		cur, err := q.GetAccountRow(ctx, accountingdb.GetAccountRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errAccountNotFound()
		}
		if err != nil {
			return err
		}
		name, description, group := cur.Name, deref(cur.Description), deref(cur.StatementGroup)
		postable, active, parent := cur.IsPostable, cur.IsActive, cur.ParentID
		requirement, defaultDept := cur.DepartmentRequirement, cur.DefaultDepartmentID
		if patch.DepartmentRequirement != nil {
			requirement = strings.ToUpper(strings.TrimSpace(*patch.DepartmentRequirement))
		}
		if patch.DefaultDepartmentID != nil {
			if *patch.DefaultDepartmentID < 1 {
				defaultDept = nil
			} else {
				defaultDept = patch.DefaultDepartmentID
			}
		}
		if requirement == DeptNone && patch.DepartmentRequirement != nil && patch.DefaultDepartmentID == nil {
			defaultDept = nil // an account that takes no department has no default
		}
		if patch.Name != nil {
			name = strings.TrimSpace(*patch.Name)
		}
		if patch.Description != nil {
			description = strings.TrimSpace(*patch.Description)
		}
		if patch.StatementGroup != nil {
			group = strings.ToUpper(strings.TrimSpace(*patch.StatementGroup))
		}
		if patch.IsPostable != nil {
			postable = *patch.IsPostable
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if patch.ParentID != nil {
			if *patch.ParentID < 1 {
				parent = nil
			} else {
				parent = patch.ParentID
			}
		}
		fields := append(validGroup(cur.AccountType, group, postable), validateText(name, description)...)
		fields = append(fields, validRule(requirement, defaultDept)...)
		if len(fields) > 0 {
			return apperr.Invalid("the account is invalid", fields...)
		}
		if err := s.checkParent(ctx, p.TenantID, propertyID, &id, cur.AccountType, parent); err != nil {
			return err
		}
		if postable && !cur.IsPostable {
			if n, err := q.CountChildren(ctx, accountingdb.CountChildrenParams{PropertyID: propertyID, ID: &id}); err != nil {
				return err
			} else if n > 0 {
				return apperr.Conflict("ACCOUNT_HAS_CHILDREN", "an account with accounts under it cannot take postings")
			}
		}
		if (!postable && cur.IsPostable) || (!active && cur.IsActive) {
			if err := s.requireUnmapped(ctx, propertyID, cur); err != nil {
				return err
			}
		}
		if err := s.requireNoLines(ctx, propertyID, cur, !postable && cur.IsPostable); err != nil {
			return err
		}
		if defaultDept != nil && !sameID(defaultDept, cur.DefaultDepartmentID) {
			if err := s.checkDefaultDepartment(ctx, p.TenantID, propertyID, defaultDept); err != nil {
				return err
			}
		}
		if err := q.UpdateAccount(ctx, accountingdb.UpdateAccountParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: name, ParentID: parent, IsPostable: postable, IsActive: active,
			StatementGroup: nullable(group), Description: nullable(description), ActorID: p.ActorID(),
			DepartmentRequirement: requirement, DefaultDepartmentID: defaultDept,
		}); err != nil {
			return err
		}
		if requirement != cur.DepartmentRequirement || !sameOrBothNil(defaultDept, cur.DefaultDepartmentID) {
			if err := s.requireSetup(ctx, p.TenantID, propertyID, &id); err != nil { // a required department must have a source for everything that posts here
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.account_updated", "gl_account", id, auditlabel.GLAccount(ctx, propertyID, id),
			map[string]any{"name": cur.Name, "postable": cur.IsPostable, "active": cur.IsActive, "group": deref(cur.StatementGroup), "parent_id": cur.ParentID, "department_requirement": cur.DepartmentRequirement, "default_department_id": cur.DefaultDepartmentID},
			map[string]any{"name": name, "postable": postable, "active": active, "group": group, "parent_id": parent, "department_requirement": requirement, "default_department_id": defaultDept}))
	})
	if err != nil {
		return Account{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// requireUnmapped refuses to switch off an account the system posts to.
func (s *Service) requireUnmapped(ctx context.Context, propertyID int64, a accountingdb.GlAccount) error {
	mapped, err := s.q(ctx).ListAccountMap(ctx, accountingdb.ListAccountMapParams{TenantID: a.TenantID, PropertyID: propertyID})
	if err != nil {
		return err
	}
	for _, m := range mapped {
		if m.AccountID == a.ID {
			return apperr.Conflict("ACCOUNT_IN_USE", "the system posts to this account: point "+m.MapKey+" at another account first").WithContext("map_key", m.MapKey)
		}
	}
	return nil
}

// DeleteAccount removes an account nobody uses (accounting.manage): no journal line, no child, no system mapping and no
// charge code, tax or service charge that carries its code.
func (s *Service) DeleteAccount(ctx context.Context, propertyID, id int64) error {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate); err != nil {
			return err
		}
		bd, err := s.businessDate(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		cur, err := q.GetAccountRow(ctx, accountingdb.GetAccountRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errAccountNotFound()
		}
		if err != nil {
			return err
		}
		children, err := q.CountChildren(ctx, accountingdb.CountChildrenParams{PropertyID: propertyID, ID: &id})
		if err != nil {
			return err
		}
		refs, err := q.CountAccountReferences(ctx, accountingdb.CountAccountReferencesParams{PropertyID: propertyID, ID: id, Code: &cur.Code})
		if err != nil {
			return err
		}
		if children > 0 || refs > 0 {
			return apperr.Conflict("ACCOUNT_IN_USE", "the account has accounts under it or is used by the system, a charge code, a tax or a service charge").
				WithContext("children", children).WithContext("references", refs)
		}
		if err := s.requireNoLines(ctx, propertyID, cur, true); err != nil {
			return err
		}
		if err := q.DeleteAccount(ctx, accountingdb.DeleteAccountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.account_deleted", "gl_account", id, cur.Code, map[string]any{"code": cur.Code, "name": cur.Name}, nil))
	})
}

// requireNoLines keeps an account that has journal lines from becoming a header (strict): its history would sit on an
// account that takes no postings. Deleting one is stopped by the foreign key of the lines.
func (s *Service) requireNoLines(ctx context.Context, propertyID int64, a accountingdb.GlAccount, strict bool) error {
	if !strict {
		return nil
	}
	n, err := s.q(ctx).CountAccountLines(ctx, accountingdb.CountAccountLinesParams{TenantID: a.TenantID, PropertyID: propertyID, AccountID: a.ID})
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflict("ACCOUNT_HAS_ENTRIES", "the account has journal entries").WithContext("lines", n)
	}
	return nil
}

// settingsErr turns a missing settings row into the error of a property without accounting.
func settingsErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
	}
	return err
}

// ---------------------------------------------------------------------------------------------------------------
// CSV: export and import of the chart

var csvHeader = []string{"code", "name", "type", "parent_code", "postable", "group", "active", "description"}

// ExportCSV is the chart as CSV rows (header first), in the layout ImportCSV reads.
func (s *Service) ExportCSV(ctx context.Context, propertyID int64) ([][]string, error) {
	list, err := s.Accounts(ctx, propertyID, AccountFilter{})
	if err != nil {
		return nil, err
	}
	out := [][]string{slices.Clone(csvHeader)}
	for _, a := range list {
		out = append(out, []string{a.Code, a.Name, a.AccountType, a.ParentCode, yesNo(a.IsPostable), a.StatementGroup, yesNo(a.IsActive), a.Description})
	}
	return out, nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

type importRow struct {
	line                                         int
	code, name, accountType, parent, group, desc string
	postable, active                             bool
}

func parseBool(v string, def bool) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return def, true
	case "yes", "true", "1", "y":
		return true, true
	case "no", "false", "0", "n":
		return false, true
	}
	return false, false
}

// parseCSV reads the rows of an import and returns every problem it finds, row by row.
func parseCSV(text string) ([]importRow, []apperr.FieldError) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	head, err := r.Read()
	if err != nil {
		return nil, []apperr.FieldError{fieldErr("csv", "EMPTY", "a header row and the accounts")}
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"code", "name", "type"} {
		if _, ok := col[need]; !ok {
			return nil, []apperr.FieldError{fieldErr("csv", "MISSING_COLUMN", "the header needs the column "+need)}
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var rows []importRow
	var fields []apperr.FieldError
	seen := map[string]int{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fields = append(fields, fieldErr(fmt.Sprintf("rows[%d]", line), "INVALID_CSV", err.Error()))
			break
		}
		if len(rows) >= maxImportRow {
			fields = append(fields, fieldErr("csv", "TOO_MANY_ROWS", "at most 2000 accounts"))
			break
		}
		row := importRow{line: line, code: strings.ToUpper(get(rec, "code")), name: get(rec, "name"), accountType: strings.ToUpper(get(rec, "type")),
			parent: strings.ToUpper(get(rec, "parent_code")), group: strings.ToUpper(get(rec, "group")), desc: get(rec, "description")}
		at := func(f string) string { return fmt.Sprintf("rows[%d].%s", line, f) }
		var ok bool
		if row.postable, ok = parseBool(get(rec, "postable"), true); !ok {
			fields = append(fields, fieldErr(at("postable"), "INVALID_VALUE", "yes or no"))
		}
		if row.active, ok = parseBool(get(rec, "active"), true); !ok {
			fields = append(fields, fieldErr(at("active"), "INVALID_VALUE", "yes or no"))
		}
		if !codePattern.MatchString(row.code) {
			fields = append(fields, fieldErr(at("code"), "INVALID_FORMAT", "1-30 characters: A-Z, 0-9, . _ : / -"))
		} else if first, dup := seen[row.code]; dup {
			fields = append(fields, fieldErr(at("code"), "DUPLICATE", fmt.Sprintf("already on row %d", first)))
		}
		seen[row.code] = line
		if !slices.Contains(accountTypes, row.accountType) {
			fields = append(fields, fieldErr(at("type"), "INVALID_VALUE", strings.Join(accountTypes, ", ")))
		} else {
			for _, f := range validGroup(row.accountType, row.group, row.postable) {
				fields = append(fields, fieldErr(at("group"), f.Code, f.Message))
			}
		}
		for _, f := range validateText(row.name, row.desc) {
			fields = append(fields, fieldErr(at(f.Field), f.Code, f.Message))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 && len(fields) == 0 {
		fields = append(fields, fieldErr("csv", "EMPTY", "at least one account"))
	}
	return rows, fields
}

// errDryRun ends a dry run's transaction without committing it.
var errDryRun = errors.New("accounting: dry run")

// ImportCSV adds and updates accounts from CSV text (accounting.manage). A row whose code exists updates that account
// (its type cannot change); other rows create one. The parent codes may appear anywhere in the file. Everything is
// checked and applied in one transaction, so a file with a mistake changes nothing; dryRun checks and applies it without
// keeping it.
func (s *Service) ImportCSV(ctx context.Context, propertyID int64, text string, dryRun bool) (ImportResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingManage)
	if err != nil {
		return ImportResult{}, err
	}
	rows, fields := parseCSV(text)
	if len(fields) > 0 {
		return ImportResult{}, apperr.Invalid("the file is invalid", fields...)
	}
	out := ImportResult{DryRun: dryRun}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate); err != nil {
			return err
		}
		bd, err := s.businessDate(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		existing, err := s.list(ctx, p.TenantID, propertyID, AccountFilter{}, nil)
		if err != nil {
			return err
		}
		byCode := map[string]Account{}
		for _, a := range existing {
			byCode[a.Code] = a
		}
		ids := map[string]int64{}
		var problems []apperr.FieldError
		for _, r := range rows {
			at := func(f string) string { return fmt.Sprintf("rows[%d].%s", r.line, f) }
			if cur, ok := byCode[r.code]; ok {
				if cur.AccountType != r.accountType {
					problems = append(problems, fieldErr(at("type"), "TYPE_CHANGE_NOT_ALLOWED", "the account is of type "+cur.AccountType))
					continue
				}
				ids[r.code] = cur.ID
				out.Updated++
				continue
			}
			id, err := q.CreateAccount(ctx, accountingdb.CreateAccountParams{
				TenantID: p.TenantID, PropertyID: propertyID, Code: r.code, Name: r.name, AccountType: r.accountType, NormalSide: defaultSide(r.accountType),
				IsPostable: r.postable, IsActive: r.active, StatementGroup: nullable(r.group), Description: nullable(r.desc), ActorID: p.ActorID(), DepartmentRequirement: DeptOptional,
			})
			if err != nil {
				return err
			}
			ids[r.code] = id
			out.Created++
		}
		if len(problems) > 0 {
			return apperr.Invalid("the file is invalid", problems...)
		}
		// Second pass: the parents (they may be later in the file), then the attributes of the accounts that existed.
		for _, r := range rows {
			at := func(f string) string { return fmt.Sprintf("rows[%d].%s", r.line, f) }
			var parent *int64
			if r.parent != "" {
				id, ok := ids[r.parent]
				if !ok {
					if cur, found := byCode[r.parent]; found {
						id, ok = cur.ID, true
					}
				}
				if !ok {
					problems = append(problems, fieldErr(at("parent_code"), "NOT_FOUND", "no account has the code "+r.parent))
					continue
				}
				parent = &id
			}
			if err := q.UpdateAccount(ctx, accountingdb.UpdateAccountParams{
				TenantID: p.TenantID, PropertyID: propertyID, ID: ids[r.code], Name: r.name, ParentID: parent, IsPostable: r.postable, IsActive: r.active,
				StatementGroup: nullable(r.group), Description: nullable(r.desc), ActorID: p.ActorID(),
				DepartmentRequirement: ruleOrDefault(byCode[r.code].DepartmentRequirement), DefaultDepartmentID: byCode[r.code].DefaultDepartmentID, // the file does not carry the department rule: it stays
			}); err != nil {
				return err
			}
		}
		if len(problems) > 0 {
			return apperr.Invalid("the file is invalid", problems...)
		}
		// The result must be a sound chart: parents are headers of the same type, nobody sits under itself, an account that
		// takes postings has no children, and the system's own accounts are still usable.
		final, err := s.list(ctx, p.TenantID, propertyID, AccountFilter{}, nil)
		if err != nil {
			return err
		}
		if fields := checkChart(final); len(fields) > 0 {
			return apperr.Invalid("the file would break the chart of accounts", fields...)
		}
		if err := s.checkMapUsable(ctx, p.TenantID, propertyID); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.accounts_imported", "property", propertyID, auditlabel.Property(ctx, propertyID), nil, map[string]any{"created": out.Created, "updated": out.Updated, "dry_run": dryRun})); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return out, nil
	}
	return out, err
}

// checkChart finds what is wrong with a whole chart.
func checkChart(list []Account) []apperr.FieldError {
	byID := map[int64]Account{}
	hasChild := map[int64]bool{}
	for _, a := range list {
		byID[a.ID] = a
		if a.ParentID != nil {
			hasChild[*a.ParentID] = true
		}
	}
	var fields []apperr.FieldError
	for _, a := range list {
		if a.IsPostable && hasChild[a.ID] {
			fields = append(fields, fieldErr("account "+a.Code, "HAS_CHILDREN", "an account that takes postings cannot have accounts under it"))
		}
		if a.ParentID == nil {
			continue
		}
		parent, ok := byID[*a.ParentID]
		switch {
		case !ok:
			fields = append(fields, fieldErr("account "+a.Code, "PARENT_NOT_FOUND", "the parent does not exist"))
		case parent.AccountType != a.AccountType:
			fields = append(fields, fieldErr("account "+a.Code, "PARENT_TYPE_MISMATCH", "the parent "+parent.Code+" is of another account type"))
		}
		seen := map[int64]bool{a.ID: true}
		for cur, depth := a.ParentID, 0; cur != nil && depth < 50; depth++ {
			if seen[*cur] {
				fields = append(fields, fieldErr("account "+a.Code, "CIRCULAR", "the accounts sit under each other"))
				break
			}
			seen[*cur] = true
			cur = byID[*cur].ParentID
		}
	}
	return fields
}
