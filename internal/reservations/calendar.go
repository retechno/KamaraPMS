package reservations

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// CalendarNight is one room type on one night: the rooms it has to sell, the rooms held, and what is left.
type CalendarNight struct {
	Date civil.Date `json:"date"`
	// Sellable is the active rooms of the type without an active OOO/OOS block that night; Blocked is the active rooms
	// that are out of sale (blocked); Held is the rooms held by CONFIRMED lines and open stays; Available is
	// Sellable - Held and is negative when the type is oversold.
	Sellable         int             `json:"sellable"`
	Blocked          int             `json:"blocked"`
	Held             int             `json:"held"`
	Available        int             `json:"available"`
	OccupancyPercent decimal.Decimal `json:"occupancy_percent"`
	// The parts of Held (Held = InHouse + Reservations) and the rooms arriving that night; see availability.Parts.
	InHouse      int `json:"in_house"`
	Arrivals     int `json:"arrivals"`
	Reservations int `json:"reservations"`
}

// CalendarType is the nights of one active room type.
type CalendarType struct {
	RoomTypeID int64           `json:"room_type_id"`
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	RoomsTotal int             `json:"rooms_total"`
	Nights     []CalendarNight `json:"nights"`
	// Beds is the same per bed type of the rooms of the type; only present when asked for.
	Beds []CalendarBed `json:"beds,omitempty"`
}

// CalendarBed is one bed type of a room type, counted over the rooms with that bed.
type CalendarBed struct {
	BedTypeID  int64           `json:"bed_type_id"`
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	RoomsTotal int             `json:"rooms_total"`
	Nights     []CalendarNight `json:"nights"`
}

// CalendarTotal is the whole property on one night.
type CalendarTotal struct {
	Date             civil.Date      `json:"date"`
	Sellable         int             `json:"sellable"`
	Blocked          int             `json:"blocked"`
	Held             int             `json:"held"`
	Available        int             `json:"available"`
	OccupancyPercent decimal.Decimal `json:"occupancy_percent"`
	InHouse          int             `json:"in_house"`
	Arrivals         int             `json:"arrivals"`
	Reservations     int             `json:"reservations"`
}

// Calendar is the availability of every active room type over a window of nights [From, To).
type Calendar struct {
	From      civil.Date      `json:"from"`
	To        civil.Date      `json:"to"`
	RoomTypes []CalendarType  `json:"room_types"`
	Totals    []CalendarTotal `json:"totals"`
}

// AvailabilityCalendar lists, per active room type and night of [from, to), the rooms to sell, blocked, held and still
// available, with the occupancy of each type and of the whole property (reservation.read). The window is after from and
// at most 62 days. With byBed each type also lists its bed types, counted over the rooms with that bed and only the
// bookings already assigned to a room (see availability.BedInventory); the totals stay per room type. It reads committed
// data and takes no locks; like a search it is advisory, only booking decides.
func (s *Service) AvailabilityCalendar(ctx context.Context, propertyID int64, from, to civil.Date, byBed bool) (Calendar, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return Calendar{}, err
	}
	if !to.After(from) || from.DaysUntil(to) > MaxTapeDays {
		return Calendar{}, apperr.Invalid("the window is invalid", fieldErr("to", "OUT_OF_RANGE", "after from, at most 62 days"))
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Calendar{}, err
	}
	var nights []civil.Date
	for d := from; d.Before(to); d = d.AddDays(1) {
		nights = append(nights, d)
	}
	types, err := s.avail.SellableTypes(ctx, p.TenantID, propertyID)
	if err != nil {
		return Calendar{}, err
	}
	typeIDs := make([]int64, len(types))
	for i, t := range types {
		typeIDs[i] = t.ID
	}
	inv, err := s.avail.Inventory(ctx, p.TenantID, propertyID, typeIDs, nights, day.BusinessDate, nil)
	if err != nil {
		return Calendar{}, err
	}
	parts, err := s.avail.Breakdown(ctx, p.TenantID, propertyID, typeIDs, nights, day.BusinessDate)
	if err != nil {
		return Calendar{}, err
	}
	totals, err := s.avail.ActiveRoomCounts(ctx, p.TenantID, propertyID)
	if err != nil {
		return Calendar{}, err
	}
	var beds []availability.RoomBed
	var bedInv map[availability.BedKey]map[civil.Date]availability.BedNight
	if byBed {
		if beds, err = s.avail.RoomBeds(ctx, p.TenantID, propertyID); err != nil {
			return Calendar{}, err
		}
		if bedInv, err = s.avail.BedInventory(ctx, p.TenantID, propertyID, nights, day.BusinessDate); err != nil {
			return Calendar{}, err
		}
	}
	out := Calendar{From: from, To: to, RoomTypes: make([]CalendarType, 0, len(types)), Totals: make([]CalendarTotal, len(nights))}
	for i, d := range nights {
		out.Totals[i].Date = d
	}
	night := func(d civil.Date, rooms, sellable, held int, pt availability.Parts) CalendarNight {
		occ := availability.Occupancy{Sellable: sellable, Booked: held}
		return CalendarNight{Date: d, Sellable: sellable, Blocked: rooms - sellable, Held: held, Available: sellable - held, OccupancyPercent: occ.Percent(), InHouse: pt.InHouse, Arrivals: pt.Arrivals, Reservations: pt.Reservations}
	}
	for _, t := range types {
		ct := CalendarType{RoomTypeID: t.ID, Code: t.Code, Name: t.Name, RoomsTotal: totals[t.ID], Nights: make([]CalendarNight, len(nights))}
		for i, d := range nights {
			n := inv[t.ID][d]
			pt := parts[t.ID][d]
			ct.Nights[i] = night(d, ct.RoomsTotal, n.Sellable, n.Demand, pt)
			tot := &out.Totals[i]
			tot.Sellable += n.Sellable
			tot.Blocked += ct.RoomsTotal - n.Sellable
			tot.Held += n.Demand
			tot.Available += n.Sellable - n.Demand
			tot.InHouse += pt.InHouse
			tot.Arrivals += pt.Arrivals
			tot.Reservations += pt.Reservations
		}
		for _, b := range beds {
			if b.RoomTypeID != t.ID {
				continue
			}
			cb := CalendarBed{BedTypeID: b.BedTypeID, Code: b.Code, Name: b.Name, RoomsTotal: b.Rooms, Nights: make([]CalendarNight, len(nights))}
			for i, d := range nights {
				n := bedInv[availability.BedKey{RoomTypeID: t.ID, BedTypeID: b.BedTypeID}][d]
				cb.Nights[i] = night(d, b.Rooms, n.Sellable, n.Held, n.Parts)
			}
			ct.Beds = append(ct.Beds, cb)
		}
		out.RoomTypes = append(out.RoomTypes, ct)
	}
	for i := range out.Totals {
		t := &out.Totals[i]
		t.OccupancyPercent = availability.Occupancy{Sellable: t.Sellable, Booked: t.Held}.Percent()
	}
	return out, nil
}
