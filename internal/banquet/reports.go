package banquet

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The reports of 36-banquet.md 4.10 (B15). Each one answers a period and reads the database only; whoever works with events or reads (banquet.read and the others) may open them.

const maxReportDays = 400

// periodOf checks a period of a report; a missing end or start takes the default.
func periodOf(from, to, defFrom, defTo civil.Date) (civil.Date, civil.Date, error) {
	if from.IsZero() {
		from = defFrom
	}
	if to.IsZero() {
		to = defTo
	}
	if to.Before(from) {
		return from, to, invalid("report", fieldErr("to", "INVALID_VALUE", "not before the start date"))
	}
	if from.DaysUntil(to) > maxReportDays {
		return from, to, invalid("report", fieldErr("to", "OUT_OF_RANGE", "400 days at most"))
	}
	return from, to, nil
}

func monthStart(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month(), 1) }
func monthEnd(d civil.Date) civil.Date   { return civil.NewDate(d.Year(), d.Month()+1, 1).AddDays(-1) }

// defaults are the month of the business date; the forecast looks several months ahead.
func (s *Service) defaults(ctx context.Context, propertyID int64, months int) (civil.Date, civil.Date) {
	today := s.today(ctx, propertyID)
	if today.IsZero() {
		today = civil.DateOf(s.clock.Now())
	}
	return monthStart(today), monthEnd(civil.NewDate(today.Year(), today.Month()+time.Month(months-1), 1))
}

func idKeyString(id int64) string { return strconv.FormatInt(id, 10) }

// ---------------------------------------------------------------- the forecast

// ForecastGroup is a row of the forecast: the revenue of the definite events and, on a row of its own, the tentative ones. The two are never added (a tentative event is a hope, a definite one is a contract).
type ForecastGroup struct {
	Key             string          `json:"key"` // the month (YYYY-MM), or the id of the event type or of the sales person
	Name            string          `json:"name"`
	Definite        decimal.Decimal `json:"definite"`
	Tentative       decimal.Decimal `json:"tentative"`
	DefiniteEvents  int             `json:"definite_events"`
	TentativeEvents int             `json:"tentative_events"`
}

// ForecastReport is the forecast of revenue of 4.10: by month, by type of event and by sales person, from the sessions that are still to be served or posted.
type ForecastReport struct {
	From      civil.Date      `json:"from"`
	To        civil.Date      `json:"to"`
	WithTax   bool            `json:"with_tax"` // service and tax are in the amounts
	Months    []ForecastGroup `json:"months"`
	ByType    []ForecastGroup `json:"by_type"`
	BySales   []ForecastGroup `json:"by_sales"`
	Definite  decimal.Decimal `json:"definite"`
	Tentative decimal.Decimal `json:"tentative"`
	// What the sales office watches next to the forecast.
	TentativeOverdue   int              `json:"tentative_overdue"`    // tentative events whose option date has gone
	Unposted           int              `json:"unposted"`             // sessions due and not posted
	UnpostedOldestDays int              `json:"unposted_oldest_days"` // the oldest of them, in days
	Conversion         *decimal.Decimal `json:"conversion_pct"`       // over the 90 days before the end of the period
}

type groupAcc struct {
	g     ForecastGroup
	defEv map[int64]bool
	tenEv map[int64]bool
}

func newAcc(key, name string) *groupAcc {
	return &groupAcc{g: ForecastGroup{Key: key, Name: name}, defEv: map[int64]bool{}, tenEv: map[int64]bool{}}
}

func (a *groupAcc) add(status string, event int64, amount decimal.Decimal) {
	if status == StatusDefinite {
		a.g.Definite = a.g.Definite.Add(amount)
		a.defEv[event] = true
	} else {
		a.g.Tentative = a.g.Tentative.Add(amount)
		a.tenEv[event] = true
	}
}

