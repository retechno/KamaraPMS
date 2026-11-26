package frontdesk_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/rooms/roomstest"
)

// previewThenCheckOut pays what the check-out preview says the guest owes, then checks the stay out with the
// early departure it says is needed: the check-out must go through and post exactly the nights the preview listed.
func (f *fx) previewThenCheckOut(t *testing.T, ctx context.Context, st frontdesk.CheckInResult) (frontdesk.CheckOutPreview, frontdesk.CheckOutResult) {
	t.Helper()
	pv, err := f.Front.CheckOutPreview(ctx, f.propID, st.Stay.ID)
	must(t, err)
	if len(pv.Folios) != 1 || pv.Folios[0].FolioID != st.Folio.ID {
		t.Fatalf("one guest folio: %+v", pv.Folios)
	}
	due := decimal.RequireFromString(pv.Folios[0].Due)
	if due.IsPositive() {
		f.pay(t, st.Folio.ID, pv.Folios[0].Due)
	}
	res, err := f.Front.CheckOut(ctx, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: f.detail(t, st.Stay.ID).Stay.Version, ConfirmEarlyDeparture: pv.EarlyDeparture})
	if err != nil {
		t.Fatalf("the check-out refused what its preview asked for (%+v): %v", pv, err)
	}
	ready := readyNights(pv)
	if len(res.PostedRoomCharges) != len(ready) {
		t.Fatalf("preview %d nights, check-out posted %d: %+v", len(ready), len(res.PostedRoomCharges), res.PostedRoomCharges)
	}
	if res.Stay.DepartureDate != pv.DepartureDate {
		t.Fatalf("departure: preview %s, check-out %s", pv.DepartureDate, res.Stay.DepartureDate)
	}
	return pv, res
}

// readyNights are the nights the preview says the check-out will post.
func readyNights(pv frontdesk.CheckOutPreview) []roomcharge.Result {
	var out []roomcharge.Result
	for _, r := range pv.RoomCharges {
		if r.Status == "READY" {
			out = append(out, r)
		}
	}
	return out
}

// KD-06: a guest who arrived before today and leaves early in the daytime is not charged tonight; the preview
// must not ask for it (the wizard used to add tonight from the night audit's dry run of the booked stay).
func TestCheckOutPreviewDaytimeEarlyDepartureOfAnEarlierArrival(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04") // arrives 30 Sep, booked to 4 Oct
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err) // 30 Sep is charged, as the night audit would
	f.nextDay(t)
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour)) // 1 Oct, daytime, local date = business date
	pv, res := f.previewThenCheckOut(t, f.admin, st)
	if !pv.EarlyDeparture || pv.DepartureDate != d("2026-10-01") {
		t.Fatalf("early departure to today: %+v", pv)
	}
	if len(readyNights(pv)) != 0 || pv.Folios[0].RoomCharges != "0" || pv.Folios[0].Due != pv.Folios[0].Balance {
		t.Fatalf("tonight is not charged: %+v", pv)
	}
	if len(res.PostedRoomCharges) != 0 {
		t.Fatalf("nothing posted: %+v", res.PostedRoomCharges)
	}
}

func TestCheckOutPreviewArrivalDayEarlyDeparture(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03") // arrives today, leaves today: the night of arrival is charged
	pv, _ := f.previewThenCheckOut(t, f.admin, st)
	if !pv.EarlyDeparture || pv.DepartureDate != d("2026-10-01") || len(readyNights(pv)) != 1 || readyNights(pv)[0].ServiceDate != roomstest.BD {
		t.Fatalf("the night of arrival: %+v", pv)
	}
	if decimal.RequireFromString(pv.Folios[0].RoomCharges).IsZero() || pv.Folios[0].Due != pv.Folios[0].RoomCharges {
		t.Fatalf("due = tonight: %+v", pv.Folios[0])
	}
}

func TestCheckOutPreviewOnTheBookedDay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02")
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	f.nextDay(t)
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour))
	f.nextDay(t) // 2 Oct: 1 Oct was never charged (no audit ran)
	f.Clock.Set(roomstest.T0.Add(48 * time.Hour))
	pv, _ := f.previewThenCheckOut(t, f.admin, st)
	if pv.EarlyDeparture || pv.DepartureDate != d("2026-10-02") || len(readyNights(pv)) != 1 || readyNights(pv)[0].ServiceDate != d("2026-10-01") {
		t.Fatalf("on time, the missing night only: %+v", pv)
	}
}

func TestCheckOutPreviewAfterMidnightChargesTheNightOfTheBusinessDate(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04")
	f.Clock.Set(roomstest.T0.Add(6 * time.Hour)) // 02:00 local on 1 Oct, the audit of 30 Sep has not run
	pv, _ := f.previewThenCheckOut(t, f.admin, st)
	if !pv.EarlyDeparture || pv.DepartureDate != d("2026-10-01") || len(readyNights(pv)) != 1 || readyNights(pv)[0].ServiceDate != roomstest.BD {
		t.Fatalf("the consumed night: %+v", pv)
	}
}

// The preview needs the right to check out, nothing more: a desk without folio.post_charge or nightaudit.run sees the
// same amount (before, the wizard skipped the dry run for it and asked for the balance only, which the check-out of an
// arrival-day guest then refused).
func TestCheckOutPreviewForACheckOutOnlyRole(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	desk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckout, auth.PermFolioRead, auth.PermPaymentPost)
	pv, _ := f.previewThenCheckOut(t, desk, st)
	if len(readyNights(pv)) != 1 || pv.Folios[0].Due == "0" {
		t.Fatalf("tonight is shown to the desk too: %+v", pv)
	}
	reader := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Front.CheckOutPreview(reader, f.propID, st.Stay.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Front.CheckOutPreview(f.admin, f.propID, 99999)
	wantCode(t, err, "STAY_NOT_FOUND")
	_, err = f.Front.CheckOutPreview(f.admin, f.propID, st.Stay.ID) // checked out already
	wantCode(t, err, "STAY_NOT_OPEN")
}
