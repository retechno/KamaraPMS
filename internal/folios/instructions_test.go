package folios_test

import (
	"context"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
)

func (p *payerFx) line(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT reservation_room_id FROM stays WHERE id = $1`, p.stay).Scan(&id))
	return id
}

func (p *payerFx) otherCompany(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'OTHER', 'Other Corp', 1000000) RETURNING id`, p.tenantID, p.propID).Scan(&id))
	return id
}

func (p *payerFx) set(t *testing.T, in ...folios.InstructionInput) folios.Instructions {
	t.Helper()
	out, err := p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation, p.line(t), in)
	must(t, err)
	return out
}

func (p *payerFx) target(t *testing.T, r folios.Route) folios.Target {
	t.Helper()
	got, err := p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, p.stay, r)
	must(t, err)
	return got
}

func (p *payerFx) companyFolioOf(t *testing.T, company int64) int64 {
	t.Helper()
	list, err := p.Folios.StayFolios(p.admin, p.tenantID, p.propID, p.stay)
	must(t, err)
	for _, f := range list {
		if f.BillToCompanyID != nil && *f.BillToCompanyID == company {
			return f.ID
		}
	}
	t.Fatalf("the stay has no folio for company %d: %+v", company, list)
	return 0
}

func TestInstructionOpensTheCompanyFolioOfAStayInHouse(t *testing.T) {
	p := setupPayer(t)
	out := p.set(t, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: p.company})
	if len(out.Instructions) != 1 || out.Instructions[0].CompanyName != "Acme Corp" || out.Instructions[0].Scope != "ROOM" {
		t.Fatalf("instructions: %+v", out)
	}
	cf := p.companyFolioOf(t, p.company)
	if n := p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.billing_instructions_set' AND entity_id = $1`, p.line(t)); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	// the same set again opens nothing more, and removing it does not delete the folio (the ledger is append-only)
	p.set(t, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: p.company})
	p.set(t)
	if n := p.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1`, p.stay); n != 2 {
		t.Fatalf("folios of the stay: %d", n)
	}
	if got := p.target(t, folios.RoomRoute); got.FolioID != p.guestFolio || got.Routed {
		t.Fatalf("with no instruction the night goes to the guest folio, got %+v", got)
	}
	_ = cf
}

func TestRoutingPrecedence(t *testing.T) {
	p := setupPayer(t)
	other := p.otherCompany(t)
	p.set(t,
		folios.InstructionInput{Scope: folios.ScopeAll, CompanyID: p.company},
		folios.InstructionInput{Scope: folios.ScopeChargeCode, ChargeCodeID: &p.minibar, CompanyID: other})
	acme, oth := p.companyFolioOf(t, p.company), p.companyFolioOf(t, other)

	check := func(what string, r folios.Route, want int64) {
		t.Helper()
		if got := p.target(t, r); got.FolioID != want || !got.Routed {
			t.Fatalf("%s: %+v, want folio %d routed", what, got, want)
		}
	}
	check("the code an instruction names", folios.Route{ChargeCodeID: p.minibar}, oth)
	check("another code: all", folios.Route{ChargeCodeID: p.laundry}, acme)
	check("the room with no room instruction: all", folios.RoomRoute, acme)

	p.set(t,
		folios.InstructionInput{Scope: folios.ScopeAll, CompanyID: p.company},
		folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: other},
		folios.InstructionInput{Scope: folios.ScopeChargeCode, ChargeCodeID: &p.minibar, CompanyID: other})
	check("the room instruction beats all", folios.RoomRoute, oth)
	check("a code that is not the room stays on all", folios.Route{ChargeCodeID: p.laundry}, acme)
	// a code instruction wins even for a room code
	check("a charge code instruction beats the room instruction", folios.Route{ChargeCodeID: p.minibar, IsRoom: true}, oth)

	m, err := p.Folios.ResolveRoomTargets(p.admin, p.tenantID, p.propID, []int64{p.stay})
	must(t, err)
	if m[p.stay].Default.FolioID != oth || m[p.stay].ByCode[p.minibar].FolioID != oth {
		t.Fatalf("batch: %+v", m[p.stay])
	}
}

// A closed company folio is a blocker: the answer says there is no folio and that an instruction chose it, and nothing falls back to the guest folio.
func TestClosedCompanyFolioIsNotAFallback(t *testing.T) {
	p := setupPayer(t)
	p.set(t, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: p.company})
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, p.companyFolioOf(t, p.company)))
	got := p.target(t, folios.RoomRoute)
	if got.FolioID != 0 || !got.Routed {
		t.Fatalf("target %+v, want none and routed", got)
	}
	m, err := p.Folios.ResolveRoomTargets(p.admin, p.tenantID, p.propID, []int64{p.stay})
	must(t, err)
	if m[p.stay].Default.FolioID != 0 || !m[p.stay].Default.Routed {
		t.Fatalf("batch: %+v", m[p.stay])
	}
}

func TestInstructionsAreValidated(t *testing.T) {
	p := setupPayer(t)
	line := p.line(t)
	set := func(in ...folios.InstructionInput) error {
		_, err := p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation, line, in)
		return err
	}
	zero := int64(0)
	wantCode(t, set(folios.InstructionInput{Scope: "ALL", CompanyID: p.company}, folios.InstructionInput{Scope: "ALL", CompanyID: p.company}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "CHARGE_CODE", CompanyID: p.company}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "CHARGE_CODE", ChargeCodeID: &zero, CompanyID: p.company}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "ROOM", ChargeCodeID: &p.minibar, CompanyID: p.company}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "EVERYTHING", CompanyID: p.company}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "ALL"}), "VALIDATION_FAILED")
	wantCode(t, set(folios.InstructionInput{Scope: "ALL", CompanyID: 999999}), "COMPANY_NOT_FOUND")
	missing := int64(999999)
	wantCode(t, set(folios.InstructionInput{Scope: "CHARGE_CODE", ChargeCodeID: &missing, CompanyID: p.company}), "CHARGE_CODE_NOT_FOUND")
	// nothing was kept by a failed set
	if n := p.Count(t, `SELECT count(*) FROM folio_billing_instructions`); n != 0 {
		t.Fatalf("instructions after the failures: %d", n)
	}
	// another reservation's line is not found
	_, err := p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation+1000, line, nil)
	wantCode(t, err, "RESERVATION_NOT_FOUND")
	_, err = p.Folios.GetBillingInstructions(p.admin, p.propID, p.reservation, line+1000)
	wantCode(t, err, "RESERVATION_ROOM_NOT_FOUND")
}

func TestInstructionsNeedThePermissions(t *testing.T) {
	p := setupPayer(t)
	line := p.line(t)
	reader := p.User(t, p.tenantID, p.propID, auth.PermReservationRead)
	if _, err := p.Folios.GetBillingInstructions(reader, p.propID, p.reservation, line); err != nil {
		t.Fatal(err)
	}
	_, err := p.Folios.SetBillingInstructions(reader, p.propID, p.reservation, line, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := p.User(t, p.tenantID, p.propID)
	_, err = p.Folios.GetBillingInstructions(nobody, p.propID, p.reservation, line)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestInstructionsOfAnOverLineCannotChange(t *testing.T) {
	p := setupPayer(t)
	must(t, p.Exec(t, `UPDATE reservation_rooms SET status = 'CANCELLED', cancelled_at = now() WHERE id = $1`, p.line(t)))
	_, err := p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation, p.line(t), []folios.InstructionInput{{Scope: "ALL", CompanyID: p.company}})
	wantCode(t, err, "INSTRUCTION_LINE_CLOSED")
}

// Before check-in nothing is opened; the check-in opens the company folio with the guest folio.
func TestCheckInOpensTheCompanyFolios(t *testing.T) {
	p := setupPayer(t)
	var typeID int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT room_type_id FROM rooms WHERE property_id = $1 AND room_number = '101'`, p.propID).Scan(&typeID))
	room := p.Room(t, p.admin, p.propID, typeID, "102")
	stay := p.Stay(t, p.tenantID, p.propID, typeID, room.ID, "2026-10-01", "2026-10-03")
	var line, res int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT s.reservation_room_id, rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&line, &res))
	must(t, p.Exec(t, `INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES ($1, $2, $3, 'ALL', $4)`, p.tenantID, p.propID, line, p.company))
	if n := p.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1`, stay); n != 0 {
		t.Fatalf("folios before the check-in: %d", n)
	}
	must(t, p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		_, err := p.Folios.AttachStayFolio(ctx, p.principal, p.propID, res, stay, 0)
		return err
	}))
	if n := p.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND folio_type = 'GUEST'`, stay); n != 1 {
		t.Fatalf("guest folios: %d", n)
	}
	if n := p.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY' AND bill_to_company_id = $2 AND status = 'OPEN'`, stay, p.company); n != 1 {
		t.Fatalf("company folios: %d", n)
	}
}

