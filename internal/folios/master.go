package folios

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The master folio of a booking group and the routing of its members (docs/architecture/30-group-folio.md, part C).
//
// A folio of type MASTER belongs to a group: it has no stay and no reservation. The group's instructions (group_billing_instructions) say which charges of its members go to it when they are
// posted; ResolveTarget is still the only place that decides (routing.go). A line can stay out of the routing of its group. The master is opened by hand or by the first instruction, never
// inside a posting run: it takes a document number, the last lock of the order.

const (
	// FolioTypeMaster is the type of the master folio of a group.
	FolioTypeMaster = "MASTER"
	// FolioTypeOutlet is the type of the folio of a bill of an outlet (docs/architecture/34-pos.md): it has no reservation, stay, group or company.
	FolioTypeOutlet = "OUTLET"
	// maxLinesInContext is how many lines an error names (the screen shows them; the rest is counted).
	maxLinesInContext = 20
)

// MasterFolio is the master folio of a group as the group screen shows it. Balance is empty for a reader who may not read folios.
type MasterFolio struct {
	ID                int64      `json:"id"`
	FolioNumber       string     `json:"folio_number"`
	Status            string     `json:"status"`
	Balance           string     `json:"balance"`
	Version           int32      `json:"version"`
	BillToCompanyID   *int64     `json:"bill_to_company_id"`
	BillToCompanyName string     `json:"bill_to_company_name,omitempty"`
	OpenedAt          time.Time  `json:"opened_at"`
	ClosedAt          *time.Time `json:"closed_at"`
}

// GroupInstruction is one instruction of a group: a charge code, the room or everything goes to the master.
type GroupInstruction struct {
	Scope          string `json:"scope"`
	ChargeCodeID   *int64 `json:"charge_code_id"`
	ChargeCode     string `json:"charge_code,omitempty"`
	ChargeCodeName string `json:"charge_code_name,omitempty"`
}

// GroupInstructionInput is one instruction as it is set.
type GroupInstructionInput struct {
	Scope        string `json:"scope"`
	ChargeCodeID *int64 `json:"charge_code_id"`
}

