package reports

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// The housekeeping and maintenance reports (report.view). Like every report they only read.

// ---------------------------------------------------------------- productivity

// HousekeepingLine is one person's work on one business date.
type HousekeepingLine struct {
	BusinessDate civil.Date `json:"business_date"`
	UserID       int64      `json:"user_id"`
	User         string     `json:"user"`
	Cleaned      int        `json:"rooms_cleaned"`
	Inspected    int        `json:"rooms_inspected"`
	TasksDone    int        `json:"tasks_done"`
	TasksSkipped int        `json:"tasks_skipped"`
	AvgMinutes   string     `json:"avg_task_minutes"`
}

// HousekeepingPerson totals one person over the range.
type HousekeepingPerson struct {
	UserID       int64  `json:"user_id"`
	User         string `json:"user"`
	Days         int    `json:"days_worked"`
	Cleaned      int    `json:"rooms_cleaned"`
	Inspected    int    `json:"rooms_inspected"`
	TasksDone    int    `json:"tasks_done"`
	TasksSkipped int    `json:"tasks_skipped"`
	AvgMinutes   string `json:"avg_task_minutes"`
}

// HousekeepingProductivity is what each housekeeper did over a range.
type HousekeepingProductivity struct {
	From   civil.Date           `json:"from"`
	To     civil.Date           `json:"to"`
	Lines  []HousekeepingLine   `json:"lines"`
	People []HousekeepingPerson `json:"people"`
}

func (r HousekeepingProductivity) CSV() ([]string, [][]string) {
	h := []string{"business_date", "user", "rooms_cleaned", "rooms_inspected", "tasks_done", "tasks_skipped", "avg_task_minutes"}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{l.BusinessDate.String(), l.User, itoa(l.Cleaned), itoa(l.Inspected), itoa(l.TasksDone), itoa(l.TasksSkipped), l.AvgMinutes})
	}
	return h, rows
}

type hkKey struct {
	date civil.Date
	user int64
}

type hkAcc struct {
	name                 string
	cleaned, inspected   int
	done, skipped, timed int
	minutes              decimal.Decimal
}

func avg(minutes decimal.Decimal, n int) string {
	if n == 0 {
		return ""
	}
	return minutes.DivRound(decimal.NewFromInt(int64(n)), 1).StringFixed(1)
}

