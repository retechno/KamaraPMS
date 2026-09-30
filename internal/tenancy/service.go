package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy/tenancydb"
)

// Service is the tenancy application service.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	store store
}

// NewService wires the tenancy service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, store: store{txm: txm}}
}

// ---------------------------------------------------------------------------
// Tenants (operator actions, used by cmd/pms-admin)

// CreateTenant registers a new tenant.
func (s *Service) CreateTenant(ctx context.Context, code, name, timezone string) (Tenant, error) {
	code = NormalizeCode(code)
	var fields []apperr.FieldError
	fields = append(fields, validateCode("code", code)...)
	if name == "" {
		fields = append(fields, apperr.FieldError{Field: "name", Code: "REQUIRED"})
	}
	if ValidateTimezone(timezone) != nil {
		fields = append(fields, apperr.FieldError{Field: "timezone", Code: "INVALID_TIMEZONE"})
	}
	if len(fields) > 0 {
		return Tenant{}, apperr.Invalid("the tenant is invalid", fields...)
	}

	var t Tenant
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		row, err := s.store.q(ctx).CreateTenant(ctx, tenancydb.CreateTenantParams{Code: code, Name: name, Timezone: timezone})
		if err != nil {
			return err
		}
		t = toTenant(row)
		return s.audit.Write(ctx, audit.Entry{TenantID: t.ID, Action: "tenant.created", EntityType: "tenant", EntityID: t.ID, New: t})
	})
	return t, err
}

// ---------------------------------------------------------------------------
// Properties

// CreatePropertyInput is the request to open a new property.
type CreatePropertyInput struct {
	Code                string
	Settings            PropertySettings
	OpeningBusinessDate civil.Date
}

// PropertyWithDay is a property together with its current business date.
type PropertyWithDay struct {
	Property
	BusinessDate civil.Date `json:"business_date"`
}

// CreateProperty opens a property: the property row, its first OPEN business
// day and its document sequences, atomically.
func (s *Service) CreateProperty(ctx context.Context, in CreatePropertyInput) (PropertyWithDay, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return PropertyWithDay{}, err
	}

	in.Code = NormalizeCode(in.Code)
	in.Settings.Normalize()
	fields := validateCode("code", in.Code)
	fields = append(fields, in.Settings.Validate()...)
	if in.OpeningBusinessDate.IsZero() {
		fields = append(fields, apperr.FieldError{Field: "opening_business_date", Code: "REQUIRED"})
	} else if ValidateTimezone(in.Settings.Timezone) == nil {
		loc := Property{PropertySettings: in.Settings}.Location()
		if fe := ValidateOpeningDate(in.OpeningBusinessDate, s.clock.Now(), loc); fe != nil {
			fields = append(fields, *fe)
		}
	}
	if len(fields) > 0 {
		return PropertyWithDay{}, apperr.Invalid("the property is invalid", fields...)
	}

	var out PropertyWithDay
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.store.q(ctx)
		st := in.Settings
		row, err := q.CreateProperty(ctx, tenancydb.CreatePropertyParams{
			TenantID:                        p.TenantID,
			Code:                            in.Code,
			Name:                            st.Name,
			Address:                         nullable(st.Address),
			City:                            nullable(st.City),
			CountryCode:                     nullable(st.CountryCode),
			Timezone:                        st.Timezone,
			CurrencyCode:                    st.CurrencyCode,
			CurrencyDecimals:                decimals16(st.CurrencyDecimals),
			CheckInTime:                     st.CheckInTime,
			CheckOutTime:                    st.CheckOutTime,
			RequireRoomInspectionForCheckin: st.RequireRoomInspectionForCheckin,
			NightAuditMarksOccupiedDirty:    st.NightAuditMarksOccupiedDirty,
			NightAuditEarliestTime:          st.NightAuditEarliestTime,
			ActorID:                         p.ActorID(),
		})
		if err != nil {
			return err
		}
		prop := toProperty(row)

		day, err := q.InsertBusinessDay(ctx, tenancydb.InsertBusinessDayParams{
			TenantID: p.TenantID, PropertyID: prop.ID, BusinessDate: in.OpeningBusinessDate,
			OpenedAt: s.clock.Now(), OpenedBy: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for _, seq := range defaultSequences {
			if err := q.CreateDocumentSequence(ctx, tenancydb.CreateDocumentSequenceParams{
				TenantID: p.TenantID, PropertyID: prop.ID, SequenceType: string(seq.Type), Prefix: seq.Prefix,
			}); err != nil {
				return err
			}
		}

		out = PropertyWithDay{Property: prop, BusinessDate: day.BusinessDate}
		bd := day.BusinessDate
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &prop.ID, BusinessDate: &bd, UserID: p.ActorID(),
			Action: "property.created", EntityType: "property", EntityID: prop.ID, New: prop}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &prop.ID, BusinessDate: &bd, UserID: p.ActorID(),
			Action: "business_day.opened", EntityType: "business_day", EntityID: day.ID, New: toBusinessDay(day)})
	})
	return out, err
}

