package folios

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Billing instructions (docs/architecture/18-architecture-decisions.md section 3): who pays what on a reservation line. They are intent; the folio a charge lands on is decided by
// ResolveTarget. The set of a line is replaced as a whole. A change applies from the next night not yet posted: a night already posted stays where it is.

// Instruction scopes.
const (
	ScopeAll        = "ALL"
	ScopeRoom       = "ROOM"
	ScopeChargeCode = "CHARGE_CODE"
)

// InstructionInput is one instruction of a line.
type InstructionInput struct {
	Scope        string `json:"scope"`
	ChargeCodeID *int64 `json:"charge_code_id"`
	CompanyID    int64  `json:"company_id"`
}

// Instruction is an instruction as it is read.
type Instruction struct {
	Scope          string `json:"scope"`
	ChargeCodeID   *int64 `json:"charge_code_id"`
	ChargeCode     string `json:"charge_code,omitempty"`
	ChargeCodeName string `json:"charge_code_name,omitempty"`
	CompanyID      int64  `json:"company_id"`
	CompanyName    string `json:"company_name"`
}

// Instructions are the billing instructions of a line.
type Instructions struct {
	Instructions []Instruction `json:"instructions"`
}

func errLineNotFound() *apperr.Error {
	return apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the reservation room does not exist in this property")
}

func (s *Service) listInstructions(ctx context.Context, tenantID, propertyID, lineID int64) ([]Instruction, error) {
	rows, err := s.q(ctx).ListLineInstructions(ctx, foliosdb.ListLineInstructionsParams{TenantID: tenantID, PropertyID: propertyID, ReservationRoomID: lineID})
	if err != nil {
		return nil, err
	}
	out := make([]Instruction, 0, len(rows))
	for _, r := range rows {
		in := Instruction{Scope: r.Scope, ChargeCodeID: r.ChargeCodeID, CompanyID: r.CompanyID, CompanyName: r.CompanyName}
		if r.ChargeCode != nil {
			in.ChargeCode = *r.ChargeCode
		}
		if r.ChargeCodeName != nil {
			in.ChargeCodeName = *r.ChargeCodeName
		}
		out = append(out, in)
	}
	return out, nil
}

// GetBillingInstructions reads the instructions of a line of a reservation (permission reservation.read).
func (s *Service) GetBillingInstructions(ctx context.Context, propertyID, reservationID, lineID int64) (Instructions, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationRead)
	if err != nil {
		return Instructions{}, err
	}
	if _, err := s.q(ctx).GetInstructionLine(ctx, foliosdb.GetInstructionLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID, ID: lineID}); err != nil {
		return Instructions{}, orNotFound(err, errLineNotFound())
	}
	list, err := s.listInstructions(ctx, p.TenantID, propertyID, lineID)
	return Instructions{Instructions: list}, err
}

