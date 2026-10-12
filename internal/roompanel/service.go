package roompanel

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlog"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations"
	"kamarapms/internal/tenancy"
)

// Service builds the room panel from the services that own the data.
type Service struct {
	txm          *db.TxManager
	days         *tenancy.Service
	housekeeping *housekeeping.Service
	frontdesk    *frontdesk.Service
	reservations *reservations.Service
	folios       *folios.Service
	audit        *auditlog.Reader
}

// NewService returns the service.
func NewService(txm *db.TxManager, days *tenancy.Service, hk *housekeeping.Service, fd *frontdesk.Service, res *reservations.Service, f *folios.Service, a *auditlog.Reader) *Service {
	return &Service{txm: txm, days: days, housekeeping: hk, frontdesk: fd, reservations: res, folios: f, audit: a}
}

// historyLimit caps each source of the history of a day: a room has a handful of events a day, and a page of hundreds is not a panel.
const historyLimit = 30

// denied reports whether the caller was refused a part: that part is then left out, and the others still answer.
func denied(err error) bool { return apperr.IsCode(err, "PERMISSION_DENIED") }

// Get returns the panel of a room on the open business date. The room is read the way the room board reads it (any caller with access to the property); every other part needs the
// permission its own module asks for, and is left out without it. The parts are read in one READ ONLY, REPEATABLE READ transaction, so they describe the same moment, and nothing
// is locked. A room of another property or tenant is 404 ROOM_NOT_FOUND.
func (s *Service) Get(ctx context.Context, propertyID, roomID int64) (Summary, error) {
	var out Summary
	err := s.txm.WithinReadTx(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.build(ctx, propertyID, roomID)
		return err
	})
	return out, err
}

func (s *Service) build(ctx context.Context, propertyID, roomID int64) (Summary, error) {
	board, err := s.housekeeping.Board(ctx, propertyID, housekeeping.BoardFilter{RoomID: &roomID})
	if err != nil {
		return Summary{}, err
	}
	if len(board) == 0 {
		return Summary{}, apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Room: board[0], BusinessDate: day.BusinessDate, History: []HistoryItem{}}

	stay, err := s.stayPart(ctx, propertyID, roomID, day.BusinessDate)
	switch {
	case err == nil:
		out.Access.Stay, out.Stay = true, stay
	case !denied(err):
		return Summary{}, err
	}

	if out.Stay != nil {
		fol, err := s.folioPart(ctx, propertyID, out.Stay.ID)
		switch {
		case err == nil:
			out.Access.Folio, out.Folio = true, fol
		case !denied(err):
			return Summary{}, err
		}
	} else if out.Access.Stay {
		// nobody is in the room: there is no folio to show, whatever the caller may read
		out.Access.Folio = true
		arrival, err := s.frontdesk.NextArrivalOfRoom(ctx, propertyID, roomID, day.BusinessDate)
		switch {
		case err == nil:
			out.Access.Arrival, out.NextArrival = true, arrival
		case !denied(err):
			return Summary{}, err
		}
	}

	history, canAudit, err := s.historyPart(ctx, propertyID, roomID, out.Stay, day.BusinessDate)
	if err != nil {
		return Summary{}, err
	}
	out.Access.Audit, out.History = canAudit, history
	return out, nil
}

