package companies

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/companies/companiesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service manages companies (company.manage to write; reservation.read or cityledger.read to read).
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

func (s *Service) q(ctx context.Context) *companiesdb.Queries { return companiesdb.New(s.txm.DB(ctx)) }

func errNotFound() *apperr.Error {
	return apperr.NotFound("COMPANY_NOT_FOUND", "the company does not exist in this property")
}

func toCompany(c companiesdb.Company, decimals int32) Company {
	out := Company{
		ID: c.ID, Code: c.Code, Name: c.Name, ContactName: deref(c.ContactName), Email: deref(c.Email), Phone: deref(c.Phone), Address: deref(c.Address),
		City: deref(c.City), TaxID: deref(c.TaxID), PaymentTermsDays: int(c.PaymentTermsDays), Notes: deref(c.Notes), IsActive: c.IsActive,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.CreditLimit != nil {
		v := c.CreditLimit.StringFixed(decimals)
		out.CreditLimit = &v
	}
	return out
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

func (s *Service) writer(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermCompanyManage)
}

// reader lets in whoever may read reservations or the city ledger (a booking picks a company; the ledger lists them).
func (s *Service) reader(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	err = s.authz.Require(ctx, propertyID, auth.PermReservationRead)
	if apperr.IsCode(err, "PERMISSION_DENIED") {
		if s.authz.Require(ctx, propertyID, auth.PermCityLedgerRead) == nil {
			return p, nil
		}
	}
	return p, err
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "company", EntityID: id, Old: old, New: updated}
}

// List lists companies by id after afterID.
func (s *Service) List(ctx context.Context, propertyID, afterID int64, active *bool, q string, limit int) ([]Company, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var qq *string
	if q != "" {
		qq = &q
	}
	rows, err := s.q(ctx).ListCompanies(ctx, companiesdb.ListCompaniesParams{TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: active, Q: qq, RowLimit: int32(max(1, min(limit, 1000)))}) //nolint:gosec // G115: bounded
	if err != nil {
		return nil, err
	}
	out := make([]Company, len(rows))
	for i, r := range rows {
		out[i] = toCompany(r, decimals)
	}
	return out, nil
}

// Get returns one company.
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Company, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	row, err := s.q(ctx).GetCompany(ctx, companiesdb.GetCompanyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Company{}, errNotFound()
	}
	if err != nil {
		return Company{}, err
	}
	return toCompany(row, decimals), nil
}

// Create adds a company (company.manage). A duplicate code is 409 CODE_TAKEN.
func (s *Service) Create(ctx context.Context, propertyID int64, in Input) (Company, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	in.Normalize()
	if fields := in.Validate(true, decimals); len(fields) > 0 {
		return Company{}, apperr.Invalid("the company is invalid", fields...)
	}
	var out Company
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateCompany(ctx, companiesdb.CreateCompanyParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, ContactName: nullable(in.ContactName), Email: nullable(in.Email),
			Phone: nullable(in.Phone), Address: nullable(in.Address), City: nullable(in.City), TaxID: nullable(in.TaxID), CreditLimit: limitOrNil(in.CreditLimit),
			PaymentTermsDays: int16(in.PaymentTermsDays), Notes: nullable(in.Notes), IsActive: in.IsActive, ActorID: p.ActorID(), //nolint:gosec // G115: validated 0..365
		})
		if err != nil {
			return err
		}
		out = toCompany(row, decimals)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "company.created", out.ID, nil, out))
	})
	return out, err
}

