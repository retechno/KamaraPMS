package reports

import (
	"context"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// FreeRoomLine is one room given away (complimentary or house use) over the range, with the nights it took.
type FreeRoomLine struct {
	ReservationRoomID  int64  `json:"reservation_room_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	Status             string `json:"status"`
	Guest              string `json:"guest"`
	RoomType           string `json:"room_type"`
	Room               string `json:"room,omitempty"`
	RatePlan           string `json:"rate_plan"`
	OccupancyKind      string `json:"occupancy_kind"`
	Reason             string `json:"reason"`
	Nights             int    `json:"nights"`
	// Value is what the nights would have cost on the reference plan; MissingNights counts the nights that plan has no
	// rate for (they are valued at zero).
	Value         string `json:"value"`
	MissingNights int    `json:"missing_nights"`
}

// FreeRoomGroup is the total of the lines that share a reason or a kind.
type FreeRoomGroup struct {
	Key           string `json:"key"`
	Rooms         int    `json:"rooms"`
	Nights        int    `json:"nights"`
	Value         string `json:"value"`
	MissingNights int    `json:"missing_nights"`
}

// FreeRoomsTotals is the whole range.
type FreeRoomsTotals struct {
	Rooms         int    `json:"rooms"`
	Nights        int    `json:"nights"`
	Value         string `json:"value"`
	MissingNights int    `json:"missing_nights"`
}

// FreeRoomsReport is the complimentary and house use rooms of a range, valued at the reference rate plan.
type FreeRoomsReport struct {
	From civil.Date `json:"from"`
	To   civil.Date `json:"to"`
	// ReferencePlan is the code of the plan the nights are valued at; empty when the property has none (then Value is 0).
	ReferencePlan string          `json:"reference_plan"`
	Lines         []FreeRoomLine  `json:"lines"`
	ByReason      []FreeRoomGroup `json:"by_reason"`
	ByKind        []FreeRoomGroup `json:"by_kind"`
	Totals        FreeRoomsTotals `json:"totals"`
}

// FreeRoomsList, FreeRoomsByReason and FreeRoomsByKind are the three tables of the report.
type (
	FreeRoomsList     FreeRoomsReport
	FreeRoomsByReason FreeRoomsReport
	FreeRoomsByKind   FreeRoomsReport
)

func (r FreeRoomsList) CSV() ([]string, [][]string) {
	h := []string{"confirmation_number", "status", "guest", "room_type", "room", "rate_plan", "kind", "reason", "nights", "value", "nights_without_rate"}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{l.ConfirmationNumber, l.Status, l.Guest, l.RoomType, l.Room, l.RatePlan, l.OccupancyKind, l.Reason, itoa(l.Nights), l.Value, itoa(l.MissingNights)})
	}
	return h, rows
}

func groupCSV(first string, groups []FreeRoomGroup) ([]string, [][]string) {
	h := []string{first, "rooms", "nights", "value", "nights_without_rate"}
	var rows [][]string
	for _, g := range groups {
		rows = append(rows, []string{g.Key, itoa(g.Rooms), itoa(g.Nights), g.Value, itoa(g.MissingNights)})
	}
	return h, rows
}

func (r FreeRoomsByReason) CSV() ([]string, [][]string) { return groupCSV("reason", r.ByReason) }
func (r FreeRoomsByKind) CSV() ([]string, [][]string)   { return groupCSV("kind", r.ByKind) }

// FreeRooms lists the complimentary and house use rooms with nights in [from, to] (report.view): the CONFIRMED,
// CHECKED_IN and COMPLETED lines on a plan of those kinds, valued at the grid price of the property's reference rate
// plan for the room type and night. Without a reference plan the nights are still counted and the value is zero.
func (s *Service) FreeRooms(ctx context.Context, propertyID int64, from, to civil.Date) (FreeRoomsReport, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return FreeRoomsReport{}, err
	}
	if err := checkRange(from, to); err != nil {
		return FreeRoomsReport{}, err
	}
	q := s.q(ctx)
	out := FreeRoomsReport{From: from, To: to, Lines: []FreeRoomLine{}, ByReason: []FreeRoomGroup{}, ByKind: []FreeRoomGroup{}}
	var referenceID *int64
	if ref, err := q.GetReferenceRatePlan(ctx, reportsdb.GetReferenceRatePlanParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil {
		referenceID, out.ReferencePlan = &ref.ID, ref.Code
	}
	rows, err := q.ReportFreeRoomNights(ctx, reportsdb.ReportFreeRoomNightsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to, ReferencePlanID: referenceID})
	if err != nil {
		return FreeRoomsReport{}, err
	}

	type acc struct {
		line  FreeRoomLine
		value decimal.Decimal
	}
	var order []int64
	lines := map[int64]*acc{}
	for _, r := range rows {
		a := lines[r.ReservationRoomID]
		if a == nil {
			a = &acc{line: FreeRoomLine{
				ReservationRoomID: r.ReservationRoomID, ConfirmationNumber: r.ConfirmationNumber, Status: r.Status, Guest: name(r.GuestFirstName, deref(r.GuestLastName)),
				RoomType: r.RoomTypeCode, Room: deref(r.RoomNumber), RatePlan: r.PlanCode, OccupancyKind: r.OccupancyKind, Reason: strings.TrimSpace(deref(r.OccupancyReason)),
			}}
			lines[r.ReservationRoomID] = a
			order = append(order, r.ReservationRoomID)
		}
		a.line.Nights++
		if r.ReferenceAmount == nil {
			a.line.MissingNights++
		} else {
			a.value = a.value.Add(*r.ReferenceAmount)
		}
	}

	type sums struct {
		rooms, nights, missing int
		value                  decimal.Decimal
	}
	total := sums{}
	byReason, byKind := map[string]*sums{}, map[string]*sums{}
	display := map[string]string{} // the reason as first written, for the groups that differ only in case or spacing
	add := func(m map[string]*sums, key string, a *acc) {
		g := m[key]
		if g == nil {
			g = &sums{}
			m[key] = g
		}
		g.rooms++
		g.nights += a.line.Nights
		g.missing += a.line.MissingNights
		g.value = g.value.Add(a.value)
	}
	for _, id := range order {
		a := lines[id]
		a.line.Value = fx(a.value, decimals)
		out.Lines = append(out.Lines, a.line)
		key := strings.ToLower(a.line.Reason)
		if _, ok := display[key]; !ok {
			display[key] = a.line.Reason
		}
		add(byReason, key, a)
		add(byKind, a.line.OccupancyKind, a)
		total.rooms++
		total.nights += a.line.Nights
		total.missing += a.line.MissingNights
		total.value = total.value.Add(a.value)
	}
	groups := func(m map[string]*sums, label func(string) string) []FreeRoomGroup {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		// the biggest first, by value and then by nights
		sort.Slice(keys, func(i, j int) bool {
			a, b := m[keys[i]], m[keys[j]]
			if c := a.value.Cmp(b.value); c != 0 {
				return c > 0
			}
			if a.nights != b.nights {
				return a.nights > b.nights
			}
			return keys[i] < keys[j]
		})
		res := make([]FreeRoomGroup, 0, len(keys))
		for _, k := range keys {
			g := m[k]
			res = append(res, FreeRoomGroup{Key: label(k), Rooms: g.rooms, Nights: g.nights, Value: fx(g.value, decimals), MissingNights: g.missing})
		}
		return res
	}
	out.ByReason = groups(byReason, func(k string) string { return display[k] })
	out.ByKind = groups(byKind, func(k string) string { return k })
	out.Totals = FreeRoomsTotals{Rooms: total.rooms, Nights: total.nights, Value: fx(total.value, decimals), MissingNights: total.missing}
	return out, nil
}
