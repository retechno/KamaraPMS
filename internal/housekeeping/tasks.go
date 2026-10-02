package housekeeping

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/housekeeping/housekeepingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The daily cleaning list: tasks generated from stays and reservations (or added by a supervisor), assigned to
// housekeepers, started and finished. Starting a task moves a DIRTY room to CLEANING, finishing it moves the room to
// CLEAN (never to INSPECTED: that stays an inspector's decision).

// Task types, statuses and priorities.
const (
	TaskCheckout = "CHECKOUT"
	TaskStayover = "STAYOVER"
	TaskArrival  = "ARRIVAL"
	TaskDirty    = "DIRTY"
	TaskDeep     = "DEEP"
	TaskOther    = "OTHER"

	TaskPending    = "PENDING"
	TaskInProgress = "IN_PROGRESS"
	TaskDone       = "DONE"
	TaskSkipped    = "SKIPPED"

	PriorityNormal = "NORMAL"
	PriorityHigh   = "HIGH"

	maxAssignBatch = 500
	maxTaskRows    = 2000
)

var taskTypes = []string{TaskCheckout, TaskStayover, TaskArrival, TaskDirty, TaskDeep, TaskOther}

// Task is a row of the cleaning list.
type Task struct {
	ID              int64      `json:"id"`
	RoomID          int64      `json:"room_id"`
	RoomNumber      string     `json:"room_number"`
	Floor           string     `json:"floor,omitempty"`
	RoomTypeCode    string     `json:"room_type_code"`
	TaskDate        civil.Date `json:"task_date"`
	TaskType        string     `json:"task_type"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	Source          string     `json:"source"`
	AssignedTo      *int64     `json:"assigned_to"`
	AssigneeName    string     `json:"assignee_name,omitempty"`
	Notes           string     `json:"notes,omitempty"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	RoomStatus      Status     `json:"room_status"`
	RoomStatusSince time.Time  `json:"room_status_since"`
	DND             bool       `json:"dnd"`
	MakeUp          bool       `json:"make_up_requested"`
	FlagNote        string     `json:"flag_note,omitempty"`
}

// Workload is what one housekeeper has on the list.
type Workload struct {
	UserID     *int64 `json:"user_id"` // null: not assigned yet
	Name       string `json:"name"`
	Total      int    `json:"total"`
	Pending    int    `json:"pending"`
	InProgress int    `json:"in_progress"`
	Done       int    `json:"done"`
	Skipped    int    `json:"skipped"`
}

// TaskList is the cleaning list of a business date with the workload per person.
type TaskList struct {
	Date     civil.Date `json:"date"`
	Data     []Task     `json:"data"`
	Workload []Workload `json:"workload"`
}

// TaskFilter narrows the list.
type TaskFilter struct {
	Date       *civil.Date
	Status     string
	AssignedTo *int64
	Unassigned bool
	Floor      *string
}

// Staff is a user who can be given cleaning work.
type Staff struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// GenerateResult says how many tasks a run added.
type GenerateResult struct {
	Date    civil.Date `json:"date"`
	Created int        `json:"created"`
}

// TaskInput adds a task by hand.
type TaskInput struct {
	RoomID     int64  `json:"room_id"`
	TaskType   string `json:"task_type"`
	Priority   string `json:"priority"`
	AssignedTo *int64 `json:"assigned_to"`
	Notes      string `json:"notes"`
}

func taskNotFound() *apperr.Error {
	return apperr.NotFound("TASK_NOT_FOUND", "the housekeeping task does not exist in this property")
}

func toTask(r housekeepingdb.ListHousekeepingTasksRow) Task {
	return Task{
		ID: r.ID, RoomID: r.RoomID, RoomNumber: r.RoomNumber, Floor: deref(r.Floor), RoomTypeCode: r.RoomTypeCode, TaskDate: r.TaskDate,
		TaskType: r.TaskType, Status: r.Status, Priority: r.Priority, Source: r.Source, AssignedTo: r.AssignedTo, AssigneeName: deref(r.AssigneeName),
		Notes: deref(r.Notes), StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, RoomStatus: Status(r.RoomStatus), RoomStatusSince: r.RoomStatusSince,
		DND: r.Dnd, MakeUp: r.MakeUpRequested, FlagNote: deref(r.FlagNote),
	}
}

func taskAudit(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "housekeeping_task", EntityID: id, Old: old, New: updated}
}

// Staff lists who can be assigned cleaning work (housekeeping.assign).
func (s *Service) Staff(ctx context.Context, propertyID int64) ([]Staff, error) {
	p, err := s.assigner(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListHousekeepingStaff(ctx, housekeepingdb.ListHousekeepingStaffParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Staff, len(rows))
	for i, r := range rows {
		out[i] = Staff{ID: r.ID, FullName: r.FullName, Email: r.Email}
	}
	return out, nil
}

func (s *Service) assigner(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermHousekeepingAssign)
}

