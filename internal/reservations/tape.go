package reservations

import (
	"context"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations/reservationsdb"
)

// MaxTapeDays bounds the window of the tape chart.
const MaxTapeDays = 62

// TapeBooking is a booked room line on the chart.
type TapeBooking struct {
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	ReservationRoomID  int64      `json:"reservation_room_id"`
	Status             string     `json:"status"`
	GuestName          string     `json:"guest_name,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
}

// TapeBlock is an active OOO/OOS block on the chart.
type TapeBlock struct {
	ID        int64      `json:"id"`
	BlockType string     `json:"block_type"`
	StartDate civil.Date `json:"start_date"`
	EndDate   civil.Date `json:"end_date"`
}

// TapeRoom is one row of the chart.
type TapeRoom struct {
	RoomID       int64         `json:"room_id"`
	RoomNumber   string        `json:"room_number"`
	RoomTypeID   int64         `json:"room_type_id"`
	RoomTypeCode string        `json:"room_type_code"`
	Bookings     []TapeBooking `json:"bookings"`
	Blocks       []TapeBlock   `json:"blocks"`
}

// TapeUnassigned lists the CONFIRMED lines of a room type that have no specific room yet.
type TapeUnassigned struct {
	RoomTypeID   int64         `json:"room_type_id"`
	RoomTypeCode string        `json:"room_type_code"`
	Bookings     []TapeBooking `json:"bookings"`
}

// Tape is the read-only tape chart of [from, to).
type Tape struct {
	From       civil.Date       `json:"from"`
	To         civil.Date       `json:"to"`
	Rooms      []TapeRoom       `json:"rooms"`
	Unassigned []TapeUnassigned `json:"unassigned"`
}

// TapeChart lists rooms with the bookings and blocks that touch [from, to) (reservation.read). It reads
// committed data and takes no locks. A checked-in line is shown in its line's room.
func (s *Service) TapeChart(ctx context.Context, propertyID int64, from, to civil.Date) (Tape, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return Tape{}, err
	}
	if !to.After(from) || from.DaysUntil(to) > MaxTapeDays {
		return Tape{}, apperr.Invalid("the window is invalid", fieldErr("to", "OUT_OF_RANGE", "after from, at most 62 days"))
	}
	q := s.q(ctx)
	rooms, err := q.ListTapeRooms(ctx, reservationsdb.ListTapeRoomsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Tape{}, err
	}
	lines, err := q.ListTapeLines(ctx, reservationsdb.ListTapeLinesParams{TenantID: p.TenantID, PropertyID: propertyID, WindowStart: from, WindowEnd: to})
	if err != nil {
		return Tape{}, err
	}
	blocks, err := q.ListTapeBlocks(ctx, reservationsdb.ListTapeBlocksParams{TenantID: p.TenantID, PropertyID: propertyID, WindowStart: from, WindowEnd: to})
	if err != nil {
		return Tape{}, err
	}
	out := Tape{From: from, To: to, Rooms: make([]TapeRoom, len(rooms)), Unassigned: []TapeUnassigned{}}
	byRoom := map[int64]*TapeRoom{}
	typeCode := map[int64]string{}
	for i, r := range rooms {
		out.Rooms[i] = TapeRoom{RoomID: r.ID, RoomNumber: r.RoomNumber, RoomTypeID: r.RoomTypeID, RoomTypeCode: r.RoomTypeCode, Bookings: []TapeBooking{}, Blocks: []TapeBlock{}}
		byRoom[r.ID] = &out.Rooms[i]
		typeCode[r.RoomTypeID] = r.RoomTypeCode
	}
	for _, b := range blocks {
		if row := byRoom[b.RoomID]; row != nil {
			row.Blocks = append(row.Blocks, TapeBlock{ID: b.ID, BlockType: b.BlockType, StartDate: b.StartDate, EndDate: b.EndDate})
		}
	}
	unassigned := map[int64]int{} // room type -> index in out.Unassigned
	for _, l := range lines {
		name := deref(l.GuestLastName)
		if fn := deref(l.GuestFirstName); fn != "" {
			name = fn + " " + name
		}
		b := TapeBooking{ReservationID: l.ReservationID, ConfirmationNumber: l.ConfirmationNumber, ReservationRoomID: l.ID, Status: l.Status,
			GuestName: name, ArrivalDate: l.ArrivalDate, DepartureDate: l.DepartureDate}
		if l.RoomID != nil {
			if row := byRoom[*l.RoomID]; row != nil {
				row.Bookings = append(row.Bookings, b)
			}
			continue
		}
		i, ok := unassigned[l.RoomTypeID]
		if !ok {
			out.Unassigned = append(out.Unassigned, TapeUnassigned{RoomTypeID: l.RoomTypeID, RoomTypeCode: typeCode[l.RoomTypeID], Bookings: []TapeBooking{}})
			i = len(out.Unassigned) - 1
			unassigned[l.RoomTypeID] = i
		}
		out.Unassigned[i].Bookings = append(out.Unassigned[i].Bookings, b)
	}
	return out, nil
}
