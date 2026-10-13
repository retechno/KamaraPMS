package folios_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/listsort"
)

// The master folio of a group and the routing of its members (docs/architecture/30-group-folio.md, part C).

// group makes a booking group with the company of the fixture and puts the reservation of the first stay in it.
func (p *payerFx) group(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, company_id, arrival_date, departure_date, contact_name)
		VALUES ($1, $2, 'GRP1', 'Conference', $3, '2026-10-01', '2026-10-03', 'Dewi') RETURNING id`, p.tenantID, p.propID, p.company).Scan(&id))
	must(t, p.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, id, p.reservation))
	return id
}

// joinGroup puts the reservation of a folio in the group.
func (p *payerFx) joinGroup(t *testing.T, group, folio int64) {
	t.Helper()
	must(t, p.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = (SELECT reservation_id FROM folios WHERE id = $2)`, group, folio))
}

func (p *payerFx) openMaster(t *testing.T, group int64) folios.MasterFolio {
	t.Helper()
	r, err := p.Folios.OpenMaster(p.admin, p.propID, group)
	must(t, err)
	return r.Master
}

func (p *payerFx) route(t *testing.T, group int64, scopes ...string) {
	t.Helper()
	in := folios.GroupRoutingInput{}
	for _, s := range scopes {
		x := folios.GroupInstructionInput{Scope: s}
		if s == "MINIBAR" {
			x = folios.GroupInstructionInput{Scope: folios.ScopeChargeCode, ChargeCodeID: &p.minibar}
		}
		in.Instructions = append(in.Instructions, x)
	}
	_, err := p.Folios.SetGroupRouting(p.admin, p.propID, group, in)
	must(t, err)
}

func TestTheShapeOfAMasterFolioIsHeldByTheSchema(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	insert := func(typ string, group, res, stay any, company any) error {
		return p.Exec(t, `INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, booking_group_id, reservation_id, stay_id, bill_to_company_id) VALUES ($1, $2, 'FX'||floor(random()*1000000)::text, $3, $4, $5, $6, $7)`,
			p.tenantID, p.propID, typ, group, res, stay, company)
	}
	must(t, insert("MASTER", g, nil, nil, nil)) // a master of a group: no reservation, no stay, a company or none
	for name, err := range map[string]error{
		"a second master of the group":            insert("MASTER", g, nil, nil, nil),
		"a master without a group":                insert("MASTER", nil, nil, nil, nil),
		"a master with a reservation":             insert("MASTER", g, p.reservation, nil, nil),
		"a master with a stay":                    insert("MASTER", g, nil, p.stay, nil),
		"a guest folio of a group":                insert("GUEST", g, p.reservation, nil, nil),
		"a guest folio without a reservation":     insert("GUEST", nil, nil, nil, nil),
		"a company folio without its company":     insert("COMPANY", nil, p.reservation, nil, nil),
		"a guest folio billed to a company":       insert("GUEST", nil, p.reservation, nil, p.company),
		"a master of a group that does not exist": insert("MASTER", int64(999999), nil, nil, nil),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestOpenMasterOncePerGroupWithTheCompanyOfTheGroup(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	r, err := p.Folios.OpenMaster(p.admin, p.propID, g)
	must(t, err)
	if !r.Created || r.Master.Status != "OPEN" || !strings.HasPrefix(r.Master.FolioNumber, "FOL") || r.Master.BillToCompanyID == nil || *r.Master.BillToCompanyID != p.company || r.Master.Balance != "0" {
		t.Fatalf("opened: %+v", r)
	}
	again, err := p.Folios.OpenMaster(p.admin, p.propID, g)
	must(t, err)
	if again.Created || again.Master.ID != r.Master.ID {
		t.Fatalf("a group has one master: %+v", again)
	}
	f, err := p.Folios.GetFolio(p.admin, p.propID, r.Master.ID)
	must(t, err)
	if f.FolioType != "MASTER" || f.ReservationID != nil || f.StayID != nil || f.BookingGroupID == nil || *f.BookingGroupID != g || f.BookingGroupCode != "GRP1" || f.BookingGroupContactName != "Dewi" {
		t.Fatalf("the folio: %+v", f)
	}
	// the folio list finds it by group and by type, with the group named
	gid := g
	list, err := p.Folios.ListFolios(p.admin, p.propID, folios.FolioFilter{BookingGroupID: &gid}, listsort.Sort{}, nil, 10)
	must(t, err)
	if len(list.Data) != 1 || list.Data[0].ID != r.Master.ID || list.Data[0].BookingGroupCode != "GRP1" || list.Data[0].ConfirmationNumber != "" {
		t.Fatalf("list: %+v", list.Data)
	}
	if p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.master_opened'`) != 1 {
		t.Fatal("one audit entry for the opening")
	}
	// the right is its own, and an inactive group takes no master
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioRead, auth.PermFolioPostCharge)
	_, err = p.Folios.OpenMaster(clerk, p.propID, g)
	wantCode(t, err, "PERMISSION_DENIED")
	must(t, p.Exec(t, `DELETE FROM folios WHERE id = $1`, r.Master.ID))
	must(t, p.Exec(t, `UPDATE booking_groups SET is_active = false WHERE id = $1`, g))
	_, err = p.Folios.OpenMaster(p.admin, p.propID, g)
	wantCode(t, err, "GROUP_INACTIVE")
	_, err = p.Folios.OpenMaster(p.admin, p.propID, 999999)
	wantCode(t, err, "GROUP_NOT_FOUND")
}