func (s *Service) updater(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermHousekeepingUpdate)
}

// requireStaff checks that an assignee can be given cleaning work at the property.
func (s *Service) requireStaff(ctx context.Context, tenantID, propertyID int64, userID *int64, field string) error {
	if userID == nil {
		return nil
	}
	rows, err := s.q(ctx).ListHousekeepingStaff(ctx, housekeepingdb.ListHousekeepingStaffParams{TenantID: tenantID, PropertyID: propertyID, UserID: userID})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return apperr.Invalid("the assignee is invalid", apperr.FieldError{Field: field, Code: "ASSIGNEE_INVALID", Message: "an active user who can do housekeeping at this property"})
	}
	return nil
}

// Tasks lists the cleaning list of a business date (default: the current one) with the workload per person.
func (s *Service) Tasks(ctx context.Context, propertyID int64, f TaskFilter) (TaskList, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return TaskList{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return TaskList{}, err
	}
	if f.Status != "" && !slices.Contains([]string{TaskPending, TaskInProgress, TaskDone, TaskSkipped}, f.Status) {
		return TaskList{}, apperr.Invalid("the filter is invalid", apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "PENDING, IN_PROGRESS, DONE or SKIPPED"})
	}
	var date civil.Date
	if f.Date != nil {
		date = *f.Date
	} else {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return TaskList{}, err
		}
		date = day.BusinessDate
	}
	arg := housekeepingdb.ListHousekeepingTasksParams{
		TenantID: p.TenantID, PropertyID: propertyID, TaskDate: date, AssignedTo: f.AssignedTo, Unassigned: f.Unassigned, Floor: f.Floor, RowLimit: maxTaskRows,
	}
	if f.Status != "" {
		arg.Status = &f.Status
	}
	rows, err := s.q(ctx).ListHousekeepingTasks(ctx, arg)
	if err != nil {
		return TaskList{}, err
	}
	out := TaskList{Date: date, Data: make([]Task, len(rows)), Workload: []Workload{}}
	index := map[int64]int{}
	var unassigned *Workload
	for i, r := range rows {
		t := toTask(r)
		out.Data[i] = t
		var w *Workload
		if t.AssignedTo == nil {
			if unassigned == nil {
				unassigned = &Workload{Name: "Not assigned"}
			}
			w = unassigned
		} else {
			j, ok := index[*t.AssignedTo]
			if !ok {
				out.Workload = append(out.Workload, Workload{UserID: t.AssignedTo, Name: t.AssigneeName})
				j = len(out.Workload) - 1
				index[*t.AssignedTo] = j
			}
			w = &out.Workload[j]
		}
		w.Total++
		switch t.Status {
		case TaskPending:
			w.Pending++
		case TaskInProgress:
			w.InProgress++
		case TaskDone:
			w.Done++
		case TaskSkipped:
			w.Skipped++
		}
	}
	if unassigned != nil {
		out.Workload = append(out.Workload, *unassigned)
	}
	return out, nil
}

// GenerateTasks adds the cleaning list of the current business date (housekeeping.assign): occupied rooms
// (CHECKOUT or STAYOVER), rooms a guest arrives into that are not ready, and other vacant rooms that are not clean.
// Running it again adds only what is new, so it can be run through the day.
func (s *Service) GenerateTasks(ctx context.Context, propertyID int64) (GenerateResult, error) {
	p, err := s.assigner(ctx, propertyID)
	if err != nil {
		return GenerateResult{}, err
	}
	var out GenerateResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		n, err := s.q(ctx).GenerateHousekeepingTasks(ctx, housekeepingdb.GenerateHousekeepingTasksParams{
			TenantID: p.TenantID, PropertyID: propertyID, TaskDate: day.BusinessDate, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = GenerateResult{Date: day.BusinessDate, Created: int(n)}
		return s.audit.Write(ctx, taskAudit(p, propertyID, day.BusinessDate, "housekeeping.tasks_generated", 0, nil, map[string]any{"created": n}))
	})
	return out, err
}

