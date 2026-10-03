package frontdesk_test

import (
	"context"
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// A walk-in on a complimentary plan needs the reason and the permission, and costs nothing.
func TestWalkInComplimentary(t *testing.T) {
	f := setup(t)
	var comp int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, occupancy_kind)
		SELECT tenant_id, property_id, 'COMP', 'Complimentary', room_charge_code_id, 'COMPLIMENTARY' FROM rate_plans WHERE id = $1 RETURNING id`, f.plan).Scan(&comp))
	in := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r101.ID, RatePlanID: comp, DepartureDate: d("2026-10-02"), AdultCount: 2}

	_, err := f.Front.WalkIn(f.admin, f.propID, "c1", in)
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "rooms[0].occupancy_reason" {
		t.Fatalf("a reason is required: %+v", c.Fields)
	}
	in.OccupancyReason = "Maintenance engineer"
	out, err := f.Front.WalkIn(f.admin, f.propID, "c2", in)
	must(t, err)
	res := f.reload(t, out.Reservation.ID)
	if l := res.Rooms[0]; l.OccupancyKind != "COMPLIMENTARY" || l.OccupancyReason != "Maintenance engineer" || l.NightlyRates[0].Amount.String() != "0" {
		t.Fatalf("line: %+v", l)
	}

	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermReservationCreate, auth.PermReservationRead, auth.PermGuestRead)
	in.RoomID = f.r102.ID
	_, err = f.Front.WalkIn(clerk, f.propID, "c3", in)
	wantCode(t, err, "PERMISSION_DENIED")

	// a clerk who may book free rooms but not approve them needs the manager's credentials
	booker := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermReservationCreate, auth.PermReservationRead, auth.PermGuestRead, auth.PermReservationComplimentary)
	_, managerEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationComplimentaryApprove)
	in.RoomID = f.r102.ID
	_, err = f.Front.WalkIn(booker, f.propID, "c4", in)
	wantCode(t, err, "APPROVAL_REQUIRED")
	in.OccupancyApproval = &iam.ApprovalInput{Email: managerEmail, Password: roomstest.Password}
	if _, err := f.Front.WalkIn(booker, f.propID, "c5", in); err != nil && !apperr.IsCode(err, "ROOM_NOT_READY") {
		t.Fatalf("approved walk-in: %v", err)
	}
}
