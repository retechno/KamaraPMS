package availability_test

import (
	"context"
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/rooms/roomstest"
)

// EvaluateStay over the real table: what it loads, whom it listens to, and that the precedence of the pure part is applied to stored rows.

func (f *fixture) plan(t *testing.T, code string) int64 {
	t.Helper()
	var chargeID, id int64
	if err := f.e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, f.propID).Scan(&chargeID); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, $3, $3, $4) RETURNING id`,
		f.tenantID, f.propID, code, chargeID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// grid is the attributes of a row to insert (nil is no opinion).
type grid struct {
	stopSell, cta, ctd *bool
	min, max           *int
}

func (f *fixture) restrict(t *testing.T, propertyID int64, roomType, ratePlan *int64, date string, g grid) {
	t.Helper()
	tenant := f.tenantID
	if propertyID != f.propID {
		if err := f.e.Pool.QueryRow(context.Background(), `SELECT tenant_id FROM properties WHERE id = $1`, propertyID).Scan(&tenant); err != nil {
			t.Fatal(err)
		}
	}
	err := f.e.Exec(t, `INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, stop_sell, closed_to_arrival, closed_to_departure, min_stay, max_stay)
	                    VALUES ($1, $2, $3, $4, $5::date, $6, $7, $8, $9, $10)`, tenant, propertyID, roomType, ratePlan, date, g.stopSell, g.cta, g.ctd, g.min, g.max)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) evaluate(t *testing.T, req availability.StayRequest) availability.Verdict {
	t.Helper()
	v, err := f.av.EvaluateStay(f.ctx, f.tenantID, f.propID, req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *fixture) request(plan int64, arrival, departure string) availability.StayRequest {
	return availability.StayRequest{RoomTypeID: f.typ.ID, RatePlanID: plan, Arrival: d(arrival), Departure: d(departure), BusinessDate: roomstest.BD}
}

func TestEvaluateStayReadsTheGridOfItsRoomTypeAndRatePlan(t *testing.T) {
	f := setup(t)
	bar, corp := f.plan(t, "RACK"), f.plan(t, "CORP")
	std := f.e.RoomType(t, f.ctx, f.propID, "STD")
	typ, st := &f.typ.ID, &std.ID

	f.restrict(t, f.propID, typ, nil, "2026-10-04", grid{stopSell: bp(true)})   // this room type, every plan
	f.restrict(t, f.propID, st, nil, "2026-10-05", grid{stopSell: bp(true)})    // another room type: not heard
	f.restrict(t, f.propID, nil, nil, "2026-10-03", grid{cta: bp(true)})        // the whole property
	f.restrict(t, f.propID, typ, &bar, "2026-10-06", grid{ctd: bp(true)})       // this room type and plan
	f.restrict(t, f.propID, typ, &corp, "2026-10-07", grid{stopSell: bp(true)}) // another plan: not heard by BAR

	v := f.evaluate(t, f.request(bar, "2026-10-03", "2026-10-06"))
	wantKinds(t, v.Violations, "STOP_SELL@2026-10-04", "CLOSED_TO_ARRIVAL@2026-10-03", "CLOSED_TO_DEPARTURE@2026-10-06") // the room type and plan row of the 6th closes the departure
	if v.Allowed() {
		t.Fatal("not allowed")
	}
	// the departure date 6th is closed to departure for this plan only
	wantKinds(t, f.evaluate(t, f.request(bar, "2026-10-05", "2026-10-06")).Violations, "CLOSED_TO_DEPARTURE@2026-10-06")
	wantKinds(t, f.evaluate(t, f.request(corp, "2026-10-05", "2026-10-06")).Violations)
	// the CORP row of the 7th is heard by CORP and not by BAR
	wantKinds(t, f.evaluate(t, f.request(corp, "2026-10-07", "2026-10-08")).Violations, "STOP_SELL@2026-10-07")
	wantKinds(t, f.evaluate(t, f.request(bar, "2026-10-07", "2026-10-08")).Violations)
	// a request with nothing restricted is allowed, and says so with an empty list (not null)
	free := f.evaluate(t, f.request(bar, "2026-11-01", "2026-11-03"))
	if !free.Allowed() || free.Violations == nil {
		t.Fatalf("free: %+v", free)
	}
}

func TestEvaluateStayAppliesThePrecedenceToStoredRows(t *testing.T) {
	f := setup(t)
	bar, corp := f.plan(t, "RACK"), f.plan(t, "CORP")
	typ := &f.typ.ID
	f.restrict(t, f.propID, typ, nil, "2026-10-04", grid{stopSell: bp(true), min: ip(3)})    // the room type is closed and wants three nights
	f.restrict(t, f.propID, typ, &corp, "2026-10-04", grid{stopSell: bp(false), min: ip(1)}) // CORP is open and has no minimum
	f.restrict(t, f.propID, nil, &bar, "2026-10-04", grid{min: ip(2)})                       // the plan wants two: the room type's three wins

	got := f.evaluate(t, f.request(bar, "2026-10-04", "2026-10-06")).Violations // two nights
	wantKinds(t, got, "STOP_SELL@2026-10-04", "MIN_STAY@2026-10-04")
	if got[1].Value == nil || *got[1].Value != 3 || got[1].Scope != "ROOM_TYPE" {
		t.Fatalf("the room type beats the plan: %+v", got[1])
	}
	wantKinds(t, f.evaluate(t, f.request(corp, "2026-10-04", "2026-10-06")).Violations)
}

func TestEvaluateStayIgnoresTheGridOfAnotherProperty(t *testing.T) {
	f := setup(t)
	bar := f.plan(t, "RACK")
	other := f.e.Property(t, f.tenantID, "JKT")
	// a row of the other property, even a property-wide one, is not heard here
	f.restrict(t, other.ID, nil, nil, "2026-10-04", grid{stopSell: bp(true), cta: bp(true)})
	wantKinds(t, f.evaluate(t, f.request(bar, "2026-10-04", "2026-10-05")).Violations)
	// and a property-wide row of this one is
	f.restrict(t, f.propID, nil, nil, "2026-10-04", grid{stopSell: bp(true)})
	wantKinds(t, f.evaluate(t, f.request(bar, "2026-10-04", "2026-10-05")).Violations, "STOP_SELL@2026-10-04")
}

func TestEvaluateStayRefusesABadRequestAndLeavesTheInventoryAlone(t *testing.T) {
	f := setup(t)
	bar := f.plan(t, "RACK")
	_, err := f.av.EvaluateStay(f.ctx, f.tenantID, f.propID, f.request(bar, "2026-10-05", "2026-10-05"))
	roomstest.Want(t, err, "VALIDATION_FAILED")
	// a night with no room left but no restriction is allowed: the inventory is another question
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-04", "2026-10-06", "CONFIRMED")
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-04", "2026-10-06", "CONFIRMED")
	if v := f.evaluate(t, f.request(bar, "2026-10-04", "2026-10-06")); !v.Allowed() {
		t.Fatalf("restrictions do not read the inventory: %+v", v)
	}
	if n := f.night(t, "2026-10-04", nil); n.Available != 0 {
		t.Fatalf("the type is full: %+v", n)
	}
}
