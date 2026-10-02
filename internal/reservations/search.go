package reservations

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// NightAmount is a priced night.
type NightAmount struct {
	Date   civil.Date      `json:"date"`
	Amount decimal.Decimal `json:"amount"`
}

// PlanOffer is a rate plan's price for the searched nights. A plan with nights that have no rate is shown
// with missing_nights and no estimate: it cannot be booked without overrides.
type PlanOffer struct {
	ID            int64         `json:"id"`
	Code          string        `json:"code"`
	Name          string        `json:"name"`
	PriceMode     string        `json:"price_mode"`
	Nightly       []NightAmount `json:"nightly"`
	MissingNights int           `json:"missing_nights"`
	Estimate      *Estimate     `json:"estimate"`
}

// TypeOffer is a room type's availability and prices for the searched nights.
type TypeOffer struct {
	RoomTypeID    int64                `json:"room_type_id"`
	Code          string               `json:"code"`
	Name          string               `json:"name"`
	FitsOccupancy bool                 `json:"fits_occupancy"`
	AvailableMin  int                  `json:"available_min"`
	PerNight      []availability.Night `json:"per_night"`
	RatePlans     []PlanOffer          `json:"rate_plans"`
}

// SearchResult is the answer to an availability search (advisory: only booking decides).
type SearchResult struct {
	Nights    []civil.Date `json:"nights"`
	RoomTypes []TypeOffer  `json:"room_types"`
}

// SearchAvailability lists availability per active room type and night with the price of every active rate
// plan (reservation.read). It reads committed data and takes no locks.
func (s *Service) SearchAvailability(ctx context.Context, propertyID int64, arrival, departure civil.Date, adults, children int) (SearchResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return SearchResult{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return SearchResult{}, err
	}
	bd := day.BusinessDate
	fields := validateDates("", arrival, departure, bd, true)
	if adults < 1 || children < 0 {
		fields = append(fields, fieldErr("adults", "OUT_OF_RANGE", "at least one adult, children not negative"))
	}
	if len(fields) > 0 {
		return SearchResult{}, apperr.Invalid("the search is invalid", fields...)
	}
	var nights []civil.Date
	for d := arrival; d.Before(departure); d = d.AddDays(1) {
		nights = append(nights, d)
	}
	types, err := s.avail.SellableTypes(ctx, p.TenantID, propertyID)
	if err != nil {
		return SearchResult{}, err
	}
	plans, err := s.avail.SellablePlans(ctx, p.TenantID, propertyID)
	if err != nil {
		return SearchResult{}, err
	}
	typeIDs := make([]int64, len(types))
	for i, t := range types {
		typeIDs[i] = t.ID
	}
	inv, err := s.avail.Inventory(ctx, p.TenantID, propertyID, typeIDs, nights, bd, nil)
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Nights: nights, RoomTypes: make([]TypeOffer, 0, len(types))}
	for _, t := range types {
		offer := TypeOffer{
			RoomTypeID: t.ID, Code: t.Code, Name: t.Name, PerNight: make([]availability.Night, 0, len(nights)), RatePlans: []PlanOffer{},
			FitsOccupancy: availability.FitsOccupancy(adults, children, t.MaxAdult, t.MaxChild, t.MaxOccupancy),
		}
		for i, d := range nights {
			n := inv[t.ID][d]
			n.Date = d
			if n.Available < 0 {
				n.Available = 0
			}
			offer.PerNight = append(offer.PerNight, n)
			if i == 0 || n.Available < offer.AvailableMin {
				offer.AvailableMin = n.Available
			}
		}
		for _, pl := range plans {
			po, err := s.planOffer(ctx, p.TenantID, propertyID, t.ID, pl, arrival, departure)
			if err != nil {
				return SearchResult{}, err
			}
			offer.RatePlans = append(offer.RatePlans, po)
		}
		out.RoomTypes = append(out.RoomTypes, offer)
	}
	return out, nil
}

func (s *Service) planOffer(ctx context.Context, tenantID, propertyID, typeID int64, pl availability.SellablePlan, arrival, departure civil.Date) (PlanOffer, error) {
	prices, missing, err := s.rates.PriceNights(ctx, tenantID, propertyID, pl.ID, typeID, arrival, departure)
	if err != nil {
		return PlanOffer{}, err
	}
	po := PlanOffer{ID: pl.ID, Code: pl.Code, Name: pl.Name, PriceMode: prices.PriceMode, Nightly: make([]NightAmount, len(prices.Nights)), MissingNights: len(missing)}
	charges := make([]billingconfig.NightCharge, len(prices.Nights))
	for i, n := range prices.Nights {
		po.Nightly[i] = NightAmount{Date: n.Date, Amount: n.Amount}
		charges[i] = billingconfig.NightCharge{ChargeCodeID: prices.RoomChargeCodeID, PriceMode: chargecalc.PriceMode(prices.PriceMode), Amount: n.Amount}
	}
	if len(missing) == 0 {
		est, err := s.billing.Estimate(ctx, tenantID, propertyID, charges)
		if err != nil {
			return PlanOffer{}, err
		}
		po.Estimate = &Estimate{Net: est.Net, Service: est.Service, Tax: est.Tax, Total: est.Total}
	}
	return po, nil
}

// FreeRooms lists the free rooms of a type for the dates, with their housekeeping status (reservation.read).
func (s *Service) FreeRooms(ctx context.Context, propertyID, roomTypeID int64, arrival, departure civil.Date) ([]availability.FreeRoom, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if fields := validateDates("", arrival, departure, day.BusinessDate, true); len(fields) > 0 {
		return nil, apperr.Invalid("the search is invalid", fields...)
	}
	if _, err := s.bookingTypes(ctx, p.TenantID, propertyID, []int64{roomTypeID}); err != nil {
		return nil, err
	}
	rooms, err := s.avail.FreeRooms(ctx, p.TenantID, propertyID, roomTypeID, day.BusinessDate, arrival, departure)
	if rooms == nil {
		rooms = []availability.FreeRoom{}
	}
	return rooms, err
}