// GroupLine is a line of a reservation of the group, with whether it stays out of the routing of the group.
type GroupLine struct {
	ReservationRoomID  int64  `json:"reservation_room_id"`
	ReservationID      int64  `json:"reservation_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	Status             string `json:"status"`
	Excluded           bool   `json:"excluded"`
	// StayID, RoomNumber and GuestName tell who is in the room now (the open stay), for the charge form of the master folio: a charge can be posted for a guest.
	StayID     *int64 `json:"stay_id"`
	RoomNumber string `json:"room_number,omitempty"`
	GuestName  string `json:"guest_name,omitempty"`
}

// GroupRouting is the master folio of a group, its instructions and the lines that are excluded.
type GroupRouting struct {
	GroupID      int64              `json:"group_id"`
	Master       *MasterFolio       `json:"master"`
	Instructions []GroupInstruction `json:"instructions"`
	Lines        []GroupLine        `json:"lines"`
}

// GroupRoutingInput replaces the instructions of a group and the set of excluded lines.
type GroupRoutingInput struct {
	Instructions  []GroupInstructionInput `json:"instructions"`
	ExcludedLines []int64                 `json:"excluded_lines"`
}

func errGroupNotFound() *apperr.Error {
	return apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
}

func (s *Service) masterView(ctx context.Context, tenantID, propertyID int64, f foliosdb.Folio, withBalance bool) (MasterFolio, error) {
	out := MasterFolio{ID: f.ID, FolioNumber: f.FolioNumber, Status: f.Status, Version: f.Version, BillToCompanyID: f.BillToCompanyID, OpenedAt: f.OpenedAt, ClosedAt: f.ClosedAt}
	if f.BillToCompanyID != nil {
		name, err := s.q(ctx).GetCompanyName(ctx, foliosdb.GetCompanyNameParams{TenantID: tenantID, PropertyID: propertyID, ID: *f.BillToCompanyID})
		if err == nil {
			out.BillToCompanyName = name
		}
	}
	if withBalance {
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return out, err
		}
		balance, _, err := s.balanceOf(ctx, propertyID, f.ID)
		if err != nil {
			return out, err
		}
		out.Balance = fixed(balance, decimals)
	}
	return out, nil
}

func (s *Service) groupInstructions(ctx context.Context, tenantID, propertyID, groupID int64) ([]GroupInstruction, error) {
	rows, err := s.q(ctx).ListGroupInstructions(ctx, foliosdb.ListGroupInstructionsParams{TenantID: tenantID, PropertyID: propertyID, GroupID: groupID})
	if err != nil {
		return nil, err
	}
	out := make([]GroupInstruction, 0, len(rows))
	for _, r := range rows {
		out = append(out, GroupInstruction{Scope: r.Scope, ChargeCodeID: r.ChargeCodeID, ChargeCode: deref(r.ChargeCode), ChargeCodeName: deref(r.ChargeCodeName)})
	}
	return out, nil
}

func (s *Service) groupLines(ctx context.Context, tenantID, propertyID, groupID int64) ([]GroupLine, error) {
	rows, err := s.q(ctx).ListGroupLines(ctx, foliosdb.ListGroupLinesParams{TenantID: tenantID, PropertyID: propertyID, GroupID: &groupID})
	if err != nil {
		return nil, err
	}
	out := make([]GroupLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, GroupLine{ReservationRoomID: r.ReservationRoomID, ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber, Status: r.Status, Excluded: r.Excluded, StayID: stayPtr(r.OpenStayID), RoomNumber: r.RoomNumber, GuestName: r.GuestName})
	}
	return out, nil
}

// GetGroupRouting reads the master folio of a group, its instructions and its lines (reservation.read; the balance of the master only with folio.read).
func (s *Service) GetGroupRouting(ctx context.Context, propertyID, groupID int64) (GroupRouting, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return GroupRouting{}, err
	}
	q := s.q(ctx)
	if _, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
		return GroupRouting{}, orNotFound(err, errGroupNotFound())
	}
	out := GroupRouting{GroupID: groupID}
	m, err := q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
	switch {
	case err == nil:
		view, err := s.masterView(ctx, p.TenantID, propertyID, m, s.authz.Require(ctx, propertyID, auth.PermFolioRead) == nil)
		if err != nil {
			return out, err
		}
		out.Master = &view
	case !errors.Is(err, pgx.ErrNoRows):
		return out, err
	}
	if out.Instructions, err = s.groupInstructions(ctx, p.TenantID, propertyID, groupID); err != nil {
		return out, err
	}
	out.Lines, err = s.groupLines(ctx, p.TenantID, propertyID, groupID)
	return out, err
}

// MasterResult is the master folio after it was opened, and whether this call opened it.
type MasterResult struct {
	Master  MasterFolio `json:"master"`
	Created bool        `json:"created"`
}

// openMaster opens the master folio of a group that has none, in the caller's transaction. The caller holds the business day, the group row (level 45) and takes the document number here (level 50).
func (s *Service) openMaster(ctx context.Context, p auth.Principal, propertyID int64, group foliosdb.GetBookingGroupBriefRow) (foliosdb.Folio, error) {
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFolio)
	if err != nil {
		return foliosdb.Folio{}, err
	}
	return s.q(ctx).InsertMasterFolio(ctx, foliosdb.InsertMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, GroupID: &group.ID, CompanyID: group.CompanyID, ActorID: p.ActorID()})
}

// OpenMaster opens the master folio of a group (folio.master_manage). A group has one: asking again returns it. The company of the group is copied onto the folio, which is then the buyer
// of its invoices. Lock order: the business day, the group, then the document number.
func (s *Service) OpenMaster(ctx context.Context, propertyID, groupID int64) (MasterResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioMasterManage)
	if err != nil {
		return MasterResult{}, err
	}
	var out MasterResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Groups, db.ForUpdate, propertyID, []int64{groupID}); err != nil { // L45
			return mapNotFound(err, errGroupNotFound())
		}
		q := s.q(ctx)
		group, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
		if err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		existing, err := q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
		if err == nil {
			out.Master, err = s.masterView(ctx, p.TenantID, propertyID, existing, true)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !group.IsActive {
			return apperr.Conflict("GROUP_INACTIVE", "the group is inactive")
		}
		f, err := s.openMaster(ctx, p, propertyID, group)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio.master_opened", "folio", f.ID, f.FolioNumber, nil,
			map[string]any{"folio_number": f.FolioNumber, "group_id": groupID, "group_code": group.Code, "bill_to_company_id": f.BillToCompanyID})); err != nil {
			return err
		}
		out.Created = true
		out.Master, err = s.masterView(ctx, p.TenantID, propertyID, f, true)
		return err
	})
	return out, err
}

func validateGroupRouting(in GroupRoutingInput) []apperr.FieldError {
	var fields []apperr.FieldError
	seen := map[string]bool{}
	for i, x := range in.Instructions {
		f := func(name string) string { return fmt.Sprintf("instructions[%d].%s", i, name) }
		var k string
		switch x.Scope {
		case ScopeAll, ScopeRoom:
			if x.ChargeCodeID != nil {
				fields = append(fields, fieldErr(f("charge_code_id"), "INVALID_VALUE", "only for the scope CHARGE_CODE"))
			}
			k = x.Scope
		case ScopeChargeCode:
			if x.ChargeCodeID == nil || *x.ChargeCodeID < 1 {
				fields = append(fields, fieldErr(f("charge_code_id"), "REQUIRED", "a charge code is required for the scope CHARGE_CODE"))
				continue
			}
			k = fmt.Sprintf("%s:%d", x.Scope, *x.ChargeCodeID)
		default:
			fields = append(fields, fieldErr(f("scope"), "INVALID_VALUE", "ALL, ROOM or CHARGE_CODE"))
			continue
		}
		if seen[k] {
			fields = append(fields, fieldErr(f("scope"), "DUPLICATE", "one instruction for each scope and charge code"))
		}
		seen[k] = true
	}
	return fields
}

// SetGroupRouting replaces the instructions of a group and the set of lines that stay out of them (folio.master_manage). It applies from the next night not yet posted: a night already posted
// stays where it is (moving it is a transfer). An instruction needs the master folio, so the first one opens it. Lock order: the business day, the open stays of the members ascending (so that
// this and a posting run take turns), the master folio, the group, then the document number when the master is opened.
func (s *Service) SetGroupRouting(ctx context.Context, propertyID, groupID int64, in GroupRoutingInput) (GroupRouting, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioMasterManage)
	if err != nil {
		return GroupRouting{}, err
	}
	if fields := validateGroupRouting(in); len(fields) > 0 {
		return GroupRouting{}, apperr.Invalid("the routing is invalid", fields...)
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		stays, err := q.ListGroupOpenStayIDs(ctx, foliosdb.ListGroupOpenStayIDsParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Stays, db.ForUpdate, propertyID, stays); err != nil { // L41, ascending
			return err
		}
		master, err := q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
		hasMaster := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasMaster {
			if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, []int64{master.ID}); err != nil { // L42
				return mapNotFound(err, errFolioNotFound())
			}
		}
		if err := db.LockRows(ctx, db.Groups, db.ForUpdate, propertyID, []int64{groupID}); err != nil { // L45
			return mapNotFound(err, errGroupNotFound())
		}
		group, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
		if err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		lines, err := s.groupLines(ctx, p.TenantID, propertyID, groupID)
		if err != nil {
			return err
		}
		inGroup := map[int64]bool{}
		for _, l := range lines {
			inGroup[l.ReservationRoomID] = true
		}
		var bad []int64
		excluded := map[int64]bool{}
		for _, id := range in.ExcludedLines {
			if !inGroup[id] {
				bad = append(bad, id)
			}
			excluded[id] = true
		}
		if len(bad) > 0 {
			return apperr.Invalid("the routing is invalid", fieldErr("excluded_lines", "NOT_IN_GROUP", "every line must belong to a reservation of the group")).WithContext("line_ids", bad)
		}
		for i, x := range in.Instructions {
			if x.ChargeCodeID == nil {
				continue
			}
			if _, err := q.GetChargeTypeOfCode(ctx, foliosdb.GetChargeTypeOfCodeParams{PropertyID: propertyID, ID: *x.ChargeCodeID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return apperr.Invalid("the routing is invalid", fieldErr(fmt.Sprintf("instructions[%d].charge_code_id", i), "NOT_FOUND", "a charge code of this property"))
				}
				return err
			}
		}
		beforeInstr, err := s.groupInstructions(ctx, p.TenantID, propertyID, groupID)
		if err != nil {
			return err
		}
		var beforeExcl []int64
		for _, l := range lines {
			if l.Excluded {
				beforeExcl = append(beforeExcl, l.ReservationRoomID)
			}
		}
		opened := ""
		if len(in.Instructions) > 0 && !hasMaster {
			if !group.IsActive {
				return apperr.Conflict("GROUP_INACTIVE", "the group is inactive")
			}
			f, err := s.openMaster(ctx, p, propertyID, group) // L50
			if err != nil {
				return err
			}
			opened = f.FolioNumber
		}
		if err := q.DeleteGroupInstructions(ctx, foliosdb.DeleteGroupInstructionsParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: groupID}); err != nil {
			return err
		}
		for _, x := range in.Instructions {
			if err := q.InsertGroupInstruction(ctx, foliosdb.InsertGroupInstructionParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: groupID, Scope: x.Scope, ChargeCodeID: x.ChargeCodeID, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		if err := q.DeleteGroupExclusions(ctx, foliosdb.DeleteGroupExclusionsParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID}); err != nil {
			return err
		}
		ids := make([]int64, 0, len(excluded))
		for id := range excluded {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if err := q.InsertGroupExclusion(ctx, foliosdb.InsertGroupExclusionParams{ReservationRoomID: id, TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		afterInstr, err := s.groupInstructions(ctx, p.TenantID, propertyID, groupID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "group.routing_set", "booking_group", groupID, group.Code,
			map[string]any{"instructions": beforeInstr, "excluded_lines": beforeExcl},
			map[string]any{"instructions": afterInstr, "excluded_lines": ids, "master_opened": opened}))
	})
	if err != nil {
		return GroupRouting{}, err
	}
	return s.GetGroupRouting(ctx, propertyID, groupID)
}

// requireMasterCanClose is the part of closing a folio that is the master's: every line of the group that puts charges on the master is over (COMPLETED, CANCELLED or NO_SHOW), no such member in house and none still to arrive; a room that stays out of the master is not waited for. The
// lines are read after the folio lock, so a check-out that committed while this waited is seen.
func (s *Service) requireMasterCanClose(ctx context.Context, tenantID, propertyID int64, f foliosdb.Folio) error {
	if f.BookingGroupID == nil {
		return nil
	}
	rows, err := s.q(ctx).ListGroupLinesNotOver(ctx, foliosdb.ListGroupLinesNotOverParams{TenantID: tenantID, PropertyID: propertyID, GroupID: f.BookingGroupID})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	named := make([]map[string]any, 0, maxLinesInContext)
	for i, r := range rows {
		if i == maxLinesInContext {
			break
		}
		named = append(named, map[string]any{"reservation_room_id": r.ReservationRoomID, "confirmation_number": r.ConfirmationNumber, "status": r.Status})
	}
	return apperr.Conflict("MASTER_FOLIO_GROUP_ACTIVE", "the group still has rooms that are not over: the master folio closes after the last check-out").
		WithContext("lines", named).WithContext("lines_total", len(rows))
}

// RoutingNotice is where the room charges of a line go, for the notice on a reservation or a folio: the company folio of its own instruction, the master folio of its group, or its guest folio.
type RoutingNotice struct {
	// Room says where a room night goes: LINE (a company folio, by an instruction of the line), GROUP (the master folio of the group) or NONE (the guest folio).
	Room     string `json:"room"`
	Excluded bool   `json:"excluded"`
	Group    *struct {
		ID           int64  `json:"id"`
		Code         string `json:"code"`
		Name         string `json:"name"`
		MasterID     int64  `json:"master_folio_id"`
		MasterNumber string `json:"master_folio_number"`
		MasterStatus string `json:"master_status"`
	} `json:"group"`
	GroupInstructions []GroupInstruction `json:"group_instructions"`
}

// GetLineRouting says where the charges of a line of a reservation go (reservation.read): the instruction of the line wins over those of its group, and a line excluded from the routing of its
// group ignores them. A room night is judged by the room and the "all" instructions; an instruction for one charge code is listed with the group's.
func (s *Service) GetLineRouting(ctx context.Context, propertyID, reservationID, lineID int64) (RoutingNotice, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return RoutingNotice{}, err
	}
	out := RoutingNotice{Room: "NONE", GroupInstructions: []GroupInstruction{}}
	q := s.q(ctx)
	if _, err := q.GetInstructionLine(ctx, foliosdb.GetInstructionLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID, ID: lineID}); err != nil {
		return out, orNotFound(err, errLineNotFound())
	}
	own, err := s.listInstructions(ctx, p.TenantID, propertyID, lineID)
	if err != nil {
		return out, err
	}
	for _, i := range own {
		if i.Scope == ScopeRoom || i.Scope == ScopeAll {
			out.Room = "LINE"
		}
	}
	g, err := q.GetLineGroupRouting(ctx, foliosdb.GetLineGroupRoutingParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID, ID: lineID})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil // a reservation that is in no group
	}
	if err != nil {
		return out, err
	}
	out.Excluded = g.Excluded
	instr, err := s.groupInstructions(ctx, p.TenantID, propertyID, *g.BookingGroupID)
	if err != nil {
		return out, err
	}
	out.GroupInstructions = instr
	out.Group = &struct {
		ID           int64  `json:"id"`
		Code         string `json:"code"`
		Name         string `json:"name"`
		MasterID     int64  `json:"master_folio_id"`
		MasterNumber string `json:"master_folio_number"`
		MasterStatus string `json:"master_status"`
	}{ID: *g.BookingGroupID, Code: g.GroupCode, Name: g.GroupName, MasterID: g.MasterID, MasterNumber: g.MasterNumber, MasterStatus: g.MasterStatus}
	if out.Room == "NONE" && !g.Excluded {
		for _, i := range instr {
			if i.Scope == ScopeRoom || i.Scope == ScopeAll {
				out.Room = "GROUP"
			}
		}
	}
	return out, nil
}

// GroupRooms lists the numbers of the rooms the members of a group have stayed in (folio.read), for the invoice of its master folio.
func (s *Service) GroupRooms(ctx context.Context, propertyID, groupID int64) ([]string, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return nil, err
	}
	return s.q(ctx).ListGroupRoomNumbers(ctx, foliosdb.ListGroupRoomNumbersParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
}

// MasterInfo tells the desk that a group still has an open bill: the master folio, with its balance.
type MasterInfo struct {
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
	Balance     string `json:"balance"`
	GroupCode   string `json:"group_code"`
	GroupName   string `json:"group_name"`
}

// OpenMasterOfFinishedGroup is the open master folio of the group of a reservation when every room that puts charges on it is over (the last such member has gone), else nil. A room that stays out of
// the master neither is waited for nor says it: the check-out of lineID answers only when lineID is not one of those. The answer lets the desk know the group still has an open bill; it blocks nothing.
// It reads in the caller's transaction and takes no lock.
func (s *Service) OpenMasterOfFinishedGroup(ctx context.Context, tenantID, propertyID, reservationID, lineID int64) (*MasterInfo, error) {
	q := s.q(ctx)
	if out, err := q.IsLineExcluded(ctx, lineID); err != nil || out {
		return nil, err
	}
	rows, err := q.ListReservationGroups(ctx, foliosdb.ListReservationGroupsParams{TenantID: tenantID, PropertyID: propertyID, Ids: []int64{reservationID}})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0].BookingGroupID == nil {
		return nil, nil
	}
	groupID := *rows[0].BookingGroupID
	m, err := q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: tenantID, PropertyID: propertyID, GroupID: &groupID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && m.Status != "OPEN") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	left, err := q.ListGroupLinesNotOver(ctx, foliosdb.ListGroupLinesNotOverParams{TenantID: tenantID, PropertyID: propertyID, GroupID: &groupID})
	if err != nil || len(left) > 0 {
		return nil, err
	}
	g, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: tenantID, PropertyID: propertyID, ID: groupID})
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, m.ID)
	if err != nil {
		return nil, err
	}
	return &MasterInfo{FolioID: m.ID, FolioNumber: m.FolioNumber, Balance: fixed(balance, decimals), GroupCode: g.Code, GroupName: g.Name}, nil
}

// PostedCharges is what a group's instructions would send to the master and is already on the folios of its members: one row for each folio and charge code.
type PostedCharges struct {
	FolioID            int64  `json:"folio_id"`
	FolioNumber        string `json:"folio_number"`
	ReservationID      int64  `json:"reservation_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	ChargeCodeID       int64  `json:"charge_code_id"`
	ChargeCode         string `json:"charge_code"`
	ChargeCodeName     string `json:"charge_code_name"`
	Count              int    `json:"count"`
	Total              string `json:"total"`
}