func TestTheResolverRoutesTheMembersOfAGroupToItsMasterBehindTheirOwnInstructions(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	other := p.otherFolio(t)
	p.joinGroup(t, g, other)
	var otherStay int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT stay_id FROM folios WHERE id = $1`, other).Scan(&otherStay))
	target := func(stay int64, route folios.Route) (int64, bool) {
		got, err := p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, stay, route)
		must(t, err)
		return got.FolioID, got.Routed
	}

	// no instruction: the guest folios, as before
	if id, routed := target(p.stay, folios.RoomRoute); id != p.guestFolio || routed {
		t.Fatalf("no rule: %d %v", id, routed)
	}
	// ROOM: the room nights go to the master, a minibar charge does not
	p.route(t, g, folios.ScopeRoom)
	master := p.openMaster(t, g).ID
	if id, routed := target(p.stay, folios.RoomRoute); id != master || !routed {
		t.Fatalf("room: %d %v", id, routed)
	}
	if id, _ := target(p.stay, folios.Route{ChargeCodeID: p.minibar}); id != p.guestFolio {
		t.Fatalf("a minibar charge goes to the guest: %d", id)
	}
	// the room nights of every member
	if id, _ := target(otherStay, folios.RoomRoute); id != master {
		t.Fatalf("the other member: %d", id)
	}
	// a charge code of its own, and ALL
	p.route(t, g, folios.ScopeRoom, "MINIBAR")
	if id, _ := target(p.stay, folios.Route{ChargeCodeID: p.minibar}); id != master {
		t.Fatalf("the minibar goes to the master: %d", id)
	}
	if id, _ := target(p.stay, folios.Route{ChargeCodeID: p.laundry}); id != p.guestFolio {
		t.Fatalf("the laundry stays with the guest: %d", id)
	}
	p.route(t, g, folios.ScopeAll)
	if id, _ := target(p.stay, folios.Route{ChargeCodeID: p.laundry}); id != master {
		t.Fatalf("all goes to the master: %d", id)
	}

	// a line excluded from the routing keeps to its guest folio
	lines, err := p.Folios.GetGroupRouting(p.admin, p.propID, g)
	must(t, err)
	var excluded int64
	for _, l := range lines.Lines {
		var stay int64
		must(t, p.Pool.QueryRow(context.Background(), `SELECT COALESCE(max(id), 0) FROM stays WHERE reservation_room_id = $1`, l.ReservationRoomID).Scan(&stay))
		if stay == p.stay {
			excluded = l.ReservationRoomID
		}
	}
	if excluded == 0 {
		t.Fatalf("the line of the first stay is not listed: %+v", lines.Lines)
	}
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}}, ExcludedLines: []int64{excluded}})
	must(t, err)
	if id, routed := target(p.stay, folios.RoomRoute); id != p.guestFolio || routed {
		t.Fatalf("an excluded line: %d %v", id, routed)
	}
	if id, _ := target(otherStay, folios.RoomRoute); id != master {
		t.Fatalf("the others still go to the master: %d", id)
	}

	// the instruction of the line itself (a company folio) wins over the group, excluded or not
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}}})
	must(t, err)
	cf := p.companyFolio(t, "FC1")
	var line int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT reservation_room_id FROM stays WHERE id = $1`, p.stay).Scan(&line))
	_, err = p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation, line, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: p.company}})
	must(t, err)
	if id, routed := target(p.stay, folios.RoomRoute); id != cf || !routed {
		t.Fatalf("the instruction of the line wins: %d %v", id, routed)
	}
	if id, _ := target(p.stay, folios.Route{ChargeCodeID: p.laundry}); id != master {
		t.Fatalf("what the line does not name follows the group: %d", id)
	}

	// the batch of the expected-charge loader says the same
	m, err := p.Folios.ResolveRoomTargets(p.admin, p.tenantID, p.propID, []int64{p.stay, otherStay})
	must(t, err)
	if m[p.stay].Default.FolioID != cf || m[otherStay].Default.FolioID != master {
		t.Fatalf("batch: %+v", m)
	}

	// a master that is closed does not stop anything: the rule is not applied and the night goes to the guest folio (the night audit says so as a warning); the instruction of the line itself is another matter
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, master))
	if id, routed := target(otherStay, folios.RoomRoute); id != other || routed {
		t.Fatalf("a closed master: %d %v, want the guest folio %d", id, routed, other)
	}
	if id, routed := target(otherStay, folios.Route{ChargeCodeID: p.laundry}); id != other || routed {
		t.Fatalf("a closed master, another charge: %d %v", id, routed)
	}
	m, err = p.Folios.ResolveRoomTargets(p.admin, p.tenantID, p.propID, []int64{otherStay})
	must(t, err)
	if m[otherStay].Default.FolioID != other || m[otherStay].Default.Routed {
		t.Fatalf("the batch: %+v", m[otherStay])
	}
}

