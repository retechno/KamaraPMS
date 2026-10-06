package availability

import (
	"context"
	"sort"

	"kamarapms/internal/availability/availabilitydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The stock of a bed variant (docs/architecture/16-bed-variants.md). For one room type and one night, let R be the sellable
// rooms of the type and R_B those with bed B. The demand is fixed (it sits in a room already), locked (it keeps bed B, no room
// yet) or any (a booking with no preference). Whether it all fits is a matching of demand to rooms, and Hall's condition
// reduces to two counts, which are exact:
//
//	for every bed B:    fixed_B + locked_B <= R_B
//	for the type:       fixed + locked + any <= R
//
// So a new locked booking adds one to both lines, and a booking with no preference adds one to the type line only.

// BedStock is the inventory of one bed variant (a room type and a bed type) on one night: the rooms with that bed that can be
// sold, the demand that already sits in such a room, and the demand that keeps the bed without a room yet.
type BedStock struct {
	Sellable int `json:"sellable"`
	Fixed    int `json:"fixed"`
	Locked   int `json:"locked"`
}

// Free is what the rooms with the bed can still take: sellable - fixed - locked.
func (b BedStock) Free() int { return b.Sellable - b.Fixed - b.Locked }

// BedExtra is additional demand on the line of a variant: rooms per (room type, bed type) per night.
type BedExtra map[BedKey]map[civil.Date]int

// Add records count more rooms that need the bed on every night of [from, to).
func (e BedExtra) Add(k BedKey, from, to civil.Date, count int) {
	if e[k] == nil {
		e[k] = map[civil.Date]int{}
	}
	for d := from; d.Before(to); d = d.AddDays(1) {
		e[k][d] += count
	}
}

// Keys lists the variants mentioned, ascending.
func (e BedExtra) Keys() []BedKey {
	out := make([]BedKey, 0, len(e))
	for k := range e {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RoomTypeID != out[j].RoomTypeID {
			return out[i].RoomTypeID < out[j].RoomTypeID
		}
		return out[i].BedTypeID < out[j].BedTypeID
	})
	return out
}

