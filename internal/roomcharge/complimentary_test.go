package roomcharge_test

import (
	"context"
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// A complimentary night posts as a room charge of zero: no price, no service, no tax. The register still records
// it, so the night is not missing and night audit has nothing to catch up.
func TestComplimentaryNightPostsZero(t *testing.T) {
	f := setup(t)
	var comp, roomCode int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, f.propID).Scan(&roomCode))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, occupancy_kind) VALUES ($1, $2, 'COMP', 'Complimentary', $3, 'COMPLIMENTARY') RETURNING id`,
		f.tenantID, f.propID, roomCode).Scan(&comp))
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: comp, Arrival: roomstest.BD, Departure: d("2026-10-02"), Adults: 2, OccupancyReason: "Owner guest"},
	}})
	must(t, err)
	_, err = f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &f.r101.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)

	out, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(out.Results) != 1 || out.Results[0].Status != "POSTED" || out.Results[0].Total != "0" {
		t.Fatalf("post: %+v", out)
	}
	var debit, service, tax string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT debit::text, service_charge_total::text, tax_total::text FROM folio_items`).Scan(&debit, &service, &tax))
	if debit != "0.000" || service != "0.000" || tax != "0.000" {
		t.Fatalf("zero item: %s %s %s", debit, service, tax)
	}
	if f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`) != 1 {
		t.Fatal("the register records the night")
	}
}
