package housekeeping

import (
	"context"
	"slices"
	"strings"

	"kamarapms/internal/housekeeping/housekeepingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// MaxBulkRooms is how many rooms one bulk change takes.
const MaxBulkRooms = 100

// BulkInput is a manual status change of several rooms to one status.
type BulkInput struct {
	RoomIDs []int64 `json:"room_ids"`
	Status  Status  `json:"status"`
	Notes   string  `json:"notes"`
}

// BulkError says why a room was left as it was: the code of the refusal that the single change would have given.
type BulkError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Context map[string]any `json:"context,omitempty"`
}

// BulkResult is what became of one room.
type BulkResult struct {
	RoomID     int64      `json:"room_id"`
	RoomNumber string     `json:"room_number,omitempty"`
	OK         bool       `json:"ok"`
	From       Status     `json:"from,omitempty"`
	Status     Status     `json:"status,omitempty"` // the status the room has now
	Error      *BulkError `json:"error,omitempty"`
}

// BulkSummary counts the results.
type BulkSummary struct {
	Requested int `json:"requested"`
	Changed   int `json:"changed"`
	Failed    int `json:"failed"`
}

// BulkOutcome is the answer of a bulk change: one result per room, in the order asked.
type BulkOutcome struct {
	Summary BulkSummary  `json:"summary"`
	Results []BulkResult `json:"results"`
}

// SetStatusBulk changes the housekeeping status of up to 100 rooms (POST {P}/housekeeping/bulk). It is the manual change of
// SetStatus for many rooms, with the same rule (checkTransition), the same log and the same audit entry per room that changes.
//
// It is not all-or-nothing: a room that cannot make the change (a transition that is not legal, a room that is not in the
// property) is reported with its code and the others change. The request as a whole is refused for what holds for every
// room: no permission (INSPECTED also needs housekeeping.inspect), an invalid status or list, a closed business day.
//
// One transaction: the business day is locked once (FOR SHARE), then the housekeeping rows of all the rooms in ascending room
// id (so two bulk changes, or a bulk and a single change, cannot deadlock), then each room is decided from its locked status.
// Nothing is decided before the locks are held, and a refusal writes nothing, so no savepoint is needed.
func (s *Service) SetStatusBulk(ctx context.Context, propertyID int64, in BulkInput) (BulkOutcome, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return BulkOutcome{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermHousekeepingUpdate); err != nil {
		return BulkOutcome{}, err
	}
	if in.Status == Inspected {
		if err := s.authz.Require(ctx, propertyID, auth.PermHousekeepingInspect); err != nil {
			return BulkOutcome{}, err
		}
	}
	if !in.Status.Valid() {
		return BulkOutcome{}, apperr.Invalid("the housekeeping status is invalid",
			apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "CLEAN, DIRTY, CLEANING or INSPECTED"})
	}
	notes := strings.TrimSpace(in.Notes)
	if len([]rune(notes)) > maxNotes {
		return BulkOutcome{}, apperr.Invalid("the notes are too long", apperr.FieldError{Field: "notes", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	if notes == "" {
		notes = "Bulk change"
	}
	// The rooms in the order asked, each once.
	asked := make([]int64, 0, len(in.RoomIDs))
	for _, id := range in.RoomIDs {
		if !slices.Contains(asked, id) {
			asked = append(asked, id)
		}
	}
	if len(asked) == 0 || len(asked) > MaxBulkRooms {
		return BulkOutcome{}, apperr.Invalid("the list of rooms is invalid", apperr.FieldError{Field: "room_ids", Code: "OUT_OF_RANGE", Message: "between 1 and 100 rooms"})
	}

	var out BulkOutcome
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := db.EnterLockLevel(ctx, db.LevelRooms); err != nil {
			return err
		}
		rows, err := s.q(ctx).ListRoomHousekeepingForUpdate(ctx, housekeepingdb.ListRoomHousekeepingForUpdateParams{
			TenantID: p.TenantID, PropertyID: propertyID, RoomIds: asked,
		})
		if err != nil {
			return err
		}
		locked := make(map[int64]housekeepingdb.ListRoomHousekeepingForUpdateRow, len(rows))
		for _, r := range rows {
			locked[r.RoomID] = r
		}

		out = BulkOutcome{Results: make([]BulkResult, 0, len(asked))}
		for _, id := range asked {
			row, ok := locked[id]
			if !ok {
				out.Results = append(out.Results, BulkResult{RoomID: id, Error: bulkError(roomNotFound())})
				continue
			}
			from := Status(row.Status)
			if terr := checkTransition(from, in.Status); terr != nil {
				out.Results = append(out.Results, BulkResult{RoomID: id, RoomNumber: row.RoomNumber, From: from, Status: from, Error: bulkError(terr)})
				continue
			}
			if _, err := s.apply(ctx, change{
				TenantID: p.TenantID, PropertyID: propertyID, RoomID: id, From: from, To: in.Status,
				Source: SourceManual, Notes: notes, BusinessDate: day.BusinessDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
			out.Results = append(out.Results, BulkResult{RoomID: id, RoomNumber: row.RoomNumber, OK: true, From: from, Status: in.Status})
		}
		out.Summary = BulkSummary{Requested: len(asked)}
		for _, r := range out.Results {
			if r.OK {
				out.Summary.Changed++
			} else {
				out.Summary.Failed++
			}
		}
		return nil
	})
	return out, err
}

func bulkError(e *apperr.Error) *BulkError {
	return &BulkError{Code: e.Code, Message: e.Message, Context: e.Context}
}
