package assets

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// DepartmentChecker says whether a department belongs to the property and is usable.
type DepartmentChecker interface {
	Check(ctx context.Context, tenantID, propertyID, id int64, field string) error
}

// Service is the application service of the fixed asset register (docs/architecture/37-fixed-assets.md, PR 1). Reading needs one of the asset permissions; the setup (categories, locations,
// settings, fiscal rates, opening balances) needs asset.setup; changing or cancelling an asset needs asset.manage. The ledger and the state of an asset are written by ledger.go only.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	acct  *accounting.Service
	depts DepartmentChecker
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, acct *accounting.Service, depts DepartmentChecker) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, acct: acct, depts: depts}
}

func (s *Service) q(ctx context.Context) *assetsdb.Queries { return assetsdb.New(s.txm.DB(ctx)) }

// Who may read the register, the settings and the setup lists: whoever holds an asset permission (the menu appears with any of them).
var readers = []auth.Permission{auth.PermAssetRead, auth.PermAssetManage, auth.PermAssetDepreciate, auth.PermAssetDispose, auth.PermAssetSetup}

// categoryReaders may read the categories and the locations: the readers, and the people who enter bills (the bill form marks the lines that will wait in the register, document 33).
var categoryReaders = append(append([]auth.Permission{}, readers...), auth.PermPayablesPost, auth.PermPayablesView)

func (s *Service) require(ctx context.Context, propertyID int64, perms ...auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, perms...)
}

// ControlAccountIDs are the accumulated depreciation accounts of the categories: the accounts that only the register posts to (accounting.AssetSource, 37-fixed-assets.md F5).
func (s *Service) ControlAccountIDs(ctx context.Context, tenantID, propertyID int64) ([]int64, error) {
	return s.q(ctx).ControlAccountIDs(ctx, assetsdb.ControlAccountIDsParams{TenantID: tenantID, PropertyID: propertyID})
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

// lockSettings takes the settings row of the register, the lock target of the module (level 47, after the accounting settings): FOR SHARE for a change of one asset or of the masters,
// FOR UPDATE for an import. The row is made on first use.
func (s *Service) lockSettings(ctx context.Context, p auth.Principal, propertyID int64, mode db.LockMode) (assetsdb.FaSetting, error) {
	row, err := s.q(ctx).EnsureSettings(ctx, assetsdb.EnsureSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()})
	if err != nil {
		return row, err
	}
	if err := db.LockRows(ctx, db.FASettings, mode, propertyID, []int64{row.ID}); err != nil {
		return row, err
	}
	return s.q(ctx).GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
}

const labelAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0, O, 1, I: a label is read by eye and by camera

// newLabelCode makes the random 12-character code of a label (a label never exposes the id or the order of the numbers).
func newLabelCode() (string, error) {
	out := make([]byte, labelCodeLength)
	max := big.NewInt(int64(len(labelAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = labelAlphabet[n.Int64()]
	}
	return string(out), nil
}

func moneyOK(d decimal.Decimal, decimals int32) bool {
	return !d.IsNegative() && d.LessThan(decimal.NewFromInt(moneyLimitExclusive)) && d.Equal(d.Round(decimals))
}

func ptr[T any](v T) *T { return &v }