func finishAccs(m map[string]*groupAcc, sortByName bool) []ForecastGroup {
	out := make([]ForecastGroup, 0, len(m))
	for _, a := range m {
		a.g.DefiniteEvents, a.g.TentativeEvents = len(a.defEv), len(a.tenEv)
		out = append(out, a.g)
	}
	sort.Slice(out, func(i, j int) bool {
		if sortByName && out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Forecast answers the revenue the sessions of the period are expected to bring, from the lines of the events by the same plan that posting follows (the guests are the posted, else the guaranteed or
// actual, else the estimated number). A session already posted counts what was posted. withTax adds service and tax to the amounts.
func (s *Service) Forecast(ctx context.Context, propertyID int64, from, to civil.Date, withTax bool) (ForecastReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return ForecastReport{}, err
	}
	df, dt := s.defaults(ctx, propertyID, 6)
	if from, to, err = periodOf(from, to, df, dt); err != nil {
		return ForecastReport{}, err
	}
	rows, err := s.q(ctx).ReportForecastSessions(ctx, banquetdb.ReportForecastSessionsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return ForecastReport{}, err
	}
	out := ForecastReport{From: from, To: to, WithTax: withTax}
	months, types, sales := map[string]*groupAcc{}, map[string]*groupAcc{}, map[string]*groupAcc{}
	today := s.today(ctx, propertyID)
	overdue := map[int64]bool{}
	estimated := map[int64]map[int64]decimal.Decimal{} // event -> session -> amount
	get := func(m map[string]*groupAcc, key, name string) *groupAcc {
		if m[key] == nil {
			m[key] = newAcc(key, name)
		}
		return m[key]
	}
	for _, r := range rows {
		per, ok := estimated[r.EventID]
		if !ok {
			lines, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, r.EventID)
			if err != nil {
				return ForecastReport{}, err
			}
			bySession := make(map[int64]int64, len(lines))
			for _, l := range lines {
				bySession[l.ID] = l.SessionID
			}
			per = map[int64]decimal.Decimal{}
			for _, el := range est.Lines {
				amount := el.Net
				if withTax {
					amount = el.Total
				}
				per[bySession[el.LineID]] = per[bySession[el.LineID]].Add(amount)
			}
			estimated[r.EventID] = per
		}
		amount := per[r.SessionID]
		mk := r.SessionDate.String()[:7]
		get(months, mk, mk).add(r.Status, r.EventID, amount)
		get(types, idKeyString(r.EventTypeID), r.TypeName).add(r.Status, r.EventID, amount)
		get(sales, idKeyString(r.SalesUserID), r.SalesName).add(r.Status, r.EventID, amount)
		if r.Status == StatusTentative && r.OptionUntil != nil && !today.IsZero() && r.OptionUntil.Before(today) {
			overdue[r.EventID] = true
		}
	}
	out.Months, out.ByType, out.BySales = finishAccs(months, false), finishAccs(types, true), finishAccs(sales, true)
	for _, a := range out.Months {
		out.Definite = out.Definite.Add(a.Definite)
		out.Tentative = out.Tentative.Add(a.Tentative)
	}
	out.TentativeOverdue = len(overdue)
	un, err := s.unposted(ctx, p.TenantID, propertyID)
	if err != nil {
		return ForecastReport{}, err
	}
	out.Unposted = len(un)
	for _, u := range un {
		out.UnpostedOldestDays = max(out.UnpostedOldestDays, u.DaysLate)
	}
	prod, err := s.productionRows(ctx, p.TenantID, propertyID, to.AddDays(-90), to)
	if err != nil {
		return ForecastReport{}, err
	}
	var def, lost int
	for _, r := range prod {
		def, lost = def+r.Definite, lost+r.Lost
	}
	out.Conversion = Conversion(def, lost)
	return out, nil
}

// ---------------------------------------------------------------- revenue and occupancy by room

// RoomRow is a room in the report of rooms: what it sold, how long it was held and what was posted from it.
type RoomRow struct {
	RoomID         int64            `json:"room_id"`
	Code           string           `json:"code"`
	Name           string           `json:"name"`
	IsCombo        bool             `json:"is_combo"`
	Sessions       int              `json:"sessions"`        // definite and completed sessions held in the room
	Hours          decimal.Decimal  `json:"hours"`           // the hours it was held, inside the hours of operation (a combination and its parts block each other)
	AvailableHours decimal.Decimal  `json:"available_hours"` // the hours of operation x the days of the period
	OccupancyPct   *decimal.Decimal `json:"occupancy_pct"`
	PostedSessions int              `json:"posted_sessions"`
	Revenue        decimal.Decimal  `json:"revenue"` // net, of the sessions posted in the period
	RevenuePerHour *decimal.Decimal `json:"revenue_per_hour"`
}

// RoomsReport is the report of revenue and occupancy by room.
type RoomsReport struct {
	From          civil.Date      `json:"from"`
	To            civil.Date      `json:"to"`
	OperatingFrom civil.TimeOfDay `json:"operating_from"`
	OperatingTo   civil.TimeOfDay `json:"operating_to"`
	Days          int             `json:"days"`
	Rooms         []RoomRow       `json:"rooms"`
	Revenue       decimal.Decimal `json:"revenue"`
}

// Rooms answers, for each active room, the hours it was held (definite and completed sessions) against the hours of operation x the days, and the net revenue posted from its sessions.
func (s *Service) Rooms(ctx context.Context, propertyID int64, from, to civil.Date) (RoomsReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return RoomsReport{}, err
	}
	df, dt := s.defaults(ctx, propertyID, 1)
	if from, to, err = periodOf(from, to, df, dt); err != nil {
		return RoomsReport{}, err
	}
	st, err := s.settings(ctx, propertyID)
	if err != nil {
		return RoomsReport{}, err
	}
	opFrom, opTo := DefaultOperatingFrom, DefaultOperatingTo
	if st.OperatingFrom != nil && st.OperatingTo != nil {
		opFrom, opTo = minutesOf(*st.OperatingFrom), minutesOf(*st.OperatingTo)
	}
	active := true
	rooms, err := s.rooms(ctx, p.TenantID, propertyID, nil, &active)
	if err != nil {
		return RoomsReport{}, err
	}
	q := s.q(ctx)
	sessions, err := q.ReportRoomSessions(ctx, banquetdb.ReportRoomSessionsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return RoomsReport{}, err
	}
	rev, err := q.ReportRoomRevenue(ctx, banquetdb.ReportRoomRevenueParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return RoomsReport{}, err
	}
	days := from.DaysUntil(to) + 1
	out := RoomsReport{From: from, To: to, Days: days, Rooms: []RoomRow{}}
	out.OperatingFrom, _ = civil.NewTimeOfDay(opFrom/60, opFrom%60)
	out.OperatingTo, _ = civil.NewTimeOfDay(opTo/60, opTo%60)
	block := Blockers(rooms)
	type key struct {
		room int64
		day  civil.Date
	}
	spans := map[key][]Span{}
	direct := map[int64]int{}
	for _, r := range sessions {
		if r.RoomID == nil || r.StartTime == nil || r.EndTime == nil {
			continue
		}
		direct[*r.RoomID]++
		sp := SessionSpan(*r.StartTime, *r.EndTime, r.EndsNextDay)
		for _, blocked := range block[*r.RoomID] {
			k := key{blocked, r.SessionDate}
			spans[k] = append(spans[k], sp)
		}
	}
	available := decimal.NewFromInt(int64((opTo - opFrom) * days)).Div(decimal.NewFromInt(60))
	for _, rm := range rooms {
		row := RoomRow{RoomID: rm.ID, Code: rm.Code, Name: rm.Name, IsCombo: rm.IsCombo, Sessions: direct[rm.ID], AvailableHours: available.Round(2)}
		minutes := 0
		for k, v := range spans {
			if k.room == rm.ID {
				minutes += OccupiedMinutes(v, opFrom, opTo)
			}
		}
		row.Hours = decimal.NewFromInt(int64(minutes)).Div(decimal.NewFromInt(60)).Round(2)
		row.OccupancyPct = Percent(row.Hours, available)
		for _, r := range rev {
			if r.RoomID != nil && *r.RoomID == rm.ID {
				row.PostedSessions, row.Revenue = int(r.Sessions), r.Net
			}
		}
		if row.Hours.IsPositive() {
			v := row.Revenue.Div(row.Hours).Round(2)
			row.RevenuePerHour = &v
		}
		out.Revenue = out.Revenue.Add(row.Revenue)
		out.Rooms = append(out.Rooms, row)
	}
	return out, nil
}

// ---------------------------------------------------------------- guaranteed against actual

// GuestRow is a session with its three numbers of guests.
type GuestRow struct {
	SessionID     int64      `json:"session_id"`
	EventID       int64      `json:"event_id"`
	EventNumber   string     `json:"event_number"`
	EventName     string     `json:"event_name"`
	SessionNo     int        `json:"session_no"`
	SessionName   string     `json:"session_name"`
	SessionDate   civil.Date `json:"session_date"`
	RoomName      *string    `json:"room_name"`
	PaxEstimated  int        `json:"pax_estimated"`
	PaxGuaranteed *int       `json:"pax_guaranteed"`
	PaxActual     *int       `json:"pax_actual"`
	// Difference is the actual number less the guaranteed one, when both are known; a positive number is guests the hotel served beyond the guarantee, a negative one guests it was paid for and did not serve.
	Difference *int `json:"difference"`
}

// GuestsReport is the guaranteed against the actual guests for the sessions of the period.
type GuestsReport struct {
	From          civil.Date `json:"from"`
	To            civil.Date `json:"to"`
	Rows          []GuestRow `json:"rows"`
	Guaranteed    int        `json:"guaranteed"` // the totals are over the sessions that have both numbers
	Actual        int        `json:"actual"`
	Difference    int        `json:"difference"`
	ComparedCount int        `json:"compared_count"`
	Over          int        `json:"over"`  // sessions that served more guests than guaranteed
	Under         int        `json:"under"` // and fewer
}

// Guests answers the sessions of definite and completed events in the period with their estimated, guaranteed and actual guests.
func (s *Service) Guests(ctx context.Context, propertyID int64, from, to civil.Date) (GuestsReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return GuestsReport{}, err
	}
	df, dt := s.defaults(ctx, propertyID, 1)
	if from, to, err = periodOf(from, to, df, dt); err != nil {
		return GuestsReport{}, err
	}
	rows, err := s.q(ctx).ReportGuests(ctx, banquetdb.ReportGuestsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return GuestsReport{}, err
	}
	out := GuestsReport{From: from, To: to, Rows: make([]GuestRow, 0, len(rows))}
	for _, r := range rows {
		g := GuestRow{SessionID: r.SessionID, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, SessionNo: int(r.SessionNo), SessionName: r.SessionName,
			SessionDate: r.SessionDate, RoomName: r.RoomName, PaxEstimated: int(r.PaxEstimated)}
		if r.PaxGuaranteed != nil {
			g.PaxGuaranteed = ptr(int(*r.PaxGuaranteed))
		}
		if r.PaxActual != nil {
			g.PaxActual = ptr(int(*r.PaxActual))
		}
		if g.PaxGuaranteed != nil && g.PaxActual != nil {
			d := *g.PaxActual - *g.PaxGuaranteed
			g.Difference = &d
			out.Guaranteed, out.Actual, out.Difference, out.ComparedCount = out.Guaranteed+*g.PaxGuaranteed, out.Actual+*g.PaxActual, out.Difference+d, out.ComparedCount+1
			switch {
			case d > 0:
				out.Over++
			case d < 0:
				out.Under++
			}
		}
		out.Rows = append(out.Rows, g)
	}
	return out, nil
}

