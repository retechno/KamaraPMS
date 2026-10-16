package pos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/pos/posdb"
)

// The kitchen display and the queue of print jobs (docs/architecture/34-pos.md 10.2, 19.2). A ticket is one station's part of a round;
// its status is NEW → COOKING → READY → SERVED (D19: per ticket). A line that was voided stays on the ticket, struck through.

// Ticket statuses.
const (
	TicketNew     = "NEW"
	TicketCooking = "COOKING"
	TicketReady   = "READY"
	TicketServed  = "SERVED"
)

// Ticket actions of the kitchen and the waiter.
const (
	TicketStart       = "START"
	TicketReadyAction = "READY"
	TicketServe       = "SERVE"
)

// TicketLine is a line of a ticket.
type TicketLine struct {
	ID       int64           `json:"id"`
	Quantity decimal.Decimal `json:"quantity"`
	Name     string          `json:"name"`
	Options  string          `json:"options,omitempty"`
	Note     string          `json:"note,omitempty"`
	Void     bool            `json:"is_void"`
}

// KitchenTicket is a ticket on the board of a station.
type KitchenTicket struct {
	ID           int64        `json:"id"`
	Kind         string       `json:"kind"`
	Status       string       `json:"status"`
	RoundNo      int          `json:"round_no"`
	IsAddition   bool         `json:"is_addition"`
	BillID       int64        `json:"bill_id"`
	BillNumber   string       `json:"bill_number"`
	OutletID     int64        `json:"outlet_id"`
	OutletName   string       `json:"outlet_name"`
	OrderType    string       `json:"order_type"`
	TableCode    string       `json:"table_code,omitempty"`
	RoomNumber   string       `json:"room_number,omitempty"`
	CustomerName string       `json:"customer_name,omitempty"`
	Waiter       string       `json:"waiter,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	StartedAt    *time.Time   `json:"started_at"`
	ReadyAt      *time.Time   `json:"ready_at"`
	ServedAt     *time.Time   `json:"served_at"`
	WaitSeconds  int          `json:"wait_seconds"`
	Late         bool         `json:"is_late"`
	Lines        []TicketLine `json:"lines"`
}

// OutletRef names an outlet a station serves.
type OutletRef struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// StationBoard is the kitchen display of one station.
type StationBoard struct {
	Station            Station         `json:"station"`
	Outlets            []OutletRef     `json:"outlets"`
	Now                time.Time       `json:"now"`
	AverageWaitSeconds int             `json:"average_wait_seconds"`
	LateCount          int             `json:"late_count"`
	Tickets            []KitchenTicket `json:"tickets"`
	History            []KitchenTicket `json:"history"`
}

// TicketMoved is the answer of a change of status.
type TicketMoved struct {
	ID        int64  `json:"id"`
	StationID *int64 `json:"station_id"`
	Status    string `json:"status"`
	Changed   bool   `json:"changed"`
}

var kitchenReaders = []auth.Permission{auth.PermPOSKDS, auth.PermPOSManage}

// StationBoard reads the display of a station: the open tickets (new, cooking, ready) and the tickets served in the last hour.
func (s *Service) StationBoard(ctx context.Context, propertyID, stationID int64) (StationBoard, error) {
	p, err := s.canRead(ctx, propertyID, kitchenReaders...)
	if err != nil {
		return StationBoard{}, err
	}
	stations, err := s.ListStations(ctx, propertyID)
	if err != nil {
		return StationBoard{}, err
	}
	var st *Station
	for i := range stations {
		if stations[i].ID == stationID {
			st = &stations[i]
		}
	}
	if st == nil {
		return StationBoard{}, notFound("STATION_NOT_FOUND", "station")
	}
	q := s.q(ctx)
	now := s.clock.Now()
	rows, err := q.StationBoardTickets(ctx, posdb.StationBoardTicketsParams{TenantID: p.TenantID, PropertyID: propertyID, StationID: &stationID, WithHistory: true, HistoryFrom: now.Add(-time.Hour)})
	if err != nil {
		return StationBoard{}, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	lineRows, err := q.StationBoardLines(ctx, posdb.StationBoardLinesParams{TenantID: p.TenantID, PropertyID: propertyID, TicketIds: ids})
	if err != nil {
		return StationBoard{}, err
	}
	lines := map[int64][]TicketLine{}
	for _, l := range lineRows {
		lines[l.TicketID] = append(lines[l.TicketID], TicketLine{ID: l.ID, Quantity: l.Quantity, Name: l.Name, Options: l.OptionsText, Note: l.Note, Void: l.IsVoid})
	}
	outlets, err := q.StationOutlets(ctx, posdb.StationOutletsParams{TenantID: p.TenantID, PropertyID: propertyID, StationID: &stationID})
	if err != nil {
		return StationBoard{}, err
	}
	out := StationBoard{Station: *st, Now: now, Outlets: make([]OutletRef, len(outlets)), Tickets: []KitchenTicket{}, History: []KitchenTicket{}}
	for i, o := range outlets {
		out.Outlets[i] = OutletRef{ID: o.ID, Code: o.Code, Name: o.Name}
	}
	var sum, n int
	for _, r := range rows {
		t := KitchenTicket{ID: r.ID, Kind: r.Kind, Status: r.Status, RoundNo: int(r.RoundNo), IsAddition: r.RoundNo > 1, BillID: r.BillID, BillNumber: r.BillNumber,
			OutletID: r.OutletID, OutletName: r.OutletName, OrderType: r.OrderType, TableCode: r.TableCode, RoomNumber: r.RoomNumber, CustomerName: r.CustomerName, Waiter: r.WaiterName,
			CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, ReadyAt: r.ReadyAt, ServedAt: r.ServedAt, Lines: lines[r.ID]}
		if t.Lines == nil {
			t.Lines = []TicketLine{}
		}
		switch r.Status {
		case TicketNew, TicketCooking:
			t.WaitSeconds = secondsSince(now, r.CreatedAt)
			t.Late = t.WaitSeconds > st.LateAfterMinutes*60
			if t.Late {
				out.LateCount++
			}
		case TicketReady:
			t.WaitSeconds = secondsSince(now, timeOr(r.ReadyAt, r.CreatedAt))
		default:
			t.WaitSeconds = secondsSince(now, timeOr(r.ServedAt, r.CreatedAt))
		}
		if r.ReadyAt != nil {
			sum += int(r.ReadyAt.Sub(r.CreatedAt).Seconds())
			n++
		}
		if r.Status == TicketServed {
			out.History = append(out.History, t)
		} else {
			out.Tickets = append(out.Tickets, t)
		}
	}
	if n > 0 {
		out.AverageWaitSeconds = sum / n
	}
	return out, nil
}

func secondsSince(now, then time.Time) int {
	if s := int(now.Sub(then).Seconds()); s > 0 {
		return s
	}
	return 0
}

func timeOr(t *time.Time, fallback time.Time) time.Time {
	if t != nil {
		return *t
	}
	return fallback
}

// AdvanceTicket moves a ticket on: START (new → cooking), READY (new or cooking → ready) and SERVE (ready → served). The change is one
// conditional statement, so two screens that press the same button at once make one change and both get the same answer; a button for a
// status the ticket has already passed is a conflict.
func (s *Service) AdvanceTicket(ctx context.Context, propertyID, ticketID int64, action string) (TicketMoved, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return TicketMoved{}, err
	}
	// who may touch a ticket at all is asked before the action is read, so that a caller without a permission is told so whatever they sent
	if err := auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSKDS, auth.PermPOSOrder, auth.PermPOSCashier); err != nil {
		return TicketMoved{}, err
	}
	var to, auditAction string
	var from []string
	switch action {
	case TicketStart:
		to, from, auditAction = TicketCooking, []string{TicketNew}, "pos.ticket_started"
		err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSKDS)
	case TicketReadyAction:
		to, from, auditAction = TicketReady, []string{TicketCooking, TicketNew}, "pos.ticket_ready"
		err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSKDS)
	case TicketServe:
		to, from, auditAction = TicketServed, []string{TicketReady}, "pos.ticket_served"
		err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSKDS, auth.PermPOSOrder, auth.PermPOSCashier)
	default:
		return TicketMoved{}, apperr.Invalid("the action is invalid", fieldErr("action", "INVALID_VALUE", "START, READY or SERVE"))
	}
	if err != nil {
		return TicketMoved{}, err
	}
	var res TicketMoved
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		for attempt := 0; attempt < 3; attempt++ {
			cur, err := q.GetTicketForUpdateState(ctx, posdb.GetTicketForUpdateStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ticketID})
			if err != nil {
				return orNotFound(err, notFound("TICKET_NOT_FOUND", "ticket"))
			}
			if cur.Kind == "VOID" {
				return apperr.Conflict("TICKET_STATUS_INVALID", "a cancellation ticket has no status to move")
			}
			if cur.Status == to { // the other screen was first: the same answer, nothing to do
				res = TicketMoved{ID: cur.ID, StationID: cur.StationID, Status: cur.Status}
				return nil
			}
			if !contains(from, cur.Status) {
				return apperr.Conflict("TICKET_STATUS_INVALID", fmt.Sprintf("a ticket that is %s cannot be moved to %s", cur.Status, to)).WithContext("status", cur.Status)
			}
			row, err := q.AdvanceTicket(ctx, posdb.AdvanceTicketParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ticketID, FromStatus: cur.Status, ToStatus: to, At: s.clock.Now(), ActorID: p.ActorID()})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) { // it moved between the read and the update: look again
					continue
				}
				return err
			}
			res = TicketMoved{ID: row.ID, StationID: row.StationID, Status: row.Status, Changed: true}
			return s.audit.Write(ctx, s.entry(p, propertyID, bd, auditAction, "pos_ticket", ticketID, "", map[string]any{"status": cur.Status}, map[string]any{"status": to}))
		}
		return apperr.Busy("RESOURCE_BUSY", "the ticket is being changed; try again")
	})
	return res, err
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- print jobs of tickets

// queueTicketJob puts the ticket of a station that has a printer in the queue, when the outlet works in PRINT mode. The agent that prints
// the queue is stage 3b; until then a job stays QUEUED and is shown in the setup of the stations and printers.
func (s *Service) queueTicketJob(ctx context.Context, p auth.Principal, propertyID int64, o Outlet, ticketID int64, stationID *int64, kind string) error {
	if o.OrderMode != "PRINT" || stationID == nil {
		return nil
	}
	q := s.q(ctx)
	st, err := q.StationPrinter(ctx, posdb.StationPrinterParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *stationID})
	if err != nil {
		return err
	}
	if st.PrinterID == nil {
		return nil
	}
	_, err = q.InsertPrintJob(ctx, posdb.InsertPrintJobParams{TenantID: p.TenantID, PropertyID: propertyID, PrinterID: *st.PrinterID, Kind: "TICKET", SourceType: "TICKET", SourceID: ticketID,
		DedupeKey: fmt.Sprintf("TICKET:%d:1", ticketID), CreatedBy: p.ActorID()})
	if errors.Is(err, pgx.ErrNoRows) { // the same ticket again: one job
		return nil
	}
	return err
}

// PrintJob is a job of the queue as the setup shows it.
type PrintJob struct {
	ID          string     `json:"id"`
	PrinterID   int64      `json:"printer_id"`
	PrinterName string     `json:"printer_name"`
	Kind        string     `json:"kind"`
	SourceType  string     `json:"source_type"`
	SourceID    int64      `json:"source_id"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	CreatedAt   time.Time  `json:"created_at"`
	PrintedAt   *time.Time `json:"printed_at"`
	ErrorCode   string     `json:"error_code,omitempty"`
	BillNumber  string     `json:"bill_number,omitempty"`
	TableCode   string     `json:"table_code,omitempty"`
	Station     string     `json:"station,omitempty"`
	TicketKind  string     `json:"ticket_kind,omitempty"`
}