func TestCompanyFolioOnlyTransfersToItsCompany(t *testing.T) {
	p := setupPayer(t)
	other := p.otherCompany(t)
	cf := p.companyFolio(t, "FC1")
	_, err := p.Folios.PostCharge(p.admin, p.propID, cf, "k1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	_, err = p.Folios.Transfer(p.admin, p.propID, cf, "t1", folios.TransferInput{CompanyID: other, Amount: "1000"})
	e := code(t, err, "TRANSFER_COMPANY_MISMATCH")
	if e.Context["bill_to_company_id"] != p.company {
		t.Fatalf("context: %v", e.Context)
	}
	if n := p.Count(t, `SELECT count(*) FROM payments WHERE folio_id = $1`, cf); n != 0 {
		t.Fatalf("payments after the refusal: %d", n)
	}
	res, err := p.Folios.Transfer(p.admin, p.propID, cf, "t2", folios.TransferInput{CompanyID: p.company, Amount: "122100"})
	must(t, err)
	if res.FolioBalance != "0" {
		t.Fatalf("balance after the transfer: %s", res.FolioBalance)
	}
	// a guest folio can still go to any company
	_, err = p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "k2", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	_, err = p.Folios.Transfer(p.admin, p.propID, p.guestFolio, "t3", folios.TransferInput{CompanyID: other, Amount: "1000"})
	must(t, err)
}
