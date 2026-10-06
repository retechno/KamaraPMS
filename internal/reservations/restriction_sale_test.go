package reservations_test

import (
	"context"
	"encoding/json"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// Sales restrictions on a sale (docs/architecture/18-architecture-decisions.md, decision 2): every path that sells a night asks the one evaluator and refuses with 409
// STAY_RESTRICTED, except what is not a sale. A person who may override does it with a reason and an approval, and it is audited.

func bp(b bool) *bool { return &b }
func ip(n int) *int   { return &n }

// restrict fills the grid with the real service (rate.manage).
func (f *fx) restrict(t *testing.T, roomType, ratePlan *int64, from, to string, set rates.RestrictionSet) {
	t.Helper()
	in := rates.FillRestrictionsInput{From: d(from), To: d(to), Set: set}
	if roomType != nil {
		in.RoomTypeIDs = []int64{*roomType}
	}
	if ratePlan != nil {
		in.RatePlanIDs = []int64{*ratePlan}
	}
	if _, err := f.Rates.FillRestrictions(f.admin, f.propID, in); err != nil {
		t.Fatal(err)
	}
}

// restricted asserts the error is the refusal of a restriction and returns its context.
func restricted(t *testing.T, err error, wantTypes ...string) map[string]any {
	t.Helper()
	c := code(t, err, "STAY_RESTRICTED")
	raw, _ := json.Marshal(c.Context["violations"])
	var vs []struct {
		Type string `json:"type"`
	}
	if e := json.Unmarshal(raw, &vs); e != nil {
		t.Fatal(e)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.Type)
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("violations %v, want %v", got, wantTypes)
	}
	for i := range got {
		if got[i] != wantTypes[i] {
			t.Fatalf("violations %v, want %v", got, wantTypes)
		}
	}
	return c.Context
}

