package expected

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dp(s string) *civil.Date { x := d(s); return &x }

// base: stay 1 (30 Sep - 2 Oct) in room 101, priced, with an open folio.
func base() Snapshot {
	return Snapshot{
		Stays:    []Stay{{ID: 1, Number: "STY1", Status: "OPEN", LineStatus: "CHECKED_IN", ReservationRoomID: 10, Arrival: d("2026-09-30"), Departure: d("2026-10-02"), GuestName: "Siti"}},
		Segments: []Segment{{ID: 100, StayID: 1, RoomID: 1, RoomNumber: "101", Start: d("2026-09-30")}},
		Nights: []Night{
			{ReservationRoomID: 10, Date: d("2026-09-30"), ChargeCodeID: 5, ChargeCode: "ROOM", ChargeCodeActive: true, ChargeCodeIsRoom: true, PriceMode: "EXCLUSIVE", Amount: decimal.NewFromInt(1000000)},
			{ReservationRoomID: 10, Date: d("2026-10-01"), ChargeCodeID: 5, ChargeCode: "ROOM", ChargeCodeActive: true, ChargeCodeIsRoom: true, PriceMode: "EXCLUSIVE", Amount: decimal.NewFromInt(1100000)},
		},
		Folios: map[int64]int64{1: 77},
	}
}

func statuses(cs []Charge) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Status + ":" + c.Reason
	}
	return out
}