// GetProperty returns a property the caller may access (404 otherwise, so
// other tenants' properties are indistinguishable from missing ones).
func (s *Service) GetProperty(ctx context.Context, propertyID int64) (Property, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Property{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Property{}, err
	}
	row, err := s.store.q(ctx).GetProperty(ctx, tenancydb.GetPropertyParams{TenantID: p.TenantID, ID: propertyID})
	if err != nil {
		return Property{}, notFound(err, errPropertyNotFound)
	}
	return toProperty(row), nil
}

// GetPropertyWithDay returns the property and its current business date.
func (s *Service) GetPropertyWithDay(ctx context.Context, propertyID int64) (PropertyWithDay, error) {
	prop, err := s.GetProperty(ctx, propertyID)
	if err != nil {
		return PropertyWithDay{}, err
	}
	day, err := s.CurrentBusinessDay(ctx, prop.ID)
	if err != nil {
		return PropertyWithDay{}, err
	}
	return PropertyWithDay{Property: prop, BusinessDate: day.BusinessDate}, nil
}

// ListProperties returns the properties the caller may access, by id, after afterID.
func (s *Service) ListProperties(ctx context.Context, afterID int64, limit int) ([]Property, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	q := s.store.q(ctx)
	var rows []tenancydb.Property
	if p.IsTenantAdmin {
		rows, err = q.ListProperties(ctx, tenancydb.ListPropertiesParams{TenantID: p.TenantID, AfterID: afterID, RowLimit: rowLimit(limit)})
	} else {
		rows, err = q.ListPropertiesForUser(ctx, tenancydb.ListPropertiesForUserParams{
			TenantID: p.TenantID, UserID: p.UserID, AfterID: afterID, RowLimit: rowLimit(limit)})
	}
	if err != nil {
		return nil, err
	}
	out := make([]Property, len(rows))
	for i, r := range rows {
		out[i] = toProperty(r)
	}
	return out, nil
}

// PropertyPatch changes selected settings; nil fields are left unchanged.
type PropertyPatch struct {
	Name                            *string
	Address                         *string
	City                            *string
	CountryCode                     *string
	Timezone                        *string
	CurrencyCode                    *string
	CurrencyDecimals                *int32
	CheckInTime                     *civil.TimeOfDay
	CheckOutTime                    *civil.TimeOfDay
	RequireRoomInspectionForCheckin *bool
	NightAuditMarksOccupiedDirty    *bool
	NightAuditEarliestTime          *civil.TimeOfDay
	Status                          *string
}

// UpdateProperty applies a patch. The currency is locked once financial
// transactions exist (checked here for a clear error; the database enforces it too).
func (s *Service) UpdateProperty(ctx context.Context, propertyID int64, patch PropertyPatch) (Property, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Property{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPropertyManage); err != nil {
		return Property{}, err
	}

	var out Property
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.store.q(ctx)
		row, err := q.GetPropertyForUpdate(ctx, tenancydb.GetPropertyForUpdateParams{TenantID: p.TenantID, ID: propertyID})
		if err != nil {
			return notFound(err, errPropertyNotFound)
		}
		before := toProperty(row)
		st := before.PropertySettings
		status := before.Status
		apply(&st.Name, patch.Name)
		apply(&st.Address, patch.Address)
		apply(&st.City, patch.City)
		apply(&st.CountryCode, patch.CountryCode)
		apply(&st.Timezone, patch.Timezone)
		apply(&st.CurrencyCode, patch.CurrencyCode)
		apply(&st.CurrencyDecimals, patch.CurrencyDecimals)
		apply(&st.CheckInTime, patch.CheckInTime)
		apply(&st.CheckOutTime, patch.CheckOutTime)
		apply(&st.RequireRoomInspectionForCheckin, patch.RequireRoomInspectionForCheckin)
		apply(&st.NightAuditMarksOccupiedDirty, patch.NightAuditMarksOccupiedDirty)
		apply(&st.NightAuditEarliestTime, patch.NightAuditEarliestTime)
		apply(&status, patch.Status)
		st.Normalize()

		fields := st.Validate()
		if status != PropertyActive && status != PropertyInactive {
			fields = append(fields, apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "ACTIVE or INACTIVE"})
		}
		if len(fields) > 0 {
			return apperr.Invalid("the property settings are invalid", fields...)
		}

		if st.CurrencyCode != before.CurrencyCode || st.CurrencyDecimals != before.CurrencyDecimals {
			locked, err := q.PropertyHasFinancialData(ctx, propertyID)
			if err != nil {
				return err
			}
			if locked {
				return apperr.Conflict("CURRENCY_LOCKED", "the property currency cannot change once financial transactions exist")
			}
		}

		updated, err := q.UpdateProperty(ctx, tenancydb.UpdatePropertyParams{
			TenantID:                        p.TenantID,
			ID:                              propertyID,
			Name:                            st.Name,
			Address:                         nullable(st.Address),
			City:                            nullable(st.City),
			CountryCode:                     nullable(st.CountryCode),
			Timezone:                        st.Timezone,
			CurrencyCode:                    st.CurrencyCode,
			CurrencyDecimals:                decimals16(st.CurrencyDecimals),
			CheckInTime:                     st.CheckInTime,
			CheckOutTime:                    st.CheckOutTime,
			RequireRoomInspectionForCheckin: st.RequireRoomInspectionForCheckin,
			NightAuditMarksOccupiedDirty:    st.NightAuditMarksOccupiedDirty,
			NightAuditEarliestTime:          st.NightAuditEarliestTime,
			Status:                          status,
			ActorID:                         p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toProperty(updated)

		day, err := q.GetOpenBusinessDay(ctx, propertyID)
		if err != nil {
			return notFound(err, errBusinessDayNotFound)
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &out.ID, BusinessDate: &day.BusinessDate,
			UserID: p.ActorID(), Action: "property.updated", EntityType: "property", EntityID: out.ID, Old: before, New: out})
	})
	return out, err
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// ---------------------------------------------------------------------------
// Business day

