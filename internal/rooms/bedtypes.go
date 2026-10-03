package rooms

import (
	"context"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomsdb"
	"kamarapms/internal/tenancy"
)

// ListBedTypes lists the bed types of a property in their sort order, optionally only active or inactive ones.
func (s *Service) ListBedTypes(ctx context.Context, propertyID int64, active *bool) ([]BedType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListBedTypes(ctx, roomsdb.ListBedTypesParams{TenantID: p.TenantID, PropertyID: propertyID, Active: active})
	if err != nil {
		return nil, err
	}
	out := make([]BedType, len(rows))
	for i, r := range rows {
		out[i] = toBedType(r)
	}
	return out, nil
}

// CreateBedType adds a bed type to the catalogue (room.manage).
func (s *Service) CreateBedType(ctx context.Context, propertyID int64, in BedTypeInput) (BedType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return BedType{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return BedType{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return BedType{}, apperr.Invalid("the bed type is invalid", fields...)
	}
	var out BedType
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateBedType(ctx, roomsdb.CreateBedTypeParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, SortOrder: in.SortOrder, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toBedType(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "bed_type.created", "bed_type", out.ID, nil, out))
	})
	return out, err
}

// BedTypePatch changes selected attributes; nil fields stay unchanged.
type BedTypePatch struct {
	Name      *string
	SortOrder *int32
	IsActive  *bool
}

// UpdateBedType edits a bed type (room.manage). A bed type that rooms use can be switched off: the rooms keep it, it is
// only no longer offered for new choices.
func (s *Service) UpdateBedType(ctx context.Context, propertyID, id int64, patch BedTypePatch) (BedType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return BedType{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return BedType{}, err
	}
	var out BedType
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetBedType(ctx, roomsdb.GetBedTypeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errBedTypeNotFound())
		}
		before := toBedType(row)
		in := BedTypeInput{Code: before.Code, Name: before.Name, SortOrder: before.SortOrder, IsActive: before.IsActive}
		apply(&in.Name, patch.Name)
		apply(&in.SortOrder, patch.SortOrder)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the bed type is invalid", fields...)
		}
		updated, err := q.UpdateBedType(ctx, roomsdb.UpdateBedTypeParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, SortOrder: in.SortOrder, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toBedType(updated)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "bed_type.updated", "bed_type", id, before, out))
	})
	return out, err
}

// requireActiveBedType checks that a bed type that is being newly chosen exists in the property and is active.
func (s *Service) requireActiveBedType(ctx context.Context, tenantID, propertyID, bedTypeID int64) error {
	b, err := s.q(ctx).GetBedType(ctx, roomsdb.GetBedTypeParams{TenantID: tenantID, PropertyID: propertyID, ID: bedTypeID})
	if err != nil {
		return orNotFound(err, errBedTypeNotFound())
	}
	if !b.IsActive {
		return apperr.Invalid("the bed type is inactive",
			apperr.FieldError{Field: "bed_type_id", Code: "BED_TYPE_INACTIVE", Message: "choose an active bed type"})
	}
	return nil
}

// SeedProperty gives a new property the standard catalogue of bed types. It is registered as a property creation hook
// and runs inside that transaction.
func (s *Service) SeedProperty(ctx context.Context, in tenancy.PropertyCreated) error {
	created, err := s.q(ctx).SeedBedTypes(ctx, roomsdb.SeedBedTypesParams{TenantID: in.TenantID, PropertyID: in.PropertyID, ActorID: in.ActorID})
	if err != nil {
		return err
	}
	bd := in.BusinessDate
	return s.audit.Write(ctx, audit.Entry{
		TenantID: in.TenantID, PropertyID: &in.PropertyID, BusinessDate: &bd, UserID: in.ActorID,
		Action: "bed_types.seeded", EntityType: "property", EntityID: in.PropertyID, New: map[string]any{"created": created},
	})
}
