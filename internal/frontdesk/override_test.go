package frontdesk_test

import (
	"context"
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// A walk-in and an extension take a rate override under the same rule as a booking: a reason, and the approval of
// someone who may approve it.
func TestRateOverrideOnWalkInAndExtension(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationRead, auth.PermGuestRead, auth.PermReservationOverrideRate)
	_, approverEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationOverrideApprove)
	approval := &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password}

	in := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r101.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-02"), AdultCount: 2,
		NightlyOverrides: []reservations.NightOverride{{Date: roomstest.BD, Amount: "700000"}}}
	_, err := f.Front.WalkIn(clerk, f.propID, "o1", in)
	wantCode(t, err, "VALIDATION_FAILED") // no reason
	in.RateOverrideReason = "Walk-in discount"
	_, err = f.Front.WalkIn(clerk, f.propID, "o2", in)
	wantCode(t, err, "APPROVAL_REQUIRED")
	in.RateOverrideApproval = approval
	out, err := f.Front.WalkIn(clerk, f.propID, "o3", in)
	must(t, err)
	if n := f.reload(t, out.Reservation.ID).Rooms[0].NightlyRates[0]; !n.IsOverride || n.Amount.String() != "700000" {
		t.Fatalf("the walk-in price: %+v", n)
	}

	// extending the stay with a new price for the extra night
	ext := frontdesk.ChangeDepartureInput{Version: out.Stay.Version, DepartureDate: d("2026-10-03"),
		NightlyOverrides: []reservations.NightOverride{{Date: d("2026-10-02"), Amount: "650000"}}}
	_, err = f.Front.ChangeDeparture(clerk, f.propID, out.Stay.ID, ext)
	wantCode(t, err, "VALIDATION_FAILED")
	ext.RateOverrideReason = "Extension offer"
	_, err = f.Front.ChangeDeparture(clerk, f.propID, out.Stay.ID, ext)
	wantCode(t, err, "APPROVAL_REQUIRED")
	ext.RateOverrideApproval = approval
	if _, err := f.Front.ChangeDeparture(clerk, f.propID, out.Stay.ID, ext); err != nil {
		t.Fatalf("extension: %v", err)
	}
	var approvedBy int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->>'override_approved_by')::bigint FROM audit_logs WHERE action = 'stay.departure_changed'`).Scan(&approvedBy))
	if approvedBy == 0 {
		t.Fatal("the extension records the approver")
	}
}