func validateInstructions(in []InstructionInput) []apperr.FieldError {
	var fields []apperr.FieldError
	seen := map[string]bool{}
	for i, x := range in {
		f := func(name string) string { return fmt.Sprintf("instructions[%d].%s", i, name) }
		if x.CompanyID < 1 {
			fields = append(fields, fieldErr(f("company_id"), "REQUIRED", "a company is required"))
		}
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

// SetBillingInstructions replaces the instructions of a line (permission reservation.update). A line that is over (checked out, cancelled, no-show) cannot be changed. For a guest in
// house the company folios the instructions need are opened now, so that no posting run has to create one. Lock order: the business day, the reservation, the stay, then the folio
// numbers.
func (s *Service) SetBillingInstructions(ctx context.Context, propertyID, reservationID, lineID int64, in []InstructionInput) (Instructions, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Instructions{}, err
	}
	if fields := validateInstructions(in); len(fields) > 0 {
		return Instructions{}, apperr.Invalid("the billing instructions are invalid", fields...)
	}
	var out Instructions
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, []int64{reservationID}); err != nil {
			return mapNotFound(err, apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property"))
		}
		q := s.q(ctx)
		line, err := q.GetInstructionLine(ctx, foliosdb.GetInstructionLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID, ID: lineID})
		if err != nil {
			return orNotFound(err, errLineNotFound())
		}
		switch line.Status {
		case "COMPLETED", "CANCELLED", "NO_SHOW":
			return apperr.Conflict("INSTRUCTION_LINE_CLOSED", "the room is over: its billing instructions can no longer change").WithContext("status", line.Status)
		}
		stay, err := q.GetLineStay(ctx, foliosdb.GetLineStayParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationRoomID: lineID})
		hasStay := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasStay {
			if err := db.LockRows(ctx, db.Stays, db.ForUpdate, propertyID, []int64{stay.ID}); err != nil { // L4
				return err
			}
		}
		before, err := s.listInstructions(ctx, p.TenantID, propertyID, lineID)
		if err != nil {
			return err
		}
		if err := q.DeleteLineInstructions(ctx, foliosdb.DeleteLineInstructionsParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationRoomID: lineID}); err != nil {
			return err
		}
		for _, x := range in {
			if err := q.InsertInstruction(ctx, foliosdb.InsertInstructionParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationRoomID: lineID, Scope: x.Scope,
				ChargeCodeID: x.ChargeCodeID, CompanyID: x.CompanyID, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		var opened []StayFolio
		if hasStay && stay.Status == "OPEN" {
			if opened, err = s.ensureCompanyFolios(ctx, p, propertyID, reservationID, stay.ID); err != nil {
				return err
			}
		}
		after, err := s.listInstructions(ctx, p.TenantID, propertyID, lineID)
		if err != nil {
			return err
		}
		numbers := make([]string, len(opened))
		for i, f := range opened {
			numbers[i] = f.FolioNumber
		}
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(),
			Action: "folio.billing_instructions_set", EntityType: "reservation_room", EntityID: lineID,
			Old: map[string]any{"instructions": before}, New: map[string]any{"instructions": after, "company_folios_opened": numbers}}); err != nil {
			return err
		}
		out = Instructions{Instructions: after}
		return nil
	})
	return out, err
}

// ensureCompanyFolios opens, for a stay, the company folios its line's instructions point at and that do not exist yet (open or closed: a closed one is not reopened, and the night
// that needs it is a blocker). Folio numbers come from the sequence, the last lock of the order, so this runs before any posting run takes its locks. The caller holds the stay lock.
func (s *Service) ensureCompanyFolios(ctx context.Context, p auth.Principal, propertyID, reservationID, stayID int64) ([]StayFolio, error) {
	q := s.q(ctx)
	rules, err := q.ListStayInstructions(ctx, foliosdb.ListStayInstructionsParams{TenantID: p.TenantID, PropertyID: propertyID, StayIds: []int64{stayID}})
	if err != nil {
		return nil, err
	}
	have := map[int64]bool{}
	existing, err := q.ListStayFolios(ctx, foliosdb.ListStayFoliosParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return nil, err
	}
	for _, f := range existing {
		if f.BillToCompanyID != nil {
			have[*f.BillToCompanyID] = true
		}
	}
	var need []int64
	for _, r := range rules {
		if !have[r.CompanyID] {
			have[r.CompanyID] = true
			need = append(need, r.CompanyID)
		}
	}
	sort.Slice(need, func(i, j int) bool { return need[i] < need[j] })
	var out []StayFolio
	for _, company := range need {
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFolio)
		if err != nil {
			return nil, err
		}
		f, err := q.InsertCompanyFolio(ctx, foliosdb.InsertCompanyFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, ReservationID: reservationID, StayID: &stayID,
			CompanyID: &company, ActorID: p.ActorID()})
		if err != nil {
			return nil, err
		}
		sf, err := s.stayFolio(ctx, propertyID, f)
		if err != nil {
			return nil, err
		}
		out = append(out, sf)
	}
	return out, nil
}