func TestSettingTheRoutingNeedsItsRightValidatesAndIsAudited(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	clerk := p.User(t, p.tenantID, p.propID, auth.PermReservationRead, auth.PermFolioRead)
	_, err := p.Folios.SetGroupRouting(clerk, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}}})
	wantCode(t, err, "PERMISSION_DENIED")
	for name, in := range map[string]folios.GroupRoutingInput{
		"a bad scope":            {Instructions: []folios.GroupInstructionInput{{Scope: "EVERYTHING"}}},
		"a code on ROOM":         {Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeRoom, ChargeCodeID: &p.minibar}}},
		"no code on CHARGE_CODE": {Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeChargeCode}}},
		"the same scope twice":   {Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}, {Scope: folios.ScopeAll}}},
	} {
		_, err := p.Folios.SetGroupRouting(p.admin, p.propID, g, in)
		if !strings.Contains(err.Error(), "invalid") {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{ExcludedLines: []int64{999999}})
	wantCode(t, err, "VALIDATION_FAILED")
	unknown := int64(999999)
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeChargeCode, ChargeCodeID: &unknown}}})
	wantCode(t, err, "VALIDATION_FAILED")
	if p.Count(t, `SELECT count(*) FROM group_billing_instructions`) != 0 || p.Count(t, `SELECT count(*) FROM folios WHERE folio_type = 'MASTER'`) != 0 {
		t.Fatal("a refused request writes nothing")
	}
	// the first instruction opens the master; an empty set clears the instructions and leaves the master
	p.route(t, g, folios.ScopeRoom)
	if p.Count(t, `SELECT count(*) FROM folios WHERE folio_type = 'MASTER' AND status = 'OPEN'`) != 1 || p.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'group.routing_set'`) != 1 {
		t.Fatal("the master is opened by the first instruction, and the change is audited")
	}
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{})
	must(t, err)
	got, err := p.Folios.GetGroupRouting(p.admin, p.propID, g)
	must(t, err)
	if len(got.Instructions) != 0 || got.Master == nil {
		t.Fatalf("routing: %+v", got)
	}
}

// A payment on the master is a deposit before its first charge and a settlement after it; the folios of a stay keep the old rule, row by row.
func TestIsDepositByRoleOfTheFolio(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	master := p.openMaster(t, g).ID
	pay := func(folio int64, key string) {
		_, err := p.Folios.PostPayment(p.admin, p.propID, folio, key, folios.PaymentInput{Amount: "50000", PaymentMethod: "CASH"})
		must(t, err)
	}
	pay(master, "m1") // before the first charge
	_, err := p.Folios.PostCharge(p.admin, p.propID, master, "c1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	pay(master, "m2") // after it
	// the guest folio: a payment before the check-in is a deposit, and after it a settlement (the old rule)
	pay(p.guestFolio, "g1")
	deposit := func(folio int64) []bool {
		rows, err := p.Pool.Query(context.Background(), `SELECT is_deposit FROM folio_item_gl WHERE folio_id = $1 AND payment_id IS NOT NULL ORDER BY item_id`, folio)
		must(t, err)
		defer rows.Close()
		var out []bool
		for rows.Next() {
			var b bool
			must(t, rows.Scan(&b))
			out = append(out, b)
		}
		return out
	}
	if got := deposit(master); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("master: %v, want [true false]", got)
	}
	// every case that exists already: a deposit on the reservation (a folio of no stay), the refund of part of it, a payment that was voided, a transfer to the city ledger
	dep, err := p.Folios.Deposit(p.admin, p.propID, p.reservation, "dep", folios.PaymentInput{Amount: "80000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = p.Folios.Refund(p.admin, p.propID, dep.Payment.ID, "rf", folios.RefundInput{Amount: "20000", Reason: "changed plans", Approval: p.approval()})
	must(t, err)
	voided, err := p.Folios.PostPayment(p.admin, p.propID, p.guestFolio, "g2", folios.PaymentInput{Amount: "30000", PaymentMethod: "CARD"})
	must(t, err)
	_, err = p.Folios.Void(p.admin, p.propID, voided.Payment.ID, folios.CorrectionInput{Reason: "wrong card", Approval: p.approval()})
	must(t, err)
	p.charge(t, p.guestFolio, "cg")
	_, err = p.Folios.Transfer(p.admin, p.propID, p.guestFolio, "tr", folios.TransferInput{CompanyID: p.company, Amount: "10000"})
	must(t, err)
	// on the master: the refund of its deposit follows the deposit, and a transfer to the city ledger is no deposit
	var depItem int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT min(item_id) FROM folio_item_gl WHERE folio_id = $1 AND payment_id IS NOT NULL`, master).Scan(&depItem))
	var firstPayment int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT payment_id FROM folio_items WHERE id = $1`, depItem).Scan(&firstPayment))
	_, err = p.Folios.Refund(p.admin, p.propID, firstPayment, "rfm", folios.RefundInput{Amount: "10000", Reason: "too much", Approval: p.approval()})
	must(t, err)
	_, err = p.Folios.Transfer(p.admin, p.propID, master, "trm", folios.TransferInput{CompanyID: p.company, Amount: "20000"})
	must(t, err)
	if got := deposit(master); len(got) != 4 || !got[0] || got[1] || !got[2] || got[3] {
		t.Fatalf("master rows (deposit, settlement, refund of the deposit, city ledger): %v, want [true false true false]", got)
	}
	// the old expression, evaluated by hand for every row of a stay or of no stay, is what the view says for them
	var diff int
	must(t, p.Pool.QueryRow(context.Background(), `SELECT count(*) FROM folio_item_gl v
		JOIN folio_items fi ON fi.id = v.item_id
		JOIN folios f ON f.id = fi.folio_id AND f.folio_type <> 'MASTER'
		JOIN payments pay ON pay.id = v.payment_id
		JOIN payments rp ON rp.id = COALESCE(pay.refund_of_payment_id, pay.id)
		LEFT JOIN stays st ON st.id = f.stay_id
		WHERE v.is_deposit IS DISTINCT FROM (rp.payment_method <> 'CITY_LEDGER' AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at))`).Scan(&diff))
	if diff != 0 {
		t.Fatalf("%d rows of a folio of a stay differ from the old rule", diff)
	}
	// a city ledger transfer is never a deposit, on a master as anywhere
	var cl bool
	must(t, p.Pool.QueryRow(context.Background(), `SELECT COALESCE(bool_or(is_deposit), false) FROM folio_item_gl WHERE folio_id = $1 AND payment_method = 'CITY_LEDGER'`, master).Scan(&cl))
	if cl {
		t.Fatal("a transfer to the city ledger is not a deposit")
	}
}

func TestClosingTheMasterNeedsItsRightAZeroBalanceAndEveryRoomOver(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	master := p.openMaster(t, g)
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioRead, auth.PermFolioPostCharge)
	closer := p.User(t, p.tenantID, p.propID, auth.PermFolioRead, auth.PermFolioMasterClose)
	// an ordinary cashier does not close the bill of a whole group
	_, err := p.Folios.CloseFolio(clerk, p.propID, master.ID, master.Version)
	wantCode(t, err, "PERMISSION_DENIED")
	// a room of the group is not over: the master stays open, and the answer names the line
	_, err = p.Folios.CloseFolio(closer, p.propID, master.ID, master.Version)
	e := code(t, err, "MASTER_FOLIO_GROUP_ACTIVE")
	if e.Context["lines_total"] != 1 {
		t.Fatalf("context: %v", e.Context)
	}
	must(t, p.Exec(t, `UPDATE reservation_rooms SET status = 'COMPLETED' WHERE reservation_id = $1`, p.reservation))
	// a balance
	_, err = p.Folios.PostCharge(p.admin, p.propID, master.ID, "c1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	cur, err := p.Folios.GetFolio(p.admin, p.propID, master.ID)
	must(t, err)
	_, err = p.Folios.CloseFolio(closer, p.propID, master.ID, cur.Version)
	wantCode(t, err, "FOLIO_BALANCE_NOT_ZERO")
	// paid, then closed; the day it closed is recorded; a second close is refused
	_, err = p.Folios.PostPayment(p.admin, p.propID, master.ID, "pay", folios.PaymentInput{Amount: cur.Balance, PaymentMethod: "CASH"})
	must(t, err)
	cur, err = p.Folios.GetFolio(p.admin, p.propID, master.ID)
	must(t, err)
	closed, err := p.Folios.CloseFolio(closer, p.propID, master.ID, cur.Version)
	must(t, err)
	if closed.Status != "CLOSED" || p.Count(t, `SELECT count(*) FROM folios WHERE id = $1 AND closed_on IS NOT NULL`, master.ID) != 1 {
		t.Fatalf("closed: %+v", closed)
	}
	_, err = p.Folios.CloseFolio(closer, p.propID, master.ID, closed.Version)
	wantCode(t, err, "FOLIO_CLOSED")
	// a master closed by mistake is reopened, not replaced
	_, err = p.Folios.OpenMaster(p.admin, p.propID, g)
	must(t, err)
	if p.Count(t, `SELECT count(*) FROM folios WHERE folio_type = 'MASTER'`) != 1 {
		t.Fatal("one master for each group")
	}
}

func TestTwoClosesOfTheMasterAndAPaymentAgainstItsClose(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	master := p.openMaster(t, g)
	must(t, p.Exec(t, `UPDATE reservation_rooms SET status = 'COMPLETED' WHERE reservation_id = $1`, p.reservation))
	closer := p.User(t, p.tenantID, p.propID, auth.PermFolioRead, auth.PermFolioMasterClose)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.Folios.CloseFolio(closer, p.propID, master.ID, master.Version)
		}()
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else if !strings.Contains(e.Error(), "closed") && !strings.Contains(e.Error(), "changed") {
			t.Fatalf("the loser must be told the folio is closed or was changed: %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("one of two closes wins: %v", errs)
	}
	// a payment after the close is refused
	_, err := p.Folios.PostPayment(p.admin, p.propID, master.ID, "late", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	wantCode(t, err, "FOLIO_CLOSED")

	// a payment racing the close of another master: whichever takes the folio lock first; either the folio closes with a zero balance or the payment makes the close fail, never both half done
	g2 := p.group2(t)
	m2 := p.openMaster(t, g2)
	var wg2 sync.WaitGroup
	var closeErr, payErr error
	wg2.Add(2)
	go func() {
		defer wg2.Done()
		_, closeErr = p.Folios.CloseFolio(closer, p.propID, m2.ID, m2.Version)
	}()
	go func() {
		defer wg2.Done()
		_, payErr = p.Folios.PostPayment(p.admin, p.propID, m2.ID, "race", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	}()
	wg2.Wait()
	var bal string
	must(t, p.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0)::numeric(18,3)::text FROM folio_items WHERE folio_id = $1`, m2.ID).Scan(&bal))
	var status string
	must(t, p.Pool.QueryRow(context.Background(), `SELECT status FROM folios WHERE id = $1`, m2.ID).Scan(&status))
	if status == "CLOSED" && bal != "0.000" {
		t.Fatalf("a folio closed with a balance %s (close %v, payment %v)", bal, closeErr, payErr)
	}
}