// CurrentBusinessDay returns the OPEN business day (no lock). Use it for
// display and reads; writers use RequireOpenBusinessDay inside their transaction.
func (s *Service) CurrentBusinessDay(ctx context.Context, propertyID int64) (BusinessDay, error) {
	row, err := s.store.q(ctx).GetOpenBusinessDay(ctx, propertyID)
	if err != nil {
		return BusinessDay{}, notFound(err, errBusinessDayNotFound)
	}
	return toBusinessDay(row), nil
}

// RequireOpenBusinessDay locks the OPEN business day (lock level L1) and
// returns it. Every business-dated write calls this first with db.ForShare, so
// it cannot interleave with night audit, which uses db.ForUpdate. If expected
// is set and differs, the caller is acting on a stale screen.
func (s *Service) RequireOpenBusinessDay(ctx context.Context, propertyID int64, mode db.LockMode, expected *civil.Date) (BusinessDay, error) {
	if err := db.EnterLockLevel(ctx, db.LevelBusinessDay); err != nil {
		return BusinessDay{}, err
	}
	q := s.store.q(ctx)
	lock := func() (tenancydb.BusinessDay, error) {
		switch mode {
		case db.ForShare:
			return q.LockOpenBusinessDayForShare(ctx, propertyID)
		case db.ForUpdate:
			return q.LockOpenBusinessDayForUpdate(ctx, propertyID)
		}
		return tenancydb.BusinessDay{}, fmt.Errorf("tenancy: invalid lock mode %q", mode)
	}
	row, err := lock()
	if errors.Is(err, pgx.ErrNoRows) {
		// We waited for night audit, which closed the day we were queued on. Under
		// READ COMMITTED the re-checked row no longer matches status = 'OPEN', and
		// the newly opened day is invisible to this statement's snapshot. A new
		// statement takes a new snapshot and finds the new OPEN day.
		row, err = lock()
	}
	if err != nil {
		return BusinessDay{}, notFound(err, errBusinessDayNotFound)
	}
	day := toBusinessDay(row)
	if expected != nil && !expected.Equal(day.BusinessDate) {
		return BusinessDay{}, apperr.Conflict("BUSINESS_DATE_MISMATCH", "the business date has changed; refresh and try again").
			WithContext("business_date", day.BusinessDate.String()).
			WithContext("requested_business_date", expected.String())
	}
	return day, nil
}

// DayClock relates the property's business date to server and local time.
func (s *Service) DayClock(ctx context.Context, prop Property) (DayClock, error) {
	day, err := s.CurrentBusinessDay(ctx, prop.ID)
	if err != nil {
		return DayClock{}, err
	}
	return EvaluateDay(day.BusinessDate, s.clock.Now(), prop.Location(), prop.NightAuditEarliestTime), nil
}

