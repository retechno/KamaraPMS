package reservations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations/reservationsdb"
)

// The cancellation of the rooms of a group in one request (docs/architecture/30-group-folio.md part F). Each reservation is cancelled in a transaction of its own, in ascending order of id, and the
// answer says what became of each: nothing is rolled back for the others, and a repeat of the request finds what the first one did (a reservation that is cancelled answers ALREADY_CANCELLED, whatever
// version the repeat names). The cancellation never moves money: the deposits that a cancelled reservation holds are listed, and what to do with each (keep it, refund it, turn it into the fee of the
// cancellation) is decided by a person, one reservation at a time, with the refund and the fee that exist.

const maxGroupCancelRows = 100

// GroupCancelRow names a reservation of the group with the version the person saw.
type GroupCancelRow struct {
	ReservationID int64 `json:"reservation_id"`
	Version       int32 `json:"version"`
}

// GroupCancelInput is the request: one reason for all, and the reservations.
type GroupCancelInput struct {
	Reason string           `json:"reason"`
	Rows   []GroupCancelRow `json:"rows"`
}

// GroupReinstateInput is the request to bring cancelled reservations back.
type GroupReinstateInput struct {
	Rows []GroupCancelRow `json:"rows"`
}

// Outcomes of a row and of a room.
const (
	RowCancelled        = "CANCELLED"        // every room of the reservation is cancelled now
	RowPartlyCancelled  = "PARTLY_CANCELLED" // the rooms that were waiting are cancelled, those checked in stay
	RowAlreadyCancelled = "ALREADY_CANCELLED"
	RowSkipped          = "SKIPPED" // nothing to cancel: every room is checked in, checked out or a no-show
	RowFailed           = "FAILED"
	RowReinstated       = "REINSTATED"
)

// GroupCancelRoom is what became of a room of the reservation.
type GroupCancelRoom struct {
	LineID int64  `json:"line_id"`
	Status string `json:"status"` // CANCELLED, ALREADY_CANCELLED or SKIPPED
	Code   string `json:"code,omitempty"`
}

// HeldDeposit is a deposit the reservation still holds, which a person may refund.
type HeldDeposit struct {
	PaymentID     int64  `json:"payment_id"`
	PaymentNumber string `json:"payment_number"`
	PaymentMethod string `json:"payment_method"`
	Amount        string `json:"amount"`
	Refundable    string `json:"refundable"`
	FolioID       int64  `json:"folio_id"`
	FolioNumber   string `json:"folio_number"`
}

// GroupCancelRowResult is what happened to one reservation.
type GroupCancelRowResult struct {
	ReservationID      int64             `json:"reservation_id"`
	ConfirmationNumber string            `json:"confirmation_number,omitempty"`
	Status             string            `json:"status"`
	Code               string            `json:"code,omitempty"`
	Message            string            `json:"message,omitempty"`
	Rooms              []GroupCancelRoom `json:"rooms,omitempty"`
	// Deposits are the deposits a reservation that was cancelled still holds and that can be refunded.
	Deposits []HeldDeposit `json:"deposits,omitempty"`
}

// GroupCancelResult answers a request that was understood, whatever the rows did. For a reinstatement the counters are those of the reservations that changed (Cancelled), were skipped and failed.
type GroupCancelResult struct {
	BatchID          string                 `json:"batch_id"`
	Rows             []GroupCancelRowResult `json:"rows"`
	Cancelled        int                    `json:"cancelled"`
	PartlyCancelled  int                    `json:"partly_cancelled"`
	AlreadyCancelled int                    `json:"already_cancelled"`
	Skipped          int                    `json:"skipped"`
	Failed           int                    `json:"failed"`
	RoomsCancelled   int                    `json:"rooms_cancelled"`
	RoomsSkipped     int                    `json:"rooms_skipped"`
	DepositsHeld     int                    `json:"deposits_held"`
}

func withBatchID(m map[string]any, batch string) map[string]any {
	if batch != "" {
		m["batch_id"] = batch
	}
	return m
}

// failure is the code and the message of an error, as a row says them.
func failure(err error) (code, msg string) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code, ae.Message
	}
	return "INTERNAL", "the reservation could not be changed"
}

