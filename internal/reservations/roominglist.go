package reservations

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"kamarapms/internal/audit"
	"kamarapms/internal/guests"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations/reservationsdb"

	"context"
)

// The rooming list of a group (docs/architecture/30-group-folio.md, part A): who sleeps in each room booked for it. A row is a reservation room; what is edited is the guest of that room
// (name, e-mail, phone), in place. The guest made by a bulk booking is a placeholder ("group name · 7") until it is named. Linking a room to a guest that exists already is the
// change of the guest of a line, which the reservation screen has (`PATCH /reservations/{id}/rooms/{lineId}`).

// RoomingRow is a reservation room of the group with its guest.
type RoomingRow struct {
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	ReservationStatus  string     `json:"reservation_status"`
	Version            int32      `json:"version"`
	LineID             int64      `json:"line_id"`
	LineStatus         string     `json:"line_status"`
	RoomType           string     `json:"room_type"`
	RoomNumber         string     `json:"room_number,omitempty"`
	Arrival            civil.Date `json:"arrival_date"`
	Departure          civil.Date `json:"departure_date"`
	GuestID            *int64     `json:"guest_id"`
	Name               string     `json:"name"`
	Email              string     `json:"email"`
	Phone              string     `json:"phone"`
	IsPlaceholder      bool       `json:"is_placeholder"`
}

// RoomingEdit is the new name, e-mail and phone of the guest of a room. The whole row is sent: an empty e-mail or phone clears it.
type RoomingEdit struct {
	LineID int64  `json:"line_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Phone  string `json:"phone"`
}

// RoomingResult says what an update changed.
type RoomingResult struct {
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
}

func fullName(first, last *string) string {
	return strings.TrimSpace(deref(first) + " " + deref(last))
}

func (s *Service) roomingRows(ctx context.Context, tenantID, propertyID, groupID int64) ([]RoomingRow, error) {
	rows, err := s.q(ctx).ListRoomingRows(ctx, reservationsdb.ListRoomingRowsParams{TenantID: tenantID, PropertyID: propertyID, GroupID: &groupID})
	if err != nil {
		return nil, err
	}
	out := make([]RoomingRow, len(rows))
	for i, r := range rows {
		out[i] = RoomingRow{
			ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber, ReservationStatus: r.ReservationStatus, Version: r.ReservationVersion,
			LineID: r.LineID, LineStatus: r.LineStatus, RoomType: r.RoomTypeCode, RoomNumber: deref(r.RoomNumber), Arrival: r.ArrivalDate, Departure: r.DepartureDate,
			GuestID: r.GuestID, Name: fullName(r.FirstName, r.LastName), Email: deref(r.Email), Phone: deref(r.Phone), IsPlaceholder: r.IsPlaceholder,
		}
	}
	return out, nil
}

// RoomingList lists the rooms booked for a group with their guests (reservation.read and guest.read).
func (s *Service) RoomingList(ctx context.Context, propertyID, groupID int64) ([]RoomingRow, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return nil, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermGuestRead); err != nil {
		return nil, err
	}
	if _, err := s.q(ctx).GroupForBatch(ctx, reservationsdb.GroupForBatchParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
		return nil, orNotFound(err, errGroupNotFound())
	}
	return s.roomingRows(ctx, p.TenantID, propertyID, groupID)
}

// RoomingChange is what an edit would do to one room.
type RoomingChange struct {
	LineID             int64  `json:"line_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	FromName           string `json:"from_name"`
	ToName             string `json:"to_name"`
	FromEmail          string `json:"from_email"`
	ToEmail            string `json:"to_email"`
	FromPhone          string `json:"from_phone"`
	ToPhone            string `json:"to_phone"`
}

