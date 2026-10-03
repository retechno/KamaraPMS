package availability

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/availability/availabilitydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// maxHorizonNights caps how far forward an inventory check looks (demand cannot start beyond the last
// booked departure anyway).
const maxHorizonNights = 3000

// Service reads inventory. It joins the caller's transaction when there is one (writers call it after taking
// their locks) and otherwise reads committed data (search).
type Service struct{ txm *db.TxManager }

// NewService returns the availability service.
func NewService(txm *db.TxManager) *Service { return &Service{txm: txm} }

func (s *Service) q(ctx context.Context) *availabilitydb.Queries {
	return availabilitydb.New(s.txm.DB(ctx))
}

func isoDates(dates []civil.Date) []string {
	out := make([]string, len(dates))
	for i, d := range dates {
		out[i] = d.String()
	}
	return out
}

// Inventory returns sellable, demand and available per room type and night. Nights before the business
// date carry no stay demand. excludeLine removes one reservation line's own demand.
func (s *Service) Inventory(ctx context.Context, tenantID, propertyID int64, typeIDs []int64, dates []civil.Date, bd civil.Date, excludeLine *int64) (map[int64]map[civil.Date]Night, error) {
	out := map[int64]map[civil.Date]Night{}
	if len(typeIDs) == 0 || len(dates) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).NightInventory(ctx, availabilitydb.NightInventoryParams{
		TenantID: tenantID, PropertyID: propertyID, BusinessDate: bd, NextDate: bd.AddDays(1),
		RoomTypeIds: typeIDs, Dates: isoDates(dates), ExcludeLineID: excludeLine,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if out[r.RoomTypeID] == nil {
			out[r.RoomTypeID] = map[civil.Date]Night{}
		}
		sell, dem := int(r.Sellable), int(r.Demand)
		out[r.RoomTypeID][r.Night] = Night{Date: r.Night, Sellable: sell, Demand: dem, Available: sell - dem}
	}
	return out, nil
}

// Occupancy is how full the property is on a night: the rooms held against the rooms that can be sold.
type Occupancy struct {
	Sellable int
	Booked   int
}

// Percent is the share of the sellable rooms that are held, with two decimals (0 when nothing can be sold).
func (o Occupancy) Percent() decimal.Decimal {
	if o.Sellable <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromInt(int64(o.Booked)).Mul(decimal.NewFromInt(100)).DivRound(decimal.NewFromInt(int64(o.Sellable)), 2)
}

// PropertyOccupancy returns the occupancy of the whole property for each date, as of the business date bd.
func (s *Service) PropertyOccupancy(ctx context.Context, tenantID, propertyID int64, bd civil.Date, dates []civil.Date) (map[civil.Date]Occupancy, error) {
	out := make(map[civil.Date]Occupancy, len(dates))
	if len(dates) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).PropertyOccupancy(ctx, availabilitydb.PropertyOccupancyParams{
		TenantID: tenantID, PropertyID: propertyID, BusinessDate: bd, NextDate: bd.AddDays(1), Dates: isoDates(dates),
	})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Night] = Occupancy{Sellable: int(r.Sellable), Booked: int(r.Booked)}
	}
	return out, nil
}

// Shortfalls tests extra demand against the inventory and returns the nights that do not fit.
func (s *Service) Shortfalls(ctx context.Context, tenantID, propertyID int64, bd civil.Date, extra Extra, excludeLine *int64) ([]Shortfall, error) {
	inv, err := s.Inventory(ctx, tenantID, propertyID, extra.TypeIDs(), extra.Dates(), bd, excludeLine)
	if err != nil {
		return nil, err
	}
	return FindShortfalls(inv, extra), nil
}

// RequireAvailable is Shortfalls as an error: 409 ROOM_TYPE_NOT_AVAILABLE with the failing nights in
// context.nights (at most 60 are listed; context.short_nights has the total).
func (s *Service) RequireAvailable(ctx context.Context, tenantID, propertyID int64, bd civil.Date, extra Extra, excludeLine *int64) error {
	short, err := s.Shortfalls(ctx, tenantID, propertyID, bd, extra, excludeLine)
	if err != nil {
		return err
	}
	if len(short) == 0 {
		return nil
	}
	return apperr.Conflict("ROOM_TYPE_NOT_AVAILABLE", "the room type has no availability on some nights").
		WithContext("nights", capShortfalls(short)).WithContext("short_nights", len(short))
}