// checkGroupRows validates a request that names reservations: how many, and each once. It returns them in ascending order of id, the order every set of rows is taken in.
func checkGroupRows(key string, rows []GroupCancelRow) ([]GroupCancelRow, error) {
	if key == "" || len(key) > 100 {
		return nil, apperr.Invalid("the request is invalid", fieldErr("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	switch {
	case len(rows) == 0:
		return nil, apperr.New(apperr.KindInvalid, "GROUP_CANCEL_EMPTY", "the request names no reservation")
	case len(rows) > maxGroupCancelRows:
		return nil, apperr.New(apperr.KindInvalid, "TOO_MANY_ROWS", fmt.Sprintf("the request names more than %d reservations", maxGroupCancelRows))
	}
	seen := map[int64]bool{}
	for i, r := range rows {
		if r.ReservationID < 1 || seen[r.ReservationID] {
			return nil, apperr.Invalid("the request is invalid", fieldErr(fmt.Sprintf("rows[%d].reservation_id", i), "INVALID_VALUE", "a reservation of the group, named once"))
		}
		seen[r.ReservationID] = true
		if r.Version < 1 {
			return nil, apperr.Invalid("the request is invalid", fieldErr(fmt.Sprintf("rows[%d].version", i), "REQUIRED", "the version of the reservation"))
		}
	}
	out := append([]GroupCancelRow(nil), rows...)
	sort.Slice(out, func(i, j int) bool { return out[i].ReservationID < out[j].ReservationID })
	return out, nil
}

// GroupCancel cancels the rooms of the reservations the request names (group.cancel, checked for the whole request: 403 and nothing is done without it). A room that is checked in, checked out or
// a no-show is skipped; a reservation with some rooms in house has only its waiting rooms cancelled (room by room, as CancelLine does) and stays; a reservation with none in house is cancelled whole.
func (s *Service) GroupCancel(ctx context.Context, propertyID, groupID int64, key string, in GroupCancelInput) (GroupCancelResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermGroupCancel)
	if err != nil {
		return GroupCancelResult{}, err
	}
	reason, err := requireReason(strings.TrimSpace(in.Reason))
	if err != nil {
		return GroupCancelResult{}, err
	}
	rows, err := checkGroupRows(key, in.Rows)
	if err != nil {
		return GroupCancelResult{}, err
	}
	group, err := s.q(ctx).GroupBrief(ctx, reservationsdb.GroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupCancelResult{}, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
		}
		return GroupCancelResult{}, err
	}
	out := GroupCancelResult{BatchID: key, Rows: make([]GroupCancelRowResult, 0, len(rows))}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res := s.groupCancelRow(ctx, p, propertyID, groupID, key, reason, r)
		switch res.Status {
		case RowCancelled:
			out.Cancelled++
		case RowPartlyCancelled:
			out.PartlyCancelled++
		case RowAlreadyCancelled:
			out.AlreadyCancelled++
		case RowSkipped:
			out.Skipped++
		default:
			out.Failed++
		}
		for _, room := range res.Rooms {
			switch room.Status {
			case RowCancelled:
				out.RoomsCancelled++
			case RowSkipped:
				out.RoomsSkipped++
			}
		}
		out.Rows = append(out.Rows, res)
	}
	s.fillHeld(ctx, p.TenantID, propertyID, &out)
	for _, r := range out.Rows {
		if len(r.Deposits) > 0 {
			out.DepositsHeld++
		}
	}
	if out.RoomsCancelled > 0 { // one entry for the request; the entries of the reservations carry the same batch_id
		err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
			if err != nil {
				return err
			}
			return s.audit.Write(ctx, audit.Entry{
				TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(), Action: "group.cancelled", EntityType: "booking_group", EntityID: group.ID, EntityLabel: group.Code,
				New: map[string]any{
					"batch_id": key, "reason": reason, "reservations": len(rows), "cancelled": out.Cancelled, "partly_cancelled": out.PartlyCancelled, "already_cancelled": out.AlreadyCancelled,
					"skipped": out.Skipped, "failed": out.Failed, "rooms_cancelled": out.RoomsCancelled, "rooms_skipped": out.RoomsSkipped, "deposits_held": out.DepositsHeld,
				},
			})
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// fillHeld adds to the reservations that were cancelled the deposits they still hold and that can be refunded.
func (s *Service) fillHeld(ctx context.Context, tenantID, propertyID int64, out *GroupCancelResult) {
	var ids []int64
	for _, r := range out.Rows {
		if r.Status == RowCancelled || r.Status == RowAlreadyCancelled {
			ids = append(ids, r.ReservationID)
		}
	}
	if len(ids) == 0 {
		return
	}
	held, err := s.q(ctx).ListHeldDeposits(ctx, reservationsdb.ListHeldDepositsParams{TenantID: tenantID, PropertyID: propertyID, ReservationIds: ids})
	if err != nil {
		return // the cancellation is done; the group page reads what is held again
	}
	byRes := map[int64][]HeldDeposit{}
	for _, h := range held {
		if h.ReservationID == nil {
			continue
		}
		byRes[*h.ReservationID] = append(byRes[*h.ReservationID], HeldDeposit{
			PaymentID: h.PaymentID, PaymentNumber: h.PaymentNumber, PaymentMethod: h.PaymentMethod, Amount: h.Amount.String(), Refundable: h.Refundable.String(), FolioID: h.FolioID, FolioNumber: h.FolioNumber,
		})
	}
	for i := range out.Rows {
		out.Rows[i].Deposits = byRes[out.Rows[i].ReservationID]
	}
}

// classifyRooms says what a cancellation of the group does with each room of a reservation: the rooms that wait (draft or confirmed) are cancelled, the rooms in house or done and the no-shows are skipped,
// the rooms already cancelled are told so. It returns how many rooms wait and whether one is in house or done.
func classifyRooms(lines []reservationsdb.ReservationRoom) (rooms []GroupCancelRoom, waiting int, inHouse bool) {
	for _, l := range lines {
		switch l.Status {
		case LineDraft, LineConfirmed:
			waiting++
		case LineCheckedIn, LineCompleted:
			inHouse = true
			rooms = append(rooms, GroupCancelRoom{LineID: l.ID, Status: RowSkipped, Code: "ALREADY_CHECKED_IN"})
		case LineNoShow:
			rooms = append(rooms, GroupCancelRoom{LineID: l.ID, Status: RowSkipped, Code: "NO_SHOW"})
		default:
			rooms = append(rooms, GroupCancelRoom{LineID: l.ID, Status: RowAlreadyCancelled})
		}
	}
	return rooms, waiting, inHouse
}

// groupCancelRow does one reservation, in a transaction of its own.
func (s *Service) groupCancelRow(ctx context.Context, p auth.Principal, propertyID, groupID int64, batch, reason string, r GroupCancelRow) GroupCancelRowResult {
	res := GroupCancelRowResult{ReservationID: r.ReservationID, Status: RowFailed}
	q := s.q(ctx)
	pre, err := q.GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.ReservationID})
	if err != nil {
		res.Code, res.Message = failure(orNotFound(err, errNotFound()))
		return res
	}
	res.ConfirmationNumber = pre.ConfirmationNumber
	if pre.BookingGroupID == nil || *pre.BookingGroupID != groupID {
		res.Code, res.Message = "RESERVATION_NOT_IN_GROUP", "the reservation is not a reservation of this group"
		return res
	}
	if pre.Status == StatusCancelled { // done by an earlier request (or by someone else): the repeat is answered as it was, whatever version it names
		res.Status = RowAlreadyCancelled
		if lines, err := q.ListLines(ctx, reservationsdb.ListLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: r.ReservationID}); err == nil {
			for _, l := range lines {
				res.Rooms = append(res.Rooms, GroupCancelRoom{LineID: l.ID, Status: RowAlreadyCancelled})
			}
		}
		return res
	}
	// nothing waits any more (a repeat of a request that went through, or rooms that are all in house): the answer is read, with no lock and whatever version the request names
	if lines, err := q.ListLines(ctx, reservationsdb.ListLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: r.ReservationID}); err == nil {
		if rooms, waiting, inHouse := classifyRooms(lines); waiting == 0 {
			res.Status, res.Rooms, res.Code = RowSkipped, rooms, "ALREADY_CHECKED_IN"
			if !inHouse {
				res.Code = "NO_SHOW"
			}
			return res
		}
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		res.Rooms = nil
		st, err := s.lock(ctx, p.TenantID, propertyID, r.ReservationID, r.Version, lockOpts{})
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		now := s.clock.Now()
		var toCancel []reservationsdb.ReservationRoom
		inHouse := false
		for _, l := range st.lines {
			switch l.Status {
			case LineDraft, LineConfirmed:
				toCancel = append(toCancel, l)
			case LineCheckedIn, LineCompleted:
				inHouse = true
				res.Rooms = append(res.Rooms, GroupCancelRoom{LineID: l.ID, Status: RowSkipped, Code: "ALREADY_CHECKED_IN"})
			case LineNoShow:
				res.Rooms = append(res.Rooms, GroupCancelRoom{LineID: l.ID, Status: RowSkipped, Code: "NO_SHOW"})
			default:
				res.Rooms = append(res.Rooms, GroupCancelRoom{LineID: l.ID, Status: RowAlreadyCancelled})
			}
		}
		if len(toCancel) == 0 {
			res.Status, res.Code = RowSkipped, "ALREADY_CHECKED_IN"
			if !inHouse {
				res.Code = "NO_SHOW"
			}
			return nil
		}
		for _, l := range toCancel {
			l.Status, l.CancelledAt, l.CancelledBy, l.CancellationReason, l.RoomID = LineCancelled, &now, p.ActorID(), &reason, nil
			if _, err := s.saveLine(ctx, p, propertyID, l); err != nil {
				return err
			}
			res.Rooms = append(res.Rooms, GroupCancelRoom{LineID: l.ID, Status: RowCancelled})
		}
		sort.Slice(res.Rooms, func(i, j int) bool { return res.Rooms[i].LineID < res.Rooms[j].LineID })
		label := auditlabel.Reservation(ctx, propertyID, r.ReservationID)
		if inHouse { // a room is in house: the reservation stays and only the waiting rooms are cancelled, with one entry for each as CancelLine writes it
			if _, err := s.bump(ctx, p, propertyID, r.ReservationID); err != nil {
				return err
			}
			for _, l := range toCancel {
				if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_cancelled", r.ReservationID, label,
					map[string]any{"reservation_room_id": l.ID, "status": LineConfirmed}, withBatchID(map[string]any{"status": LineCancelled, "reason": reason, "reservation_cancelled": false}, batch))); err != nil {
					return err
				}
			}
			res.Status = RowPartlyCancelled
			return nil
		}
		header, err := s.q(ctx).CancelReservation(ctx, reservationsdb.CancelReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.ReservationID, Now: now, Reason: &reason, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		res.Status = RowCancelled
		return s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.cancelled", r.ReservationID, label,
			map[string]any{"status": st.res.Status}, withBatchID(map[string]any{"status": header.Status, "reason": reason, "rooms_cancelled": len(toCancel)}, batch)))
	})
	if err != nil {
		res.Status, res.Rooms = RowFailed, nil
		res.Code, res.Message = failure(err)
	}
	return res
}

