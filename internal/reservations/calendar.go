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
}

// CalendarType is the nights of one active room type.
type CalendarType struct {
	RoomTypeID int64           `json:"room_type_id"`
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
// at most 62 days. It reads committed data and takes no locks; like a search it is advisory, only booking decides.
func (s *Service) AvailabilityCalendar(ctx context.Context, propertyID int64, from, to civil.Date) (Calendar, error) {
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
	totals, err := s.avail.ActiveRoomCounts(ctx, p.TenantID, propertyID)
	if err != nil {
		return Calendar{}, err
	}
	out := Calendar{From: from, To: to, RoomTypes: make([]CalendarType, 0, len(types)), Totals: make([]CalendarTotal, len(nights))}
	for i, d := range nights {
		out.Totals[i].Date = d
	}
	for _, t := range types {
		ct := CalendarType{RoomTypeID: t.ID, Code: t.Code, Name: t.Name, RoomsTotal: totals[t.ID], Nights: make([]CalendarNight, len(nights))}
		for i, d := range nights {
			n := inv[t.ID][d]
			occ := availability.Occupancy{Sellable: n.Sellable, Booked: n.Demand}
			ct.Nights[i] = CalendarNight{
				Date: d, Sellable: n.Sellable, Blocked: ct.RoomsTotal - n.Sellable, Held: n.Demand, Available: n.Sellable - n.Demand, OccupancyPercent: occ.Percent(),
			}
			tot := &out.Totals[i]
			tot.Sellable += n.Sellable
			tot.Blocked += ct.RoomsTotal - n.Sellable
			tot.Held += n.Demand
			tot.Available += n.Sellable - n.Demand
		}
		out.RoomTypes = append(out.RoomTypes, ct)
	}
	for i := range out.Totals {
		t := &out.Totals[i]
		t.OccupancyPercent = availability.Occupancy{Sellable: t.Sellable, Booked: t.Held}.Percent()
	}
	return out, nil
}