// BusinessDayHistory lists business days newest first, before the given date.
func (s *Service) BusinessDayHistory(ctx context.Context, propertyID int64, before *civil.Date, limit int) ([]BusinessDay, error) {
	cursor := civil.NewDate(9999, 12, 31)
	if before != nil {
		cursor = *before
	}
	rows, err := s.store.q(ctx).ListBusinessDays(ctx, tenancydb.ListBusinessDaysParams{
		PropertyID: propertyID, BeforeDate: cursor, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]BusinessDay, len(rows))
	for i, r := range rows {
		out[i] = toBusinessDay(r)
	}
	return out, nil
}

// CloseAndOpenNext closes the OPEN business day and opens the next one. It is
// the only way the business date moves, and is called by night audit (M13)
// inside its transaction after all checks passed. It enforces the time guard.
func (s *Service) CloseAndOpenNext(ctx context.Context, prop Property, expected civil.Date, closedBy *int64, summary json.RawMessage) (closed, opened BusinessDay, err error) {
	day, err := s.RequireOpenBusinessDay(ctx, prop.ID, db.ForUpdate, &expected)
	if err != nil {
		return BusinessDay{}, BusinessDay{}, err
	}
	now := s.clock.Now()
	if c := EvaluateDay(day.BusinessDate, now, prop.Location(), prop.NightAuditEarliestTime); !c.NightAuditAllowed {
		return BusinessDay{}, BusinessDay{}, apperr.Conflict("NIGHT_AUDIT_TOO_EARLY",
			"the business date cannot be closed before its end of day").
			WithContext("business_date", day.BusinessDate.String()).
			WithContext("allowed_from", c.NightAuditAllowedFrom)
	}
	if summary == nil {
		summary = json.RawMessage(`{}`)
	}

	q := s.store.q(ctx)
	closedRow, err := q.CloseBusinessDay(ctx, tenancydb.CloseBusinessDayParams{ID: day.ID, ClosedAt: &now, ClosedBy: closedBy, Summary: summary})
	if err != nil {
		return BusinessDay{}, BusinessDay{}, err
	}
	openedRow, err := q.InsertBusinessDay(ctx, tenancydb.InsertBusinessDayParams{
		TenantID: prop.TenantID, PropertyID: prop.ID, BusinessDate: day.BusinessDate.AddDays(1), OpenedAt: now, OpenedBy: closedBy,
	})
	if err != nil {
		return BusinessDay{}, BusinessDay{}, err
	}
	closed, opened = toBusinessDay(closedRow), toBusinessDay(openedRow)

	for _, e := range []audit.Entry{
		{Action: "business_day.closed", EntityID: closed.ID, BusinessDate: &closed.BusinessDate, Old: day, New: closed},
		{Action: "business_day.opened", EntityID: opened.ID, BusinessDate: &opened.BusinessDate, New: opened},
	} {
		e.TenantID, e.PropertyID, e.UserID, e.EntityType = prop.TenantID, &prop.ID, closedBy, "business_day"
		if err := s.audit.Write(ctx, e); err != nil {
			return BusinessDay{}, BusinessDay{}, err
		}
	}
	return closed, opened, nil
}

// ---------------------------------------------------------------------------
// Document numbers

// NextDocumentNumber allocates the next gapless number of a series, e.g.
// "RES000042". It must run inside the business transaction (a rollback returns
// the number) and is lock level L5: call it after all other locks.
func (s *Service) NextDocumentNumber(ctx context.Context, propertyID int64, t SequenceType) (string, error) {
	if err := db.EnterLockLevel(ctx, db.LevelSequences); err != nil {
		return "", err
	}
	row, err := s.store.q(ctx).NextDocumentNumber(ctx, tenancydb.NextDocumentNumberParams{PropertyID: propertyID, SequenceType: string(t)})
	if err != nil {
		return "", notFound(err, apperr.NotFound("SEQUENCE_NOT_FOUND", "the document sequence does not exist"))
	}
	return fmt.Sprintf("%s%06d", row.Prefix, row.Number), nil
}

// TenantByCode looks up a tenant (operator tooling).
func (s *Service) TenantByCode(ctx context.Context, code string) (Tenant, error) {
	row, err := s.store.q(ctx).GetTenantByCode(ctx, NormalizeCode(code))
	if err != nil {
		return Tenant{}, notFound(err, apperr.NotFound("TENANT_NOT_FOUND", "no tenant with this code"))
	}
	return toTenant(row), nil
}

// rowLimit bounds a page size for SQL LIMIT (the HTTP layer caps it at 201).
func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}

// decimals16 converts currency decimals, which Validate bounds to 0..3.
func decimals16(d int32) int16 {
	if d < 0 || d > 3 {
		return -1 // invalid: rejected by the database CHECK, never silently wrapped
	}
	return int16(d) //nolint:gosec // G115: bounded to 0..3 above
}
