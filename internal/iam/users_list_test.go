package iam_test

import (
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
)

// KD-12: the list of users showed "No property access" for every staff member, because ListUsers did not load the
// grants. Each user of the page now carries its grants, the same as GetUser returns them, and a user without any stays empty.
func TestListUsersCarriesTheGrantsOfEachUser(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	e.tenantWithAdmin(t, "XYZ", "admin@xyz.com")
	adminCtx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))
	xyzCtx := e.as(t, e.login(t, "XYZ", "admin@xyz.com", pw))
	bali, jkt := e.property(t, adminCtx, "BALI"), e.property(t, adminCtx, "JKT")
	sg := e.property(t, xyzCtx, "SG")

	name, other := "Front Desk", "Cashier"
	perms := []auth.Permission{auth.PermFolioRead}
	fdRole, err := e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &perms})
	if err != nil {
		t.Fatal(err)
	}
	cashRole, err := e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &other, Permissions: &perms})
	if err != nil {
		t.Fatal(err)
	}
	xyzRole, err := e.iam.CreateRole(xyzCtx, iam.RoleInput{Name: &name, Permissions: &perms})
	if err != nil {
		t.Fatal(err)
	}
	two, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "fd@hotel.com", FullName: "Front Desk", Password: pw,
		Grants: []iam.GrantInput{{PropertyID: jkt, RoleID: cashRole.ID}, {PropertyID: bali, RoleID: fdRole.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	none, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "new@hotel.com", FullName: "New", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.iam.CreateUser(xyzCtx, iam.CreateUserInput{Email: "fd@xyz.com", FullName: "Other tenant", Password: pw,
		Grants: []iam.GrantInput{{PropertyID: sg, RoleID: xyzRole.ID}}}); err != nil {
		t.Fatal(err)
	}

	users, err := e.iam.ListUsers(adminCtx, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]iam.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	if len(byID) != 3 {
		t.Fatalf("users of ABC: %d, want 3 (the admin, fd, new)", len(byID))
	}
	got := byID[two.ID].Grants
	if len(got) != 2 || got[0].PropertyCode != "BALI" || got[0].RoleName != "Front Desk" || got[1].PropertyCode != "JKT" || got[1].RoleName != "Cashier" {
		t.Fatalf("grants of fd@hotel.com in the list: %+v", got)
	}
	want, err := e.iam.GetUser(adminCtx, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Grants) != len(got) || want.Grants[0] != got[0] || want.Grants[1] != got[1] {
		t.Fatalf("the list (%+v) and GetUser (%+v) disagree", got, want.Grants)
	}
	if g := byID[none.ID].Grants; g == nil || len(g) != 0 {
		t.Fatalf("a user without grants: %#v, want an empty list", g)
	}

	// The page boundary: a page that starts after the first user still carries the grants of its users.
	page, err := e.iam.ListUsers(adminCtx, two.ID-1, 1)
	if err != nil || len(page) != 1 || page[0].ID != two.ID || len(page[0].Grants) != 2 {
		t.Fatalf("one-user page: %v %+v", err, page)
	}
}
