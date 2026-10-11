// Package maintenance keeps the maintenance requests of a property: a problem is reported (against a room or a place),
// assigned to a technician, worked on and resolved. A request about a room can take the room out of sale through a room
// block (OOO or OOS), which is created and released together with the request's own changes.
package maintenance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/maintenance/maintenancedb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms"
	"kamarapms/internal/tenancy"
)

// Categories, priorities and statuses.
const (
	StatusOpen       = "OPEN"
	StatusInProgress = "IN_PROGRESS"
	StatusResolved   = "RESOLVED"
	StatusCancelled  = "CANCELLED"

	maxText    = 1000
	maxPlace   = 150
	maxListing = 200
)

var (
	categories = []string{"PLUMBING", "ELECTRICAL", "AC", "FURNITURE", "APPLIANCE", "OTHER"}
	priorities = []string{"LOW", "NORMAL", "HIGH", "URGENT"}
)

// BlockBrief is the room block taken for a request.
type BlockBrief struct {
	ID        int64      `json:"id"`
	BlockType string     `json:"block_type"`
	StartDate civil.Date `json:"start_date"`
	EndDate   civil.Date `json:"end_date"`
	Status    string     `json:"status"`
}

// Request is a maintenance request.
type Request struct {
	ID             int64       `json:"id"`
	RequestNumber  string      `json:"request_number"`
	RoomID         *int64      `json:"room_id"`
	RoomNumber     string      `json:"room_number,omitempty"`
	Location       string      `json:"location,omitempty"`
	Category       string      `json:"category"`
	Description    string      `json:"description"`
	Priority       string      `json:"priority"`
	Status         string      `json:"status"`
	BusinessDate   civil.Date  `json:"business_date"`
	ReportedBy     *int64      `json:"reported_by"`
	ReporterName   string      `json:"reporter_name,omitempty"`
	ReportedAt     time.Time   `json:"reported_at"`
	AssignedTo     *int64      `json:"assigned_to"`
	AssigneeName   string      `json:"assignee_name,omitempty"`
	StartedAt      *time.Time  `json:"started_at"`
	ClosedAt       *time.Time  `json:"closed_at"`
	ResolutionNote string      `json:"resolution_note,omitempty"`
	Block          *BlockBrief `json:"block"`
}

// Staff is a user who can take maintenance work.
type Staff struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// CreateInput reports a problem.
type CreateInput struct {
	RoomID      *int64 `json:"room_id"`
	Location    string `json:"location"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// Patch changes the details of an open request; nil fields stay as they are.
type Patch struct {
	Location    *string `json:"location"`
	Category    *string `json:"category"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
}

// CloseInput resolves or cancels a request. ReleaseBlock also cancels the room block taken for it.
type CloseInput struct {
	Note         string `json:"note"`
	ReleaseBlock bool   `json:"release_block"`
}

// BlockInput takes the request's room out of sale.
type BlockInput struct {
	BlockType string      `json:"block_type"`
	StartDate *civil.Date `json:"start_date"`
	EndDate   civil.Date  `json:"end_date"`
}

// Filter narrows the list.
type Filter struct {
	Status     string
	OpenOnly   bool
	RoomID     *int64
	AssignedTo *int64
	Category   string
	Priority   string
}

// Service is the maintenance application service. Reporting and reading need maintenance.report; everything that
// changes a request needs maintenance.manage (and room_block.manage for the room block).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	rooms *rooms.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, r *rooms.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, rooms: r}
}

func (s *Service) q(ctx context.Context) *maintenancedb.Queries {
	return maintenancedb.New(s.txm.DB(ctx))
}

func errNotFound() *apperr.Error {
	return apperr.NotFound("MAINTENANCE_REQUEST_NOT_FOUND", "the maintenance request does not exist in this property")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func (s *Service) reporter(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermMaintenanceReport)
}

func (s *Service) manager(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermMaintenanceManage)
}

func entry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "maintenance_request", EntityID: id, EntityLabel: label, Old: old, New: updated}
}