// RoomingProblem is a row of an edit or of a file that cannot be applied. Row is the place in the request (from 1; in a file, the line of the file).
type RoomingProblem struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// plan checks every edit against the rooms of the group and returns what would change and every problem.
func plan(rows []RoomingRow, edits []RoomingEdit, rowNumber func(i int) int, column func(field string) string) ([]RoomingChange, []RoomingProblem) {
	byLine := make(map[int64]RoomingRow, len(rows))
	for _, r := range rows {
		byLine[r.LineID] = r
	}
	var changes []RoomingChange
	var problems []RoomingProblem
	seen := map[int64]bool{}
	bad := func(i int, field, code, msg string) {
		problems = append(problems, RoomingProblem{Row: rowNumber(i), Column: column(field), Code: code, Message: msg})
	}
	for i, e := range edits {
		cur, ok := byLine[e.LineID]
		switch {
		case !ok:
			bad(i, "line_id", "NOT_IN_GROUP", "the room is not a room of this group")
			continue
		case seen[e.LineID]:
			bad(i, "line_id", "DUPLICATE", "the room is in the list twice")
			continue
		case cur.LineStatus == LineCancelled || cur.LineStatus == LineNoShow:
			bad(i, "line_id", "ROOM_NOT_ACTIVE", "the room is cancelled or a no-show")
			continue
		case cur.GuestID == nil:
			bad(i, "line_id", "NO_GUEST", "the room has no guest of its own")
			continue
		}
		seen[e.LineID] = true
		name, email, phone := strings.TrimSpace(e.Name), strings.TrimSpace(e.Email), strings.TrimSpace(e.Phone)
		first, last := splitName(name)
		if name == cur.Name { // unchanged: the placeholder "Group · 7" is not split into a first and a last name
			first, last = "", ""
		}
		probe := guests.Profile{FirstName: first, LastName: last, Email: email, Phone: phone}
		if name == cur.Name || name == "" {
			probe.LastName = "x"
		}
		if name == "" {
			bad(i, "name", "REQUIRED", "a name")
		}
		for _, f := range probe.Validate(civil.Date{}) {
			switch f.Field {
			case "email", "phone":
				bad(i, f.Field, f.Code, f.Message)
			case "last_name", "first_name":
				bad(i, "name", f.Code, f.Message)
			}
		}
		if name == cur.Name && email == cur.Email && phone == cur.Phone {
			continue
		}
		changes = append(changes, RoomingChange{LineID: e.LineID, ConfirmationNumber: cur.ConfirmationNumber, FromName: cur.Name, ToName: name, FromEmail: cur.Email, ToEmail: email, FromPhone: cur.Phone, ToPhone: phone})
	}
	return changes, problems
}

// UpdateRoomingList edits the guests of rooms of a group, all rows or none (reservation.update and guest.write).
func (s *Service) UpdateRoomingList(ctx context.Context, propertyID, groupID int64, edits []RoomingEdit) (RoomingResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return RoomingResult{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermGuestWrite); err != nil {
		return RoomingResult{}, err
	}
	if len(edits) == 0 || len(edits) > MaxBatchRooms*10 {
		return RoomingResult{}, apperr.Invalid("the rooming list is invalid", fieldErr("rows", "OUT_OF_RANGE", "between 1 and 1000 rows"))
	}
	var out RoomingResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		g, err := s.q(ctx).GroupForBatch(ctx, reservationsdb.GroupForBatchParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
		if err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		rows, err := s.roomingRows(ctx, p.TenantID, propertyID, groupID)
		if err != nil {
			return err
		}
		changes, problems := plan(rows, edits, func(i int) int { return i + 1 }, func(f string) string { return f })
		if len(problems) > 0 {
			fields := make([]apperr.FieldError, len(problems))
			for i, pr := range problems {
				fields[i] = fieldErr(fmt.Sprintf("rows[%d].%s", pr.Row-1, pr.Column), pr.Code, pr.Message)
			}
			return apperr.Invalid("the rooming list is invalid", fields...)
		}
		byLine := make(map[int64]RoomingRow, len(rows))
		for _, r := range rows {
			byLine[r.LineID] = r
		}
		for _, c := range changes {
			cur := byLine[c.LineID]
			patch := guests.Patch{Email: &c.ToEmail, Phone: &c.ToPhone}
			if c.ToName != cur.Name {
				first, last := splitName(c.ToName)
				patch.FirstName, patch.LastName = &first, &last
			}
			if _, err := s.guests.Update(ctx, *cur.GuestID, patch); err != nil {
				return err
			}
		}
		out = RoomingResult{Updated: len(changes), Unchanged: len(edits) - len(changes)}
		bd := day.BusinessDate
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: "group.rooming_list_updated",
			EntityType: "booking_group", EntityID: groupID, EntityLabel: g.Code, New: map[string]any{"updated": out.Updated, "unchanged": out.Unchanged}})
	})
	return out, err
}

// RoomingImport is what a file of the rooming list says, and what it did.
type RoomingImport struct {
	DryRun   bool             `json:"dry_run"`
	Rows     int              `json:"rows"`
	Changes  []RoomingChange  `json:"changes"`
	Problems []RoomingProblem `json:"problems"`
	Applied  bool             `json:"applied"`
}

