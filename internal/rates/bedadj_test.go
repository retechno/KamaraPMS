package rates_test

import (
	"testing"

	"kamarapms/internal/rates"
)

func TestParseBedAdjustment(t *testing.T) {
	cases := []struct {
		kind, amount string
		decimals     int32
		field        string // the field that is refused, "" when it is accepted
	}{
		{"AMOUNT", "50000", 0, ""},
		{"AMOUNT", "-25000", 0, ""},
		{"AMOUNT", "10.5", 0, "amount"},
		{"AMOUNT", "10.505", 3, ""},
		{"AMOUNT", "x", 2, "amount"},
		{"PERCENT", "12.5", 0, ""},
		{"PERCENT", "-100", 0, ""},
		{"PERCENT", "-100.1", 0, "amount"},
		{"PERCENT", "1000", 0, ""},
		{"PERCENT", "1000.1", 0, "amount"},
		{"PERCENT", "1.2345", 0, "amount"},
		{"FREE", "1", 0, "adjust_kind"},
	}
	for _, c := range cases {
		_, ferr := rates.ParseBedAdjustment(c.kind, c.amount, c.decimals)
		got := ""
		if ferr != nil {
			got = ferr.Field
		}
		if got != c.field {
			t.Errorf("%s %s (%d decimals): refused field %q, want %q", c.kind, c.amount, c.decimals, got, c.field)
		}
	}
}

func TestBedAdjustments(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t, "BAR", f.room)
	king := f.FirstBedType(t, f.bali)

	add := func(kind, amount, from string) (rates.BedAdjustment, error) {
		return f.Rates.AddBedAdjustment(f.admin, f.bali, plan.ID, rates.BedAdjustmentInput{RoomTypeID: f.dlx, BedTypeID: king, AdjustKind: kind, Amount: amount, EffectiveFrom: d(from)})
	}
	first, err := add("AMOUNT", "50000", "2026-09-01")
	if err != nil || first.ID == 0 || first.Amount != "50000" {
		t.Fatalf("add: %v %+v", err, first)
	}
	if _, err := add("PERCENT", "10", "2026-11-01"); err != nil {
		t.Fatal(err)
	}

	// The same start date twice is refused; a bed of nowhere, a plan of nowhere and a bad figure too.
	_, err = add("AMOUNT", "1", "2026-09-01")
	wantCode(t, err, "BED_ADJUSTMENT_EXISTS")
	_, err = f.Rates.AddBedAdjustment(f.admin, f.bali, plan.ID, rates.BedAdjustmentInput{RoomTypeID: f.dlx, BedTypeID: 999999, AdjustKind: "AMOUNT", Amount: "1", EffectiveFrom: d("2026-10-05")})
	wantCode(t, err, "BED_TYPE_NOT_FOUND")
	_, err = f.Rates.AddBedAdjustment(f.admin, f.bali, 999999, rates.BedAdjustmentInput{RoomTypeID: f.dlx, BedTypeID: king, AdjustKind: "AMOUNT", Amount: "1", EffectiveFrom: d("2026-10-05")})
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	_, err = f.Rates.AddBedAdjustment(f.admin, f.bali, plan.ID, rates.BedAdjustmentInput{RoomTypeID: 999999, BedTypeID: king, AdjustKind: "AMOUNT", Amount: "1", EffectiveFrom: d("2026-10-05")})
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
	_, err = add("PERCENT", "2000", "2026-10-05")
	wantCode(t, err, "VALIDATION_FAILED")

	// The list shows the newest row first and marks the one in force on the business date.
	list, err := f.Rates.ListBedAdjustments(f.admin, f.bali, plan.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	if list[0].AdjustKind != "PERCENT" || list[0].InForce || list[1].AdjustKind != "AMOUNT" || !list[1].InForce {
		t.Fatalf("order and the row in force: %+v", list)
	}
	if list[1].BedTypeCode == "" || list[1].RoomTypeCode != "DLX" {
		t.Fatalf("codes: %+v", list[1])
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'rate_plan.bed_adjustment_added'`); n != 2 {
		t.Fatalf("audit entries: %d", n)
	}
}
