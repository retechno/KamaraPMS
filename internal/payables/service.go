package payables

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/payables/payablesdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/tenancy"
)

const (
	maxPage        = 200
	maxBillLines   = 100
	maxAllocations = 100
)

// Service is the payables application service. Reading needs payables.view, suppliers payables.manage, and entering
// bills and payments payables.post (voiding one also needs an approval).
type Service struct {
	txm    *db.TxManager
	clock  clock.Clock
	audit  *audit.Writer
	authz  auth.Authorizer
	days   *tenancy.Service
	acct   *accounting.Service
	iam    *iam.Service
	tax    *taxfiling.Service // the PKP status on the date of a bill (how its input VAT is treated)
	guard  VoidGuard
	assets AssetGuard
}

// VoidGuard is asked before a bill is voided, inside the transaction: the inventory answers 409 BILL_HAS_STOCK_MATCH for a bill that is matched to goods received (35-inventory.md F13), because the way to
// void it is to unmatch it, which reverses the clearing of its variance first.
type VoidGuard interface {
	CheckVoid(ctx context.Context, tenantID, propertyID, billID int64) error
}

// AssetGuard is asked, inside the transaction and with the supplier of the bill locked, before a bill is voided (lineNo 0) or a credit note is posted on one of its lines (lineNo of the bill line):
// the fixed assets answer 409 BILL_LINE_IS_ASSET for a line that has been made an asset, because the register mirrors that line (37-fixed-assets.md F8). The way out is to cancel the asset.
type AssetGuard interface {
	CheckBillLine(ctx context.Context, tenantID, propertyID, billID int64, lineNo int32) error
}

// SetAssetGuard wires the guard of the fixed assets; without one every bill can be voided as before.
func (s *Service) SetAssetGuard(g AssetGuard) { s.assets = g }

// SetVoidGuard wires the guard; without one every bill can be voided as before.
func (s *Service) SetVoidGuard(g VoidGuard) { s.guard = g }

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, acct *accounting.Service, iamSvc *iam.Service, tax *taxfiling.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, acct: acct, iam: iamSvc, tax: tax}
}

func (s *Service) q(ctx context.Context) *payablesdb.Queries { return payablesdb.New(s.txm.DB(ctx)) }

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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

func entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

func errSupplierNotFound() *apperr.Error {
	return apperr.NotFound("SUPPLIER_NOT_FOUND", "the supplier does not exist in this property")
}
func errBillNotFound() *apperr.Error {
	return apperr.NotFound("BILL_NOT_FOUND", "the bill does not exist in this property")
}
func errPaymentNotFound() *apperr.Error {
	return apperr.NotFound("SUPPLIER_PAYMENT_NOT_FOUND", "the payment does not exist in this property")
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func limitOf(n int) int32 {
	if n < 1 || n > maxPage {
		return maxPage
	}
	return int32(n)
}

// retryOnDuplicate runs a use case again once when a concurrent request with the same Idempotency-Key won the insert:
// the second run finds the key and replays the first result.
func retryOnDuplicate(key string, run func() error) error {
	err := run()
	var ae *apperr.Error
	if key != "" && errors.As(err, &ae) && ae.Code == "DUPLICATE_REQUEST" {
		return run()
	}
	return err
}

func asApp(err error, target **apperr.Error) bool { return errors.As(err, target) }