// HousekeepingProductivity reports what each housekeeper did between two business dates.
func (s *Service) HousekeepingProductivity(ctx context.Context, propertyID int64, from, to civil.Date) (HousekeepingProductivity, error) {
	p, _, err := s.access(ctx, propertyID)
	if err != nil {
		return HousekeepingProductivity{}, err
	}
	if err := checkRange(from, to); err != nil {
		return HousekeepingProductivity{}, err
	}
	q := s.q(ctx)
	cleaned, err := q.ReportHousekeepingCleaned(ctx, reportsdb.ReportHousekeepingCleanedParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return HousekeepingProductivity{}, err
	}
	tasks, err := q.ReportHousekeepingTasks(ctx, reportsdb.ReportHousekeepingTasksParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return HousekeepingProductivity{}, err
	}
	byKey := map[hkKey]*hkAcc{}
	get := func(d civil.Date, user int64, name string) *hkAcc {
		k := hkKey{d, user}
		if byKey[k] == nil {
			byKey[k] = &hkAcc{name: name}
		}
		return byKey[k]
	}
	for _, r := range cleaned {
		a := get(r.BusinessDate, deref64(r.UserID), r.FullName)
		a.cleaned, a.inspected = a.cleaned+int(r.Cleaned), a.inspected+int(r.Inspected)
	}
	for _, r := range tasks {
		a := get(r.TaskDate, deref64(r.UserID), r.FullName)
		a.done, a.skipped, a.timed, a.minutes = a.done+int(r.Done), a.skipped+int(r.Skipped), a.timed+int(r.Timed), a.minutes.Add(r.Minutes)
	}
	keys := make([]hkKey, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if c := keys[i].date.Compare(keys[j].date); c != 0 {
			return c < 0
		}
		if byKey[keys[i]].name != byKey[keys[j]].name {
			return byKey[keys[i]].name < byKey[keys[j]].name
		}
		return keys[i].user < keys[j].user
	})
	out := HousekeepingProductivity{From: from, To: to, Lines: []HousekeepingLine{}, People: []HousekeepingPerson{}}
	people := map[int64]*hkAcc{}
	days := map[int64]int{}
	var order []int64
	for _, k := range keys {
		a := byKey[k]
		out.Lines = append(out.Lines, HousekeepingLine{
			BusinessDate: k.date, UserID: k.user, User: a.name, Cleaned: a.cleaned, Inspected: a.inspected, TasksDone: a.done, TasksSkipped: a.skipped, AvgMinutes: avg(a.minutes, a.timed),
		})
		t := people[k.user]
		if t == nil {
			t = &hkAcc{name: a.name}
			people[k.user] = t
			order = append(order, k.user)
		}
		t.cleaned, t.inspected, t.done, t.skipped, t.timed = t.cleaned+a.cleaned, t.inspected+a.inspected, t.done+a.done, t.skipped+a.skipped, t.timed+a.timed
		t.minutes = t.minutes.Add(a.minutes)
		days[k.user]++
	}
	sort.SliceStable(order, func(i, j int) bool { return people[order[i]].name < people[order[j]].name })
	for _, u := range order {
		t := people[u]
		out.People = append(out.People, HousekeepingPerson{
			UserID: u, User: t.name, Days: days[u], Cleaned: t.cleaned, Inspected: t.inspected, TasksDone: t.done, TasksSkipped: t.skipped, AvgMinutes: avg(t.minutes, t.timed),
		})
	}
	return out, nil
}

func deref64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// ---------------------------------------------------------------- rooms that are not clean

// DirtyRoom is a room that is not clean yet.
type DirtyRoom struct {
	RoomNumber string `json:"room_number"`
	Floor      string `json:"floor,omitempty"`
	RoomType   string `json:"room_type"`
	Status     string `json:"status"`
	Since      string `json:"since"`
	Hours      int    `json:"hours"`
	Occupancy  string `json:"occupancy"`
	Priority   string `json:"priority"`
	DND        bool   `json:"dnd"`
	Block      string `json:"block,omitempty"`
}

// DirtyRooms lists the rooms that are DIRTY or CLEANING, longest first.
type DirtyRooms struct {
	MinHours int         `json:"min_hours"`
	Rows     []DirtyRoom `json:"rows"`
}

func (r DirtyRooms) CSV() ([]string, [][]string) {
	h := []string{"room_number", "floor", "room_type", "status", "since", "hours", "occupancy", "priority", "dnd", "block"}
	var rows [][]string
	for _, d := range r.Rows {
		dnd := "no"
		if d.DND {
			dnd = "yes"
		}
		rows = append(rows, []string{d.RoomNumber, d.Floor, d.RoomType, d.Status, d.Since, itoa(d.Hours), d.Occupancy, d.Priority, dnd, d.Block})
	}
	return h, rows
}

// HousekeepingDirty lists the rooms that have been DIRTY or CLEANING for at least minHours hours, longest first.
func (s *Service) HousekeepingDirty(ctx context.Context, propertyID int64, minHours int) (DirtyRooms, error) {
	p, _, err := s.access(ctx, propertyID)
	if err != nil {
		return DirtyRooms{}, err
	}
	if minHours < 0 || minHours > 24*365 {
		return DirtyRooms{}, apperr.Invalid("the query is invalid", apperr.FieldError{Field: "min_hours", Code: "OUT_OF_RANGE", Message: "between 0 and 8760"})
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return DirtyRooms{}, err
	}
	rows, err := s.q(ctx).ReportHousekeepingDirty(ctx, reportsdb.ReportHousekeepingDirtyParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: day.BusinessDate, MinHours: int32(minHours)}) //nolint:gosec // G115: bounded above
	if err != nil {
		return DirtyRooms{}, err
	}
	out := DirtyRooms{MinHours: minHours, Rows: []DirtyRoom{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, DirtyRoom{
			RoomNumber: r.RoomNumber, Floor: deref(r.Floor), RoomType: r.RoomTypeCode, Status: r.Status, Since: r.Since.UTC().Format("2006-01-02 15:04") + " UTC",
			Hours: int(r.Hours), Occupancy: r.Occupancy, Priority: r.Priority, DND: r.Dnd, Block: deref(r.BlockType),
		})
	}
	return out, nil
}