// group2 is a second group whose only reservation is the one of the other stay.
func (p *payerFx) group2(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date) VALUES ($1, $2, 'GRP2', 'Wedding', '2026-10-01', '2026-10-03') RETURNING id`, p.tenantID, p.propID).Scan(&id))
	return id
}

func TestTransfersWithTheMasterFolioHaveTheirOwnScope(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	master := p.openMaster(t, g).ID
	item := p.charge(t, p.guestFolio, "k")
	// a member to its master: GROUP_MASTER, with the ordinary approval
	res, err := p.moveItems(p.guestFolio, master, p.items(item))
	must(t, err)
	if res.Scope != "GROUP_MASTER" || p.balance(t, p.guestFolio) != "0" || p.balance(t, master) != "122100" {
		t.Fatalf("to the master: %s %s %s", res.Scope, p.balance(t, p.guestFolio), p.balance(t, master))
	}
	// the master back to a member, and a balance both ways
	back, err := p.Folios.TransferBalance(p.admin, p.propID, master, "b1", folios.TransferBalanceInput{FolioID: p.guestFolio, Amount: "22100", Reason: "the guest pays that part", Approval: p.approval()})
	must(t, err)
	if back.Scope != "GROUP_MASTER" || p.balance(t, master) != "100000" || p.balance(t, p.guestFolio) != "22100" {
		t.Fatalf("from the master: %s %s %s", back.Scope, p.balance(t, master), p.balance(t, p.guestFolio))
	}
	// the return of what went to the master sends it back to the member
	var tid int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT id FROM folio_transfers WHERE to_folio_id = $1 AND original_item_id IS NOT NULL`, master).Scan(&tid))
	_, err = p.Folios.ReturnTransfers(p.admin, p.propID, master, folios.ReturnInput{TransferIDs: []int64{tid}, To: "PREVIOUS", Reason: "wrong bill", Approval: p.approval()})
	must(t, err)
	if p.balance(t, master) != "-22100" {
		t.Fatalf("after the return: %s", p.balance(t, master))
	}
	// a member of another group (a folio of no relation) is ANY: it needs folio.transfer_any
	other := p.otherFolio(t)
	item2 := p.charge(t, other, "k2")
	_, err = p.Folios.TransferItems(p.admin, p.propID, other, folios.TransferItemsInput{FolioID: master, ItemIDs: []int64{item2}, Reason: "x", Approval: p.approval()})
	if err != nil {
		t.Logf("an unrelated folio to a master needs transfer_any, the admin holds it: %v", err)
	}
	if got := p.Count(t, `SELECT count(*) FROM folio_transfer_batches WHERE scope = 'ANY'`); got != 1 {
		t.Fatalf("a folio of another reservation and group goes to the master as ANY: %d batches", got)
	}
	// two masters do not transfer to each other
	g2 := p.group2(t)
	m2 := p.openMaster(t, g2).ID
	_, err = p.Folios.TransferBalance(p.admin, p.propID, master, "b2", folios.TransferBalanceInput{FolioID: m2, Amount: "1", Reason: "x", Approval: p.approval()})
	wantCode(t, err, "FOLIO_TRANSFER_INVALID")
	// the targets: the master of the group is offered to a member, and the members to the master
	targets, err := p.Folios.TransferTargets(p.admin, p.propID, p.guestFolio, "", 25)
	must(t, err)
	found := false
	for _, x := range targets {
		if x.ID == master {
			found = x.Scope == "GROUP_MASTER" && x.Guest == "Conference" && x.FolioType == "MASTER"
		}
	}
	if !found {
		t.Fatalf("targets of a member: %+v", targets)
	}
	targets, err = p.Folios.TransferTargets(p.admin, p.propID, master, "", 25)
	must(t, err)
	found = false
	for _, x := range targets {
		if x.ID == p.guestFolio {
			found = x.Scope == "GROUP_MASTER"
		}
	}
	if !found {
		t.Fatalf("targets of the master: %+v", targets)
	}
}

