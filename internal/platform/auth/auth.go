// Package auth is the identity kernel shared by all modules: the authenticated
// caller (Principal), the permission catalogue and the Authorizer interface.
//
// Token verification lives in the iam module, which sets the Principal; business
// modules only read it and ask the Authorizer, so they never depend on iam.
package auth

import (
	"context"
	"net/http"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
)

// Principal is the authenticated caller.
type Principal struct {
	TenantID      int64
	UserID        int64
	SessionID     int64
	IsTenantAdmin bool
}

// ActorID returns the user id for created_by / audit columns, or nil.
func (p Principal) ActorID() *int64 {
	if p.UserID == 0 {
		return nil
	}
	id := p.UserID
	return &id
}

type principalKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller, if authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Require returns the caller or an UNAUTHENTICATED error.
func Require(ctx context.Context) (Principal, error) {
	if p, ok := PrincipalFrom(ctx); ok {
		return p, nil
	}
	return Principal{}, apperr.Unauthorized("UNAUTHENTICATED", "authentication is required")
}

// RequireTenantAdmin returns the caller if it is a tenant administrator.
func RequireTenantAdmin(ctx context.Context) (Principal, error) {
	p, err := Require(ctx)
	if err != nil {
		return Principal{}, err
	}
	if !p.IsTenantAdmin {
		return Principal{}, apperr.Forbidden("PERMISSION_DENIED", "this action requires a tenant administrator")
	}
	return p, nil
}

// Authorizer answers property-scoped access questions for the current caller.
//
//   - CanAccess returns PROPERTY_NOT_FOUND (404) when the caller has no access to
//     the property, so other tenants' and unassigned properties look identical.
//   - Require additionally returns PERMISSION_DENIED (403) when the caller's role at
//     the property lacks the permission. Tenant administrators pass every check.
type Authorizer interface {
	CanAccess(ctx context.Context, propertyID int64) error
	Require(ctx context.Context, propertyID int64, perm Permission) error
}

// RequireAuthenticated rejects requests without a principal (401 problem).
func RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Require(r.Context()); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