// ---------------------------------------------------------------- production by sales

// SalesRow is the production of one sales person in the period.
type SalesRow struct {
	SalesUserID   int64            `json:"sales_user_id"`
	SalesName     string           `json:"sales_name"`
	Created       int              `json:"created"`  // events opened in the period
	Definite      int              `json:"definite"` // events made definite in the period
	Lost          int              `json:"lost"`     // events lost in the period
	DefiniteValue decimal.Decimal  `json:"definite_value"`
	LostValue     decimal.Decimal  `json:"lost_value"`
	Conversion    *decimal.Decimal `json:"conversion_pct"` // definite / (definite + lost)
}

// SalesReport is the production by sales person.
type SalesReport struct {
	From  civil.Date `json:"from"`
	To    civil.Date `json:"to"`
	Rows  []SalesRow `json:"rows"`
	Total SalesRow   `json:"total"`
}

func (s *Service) productionRows(ctx context.Context, tenantID, propertyID int64, from, to civil.Date) ([]SalesRow, error) {
	rows, err := s.q(ctx).ReportSalesEvents(ctx, banquetdb.ReportSalesEventsParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return nil, err
	}
	in := func(d string) bool { return d != "" && d >= from.String() && d <= to.String() }
	by := map[int64]*SalesRow{}
	for _, r := range rows {
		x := by[r.SalesUserID]
		if x == nil {
			x = &SalesRow{SalesUserID: r.SalesUserID, SalesName: r.SalesName}
			by[r.SalesUserID] = x
		}
		if in(r.CreatedOn) {
			x.Created++
		}
		if in(r.DefiniteOn) {
			x.Definite++
			x.DefiniteValue = x.DefiniteValue.Add(r.ContractValueEstimate)
		}
		if in(r.LostOn) {
			x.Lost++
			x.LostValue = x.LostValue.Add(r.ContractValueEstimate)
		}
	}
	out := make([]SalesRow, 0, len(by))
	for _, x := range by {
		x.Conversion = Conversion(x.Definite, x.Lost)
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SalesName < out[j].SalesName })
	return out, nil
}

