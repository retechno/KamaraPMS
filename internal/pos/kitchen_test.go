package pos_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/pos"
)

// The kitchen display, the queue of print jobs and the events of the screens (docs/architecture/34-pos.md stage 3a), on a real database.

type kitchenFx struct {
	*billFx
	grill, bar   pos.Station
	satay, juice pos.Product
	kds          context.Context
	printer      pos.Printer
	tbl          int64
}

func newKitchenFx(t *testing.T) *kitchenFx {
	t.Helper()
	b := newBillFx(t)
	k := &kitchenFx{billFx: b}
	pr, err := b.svc.CreatePrinter(b.admin, b.propID, pos.PrinterInput{Name: "Kitchen printer", Kind: "KITCHEN", PaperWidthMM: 80, Codepage: "CP437"})
	must(t, err)
	k.printer = pr
	k.grill, err = b.svc.CreateStation(b.admin, b.propID, pos.StationInput{Code: "GRILL", Name: "Grill", PrinterID: &pr.ID, KDSEnabled: true, LateAfterMinutes: 15})
	must(t, err)
	k.bar, err = b.svc.CreateStation(b.admin, b.propID, pos.StationInput{Code: "BAR", Name: "Bar", KDSEnabled: true, LateAfterMinutes: 5})
	must(t, err)
	code := b.chargeCode(t, "RESTAURANT")
	mk := func(c, name, price string, st int64) pos.Product {
		p, err := b.svc.CreateProduct(b.admin, b.propID, pos.ProductInput{Code: c, Name: name, CategoryID: b.k.cat.ID, ChargeCodeID: code, StationID: &st,
			Prices: []pos.ProductPriceIn{{PriceListID: b.k.priceList.ID, Price: dec(price), IsSold: true}}})
		must(t, err)
		return p
	}
	k.satay, k.juice = mk("SATAY", "Sate ayam", "40000", k.grill.ID), mk("JUICE", "Jus alpukat", "25000", k.bar.ID)
	k.kds = b.User(t, b.tenantID, b.propID, auth.PermPOSKDS)
	return k
}

// sent opens a bill at a table and sends a satay and a juice.
func (k *kitchenFx) sent(t *testing.T) pos.Bill {
	t.Helper()
	if k.tbl == 0 {
		k.tbl = k.table(t)
	}
	bill := k.open(t, k.waiterC, &k.tbl)
	bill = k.add(t, k.waiterC, bill, k.satay, "2")
	bill = k.add(t, k.waiterC, bill, k.juice, "1")
	return k.send(t, k.waiterC, bill)
}

func (k *kitchenFx) board(t *testing.T, st pos.Station) pos.StationBoard {
	t.Helper()
	b, err := k.svc.StationBoard(k.kds, k.propID, st.ID)
	must(t, err)
	return b
}

func TestASendMakesOneTicketForEachStationAndAJobForAStationWithAPrinter(t *testing.T) {
	k := newKitchenFx(t)
	bill := k.sent(t)
	g, b := k.board(t, k.grill), k.board(t, k.bar)
	if len(g.Tickets) != 1 || len(b.Tickets) != 1 || g.Tickets[0].Status != pos.TicketNew || g.Tickets[0].BillNumber != bill.Number {
		t.Fatalf("boards: %+v / %+v", g.Tickets, b.Tickets)
	}
	tk := g.Tickets[0]
	if len(tk.Lines) != 1 || tk.Lines[0].Name != "Sate ayam" || tk.Lines[0].Quantity.String() != "2" || tk.TableCode != "T5" || tk.IsAddition || tk.OutletName != "Restoran" {
		t.Fatalf("ticket: %+v", tk)
	}
	if len(g.Outlets) != 1 || g.Outlets[0].Name != "Restoran" {
		t.Fatalf("the outlets of the station: %+v", g.Outlets)
	}
	// the outlet works in PRINT mode: the grill has a printer, so a job waits; the bar has none
	q, err := k.svc.ListPrintJobs(k.admin, k.propID, nil, "", 0)
	must(t, err)
	if len(q.Jobs) != 1 || q.Jobs[0].Status != "QUEUED" || q.Jobs[0].PrinterID != k.printer.ID || q.Jobs[0].Station != "Grill" || q.Jobs[0].TableCode != "T5" || q.Queued[k.printer.ID] != 1 {
		t.Fatalf("queue: %+v", q)
	}
	// a second round is a second ticket, marked as an addition
	bill = k.add(t, k.waiterC, bill, k.satay, "1")
	k.send(t, k.waiterC, bill)
	g = k.board(t, k.grill)
	if len(g.Tickets) != 2 || !g.Tickets[1].IsAddition || g.Tickets[0].IsAddition {
		t.Fatalf("an addition: %+v", g.Tickets)
	}
}