func capShortfalls(s []Shortfall) []Shortfall {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// horizon is the first date on which nothing of the type is held any more (exclusive end of demand).
func (s *Service) horizon(ctx context.Context, tenantID, propertyID, roomTypeID int64, bd civil.Date) (civil.Date, error) {
	q := s.q(ctx)
	lines, err := q.MaxLineDeparture(ctx, availabilitydb.MaxLineDepartureParams{TenantID: tenantID, PropertyID: propertyID, RoomTypeID: roomTypeID})
	if err != nil {
		return civil.Date{}, err
	}
	stays, err := q.MaxStayDeparture(ctx, availabilitydb.MaxStayDepartureParams{TenantID: tenantID, PropertyID: propertyID, RoomTypeID: roomTypeID})
	if err != nil {
		return civil.Date{}, err
	}
	h := lines
	if stays.After(h) {
		h = stays
	}
	if limit := bd.AddDays(maxHorizonNights); h.After(limit) {
		h = limit
	}
	return h, nil
}

// RemovalShortfalls: a room leaves its type's sellable stock (deactivated, or moved to another type). It
// returns the nights, from the business date on, on which the type would then be oversold. Nights on which
// the room is blocked are already outside the stock and are not counted.
func (s *Service) RemovalShortfalls(ctx context.Context, tenantID, propertyID, roomTypeID, roomID int64, bd civil.Date) ([]Shortfall, error) {
	h, err := s.horizon(ctx, tenantID, propertyID, roomTypeID, bd)
	if err != nil || !h.After(bd) {
		return nil, err
	}
	blocks, err := s.q(ctx).ListActiveRoomBlocks(ctx, availabilitydb.ListActiveRoomBlocksParams{TenantID: tenantID, PropertyID: propertyID, RoomID: roomID})
	if err != nil {
		return nil, err
	}
	extra := Extra{}
	for d := bd; d.Before(h); d = d.AddDays(1) {
		blocked := false
		for _, b := range blocks {
			blocked = blocked || (!d.Before(b.StartDate) && d.Before(b.EndDate))
		}
		if !blocked {
			extra.Add(roomTypeID, d, d.AddDays(1), 1)
		}
	}
	return s.Shortfalls(ctx, tenantID, propertyID, bd, extra, nil)
}

// BlockShortfalls: a room of the type is blocked (OOO or OOS) on [from, to). It returns the nights on which
// the type would then be oversold. Nights before the business date are ignored, and nights past the last
// booked departure cannot be short (the blocked room itself was sellable there).
func (s *Service) BlockShortfalls(ctx context.Context, tenantID, propertyID, roomTypeID int64, bd, from, to civil.Date) ([]Shortfall, error) {
	if from.Before(bd) {
		from = bd
	}
	h, err := s.horizon(ctx, tenantID, propertyID, roomTypeID, bd)
	if err != nil {
		return nil, err
	}
	if to.After(h) {
		to = h
	}
	if !to.After(from) {
		return nil, nil
	}
	extra := Extra{}
	extra.Add(roomTypeID, from, to, 1)
	return s.Shortfalls(ctx, tenantID, propertyID, bd, extra, nil)
}

// RoomIssues lists why a specific room is not free for [start, end): inactive, blocked, held by another
// CONFIRMED line, or occupied by an open stay. No issues means it is free. excludeLine ignores one line
// (the one being assigned or amended).
func (s *Service) RoomIssues(ctx context.Context, tenantID, propertyID, roomID int64, bd, start, end civil.Date, excludeLine *int64) ([]RoomIssue, error) {
	return s.RoomIssuesFor(ctx, tenantID, propertyID, roomID, bd, start, end, excludeLine, nil)
}

// RoomIssuesFor is RoomIssues that also ignores one open stay (the stay being extended holds its own room).
func (s *Service) RoomIssuesFor(ctx context.Context, tenantID, propertyID, roomID int64, bd, start, end civil.Date, excludeLine, excludeStay *int64) ([]RoomIssue, error) {
	q := s.q(ctx)
	room, err := q.GetRoomForCheck(ctx, availabilitydb.GetRoomForCheckParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return nil, err
	}
	var issues []RoomIssue
	if !room.IsActive {
		issues = append(issues, RoomIssue{Kind: IssueInactive})
	}
	blocks, err := q.ListRoomBlockOverlaps(ctx, availabilitydb.ListRoomBlockOverlapsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, StartDate: start, EndDate: end})
	if err != nil {
		return nil, err
	}
	for _, b := range blocks {
		issues = append(issues, RoomIssue{Kind: IssueBlocked, ID: b.ID, From: b.StartDate, To: b.EndDate})
	}
	lines, err := q.ListRoomLineOverlaps(ctx, availabilitydb.ListRoomLineOverlapsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: &roomID, StartDate: start, EndDate: end, ExcludeLineID: excludeLine})
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		issues = append(issues, RoomIssue{Kind: IssueReserved, ID: l.ID, From: l.ArrivalDate, To: l.DepartureDate})
	}
	stays, err := q.ListRoomStayOverlaps(ctx, availabilitydb.ListRoomStayOverlapsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, BusinessDate: bd, NextDate: bd.AddDays(1), StartDate: start, EndDate: end, ExcludeStayID: excludeStay})
	if err != nil {
		return nil, err
	}
	for _, st := range stays {
		to := st.DepartureDate
		if !to.After(bd) {
			to = bd.AddDays(1)
		}
		issues = append(issues, RoomIssue{Kind: IssueOccupied, ID: st.ID, From: bd, To: to})
	}
	return issues, nil
}