// Sales answers the production of each sales person: the events opened, made definite and lost in the period, the value of the definite and of the lost ones, and the conversion.
func (s *Service) Sales(ctx context.Context, propertyID int64, from, to civil.Date) (SalesReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return SalesReport{}, err
	}
	df, dt := s.defaults(ctx, propertyID, 1)
	if from, to, err = periodOf(from, to, df, dt); err != nil {
		return SalesReport{}, err
	}
	rows, err := s.productionRows(ctx, p.TenantID, propertyID, from, to)
	if err != nil {
		return SalesReport{}, err
	}
	out := SalesReport{From: from, To: to, Rows: rows}
	for _, r := range rows {
		out.Total.Created, out.Total.Definite, out.Total.Lost = out.Total.Created+r.Created, out.Total.Definite+r.Definite, out.Total.Lost+r.Lost
		out.Total.DefiniteValue, out.Total.LostValue = out.Total.DefiniteValue.Add(r.DefiniteValue), out.Total.LostValue.Add(r.LostValue)
	}
	out.Total.Conversion = Conversion(out.Total.Definite, out.Total.Lost)
	return out, nil
}

// ---------------------------------------------------------------- the lost and the cancelled

// LostRow is an event that was lost or cancelled.
type LostRow struct {
	EventID     int64           `json:"event_id"`
	EventNumber string          `json:"event_number"`
	EventName   string          `json:"event_name"`
	Status      string          `json:"status"`
	SalesName   string          `json:"sales_name"`
	Value       decimal.Decimal `json:"value"`
	ReasonCode  string          `json:"reason_code"`
	ReasonName  string          `json:"reason_name"`
	ReasonNote  *string         `json:"reason_note"`
	ClosedOn    civil.Date      `json:"closed_on"`
}