func TestATicketGoesNewCookingReadyServedAndTheBillShowsIt(t *testing.T) {
	k := newKitchenFx(t)
	bill := k.sent(t)
	id := k.board(t, k.grill).Tickets[0].ID
	status := func() string {
		b, err := k.svc.GetBill(k.waiterC, k.propID, bill.ID)
		must(t, err)
		for _, l := range b.Lines {
			if l.ProductName == "Sate ayam" {
				return l.TicketStatus
			}
		}
		return ""
	}
	if status() != "NEW" {
		t.Fatalf("status %q", status())
	}
	// only the kitchen starts it; a waiter may not
	_, err := k.svc.AdvanceTicket(k.waiterC, k.propID, id, pos.TicketStart)
	wantCode(t, err, "PERMISSION_DENIED")
	m, err := k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketStart)
	must(t, err)
	if m.Status != "COOKING" || !m.Changed || status() != "COOKING" {
		t.Fatalf("started: %+v %q", m, status())
	}
	// serving needs it ready
	_, err = k.svc.AdvanceTicket(k.waiterC, k.propID, id, pos.TicketServe)
	wantCode(t, err, "TICKET_STATUS_INVALID")
	m, err = k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketReadyAction)
	must(t, err)
	if m.Status != "READY" || status() != "READY" {
		t.Fatalf("ready: %+v", m)
	}
	// start again after ready is a conflict, ready again is the same answer
	_, err = k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketStart)
	wantCode(t, err, "TICKET_STATUS_INVALID")
	m, err = k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketReadyAction)
	must(t, err)
	if m.Changed {
		t.Fatalf("a repeat changes nothing: %+v", m)
	}
	// the waiter picks it up
	m, err = k.svc.AdvanceTicket(k.waiterC, k.propID, id, pos.TicketServe)
	must(t, err)
	if m.Status != "SERVED" || status() != "SERVED" {
		t.Fatalf("served: %+v", m)
	}
	b := k.board(t, k.grill)
	if len(b.Tickets) != 0 || len(b.History) != 1 {
		t.Fatalf("served tickets are history: %+v", b)
	}
	var by int64
	must(t, k.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE entity_type = 'pos_ticket' AND entity_id = $1`, id).Scan(&by))
	if by != 3 {
		t.Fatalf("three changes audited, got %d", by)
	}
	// a ticket that does not exist, or of another station's kind of action
	_, err = k.svc.AdvanceTicket(k.kds, k.propID, 999999, pos.TicketStart)
	wantCode(t, err, "TICKET_NOT_FOUND")
	_, err = k.svc.AdvanceTicket(k.kds, k.propID, id, "EAT")
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestTwoKitchenScreensPressReadyAtOnceAndOneChangeIsMade(t *testing.T) {
	k := newKitchenFx(t)
	k.sent(t)
	id := k.board(t, k.grill).Tickets[0].ID
	_, err := k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketStart)
	must(t, err)
	for round := 0; round < 3; round++ {
		if round > 0 { // a fresh ticket for each round of the race
			k.sent(t)
			id = k.board(t, k.grill).Tickets[len(k.board(t, k.grill).Tickets)-1].ID
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		res := make([]pos.TicketMoved, 2)
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				res[i], errs[i] = k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketReadyAction)
			}(i)
		}
		close(start)
		wg.Wait()
		changed := 0
		for i := range res {
			if errs[i] != nil || res[i].Status != "READY" {
				t.Fatalf("round %d screen %d: %+v %v", round, i, res[i], errs[i])
			}
			if res[i].Changed {
				changed++
			}
		}
		if changed != 1 {
			t.Fatalf("round %d: %d changes, want 1", round, changed)
		}
		var n int
		must(t, k.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE entity_type = 'pos_ticket' AND entity_id = $1 AND action = 'pos.ticket_ready'`, id).Scan(&n))
		if n != 1 {
			t.Fatalf("round %d: %d audit rows", round, n)
		}
	}
}

