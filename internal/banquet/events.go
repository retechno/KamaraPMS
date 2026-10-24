package banquet

import (
	"context"
	"slices"
	"strings"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

var civilEpoch = civil.MustParseDate("1900-01-01")

func dateOrNil(d civil.Date) *civil.Date {
	if d.IsZero() || d.Equal(civilEpoch) {
		return nil
	}
	return &d
}

func (s *Service) overdue(status string, optionUntil *civil.Date, today civil.Date) bool {
	return status == StatusTentative && optionUntil != nil && !today.IsZero() && optionUntil.Before(today)
}

// ---------------------------------------------------------------- reads

// ListEvents answers the events of the property, newest session first by date.
func (s *Service) ListEvents(ctx context.Context, propertyID int64, f EventFilter) ([]EventRow, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return nil, err
	}
	for _, st := range f.Statuses {
		if !slices.Contains([]string{StatusInquiry, StatusTentative, StatusDefinite, StatusCompleted, StatusCancelled, StatusLost}, st) {
			return nil, invalid("filter", fieldErr("status", "INVALID_VALUE", "an unknown status: "+st))
		}
	}
	limit := f.Limit
	if limit < 1 || limit > maxRows {
		limit = maxRows
	}
	today := s.today(ctx, propertyID)
	var q *string
	if f.Query != "" {
		q = &f.Query
	}
	if f.Statuses == nil {
		f.Statuses = []string{}
	}
	rows, err := s.q(ctx).ListEvents(ctx, banquetdb.ListEventsParams{TenantID: p.TenantID, PropertyID: propertyID, Statuses: f.Statuses, SalesUserID: f.SalesUserID, EventTypeID: f.EventTypeID,
		DateFrom: f.From, DateTo: f.To, OptionOverdue: f.OptionOverdue, Today: today, Q: q, RowLimit: int32(limit)}) //nolint:gosec // G115: bounded above
	if err != nil {
		return nil, err
	}
	out := make([]EventRow, len(rows))
	for i, r := range rows {
		out[i] = EventRow{ID: r.ID, Number: r.Number, Status: r.Status, Name: r.Name, EventTypeID: r.EventTypeID, EventTypeName: r.EventTypeName, BookerName: r.BookerName,
			SalesUserID: r.SalesUserID, SalesName: r.SalesName, OptionUntil: r.OptionUntil, OptionOverdue: s.overdue(r.Status, r.OptionUntil, today),
			FirstDate: dateOrNil(r.FirstDate), LastDate: dateOrNil(r.LastDate), Sessions: int(r.Sessions), PaxEstimated: int(r.PaxEstimated), Version: r.Version}
	}
	return out, nil
}

