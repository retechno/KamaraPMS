package reservations

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations/reservationsdb"
)

// load builds the detail view of a reservation row: lines with nightly snapshots, estimates, stays and folios.
func (s *Service) load(ctx context.Context, tenantID, propertyID int64, res reservationsdb.Reservation) (Reservation, error) {
	q := s.q(ctx)
	lines, err := q.ListLines(ctx, reservationsdb.ListLinesParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: res.ID})
	if err != nil {
		return Reservation{}, err
	}
	lineIDs := make([]int64, len(lines))
	typeIDs := make([]int64, 0, len(lines))
	planIDs := make([]int64, 0, len(lines))
	roomIDs := make([]int64, 0, len(lines))
	guestIDs := make([]int64, 0, len(lines)+1)
	if res.GuestID != nil {
		guestIDs = append(guestIDs, *res.GuestID)
	}
	for i, l := range lines {
		lineIDs[i] = l.ID
		typeIDs = append(typeIDs, l.RoomTypeID)
		planIDs = append(planIDs, l.RatePlanID)
		if l.RoomID != nil {
			roomIDs = append(roomIDs, *l.RoomID)
		}
		if l.GuestID != nil {
			guestIDs = append(guestIDs, *l.GuestID)
		}
	}
	types, err := q.GetRoomTypesForBooking(ctx, reservationsdb.GetRoomTypesForBookingParams{TenantID: tenantID, PropertyID: propertyID, Ids: typeIDs})
	if err != nil {
		return Reservation{}, err
	}
	typeCode := map[int64]string{}
	for _, t := range types {
		typeCode[t.ID] = t.Code
	}
	plans, err := q.ListRatePlanBriefs(ctx, reservationsdb.ListRatePlanBriefsParams{TenantID: tenantID, PropertyID: propertyID, Ids: planIDs})
	if err != nil {
		return Reservation{}, err
	}
	planCode, planKind := map[int64]string{}, map[int64]string{}
	for _, pl := range plans {
		planCode[pl.ID], planKind[pl.ID] = pl.Code, pl.OccupancyKind
	}
	bedIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		if l.RequestedBedTypeID != nil {
			bedIDs = append(bedIDs, *l.RequestedBedTypeID)
		}
	}
	beds, err := q.ListBedTypeBriefs(ctx, reservationsdb.ListBedTypeBriefsParams{TenantID: tenantID, PropertyID: propertyID, Ids: bedIDs})
	if err != nil {
		return Reservation{}, err
	}
	bedBy := map[int64]reservationsdb.ListBedTypeBriefsRow{}
	for _, b := range beds {
		bedBy[b.ID] = b
	}
	rooms, err := q.ListRoomNumbers(ctx, reservationsdb.ListRoomNumbersParams{PropertyID: propertyID, Ids: roomIDs})
	if err != nil {
		return Reservation{}, err
	}
	roomNumber := map[int64]string{}
	for _, r := range rooms {
		roomNumber[r.ID] = r.RoomNumber
	}
	gs, err := q.ListGuestBriefs(ctx, reservationsdb.ListGuestBriefsParams{TenantID: tenantID, Ids: guestIDs})
	if err != nil {
		return Reservation{}, err
	}
	guestBy := map[int64]*GuestName{}
	for _, g := range gs {
		guestBy[g.ID] = &GuestName{ID: g.ID, Code: g.Code, FirstName: deref(g.FirstName), LastName: g.LastName}
	}
	rateRows, err := q.ListNightRates(ctx, reservationsdb.ListNightRatesParams{TenantID: tenantID, PropertyID: propertyID, LineIds: lineIDs})
	if err != nil {
		return Reservation{}, err
	}
	ratesBy := map[int64][]NightRate{}
	chargesBy := map[int64][]billingconfig.NightCharge{}
	for _, r := range rateRows {
		ratesBy[r.ReservationRoomID] = append(ratesBy[r.ReservationRoomID], NightRate{
			Date: r.StayDate, RatePlanID: r.RatePlanID, ChargeCodeID: r.ChargeCodeID, PriceMode: r.PriceMode, BaseRate: r.BaseRate,
			DiscountAmount: r.DiscountAmount, Amount: r.Amount, IsOverride: r.IsOverride, GridRate: r.GridRate, YieldRules: r.YieldRules,
		})
		chargesBy[r.ReservationRoomID] = append(chargesBy[r.ReservationRoomID], billingconfig.NightCharge{
			ChargeCodeID: r.ChargeCodeID, PriceMode: chargecalc.PriceMode(r.PriceMode), Amount: r.Amount,
		})
	}
	stays, err := q.ListStaysOfLines(ctx, reservationsdb.ListStaysOfLinesParams{PropertyID: propertyID, LineIds: lineIDs})
	if err != nil {
		return Reservation{}, err
	}
	stayBy := map[int64]int64{}
	for _, st := range stays {
		stayBy[st.ReservationRoomID] = st.ID
	}
	folioRows, err := q.ListReservationFolios(ctx, reservationsdb.ListReservationFoliosParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: res.ID})
	if err != nil {
		return Reservation{}, err
	}

	out := Reservation{
		ID: res.ID, ConfirmationNumber: res.ConfirmationNumber, GuestID: res.GuestID, ReservationDate: res.ReservationDate,
		Source: res.Source, Market: deref(res.Market), Status: res.Status, SpecialRequest: deref(res.SpecialRequest), Remarks: deref(res.Remarks),
		ConfirmedAt: res.ConfirmedAt, CancelledAt: res.CancelledAt, CancellationReason: deref(res.CancellationReason),
		Version: res.Version, CreatedAt: res.CreatedAt, Rooms: make([]Line, 0, len(lines)), Folios: make([]FolioBrief, 0, len(folioRows)),
	}
	if res.GuestID != nil {
		out.Guest = guestBy[*res.GuestID]
	}
	out.CompanyID, out.BookingGroupID = res.CompanyID, res.BookingGroupID
	if res.CompanyID != nil {
		c, err := q.CompanyRef(ctx, reservationsdb.CompanyRefParams{TenantID: tenantID, PropertyID: propertyID, ID: *res.CompanyID})
		if err != nil {
			return Reservation{}, err
		}
		out.CompanyName = c.Name
	}
	if res.BookingGroupID != nil {
		g, err := q.GroupRef(ctx, reservationsdb.GroupRefParams{TenantID: tenantID, PropertyID: propertyID, ID: *res.BookingGroupID})
		if err != nil {
			return Reservation{}, err
		}
		out.GroupCode = g.Code
	}
	statuses := make([]string, 0, len(lines))
	var arrival, departure, allArrival, allDeparture civil.Date
	for i, l := range lines {
		line := Line{
			ID: l.ID, Status: l.Status, RoomTypeID: l.RoomTypeID, RoomTypeCode: typeCode[l.RoomTypeID], RoomID: l.RoomID,
			RatePlanID: l.RatePlanID, RatePlanCode: planCode[l.RatePlanID], OccupancyKind: planKind[l.RatePlanID], OccupancyReason: deref(l.OccupancyReason), GuestID: l.GuestID, ArrivalDate: l.ArrivalDate,
			DepartureDate: l.DepartureDate, Nights: l.ArrivalDate.DaysUntil(l.DepartureDate), AdultCount: int(l.AdultCount),
			ChildCount: int(l.ChildCount), CancelledAt: l.CancelledAt, CancellationReason: deref(l.CancellationReason), NoShowAt: l.NoShowAt,
			NightlyRates: ratesBy[l.ID],
		}
		line.BedLocked = l.BedLocked
		if l.RequestedBedTypeID != nil {
			line.BedTypeID = l.RequestedBedTypeID
			line.BedTypeCode, line.BedTypeName = bedBy[*l.RequestedBedTypeID].Code, bedBy[*l.RequestedBedTypeID].Name
		}
		if line.NightlyRates == nil {
			line.NightlyRates = []NightRate{}
		}
		if l.RoomID != nil {
			line.RoomNumber = roomNumber[*l.RoomID]
		}
		if l.GuestID != nil {
			line.Guest = guestBy[*l.GuestID]
		}
		if id, ok := stayBy[l.ID]; ok {
			line.StayID = &id
		}
		est, err := s.billing.Estimate(ctx, tenantID, propertyID, chargesBy[l.ID])
		if err != nil {
			return Reservation{}, err
		}
		line.Estimate = Estimate{Net: est.Net, Service: est.Service, Tax: est.Tax, Total: est.Total}
		out.Rooms = append(out.Rooms, line)
		statuses = append(statuses, l.Status)
		if i == 0 || l.ArrivalDate.Before(allArrival) {
			allArrival = l.ArrivalDate
		}
		if i == 0 || l.DepartureDate.After(allDeparture) {
			allDeparture = l.DepartureDate
		}
		if l.Status != LineCancelled {
			if arrival.IsZero() || l.ArrivalDate.Before(arrival) {
				arrival = l.ArrivalDate
			}
			if departure.IsZero() || l.DepartureDate.After(departure) {
				departure = l.DepartureDate
			}
		}
	}
	if arrival.IsZero() {
		arrival, departure = allArrival, allDeparture
	}
	out.ArrivalDate, out.DepartureDate = arrival, departure
	out.DisplayStatus = DisplayStatus(res.Status, statuses)
	for _, f := range folioRows {
		out.Folios = append(out.Folios, FolioBrief{ID: f.ID, FolioNumber: f.FolioNumber, StayID: f.StayID, Status: f.Status, Balance: f.Balance})
	}
	return out, nil
}