// Patch changes selected attributes; nil fields stay unchanged. CreditLimit "" removes the limit.
type Patch struct {
	Name             *string
	ContactName      *string
	Email            *string
	Phone            *string
	Address          *string
	City             *string
	TaxID            *string
	CreditLimit      *string
	PaymentTermsDays *int
	Notes            *string
	IsActive         *bool
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// Update edits a company (company.manage). A company that still owes money cannot be deactivated (409
// COMPANY_HAS_BALANCE). Lowering the limit below what is owed is allowed: it only stops new transfers.
func (s *Service) Update(ctx context.Context, propertyID, id int64, patch Patch) (Company, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Company{}, err
	}
	var out Company
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Companies, db.ForUpdate, propertyID, []int64{id}); err != nil {
			if apperr.IsCode(err, "NOT_FOUND") {
				return errNotFound()
			}
			return err
		}
		q := s.q(ctx)
		row, err := q.GetCompany(ctx, companiesdb.GetCompanyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound()
		}
		if err != nil {
			return err
		}
		before := toCompany(row, decimals)
		in := Input{Code: before.Code, Name: before.Name, ContactName: before.ContactName, Email: before.Email, Phone: before.Phone, Address: before.Address,
			City: before.City, TaxID: before.TaxID, PaymentTermsDays: before.PaymentTermsDays, Notes: before.Notes, IsActive: before.IsActive}
		if before.CreditLimit != nil {
			in.CreditLimit = *before.CreditLimit
		}
		apply(&in.Name, patch.Name)
		apply(&in.ContactName, patch.ContactName)
		apply(&in.Email, patch.Email)
		apply(&in.Phone, patch.Phone)
		apply(&in.Address, patch.Address)
		apply(&in.City, patch.City)
		apply(&in.TaxID, patch.TaxID)
		apply(&in.CreditLimit, patch.CreditLimit)
		apply(&in.PaymentTermsDays, patch.PaymentTermsDays)
		apply(&in.Notes, patch.Notes)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false, decimals); len(fields) > 0 {
			return apperr.Invalid("the company is invalid", fields...)
		}
		if before.IsActive && !in.IsActive {
			owes, err := q.CompanyOwes(ctx, companiesdb.CompanyOwesParams{PropertyID: propertyID, ID: &id})
			if err != nil {
				return err
			}
			if owes.IsPositive() {
				return apperr.Conflict("COMPANY_HAS_BALANCE", "the company still owes money: settle the account before deactivating it").WithContext("balance", owes.StringFixed(decimals))
			}
		}
		updated, err := q.UpdateCompany(ctx, companiesdb.UpdateCompanyParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, ContactName: nullable(in.ContactName), Email: nullable(in.Email), Phone: nullable(in.Phone),
			Address: nullable(in.Address), City: nullable(in.City), TaxID: nullable(in.TaxID), CreditLimit: limitOrNil(in.CreditLimit),
			PaymentTermsDays: int16(in.PaymentTermsDays), Notes: nullable(in.Notes), IsActive: in.IsActive, ActorID: p.ActorID(), //nolint:gosec // G115: validated 0..365
		})
		if err != nil {
			return err
		}
		out = toCompany(updated, decimals)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "company.updated", id, before, out))
	})
	return out, err
}

// LockForTransfer implements folios.CompanyGate: the company must exist, be active, and stay within its credit
// limit after a transfer of amount. The row lock (level 44) serialises transfers and receipts of one company.
func (s *Service) LockForTransfer(ctx context.Context, propertyID, companyID int64, amount decimal.Decimal) error {
	row, owes, err := s.lockWithBalance(ctx, propertyID, companyID)
	if err != nil {
		return err
	}
	if !row.IsActive {
		return apperr.Conflict("COMPANY_INACTIVE", "the company is inactive")
	}
	if row.CreditLimit != nil {
		after := owes.Add(amount)
		if after.GreaterThan(*row.CreditLimit) {
			return apperr.Conflict("CREDIT_LIMIT_EXCEEDED", "the transfer would take the company above its credit limit").
				WithContext("credit_limit", row.CreditLimit.String()).WithContext("balance", owes.String())
		}
	}
	return nil
}

// LockForVoid implements folios.CompanyGate.
func (s *Service) LockForVoid(ctx context.Context, propertyID, companyID int64, amount decimal.Decimal) error {
	_, owes, err := s.lockWithBalance(ctx, propertyID, companyID)
	if err != nil {
		return err
	}
	if owes.Sub(amount).IsNegative() {
		return apperr.Conflict("COMPANY_BALANCE_SETTLED", "receipts already settled this transfer: void the receipt first").WithContext("balance", owes.String())
	}
	return nil
}

// LockForReceipt locks a company for a receipt and returns the balance it owes.
func (s *Service) LockForReceipt(ctx context.Context, propertyID, companyID int64) (decimal.Decimal, error) {
	_, owes, err := s.lockWithBalance(ctx, propertyID, companyID)
	return owes, err
}

func (s *Service) lockWithBalance(ctx context.Context, propertyID, companyID int64) (companiesdb.Company, decimal.Decimal, error) {
	if err := db.LockRows(ctx, db.Companies, db.ForUpdate, propertyID, []int64{companyID}); err != nil {
		if apperr.IsCode(err, "NOT_FOUND") {
			return companiesdb.Company{}, decimal.Zero, errNotFound()
		}
		return companiesdb.Company{}, decimal.Zero, err
	}
	p, err := auth.Require(ctx)
	if err != nil {
		return companiesdb.Company{}, decimal.Zero, err
	}
	q := s.q(ctx)
	row, err := q.GetCompany(ctx, companiesdb.GetCompanyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: companyID})
	if err != nil {
		return row, decimal.Zero, orNF(err)
	}
	owes, err := q.CompanyOwes(ctx, companiesdb.CompanyOwesParams{PropertyID: propertyID, ID: &companyID})
	return row, owes, err
}

func orNF(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound()
	}
	return err
}