func TestEverySalePathAsksTheSalesRestrictions(t *testing.T) {
	// the night of 2026-10-05 is closed for sale for DLX, every plan
	closed := func(f *fx) {
		f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	}

	t.Run("create a draft", func(t *testing.T) {
		f := setup(t)
		closed(f)
		_, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.dlx, "2026-10-04", "2026-10-07")))
		restricted(t, err, "STOP_SELL")
		if n := f.count(t, `SELECT count(*) FROM reservations`); n != 0 {
			t.Fatalf("a refused sale writes nothing: %d", n)
		}
	})
	t.Run("create a confirmed reservation", func(t *testing.T) {
		f := setup(t)
		closed(f)
		_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-05", "2026-10-06")))
		restricted(t, err, "STOP_SELL")
		// another room type, and a stay that does not touch the night, are not refused
		f.book(t, f.std, "2026-10-05", "2026-10-06")
		f.book(t, f.dlx, "2026-10-06", "2026-10-07")
	})
	t.Run("a room added to a reservation", func(t *testing.T) {
		f := setup(t)
		closed(f)
		res := f.book(t, f.std, "2026-10-02", "2026-10-03")
		_, err := f.Res.AddLine(f.admin, f.propID, res.ID, res.Version, f.line(f.dlx, "2026-10-04", "2026-10-07"))
		restricted(t, err, "STOP_SELL")
	})
	t.Run("amended dates that sell a closed night, in a draft and in a confirmed reservation", func(t *testing.T) {
		f := setup(t)
		closed(f)
		for _, confirm := range []bool{false, true} {
			res, err := f.Res.Create(f.admin, f.propID, "", f.input(confirm, f.line(f.dlx, "2026-10-02", "2026-10-04")))
			must(t, err)
			later := d("2026-10-07")
			_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, Departure: &later})
			restricted(t, err, "STOP_SELL")
		}
	})
	t.Run("an amended room type or rate plan is a new sale", func(t *testing.T) {
		f := setup(t)
		closed(f)
		res := f.book(t, f.std, "2026-10-05", "2026-10-06")
		_, err := f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RoomTypeID: &f.dlx.ID})
		restricted(t, err, "STOP_SELL")
		// a plan that is closed for the night (a plan-level row), the room type being open
		corp := f.compPlan(t, "CORP", "PAID")
		f.restrict(t, nil, &corp, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
		_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RatePlanID: &corp})
		restricted(t, err, "STOP_SELL")
	})
	t.Run("the confirmation of a draft made before the night was closed", func(t *testing.T) {
		f := setup(t)
		res, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.dlx, "2026-10-04", "2026-10-07")))
		must(t, err)
		closed(f)
		_, err = f.Res.Confirm(f.admin, f.propID, res.ID, res.Version)
		restricted(t, err, "STOP_SELL")
		if n := f.count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CONFIRMED'`); n != 0 {
			t.Fatalf("nothing is confirmed: %d", n)
		}
	})
	t.Run("the reinstatement of a cancelled reservation", func(t *testing.T) {
		f := setup(t)
		res := f.book(t, f.dlx, "2026-10-04", "2026-10-07")
		c, err := f.Res.Cancel(f.admin, f.propID, res.ID, res.Version, "changed plans")
		must(t, err)
		closed(f)
		_, err = f.Res.Reinstate(f.admin, f.propID, res.ID, c.Reservation.Version)
		restricted(t, err, "STOP_SELL")
	})
	t.Run("every kind of restriction on a new sale", func(t *testing.T) {
		f := setup(t)
		f.restrict(t, &f.dlx.ID, nil, "2026-10-08", "2026-10-09", rates.RestrictionSet{ClosedToArrival: bp(true), MinStay: ip(3)}) // the 8th: no arrival, at least three nights
		f.restrict(t, &f.dlx.ID, nil, "2026-10-09", "2026-10-10", rates.RestrictionSet{MaxStay: ip(2)})                            // the 9th: at most two nights
		f.restrict(t, &f.dlx.ID, nil, "2026-10-11", "2026-10-12", rates.RestrictionSet{ClosedToDeparture: bp(true)})               // the 11th: no departure
		_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-08", "2026-10-10")))
		ctx := restricted(t, err, "CLOSED_TO_ARRIVAL", "MIN_STAY") // two nights: under the minimum of 3
		if ctx["overridable"] != true {
			t.Fatalf("a phone booking can be overridden: %v", ctx)
		}
		_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-09", "2026-10-11")))
		restricted(t, err, "CLOSED_TO_DEPARTURE") // two nights from the 9th are within the maximum, but the 11th is closed to departure
		_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-09", "2026-10-12")))
		restricted(t, err, "MAX_STAY")               // three nights from the 9th: over the maximum of two (the 12th is open to departure)
		f.book(t, f.dlx, "2026-10-09", "2026-10-10") // one night from the 9th is fine
	})
	t.Run("the offers of a search say so, and agree with the booking", func(t *testing.T) {
		f := setup(t)
		corp := f.compPlan(t, "CORP", "PAID")
		closed(f)
		f.restrict(t, &f.dlx.ID, &corp, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(false)}) // CORP is open inside the closed room type
		res, err := f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-04"), d("2026-10-07"), 2, 0)
		must(t, err)
		o := dlxOffer(t, res, f.dlx.ID)
		for _, po := range o.RatePlans {
			switch po.Code {
			case "BAR":
				if po.Bookable || len(po.Restrictions) != 1 || po.Restrictions[0].Type != "STOP_SELL" {
					t.Fatalf("BAR is closed: %+v", po)
				}
			case "CORP":
				if !po.Bookable || po.Restrictions == nil || len(po.Restrictions) != 0 {
					t.Fatalf("CORP is open, and says so with an empty list: %+v", po)
				}
			}
		}
		// the variants of the room type carry the same verdicts
		for _, b := range o.Beds {
			for _, po := range b.RatePlans {
				if po.Code == "BAR" && po.Bookable {
					t.Fatalf("a variant is as closed as its room type: %+v", po)
				}
			}
		}
		// what the search says is what the booking does
		_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-04", "2026-10-07")))
		restricted(t, err, "STOP_SELL")
		_, err = f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: corp, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "900000"})
		must(t, err)
		line := f.line(f.dlx, "2026-10-04", "2026-10-07")
		line.RatePlanID = corp
		if _, err := f.Res.Create(f.admin, f.propID, "", f.input(true, line)); err != nil {
			t.Fatalf("CORP books: %v", err)
		}
	})
}