func TestWhereTheChargesOfALineGo(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	var line int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT reservation_room_id FROM stays WHERE id = $1`, p.stay).Scan(&line))
	n, err := p.Folios.GetLineRouting(p.admin, p.propID, p.reservation, line)
	must(t, err)
	if n.Room != "NONE" || n.Group == nil || n.Group.Code != "GRP1" || n.Excluded || len(n.GroupInstructions) != 0 {
		t.Fatalf("no instruction: %+v", n)
	}
	p.route(t, g, folios.ScopeRoom)
	n, err = p.Folios.GetLineRouting(p.admin, p.propID, p.reservation, line)
	must(t, err)
	if n.Room != "GROUP" || n.Group.MasterNumber == "" || n.Group.MasterStatus != "OPEN" {
		t.Fatalf("the group routes the room: %+v", n)
	}
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeRoom}}, ExcludedLines: []int64{line}})
	must(t, err)
	n, err = p.Folios.GetLineRouting(p.admin, p.propID, p.reservation, line)
	must(t, err)
	if n.Room != "NONE" || !n.Excluded {
		t.Fatalf("excluded: %+v", n)
	}
	_, err = p.Folios.SetBillingInstructions(p.admin, p.propID, p.reservation, line, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: p.company}})
	must(t, err)
	n, err = p.Folios.GetLineRouting(p.admin, p.propID, p.reservation, line)
	must(t, err)
	if n.Room != "LINE" {
		t.Fatalf("the instruction of the line: %+v", n)
	}
	// a reservation outside a group has no group part
	other := p.otherFolio(t)
	var res, oline int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT f.reservation_id, s.reservation_room_id FROM folios f JOIN stays s ON s.id = f.stay_id WHERE f.id = $1`, other).Scan(&res, &oline))
	n, err = p.Folios.GetLineRouting(p.admin, p.propID, res, oline)
	must(t, err)
	if n.Group != nil || n.Room != "NONE" {
		t.Fatalf("no group: %+v", n)
	}
}