func view(r maintenancedb.ListRequestsRow) Request {
	out := Request{
		ID: r.ID, RequestNumber: r.RequestNumber, RoomID: r.RoomID, RoomNumber: deref(r.RoomNumber), Location: deref(r.Location), Category: r.Category,
		Description: r.Description, Priority: r.Priority, Status: r.Status, BusinessDate: r.BusinessDate, ReportedBy: r.ReportedBy, ReporterName: deref(r.ReporterName),
		ReportedAt: r.ReportedAt, AssignedTo: r.AssignedTo, AssigneeName: deref(r.AssigneeName), StartedAt: r.StartedAt, ClosedAt: r.ClosedAt,
		ResolutionNote: deref(r.ResolutionNote),
	}
	if r.RoomBlockID != nil && r.BlockType != nil && r.BlockStart != nil && r.BlockEnd != nil {
		out.Block = &BlockBrief{ID: *r.RoomBlockID, BlockType: *r.BlockType, StartDate: *r.BlockStart, EndDate: *r.BlockEnd, Status: deref(r.BlockStatus)}
	}
	return out
}

// Staff lists who can take maintenance work (maintenance.manage).
func (s *Service) Staff(ctx context.Context, propertyID int64) ([]Staff, error) {
	p, err := s.manager(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListMaintenanceStaff(ctx, maintenancedb.ListMaintenanceStaffParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Staff, len(rows))
	for i, r := range rows {
		out[i] = Staff{ID: r.ID, FullName: r.FullName, Email: r.Email}
	}
	return out, nil
}

// List lists requests, newest first, before the id beforeID (maintenance.report).
func (s *Service) List(ctx context.Context, propertyID int64, f Filter, beforeID *int64, limit int) ([]Request, error) {
	p, err := s.reporter(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var fields []apperr.FieldError
	if f.Status != "" && !slices.Contains([]string{StatusOpen, StatusInProgress, StatusResolved, StatusCancelled}, f.Status) {
		fields = append(fields, fieldErr("status", "INVALID_VALUE", "OPEN, IN_PROGRESS, RESOLVED or CANCELLED"))
	}
	if f.Category != "" && !slices.Contains(categories, f.Category) {
		fields = append(fields, fieldErr("category", "INVALID_VALUE", strings.Join(categories, ", ")))
	}
	if f.Priority != "" && !slices.Contains(priorities, f.Priority) {
		fields = append(fields, fieldErr("priority", "INVALID_VALUE", strings.Join(priorities, ", ")))
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the filter is invalid", fields...)
	}
	return s.list(ctx, p.TenantID, propertyID, f, nil, beforeID, limit)
}

func (s *Service) list(ctx context.Context, tenantID, propertyID int64, f Filter, id, beforeID *int64, limit int) ([]Request, error) {
	arg := maintenancedb.ListRequestsParams{
		TenantID: tenantID, PropertyID: propertyID, ID: id, BeforeID: beforeID, OpenOnly: f.OpenOnly, RoomID: f.RoomID, AssignedTo: f.AssignedTo,
		RowLimit: int32(max(1, min(limit, maxListing+1))), //nolint:gosec // G115: bounded
	}
	if f.Status != "" {
		arg.Status = &f.Status
	}
	if f.Category != "" {
		arg.Category = &f.Category
	}
	if f.Priority != "" {
		arg.Priority = &f.Priority
	}
	rows, err := s.q(ctx).ListRequests(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]Request, len(rows))
	for i, r := range rows {
		out[i] = view(r)
	}
	return out, nil
}

// Get returns one request (maintenance.report).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Request, error) {
	p, err := s.reporter(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

func (s *Service) load(ctx context.Context, tenantID, propertyID, id int64) (Request, error) {
	rows, err := s.list(ctx, tenantID, propertyID, Filter{}, &id, nil, 1)
	if err != nil {
		return Request{}, err
	}
	if len(rows) == 0 {
		return Request{}, errNotFound()
	}
	return rows[0], nil
}

func validate(category, description, priority, location string, hasRoom bool) []apperr.FieldError {
	var fields []apperr.FieldError
	if !slices.Contains(categories, category) {
		fields = append(fields, fieldErr("category", "INVALID_VALUE", strings.Join(categories, ", ")))
	}
	if description == "" || len([]rune(description)) > maxText {
		fields = append(fields, fieldErr("description", "REQUIRED", "1-1000 characters"))
	}
	if !slices.Contains(priorities, priority) {
		fields = append(fields, fieldErr("priority", "INVALID_VALUE", strings.Join(priorities, ", ")))
	}
	if len([]rune(location)) > maxPlace {
		fields = append(fields, fieldErr("location", "TOO_LONG", "at most 150 characters"))
	}
	if !hasRoom && location == "" {
		fields = append(fields, fieldErr("location", "REQUIRED", "a room or a place"))
	}
	return fields
}

// Create reports a problem (maintenance.report): against a room, or against a place such as "Lobby". The request
// starts OPEN.
func (s *Service) Create(ctx context.Context, propertyID int64, in CreateInput) (Request, error) {
	p, err := s.reporter(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	in.Category = strings.ToUpper(strings.TrimSpace(in.Category))
	in.Priority = strings.ToUpper(strings.TrimSpace(in.Priority))
	in.Description = strings.TrimSpace(in.Description)
	in.Location = strings.TrimSpace(in.Location)
	if in.Priority == "" {
		in.Priority = "NORMAL"
	}
	if in.RoomID != nil && *in.RoomID < 1 {
		in.RoomID = nil
	}
	if fields := validate(in.Category, in.Description, in.Priority, in.Location, in.RoomID != nil); len(fields) > 0 {
		return Request{}, apperr.Invalid("the request is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if in.RoomID != nil {
			if _, err := q.RoomNumber(ctx, maintenancedb.RoomNumberParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.RoomID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
				}
				return err
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqMaintenance) // last in the lock order
		if err != nil {
			return err
		}
		id, err = q.InsertRequest(ctx, maintenancedb.InsertRequestParams{
			TenantID: p.TenantID, PropertyID: propertyID, RequestNumber: number, RoomID: in.RoomID, Location: nullable(in.Location), Category: in.Category,
			Description: in.Description, Priority: in.Priority, BusinessDate: day.BusinessDate, ActorID: p.ActorID(), Now: s.clock.Now(),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "maintenance.reported", id, number, nil, map[string]any{
			"request_number": number, "room_id": in.RoomID, "location": in.Location, "category": in.Category, "priority": in.Priority,
		}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

// change runs a use case on one request: business day (share), then the request row, then whatever the use case
// locks (rooms), always in that order. The request must be open (OPEN or IN_PROGRESS) unless allowClosed.
func (s *Service) change(ctx context.Context, propertyID, id int64, perm auth.Permission, f func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (action string, detail any, err error)) (Request, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Request{}, err
	}
	if err := s.authz.Require(ctx, propertyID, perm); err != nil {
		return Request{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cur, err := s.q(ctx).GetRequestForUpdate(ctx, maintenancedb.GetRequestForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound()
		}
		if err != nil {
			return err
		}
		action, detail, err := f(ctx, p, day.BusinessDate, cur)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, action, id, cur.RequestNumber, map[string]any{"status": cur.Status}, detail))
	})
	if err != nil {
		return Request{}, err
	}
	return s.load(ctx, p.TenantID, propertyID, id)
}

func requireOpen(cur maintenancedb.MaintenanceRequest) error {
	if cur.Status != StatusOpen && cur.Status != StatusInProgress {
		return apperr.Conflict("REQUEST_NOT_OPEN", "the request is already closed").WithContext("status", cur.Status)
	}
	return nil
}

// Update changes the details of an open request (maintenance.manage).
func (s *Service) Update(ctx context.Context, propertyID, id int64, patch Patch) (Request, error) {
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if err := requireOpen(cur); err != nil {
			return "", nil, err
		}
		category, description, priority, location := cur.Category, cur.Description, cur.Priority, deref(cur.Location)
		if patch.Category != nil {
			category = strings.ToUpper(strings.TrimSpace(*patch.Category))
		}
		if patch.Description != nil {
			description = strings.TrimSpace(*patch.Description)
		}
		if patch.Priority != nil {
			priority = strings.ToUpper(strings.TrimSpace(*patch.Priority))
		}
		if patch.Location != nil {
			location = strings.TrimSpace(*patch.Location)
		}
		if fields := validate(category, description, priority, location, cur.RoomID != nil); len(fields) > 0 {
			return "", nil, apperr.Invalid("the request is invalid", fields...)
		}
		if err := s.q(ctx).UpdateRequestDetails(ctx, maintenancedb.UpdateRequestDetailsParams{
			TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Category: category, Description: description, Priority: priority, Location: nullable(location),
		}); err != nil {
			return "", nil, err
		}
		return "maintenance.updated", map[string]any{"category": category, "priority": priority, "location": location}, nil
	})
}

// Assign gives an open request to a technician, or takes it back with a null user (maintenance.manage).
func (s *Service) Assign(ctx context.Context, propertyID, id int64, userID *int64) (Request, error) {
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if err := requireOpen(cur); err != nil {
			return "", nil, err
		}
		q := s.q(ctx)
		if userID != nil {
			staff, err := q.ListMaintenanceStaff(ctx, maintenancedb.ListMaintenanceStaffParams{TenantID: p.TenantID, PropertyID: cur.PropertyID, UserID: userID})
			if err != nil {
				return "", nil, err
			}
			if len(staff) == 0 {
				return "", nil, apperr.Invalid("the assignee is invalid", fieldErr("user_id", "ASSIGNEE_INVALID", "an active user who can take maintenance work at this property"))
			}
		}
		if err := q.AssignRequest(ctx, maintenancedb.AssignRequestParams{TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, AssignedTo: userID, Now: s.clock.Now()}); err != nil {
			return "", nil, err
		}
		return "maintenance.assigned", map[string]any{"assigned_to": userID}, nil
	})
}