// GroupReinstate brings the cancelled reservations the request names back (group.cancel, checked for the whole request: it is the same mass action, undone, and a person who may cancel a group may bring it
// back; reservation.reinstate, the right of one reservation, is not asked for each row). A reservation whose rooms were taken meanwhile is a failed row with
// the code ROOM_UNAVAILABLE; a reservation that is not cancelled is skipped (NOT_CANCELLED). The counter Cancelled of the answer counts the reservations that were reinstated.
func (s *Service) GroupReinstate(ctx context.Context, propertyID, groupID int64, key string, in GroupReinstateInput) (GroupCancelResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermGroupCancel)
	if err != nil {
		return GroupCancelResult{}, err
	}
	rows, err := checkGroupRows(key, in.Rows)
	if err != nil {
		return GroupCancelResult{}, err
	}
	group, err := s.q(ctx).GroupBrief(ctx, reservationsdb.GroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupCancelResult{}, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
		}
		return GroupCancelResult{}, err
	}
	out := GroupCancelResult{BatchID: key, Rows: make([]GroupCancelRowResult, 0, len(rows))}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res := s.groupReinstateRow(ctx, p, propertyID, groupID, key, r)
		switch res.Status {
		case RowReinstated:
			out.Cancelled++
		case RowSkipped:
			out.Skipped++
		default:
			out.Failed++
		}
		out.Rows = append(out.Rows, res)
	}
	if out.Cancelled > 0 {
		err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
			if err != nil {
				return err
			}
			return s.audit.Write(ctx, audit.Entry{
				TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(), Action: "group.reinstated", EntityType: "booking_group", EntityID: group.ID, EntityLabel: group.Code,
				New: map[string]any{"batch_id": key, "reservations": len(rows), "reinstated": out.Cancelled, "skipped": out.Skipped, "failed": out.Failed},
			})
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// roomGone is the code of an availability error: the room type, the bed or the room is not free any more for the nights of a reservation that is brought back.
func roomGone(code string) bool {
	return code == "ROOM_TYPE_NOT_AVAILABLE" || code == "BED_NOT_AVAILABLE" || code == "ROOM_NOT_AVAILABLE"
}

func (s *Service) groupReinstateRow(ctx context.Context, p auth.Principal, propertyID, groupID int64, batch string, r GroupCancelRow) GroupCancelRowResult {
	res := GroupCancelRowResult{ReservationID: r.ReservationID, Status: RowFailed}
	pre, err := s.q(ctx).GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.ReservationID})
	if err != nil {
		res.Code, res.Message = failure(orNotFound(err, errNotFound()))
		return res
	}
	res.ConfirmationNumber = pre.ConfirmationNumber
	if pre.BookingGroupID == nil || *pre.BookingGroupID != groupID {
		res.Code, res.Message = "RESERVATION_NOT_IN_GROUP", "the reservation is not a reservation of this group"
		return res
	}
	if pre.Status != StatusCancelled {
		res.Status, res.Code, res.Message = RowSkipped, "NOT_CANCELLED", "the reservation is not cancelled"
		return res
	}
	if _, err := s.reinstate(ctx, p, propertyID, r.ReservationID, r.Version, batch); err != nil {
		res.Code, res.Message = failure(err)
		if roomGone(res.Code) {
			res.Message = "the rooms of the reservation were taken since it was cancelled (" + res.Code + ")"
			res.Code = "ROOM_UNAVAILABLE"
		}
		return res
	}
	res.Status = RowReinstated
	return res
}