// LostGroup is a reason with how many events and how much value it cost.
type LostGroup struct {
	Status     string          `json:"status"`
	ReasonCode string          `json:"reason_code"`
	ReasonName string          `json:"reason_name"`
	Events     int             `json:"events"`
	Value      decimal.Decimal `json:"value"`
}

// LostReport is the lost and cancelled events with the reasons given.
type LostReport struct {
	From      civil.Date      `json:"from"`
	To        civil.Date      `json:"to"`
	Groups    []LostGroup     `json:"groups"`
	Rows      []LostRow       `json:"rows"`
	Lost      int             `json:"lost"`
	Cancelled int             `json:"cancelled"`
	Value     decimal.Decimal `json:"value"`
}

// Lost answers the events that were lost or cancelled in the period, grouped by the reason that was given.
func (s *Service) Lost(ctx context.Context, propertyID int64, from, to civil.Date) (LostReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return LostReport{}, err
	}
	df, dt := s.defaults(ctx, propertyID, 1)
	if from, to, err = periodOf(from, to, df, dt); err != nil {
		return LostReport{}, err
	}
	rows, err := s.q(ctx).ReportLostEvents(ctx, banquetdb.ReportLostEventsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return LostReport{}, err
	}
	out := LostReport{From: from, To: to, Rows: make([]LostRow, 0, len(rows)), Groups: []LostGroup{}}
	groups := map[string]*LostGroup{}
	for _, r := range rows {
		closed, _ := civil.ParseDate(r.ClosedOn)
		out.Rows = append(out.Rows, LostRow{EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, Status: r.Status, SalesName: r.SalesName, Value: r.ContractValueEstimate,
			ReasonCode: r.ReasonCode, ReasonName: r.ReasonName, ReasonNote: r.ReasonNote, ClosedOn: closed})
		k := r.Status + "/" + r.ReasonCode
		g := groups[k]
		if g == nil {
			g = &LostGroup{Status: r.Status, ReasonCode: r.ReasonCode, ReasonName: r.ReasonName}
			groups[k] = g
		}
		g.Events++
		g.Value = g.Value.Add(r.ContractValueEstimate)
		out.Value = out.Value.Add(r.ContractValueEstimate)
		if r.Status == StatusLost {
			out.Lost++
		} else {
			out.Cancelled++
		}
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].Events != out.Groups[j].Events {
			return out.Groups[i].Events > out.Groups[j].Events
		}
		return out.Groups[i].ReasonCode < out.Groups[j].ReasonCode
	})
	return out, nil
}