var csvColumns = []string{"confirmation_number", "name", "email", "phone"}

var nonBreaking = regexp.MustCompile(`^\x{FEFF}`)

// ImportRoomingList reads a CSV (the columns confirmation_number, name, email, phone), checks every row and, unless dryRun, applies all of it or none. Every wrong row is reported, not the
// first only. The file may be edited from the list that `RoomingList` gives.
func (s *Service) ImportRoomingList(ctx context.Context, propertyID, groupID int64, data []byte, dryRun bool) (RoomingImport, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return RoomingImport{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermGuestWrite); err != nil {
		return RoomingImport{}, err
	}
	out := RoomingImport{DryRun: dryRun, Changes: []RoomingChange{}, Problems: []RoomingProblem{}}
	if _, err := s.q(ctx).GroupForBatch(ctx, reservationsdb.GroupForBatchParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
		return out, orNotFound(err, errGroupNotFound())
	}
	rows, err := s.roomingRows(ctx, p.TenantID, propertyID, groupID)
	if err != nil {
		return out, err
	}
	r := csv.NewReader(bytes.NewReader([]byte(nonBreaking.ReplaceAllString(string(data), ""))))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		out.Problems = append(out.Problems, RoomingProblem{Row: 1, Code: "EMPTY_FILE", Message: "the file has no header row"})
		return out, importInvalid(out)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"confirmation_number", "name"} {
		if _, ok := col[need]; !ok {
			out.Problems = append(out.Problems, RoomingProblem{Row: 1, Column: need, Code: "MISSING_COLUMN", Message: "the column " + need + " is required (columns: " + strings.Join(csvColumns, ", ") + ")"})
		}
	}
	if len(out.Problems) > 0 {
		return out, importInvalid(out)
	}
	byNumber := map[string][]RoomingRow{}
	for _, row := range rows {
		byNumber[strings.ToUpper(row.ConfirmationNumber)] = append(byNumber[strings.ToUpper(row.ConfirmationNumber)], row)
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var edits []RoomingEdit
	var fileLine []int
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			out.Problems = append(out.Problems, RoomingProblem{Row: line, Code: "INVALID_CSV", Message: err.Error()})
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		number := strings.ToUpper(get(rec, "confirmation_number"))
		matches := byNumber[number]
		switch {
		case number == "":
			out.Problems = append(out.Problems, RoomingProblem{Row: line, Column: "confirmation_number", Code: "REQUIRED", Message: "a confirmation number"})
			continue
		case len(matches) == 0:
			out.Problems = append(out.Problems, RoomingProblem{Row: line, Column: "confirmation_number", Code: "NOT_IN_GROUP", Message: "no reservation of this group has this number"})
			continue
		case len(matches) > 1:
			out.Problems = append(out.Problems, RoomingProblem{Row: line, Column: "confirmation_number", Code: "AMBIGUOUS", Message: "the reservation has several rooms: edit them in the list"})
			continue
		}
		edits = append(edits, RoomingEdit{LineID: matches[0].LineID, Name: get(rec, "name"), Email: get(rec, "email"), Phone: get(rec, "phone")})
		fileLine = append(fileLine, line)
	}
	out.Rows = len(edits) + len(out.Problems)
	changes, problems := plan(rows, edits, func(i int) int { return fileLine[i] }, func(f string) string {
		if f == "line_id" {
			return "confirmation_number"
		}
		return f
	})
	out.Problems = append(out.Problems, problems...)
	slices.SortFunc(out.Problems, func(a, b RoomingProblem) int { return a.Row - b.Row })
	out.Changes = append(out.Changes, changes...)
	if len(out.Problems) > 0 {
		if dryRun {
			return out, nil
		}
		return out, importInvalid(out)
	}
	if dryRun {
		return out, nil
	}
	if _, err := s.UpdateRoomingList(ctx, propertyID, groupID, edits); err != nil {
		return out, err
	}
	out.Applied = true
	return out, nil
}

func importInvalid(in RoomingImport) error {
	return apperr.New(apperr.KindInvalid, "ROOMING_IMPORT_INVALID", "the file has "+strconv.Itoa(len(in.Problems))+" wrong row(s); nothing was changed").WithContext("problems", in.Problems)
}

var _ = db.LevelNone