// GroupHolding is a reservation of a group that has no live room and still holds something: what it holds, and the deposits that can be refunded.
type GroupHolding struct {
	ReservationID      int64         `json:"reservation_id"`
	ConfirmationNumber string        `json:"confirmation_number"`
	GuestName          string        `json:"guest_name,omitempty"`
	HeldBalance        string        `json:"held_balance"`
	Deposits           []HeldDeposit `json:"deposits"`
}

// GroupHoldings lists what the cancelled reservations of a group still hold (folio.read): the follow-up of a cancellation, as long as a deposit is kept or a fee is open.
func (s *Service) GroupHoldings(ctx context.Context, propertyID, groupID int64) ([]GroupHolding, error) {
	p, err := s.writer(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return nil, err
	}
	if _, err := s.q(ctx).GroupBrief(ctx, reservationsdb.GroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
		}
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListGroupHoldings(ctx, reservationsdb.ListGroupHoldingsParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ReservationID
	}
	held, err := s.q(ctx).ListHeldDeposits(ctx, reservationsdb.ListHeldDepositsParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationIds: ids})
	if err != nil {
		return nil, err
	}
	byRes := map[int64][]HeldDeposit{}
	for _, h := range held {
		if h.ReservationID == nil {
			continue
		}
		byRes[*h.ReservationID] = append(byRes[*h.ReservationID], HeldDeposit{
			PaymentID: h.PaymentID, PaymentNumber: h.PaymentNumber, PaymentMethod: h.PaymentMethod, Amount: h.Amount.String(), Refundable: h.Refundable.String(), FolioID: h.FolioID, FolioNumber: h.FolioNumber,
		})
	}
	out := make([]GroupHolding, len(rows))
	for i, r := range rows {
		deps := byRes[r.ReservationID]
		if deps == nil {
			deps = []HeldDeposit{}
		}
		out[i] = GroupHolding{ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber, GuestName: r.GuestName, HeldBalance: r.HeldBalance.StringFixed(decimals), Deposits: deps}
	}
	return out, nil
}
