package inventory

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// DepartmentChecker says whether a department of the property takes a new use (internal/departments).
type DepartmentChecker interface {
	Check(ctx context.Context, tenantID, propertyID, id int64, field string) error
}

// Service is the application service of the inventory (docs/architecture/35-inventory.md, PR 1): the settings, the masters, the opening stock, the stock and its card. Reading needs
// inventory.view or inventory.manage; changing the masters and the settings needs inventory.manage; the opening stock needs inventory.opening.
type Service struct {
	txm    *db.TxManager
	clock  clock.Clock
	audit  *audit.Writer
	authz  auth.Authorizer
	days   *tenancy.Service
	depts  DepartmentChecker
	ledger *Ledger
	// pay and approver are wired by internal/app (SetPayables, SetApprover): the bills of the purchasing are posted by the payables, and a price difference above the tolerance is approved with credentials.
	pay      Payables
	approver Approver
	// occupancy is the front desk's answer to which rooms were slept in (SetOccupancy): the amenities of the close need it.
	occupancy Occupancy
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, depts DepartmentChecker) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, depts: depts, ledger: &Ledger{}}
}

func (s *Service) q(ctx context.Context) *inventorydb.Queries { return inventorydb.New(s.txm.DB(ctx)) }

// readers may read the masters and the stock: who manages the module and who works with it.
// The permissions of a screen that uses the masters read them too (35-inventory.md 9.1, document 33): a request is written by who holds inventory.request, an order by inventory.purchase, a receipt by
// inventory.receive, a bill by inventory.bill.
var readers = []auth.Permission{auth.PermInventoryView, auth.PermInventoryManage, auth.PermInventoryOpening, auth.PermInventoryRequest, auth.PermInventoryPurchase, auth.PermInventoryReceive, auth.PermInventoryBill}

func (s *Service) canRead(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, readers...)
}

func (s *Service) manage(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryManage)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func invalid(what string, fields ...apperr.FieldError) *apperr.Error {
	return apperr.Invalid("the "+what+" is invalid", fields...)
}

func notFound(code, what string) *apperr.Error {
	return apperr.NotFound(code, "the "+what+" does not exist in this property")
}

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func versionConflict(what string, current int32) *apperr.Error {
	return apperr.Conflict("VERSION_CONFLICT", "the "+what+" was changed by someone else").WithContext("current_version", current)
}

func (s *Service) entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

// write runs a change in one transaction: the open business day in share mode first (the date of the audit entry), then fn.
func (s *Service) write(ctx context.Context, propertyID int64, fn func(ctx context.Context, day civil.Date) error) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		return fn(ctx, day.BusinessDate)
	})
}

func checkName(f, name string, max int) []apperr.FieldError {
	if n := len([]rune(trimmed(name))); n < 1 || n > max {
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "1 to "+itoa(max)+" characters")}
	}
	return nil
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

func normalCode(code string) string { return strings.ToUpper(trimmed(code)) }

func optional(s *string) *string {
	if s == nil {
		return nil
	}
	t := trimmed(*s)
	if t == "" {
		return nil
	}
	return &t
}

func active(v *bool) bool { return v == nil || *v }

// book reads what the ledger writer needs about the property: the settings (the costing method), the decimals of the currency and the open day, and makes the settings row if the
// property has none yet (the first act of the module).
func (s *Service) book(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date) (Book, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Book{}, err
	}
	cfg, err := s.q(ctx).EnsureSettings(ctx, inventorydb.EnsureSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()})
	if err != nil {
		return Book{}, err
	}
	return Book{TenantID: p.TenantID, PropertyID: propertyID, Date: day, Decimals: prop.CurrencyDecimals, Method: cfg.CostingMethod, ActorID: p.ActorID()}, nil
}