// What is not a sale is not asked, and a guest keeps what was sold.
func TestWhatIsNotASaleIsNotRefusedByARestriction(t *testing.T) {
	f := setup(t)
	king := f.bed(t, "KING")
	res := f.book(t, f.dlx, "2026-10-04", "2026-10-07")
	line := res.Rooms[0]
	// the nights the guest has are closed afterwards, and so is the arrival and the departure
	f.restrict(t, &f.dlx.ID, nil, "2026-10-04", "2026-10-07", rates.RestrictionSet{StopSell: bp(true), ClosedToArrival: bp(true), ClosedToDeparture: bp(true), MinStay: ip(9)})
	f.restrict(t, &f.dlx.ID, nil, "2026-10-07", "2026-10-08", rates.RestrictionSet{ClosedToDeparture: bp(true)})

	adults := 1
	got, err := f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: res.Version, Adults: &adults, BedTypeID: &king.ID})
	must(t, err) // the guests and the bed
	got, err = f.Res.AssignRoom(f.admin, f.propID, res.ID, line.ID, got.Version, f.r101.ID, false)
	must(t, err) // a room
	got, err = f.Res.UnassignRoom(f.admin, f.propID, res.ID, line.ID, got.Version)
	must(t, err)
	// shorter, inside the nights that were sold: nothing new is asked of the nights, and the arrival is the same;
	// the departure is new (the 6th), and it is not closed to departure
	earlier := d("2026-10-06")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Departure: &earlier})
	restricted(t, err, "CLOSED_TO_DEPARTURE", "MIN_STAY") // the departure and the length are new: those are asked, not the nights nor the arrival
	// a cancellation is not a sale
	if _, err := f.Res.Cancel(f.admin, f.propID, res.ID, got.Version, "changed plans"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// The override: a person who holds reservation.override_restriction, a reason, and an approval.
func TestAnOverrideNeedsAPermissionAReasonAndAnApproval(t *testing.T) {
	f := setup(t)
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	base := []auth.Permission{auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate}
	mk := func(ro *reservations.RestrictionOverride) reservations.CreateInput {
		in := f.input(false, f.line(f.dlx, "2026-10-04", "2026-10-07")) // a draft is a sale too; the clerk has no booker to give
		in.GuestID = nil
		in.RestrictionOverride = ro
		return in
	}
	_, managerEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRestrictionApprove)
	_, plainEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRead)

	// a clerk without the permission to override is refused even with a reason and a valid approval
	plain := f.User(t, f.tenantID, f.propID, base...)
	_, err := f.Res.Create(plain, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP", Approval: &iam.ApprovalInput{Email: managerEmail, Password: roomstest.Password}}))
	wantCode(t, err, "PERMISSION_DENIED")

	clerk := f.User(t, f.tenantID, f.propID, append(base, auth.PermReservationOverrideRestriction)...)
	_, err = f.Res.Create(clerk, f.propID, "", mk(&reservations.RestrictionOverride{}))
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Field != "restriction_override.reason" {
		t.Fatalf("the reason is required: %+v", c.Fields)
	}
	// without an approver
	_, err = f.Res.Create(clerk, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP"}))
	if c := code(t, err, "APPROVAL_REQUIRED"); c.Context["permission"] != string(auth.PermReservationRestrictionApprove) {
		t.Fatalf("context: %+v", c.Context)
	}
	_, err = f.Res.Create(clerk, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP", Approval: &iam.ApprovalInput{Email: managerEmail, Password: "wrong"}}))
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	_, err = f.Res.Create(clerk, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP", Approval: &iam.ApprovalInput{Email: plainEmail, Password: roomstest.Password}}))
	wantCode(t, err, "APPROVAL_NOT_PERMITTED")
	if n := f.count(t, `SELECT count(*) FROM reservations`); n != 0 {
		t.Fatalf("nothing is written until the override is approved: %d", n)
	}

	// approved with the credentials of a manager: the sale goes through and the audit entry says who approved, why and what was broken
	res, err := f.Res.Create(clerk, f.propID, "", mk(&reservations.RestrictionOverride{Reason: "VIP guest of the owner", Approval: &iam.ApprovalInput{Email: managerEmail, Password: roomstest.Password}}))
	if err != nil {
		t.Fatalf("approved: %v", err)
	}
	var approvedBy int64
	var reason, kind string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->'restriction_override'->>'approved_by')::bigint, new_data->'restriction_override'->>'reason', new_data->'restriction_override'->'violations'->0->>'type'
		FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1`, res.ID).Scan(&approvedBy, &reason, &kind))
	if approvedBy == 0 || reason != "VIP guest of the owner" || kind != "STOP_SELL" {
		t.Fatalf("the audit entry: %d %q %q", approvedBy, reason, kind)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE new_data::text LIKE '%`+roomstest.Password+`%'`) != 0 {
		t.Fatal("the password is never recorded")
	}

	// whoever holds both permissions approves their own override
	self := f.User(t, f.tenantID, f.propID, append(base, auth.PermReservationOverrideRestriction, auth.PermReservationRestrictionApprove)...)
	in := mk(&reservations.RestrictionOverride{Reason: "Owner stays"})
	in.Rooms[0].Arrival, in.Rooms[0].Departure = d("2026-10-05"), d("2026-10-06")
	if _, err := f.Res.Create(self, f.propID, "", in); err != nil {
		t.Fatalf("own approval: %v", err)
	}

	// an override for a stay that breaks nothing is not asked for and not recorded
	free := mk(&reservations.RestrictionOverride{Reason: "not needed"})
	free.Rooms[0].Arrival, free.Rooms[0].Departure = d("2026-10-10"), d("2026-10-11")
	r2, err := f.Res.Create(self, f.propID, "", free)
	must(t, err)
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.created' AND entity_id = $1 AND new_data ? 'restriction_override'`, r2.ID); n != 0 {
		t.Fatalf("no restriction was broken: %d", n)
	}
}

