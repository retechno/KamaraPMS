package frontdesk

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk/frontdeskdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// ListStays lists stays, newest first (reservation.read): the in-house and due-out lists. before is the id to
// continue after (0 for the first page).
func (s *Service) ListStays(ctx context.Context, propertyID int64, f StayFilter, before int64, limit int) ([]StaySummary, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListStays(ctx, frontdeskdb.ListStaysParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, Status: nullable(f.Status), DepartureDate: f.DepartureDate, DepartureUntil: f.DepartureUntil, RoomID: f.RoomID, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]StaySummary, len(rows))
	for i, r := range rows {
		roomID := r.RoomID
		out[i] = StaySummary{
			ID: r.ID, StayNumber: r.StayNumber, Status: r.Status, GuestID: r.GuestID, GuestName: guestName(r.GuestFirstName, r.GuestLastName),
			RoomID: &roomID, RoomNumber: r.RoomNumber, ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber,
			ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate, AdultCount: int(r.AdultCount), ChildCount: int(r.ChildCount), Version: r.Version,
		}
	}
	return out, nil
}

// ListInHouse lists the OPEN stays for the in-house screen, newest first (reservation.read): guest, room and type, company, the price of the night the stay is in and the balance of its folios.
// before is the id to continue after (0 for the first page). Balances come from the folio service (the ledger), the price from the snapshot of the booking.
func (s *Service) ListInHouse(ctx context.Context, propertyID, before int64, limit int) ([]InHouseRow, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	rows, err := q.ListInHouse(ctx, frontdeskdb.ListInHouseParams{TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, RowLimit: rowLimit(limit)})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var onDate *civil.Date // without an open business day the arrival night is shown
	if day, err := s.days.CurrentBusinessDay(ctx, propertyID); err == nil {
		onDate = &day.BusinessDate
	} else if !apperr.IsCode(err, "BUSINESS_DAY_NOT_FOUND") {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	rates, err := q.ListInHouseRates(ctx, frontdeskdb.ListInHouseRatesParams{TenantID: p.TenantID, PropertyID: propertyID, StayIds: ids, OnDate: onDate})
	if err != nil {
		return nil, err
	}
	rateOf := make(map[int64]frontdeskdb.ListInHouseRatesRow, len(rates))
	for _, r := range rates {
		rateOf[r.StayID] = r
	}
	instr, err := q.ListInHouseInstructions(ctx, frontdeskdb.ListInHouseInstructionsParams{TenantID: p.TenantID, PropertyID: propertyID, StayIds: ids})
	if err != nil {
		return nil, err
	}
	billing := map[int64][]InHouseBilling{}
	for _, i := range instr {
		billing[i.StayID] = append(billing[i.StayID], InHouseBilling{Scope: i.Scope, ChargeCode: deref(i.ChargeCode), CompanyID: i.CompanyID, CompanyName: i.CompanyName})
	}
	out := make([]InHouseRow, len(rows))
	folioOf := make([][]folios.StayFolio, len(rows))
	names := map[int64]string{}
	var companyIDs []int64
	for i, r := range rows {
		if folioOf[i], err = s.folios.StayFolios(ctx, p.TenantID, propertyID, r.ID); err != nil {
			return nil, err
		}
		for _, f := range folioOf[i] {
			if f.BillToCompanyID != nil {
				if _, seen := names[*f.BillToCompanyID]; !seen {
					names[*f.BillToCompanyID] = ""
					companyIDs = append(companyIDs, *f.BillToCompanyID)
				}
			}
		}
	}
	if len(companyIDs) > 0 {
		named, err := q.ListCompanyNames(ctx, frontdeskdb.ListCompanyNamesParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: companyIDs})
		if err != nil {
			return nil, err
		}
		for _, c := range named {
			names[c.ID] = c.Name
		}
	}
	for i, r := range rows {
		total := decimal.Zero
		for _, f := range folioOf[i] {
			b, err := decimal.NewFromString(f.Balance)
			if err != nil {
				return nil, err
			}
			total = total.Add(b)
		}
		rt := rateOf[r.ID]
		bill := billing[r.ID]
		if bill == nil {
			bill = []InHouseBilling{}
		}
		out[i] = InHouseRow{
			ID: r.ID, StayNumber: r.StayNumber, Version: r.Version, ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber,
			Guest:   InHouseGuest{ID: r.GuestID, Name: guestName(r.GuestFirstName, r.GuestLastName)},
			Room:    InHouseRoom{ID: r.RoomID, Number: r.RoomNumber, RoomTypeCode: r.RoomTypeCode, RoomTypeName: r.RoomTypeName},
			Company: payingCompany(bill, folioOf[i], names), Billing: bill,
			Rate:    InHouseRate{RatePlanCode: r.RatePlanCode, RatePlanName: r.RatePlanName, Amount: rt.Amount.StringFixed(prop.CurrencyDecimals), PriceMode: rt.PriceMode, IsOverride: rt.IsOverride},
			Stay:    InHouseStay{Arrival: r.ArrivalDate, Departure: r.DepartureDate, Nights: r.ArrivalDate.DaysUntil(r.DepartureDate), Adults: int(r.AdultCount), Children: int(r.ChildCount)},
			Balance: InHouseBalance{Amount: total.StringFixed(prop.CurrencyDecimals), Status: balanceStatus(total), Folios: folioOf[i]},
		}
	}
	return out, nil
}