// ---------------------------------------------------------------- the daily schedule

// ScheduleRow is a session of the day, for the board and for print.
type ScheduleRow struct {
	SessionID     int64            `json:"session_id"`
	EventID       int64            `json:"event_id"`
	EventNumber   string           `json:"event_number"`
	EventName     string           `json:"event_name"`
	Status        string           `json:"status"`
	SessionNo     int              `json:"session_no"`
	SessionName   string           `json:"session_name"`
	StartTime     *civil.TimeOfDay `json:"start_time"`
	EndTime       *civil.TimeOfDay `json:"end_time"`
	EndsNextDay   bool             `json:"ends_next_day"`
	RoomName      *string          `json:"room_name"`
	SetupStyle    *string          `json:"setup_style"`
	PaxEstimated  int              `json:"pax_estimated"`
	PaxGuaranteed *int             `json:"pax_guaranteed"`
	PaxActual     *int             `json:"pax_actual"`
	ContactName   *string          `json:"contact_name"`
	ContactPhone  *string          `json:"contact_phone"`
	SalesName     string           `json:"sales_name"`
	// BeoRevision is the revision of the BEO that is out (0: none yet) and BeoUnread the sections with a text no department has acknowledged for it.
	BeoRevision int `json:"beo_revision"`
	BeoUnread   int `json:"beo_unread"`
}

// ScheduleReport is the schedule of one day.
type ScheduleReport struct {
	Day  civil.Date    `json:"day"`
	Rows []ScheduleRow `json:"rows"`
}

// Schedule answers the sessions of tentative, definite and completed events on a day, by their start time.
func (s *Service) Schedule(ctx context.Context, propertyID int64, day civil.Date) (ScheduleReport, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return ScheduleReport{}, err
	}
	if day.IsZero() {
		if day = s.today(ctx, propertyID); day.IsZero() {
			return ScheduleReport{}, apperr.Conflict("NO_BUSINESS_DAY", "the property has no open business day")
		}
	}
	rows, err := s.q(ctx).ReportSchedule(ctx, banquetdb.ReportScheduleParams{TenantID: p.TenantID, PropertyID: propertyID, Day: day})
	if err != nil {
		return ScheduleReport{}, err
	}
	out := ScheduleReport{Day: day, Rows: make([]ScheduleRow, 0, len(rows))}
	for _, r := range rows {
		x := ScheduleRow{SessionID: r.SessionID, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, Status: r.Status, SessionNo: int(r.SessionNo), SessionName: r.SessionName,
			StartTime: r.StartTime, EndTime: r.EndTime, EndsNextDay: r.EndsNextDay, RoomName: r.RoomName, SetupStyle: r.SetupStyle, PaxEstimated: int(r.PaxEstimated),
			ContactName: r.ContactName, ContactPhone: r.ContactPhone, SalesName: r.SalesName, BeoRevision: int(r.BeoRevision), BeoUnread: int(r.BeoUnread)}
		if r.PaxGuaranteed != nil {
			x.PaxGuaranteed = ptr(int(*r.PaxGuaranteed))
		}
		if r.PaxActual != nil {
			x.PaxActual = ptr(int(*r.PaxActual))
		}
		out.Rows = append(out.Rows, x)
	}
	return out, nil
}