// Start begins work on an OPEN request (maintenance.manage); an unassigned request goes to the person who starts it.
func (s *Service) Start(ctx context.Context, propertyID, id int64) (Request, error) {
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if cur.Status != StatusOpen {
			return "", nil, apperr.Conflict("REQUEST_NOT_OPEN", "only an open request can be started").WithContext("status", cur.Status)
		}
		if err := s.q(ctx).StartRequest(ctx, maintenancedb.StartRequestParams{TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Now: s.clock.Now(), ActorID: p.ActorID()}); err != nil {
			return "", nil, err
		}
		return "maintenance.started", map[string]any{"status": StatusInProgress}, nil
	})
}

// closeRequest ends a request as RESOLVED or CANCELLED; with ReleaseBlock the room block taken for it is cancelled
// too (room_block.manage). A cancelled request needs a note saying why.
func (s *Service) closeRequest(ctx context.Context, propertyID, id int64, status string, in CloseInput) (Request, error) {
	in.Note = strings.TrimSpace(in.Note)
	switch {
	case len([]rune(in.Note)) > maxText:
		return Request{}, apperr.Invalid("the note is too long", fieldErr("note", "TOO_LONG", "at most 1000 characters"))
	case status == StatusCancelled && in.Note == "":
		return Request{}, apperr.Invalid("a reason is required", fieldErr("note", "REQUIRED", "why the request is cancelled"))
	}
	action := "maintenance.resolved"
	if status == StatusCancelled {
		action = "maintenance.cancelled"
	}
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if err := requireOpen(cur); err != nil {
			return "", nil, err
		}
		released := false
		if in.ReleaseBlock && cur.RoomBlockID != nil {
			row, err := s.rooms.GetBlock(ctx, cur.PropertyID, *cur.RoomBlockID)
			if err != nil {
				return "", nil, err
			}
			if row.Status == rooms.BlockActive {
				if _, err := s.rooms.CancelBlock(ctx, cur.PropertyID, *cur.RoomBlockID, "Maintenance request "+cur.RequestNumber+" "+strings.ToLower(status)); err != nil {
					return "", nil, err
				}
				released = true
			}
		}
		if err := s.q(ctx).CloseRequest(ctx, maintenancedb.CloseRequestParams{
			TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, Status: status, Now: s.clock.Now(), ActorID: p.ActorID(), Note: nullable(in.Note),
		}); err != nil {
			return "", nil, err
		}
		return action, map[string]any{"status": status, "note": in.Note, "block_released": released}, nil
	})
}

