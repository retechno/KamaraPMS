// Package iam is identity and access management: login, sessions (rotating
// refresh tokens), users, roles, property grants and authorization.
package iam

import (
	"net/mail"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// User is a login identity (never exposes the password hash).
type User struct {
	ID            int64      `json:"id"`
	Email         string     `json:"email"`
	FullName      string     `json:"full_name"`
	IsTenantAdmin bool       `json:"is_tenant_admin"`
	IsActive      bool       `json:"is_active"`
	LastLoginAt   *time.Time `json:"last_login_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	Grants        []Grant    `json:"grants"`
}

// Grant gives a user one role at one property.
type Grant struct {
	PropertyID   int64  `json:"property_id"`
	PropertyCode string `json:"property_code"`
	PropertyName string `json:"property_name"`
	RoleID       int64  `json:"role_id"`
	RoleName     string `json:"role_name"`
}

// GrantInput is a requested (property, role) pair.
type GrantInput struct {
	PropertyID int64 `json:"property_id"`
	RoleID     int64 `json:"role_id"`
}

// Role is a named set of permissions.
type Role struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	IsSystem    bool              `json:"is_system"`
	Permissions []auth.Permission `json:"permissions"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Me is the caller's identity and what they can do where.
type Me struct {
	User       User         `json:"user"`
	Tenant     MeTenant     `json:"tenant"`
	Properties []MeProperty `json:"properties"`
}

// MeTenant identifies the caller's tenant.
type MeTenant struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// MeProperty is a property the caller can access, with their permissions there.
type MeProperty struct {
	ID          int64             `json:"id"`
	Code        string            `json:"code"`
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	Permissions []auth.Permission `json:"permissions"`
}

// Session is the result of login or refresh.
type Session struct {
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int       `json:"expires_in"`
	ExpiresAt        time.Time `json:"expires_at"`
	User             User      `json:"user"`
	RefreshToken     string    `json:"-"` // delivered as an httpOnly cookie only
	RefreshExpiresAt time.Time `json:"-"`
}

// normalizeEmail lower-cases and validates an email address.
func normalizeEmail(s string) (string, *apperr.FieldError) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return s, &apperr.FieldError{Field: "email", Code: "INVALID_FORMAT", Message: "a valid email address"}
	}
	return s, nil
}

func validateFullName(s string) *apperr.FieldError {
	if n := len(strings.TrimSpace(s)); n == 0 || n > 200 {
		return &apperr.FieldError{Field: "full_name", Code: "REQUIRED", Message: "1-200 characters"}
	}
	return nil
}

// normalizePermissions validates codes against the catalogue and removes duplicates.
func normalizePermissions(in []auth.Permission) ([]auth.Permission, []apperr.FieldError) {
	seen := map[auth.Permission]bool{}
	var out []auth.Permission
	var errs []apperr.FieldError
	for _, p := range in {
		if !auth.ValidPermission(p) {
			errs = append(errs, apperr.FieldError{Field: "permissions", Code: "UNKNOWN_PERMISSION", Message: string(p)})
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, errs
}
