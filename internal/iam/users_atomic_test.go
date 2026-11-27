package iam_test

import (
	"context"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
)

// Saving a user from the screen was two requests (the profile, then the grants): when the second failed, the profile was
// saved and the access not. PATCH /users/{id} now takes the grants too, in the same transaction: both are saved, or neither.
func TestUpdateUserSavesTheProfileAndTheGrantsTogether(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	e.tenantWithAdmin(t, "XYZ", "admin@xyz.com")
	adminCtx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))
	xyzCtx := e.as(t, e.login(t, "XYZ", "admin@xyz.com", pw))
	bali, jkt := e.property(t, adminCtx, "BALI"), e.property(t, adminCtx, "JKT")
	sg := e.property(t, xyzCtx, "SG")
	name := "Front Desk"
	perms := []auth.Permission{auth.PermFolioRead}
	role, err := e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &perms})
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "fd@hotel.com", FullName: "Front Desk", Password: pw,
		Grants: []iam.GrantInput{{PropertyID: bali, RoleID: role.ID}}})
	if err != nil {
		t.Fatal(err)
	}

	// both in one request
	renamed := "Dewi Anggraeni"
	grants := []iam.GrantInput{{PropertyID: jkt, RoleID: role.ID}, {PropertyID: bali, RoleID: role.ID}}
	out, err := e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{FullName: &renamed, Grants: &grants})
	if err != nil {
		t.Fatal(err)
	}
	if out.FullName != renamed || len(out.Grants) != 2 || out.Grants[0].PropertyCode != "BALI" || out.Grants[1].PropertyCode != "JKT" {
		t.Fatalf("saved together: %+v", out)
	}
	if n := e.auditCount(t, u.ID, "user.grants_replaced"); n != 1 {
		t.Fatalf("user.grants_replaced entries: %d, want 1 (the same trail as PUT /properties)", n)
	}

	// a grant the database refuses (another tenant's property) leaves the profile as it was: nothing half saved
	again := "Half Saved"
	bad := []iam.GrantInput{{PropertyID: sg, RoleID: role.ID}}
	_, err = e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{FullName: &again, Grants: &bad})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	got, err := e.iam.GetUser(adminCtx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != renamed || len(got.Grants) != 2 {
		t.Fatalf("after the refused grant: %+v, want the name %q and the two grants kept", got, renamed)
	}

	// grants that do not validate are refused before anything is written
	twice := []iam.GrantInput{{PropertyID: bali, RoleID: role.ID}, {PropertyID: bali, RoleID: role.ID}}
	_, err = e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{FullName: &again, Grants: &twice})
	wantCode(t, err, "VALIDATION_FAILED")

	// a profile the rules refuse leaves the grants as they were
	admin, err := e.iam.ListUsers(adminCtx, 0, 1)
	if err != nil || len(admin) != 1 || !admin[0].IsTenantAdmin {
		t.Fatalf("the admin: %v %+v", err, admin)
	}
	off := false
	none := []iam.GrantInput{{PropertyID: jkt, RoleID: role.ID}}
	_, err = e.iam.UpdateUser(adminCtx, admin[0].ID, iam.UserPatch{IsActive: &off, Grants: &none})
	wantCode(t, err, "LAST_ADMIN")
	if got, err := e.iam.GetUser(adminCtx, admin[0].ID); err != nil || len(got.Grants) != 0 {
		t.Fatalf("grants of the last admin after the refused deactivation: %v %+v", err, got.Grants)
	}

	// without grants in the patch, the grants are not touched
	third := "Dewi A."
	out, err = e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{FullName: &third})
	if err != nil || len(out.Grants) != 2 {
		t.Fatalf("a patch without grants: %v %+v", err, out)
	}
	// an empty list takes every grant away
	empty := []iam.GrantInput{}
	out, err = e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{Grants: &empty})
	if err != nil || len(out.Grants) != 0 || out.FullName != third {
		t.Fatalf("an empty grant list: %v %+v", err, out)
	}
}

func (e env) auditCount(t *testing.T, userID int64, action string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE entity_type = 'user' AND entity_id = $1 AND action = $2`, userID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