// CreateTask adds a task by hand for the current business date (housekeeping.assign).
func (s *Service) CreateTask(ctx context.Context, propertyID int64, in TaskInput) (Task, error) {
	p, err := s.assigner(ctx, propertyID)
	if err != nil {
		return Task{}, err
	}
	in.TaskType = strings.ToUpper(strings.TrimSpace(in.TaskType))
	in.Priority = strings.ToUpper(strings.TrimSpace(in.Priority))
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Priority == "" {
		in.Priority = PriorityNormal
	}
	var fields []apperr.FieldError
	if in.RoomID < 1 {
		fields = append(fields, apperr.FieldError{Field: "room_id", Code: "REQUIRED", Message: "a room"})
	}
	if !slices.Contains(taskTypes, in.TaskType) {
		fields = append(fields, apperr.FieldError{Field: "task_type", Code: "INVALID_VALUE", Message: strings.Join(taskTypes, ", ")})
	}
	if in.Priority != PriorityNormal && in.Priority != PriorityHigh {
		fields = append(fields, apperr.FieldError{Field: "priority", Code: "INVALID_VALUE", Message: "NORMAL or HIGH"})
	}
	if len([]rune(in.Notes)) > maxNotes {
		fields = append(fields, apperr.FieldError{Field: "notes", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	if len(fields) > 0 {
		return Task{}, apperr.Invalid("the task is invalid", fields...)
	}
	var id int64
	var bd civil.Date
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		bd = day.BusinessDate
		q := s.q(ctx)
		exists, err := q.RoomExists(ctx, housekeepingdb.RoomExistsParams{TenantID: p.TenantID, PropertyID: propertyID, RoomID: in.RoomID})
		if err != nil {
			return err
		}
		if !exists {
			return roomNotFound()
		}
		if err := s.requireStaff(ctx, p.TenantID, propertyID, in.AssignedTo, "assigned_to"); err != nil {
			return err
		}
		row, err := q.InsertManualTask(ctx, housekeepingdb.InsertManualTaskParams{
			TenantID: p.TenantID, PropertyID: propertyID, RoomID: in.RoomID, TaskDate: bd, TaskType: in.TaskType, Priority: in.Priority,
			AssignedTo: in.AssignedTo, Now: s.clock.Now(), Notes: nullableText(in.Notes), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		id = row.ID
		return s.audit.Write(ctx, taskAudit(p, propertyID, bd, "housekeeping.task_created", id, nil, map[string]any{"room_id": in.RoomID, "type": in.TaskType, "assigned_to": in.AssignedTo}))
	})
	if err != nil {
		return Task{}, err
	}
	return s.task(ctx, p.TenantID, propertyID, bd, id)
}

// task reads one task of the list of a date.
func (s *Service) task(ctx context.Context, tenantID, propertyID int64, date civil.Date, id int64) (Task, error) {
	row, err := s.q(ctx).GetHousekeepingTask(ctx, housekeepingdb.GetHousekeepingTaskParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, taskNotFound()
	}
	if err != nil {
		return Task{}, err
	}
	rows, err := s.q(ctx).ListHousekeepingTasks(ctx, housekeepingdb.ListHousekeepingTasksParams{TenantID: tenantID, PropertyID: propertyID, TaskDate: row.TaskDate, RowLimit: maxTaskRows})
	if err != nil {
		return Task{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return toTask(r), nil
		}
	}
	return Task{}, taskNotFound()
}

// AssignTasks gives tasks to a housekeeper, or takes them back with a null user (housekeeping.assign). Only tasks that
// are still open can be assigned; if any of them is not, nothing changes (409 TASK_NOT_ASSIGNABLE).
func (s *Service) AssignTasks(ctx context.Context, propertyID int64, taskIDs []int64, userID *int64) (int, error) {
	p, err := s.assigner(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	ids := slices.Clone(taskIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 || len(ids) > maxAssignBatch {
		return 0, apperr.Invalid("the assignment is invalid", apperr.FieldError{Field: "task_ids", Code: "OUT_OF_RANGE", Message: "between 1 and 500 tasks"})
	}
	var n int
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.requireStaff(ctx, p.TenantID, propertyID, userID, "user_id"); err != nil {
			return err
		}
		done, err := s.q(ctx).AssignHousekeepingTasks(ctx, housekeepingdb.AssignHousekeepingTasksParams{
			TenantID: p.TenantID, PropertyID: propertyID, Ids: ids, AssignedTo: userID, Now: s.clock.Now(),
		})
		if err != nil {
			return err
		}
		if len(done) != len(ids) {
			var missing []int64
			for _, id := range ids {
				if !slices.Contains(done, id) {
					missing = append(missing, id)
				}
			}
			return apperr.Conflict("TASK_NOT_ASSIGNABLE", "a task does not exist or is already finished").WithContext("task_ids", missing)
		}
		n = len(done)
		return s.audit.Write(ctx, taskAudit(p, propertyID, day.BusinessDate, "housekeeping.tasks_assigned", 0, nil, map[string]any{"task_ids": ids, "user_id": userID}))
	})
	return n, err
}

// lockTaskRoom reads a task and locks its room's housekeeping row (level 30). The task row itself is changed only
// afterwards, by a conditional update, so every use case takes the room lock before touching a task.
func (s *Service) lockTaskRoom(ctx context.Context, tenantID, propertyID, taskID int64) (housekeepingdb.HousekeepingTask, Status, error) {
	row, err := s.q(ctx).GetHousekeepingTask(ctx, housekeepingdb.GetHousekeepingTaskParams{TenantID: tenantID, PropertyID: propertyID, ID: taskID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, "", taskNotFound()
	}
	if err != nil {
		return row, "", err
	}
	from, err := s.lockStatus(ctx, tenantID, propertyID, row.RoomID)
	return row, from, err
}

// StartTask starts a pending task (housekeeping.update): an unassigned task goes to the person who starts it, and a
// DIRTY room becomes CLEANING.
func (s *Service) StartTask(ctx context.Context, propertyID, taskID int64) (Task, error) {
	p, err := s.updater(ctx, propertyID)
	if err != nil {
		return Task{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		task, from, err := s.lockTaskRoom(ctx, p.TenantID, propertyID, taskID)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).StartHousekeepingTask(ctx, housekeepingdb.StartHousekeepingTaskParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: taskID, Now: s.clock.Now(), ActorID: p.ActorID(),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Conflict("TASK_NOT_PENDING", "only a pending task can be started").WithContext("status", task.Status)
			}
			return err
		}
		if from == Dirty {
			if _, err := s.apply(ctx, change{
				TenantID: p.TenantID, PropertyID: propertyID, RoomID: task.RoomID, From: from, To: Cleaning, Source: SourceManual,
				Notes: "Cleaning started (task)", BusinessDate: day.BusinessDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, taskAudit(p, propertyID, day.BusinessDate, "housekeeping.task_started", taskID, map[string]any{"status": task.Status}, map[string]any{"status": TaskInProgress}))
	})
	if err != nil {
		return Task{}, err
	}
	return s.task(ctx, p.TenantID, propertyID, civil.Date{}, taskID)
}

// CompleteTask finishes a task (housekeeping.update): a DIRTY or CLEANING room becomes CLEAN. A room that is already
// CLEAN or INSPECTED keeps its status.
func (s *Service) CompleteTask(ctx context.Context, propertyID, taskID int64, notes string) (Task, error) {
	p, err := s.updater(ctx, propertyID)
	if err != nil {
		return Task{}, err
	}
	notes = strings.TrimSpace(notes)
	if len([]rune(notes)) > maxNotes {
		return Task{}, apperr.Invalid("the notes are too long", apperr.FieldError{Field: "notes", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		task, from, err := s.lockTaskRoom(ctx, p.TenantID, propertyID, taskID)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).CompleteHousekeepingTask(ctx, housekeepingdb.CompleteHousekeepingTaskParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: taskID, Now: s.clock.Now(), ActorID: p.ActorID(), Notes: nullableText(notes),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Conflict("TASK_ALREADY_CLOSED", "the task is already finished").WithContext("status", task.Status)
			}
			return err
		}
		if from == Dirty || from == Cleaning {
			if _, err := s.apply(ctx, change{
				TenantID: p.TenantID, PropertyID: propertyID, RoomID: task.RoomID, From: from, To: Clean, Source: SourceManual,
				Notes: "Cleaning finished (task)", BusinessDate: day.BusinessDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, taskAudit(p, propertyID, day.BusinessDate, "housekeeping.task_completed", taskID, map[string]any{"status": task.Status}, map[string]any{"status": TaskDone}))
	})
	if err != nil {
		return Task{}, err
	}
	return s.task(ctx, p.TenantID, propertyID, civil.Date{}, taskID)
}

// SkipTask closes a task without cleaning (housekeeping.update), for example because the guest asked not to be
// disturbed. A reason is required. The room's status does not change.
func (s *Service) SkipTask(ctx context.Context, propertyID, taskID int64, reason string) (Task, error) {
	p, err := s.updater(ctx, propertyID)
	if err != nil {
		return Task{}, err
	}
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return Task{}, apperr.Invalid("the reason is required", apperr.FieldError{Field: "reason", Code: "REQUIRED", Message: "why the room is not cleaned"})
	case len([]rune(reason)) > maxNotes:
		return Task{}, apperr.Invalid("the reason is too long", apperr.FieldError{Field: "reason", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		task, _, err := s.lockTaskRoom(ctx, p.TenantID, propertyID, taskID)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).SkipHousekeepingTask(ctx, housekeepingdb.SkipHousekeepingTaskParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: taskID, Now: s.clock.Now(), ActorID: p.ActorID(), Reason: &reason,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Conflict("TASK_ALREADY_CLOSED", "the task is already finished").WithContext("status", task.Status)
			}
			return err
		}
		return s.audit.Write(ctx, taskAudit(p, propertyID, day.BusinessDate, "housekeeping.task_skipped", taskID, map[string]any{"status": task.Status}, map[string]any{"status": TaskSkipped, "reason": reason}))
	})
	if err != nil {
		return Task{}, err
	}
	return s.task(ctx, p.TenantID, propertyID, civil.Date{}, taskID)
}

func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