// stayPart is the OPEN stay whose open segment is in the room (nil when the room is empty): its guest and dates from the front desk, the special request and the source from the reservation.
func (s *Service) stayPart(ctx context.Context, propertyID, roomID int64, bd civil.Date) (*Stay, error) {
	list, err := s.frontdesk.ListStays(ctx, propertyID, frontdesk.StayFilter{Status: "OPEN", RoomID: &roomID}, 0, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	st := list[0]
	out := &Stay{
		ID: st.ID, StayNumber: st.StayNumber, Guest: Guest{ID: st.GuestID, Name: st.GuestName}, ReservationID: st.ReservationID, ConfirmationNumber: st.ConfirmationNumber,
		ArrivalDate: st.ArrivalDate, DepartureDate: st.DepartureDate, Nights: st.ArrivalDate.DaysUntil(st.DepartureDate), AdultCount: st.AdultCount, ChildCount: st.ChildCount,
	}
	out.Night = nightOf(st.ArrivalDate, out.Nights, bd)
	res, err := s.reservations.Get(ctx, propertyID, st.ReservationID)
	if err != nil {
		return nil, err
	}
	out.SpecialRequest, out.Source = res.SpecialRequest, res.Source
	return out, nil
}

// nightOf is the night a stay of nights nights that arrived on arrival is in on the date: 1 on the arrival date, and never outside 1..nights (a guest who stays past the departure date
// is still in the last night, and a date before the arrival, which a stay cannot have, is the first).
func nightOf(arrival civil.Date, nights int, on civil.Date) int {
	return max(1, min(arrival.DaysUntil(on)+1, max(nights, 1)))
}

// folioPart sums the folios of the stay with the totals of the folio service (nil when the stay has no folio).
func (s *Service) folioPart(ctx context.Context, propertyID, stayID int64) (*Folio, error) {
	list, err := s.folios.StayFolioTotals(ctx, propertyID, stayID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var charges, paid, balance decimal.Decimal
	refs := make([]FolioRef, len(list))
	for i, f := range list {
		for _, p := range []struct {
			into *decimal.Decimal
			from string
		}{{&charges, f.Debit}, {&paid, f.Credit}, {&balance, f.Balance}} {
			d, err := decimal.NewFromString(p.from)
			if err != nil {
				return nil, err
			}
			*p.into = p.into.Add(d)
		}
		refs[i] = FolioRef{ID: f.ID, FolioNumber: f.FolioNumber, FolioType: f.FolioType, Status: f.Status, Balance: f.Balance}
	}
	places := prop.CurrencyDecimals
	return &Folio{Charges: charges.StringFixed(places), Paid: paid.StringFixed(places), Balance: balance.StringFixed(places), Folios: refs}, nil
}

type entity struct {
	kind string
	id   int64
}

// historyPart is what happened to the room on the business date, newest first: the changes of cleaning (any caller), and the audit entries of the room and of the stay in it
// (audit.read; without it the flag is false and only the cleaning changes are listed). The audit entry of a cleaning change is left out, the change is listed once.
func (s *Service) historyPart(ctx context.Context, propertyID, roomID int64, stay *Stay, bd civil.Date) ([]HistoryItem, bool, error) {
	items := []HistoryItem{}
	logs, err := s.housekeeping.Logs(ctx, propertyID, roomID, nil, historyLimit)
	if err != nil {
		return nil, false, err
	}
	for _, l := range logs {
		if l.BusinessDate.Equal(bd) {
			items = append(items, HistoryItem{At: l.ChangedAt, Kind: "CLEANING", From: l.FromStatus, To: l.ToStatus, Notes: l.Notes})
		}
	}
	canAudit := true
	entities := []entity{{"room", roomID}}
	if stay != nil {
		entities = append(entities, entity{"stay", stay.ID})
	}
	for _, e := range entities {
		id := e.id
		recs, err := s.audit.Search(ctx, auditlog.Filter{PropertyID: &propertyID, EntityType: e.kind, EntityID: &id, From: &bd, To: &bd}, 0, historyLimit)
		if denied(err) {
			canAudit = false
			break
		}
		if err != nil {
			return nil, false, err
		}
		for _, r := range recs {
			if r.Action == "housekeeping.changed" {
				continue
			}
			it := HistoryItem{At: r.CreatedAt, Kind: "AUDIT", Action: r.Action, EntityType: r.EntityType}
			if r.EntityLabel != nil {
				it.Label = *r.EntityLabel
			}
			if r.User != nil {
				it.User = r.User.Name
			}
			items = append(items, it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
	if len(items) > historyLimit {
		items = items[:historyLimit]
	}
	return items, canAudit, nil
}
