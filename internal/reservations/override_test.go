package reservations_test

import (
	"context"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

func overrideOn(in *reservations.CreateInput, date string, amount string) {
	in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d(date), Amount: amount}}
}

// A rate override needs three things: the permission to enter one (reservation.override_rate), a reason, and an
// approval, which is their own when the person holds reservation.override_rate_approve and otherwise the credentials
// of someone who does.
func TestRateOverrideNeedsApproval(t *testing.T) {
	f := setup(t)
	guestRead := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationOverrideRate}
	clerk := f.User(t, f.tenantID, f.propID, guestRead...)
	_, approverEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationOverrideApprove)
	_, nobodyEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRead)
	mk := func() reservations.CreateInput {
		in := f.input(false, f.line(f.dlx, "2026-10-02", "2026-10-04")) // a draft: the clerk may not read the guest
		in.GuestID = nil
		overrideOn(&in, "2026-10-02", "700000")
		return in
	}

	// no reason: refused whoever asks
	in := mk()
	_, err := f.Res.Create(f.admin, f.propID, "", in)
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "rate_override_reason" {
		t.Fatalf("fields: %+v", c.Fields)
	}

	// a clerk who may override but not approve needs the credentials of an approver
	in = mk()
	in.RateOverrideReason = "Regular guest"
	_, err = f.Res.Create(clerk, f.propID, "", in)
	if c := code(t, err, "APPROVAL_REQUIRED"); c.Context["permission"] != string(auth.PermReservationOverrideApprove) {
		t.Fatalf("context: %+v", c.Context)
	}
	in.RateOverrideApproval = &iam.ApprovalInput{Email: approverEmail, Password: "wrong"}
	_, err = f.Res.Create(clerk, f.propID, "", in)
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	in.RateOverrideApproval = &iam.ApprovalInput{Email: nobodyEmail, Password: roomstest.Password}
	_, err = f.Res.Create(clerk, f.propID, "", in)
	wantCode(t, err, "APPROVAL_NOT_PERMITTED") // a valid user, but not one who approves overrides
	if f.Count(t, `SELECT count(*) FROM reservations`) != 0 {
		t.Fatal("a refused override books nothing")
	}

	in.RateOverrideApproval = &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password}
	res, err := f.Res.Create(clerk, f.propID, "", in)
	must(t, err)
	if n := res.Rooms[0].NightlyRates[0]; !n.IsOverride || n.Amount.String() != "700000" {
		t.Fatalf("the override is kept: %+v", n)
	}
	// the audit entry says who approved and why, and never the password
	var approvedBy int64
	var reason string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->>'override_approved_by')::bigint, new_data->>'rate_override_reason' FROM audit_logs WHERE action = 'reservation.created'`).Scan(&approvedBy, &reason))
	if approvedBy == 0 || reason != "Regular guest" {
		t.Fatalf("audit: %d %q", approvedBy, reason)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE new_data::text LIKE '%`+roomstest.Password+`%'`) != 0 {
		t.Fatal("the password is never recorded")
	}
	// the stored idempotency hash does not depend on the credentials
	a, b := mk(), mk()
	a.RateOverrideApproval = &iam.ApprovalInput{Email: "a@x.test", Password: "one"}
	b.RateOverrideApproval = &iam.ApprovalInput{Email: "b@x.test", Password: "two"}
	if a.Hash() != b.Hash() {
		t.Fatal("credentials are not part of the hash")
	}

	// whoever holds the approval permission approves their own override
	self := f.User(t, f.tenantID, f.propID, append(guestRead, auth.PermReservationOverrideApprove)...)
	in = mk()
	in.Rooms[0].Arrival, in.Rooms[0].Departure = d("2026-10-10"), d("2026-10-12")
	overrideOn(&in, "2026-10-10", "650000")
	in.RateOverrideReason = "Corporate rate"
	if _, err := f.Res.Create(self, f.propID, "", in); err != nil {
		t.Fatalf("self approval: %v", err)
	}

	// without reservation.override_rate there is no override at all, approval or not
	plain := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate)
	in.RateOverrideApproval = &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password}
	_, err = f.Res.Create(plain, f.propID, "", in)
	wantCode(t, err, "PERMISSION_DENIED")
}

// Adding a room and amending one follow the same rule, with one approval per request however many lines it has.
func TestRateOverrideOnAddAndAmend(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationOverrideRate)
	_, approverEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationOverrideApprove)
	res := f.book(t, f.dlx, "2026-10-02", "2026-10-04")

	extra := f.line(f.std, "2026-10-02", "2026-10-04")
	extra.Overrides = []reservations.NightOverride{{Date: d("2026-10-03"), Amount: "300000"}}
	_, err := f.Res.AddLine(clerk, f.propID, res.ID, res.Version, extra)
	wantCode(t, err, "VALIDATION_FAILED") // no reason
	extra.RateOverrideReason = "Upgrade offer"
	_, err = f.Res.AddLine(clerk, f.propID, res.ID, res.Version, extra)
	wantCode(t, err, "APPROVAL_REQUIRED")
	extra.RateOverrideApproval = &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password}
	res, err = f.Res.AddLine(clerk, f.propID, res.ID, res.Version, extra)
	must(t, err)

	patch := reservations.LinePatch{Version: res.Version, Overrides: []reservations.NightOverride{{Date: d("2026-10-02"), Amount: "900000"}}}
	_, err = f.Res.AmendLine(clerk, f.propID, res.ID, res.Rooms[0].ID, patch)
	wantCode(t, err, "VALIDATION_FAILED")
	patch.RateOverrideReason = "Price match"
	patch.RateOverrideApproval = &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password}
	got, err := f.Res.AmendLine(clerk, f.propID, res.ID, res.Rooms[0].ID, patch)
	must(t, err)
	if n := got.Rooms[0].NightlyRates[0]; !n.IsOverride || n.Amount.String() != "900000" {
		t.Fatalf("amended: %+v", n)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('reservation.room_added', 'reservation.room_amended') AND new_data ? 'override_approved_by'`) != 2 {
		t.Fatal("both entries record the approver")
	}
}
