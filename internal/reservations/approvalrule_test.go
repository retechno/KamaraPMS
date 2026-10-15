package reservations_test

import (
	"context"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// The rules of approval (docs/architecture/32-distinct-approver.md) for the sale: a rate override (the sum of what the nights move over every room of the request), a free room (the value
// of its nights at the reference rate plan) and a sales restriction (a kind without an amount).

func (f *fx) idOf(t *testing.T, email string) (id int64) {
	t.Helper()
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id))
	return
}

func TestTheRuleOfARateOverrideAddsTheRoomsOfTheRequest(t *testing.T) {
	f := setup(t)
	perms := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationOverrideRate, auth.PermReservationOverrideApprove}
	lead := f.User(t, f.tenantID, f.propID, perms...) // enters the override and may approve it
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationOverrideApprove)
	f.SetRule(t, f.tenantID, f.propID, "RATE_OVERRIDE", "DISTINCT_ABOVE", "200000")
	mk := func(rooms int, amount string) reservations.CreateInput {
		var lines []reservations.LineInput
		for i := 0; i < rooms; i++ {
			l := f.line(f.dlx, "2026-10-02", "2026-10-03") // one night at 1,000,000
			l.Overrides = []reservations.NightOverride{{Date: d("2026-10-02"), Amount: amount}}
			lines = append(lines, l)
		}
		in := f.input(false, lines...)
		in.GuestID = nil
		in.RateOverrideReason = "Regular guest"
		return in
	}

	// one room 150,000 under the price: their own approval is within the limit
	if _, err := f.Res.Create(lead, f.propID, "", mk(1, "850000")); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
	// 200,000 exactly: allowed
	if _, err := f.Res.Create(lead, f.propID, "", mk(1, "800000")); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	before := f.Count(t, `SELECT count(*) FROM reservations`)
	// two rooms of 150,000 each: the request moves 300,000, above the limit although each room is below it
	_, err := f.Res.Create(lead, f.propID, "", mk(2, "850000"))
	e := code(t, err, "APPROVAL_REQUIRED") // their own is not enough: the credentials of another person are asked for
	if e.Context["kind"] != "RATE_OVERRIDE" || e.Context["amount"] != "300000" || e.Context["threshold"] != "200000" {
		t.Fatalf("the context: %v", e.Context)
	}
	if f.Count(t, `SELECT count(*) FROM reservations`) != before {
		t.Fatal("a refused approval books nothing")
	}
	// the same request with the credentials of someone else
	in := mk(2, "850000")
	in.RateOverrideApproval = &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	res, err := f.Res.Create(lead, f.propID, "", in)
	must(t, err)
	var by int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->>'override_approved_by')::bigint FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1`, res.ID).Scan(&by))
	if by != f.idOf(t, otherEmail) {
		t.Fatalf("approved by %d", by)
	}

	// an administrator who enters and approves their own, under DISTINCT: accepted and marked in the entry
	f.SetRule(t, f.tenantID, f.propID, "RATE_OVERRIDE", "DISTINCT")
	adminCtx, _ := f.AdminAccount(t, f.tenantID)
	res, err = f.Res.Create(adminCtx, f.propID, "", mk(1, "990000"))
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1 AND new_data->>'self_approved_by_admin' = 'true'`, res.ID) != 1 {
		t.Fatal("the administrator's own approval is marked")
	}
	_, err = f.Res.Create(lead, f.propID, "", mk(1, "990000"))
	wantCode(t, err, "APPROVAL_REQUIRED") // under DISTINCT even a small override of someone who approves needs another person
}

func TestTheRuleOfAFreeRoomIsTheValueOfItsNights(t *testing.T) {
	f := setup(t)
	comp := f.compPlan(t, "COMP", "COMPLIMENTARY")
	must(t, f.Exec(t, `UPDATE rate_plans SET is_reference = true WHERE id = $1`, f.plan)) // the nights are valued at the BAR plan: DLX 1,000,000 a night
	perms := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationComplimentary, auth.PermReservationComplimentaryApprove}
	manager := f.User(t, f.tenantID, f.propID, perms...)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationComplimentaryApprove)
	f.SetRule(t, f.tenantID, f.propID, "FREE_ROOM", "DISTINCT_ABOVE", "2000000")
	mk := func(from, to string) reservations.CreateInput {
		line := f.line(f.dlx, from, to)
		line.RatePlanID, line.OccupancyReason = comp, "Owner guest"
		in := f.input(false, line)
		in.GuestID = nil
		return in
	}
	// two nights are worth 2,000,000: at the limit, their own
	if _, err := f.Res.Create(manager, f.propID, "", mk("2026-10-02", "2026-10-04")); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	// three nights are worth 3,000,000: someone else must approve
	_, err := f.Res.Create(manager, f.propID, "", mk("2026-10-10", "2026-10-13"))
	e := code(t, err, "APPROVAL_REQUIRED")
	if e.Context["kind"] != "FREE_ROOM" || e.Context["amount"] != "3000000" {
		t.Fatalf("the value of the nights: %v", e.Context)
	}
	in := mk("2026-10-10", "2026-10-13")
	in.OccupancyApproval = &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	if _, err := f.Res.Create(manager, f.propID, "", in); err != nil {
		t.Fatalf("another approver: %v", err)
	}
}

func TestTheRuleOfASalesRestrictionHasNoAmount(t *testing.T) {
	f := setup(t)
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	base := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationOverrideRestriction, auth.PermReservationRestrictionApprove}
	lead := f.User(t, f.tenantID, f.propID, base...)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRestrictionApprove)
	f.SetRule(t, f.tenantID, f.propID, "SALES_RESTRICTION", "DISTINCT")
	mk := func(ro *reservations.RestrictionOverride) reservations.CreateInput {
		in := f.input(false, f.line(f.dlx, "2026-10-04", "2026-10-07"))
		in.GuestID = nil
		in.RestrictionOverride = ro
		return in
	}
	_, err := f.Res.Create(lead, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP"}))
	wantCode(t, err, "APPROVAL_REQUIRED")
	res, err := f.Res.Create(lead, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}}))
	must(t, err)
	var by int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->'restriction_override'->>'approved_by')::bigint FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1`, res.ID).Scan(&by))
	if by != f.idOf(t, otherEmail) {
		t.Fatalf("approved by %d", by)
	}
	// the administrator approves their own: marked
	adminCtx, _ := f.AdminAccount(t, f.tenantID)
	res, err = f.Res.Create(adminCtx, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "Owner"}))
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1 AND new_data->>'self_approved_by_admin' = 'true'`, res.ID) != 1 {
		t.Fatal("marked")
	}
}