func TestAVoidedLineIsStruckThroughOnTheTicketAndTheKitchenIsToldByPrint(t *testing.T) {
	k := newKitchenFx(t)
	bill := k.sent(t)
	var lineID int64
	for _, l := range bill.Lines {
		if l.ProductName == "Sate ayam" {
			lineID = l.ID
		}
	}
	_, err := k.svc.VoidLine(k.cashier, k.propID, bill.ID, lineID, pos.VoidLineInput{Reason: "wrong order", Approval: k.superIn})
	must(t, err)
	g := k.board(t, k.grill)
	if len(g.Tickets) != 1 || len(g.Tickets[0].Lines) != 1 || !g.Tickets[0].Lines[0].Void {
		t.Fatalf("the line stays on the ticket, struck through: %+v", g.Tickets)
	}
	// the cancellation is a ticket of its own for the printer, not a card of the display
	q, err := k.svc.ListPrintJobs(k.admin, k.propID, nil, "", 0)
	must(t, err)
	kinds := map[string]int{}
	for _, j := range q.Jobs {
		kinds[j.TicketKind]++
	}
	if len(q.Jobs) != 2 || kinds["NEW"] != 1 || kinds["VOID"] != 1 {
		t.Fatalf("jobs: %+v", q.Jobs)
	}
}

func TestEventsFollowTheChangesAndAStreamResumesWithoutLosingOne(t *testing.T) {
	k := newKitchenFx(t)
	ctx := k.waiterC
	channels := []string{"outlet:" + itoa(k.resto.ID), "station:" + itoa(k.grill.ID)}
	cur, err := k.svc.EventsStart(ctx, k.propID, 0)
	must(t, err)
	p, _ := auth.Require(ctx)

	bill := k.sent(t) // a bill, lines, a round, tickets
	evs, c1, err := k.svc.EventsSince(ctx, p.TenantID, k.propID, channels, cur)
	must(t, err)
	types := map[string]int{}
	for _, e := range evs {
		types[e.Channel+" "+e.Type]++
	}
	if types["outlet:"+itoa(k.resto.ID)+" bill.updated"] < 1 || types["outlet:"+itoa(k.resto.ID)+" table.updated"] < 1 || types["station:"+itoa(k.grill.ID)+" ticket.created"] != 1 {
		t.Fatalf("events: %+v", types)
	}
	// nothing more until something changes
	more, _, err := k.svc.EventsSince(ctx, p.TenantID, k.propID, channels, c1)
	must(t, err)
	if len(more) != 0 {
		t.Fatalf("no new events, got %+v", more)
	}
	// the connection drops; meanwhile the kitchen starts the ticket and the waiter adds a line
	id := k.board(t, k.grill).Tickets[0].ID
	_, err = k.svc.AdvanceTicket(k.kds, k.propID, id, pos.TicketStart)
	must(t, err)
	k.add(t, k.waiterC, bill, k.satay, "1")
	// it comes back with the id of the last event it had: it gets what it missed, once
	resume, err := k.svc.EventsStart(ctx, k.propID, evs[len(evs)-1].ID)
	must(t, err)
	missed, c2, err := k.svc.EventsSince(ctx, p.TenantID, k.propID, channels, resume)
	must(t, err)
	got := map[string]bool{}
	for _, e := range missed {
		got[e.Type] = true
		if e.ID <= evs[len(evs)-1].ID {
			t.Fatalf("an event it already had: %+v", e)
		}
	}
	if !got["ticket.updated"] || !got["bill.updated"] {
		t.Fatalf("missed: %+v", missed)
	}
	again, _, err := k.svc.EventsSince(ctx, p.TenantID, k.propID, channels, c2)
	must(t, err)
	if len(again) != 0 {
		t.Fatalf("read twice: %+v", again)
	}
	// an id the outbox does not hold is gone: the client reloads
	_, err = k.svc.EventsStart(ctx, k.propID, 99999999)
	wantCode(t, err, "EVENTS_GONE")
}