// What is already posted when a rule is saved is offered to be moved: the charges the rule would send to the master, not those of an excluded room or already reversed.
func TestTheChargesAlreadyPostedThatARuleWouldSendToTheMaster(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	a := p.charge(t, p.guestFolio, "k1")
	p.charge(t, p.guestFolio, "k2")
	_, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "l1", folios.ChargeInput{ChargeCodeID: p.laundry, Quantity: "1", UnitPrice: ptr("50000")})
	must(t, err)
	list, err := p.Folios.PostedForMaster(p.admin, p.propID, g)
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("no instruction, nothing to move: %+v", list)
	}
	p.route(t, g, "MINIBAR")
	list, err = p.Folios.PostedForMaster(p.admin, p.propID, g)
	must(t, err)
	if len(list) != 1 || list[0].ChargeCode != "MINIBAR" || list[0].Count != 2 || list[0].FolioID != p.guestFolio || list[0].ConfirmationNumber == "" {
		t.Fatalf("the minibar charges: %+v", list)
	}
	// a charge that was reversed is not offered
	_, err = p.Folios.Reverse(p.admin, p.propID, a, folios.CorrectionInput{Reason: "x", Approval: p.approval()})
	must(t, err)
	if list, err = p.Folios.PostedForMaster(p.admin, p.propID, g); err != nil || len(list) != 1 || list[0].Count != 1 {
		t.Fatalf("after a reversal: %+v %v", list, err)
	}
	// ALL offers the laundry too, and an excluded room offers nothing
	p.route(t, g, folios.ScopeAll)
	if list, err = p.Folios.PostedForMaster(p.admin, p.propID, g); err != nil || len(list) != 2 {
		t.Fatalf("all: %+v %v", list, err)
	}
	var line int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT reservation_room_id FROM stays WHERE id = $1`, p.stay).Scan(&line))
	_, err = p.Folios.SetGroupRouting(p.admin, p.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}}, ExcludedLines: []int64{line}})
	must(t, err)
	if list, err = p.Folios.PostedForMaster(p.admin, p.propID, g); err != nil || len(list) != 0 {
		t.Fatalf("excluded: %+v %v", list, err)
	}
	// and moving what was offered is the transfer of part B, by charge code, with the scope of the master
	p.route(t, g, folios.ScopeAll)
	master := p.masterIDOf(t, g)
	res, err := p.moveItems(p.guestFolio, master, folios.TransferItemsInput{ChargeCodeID: &p.laundry, Reason: "the group pays", Approval: p.approval()})
	must(t, err)
	if res.Scope != "GROUP_MASTER" || p.balance(t, master) != "50000" {
		t.Fatalf("moved: %s %s", res.Scope, p.balance(t, master))
	}
}

func (p *payerFx) masterIDOf(t *testing.T, group int64) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE booking_group_id = $1 AND folio_type = 'MASTER'`, group).Scan(&id))
	return id
}