// payingCompany is the company the stay is billed to: the one of its first instruction (the whole stay or the room first), else the one of a company folio, else none.
func payingCompany(bill []InHouseBilling, fs []folios.StayFolio, names map[int64]string) *InHouseCompany {
	if len(bill) > 0 {
		return &InHouseCompany{ID: bill[0].CompanyID, Name: bill[0].CompanyName}
	}
	sorted := append([]folios.StayFolio(nil), fs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, f := range sorted {
		if f.BillToCompanyID != nil {
			return &InHouseCompany{ID: *f.BillToCompanyID, Name: names[*f.BillToCompanyID]}
		}
	}
	return nil
}

func balanceStatus(d decimal.Decimal) string {
	switch d.Sign() {
	case 1:
		return "OUTSTANDING"
	case -1:
		return "CREDIT"
	}
	return "SETTLED"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Arrivals lists the CONFIRMED rooms arriving on a date (reservation.read); date defaults to the business date.
func (s *Service) Arrivals(ctx context.Context, propertyID int64, date *civil.Date) ([]Arrival, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	var d civil.Date
	if date != nil {
		d = *date
	} else {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return nil, err
		}
		d = day.BusinessDate
	}
	rows, err := s.q(ctx).ListArrivals(ctx, frontdeskdb.ListArrivalsParams{TenantID: p.TenantID, PropertyID: propertyID, Arrival: d})
	if err != nil {
		return nil, err
	}
	out := make([]Arrival, len(rows))
	for i, r := range rows {
		out[i] = Arrival{
			ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber, ReservationRoomID: r.LineID, ReservationVersion: r.ReservationVersion,
			GuestID: firstID(r.LineGuestID, r.BookerID), GuestName: guestNameOpt(r.GuestFirstName, r.GuestLastName), RoomTypeID: r.RoomTypeID,
			RoomTypeCode: r.RoomTypeCode, RoomID: r.RoomID, RoomNumber: deref(r.RoomNumber), HousekeepingStatus: deref(r.HousekeepingStatus), ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate,
			AdultCount: int(r.AdultCount), ChildCount: int(r.ChildCount),
			RequestedBedTypeID: r.RequestedBedTypeID, BedLocked: r.BedLocked, RequestedBedTypeCode: deref(r.RequestedBedTypeCode), RoomBedTypeCode: deref(r.RoomBedTypeCode),
		}
	}
	return out, nil
}

func firstID(a, b *int64) *int64 {
	if a != nil {
		return a
	}
	return b
}

func guestNameOpt(first, last *string) string {
	if last == nil {
		return ""
	}
	return guestName(first, *last)
}

// GetStay returns a stay with its segments, guests, room line, nightly rates and folio (reservation.read).
func (s *Service) GetStay(ctx context.Context, propertyID, id int64) (StayDetail, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return StayDetail{}, err
	}
	return s.stayDetail(ctx, p, propertyID, id)
}

// stayDetail builds the detail view of a stay for a caller who has already been authorized.
func (s *Service) stayDetail(ctx context.Context, p auth.Principal, propertyID, id int64) (StayDetail, error) {
	q := s.q(ctx)
	st, err := q.GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return StayDetail{}, notFound(err, errStayNotFound())
	}
	line, err := q.GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ReservationRoomID})
	if err != nil {
		return StayDetail{}, err
	}
	segs, err := q.ListSegments(ctx, frontdeskdb.ListSegmentsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: id})
	if err != nil {
		return StayDetail{}, err
	}
	out := StayDetail{
		Stay: toStay(st, line.ReservationID), Segments: make([]Segment, len(segs)), Guests: []GuestRef{},
		Line: LineRef{ID: line.ID, ReservationID: line.ReservationID, ConfirmationNumber: line.ConfirmationNumber, RoomTypeCode: line.RoomTypeCode, Status: line.Status},
	}
	for i, sg := range segs {
		out.Segments[i] = toSegment(frontdeskdb.StayRoom{ID: sg.ID, RoomID: sg.RoomID, CheckInAt: sg.CheckInAt, CheckOutAt: sg.CheckOutAt,
			StartBusinessDate: sg.StartBusinessDate, EndBusinessDate: sg.EndBusinessDate, MoveReason: sg.MoveReason}, sg.RoomNumber)
	}
	g, err := q.GetGuestBrief(ctx, frontdeskdb.GetGuestBriefParams{TenantID: p.TenantID, ID: st.GuestID})
	if err != nil {
		return StayDetail{}, err
	}
	out.Guest = &GuestRef{ID: g.ID, Code: g.Code, FirstName: deref(g.FirstName), LastName: g.LastName}
	others, err := q.ListStayGuests(ctx, frontdeskdb.ListStayGuestsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: id})
	if err != nil {
		return StayDetail{}, err
	}
	for _, o := range others {
		out.Guests = append(out.Guests, GuestRef{ID: o.ID, Code: o.Code, FirstName: deref(o.FirstName), LastName: o.LastName})
	}
	decimals, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return StayDetail{}, err
	}
	nights, err := q.ListStayNights(ctx, frontdeskdb.ListStayNightsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: id, LineID: line.ID})
	if err != nil {
		return StayDetail{}, err
	}
	out.NightlyRates = make([]NightView, len(nights))
	for i, n := range nights {
		out.NightlyRates[i] = NightView{Date: n.StayDate, Amount: n.Amount.StringFixed(decimals.CurrencyDecimals), PriceMode: n.PriceMode, IsOverride: n.IsOverride, Posted: n.Posted}
	}
	if out.Folios, err = s.folios.StayFolios(ctx, p.TenantID, propertyID, id); err != nil {
		return StayDetail{}, err
	}
	return out, nil
}