// PrintQueue is the queue of print jobs, newest first, with how many wait for each printer.
type PrintQueue struct {
	Jobs   []PrintJob    `json:"jobs"`
	Queued map[int64]int `json:"queued"`
}

// ListPrintJobs reads the queue (pos.manage or pos.cashier).
func (s *Service) ListPrintJobs(ctx context.Context, propertyID int64, printerID *int64, status string, limit int) (PrintQueue, error) {
	p, err := s.canRead(ctx, propertyID, auth.PermPOSManage, auth.PermPOSCashier)
	if err != nil {
		return PrintQueue{}, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var st *string
	if status != "" {
		st = &status
	}
	q := s.q(ctx)
	rows, err := q.ListPrintJobs(ctx, posdb.ListPrintJobsParams{TenantID: p.TenantID, PropertyID: propertyID, PrinterID: printerID, Status: st, MaxRows: int32(limit)})
	if err != nil {
		return PrintQueue{}, err
	}
	out := PrintQueue{Jobs: make([]PrintJob, len(rows)), Queued: map[int64]int{}}
	for i, r := range rows {
		out.Jobs[i] = PrintJob{ID: uuidString(r.ID), PrinterID: r.PrinterID, PrinterName: r.PrinterName, Kind: r.Kind, SourceType: r.SourceType, SourceID: r.SourceID, Status: r.Status,
			Attempts: int(r.Attempts), CreatedAt: r.CreatedAt, PrintedAt: r.PrintedAt, ErrorCode: r.ErrorCode, BillNumber: r.BillNumber, TableCode: r.TableCode, Station: r.StationName, TicketKind: r.TicketKind}
	}
	depth, err := q.PrintQueueDepth(ctx, posdb.PrintQueueDepthParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return PrintQueue{}, err
	}
	for _, d := range depth {
		out.Queued[d.PrinterID] = int(d.Queued)
	}
	return out, nil
}
