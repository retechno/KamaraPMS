package frontdesk

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk/frontdeskdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Group check-in (docs/architecture/30-group-folio.md 6.2, 6.4, 14): the rooms of a group that arrive today are checked in by one request, each in its own transaction (the check-in locks the room
// type, the room, the reservation and the folio, and takes a stay number: a second one in the same transaction would take a lower lock after a higher one). The unit of success is the row.
//
// Every key a row uses is made from the LINE, never from the request: the check-in is `gci#<line>` and the deposit `gci#<line>#dep`. The screen repeats the rows that failed in a new request with a
// new Idempotency-Key, and that must find what the first request did, not do it again: the stay of a room that was checked in is the same stay, and a deposit that was taken is not taken twice. The
// Idempotency-Key of the request is the id of the batch (batch_id of every audit entry the request writes). The same deposit key with another amount or method is IDEMPOTENCY_KEY_REUSED, shown on the
// row: the answer is never the old one in silence.

// maxGroupCheckInRows is the most rooms one request names.
const maxGroupCheckInRows = 100

// GroupDepositInput is the deposit taken for a room before its check-in.
type GroupDepositInput struct {
	Amount          string `json:"amount"`
	PaymentMethod   string `json:"payment_method"`
	ReferenceNumber string `json:"reference_number"`
}

// GroupCheckInRow is one room of the request.
type GroupCheckInRow struct {
	LineID               int64              `json:"line_id"`
	Version              int32              `json:"version"`
	RoomID               *int64             `json:"room_id"`
	GuestID              int64              `json:"guest_id"`
	AdultCount           int                `json:"adult_count"`
	ChildCount           int                `json:"child_count"`
	OverrideRoomNotReady bool               `json:"override_room_not_ready"`
	OverrideReason       string             `json:"override_reason"`
	Deposit              *GroupDepositInput `json:"deposit"`
}

// GroupCheckInInput is the request.
type GroupCheckInInput struct {
	Rows []GroupCheckInRow `json:"rows"`
}

// Outcomes of a row.
const (
	RowCheckedIn        = "CHECKED_IN"
	RowAlreadyCheckedIn = "ALREADY_CHECKED_IN"
	RowFailed           = "FAILED"
)

// GroupDepositResult says what became of the deposit of a row: RECORDED (taken now), REPLAYED (taken by an earlier request, not taken again) or FAILED.
type GroupDepositResult struct {
	Status        string `json:"status"`
	PaymentID     int64  `json:"payment_id,omitempty"`
	PaymentNumber string `json:"payment_number,omitempty"`
	Amount        string `json:"amount,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}

// GroupCheckInRowResult is what happened to one room.
type GroupCheckInRowResult struct {
	LineID  int64               `json:"line_id"`
	Status  string              `json:"status"`
	Stay    *Stay               `json:"stay,omitempty"`
	StayID  int64               `json:"stay_id,omitempty"`
	Room    string              `json:"room_number,omitempty"`
	Code    string              `json:"code,omitempty"`
	Message string              `json:"message,omitempty"`
	Deposit *GroupDepositResult `json:"deposit,omitempty"`
}

// GroupCheckInResult answers a request that was understood, whatever the rows did.
type GroupCheckInResult struct {
	BatchID          string                  `json:"batch_id"`
	Rows             []GroupCheckInRowResult `json:"rows"`
	CheckedIn        int                     `json:"checked_in"`
	AlreadyCheckedIn int                     `json:"already_checked_in"`
	Failed           int                     `json:"failed"`
	DepositsRecorded int                     `json:"deposits_recorded"`
}

func groupCheckInKey(line int64) string { return fmt.Sprintf("gci#%d", line) }
func groupDepositKey(line int64) string { return fmt.Sprintf("gci#%d#dep", line) }

// failure is the code and the message of an error, as a row says them.
func failure(err error) (code, msg string) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code, ae.Message
	}
	return "INTERNAL", "the room could not be checked in"
}

// GroupCheckIn checks in the rooms the request names (frontdesk.checkin; payment.post when a row has a deposit, checked for the whole request before anything is done: 403 and nothing is written).
func (s *Service) GroupCheckIn(ctx context.Context, propertyID, groupID int64, key string, in GroupCheckInInput) (GroupCheckInResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskCheckin)
	if err != nil {
		return GroupCheckInResult{}, err
	}
	if key == "" || len(key) > 100 {
		return GroupCheckInResult{}, apperr.Invalid("the check-in is invalid", fieldErr("Idempotency-Key", "INVALID_VALUE", "1 to 100 characters"))
	}
	switch {
	case len(in.Rows) == 0:
		return GroupCheckInResult{}, apperr.New(apperr.KindInvalid, "GROUP_CHECKIN_EMPTY", "the check-in names no room")
	case len(in.Rows) > maxGroupCheckInRows:
		return GroupCheckInResult{}, apperr.New(apperr.KindInvalid, "TOO_MANY_ROWS", fmt.Sprintf("the check-in names more than %d rooms", maxGroupCheckInRows))
	}
	seen := map[int64]bool{}
	deposits := false
	for i, r := range in.Rows {
		if r.LineID < 1 || seen[r.LineID] {
			return GroupCheckInResult{}, apperr.Invalid("the check-in is invalid", fieldErr(fmt.Sprintf("rows[%d].line_id", i), "INVALID_VALUE", "a room of the group, named once"))
		}
		seen[r.LineID] = true
		deposits = deposits || r.Deposit != nil
	}
	if deposits { // the whole request is refused up front: a deposit is money, and half a request is not what the person approved
		if err := s.authz.Require(ctx, propertyID, auth.PermPaymentPost); err != nil {
			return GroupCheckInResult{}, err
		}
	}
	q := s.q(ctx)
	group, err := q.GetGroupBrief(ctx, frontdeskdb.GetGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupCheckInResult{}, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
		}
		return GroupCheckInResult{}, err
	}
	rows := append([]GroupCheckInRow(nil), in.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].LineID < rows[j].LineID }) // ascending, like every set of rows that is taken in turn
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.LineID
	}
	lines, err := q.GroupCheckInLines(ctx, frontdeskdb.GroupCheckInLinesParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID, LineIds: ids})
	if err != nil {
		return GroupCheckInResult{}, err
	}
	lineOf := make(map[int64]frontdeskdb.GroupCheckInLinesRow, len(lines))
	for _, l := range lines {
		lineOf[l.LineID] = l
	}
	out := GroupCheckInResult{BatchID: key, Rows: make([]GroupCheckInRowResult, 0, len(rows))}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res := s.groupCheckInRow(ctx, p, propertyID, key, r, lineOf)
		switch res.Status {
		case RowCheckedIn:
			out.CheckedIn++
		case RowAlreadyCheckedIn:
			out.AlreadyCheckedIn++
		default:
			out.Failed++
		}
		if res.Deposit != nil && res.Deposit.Status == "RECORDED" {
			out.DepositsRecorded++
		}
		out.Rows = append(out.Rows, res)
	}
	if out.CheckedIn+out.DepositsRecorded > 0 { // one entry for the request, with its batch: the entries of the rooms and of the deposits carry the same batch_id
		err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
			if err != nil {
				return err
			}
			return s.audit.Write(ctx, audit.Entry{
				TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(), Action: "group.checked_in", EntityType: "booking_group", EntityID: group.ID, EntityLabel: group.Code,
				New: map[string]any{"batch_id": key, "rows": len(rows), "checked_in": out.CheckedIn, "already_checked_in": out.AlreadyCheckedIn, "failed": out.Failed, "deposits_recorded": out.DepositsRecorded},
			})
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// groupCheckInRow does one room: the deposit first (its own transaction), then the check-in (its own transaction), as the check-in panel does for one room. A deposit that was taken and a check-in that
// failed leaves the money safe on the reservation, an advance deposit that can be refunded; a deposit that failed leaves the room not checked in.
func (s *Service) groupCheckInRow(ctx context.Context, p auth.Principal, propertyID int64, batch string, r GroupCheckInRow, lineOf map[int64]frontdeskdb.GroupCheckInLinesRow) GroupCheckInRowResult {
	res := GroupCheckInRowResult{LineID: r.LineID, Status: RowFailed}
	line, ok := lineOf[r.LineID]
	if !ok {
		res.Code, res.Message = "LINE_NOT_IN_GROUP", "the room is not a room of this group"
		return res
	}
	key := groupCheckInKey(r.LineID)
	in := CheckInInput{Version: r.Version, RoomID: r.RoomID, GuestID: r.GuestID, AdultCount: r.AdultCount, ChildCount: r.ChildCount, OverrideRoomNotReady: r.OverrideRoomNotReady, OverrideReason: r.OverrideReason}
	if line.LineStatus != "CONFIRMED" {
		// checked in already, by an earlier request (the same stay is answered) or by someone else (the stay it has)
		if line.StayID != 0 {
			if got, found, err := s.replay(ctx, p, propertyID, key, r.LineID); err == nil && found {
				res.Status, res.Stay, res.Room = RowCheckedIn, &got.Stay, got.StayRoom.RoomNumber
				return res
			}
			res.Status, res.StayID = RowAlreadyCheckedIn, line.StayID
			return res
		}
		res.Code, res.Message = "LINE_NOT_CONFIRMED", "only a confirmed room can be checked in"
		return res
	}
	if r.Deposit != nil {
		dep, replayed, err := s.folios.DepositInBatch(ctx, propertyID, line.ReservationID, groupDepositKey(r.LineID), folios.PaymentInput{
			Amount: r.Deposit.Amount, PaymentMethod: r.Deposit.PaymentMethod, ReferenceNumber: r.Deposit.ReferenceNumber,
		}, batch)
		if err != nil {
			code, msg := failure(err)
			res.Deposit = &GroupDepositResult{Status: RowFailed, Code: code, Message: msg}
			res.Code, res.Message = code, msg
			return res
		}
		status := "RECORDED"
		if replayed {
			status = "REPLAYED"
		}
		res.Deposit = &GroupDepositResult{Status: status, PaymentID: dep.Payment.ID, PaymentNumber: dep.Payment.PaymentNumber, Amount: dep.Payment.Amount}
	}
	done, err := s.checkIn(ctx, p, propertyID, line.ReservationID, r.LineID, key, in, batch)
	if err != nil {
		res.Code, res.Message = failure(err)
		return res
	}
	res.Status, res.Stay, res.Room = RowCheckedIn, &done.Stay, done.StayRoom.RoomNumber
	return res
}