func TestEventsOfAnOutletAreOfThatOutletAndOfThatPropertyOnly(t *testing.T) {
	k := newKitchenFx(t)
	other, err := k.svc.CreateOutlet(k.admin, k.propID, pos.OutletInput{Code: "BARB", Name: "Bar", OutletType: "BAR", OrderMode: "PRINT", DepartmentID: k.dept, RuleProfileID: k.k.profile.ID,
		PriceListID: k.k.priceList.ID, CompTreatment: "PAYMENT_COMP"})
	must(t, err)
	p, _ := auth.Require(k.waiterC)
	cur, err := k.svc.EventsStart(k.waiterC, k.propID, 0)
	must(t, err)
	k.sent(t) // an order of the restaurant
	evs, _, err := k.svc.EventsSince(k.waiterC, p.TenantID, k.propID, []string{"outlet:" + itoa(other.ID)}, cur)
	must(t, err)
	if len(evs) != 0 {
		t.Fatalf("another outlet's channel stays quiet: %+v", evs)
	}
	// another property of the same tenant, another tenant
	otherProp := k.Property(t, k.tenantID, "UBUD").ID
	evs, _, err = k.svc.EventsSince(k.waiterC, p.TenantID, otherProp, []string{"outlet:" + itoa(k.resto.ID)}, pos.EventCursor{})
	must(t, err)
	if len(evs) != 0 {
		t.Fatalf("another property: %+v", evs)
	}
	xyz := k.Tenant(t, "XYZ")
	xp := k.Property(t, xyz.ID, "SG")
	foreign := k.User(t, xyz.ID, xp.ID, auth.PermPOSOrder, auth.PermPOSKDS, auth.PermPOSManage, auth.PermPOSCashier)
	_, err = k.svc.AuthoriseChannels(foreign, k.propID, []string{"outlet:" + itoa(k.resto.ID)})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = k.svc.AuthoriseChannels(foreign, xp.ID, []string{"outlet:" + itoa(k.resto.ID)})
	wantCode(t, err, "OUTLET_NOT_FOUND")
	_, err = k.svc.AuthoriseChannels(foreign, xp.ID, []string{"station:" + itoa(k.grill.ID)})
	wantCode(t, err, "STATION_NOT_FOUND")
}

func TestAChannelNeedsTheRightPermission(t *testing.T) {
	k := newKitchenFx(t)
	o, st := "outlet:"+itoa(k.resto.ID), "station:"+itoa(k.grill.ID)
	if _, err := k.svc.AuthoriseChannels(k.waiterC, k.propID, []string{o}); err != nil {
		t.Fatal(err)
	}
	_, err := k.svc.AuthoriseChannels(k.waiterC, k.propID, []string{st})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = k.svc.AuthoriseChannels(k.waiterC, k.propID, []string{"frontdesk"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = k.svc.AuthoriseChannels(k.waiterC, k.propID, []string{"printers"})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := k.svc.AuthoriseChannels(k.kds, k.propID, []string{st}); err != nil {
		t.Fatal(err)
	}
	_, err = k.svc.AuthoriseChannels(k.kds, k.propID, []string{o})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := k.svc.AuthoriseChannels(k.opener, k.propID, []string{"frontdesk"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "outlet:x", "table:1", "outlet:1,,station", "outlet:1;frontdesk"} {
		if _, err := pos.ParseChannels(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if chans, err := pos.ParseChannels("outlet:3, station:2,frontdesk,outlet:3"); err != nil || len(chans) != 3 {
		t.Fatalf("channels: %v %v", chans, err)
	}
}

func TestTwoWaitersAddToOneBillAndBothSeeTheOthersLine(t *testing.T) {
	k := newKitchenFx(t)
	tb := k.table(t)
	bill := k.open(t, k.waiterC, &tb)
	w2 := k.User(t, k.tenantID, k.propID, auth.PermPOSOrder)
	p, _ := auth.Require(w2)
	cur, err := k.svc.EventsStart(w2, k.propID, 0)
	must(t, err)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2)
	for i, c := range []context.Context{k.waiterC, w2} {
		wg.Add(1)
		go func(i int, c context.Context) {
			defer wg.Done()
			<-start
			_, errs[i] = k.svc.AddLine(c, k.propID, bill.ID, pos.AddLineInput{ClientUUID: uid(7000 + i), ProductID: k.satay.ID, Quantity: dec("1")})
		}(i, c)
	}
	close(start)
	wg.Wait()
	for _, e := range errs {
		must(t, e)
	}
	got, err := k.svc.GetBill(w2, k.propID, bill.ID)
	must(t, err)
	if len(got.Lines) != 2 {
		t.Fatalf("both lines: %+v", got.Lines)
	}
	evs, _, err := k.svc.EventsSince(w2, p.TenantID, k.propID, []string{"outlet:" + itoa(k.resto.ID)}, cur)
	must(t, err)
	n := 0
	for _, e := range evs {
		if e.Type == "bill.updated" && e.EntityID == bill.ID {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("the other waiter is told: %+v", evs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
