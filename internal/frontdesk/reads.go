package frontdesk

import (
	"context"

	"kamarapms/internal/frontdesk/frontdeskdb"
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
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: before, Status: nullable(f.Status), DepartureDate: f.DepartureDate, RoomID: f.RoomID, RowLimit: rowLimit(limit),
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
			RoomTypeCode: r.RoomTypeCode, RoomID: r.RoomID, RoomNumber: deref(r.RoomNumber), ArrivalDate: r.ArrivalDate, DepartureDate: r.DepartureDate,
			AdultCount: int(r.AdultCount), ChildCount: int(r.ChildCount),
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
