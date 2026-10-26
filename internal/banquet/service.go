package banquet

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service is the application service of the banquet: the masters, the events and the schedule of the meeting rooms. Reading needs one of the banquet permissions; changing the masters needs
// banquet.setup, an event banquet.manage, and confirming or cancelling a definite event banquet.confirm (36 7.1).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	// The money (PR 2): the ledger of the EVENT folio is the one folio service, the charges come from the one charge engine, and the approvals are those of iam.
	folios  *folios.Service
	billing *billingconfig.Service
	iam     *iam.Service
	// The stock (PR 4): nil, or off by the plan, and the banquet deducts nothing.
	stock StockHook
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, f *folios.Service, b *billingconfig.Service, i *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, folios: f, billing: b, iam: i}
}

func (s *Service) q(ctx context.Context) *banquetdb.Queries { return banquetdb.New(s.txm.DB(ctx)) }

// Who may read what (36 7.1): the masters are readable by whoever sets them up, works with events or reads; the events and the calendar by whoever works with them.
var (
	masterReaders = []auth.Permission{auth.PermBanquetSetup, auth.PermBanquetManage, auth.PermBanquetConfirm, auth.PermBanquetRead}
	eventReaders  = []auth.Permission{auth.PermBanquetManage, auth.PermBanquetConfirm, auth.PermBanquetRead}
	allBanquet    = masterReaders
)

func (s *Service) canRead(ctx context.Context, propertyID int64, perms ...auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, perms...)
}

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func invalid(what string, fields ...apperr.FieldError) *apperr.Error {
	return apperr.Invalid("the "+what+" is invalid", fields...)
}

// invalidCode is a refusal of the request with a code of its own (the field says where).
func invalidCode(code, field, msg string) *apperr.Error {
	e := apperr.New(apperr.KindInvalid, code, msg)
	e.Fields = []apperr.FieldError{fieldErr(field, code, msg)}
	return e
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

func active(in *bool) bool { return in == nil || *in }

func (s *Service) entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated}
}

// write runs a change in one transaction: the open business day in share mode first (the date of the audit entry and the gate of the day), then fn.
func (s *Service) write(ctx context.Context, propertyID int64, fn func(ctx context.Context, day civil.Date) error) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		return fn(ctx, day.BusinessDate)
	})
}

// today is the open business date, or the zero date when the property has none (a read then flags nothing).
func (s *Service) today(ctx context.Context, propertyID int64) civil.Date {
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return civil.Date{}
	}
	return day.BusinessDate
}

func checkName(f, name string) []apperr.FieldError {
	if n := len([]rune(strings.TrimSpace(name))); n < 1 || n > maxName {
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "1 to 100 characters")}
	}
	return nil
}

func checkCode(f, code string) []apperr.FieldError {
	if !codePatten.MatchString(code) {
		return []apperr.FieldError{fieldErr(f, "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, . _ -")}
	}
	return nil
}

func checkSort(sort int) []apperr.FieldError {
	if sort < 0 || sort > maxSort {
		return []apperr.FieldError{fieldErr("sort_order", "INVALID_VALUE", "0 to 100000")}
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