// RoomType is the type a room belongs to and whether the room can be sold at all (used by callers that need
// the physical type of an assigned room).
func (s *Service) RoomType(ctx context.Context, tenantID, propertyID, roomID int64) (typeID int64, active bool, err error) {
	room, err := s.q(ctx).GetRoomForCheck(ctx, availabilitydb.GetRoomForCheckParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return 0, false, err
	}
	return room.RoomTypeID, room.IsActive, nil
}

// FreeRoom is a specific room that can take a stay.
type FreeRoom struct {
	RoomID             int64  `json:"room_id"`
	RoomNumber         string `json:"room_number"`
	Floor              string `json:"floor,omitempty"`
	Building           string `json:"building,omitempty"`
	HousekeepingStatus string `json:"housekeeping_status"`
	// The bed type of the room, to match it with what a guest asked for (empty when the room has none).
	BedTypeID   *int64 `json:"bed_type_id"`
	BedTypeCode string `json:"bed_type_code,omitempty"`
	BedTypeName string `json:"bed_type_name,omitempty"`
}

// FreeRooms lists the free specific rooms of a type for [arrival, departure).
func (s *Service) FreeRooms(ctx context.Context, tenantID, propertyID, roomTypeID int64, bd, arrival, departure civil.Date) ([]FreeRoom, error) {
	rows, err := s.q(ctx).ListFreeRooms(ctx, availabilitydb.ListFreeRoomsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomTypeID: roomTypeID, BusinessDate: bd, NextDate: bd.AddDays(1), Arrival: arrival, Departure: departure})
	if err != nil {
		return nil, err
	}
	out := make([]FreeRoom, len(rows))
	for i, r := range rows {
		out[i] = FreeRoom{RoomID: r.RoomID, RoomNumber: r.RoomNumber, HousekeepingStatus: r.HousekeepingStatus, BedTypeID: r.BedTypeID}
		if r.BedTypeCode != nil {
			out[i].BedTypeCode = *r.BedTypeCode
		}
		if r.BedTypeName != nil {
			out[i].BedTypeName = *r.BedTypeName
		}
		if r.Floor != nil {
			out[i].Floor = *r.Floor
		}
		if r.Building != nil {
			out[i].Building = *r.Building
		}
	}
	return out, nil
}

// SellableType is an active room type as offered by a search.
type SellableType struct {
	ID           int64
	Code         string
	Name         string
	MaxAdult     int
	MaxChild     int
	MaxOccupancy int
}

// SellableTypes lists the active room types of the property in display order.
func (s *Service) SellableTypes(ctx context.Context, tenantID, propertyID int64) ([]SellableType, error) {
	rows, err := s.q(ctx).ListSellableRoomTypes(ctx, availabilitydb.ListSellableRoomTypesParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]SellableType, len(rows))
	for i, r := range rows {
		out[i] = SellableType{ID: r.ID, Code: r.Code, Name: r.Name, MaxAdult: int(r.MaxAdult), MaxChild: int(r.MaxChild), MaxOccupancy: int(r.MaxOccupancy)}
	}
	return out, nil
}

// SellablePlan is an active rate plan as offered by a search.
type SellablePlan struct {
	ID        int64
	Code      string
	Name      string
	PriceMode string
}

// SellablePlans lists the active rate plans of the property.
func (s *Service) SellablePlans(ctx context.Context, tenantID, propertyID int64) ([]SellablePlan, error) {
	rows, err := s.q(ctx).ListSellableRatePlans(ctx, availabilitydb.ListSellableRatePlansParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]SellablePlan, len(rows))
	for i, r := range rows {
		out[i] = SellablePlan{ID: r.ID, Code: r.Code, Name: r.Name, PriceMode: r.PriceMode}
	}
	return out, nil
}
