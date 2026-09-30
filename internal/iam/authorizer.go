package iam

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/iam/iamdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Authorizer implements auth.Authorizer against user_properties and role_permissions.
type Authorizer struct {
	txm *db.TxManager
}

// NewAuthorizer returns the database-backed authorizer.
func NewAuthorizer(txm *db.TxManager) *Authorizer { return &Authorizer{txm: txm} }

var _ auth.Authorizer = (*Authorizer)(nil)

func propertyNotFound() error {
	return apperr.NotFound("PROPERTY_NOT_FOUND", "the property does not exist or is not accessible")
}

// CanAccess checks that the caller may see the property at all.
func (a *Authorizer) CanAccess(ctx context.Context, propertyID int64) error {
	_, err := a.check(ctx, propertyID, "")
	return err
}

// Require checks that the caller holds perm at the property.
func (a *Authorizer) Require(ctx context.Context, propertyID int64, perm auth.Permission) error {
	allowed, err := a.check(ctx, propertyID, perm)
	if err != nil {
		return err
	}
	if !allowed {
		return apperr.Forbidden("PERMISSION_DENIED", "your role at this property does not allow this action").
			WithContext("permission", string(perm))
	}
	return nil
}

// PropertiesWith lists the properties where the caller holds perm (all tenant properties for an administrator).
func (a *Authorizer) PropertiesWith(ctx context.Context, perm auth.Permission) ([]int64, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	q := iamdb.New(a.txm.DB(ctx))
	if p.IsTenantAdmin {
		return q.ListTenantPropertyIDs(ctx, p.TenantID)
	}
	return q.ListPropertiesWithPermission(ctx, iamdb.ListPropertiesWithPermissionParams{
		TenantID: p.TenantID, UserID: p.UserID, PermissionCode: string(perm),
	})
}

func (a *Authorizer) check(ctx context.Context, propertyID int64, perm auth.Permission) (bool, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return false, err
	}
	q := iamdb.New(a.txm.DB(ctx))
	if p.IsTenantAdmin {
		ok, err := q.PropertyInTenant(ctx, iamdb.PropertyInTenantParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return false, err
		}
		if !ok {
			return false, propertyNotFound()
		}
		return true, nil
	}
	allowed, err := q.GetGrantPermission(ctx, iamdb.GetGrantPermissionParams{
		TenantID: p.TenantID, UserID: p.UserID, PropertyID: propertyID, PermissionCode: string(perm),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, propertyNotFound() // no grant: the property is invisible to this user
	}
	return allowed, err
}
