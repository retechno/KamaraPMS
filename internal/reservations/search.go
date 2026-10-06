package reservations

import (
	"context"
	"slices"

	"github.com/shopspring/decimal"

	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rates"
)

// NightAmount is a priced night.
type NightAmount struct {
	Date   civil.Date      `json:"date"`
	Amount decimal.Decimal `json:"amount"`
	// BedAdjustment is what the kept bed adds to the price (already in Amount); 0 on the line of the room type.
	BedAdjustment decimal.Decimal `json:"bed_adjustment"`
}

// PlanOffer is a rate plan's price for the searched nights. A plan with nights that have no rate is shown
// with missing_nights and no estimate: it cannot be booked without overrides.
type PlanOffer struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	PriceMode string `json:"price_mode"`
	// OccupancyKind is PAID, COMPLIMENTARY or HOUSE_USE; the last two are only offered to who may book them.
	OccupancyKind string        `json:"occupancy_kind"`
	Nightly       []NightAmount `json:"nightly"`
	MissingNights int           `json:"missing_nights"`
	Estimate      *Estimate     `json:"estimate"`
	// Bookable is false when the stay breaks a sales restriction (stop sell, closed to arrival or departure, a minimum or maximum stay); Restrictions says which, by the one
	// evaluator of the availability engine. It does not look at the rooms left (AvailableMin of the room type does). Staff who may override can still book it.
	Bookable     bool                     `json:"bookable"`
	Restrictions []availability.Violation `json:"restrictions"`
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
	// Beds are the variants of the type, one per bed type its rooms have, with their own stock and the price of a line that keeps the bed.
	// The line above (the type) is "any bed".
	Beds []BedOffer `json:"beds"`
}

// BedNightOffer is the stock of a variant on a night: the rooms with the bed that can be sold, the rooms of it that sit in a room or are kept,
// and what is left (never above the room type's).
type BedNightOffer struct {
	Date      civil.Date `json:"date"`
	Sellable  int        `json:"sellable"`
	Kept      int        `json:"kept"`
	Available int        `json:"available"`
}

// BedOffer is a bed type of a room type for the searched nights: how many rooms are left, and the price of every plan when the line keeps the bed
// (the grid price plus the supplement in force on each night).
type BedOffer struct {
	BedTypeID    int64           `json:"bed_type_id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	AvailableMin int             `json:"available_min"`
	PerNight     []BedNightOffer `json:"per_night"`
	RatePlans    []PlanOffer     `json:"rate_plans"`
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
	if s.authz.Require(ctx, propertyID, auth.PermReservationComplimentary) != nil {
		plans = slices.DeleteFunc(plans, func(pl availability.SellablePlan) bool { return pl.Kind != rates.KindPaid })
	}
	typeIDs := make([]int64, len(types))
	for i, t := range types {
		typeIDs[i] = t.ID
	}
	inv, err := s.avail.Inventory(ctx, p.TenantID, propertyID, typeIDs, nights, bd, nil)
	if err != nil {
		return SearchResult{}, err
	}
	beds, err := s.avail.RoomBeds(ctx, p.TenantID, propertyID)
	if err != nil {
		return SearchResult{}, err
	}
	keys := make([]availability.BedKey, len(beds))
	for i, b := range beds {
		keys[i] = availability.BedKey{RoomTypeID: b.RoomTypeID, BedTypeID: b.BedTypeID}
	}
	stock, err := s.avail.BedStock(ctx, p.TenantID, propertyID, keys, nights, bd, nil)
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
		verdicts := map[int64]availability.Verdict{} // one question to the evaluator per plan, whatever the bed
		for _, pl := range plans {
			v, err := s.avail.EvaluateStay(ctx, p.TenantID, propertyID, availability.StayRequest{RoomTypeID: t.ID, RatePlanID: pl.ID, Arrival: arrival, Departure: departure, BusinessDate: bd})
			if err != nil {
				return SearchResult{}, err
			}
			verdicts[pl.ID] = v
			po, err := s.planOffer(ctx, p.TenantID, propertyID, t.ID, 0, pl, v, arrival, departure)
			if err != nil {
				return SearchResult{}, err
			}
			offer.RatePlans = append(offer.RatePlans, po)
		}
		offer.Beds = []BedOffer{}
		for _, b := range beds {
			if b.RoomTypeID != t.ID {
				continue
			}
			bo := BedOffer{BedTypeID: b.BedTypeID, Code: b.Code, Name: b.Name, PerNight: make([]BedNightOffer, 0, len(nights)), RatePlans: []PlanOffer{}}
			for i, d := range nights {
				st := stock[availability.BedKey{RoomTypeID: t.ID, BedTypeID: b.BedTypeID}][d]
				avail := max(min(st.Free(), offer.PerNight[i].Available), 0)
				bo.PerNight = append(bo.PerNight, BedNightOffer{Date: d, Sellable: st.Sellable, Kept: st.Fixed + st.Locked, Available: avail})
				if i == 0 || avail < bo.AvailableMin {
					bo.AvailableMin = avail
				}
			}
			for _, pl := range plans {
				po, err := s.planOffer(ctx, p.TenantID, propertyID, t.ID, b.BedTypeID, pl, verdicts[pl.ID], arrival, departure)
				if err != nil {
					return SearchResult{}, err
				}
				bo.RatePlans = append(bo.RatePlans, po)
			}
			offer.Beds = append(offer.Beds, bo)
		}
		out.RoomTypes = append(out.RoomTypes, offer)
	}
	return out, nil
}

// planOffer prices a plan for the searched nights; with a bed type (above 0) the nights carry the supplement of that bed in force on each night
// (a complimentary or house use plan stays at zero).
func (s *Service) planOffer(ctx context.Context, tenantID, propertyID, typeID, bedID int64, pl availability.SellablePlan, verdict availability.Verdict, arrival, departure civil.Date) (PlanOffer, error) {
	prices, missing, err := s.rates.PriceNights(ctx, tenantID, propertyID, pl.ID, typeID, arrival, departure)
	if err != nil {
		return PlanOffer{}, err
	}
	var supplements []rates.BedSupplement
	if bedID > 0 && prices.OccupancyKind == rates.KindPaid {
		if supplements, err = s.rates.BedSupplements(ctx, tenantID, propertyID, pl.ID, typeID, bedID); err != nil {
			return PlanOffer{}, err
		}
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return PlanOffer{}, err
	}
	po := PlanOffer{ID: pl.ID, Code: pl.Code, Name: pl.Name, PriceMode: prices.PriceMode, OccupancyKind: pl.Kind, Nightly: make([]NightAmount, len(prices.Nights)), MissingNights: len(missing), Bookable: verdict.Allowed(), Restrictions: verdict.Violations}
	charges := make([]billingconfig.NightCharge, len(prices.Nights))
	for i, n := range prices.Nights {
		amount, adj := n.Amount, decimal.Zero
		if sup, ok := rates.SupplementOn(supplements, n.Date); ok {
			adj = rates.BedAdjustmentFor(amount, sup, decimals)
			amount = amount.Add(adj)
		}
		po.Nightly[i] = NightAmount{Date: n.Date, Amount: amount, BedAdjustment: adj}
		charges[i] = billingconfig.NightCharge{ChargeCodeID: prices.RoomChargeCodeID, PriceMode: chargecalc.PriceMode(prices.PriceMode), Amount: amount}
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