// PostedForMaster lists the charges already posted to the guest folios of the members that the instructions of the group would now send to its master (folio.read): the room nights for ROOM, the
// charge codes named for CHARGE_CODE, every charge for ALL. They stay where they are until they are moved with the transfer of charges to the master (scope GROUP_MASTER): the group screen offers
// it right after the instructions are saved.
func (s *Service) PostedForMaster(ctx context.Context, propertyID, groupID int64) ([]PostedCharges, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	if _, err := q.GetBookingGroupBrief(ctx, foliosdb.GetBookingGroupBriefParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID}); err != nil {
		return nil, orNotFound(err, errGroupNotFound())
	}
	instr, err := s.groupInstructions(ctx, p.TenantID, propertyID, groupID)
	if err != nil {
		return nil, err
	}
	if len(instr) == 0 {
		return []PostedCharges{}, nil
	}
	return s.postedFor(ctx, p.TenantID, propertyID, groupID, instr)
}

// postedFor is the list of PostedForMaster for the instructions given, without a permission check.
func (s *Service) postedFor(ctx context.Context, tenantID, propertyID, groupID int64, instr []GroupInstruction) ([]PostedCharges, error) {
	q := s.q(ctx)
	all, room := false, false
	codes := map[int64]bool{}
	for _, i := range instr {
		switch i.Scope {
		case ScopeAll:
			all = true
		case ScopeRoom:
			room = true
		case ScopeChargeCode:
			if i.ChargeCodeID != nil {
				codes[*i.ChargeCodeID] = true
			}
		}
	}
	out := []PostedCharges{}
	rows, err := q.ListPostedForMaster(ctx, foliosdb.ListPostedForMasterParams{TenantID: tenantID, PropertyID: propertyID, GroupID: &groupID})
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ChargeCodeID == nil || (!all && !codes[*r.ChargeCodeID] && (!room || r.ChargeType != "ROOM")) {
			continue
		}
		out = append(out, PostedCharges{FolioID: r.FolioID, FolioNumber: r.FolioNumber, ReservationID: r.ReservationID, ConfirmationNumber: r.ConfirmationNumber, ChargeCodeID: *r.ChargeCodeID,
			ChargeCode: r.ChargeCode, ChargeCodeName: r.ChargeCodeName, Count: int(r.ItemCount), Total: fixed(r.Total, decimals)})
	}
	return out, nil
}

func stayPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// RoomChargeFolio is where some of the room charges of a stay stand: the folio, how many nights, and what they add up to.
type RoomChargeFolio struct {
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
	FolioType   string `json:"folio_type"`
	Nights      int    `json:"nights"`
	Total       string `json:"total"`
}

// StayRoomCharges says where the room charges of a stay stand now, one entry for each folio (no permission: the caller has its own). A stay whose group's routing changed while it was in house has
// its nights on two folios, and the answer says how many on each.
func (s *Service) StayRoomCharges(ctx context.Context, tenantID, propertyID, stayID int64) ([]RoomChargeFolio, error) {
	rows, err := s.q(ctx).ListStayRoomChargesByFolio(ctx, foliosdb.ListStayRoomChargesByFolioParams{TenantID: tenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]RoomChargeFolio, 0, len(rows))
	for _, r := range rows {
		out = append(out, RoomChargeFolio{FolioID: r.FolioID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, Nights: int(r.Nights), Total: fixed(r.Total, decimals)})
	}
	return out, nil
}

// RoomChargesMaster is the open master folio the room charges of a line go to now (the routing of its group, the line not excluded and with no instruction of its own), or nil.
func (s *Service) RoomChargesMaster(ctx context.Context, tenantID, propertyID, reservationID, lineID int64) (*MasterRef, error) {
	q := s.q(ctx)
	own, err := s.listInstructions(ctx, tenantID, propertyID, lineID)
	if err != nil {
		return nil, err
	}
	for _, i := range own {
		if i.Scope == ScopeRoom || i.Scope == ScopeAll {
			return nil, nil
		}
	}
	g, err := q.GetLineGroupRouting(ctx, foliosdb.GetLineGroupRoutingParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: reservationID, ID: lineID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (g.Excluded || g.MasterStatus != "OPEN")) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	instr, err := s.groupInstructions(ctx, tenantID, propertyID, *g.BookingGroupID)
	if err != nil {
		return nil, err
	}
	for _, i := range instr {
		if i.Scope == ScopeRoom || i.Scope == ScopeAll {
			return &MasterRef{FolioID: g.MasterID, FolioNumber: g.MasterNumber}, nil
		}
	}
	return nil, nil
}

// MasterRef names a master folio.
type MasterRef struct {
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
}