// ---------------------------------------------------------------- maintenance

// MaintenanceLine is one category.
type MaintenanceLine struct {
	Category   string `json:"category"`
	Reported   int    `json:"reported"`
	Resolved   int    `json:"resolved"`
	Cancelled  int    `json:"cancelled"`
	StillOpen  int    `json:"still_open"`
	AvgHours   string `json:"avg_hours_to_resolve"`
	resolveSum decimal.Decimal
}

// MaintenanceBacklog is what is open now, whenever it was reported.
type MaintenanceBacklog struct {
	OpenNow      int `json:"open_now"`
	HighPriority int `json:"high_priority"`
	OldestHours  int `json:"oldest_hours"`
}

// MaintenanceReport summarises the requests reported in a range, with the backlog of today.
type MaintenanceReport struct {
	From    civil.Date         `json:"from"`
	To      civil.Date         `json:"to"`
	Lines   []MaintenanceLine  `json:"lines"`
	Totals  MaintenanceLine    `json:"totals"`
	Backlog MaintenanceBacklog `json:"backlog"`
}

func (r MaintenanceReport) CSV() ([]string, [][]string) {
	h := []string{"category", "reported", "resolved", "cancelled", "still_open", "avg_hours_to_resolve"}
	var rows [][]string
	for _, l := range r.Lines {
		rows = append(rows, []string{l.Category, itoa(l.Reported), itoa(l.Resolved), itoa(l.Cancelled), itoa(l.StillOpen), l.AvgHours})
	}
	return h, rows
}

// Maintenance reports the maintenance requests reported between two business dates.
func (s *Service) Maintenance(ctx context.Context, propertyID int64, from, to civil.Date) (MaintenanceReport, error) {
	p, _, err := s.access(ctx, propertyID)
	if err != nil {
		return MaintenanceReport{}, err
	}
	if err := checkRange(from, to); err != nil {
		return MaintenanceReport{}, err
	}
	q := s.q(ctx)
	rows, err := q.ReportMaintenanceByCategory(ctx, reportsdb.ReportMaintenanceByCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return MaintenanceReport{}, err
	}
	back, err := q.ReportMaintenanceBacklog(ctx, reportsdb.ReportMaintenanceBacklogParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return MaintenanceReport{}, err
	}
	out := MaintenanceReport{From: from, To: to, Lines: []MaintenanceLine{}, Totals: MaintenanceLine{Category: "Total"}, Backlog: MaintenanceBacklog{
		OpenNow: int(back.OpenNow), HighPriority: int(back.HighPriority), OldestHours: int(back.OldestHours),
	}}
	for _, r := range rows {
		l := MaintenanceLine{Category: r.Category, Reported: int(r.Reported), Resolved: int(r.Resolved), Cancelled: int(r.Cancelled), StillOpen: int(r.StillOpen), resolveSum: r.ResolveHours}
		l.AvgHours = avg(r.ResolveHours, l.Resolved)
		out.Lines = append(out.Lines, l)
		t := &out.Totals
		t.Reported, t.Resolved, t.Cancelled, t.StillOpen = t.Reported+l.Reported, t.Resolved+l.Resolved, t.Cancelled+l.Cancelled, t.StillOpen+l.StillOpen
		t.resolveSum = t.resolveSum.Add(r.ResolveHours)
	}
	out.Totals.AvgHours = avg(out.Totals.resolveSum, out.Totals.Resolved)
	return out, nil
}