// Resolve marks an open request as repaired (maintenance.manage).
func (s *Service) Resolve(ctx context.Context, propertyID, id int64, in CloseInput) (Request, error) {
	return s.closeRequest(ctx, propertyID, id, StatusResolved, in)
}

// Cancel closes an open request that is not needed (maintenance.manage); a reason is required.
func (s *Service) Cancel(ctx context.Context, propertyID, id int64, in CloseInput) (Request, error) {
	return s.closeRequest(ctx, propertyID, id, StatusCancelled, in)
}

// Reopen puts a RESOLVED request back to OPEN (maintenance.manage), for a repair that did not hold.
func (s *Service) Reopen(ctx context.Context, propertyID, id int64) (Request, error) {
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if cur.Status != StatusResolved {
			return "", nil, apperr.Conflict("REQUEST_NOT_RESOLVED", "only a resolved request can be reopened").WithContext("status", cur.Status)
		}
		if err := s.q(ctx).ReopenRequest(ctx, maintenancedb.ReopenRequestParams{TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID}); err != nil {
			return "", nil, err
		}
		return "maintenance.reopened", map[string]any{"status": StatusOpen}, nil
	})
}

// Block takes the request's room out of sale (maintenance.manage and room_block.manage): an OOO or OOS room block
// from the start date (default today) to the end date (exclusive), with the request as its reason. The block can be
// released when the request is resolved or cancelled. A request has one live block at a time (409
// `REQUEST_ALREADY_BLOCKED`); the block can fail like any room block (409 `ROOM_BLOCK_CONFLICT`).
func (s *Service) Block(ctx context.Context, propertyID, id int64, in BlockInput) (Request, error) {
	in.BlockType = strings.ToUpper(strings.TrimSpace(in.BlockType))
	return s.change(ctx, propertyID, id, auth.PermMaintenanceManage, func(ctx context.Context, p auth.Principal, bd civil.Date, cur maintenancedb.MaintenanceRequest) (string, any, error) {
		if err := requireOpen(cur); err != nil {
			return "", nil, err
		}
		if cur.RoomID == nil {
			return "", nil, apperr.Conflict("REQUEST_HAS_NO_ROOM", "only a request about a room can block a room")
		}
		if cur.RoomBlockID != nil {
			b, err := s.rooms.GetBlock(ctx, cur.PropertyID, *cur.RoomBlockID)
			if err != nil {
				return "", nil, err
			}
			if b.Status == rooms.BlockActive {
				return "", nil, apperr.Conflict("REQUEST_ALREADY_BLOCKED", "the room is already blocked for this request").WithContext("room_block_id", b.ID)
			}
		}
		start := bd
		if in.StartDate != nil {
			start = *in.StartDate
		}
		reason := cur.RequestNumber + ": " + cur.Description
		if r := []rune(reason); len(r) > 500 {
			reason = string(r[:497]) + "..."
		}
		block, err := s.rooms.CreateBlock(ctx, cur.PropertyID, rooms.CreateBlockInput{RoomID: *cur.RoomID, BlockType: in.BlockType, StartDate: start, EndDate: in.EndDate, Reason: reason})
		if err != nil {
			return "", nil, err
		}
		if err := s.q(ctx).LinkRequestBlock(ctx, maintenancedb.LinkRequestBlockParams{TenantID: p.TenantID, PropertyID: cur.PropertyID, ID: cur.ID, RoomBlockID: &block.ID}); err != nil {
			return "", nil, err
		}
		return "maintenance.room_blocked", map[string]any{"room_block_id": block.ID, "type": block.BlockType, "start": block.StartDate, "end": block.EndDate}, nil
	})
}
