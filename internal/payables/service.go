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
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	acct  *accounting.Service
	iam   *iam.Service
	tax   *taxfiling.Service // the PKP status on the date of a bill (how its input VAT is treated)
}

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