// A line of the master folio says whose it is: the stay a charge was posted for, the stay of a charge moved from a member, with the room and the guest.
func TestALineOfTheMasterSaysWhoseItIs(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	master := p.openMaster(t, g).ID
	other := p.otherFolio(t) // a stay of a reservation that is in no group
	var otherStay int64
	must(t, p.Pool.QueryRow(context.Background(), `SELECT stay_id FROM folios WHERE id = $1`, other).Scan(&otherStay))

	// a charge posted to the master for a guest of the group
	res, err := p.Folios.PostCharge(p.admin, p.propID, master, "c1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000"), StayID: &p.stay})
	must(t, err)
	if res.Item.StayID == nil || *res.Item.StayID != p.stay || res.Item.GuestName == "" || res.Item.RoomNumber != "101" {
		t.Fatalf("the line: %+v", res.Item)
	}
	// a charge with no guest says nothing, and the guest folio has no guest on its lines
	plain, err := p.Folios.PostCharge(p.admin, p.propID, master, "c2", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("50000")})
	must(t, err)
	if plain.Item.StayID != nil || plain.Item.GuestName != "" {
		t.Fatalf("a charge for nobody: %+v", plain.Item)
	}
	mine, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "c3", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("10000")})
	must(t, err)
	if mine.Item.GuestName != "" {
		t.Fatalf("a guest folio does not repeat the guest on every line: %+v", mine.Item)
	}
	// only on the master, and only for a stay of the group
	_, err = p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, "c4", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("1"), StayID: &p.stay})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.Folios.PostCharge(p.admin, p.propID, master, "c5", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("1"), StayID: &otherStay})
	wantCode(t, err, "VALIDATION_FAILED")

	// a charge moved from a member to the master keeps the stay it was for, and goes back to a member under the stay of that folio
	item := p.charge(t, p.guestFolio, "k1")
	_, err = p.moveItems(p.guestFolio, master, p.items(item))
	must(t, err)
	f, err := p.Folios.GetFolio(p.admin, p.propID, master)
	must(t, err)
	named := 0
	for _, it := range f.Items {
		if it.TransactionType == "CHARGE" && it.StayID != nil && *it.StayID == p.stay && it.GuestName != "" {
			named++
		}
	}
	if named != 2 {
		t.Fatalf("%d lines name the guest, want the posted charge and the moved one", named)
	}
	// the lines of the group tell the charge form who is in each room
	r, err := p.Folios.GetGroupRouting(p.admin, p.propID, g)
	must(t, err)
	if len(r.Lines) != 1 || r.Lines[0].StayID == nil || *r.Lines[0].StayID != p.stay || r.Lines[0].RoomNumber != "101" || r.Lines[0].GuestName == "" {
		t.Fatalf("lines: %+v", r.Lines)
	}
}

