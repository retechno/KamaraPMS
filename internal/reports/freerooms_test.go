package reports_test

import (
	"context"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reports"
)

func yes() *bool { b := true; return &b }

// The report lists the complimentary and house use rooms, values their nights at the reference plan, and groups them.
func TestFreeRoomsReport(t *testing.T) {
	f := setup(t)
	r103 := f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)
	r104 := f.Room(t, f.admin, f.propID, f.dlx.ID, "104", housekeeping.Clean)
	comp, house := f.freePlan(t, "COMP", "COMPLIMENTARY"), f.freePlan(t, "HOUSE", "HOUSE_USE")
	f.stay(t, f.r101, "2026-10-02") // a paid stay: not in the report
	f.stayOn(t, comp, f.r102, "2026-10-02", "Owner guest")
	f.stayOn(t, comp, r104, "2026-10-01", "owner guest ") // the same reason in another spelling
	f.stayOn(t, house, r103, "2026-10-02", "Staff")

	// no reference plan yet: the nights are counted, the value is zero
	rep, err := f.Reports.FreeRooms(f.admin, f.propID, d("2026-09-30"), d("2026-10-05"))
	must(t, err)
	if rep.ReferencePlan != "" || len(rep.Lines) != 3 || rep.Totals.Rooms != 3 || rep.Totals.Nights != 5 || rep.Totals.Value != "0" || rep.Totals.MissingNights != 5 {
		t.Fatalf("without a reference: %+v", rep)
	}

	// BAR becomes the reference plan: a complimentary night is worth 1,000,000
	_, err = f.Rates.UpdateRatePlan(f.admin, f.propID, f.plan, rates.RatePlanPatch{IsReference: yes()})
	must(t, err)
	rep, err = f.Reports.FreeRooms(f.admin, f.propID, d("2026-09-30"), d("2026-10-05"))
	must(t, err)
	if rep.ReferencePlan != "BAR" || rep.Totals.Nights != 5 || rep.Totals.Value != "5000000" || rep.Totals.MissingNights != 0 {
		t.Fatalf("totals: %+v", rep.Totals)
	}
	byRoom := map[string]reports.FreeRoomLine{}
	for _, l := range rep.Lines {
		byRoom[l.Room] = l
	}
	if l := byRoom["102"]; l.Nights != 2 || l.Value != "2000000" || l.OccupancyKind != "COMPLIMENTARY" || l.Reason != "Owner guest" || l.RatePlan != "COMP" {
		t.Fatalf("102: %+v", l)
	}
	if l := byRoom["104"]; l.Nights != 1 || l.Value != "1000000" {
		t.Fatalf("104: %+v", l)
	}
	// by reason: the two spellings are one reason, the biggest first
	if len(rep.ByReason) != 2 || rep.ByReason[0].Key != "Owner guest" || rep.ByReason[0].Rooms != 2 || rep.ByReason[0].Nights != 3 || rep.ByReason[0].Value != "3000000" ||
		rep.ByReason[1].Key != "Staff" || rep.ByReason[1].Value != "2000000" {
		t.Fatalf("by reason: %+v", rep.ByReason)
	}
	if len(rep.ByKind) != 2 || rep.ByKind[0].Key != "COMPLIMENTARY" || rep.ByKind[0].Nights != 3 || rep.ByKind[1].Key != "HOUSE_USE" || rep.ByKind[1].Value != "2000000" {
		t.Fatalf("by kind: %+v", rep.ByKind)
	}

	// the range limits the nights
	one, err := f.Reports.FreeRooms(f.admin, f.propID, d("2026-10-01"), d("2026-10-01"))
	must(t, err)
	if one.Totals.Nights != 2 || one.Totals.Value != "2000000" {
		t.Fatalf("1 Oct: %+v", one.Totals)
	}

	// the CSV of each table
	h, rows := reports.FreeRoomsList(rep).CSV()
	if len(h) == 0 || len(rows) != 3 {
		t.Fatalf("csv: %v %v", h, rows)
	}

	// who may not see reports
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Reports.FreeRooms(nobody, f.propID, d("2026-09-30"), d("2026-10-05"))
	if err == nil {
		t.Fatal("report.view is needed")
	}
}

// Only one paid plan is the reference plan: taking the flag over moves it, and a free plan cannot have it.
func TestReferenceRatePlan(t *testing.T) {
	f := setup(t)
	other := f.freePlan(t, "NETT", "PAID")
	comp := f.freePlan(t, "COMP", "COMPLIMENTARY")
	_, err := f.Rates.UpdateRatePlan(f.admin, f.propID, f.plan, rates.RatePlanPatch{IsReference: yes()})
	must(t, err)
	got, err := f.Rates.UpdateRatePlan(f.admin, f.propID, other, rates.RatePlanPatch{IsReference: yes()})
	must(t, err)
	if !got.IsReference {
		t.Fatalf("NETT: %+v", got)
	}
	if f.Count(t, `SELECT count(*) FROM rate_plans WHERE is_reference`) != 1 {
		t.Fatal("one reference plan per property")
	}
	var isRef bool
	must(t, f.Pool.QueryRow(context.Background(), `SELECT is_reference FROM rate_plans WHERE id = $1`, f.plan).Scan(&isRef))
	if isRef {
		t.Fatal("BAR lost the flag")
	}
	_, err = f.Rates.UpdateRatePlan(f.admin, f.propID, comp, rates.RatePlanPatch{IsReference: yes()})
	wantCode(t, err, "VALIDATION_FAILED")
}
