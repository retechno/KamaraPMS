package reservations_test

import (
	"context"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

func intp(n int) *int { return &n }

// A complimentary or house use room needs the approval of a manager: the caller's own when they hold
// reservation.complimentary_approve, otherwise the credentials of someone who does.
func TestFreeRoomNeedsAManagerApproval(t *testing.T) {
	f := setup(t)
	comp := f.compPlan(t, "COMP", "COMPLIMENTARY")
	perms := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationComplimentary}
	clerk := f.User(t, f.tenantID, f.propID, perms...)
	_, managerEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationComplimentaryApprove)
	_, plainEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRead)
	mk := func() reservations.CreateInput {
		line := f.line(f.dlx, "2026-10-02", "2026-10-04")
		line.RatePlanID, line.OccupancyReason = comp, "Owner guest"
		in := f.input(false, line)
		in.GuestID = nil
		return in
	}

	_, err := f.Res.Create(clerk, f.propID, "", mk())
	if c := code(t, err, "APPROVAL_REQUIRED"); c.Context["permission"] != string(auth.PermReservationComplimentaryApprove) {
		t.Fatalf("context: %+v", c.Context)
	}
	in := mk()
	in.OccupancyApproval = &iam.ApprovalInput{Email: managerEmail, Password: "wrong"}
	_, err = f.Res.Create(clerk, f.propID, "", in)
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	in.OccupancyApproval = &iam.ApprovalInput{Email: plainEmail, Password: roomstest.Password}
	_, err = f.Res.Create(clerk, f.propID, "", in)
	wantCode(t, err, "APPROVAL_NOT_PERMITTED")
	in.OccupancyApproval = &iam.ApprovalInput{Email: managerEmail, Password: roomstest.Password}
	if _, err := f.Res.Create(clerk, f.propID, "", in); err != nil {
		t.Fatalf("approved: %v", err)
	}
	var approvedBy int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->>'free_room_approved_by')::bigint FROM audit_logs WHERE action = 'reservation.created'`).Scan(&approvedBy))
	if approvedBy == 0 {
		t.Fatal("the audit entry records the manager")
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE new_data::text LIKE '%`+roomstest.Password+`%'`) != 0 {
		t.Fatal("the password is never recorded")
	}

	// whoever holds the approval permission approves their own booking
	self := f.User(t, f.tenantID, f.propID, append(perms, auth.PermReservationComplimentaryApprove)...)
	in = mk()
	in.Rooms[0].Arrival, in.Rooms[0].Departure = d("2026-10-10"), d("2026-10-12")
	if _, err := f.Res.Create(self, f.propID, "", in); err != nil {
		t.Fatalf("self approval: %v", err)
	}
}

// The monthly quota of free nights: going over it is refused unless the request says so knowingly, the nights of the
// lines of one request count together, a stay across two months is counted in each, and a kind without a quota is free.
func TestFreeNightQuota(t *testing.T) {
	f := setup(t)
	comp, house := f.compPlan(t, "COMP", "COMPLIMENTARY"), f.compPlan(t, "HOUSE", "HOUSE_USE")
	free := func(plan int64, arrival, departure string) reservations.CreateInput {
		line := f.line(f.dlx, arrival, departure)
		line.RatePlanID, line.OccupancyReason = plan, "Owner guest"
		return f.input(true, line)
	}

	must(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "complimentary", intp(3)))
	quotas, err := f.Res.FreeNightQuotas(f.admin, f.propID)
	must(t, err)
	if len(quotas) != 1 || quotas[0].OccupancyKind != "COMPLIMENTARY" || quotas[0].MonthlyNights != 3 {
		t.Fatalf("quotas: %+v", quotas)
	}
	if _, err := f.Res.Create(f.admin, f.propID, "", free(comp, "2026-10-02", "2026-10-04")); err != nil { // 2 of 3 nights
		t.Fatal(err)
	}
	_, err = f.Res.Create(f.admin, f.propID, "", free(comp, "2026-10-10", "2026-10-12")) // 2 more: 4 of 3
	if c := code(t, err, "FREE_NIGHT_QUOTA_EXCEEDED"); c.Context["used"] != 2 || c.Context["quota"] != 3 || c.Context["requested"] != 2 || c.Context["month"] != "2026-10-01" {
		t.Fatalf("context: %+v", c.Context)
	}
	if f.Count(t, `SELECT count(*) FROM reservations`) != 1 {
		t.Fatal("a refused booking leaves nothing behind")
	}
	// knowingly over the quota
	over := free(comp, "2026-10-10", "2026-10-12")
	over.ExceedFreeQuota = true
	if _, err := f.Res.Create(f.admin, f.propID, "", over); err != nil {
		t.Fatalf("over the quota, knowingly: %v", err)
	}
	var exceeded string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT new_data->'free_quota_exceeded'->>0 FROM audit_logs WHERE action = 'reservation.created' ORDER BY id DESC LIMIT 1`).Scan(&exceeded))
	if exceeded != "COMPLIMENTARY 2026-10-01" {
		t.Fatalf("the audit entry says it went over: %q", exceeded)
	}

	// house use has no quota; the lines of one request count together; a stay across two months counts in each
	if _, err := f.Res.Create(f.admin, f.propID, "", free(house, "2026-10-14", "2026-10-18")); err != nil {
		t.Fatalf("no quota for house use: %v", err)
	}
	must(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "HOUSE_USE", intp(3)))
	two := free(house, "2026-10-19", "2026-10-21")
	two.Rooms = append(two.Rooms, two.Rooms[0])
	_, err = f.Res.Create(f.admin, f.propID, "", two)
	wantCode(t, err, "FREE_NIGHT_QUOTA_EXCEEDED") // 2 + 2 nights in one request, quota 3 (4 used already count too)

	// removing the quota lifts the limit
	must(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "COMPLIMENTARY", nil))
	if _, err := f.Res.Create(f.admin, f.propID, "", free(comp, "2026-10-05", "2026-10-09")); err != nil {
		t.Fatalf("no quota any more: %v", err)
	}

	// who may set it, and what it takes
	clerk := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	wantCode(t, f.Res.SetFreeNightQuota(clerk, f.propID, "COMPLIMENTARY", intp(5)), "PERMISSION_DENIED")
	wantCode(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "PAID", intp(5)), "VALIDATION_FAILED")
	wantCode(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "COMPLIMENTARY", intp(-1)), "VALIDATION_FAILED")
}

// Amending the dates of a free room counts the new nights without counting its own old ones twice.
func TestFreeNightQuotaOnAmend(t *testing.T) {
	f := setup(t)
	comp := f.compPlan(t, "COMP", "COMPLIMENTARY")
	must(t, f.Res.SetFreeNightQuota(f.admin, f.propID, "COMPLIMENTARY", intp(3)))
	line := f.line(f.dlx, "2026-10-02", "2026-10-04")
	line.RatePlanID, line.OccupancyReason = comp, "Owner guest"
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, line))
	must(t, err)

	three := d("2026-10-05")
	res, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, Departure: &three}) // 3 nights: at the quota
	must(t, err)
	four := d("2026-10-06")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, Departure: &four}) // 4 nights: over
	wantCode(t, err, "FREE_NIGHT_QUOTA_EXCEEDED")
}