// Moving what is posted to the master is one transaction over every member folio: all or nothing, one approval, the batches of the log under the key of the request, and a repeat moves nothing twice.
func TestMovingThePostedChargesToTheMasterIsAllOrNothingAndIdempotent(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	other := p.otherFolio(t)
	p.joinGroup(t, g, other)
	p.charge(t, p.guestFolio, "a1")
	p.charge(t, p.guestFolio, "a2")
	p.charge(t, other, "b1")
	p.route(t, g, "MINIBAR")
	master := p.masterIDOf(t, g)
	list, err := p.Folios.PostedForMaster(p.admin, p.propID, g)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("two folios, one code: %+v", list)
	}
	in := func(count int, total string) folios.MoveToMasterInput {
		return folios.MoveToMasterInput{Reason: "the group pays the minibar", Approval: p.approval(), ExpectedCount: count, ExpectedTotal: total}
	}

	// the list changed since it was shown: the approval was for another list, nothing moves
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-stale", in(3, "999"))
	wantCode(t, err, "POSTED_LIST_CHANGED")
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-stale2", in(2, "366300"))
	wantCode(t, err, "POSTED_LIST_CHANGED")
	if p.Count(t, `SELECT count(*) FROM folio_transfers`) != 0 || p.balance(t, master) != "0" {
		t.Fatal("a refused request moves nothing")
	}
	// the shape of the request
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-bad", folios.MoveToMasterInput{Approval: p.approval(), ExpectedCount: 3, ExpectedTotal: "366300"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "", in(3, "366300"))
	wantCode(t, err, "VALIDATION_FAILED")
	clerk := p.User(t, p.tenantID, p.propID, auth.PermFolioRead)
	_, err = p.Folios.MoveToMaster(clerk, p.propID, g, "k-clerk", in(3, "366300"))
	wantCode(t, err, "PERMISSION_DENIED")

	// a master that is closed takes nothing
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, master))
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-closed", in(3, "366300"))
	wantCode(t, err, "FOLIO_CLOSED")
	must(t, p.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, master))

	// the move: everything, once
	res, err := p.Folios.MoveToMaster(p.admin, p.propID, g, "k-move", in(3, "366300"))
	must(t, err)
	if res.Replayed || res.Moved != 3 || len(res.Batches) != 2 || res.Total != "366300" || p.balance(t, master) != "366300" || p.balance(t, p.guestFolio) != "0" || p.balance(t, other) != "0" {
		t.Fatalf("moved: %+v, master %s", res, p.balance(t, master))
	}
	if p.Count(t, `SELECT count(*) FROM folio_transfer_batches WHERE idempotency_key LIKE 'k-move#%' AND scope = 'GROUP_MASTER'`) != 2 {
		t.Fatal("each folio and code is a batch of the log under the key of the request")
	}
	// the same request again answers with what was moved and moves nothing
	again, err := p.Folios.MoveToMaster(p.admin, p.propID, g, "k-move", in(3, "366300"))
	must(t, err)
	if !again.Replayed || again.Moved != 3 || p.Count(t, `SELECT count(*) FROM folio_transfers`) != 3 || p.balance(t, master) != "366300" {
		t.Fatalf("replay: %+v", again)
	}
	// and a new request has nothing left to move
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-again", in(3, "366300"))
	wantCode(t, err, "TRANSFER_SELECTION_EMPTY")
}

// A failure in the middle of the move rolls back what was moved before it: the first folio does not stay moved.
func TestAFailureInTheMiddleOfTheMoveToTheMasterMovesNothing(t *testing.T) {
	p := setupPayer(t)
	g := p.group(t)
	other := p.otherFolio(t)
	p.joinGroup(t, g, other)
	p.charge(t, p.guestFolio, "a1")
	// the second folio has more charges of the code than one transfer takes: the move of it fails after the first folio was moved
	for i := 0; i <= folios.MaxTransferItems; i++ {
		p.charge(t, other, fmt.Sprintf("b%d", i))
	}
	p.route(t, g, "MINIBAR")
	master := p.masterIDOf(t, g)
	list, err := p.Folios.PostedForMaster(p.admin, p.propID, g)
	must(t, err)
	count := 0
	for _, r := range list {
		count += r.Count
	}
	total := p.balance(t, p.guestFolio)
	_ = total
	var sum string
	must(t, p.Pool.QueryRow(context.Background(), `SELECT sum(debit - credit)::text FROM folio_items WHERE folio_id IN ($1, $2)`, p.guestFolio, other).Scan(&sum))
	_, err = p.Folios.MoveToMaster(p.admin, p.propID, g, "k-fail", folios.MoveToMasterInput{Reason: "x", Approval: p.approval(), ExpectedCount: count, ExpectedTotal: strings.TrimSuffix(sum, ".000")})
	wantCode(t, err, "TOO_MANY_ITEMS")
	if p.Count(t, `SELECT count(*) FROM folio_transfers`) != 0 || p.Count(t, `SELECT count(*) FROM folio_transfer_batches`) != 0 || p.balance(t, master) != "0" || p.balance(t, p.guestFolio) != "122100" {
		t.Fatalf("nothing moved: master %s, first folio %s", p.balance(t, master), p.balance(t, p.guestFolio))
	}
}