// Get returns a reservation (reservation.read).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return Reservation{}, err
	}
	res, err := s.q(ctx).GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Reservation{}, orNotFound(err, errNotFound())
	}
	return s.load(ctx, p.TenantID, propertyID, res)
}

// List searches reservations, newest first (reservation.read). before is the id to continue after.
func (s *Service) List(ctx context.Context, propertyID int64, f ListFilter, before int64, limit int) ([]Summary, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).SearchReservations(ctx, reservationsdb.SearchReservationsParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, Status: nullable(f.Status), ArrivalFrom: f.ArrivalFrom,
		ArrivalTo: f.ArrivalTo, Q: nullable(f.Query), CompanyID: f.CompanyID, BookingGroupID: f.GroupID, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		name := deref(r.GuestLastName)
		if fn := deref(r.GuestFirstName); fn != "" {
			name = fn + " " + name
		}
		out[i] = Summary{
			ID: r.ID, ConfirmationNumber: r.ConfirmationNumber, GuestID: r.GuestID, GuestName: name, Source: r.Source, Status: r.Status,
			ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, RoomCount: int(r.RoomCount), Version: r.Version, CreatedAt: r.CreatedAt,
			CompanyID: r.CompanyID, CompanyName: deref(r.CompanyName), BookingGroupID: r.BookingGroupID, GroupCode: deref(r.GroupCode),
		}
	}
	return out, nil
}

// folioTotals is the balance still on the reservation's folios and whether a cancelled reservation still
// has an OPEN folio with a balance (a deposit to refund or a fee to post).
func folioTotals(folios []FolioBrief) (decimal.Decimal, bool) {
	total := decimal.Zero
	open := false
	for _, f := range folios {
		total = total.Add(f.Balance)
		if f.Status == "OPEN" && !f.Balance.IsZero() {
			open = true
		}
	}
	return total, open
}
