// Package expected is the Expected Charge Engine (docs/architecture/03-financial-engines.md step 8): a loader that
// reads a snapshot of stays, segments, nightly rates and postings, and a pure evaluator that answers which
// charges should exist for each stay and night, and whether they do. It writes nothing.
package expected

import (
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// Statuses of an expected charge.
const (
	StatusReady         = "READY"
	StatusAlreadyPosted = "ALREADY_POSTED"
	StatusNotApplicable = "NOT_APPLICABLE"
	StatusError         = "ERROR"
)

// Reason codes.
const (
	ReasonStayNotActive      = "STAY_NOT_ACTIVE"
	ReasonStayClosed         = "STAY_CLOSED"
	ReasonNoRoomForNight     = "NO_ROOM_FOR_NIGHT"
	ReasonMissingNightlyRate = "MISSING_NIGHTLY_RATE"
	ReasonInvalidChargeCode  = "INVALID_CHARGE_CODE"
	ReasonNoOpenFolio        = "NO_OPEN_FOLIO"
)

// SourceRoomNight is the only charge source of the MVP.
const SourceRoomNight = "ROOM_NIGHT"

// Stay is a stay as the evaluator sees it.
type Stay struct {
	ID                int64
	Number            string
	Status            string // OPEN, CHECKED_OUT, CANCELLED
	LineStatus        string // the reservation room's status
	ReservationRoomID int64
	Arrival           civil.Date
	Departure         civil.Date
	GuestName         string
}

// Segment is a period in one room: it covers night N when Start <= N < End (End nil = open).
type Segment struct {
	ID         int64
	StayID     int64
	RoomID     int64
	RoomNumber string
	Start      civil.Date
	End        *civil.Date
}

func (s Segment) covers(n civil.Date) bool {
	return !n.Before(s.Start) && (s.End == nil || n.Before(*s.End))
}

// Night is a row of the nightly price snapshot of a reservation room.
type Night struct {
	ReservationRoomID int64
	Date              civil.Date
	RatePlanID        int64
	ChargeCodeID      int64
	ChargeCode        string
	ChargeCodeActive  bool
	ChargeCodeIsRoom  bool
	PriceMode         string
	Amount            decimal.Decimal
}

// Posting is a POSTED row of the posting register.
type Posting struct {
	StayID      int64
	ServiceDate civil.Date
	FolioItemID int64
	StayRoomID  int64
}

// Snapshot is everything the evaluator reads.
type Snapshot struct {
	Stays    []Stay
	Segments []Segment
	Nights   []Night
	Postings []Posting
	Folios   map[int64]int64 // stay id -> its OPEN guest folio
}

// Scope limits the evaluation. StayIDs empty means every stay in the snapshot. UpToDate is the last night
// considered, normally the business date.
type Scope struct {
	StayIDs  []int64
	UpToDate civil.Date
}

// Charge is one expected charge (a night of a stay) with what is known about it.
type Charge struct {
	StayID            int64
	StayNumber        string
	GuestName         string
	StayRoomID        int64
	RoomNumber        string
	ReservationRoomID int64
	ServiceDate       civil.Date
	Source            string
	ChargeCodeID      int64
	ChargeCode        string
	RatePlanID        int64
	PriceMode         string
	UnitPrice         decimal.Decimal
	Status            string
	Reason            string
	PostedItemID      *int64
	FolioID           *int64
}

// Invalid is a posted room night that should not exist: it lies outside the stay's nights, or the stay was
// cancelled.
type Invalid struct {
	StayID      int64      `json:"stay_id"`
	StayNumber  string     `json:"stay_number"`
	ServiceDate civil.Date `json:"service_date"`
	FolioItemID int64      `json:"folio_item_id"`
	Reason      string     `json:"reason"` // OUTSIDE_STAY or STAY_CANCELLED
}

// Evaluate returns the ROOM_NIGHT expected charges of the stays in scope, ordered by stay id and night. The
// rules of §8.3 are applied in order and the first match wins. Nights before the arrival or from the
// departure on are never candidates, so no night outside the stay is ever charged.
func Evaluate(snap Snapshot, scope Scope) []Charge {
	in := map[int64]bool{}
	for _, id := range scope.StayIDs {
		in[id] = true
	}
	posted := map[int64]map[civil.Date]int64{}
	for _, p := range snap.Postings {
		if posted[p.StayID] == nil {
			posted[p.StayID] = map[civil.Date]int64{}
		}
		posted[p.StayID][p.ServiceDate] = p.FolioItemID
	}
	segs := map[int64][]Segment{}
	for _, s := range snap.Segments {
		segs[s.StayID] = append(segs[s.StayID], s)
	}
	nights := map[int64]map[civil.Date]Night{}
	for _, n := range snap.Nights {
		if nights[n.ReservationRoomID] == nil {
			nights[n.ReservationRoomID] = map[civil.Date]Night{}
		}
		nights[n.ReservationRoomID][n.Date] = n
	}
	stays := append([]Stay(nil), snap.Stays...)
	sort.Slice(stays, func(i, j int) bool { return stays[i].ID < stays[j].ID })

	var out []Charge
	for _, s := range stays {
		if len(in) > 0 && !in[s.ID] {
			continue
		}
		for n := s.Arrival; n.Before(s.Departure) && !n.After(scope.UpToDate); n = n.AddDays(1) {
			c := Charge{StayID: s.ID, StayNumber: s.Number, GuestName: s.GuestName, ReservationRoomID: s.ReservationRoomID, ServiceDate: n, Source: SourceRoomNight}
			seg, hasSeg := segmentFor(segs[s.ID], n)
			if hasSeg {
				c.StayRoomID, c.RoomNumber = seg.ID, seg.RoomNumber
			}
			folio, hasFolio := snap.Folios[s.ID]
			if hasFolio {
				c.FolioID = &folio
			}
			item, isPosted := posted[s.ID][n]
			night, hasNight := nights[s.ReservationRoomID][n]
			if hasNight {
				c.ChargeCodeID, c.ChargeCode, c.RatePlanID, c.PriceMode, c.UnitPrice = night.ChargeCodeID, night.ChargeCode, night.RatePlanID, night.PriceMode, night.Amount
			}
			switch {
			case s.Status == "CANCELLED" || s.LineStatus == "CANCELLED" || s.LineStatus == "NO_SHOW":
				c.Status, c.Reason = StatusNotApplicable, ReasonStayNotActive
			case isPosted:
				c.Status, c.PostedItemID = StatusAlreadyPosted, &item
			case s.Status == "CHECKED_OUT":
				c.Status, c.Reason = StatusNotApplicable, ReasonStayClosed
			case !hasSeg:
				c.Status, c.Reason = StatusError, ReasonNoRoomForNight
			case !hasNight:
				c.Status, c.Reason = StatusError, ReasonMissingNightlyRate
			case !night.ChargeCodeActive || !night.ChargeCodeIsRoom:
				c.Status, c.Reason = StatusError, ReasonInvalidChargeCode
			case !hasFolio:
				c.Status, c.Reason = StatusError, ReasonNoOpenFolio
			default:
				c.Status = StatusReady
			}
			out = append(out, c)
		}
	}
	return out
}

func segmentFor(list []Segment, n civil.Date) (Segment, bool) {
	for _, s := range list {
		if s.covers(n) {
			return s, true
		}
	}
	return Segment{}, false
}

// FindInvalid lists the posted room nights of the stays in scope that lie outside the stay's nights (it was
// shortened after posting) or belong to a cancelled stay. Night audit treats them as blockers.
func FindInvalid(snap Snapshot, scope Scope) []Invalid {
	in := map[int64]bool{}
	for _, id := range scope.StayIDs {
		in[id] = true
	}
	byID := map[int64]Stay{}
	for _, s := range snap.Stays {
		byID[s.ID] = s
	}
	var out []Invalid
	for _, p := range snap.Postings {
		s, ok := byID[p.StayID]
		if !ok || (len(in) > 0 && !in[p.StayID]) {
			continue
		}
		switch {
		case s.Status == "CANCELLED":
			out = append(out, Invalid{StayID: s.ID, StayNumber: s.Number, ServiceDate: p.ServiceDate, FolioItemID: p.FolioItemID, Reason: "STAY_CANCELLED"})
		case p.ServiceDate.Before(s.Arrival) || !p.ServiceDate.Before(s.Departure):
			out = append(out, Invalid{StayID: s.ID, StayNumber: s.Number, ServiceDate: p.ServiceDate, FolioItemID: p.FolioItemID, Reason: "OUTSIDE_STAY"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StayID != out[j].StayID {
			return out[i].StayID < out[j].StayID
		}
		return out[i].ServiceDate.Before(out[j].ServiceDate)
	})
	return out
}