func (s *Service) event(ctx context.Context, tenantID, propertyID, id int64) (Event, error) {
	q := s.q(ctx)
	r, err := q.GetEvent(ctx, banquetdb.GetEventParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Event{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	ev := Event{ID: r.ID, Number: r.Number, Status: r.Status, Name: r.Name, EventTypeID: r.EventTypeID, EventTypeName: r.EventTypeName, BookerGuestID: r.BookerGuestID,
		BookerCompanyID: r.BookerCompanyID, BookerName: r.BookerName, ContactName: r.ContactName, ContactPhone: r.ContactPhone, ContactEmail: r.ContactEmail,
		SalesUserID: r.SalesUserID, SalesName: r.SalesName, OptionUntil: r.OptionUntil, OptionOverdue: s.overdue(r.Status, r.OptionUntil, s.today(ctx, propertyID)),
		ReasonID: r.ReasonID, ReasonName: r.ReasonName, ReasonNote: r.ReasonNote, BookingGroupID: r.BookingGroupID, BookingGroupName: r.BookingGroupName, Notes: r.Notes,
		InquiryAt: r.InquiryAt, DefiniteAt: r.DefiniteAt, ClosedAt: r.ClosedAt, Version: r.Version}
	ev.Sessions, err = s.sessions(ctx, tenantID, propertyID, id, nil, r.Status)
	return ev, err
}

func (s *Service) sessions(ctx context.Context, tenantID, propertyID, eventID int64, sessionID *int64, status string) ([]Session, error) {
	q := s.q(ctx)
	rows, err := q.ListSessions(ctx, banquetdb.ListSessionsParams{TenantID: tenantID, PropertyID: propertyID, EventID: eventID, ID: sessionID})
	if err != nil {
		return nil, err
	}
	out := make([]Session, len(rows))
	ids := []int64{}
	for i, r := range rows {
		out[i] = Session{ID: r.ID, SessionNo: int(r.SessionNo), Name: r.Name, SessionDate: r.SessionDate, StartTime: r.StartTime, EndTime: r.EndTime, EndsNextDay: r.EndsNextDay,
			RoomID: r.RoomID, RoomCode: r.RoomCode, RoomName: r.RoomName, SetupStyle: r.SetupStyle, PaxEstimated: int(r.PaxEstimated), Status: r.Status, Notes: r.Notes}
		if r.PaxGuaranteed != nil {
			out[i].PaxGuaranteed = ptr(int(*r.PaxGuaranteed))
		}
		if r.PaxActual != nil {
			out[i].PaxActual = ptr(int(*r.PaxActual))
		}
		if r.Status == "ACTIVE" {
			ids = append(ids, r.ID)
		}
	}
	if status != StatusTentative && status != StatusDefinite {
		return out, nil
	}
	holds, err := q.EventHoldRanges(ctx, banquetdb.EventHoldRangesParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	ranks, err := q.SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, h := range holds {
			if h.SessionID == out[i].ID {
				out[i].HoldFrom = ptr(h.HoldFrom.Time.Format("2006-01-02T15:04"))
				out[i].HoldTo = ptr(h.HoldTo.Time.Format("2006-01-02T15:04"))
			}
		}
		if out[i].HoldFrom == nil {
			continue
		}
		for _, rk := range ranks {
			if rk.SessionID == out[i].ID {
				out[i].RoomTaken = rk.RoomTaken
				if status == StatusTentative {
					out[i].Rank = ptr(int(rk.Rank))
				}
			}
		}
	}
	return out, nil
}

// GetEvent answers one event with its sessions and, for a tentative one, the place of each session in the queue.
func (s *Service) GetEvent(ctx context.Context, propertyID, id int64) (Event, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return Event{}, err
	}
	return s.event(ctx, p.TenantID, propertyID, id)
}

// ---------------------------------------------------------------- the event

func checkEvent(in EventInput) []apperr.FieldError {
	var f []apperr.FieldError
	if n := len([]rune(strings.TrimSpace(in.Name))); n < 1 || n > 150 {
		f = append(f, fieldErr("name", "INVALID_VALUE", "1 to 150 characters"))
	}
	if (in.BookerGuestID == nil) == (in.BookerCompanyID == nil) {
		f = append(f, fieldErr("booker_guest_id", "BOOKER_REQUIRED", "an event is booked by a guest or by a company, one of the two"))
	}
	for _, c := range []struct {
		f string
		v *string
		n int
	}{{"contact_name", in.ContactName, 100}, {"contact_phone", in.ContactPhone, 40}, {"contact_email", in.ContactEmail, 150}} {
		if c.v != nil && len([]rune(*c.v)) > c.n {
			f = append(f, fieldErr(c.f, "INVALID_VALUE", "too long"))
		}
	}
	return f
}

func blankToNil(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

// CreateEvent opens an inquiry (banquet.manage): it holds nothing and may have no room yet.
func (s *Service) CreateEvent(ctx context.Context, propertyID int64, in EventInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkEvent(in); len(f) > 0 {
		return Event{}, invalid("event", f...)
	}
	sales := p.UserID
	if in.SalesUserID != nil {
		sales = *in.SalesUserID
	}
	if sales < 1 {
		return Event{}, invalid("event", fieldErr("sales_user_id", "REQUIRED", "the salesperson of the event"))
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if in.BookingGroupID != nil {
			if err := db.LockRows(ctx, db.Groups, db.ForShare, propertyID, []int64{*in.BookingGroupID}); err != nil {
				return invalid("event", fieldErr("booking_group_id", "GROUP_NOT_FOUND", "no such group in this property"))
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqBanquetEvent)
		if err != nil {
			return err
		}
		id, err := s.q(ctx).InsertEvent(ctx, banquetdb.InsertEventParams{TenantID: p.TenantID, PropertyID: propertyID, Number: number, Name: in.Name, EventTypeID: in.EventTypeID,
			BookerGuestID: in.BookerGuestID, BookerCompanyID: in.BookerCompanyID, ContactName: blankToNil(in.ContactName), ContactPhone: blankToNil(in.ContactPhone),
			ContactEmail: blankToNil(in.ContactEmail), SalesUserID: sales, BookingGroupID: in.BookingGroupID, Notes: blankToNil(in.Notes), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_created", "banquet_event", id, number, nil, out))
	})
	return out, err
}

// UpdateEvent changes the data of an event that is not closed (banquet.manage). It never touches the holds. The option date of a tentative event can be moved here.
func (s *Service) UpdateEvent(ctx context.Context, propertyID, id int64, in EventInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkEvent(in); len(f) > 0 {
		return Event{}, invalid("event", f...)
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		hdr, err := q.EventHeader(ctx, banquetdb.EventHeaderParams{PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
		}
		groups := []int64{}
		if hdr.BookingGroupID != nil {
			groups = append(groups, *hdr.BookingGroupID)
		}
		if in.BookingGroupID != nil {
			groups = append(groups, *in.BookingGroupID)
		}
		if len(groups) > 0 {
			if err := db.LockRows(ctx, db.Groups, db.ForShare, propertyID, groups); err != nil {
				return invalid("event", fieldErr("booking_group_id", "GROUP_NOT_FOUND", "no such group in this property"))
			}
		}
		if err := db.LockRows(ctx, db.BanquetEvents, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return err
		}
		old, err := s.event(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if old.Status != StatusInquiry && old.Status != StatusTentative && old.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", old.Status)
		}
		if old.Version != in.Version {
			return apperr.Conflict("VERSION_CONFLICT", "the event was changed by someone else").WithContext("current_version", old.Version)
		}
		sales := old.SalesUserID
		if in.SalesUserID != nil {
			sales = *in.SalesUserID
		}
		option := old.OptionUntil
		if old.Status == StatusTentative {
			if in.OptionUntil == nil {
				return invalidCode("OPTION_DATE_REQUIRED", "option_until", "a tentative event has an option date")
			}
			if in.OptionUntil.Before(bd) {
				return apperr.Invalid("the event is invalid", fieldErr("option_until", "INVALID_VALUE", "the option date is not before the business date"))
			}
			option = in.OptionUntil
		}
		n, err := q.UpdateEvent(ctx, banquetdb.UpdateEventParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, EventTypeID: in.EventTypeID,
			BookerGuestID: in.BookerGuestID, BookerCompanyID: in.BookerCompanyID, ContactName: blankToNil(in.ContactName), ContactPhone: blankToNil(in.ContactPhone),
			ContactEmail: blankToNil(in.ContactEmail), SalesUserID: sales, BookingGroupID: in.BookingGroupID, Notes: blankToNil(in.Notes), OptionUntil: option, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.Conflict("VERSION_CONFLICT", "the event was changed by someone else").WithContext("current_version", old.Version)
		}
		out, err = s.event(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_updated", "banquet_event", id, out.Number, old, out))
	})
	return out, err
}