// Dates lists every night mentioned, ascending.
func (e BedExtra) Dates() []civil.Date {
	seen := map[civil.Date]bool{}
	for _, nights := range e {
		for d := range nights {
			seen[d] = true
		}
	}
	out := make([]civil.Date, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// Demand is what is to be tested: the rooms per type (the line of the type) and, for the demand that keeps a bed, also per
// variant (the line of the bed).
type Demand struct {
	Types Extra
	Beds  BedExtra
}

// NewDemand returns an empty demand.
func NewDemand() Demand { return Demand{Types: Extra{}, Beds: BedExtra{}} }

// Add records count rooms of a type on every night of [from, to). A bed type above 0 keeps the bed (a locked booking is in both
// lines); 0 is a booking with no preference (the type line only).
func (d Demand) Add(roomTypeID, bedTypeID int64, from, to civil.Date, count int) {
	d.Types.Add(roomTypeID, from, to, count)
	if bedTypeID > 0 {
		d.Beds.Add(BedKey{RoomTypeID: roomTypeID, BedTypeID: bedTypeID}, from, to, count)
	}
}

// FindBedShortfalls returns the nights (ascending, per variant) on which the demand that keeps a bed does not fit the rooms with
// that bed: fixed + locked + the extra demand must not exceed the sellable rooms of the variant. Pure.
func FindBedShortfalls(stock map[BedKey]map[civil.Date]BedStock, extra BedExtra) []Shortfall {
	var out []Shortfall
	for _, k := range extra.Keys() {
		nights := make([]civil.Date, 0, len(extra[k]))
		for d := range extra[k] {
			nights = append(nights, d)
		}
		sort.Slice(nights, func(i, j int) bool { return nights[i].Before(nights[j]) })
		for _, d := range nights {
			need := extra[k][d]
			if need <= 0 {
				continue
			}
			free := stock[k][d].Free() // a night without rows has no rooms with the bed
			if free < need {
				bed := k.BedTypeID
				out = append(out, Shortfall{RoomTypeID: k.RoomTypeID, BedTypeID: &bed, Date: d, Available: free, Requested: need})
			}
		}
	}
	return out
}

// FindDemandShortfalls tests both lines of the rule, the whole type and every variant, and lists the nights that do not fit:
// the type first, then the variants.
func FindDemandShortfalls(inventory map[int64]map[civil.Date]Night, stock map[BedKey]map[civil.Date]BedStock, demand Demand) []Shortfall {
	return append(FindShortfalls(inventory, demand.Types), FindBedShortfalls(stock, demand.Beds)...)
}

// BedStock returns, per variant and night, the rooms with the bed that can be sold and the demand that sits in them or keeps the
// bed. excludeLine removes one reservation line's own demand. Nights before the business date carry no stay demand.
func (s *Service) BedStock(ctx context.Context, tenantID, propertyID int64, keys []BedKey, dates []civil.Date, bd civil.Date, excludeLine *int64) (map[BedKey]map[civil.Date]BedStock, error) {
	out := map[BedKey]map[civil.Date]BedStock{}
	if len(keys) == 0 || len(dates) == 0 {
		return out, nil
	}
	want := map[BedKey]bool{}
	var typeIDs, bedIDs []int64
	seenType, seenBed := map[int64]bool{}, map[int64]bool{}
	for _, k := range keys {
		want[k] = true
		if !seenType[k.RoomTypeID] {
			seenType[k.RoomTypeID] = true
			typeIDs = append(typeIDs, k.RoomTypeID)
		}
		if !seenBed[k.BedTypeID] {
			seenBed[k.BedTypeID] = true
			bedIDs = append(bedIDs, k.BedTypeID)
		}
	}
	rows, err := s.q(ctx).BedStockNights(ctx, availabilitydb.BedStockNightsParams{
		TenantID: tenantID, PropertyID: propertyID, BusinessDate: bd, NextDate: bd.AddDays(1),
		RoomTypeIds: typeIDs, BedTypeIds: bedIDs, Dates: isoDates(dates), ExcludeLineID: excludeLine,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		k := BedKey{r.RoomTypeID, r.BedTypeID}
		if !want[k] {
			continue
		}
		if out[k] == nil {
			out[k] = map[civil.Date]BedStock{}
		}
		out[k][r.Night] = BedStock{Sellable: int(r.Sellable), Fixed: int(r.Fixed), Locked: int(r.Locked)}
	}
	return out, nil
}

// ShortfallsFor tests a demand against both lines of the rule, the room type and the bed variant, and returns the nights that do
// not fit (the type first).
func (s *Service) ShortfallsFor(ctx context.Context, tenantID, propertyID int64, bd civil.Date, demand Demand, excludeLine *int64) ([]Shortfall, error) {
	inv, err := s.Inventory(ctx, tenantID, propertyID, demand.Types.TypeIDs(), demand.Types.Dates(), bd, excludeLine)
	if err != nil {
		return nil, err
	}
	stock, err := s.BedStock(ctx, tenantID, propertyID, demand.Beds.Keys(), demand.Beds.Dates(), bd, excludeLine)
	if err != nil {
		return nil, err
	}
	return FindDemandShortfalls(inv, stock, demand), nil
}

// RequireAvailableFor tests a demand against both lines: 409 ROOM_TYPE_NOT_AVAILABLE when the room type is short, else 409
// BED_NOT_AVAILABLE when only the rooms with the bed are (the variant is sold out although the type is not).
func (s *Service) RequireAvailableFor(ctx context.Context, tenantID, propertyID int64, bd civil.Date, demand Demand, excludeLine *int64) error {
	short, err := s.ShortfallsFor(ctx, tenantID, propertyID, bd, demand, excludeLine)
	if err != nil {
		return err
	}
	return ShortfallError(short)
}

// ShortfallError turns a list of shortfalls into the conflict that names the failing line (nil when there are none).
func ShortfallError(short []Shortfall) error {
	if len(short) == 0 {
		return nil
	}
	var types, beds []Shortfall
	for _, f := range short {
		if f.BedTypeID == nil {
			types = append(types, f)
		} else {
			beds = append(beds, f)
		}
	}
	if len(types) > 0 {
		return apperr.Conflict("ROOM_TYPE_NOT_AVAILABLE", "the room type has no availability on some nights").
			WithContext("nights", capShortfalls(types)).WithContext("short_nights", len(types))
	}
	return apperr.Conflict("BED_NOT_AVAILABLE", "no room with this bed is available on some nights").
		WithContext("nights", capShortfalls(beds)).WithContext("short_nights", len(beds))
}

// BedChangeShortfalls: the bed type of a room changes. The room leaves the stock of its old bed from the business date on, so
// that variant must not end up oversold. A night on which the room is blocked is outside the stock already, and a night on which
// the room holds a booking moves that booking to the new bed with it (the line is unchanged); only a free night takes a room
// away from the old bed. The new bed only gains, and the type is unchanged.
func (s *Service) BedChangeShortfalls(ctx context.Context, tenantID, propertyID, roomID, newBedTypeID int64, bd civil.Date) ([]Shortfall, error) {
	q := s.q(ctx)
	room, err := q.GetRoomForCheck(ctx, availabilitydb.GetRoomForCheckParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return nil, err
	}
	if !room.IsActive || room.BedTypeID == newBedTypeID {
		return nil, nil
	}
	h, err := s.horizon(ctx, tenantID, propertyID, room.RoomTypeID, bd)
	if err != nil || !h.After(bd) {
		return nil, err
	}
	blocks, err := q.ListActiveRoomBlocks(ctx, availabilitydb.ListActiveRoomBlocksParams{TenantID: tenantID, PropertyID: propertyID, RoomID: roomID})
	if err != nil {
		return nil, err
	}
	lines, err := q.ListRoomLineOverlaps(ctx, availabilitydb.ListRoomLineOverlapsParams{TenantID: tenantID, PropertyID: propertyID, RoomID: &roomID, StartDate: bd, EndDate: h})
	if err != nil {
		return nil, err
	}
	stays, err := q.ListRoomStayOverlaps(ctx, availabilitydb.ListRoomStayOverlapsParams{TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, BusinessDate: bd, NextDate: bd.AddDays(1), StartDate: bd, EndDate: h})
	if err != nil {
		return nil, err
	}
	key := BedKey{RoomTypeID: room.RoomTypeID, BedTypeID: room.BedTypeID}
	extra := BedExtra{}
	for d := bd; d.Before(h); d = d.AddDays(1) {
		taken := false
		for _, b := range blocks {
			taken = taken || (!d.Before(b.StartDate) && d.Before(b.EndDate))
		}
		for _, l := range lines {
			taken = taken || (!d.Before(l.ArrivalDate) && d.Before(l.DepartureDate))
		}
		for _, st := range stays {
			to := st.DepartureDate
			if !to.After(bd) {
				to = bd.AddDays(1)
			}
			taken = taken || (!d.Before(bd) && d.Before(to))
		}
		if !taken {
			extra.Add(key, d, d.AddDays(1), 1)
		}
	}
	return s.ShortfallsFor(ctx, tenantID, propertyID, bd, Demand{Types: Extra{}, Beds: extra}, nil)
}

// RoomBed is the bed type of a room (every room has one).
func (s *Service) RoomBed(ctx context.Context, tenantID, propertyID, roomID int64) (int64, error) {
	room, err := s.q(ctx).GetRoomForCheck(ctx, availabilitydb.GetRoomForCheckParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return 0, err
	}
	return room.BedTypeID, nil
}
