package tenancy

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy/tenancydb"
)

// store adapts the sqlc queries to domain types. Every call runs on the ambient
// transaction when there is one, otherwise on the pool.
type store struct {
	txm *db.TxManager
}

func (s store) q(ctx context.Context) *tenancydb.Queries { return tenancydb.New(s.txm.DB(ctx)) }

var (
	errPropertyNotFound    = apperr.NotFound("PROPERTY_NOT_FOUND", "the property does not exist or is not accessible")
	errBusinessDayNotFound = apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no open business day")
)

// notFound converts pgx.ErrNoRows into the given domain error.
func notFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return clone(nf)
	}
	return err
}

// clone copies a sentinel so callers can never mutate the shared value.
func clone(e *apperr.Error) *apperr.Error {
	c := *e
	return &c
}

func toTenant(t tenancydb.Tenant) Tenant {
	return Tenant{ID: t.ID, Code: t.Code, Name: t.Name, Status: t.Status, Timezone: t.Timezone, CreatedAt: t.CreatedAt}
}

func toProperty(p tenancydb.Property) Property {
	return Property{
		ID:       p.ID,
		TenantID: p.TenantID,
		Code:     p.Code,
		PropertySettings: PropertySettings{
			Name:                            p.Name,
			Address:                         deref(p.Address),
			City:                            deref(p.City),
			CountryCode:                     deref(p.CountryCode),
			Timezone:                        p.Timezone,
			CurrencyCode:                    p.CurrencyCode,
			CurrencyDecimals:                int32(p.CurrencyDecimals),
			CheckInTime:                     p.CheckInTime,
			CheckOutTime:                    p.CheckOutTime,
			RequireRoomInspectionForCheckin: p.RequireRoomInspectionForCheckin,
			NightAuditMarksOccupiedDirty:    p.NightAuditMarksOccupiedDirty,
			NightAuditEarliestTime:          p.NightAuditEarliestTime,
		},
		Status:    p.Status,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func toBusinessDay(d tenancydb.BusinessDay) BusinessDay {
	return BusinessDay{
		ID:           d.ID,
		PropertyID:   d.PropertyID,
		BusinessDate: d.BusinessDate,
		Status:       d.Status,
		OpenedAt:     d.OpenedAt,
		OpenedBy:     d.OpenedBy,
		ClosedAt:     d.ClosedAt,
		ClosedBy:     d.ClosedBy,
		Summary:      d.Summary,
	}
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