func eq(t *testing.T, got []Charge, want ...string) {
	t.Helper()
	g := statuses(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

func TestReadyNightsUpToTheScopeDate(t *testing.T) {
	got := Evaluate(base(), Scope{UpToDate: d("2026-09-30")})
	eq(t, got, "READY:")
	c := got[0]
	if c.StayRoomID != 100 || c.RoomNumber == "" || c.ChargeCodeID != 5 || c.UnitPrice.String() != "1000000" || c.PriceMode != "EXCLUSIVE" || c.FolioID == nil || *c.FolioID != 77 || c.Source != SourceRoomNight {
		t.Fatalf("charge: %+v", c)
	}
	// the night of the departure date and later ones are never candidates, nor nights after the scope date
	eq(t, Evaluate(base(), Scope{UpToDate: d("2026-12-31")}), "READY:", "READY:")
	eq(t, Evaluate(base(), Scope{UpToDate: d("2026-09-29")}))
}

func TestRule1StayNotActive(t *testing.T) {
	for name, edit := range map[string]func(*Snapshot){
		"cancelled stay": func(s *Snapshot) { s.Stays[0].Status = "CANCELLED" },
		"cancelled line": func(s *Snapshot) { s.Stays[0].LineStatus = "CANCELLED" },
		"no-show line":   func(s *Snapshot) { s.Stays[0].LineStatus = "NO_SHOW" },
	} {
		s := base()
		edit(&s)
		eq(t, Evaluate(s, Scope{UpToDate: d("2026-10-01")}), "NOT_APPLICABLE:STAY_NOT_ACTIVE", "NOT_APPLICABLE:STAY_NOT_ACTIVE")
		_ = name
	}
	// rule 1 beats rule 2: a posted night of a cancelled stay is not reported as posted
	s := base()
	s.Stays[0].Status = "CANCELLED"
	s.Postings = []Posting{{StayID: 1, ServiceDate: d("2026-09-30"), FolioItemID: 9}}
	eq(t, Evaluate(s, Scope{UpToDate: d("2026-09-30")}), "NOT_APPLICABLE:STAY_NOT_ACTIVE")
}

func TestRule2AlreadyPostedWinsOverClosedAndErrors(t *testing.T) {
	s := base()
	s.Postings = []Posting{{StayID: 1, ServiceDate: d("2026-09-30"), FolioItemID: 9, StayRoomID: 100}}
	s.Folios = map[int64]int64{}
	s.Nights = nil
	got := Evaluate(s, Scope{UpToDate: d("2026-10-01")})
	eq(t, got, "ALREADY_POSTED:", "ERROR:MISSING_NIGHTLY_RATE")
	if got[0].PostedItemID == nil || *got[0].PostedItemID != 9 {
		t.Fatalf("item: %+v", got[0])
	}
	s.Stays[0].Status = "CHECKED_OUT"
	eq(t, Evaluate(s, Scope{UpToDate: d("2026-10-01")}), "ALREADY_POSTED:", "NOT_APPLICABLE:STAY_CLOSED")
}

func TestRule3StayClosed(t *testing.T) {
	s := base()
	s.Stays[0].Status = "CHECKED_OUT"
	eq(t, Evaluate(s, Scope{UpToDate: d("2026-10-01")}), "NOT_APPLICABLE:STAY_CLOSED", "NOT_APPLICABLE:STAY_CLOSED")
}

func TestRules4To7Errors(t *testing.T) {
	up := Scope{UpToDate: d("2026-09-30")}
	s := base()
	s.Segments[0].Start = d("2026-10-01") // nothing covers the night of 30 Sep
	eq(t, Evaluate(s, up), "ERROR:NO_ROOM_FOR_NIGHT")

	s = base()
	s.Nights = s.Nights[1:]
	eq(t, Evaluate(s, up), "ERROR:MISSING_NIGHTLY_RATE")

	s = base()
	s.Nights[0].ChargeCodeActive = false
	eq(t, Evaluate(s, up), "ERROR:INVALID_CHARGE_CODE")
	s = base()
	s.Nights[0].ChargeCodeIsRoom = false
	eq(t, Evaluate(s, up), "ERROR:INVALID_CHARGE_CODE")

	s = base()
	s.Folios = map[int64]int64{}
	got := Evaluate(s, up)
	eq(t, got, "ERROR:NO_OPEN_FOLIO")
	if got[0].FolioID != nil {
		t.Fatal("no folio to name")
	}
	// the order: no segment beats a missing rate beats a bad code beats a missing folio
	s = base()
	s.Segments, s.Nights, s.Folios = nil, nil, map[int64]int64{}
	eq(t, Evaluate(s, up), "ERROR:NO_ROOM_FOR_NIGHT")
	s.Segments = base().Segments
	eq(t, Evaluate(s, up), "ERROR:MISSING_NIGHTLY_RATE")
}

func TestTheRateComesFromTheNightlySnapshot(t *testing.T) {
	s := base()
	s.Nights[1].Amount = decimal.NewFromInt(900000)
	s.Nights[1].PriceMode = "INCLUSIVE"
	s.Nights[1].ChargeCodeID = 6
	got := Evaluate(s, Scope{UpToDate: d("2026-10-01")})
	if got[1].UnitPrice.String() != "900000" || got[1].PriceMode != "INCLUSIVE" || got[1].ChargeCodeID != 6 {
		t.Fatalf("snapshot values: %+v", got[1])
	}
}

func TestRoomMoveSegments(t *testing.T) {
	// moved on the business date (1 Oct): the night of 30 Sep stays with the old room, 1 Oct is charged to the new one
	s := base()
	s.Segments = []Segment{
		{ID: 100, StayID: 1, RoomID: 1, RoomNumber: "101", Start: d("2026-09-30"), End: dp("2026-10-01")},
		{ID: 101, StayID: 1, RoomID: 2, RoomNumber: "102", Start: d("2026-10-01")},
	}
	got := Evaluate(s, Scope{UpToDate: d("2026-10-01")})
	if got[0].StayRoomID != 100 || got[0].RoomNumber != "101" || got[1].StayRoomID != 101 || got[1].RoomNumber != "102" {
		t.Fatalf("move on the business date: %+v", got)
	}
	// moved on the arrival day: the first segment is empty, so the new room is charged for the night
	s.Segments = []Segment{
		{ID: 100, StayID: 1, RoomID: 1, RoomNumber: "101", Start: d("2026-09-30"), End: dp("2026-09-30")},
		{ID: 101, StayID: 1, RoomID: 2, RoomNumber: "102", Start: d("2026-09-30")},
	}
	got = Evaluate(s, Scope{UpToDate: d("2026-10-01")})
	if got[0].StayRoomID != 101 || got[1].StayRoomID != 101 {
		t.Fatalf("arrival-day move: %+v", got)
	}
}

func TestScopeSelectsStays(t *testing.T) {
	s := base()
	s.Stays = append(s.Stays, Stay{ID: 2, Number: "STY2", Status: "OPEN", LineStatus: "CHECKED_IN", ReservationRoomID: 11, Arrival: d("2026-09-30"), Departure: d("2026-10-01")})
	got := Evaluate(s, Scope{UpToDate: d("2026-09-30")})
	if len(got) != 2 || got[0].StayID != 1 || got[1].StayID != 2 {
		t.Fatalf("all stays, ordered: %+v", got)
	}
	got = Evaluate(s, Scope{StayIDs: []int64{2}, UpToDate: d("2026-09-30")})
	if len(got) != 1 || got[0].StayID != 2 {
		t.Fatalf("one stay: %+v", got)
	}
}

func TestFindInvalid(t *testing.T) {
	s := base()
	s.Stays[0].Departure = d("2026-10-01") // shortened after the night of 1 Oct was posted
	s.Postings = []Posting{
		{StayID: 1, ServiceDate: d("2026-09-30"), FolioItemID: 1},
		{StayID: 1, ServiceDate: d("2026-10-01"), FolioItemID: 2},
		{StayID: 1, ServiceDate: d("2026-09-29"), FolioItemID: 3},
	}
	got := FindInvalid(s, Scope{})
	if len(got) != 2 || got[0].ServiceDate != d("2026-09-29") || got[1].ServiceDate != d("2026-10-01") || got[0].Reason != "OUTSIDE_STAY" {
		t.Fatalf("outside: %+v", got)
	}
	s.Stays[0].Status = "CANCELLED"
	got = FindInvalid(s, Scope{})
	if len(got) != 3 || got[0].Reason != "STAY_CANCELLED" {
		t.Fatalf("cancelled: %+v", got)
	}
	if got := FindInvalid(s, Scope{StayIDs: []int64{9}}); len(got) != 0 {
		t.Fatalf("scope: %+v", got)
	}
}
