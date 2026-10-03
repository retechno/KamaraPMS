package reservations_test

import (
	"context"
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reservations"
)

func ptrStr(s string) *string { return &s }

// compPlan adds a complimentary (or house use) rate plan on the same room charge code as BAR.
func (f *fx) compPlan(t *testing.T, code, kind string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, occupancy_kind)
		SELECT tenant_id, property_id, $2, $2, room_charge_code_id, $3 FROM rate_plans WHERE id = $1 RETURNING id`, f.plan, code, kind).Scan(&id))
	return id
}

// A complimentary or house use room is priced at zero without a grid, needs a reason and the permission, and shows
// its kind and reason; a paid line keeps no reason.
func TestComplimentaryAndHouseUseLines(t *testing.T) {
	f := setup(t)
	comp, house := f.compPlan(t, "COMP", "COMPLIMENTARY"), f.compPlan(t, "HOUSE", "HOUSE_USE")

	in := f.line(f.dlx, "2026-10-02", "2026-10-04")
	in.RatePlanID = comp
	in.OccupancyReason = "  Owner guest "
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, in))
	must(t, err)
	line := res.Rooms[0]
	if line.OccupancyKind != "COMPLIMENTARY" || line.OccupancyReason != "Owner guest" || line.RatePlanCode != "COMP" {
		t.Fatalf("line: %+v", line)
	}
	if len(line.NightlyRates) != 2 || line.NightlyRates[0].Amount.String() != "0" || line.NightlyRates[1].Amount.String() != "0" {
		t.Fatalf("zero price without any grid rate: %+v", line.NightlyRates)
	}
	// it still holds a room of the type
	if got := f.available(t, f.dlx, "2026-10-02"); got.Demand != 1 {
		t.Fatalf("a complimentary room holds a room: %+v", got)
	}

	// the reason is required, and so is the right to give rooms away
	noReason := f.line(f.dlx, "2026-10-02", "2026-10-04")
	noReason.RatePlanID = house
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, noReason))
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "rooms[0].occupancy_reason" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	noReason.OccupancyReason = "Staff training"
	clerk := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate)
	_, err = f.Res.Create(clerk, f.propID, "", f.input(true, noReason))
	wantCode(t, err, "PERMISSION_DENIED")
	manager := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationComplimentary)
	if _, err := f.Res.Create(manager, f.propID, "", f.input(true, noReason)); err != nil {
		t.Fatalf("who may give rooms away can: %v", err)
	}

	// a paid line keeps no reason; a price override on a free room is refused
	paid := f.line(f.dlx, "2026-10-05", "2026-10-06")
	paid.OccupancyReason = "ignored"
	got, err := f.Res.Create(f.admin, f.propID, "", f.input(false, paid))
	must(t, err)
	if got.Rooms[0].OccupancyKind != "PAID" || got.Rooms[0].OccupancyReason != "" {
		t.Fatalf("paid line: %+v", got.Rooms[0])
	}
	over := f.line(f.dlx, "2026-10-05", "2026-10-06")
	over.RatePlanID = comp
	over.OccupancyReason = "x"
	over.Overrides = []reservations.NightOverride{{Date: d("2026-10-05"), Amount: "100"}}
	overIn := f.input(true, over)
	overIn.RateOverrideReason = "x"
	_, err = f.Res.Create(f.admin, f.propID, "", overIn)
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Code != "OVERRIDE_NOT_ALLOWED" {
		t.Fatalf("fields: %+v", c.Fields)
	}
}

// Amending: the reason can be changed, switching to a paid plan drops it, switching to a free plan asks for the
// permission and a reason.
func TestAmendOccupancyKind(t *testing.T) {
	f := setup(t)
	comp := f.compPlan(t, "COMP", "COMPLIMENTARY")
	res := f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate)

	_, err := f.Res.AmendLine(clerk, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RatePlanID: &comp, OccupancyReason: ptrStr("gift")})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RatePlanID: &comp})
	wantCode(t, err, "VALIDATION_FAILED")
	res, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RatePlanID: &comp, OccupancyReason: ptrStr("gift")})
	must(t, err)
	if l := res.Rooms[0]; l.OccupancyKind != "COMPLIMENTARY" || l.OccupancyReason != "gift" || l.NightlyRates[0].Amount.String() != "0" {
		t.Fatalf("now free: %+v", l)
	}
	// the reason of a free room can be edited without the permission to book one
	res, err = f.Res.AmendLine(clerk, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, OccupancyReason: ptrStr("press trip")})
	must(t, err)
	if res.Rooms[0].OccupancyReason != "press trip" {
		t.Fatalf("reason: %+v", res.Rooms[0])
	}
	// back to a paid plan: priced again and the reason is gone
	res, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, RatePlanID: &f.plan})
	must(t, err)
	if l := res.Rooms[0]; l.OccupancyKind != "PAID" || l.OccupancyReason != "" || l.NightlyRates[0].Amount.String() != "1000000" {
		t.Fatalf("paid again: %+v", l)
	}
}

// The search offers a complimentary plan only to someone who may book it.
func TestSearchHidesFreePlans(t *testing.T) {
	f := setup(t)
	f.compPlan(t, "COMP", "COMPLIMENTARY")
	plans := func(ctx context.Context) []string {
		out, err := f.Res.SearchAvailability(ctx, f.propID, d("2026-10-02"), d("2026-10-03"), 2, 0)
		must(t, err)
		var codes []string
		for _, p := range out.RoomTypes[0].RatePlans {
			codes = append(codes, p.Code+":"+p.OccupancyKind)
		}
		return codes
	}
	if got := plans(f.admin); len(got) != 2 {
		t.Fatalf("admin sees both: %v", got)
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	if got := plans(clerk); len(got) != 1 || got[0] != "BAR:PAID" {
		t.Fatalf("a clerk sees the paid plan only: %v", got)
	}
}