func TestABookingFromTheWebOrAnOTACannotOverrideARestriction(t *testing.T) {
	f := setup(t)
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	for _, source := range []string{"WEBSITE", "OTA"} {
		in := f.input(true, f.line(f.dlx, "2026-10-04", "2026-10-07"))
		in.Source = source
		in.RestrictionOverride = &reservations.RestrictionOverride{Reason: "please"} // the admin could approve it, but nobody is there for a booking like this
		_, err := f.Res.Create(f.admin, f.propID, "", in)
		ctx := restricted(t, err, "STOP_SELL")
		if ctx["overridable"] != false {
			t.Fatalf("%s: %v", source, ctx)
		}
	}
	// the same request from the front desk goes through
	in := f.input(true, f.line(f.dlx, "2026-10-04", "2026-10-07"))
	in.RestrictionOverride = &reservations.RestrictionOverride{Reason: "please"}
	if _, err := f.Res.Create(f.admin, f.propID, "", in); err != nil {
		t.Fatalf("phone: %v", err)
	}
}

// One override covers every room of the request, each violation says which room it belongs to, and the other operations take the override too.
func TestOneOverrideCoversTheRoomsOfARequestAndTheOtherSales(t *testing.T) {
	f := setup(t)
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	in := f.input(false, f.line(f.std, "2026-10-04", "2026-10-06"), f.line(f.dlx, "2026-10-04", "2026-10-07"))
	_, err := f.Res.Create(f.admin, f.propID, "", in)
	ctx := restricted(t, err, "STOP_SELL")
	raw, _ := json.Marshal(ctx["violations"])
	var vs []struct {
		Line *int `json:"line_index"`
	}
	must(t, json.Unmarshal(raw, &vs))
	if len(vs) != 1 || vs[0].Line == nil || *vs[0].Line != 1 {
		t.Fatalf("the second room is the one that breaks it: %s", raw)
	}
	over := &reservations.RestrictionOverride{Reason: "group block"}
	in.RestrictionOverride = over
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)

	// confirm, add a room and amend dates take the override through their context or their body
	confirmed, err := f.Res.Confirm(reservations.WithRestrictionOverride(f.admin, over), f.propID, res.ID, res.Version)
	must(t, err)
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.confirmed' AND new_data->'restriction_override'->>'reason' = 'group block'`); n != 1 {
		t.Fatalf("the confirmation records the override: %d", n)
	}
	add := f.line(f.dlx, "2026-10-05", "2026-10-06")
	_, err = f.Res.AddLine(f.admin, f.propID, res.ID, confirmed.Version, add)
	restricted(t, err, "STOP_SELL")
	add.RestrictionOverride = over
	if _, err := f.Res.AddLine(f.admin, f.propID, res.ID, confirmed.Version, add); err != nil {
		t.Fatalf("add with the override: %v", err)
	}
}

// The credentials of the approver are not part of what an idempotent replay compares.
func TestAnOverrideReplaysWithTheSameKey(t *testing.T) {
	f := setup(t)
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	_, managerEmail := f.Account(t, f.tenantID, f.propID, auth.PermReservationRestrictionApprove)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationOverrideRestriction)
	mk := func(password string) reservations.CreateInput {
		in := f.input(false, f.line(f.dlx, "2026-10-04", "2026-10-07"))
		in.GuestID = nil
		in.RestrictionOverride = &reservations.RestrictionOverride{Reason: "VIP", Approval: &iam.ApprovalInput{Email: managerEmail, Password: password}}
		return in
	}
	a, err := f.Res.Create(clerk, f.propID, "key-1", mk(roomstest.Password))
	must(t, err)
	b, err := f.Res.Create(clerk, f.propID, "key-1", mk(roomstest.Password))
	must(t, err)
	if a.ID != b.ID || f.count(t, `SELECT count(*) FROM reservations`) != 1 {
		t.Fatalf("a replay returns the reservation: %d %d", a.ID, b.ID)
	}
}
