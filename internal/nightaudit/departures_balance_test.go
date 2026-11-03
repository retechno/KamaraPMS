package nightaudit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomstest"
)

func (f *fx) laundry(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'LAUNDRY'`, f.propID).Scan(&id))
	return id
}

func (f *fx) charge(t *testing.T, folioID int64, key, amount string) {
	t.Helper()
	_, err := f.Folios.PostCharge(f.admin, f.propID, folioID, key, folios.ChargeInput{ChargeCodeID: f.laundry(t), Quantity: "1", UnitPrice: &amount})
	must(t, err)
}

func (f *fx) pay(t *testing.T, folioID int64, key, amount string) {
	t.Helper()
	_, err := f.Folios.PostPayment(f.admin, f.propID, folioID, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
	must(t, err)
}

// departuresWithBalances builds four unfinished departures, one for each way a balance can be:
//
//	owes     the guest folio holds a charge of 10,000                          -> OUTSTANDING
//	credit   the guest folio holds a payment of 50,000                         -> CREDIT
//	settled  the guest folio holds nothing                                     -> SETTLED
//	company  a stay billed to a company (a COMPANY folio besides the guest folio): 30,000 charged to the
//	         company folio, 10,000 paid on the guest folio                     -> OUTSTANDING 20,000, two folios
//
// No stay is routed to a master folio: that case is not built here.
func (f *fx) departuresWithBalances(t *testing.T) (owes, credit, settled, company int64) {
	t.Helper()
	r103 := f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)

	a := f.stay(t, f.r101, "2026-10-01")
	f.charge(t, a.Folio.ID, "c-owes", "10000")

	b := f.stay(t, f.r102, "2026-10-01")
	f.pay(t, b.Folio.ID, "p-credit", "50000")

	c := f.stay(t, f.r201, "2026-10-01")

	var co int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme', 100000000) RETURNING id`, f.tenantID, f.propID).Scan(&co))
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	_, err := f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: co}})
	must(t, err)
	d4, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &r103.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	fs, err := f.Folios.StayFolios(f.admin, f.tenantID, f.propID, d4.Stay.ID)
	must(t, err)
	if len(fs) != 2 || fs[1].FolioType != "COMPANY" {
		t.Fatalf("the company stay has a guest folio and a company folio: %+v", fs)
	}
	f.charge(t, fs[1].ID, "c-company", "30000")
	f.pay(t, fs[0].ID, "p-company", "10000")

	f.advanceWithoutPosting(t) // the audit was skipped: all four are due out
	return a.Stay.ID, b.Stay.ID, c.Stay.ID, d4.Stay.ID
}

func TestUnfinishedDeparturesCarryTheBalanceOfTheInHouseList(t *testing.T) {
	f := setup(t)
	owes, credit, settled, company := f.departuresWithBalances(t)

	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	got := map[int64]folios.StayBalance{}
	for _, dep := range pv.Blockers.UnresolvedDepartures {
		got[dep.StayID] = dep.Balance
	}
	if len(got) != 4 {
		t.Fatalf("departures: %+v", pv.Blockers.UnresolvedDepartures)
	}
	want := map[int64][2]string{owes: {"10000", "OUTSTANDING"}, credit: {"-50000", "CREDIT"}, settled: {"0", "SETTLED"}, company: {"20000", "OUTSTANDING"}}
	for id, w := range want {
		if got[id].Amount != w[0] || got[id].Status != w[1] {
			t.Errorf("stay %d: balance %+v, want %v", id, got[id], w)
		}
	}
	if n := len(got[company].Folios); n != 2 {
		t.Errorf("the company stay lists its two folios: %+v", got[company])
	}

	// the same value, field for field, as the in-house list gives for the same stay (the wire format is compared)
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 4 {
		t.Fatalf("in-house: %d rows", len(rows))
	}
	for _, r := range rows {
		a, err := json.Marshal(r.Balance)
		must(t, err)
		b, err := json.Marshal(got[r.ID])
		must(t, err)
		if string(a) != string(b) {
			t.Errorf("stay %d: in-house %s, night audit %s", r.ID, a, b)
		}
	}

	// the refusal of a run carries the same balances
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour)) // the end of the business day of 1 Oct
	_, err = f.run(t)
	e := code(t, err, "NIGHT_AUDIT_BLOCKED")
	raw, err := json.Marshal(e.Context["blockers"])
	must(t, err)
	var back struct {
		Deps []struct {
			StayID  int64              `json:"stay_id"`
			Balance folios.StayBalance `json:"balance"`
		} `json:"unresolved_departures"`
	}
	must(t, json.Unmarshal(raw, &back))
	if len(back.Deps) != 4 {
		t.Fatalf("refusal: %s", raw)
	}
	for _, dep := range back.Deps {
		if dep.Balance.Status != got[dep.StayID].Status || dep.Balance.Amount != got[dep.StayID].Amount {
			t.Errorf("refusal, stay %d: %+v, preview %+v", dep.StayID, dep.Balance, got[dep.StayID])
		}
	}
}

func TestUnfinishedDepartureWithoutAFolioIsNoFolio(t *testing.T) {
	f := setup(t)
	id := f.Stay(t, f.tenantID, f.propID, f.dlx.ID, f.r101.ID, "2026-09-30", "2026-10-01") // a stay seeded without a folio
	f.advanceWithoutPosting(t)
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if len(pv.Blockers.UnresolvedDepartures) != 1 || pv.Blockers.UnresolvedDepartures[0].StayID != id {
		t.Fatalf("departures: %+v", pv.Blockers.UnresolvedDepartures)
	}
	b := pv.Blockers.UnresolvedDepartures[0].Balance
	if b.Status != "NO_FOLIO" || b.Amount != "" || b.Folios == nil || len(b.Folios) != 0 {
		t.Fatalf("balance: %+v", b)
	}
}

func TestDepartureBalancesOfAnotherTenantNeverAppear(t *testing.T) {
	f := setup(t)
	owes, _, _, _ := f.departuresWithBalances(t)

	other := f.Tenant(t, "OTH")
	op := f.Property(t, other.ID, "UBUD")
	oadmin, _ := f.AdminAccount(t, other.ID)
	ot := f.RoomType(t, oadmin, op.ID, "DLX")
	or := f.Room(t, oadmin, op.ID, ot.ID, "901", housekeeping.Clean)
	foreign := f.Stay(t, other.ID, op.ID, ot.ID, or.ID, "2026-09-30", "2026-10-01")
	must(t, f.Exec(t, `INSERT INTO folios (tenant_id, property_id, folio_number, stay_id, reservation_id) SELECT s.tenant_id, s.property_id, 'F-OTHER', s.id, rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, foreign))

	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	for _, dep := range pv.Blockers.UnresolvedDepartures {
		if dep.StayID == foreign {
			t.Fatalf("a stay of another tenant is listed: %+v", dep)
		}
	}
	// asked for under this tenant and property, a foreign stay has no folio: the folio of another tenant is not read
	var m map[int64]folios.StayBalance
	must(t, f.TxM.WithinReadTx(f.admin, func(ctx context.Context) error {
		var err error
		m, err = f.Folios.StayBalances(ctx, f.tenantID, f.propID, []int64{owes, foreign})
		return err
	}))
	if m[foreign].Status != "NO_FOLIO" || len(m[foreign].Folios) != 0 || m[owes].Status != "OUTSTANDING" {
		t.Fatalf("balances: %+v", m)
	}
}

// The balances of many stays cost the same number of table scans as those of one: two statements read folios and folio_items, whatever the number of stays. The stay by stay
// reading (StayFolios) grows with the stays, which shows that the counter measures something.
func TestBalancesOfManyStaysAreReadInBatch(t *testing.T) {
	f := setup(t)
	owes, credit, settled, company := f.departuresWithBalances(t)
	all := []int64{owes, credit, settled, company}

	// the scans of the two tables made by fn, in its own transaction: the counter of the session is read before and after
	scans := func(fn func(ctx context.Context) error) int {
		var n int
		must(t, f.TxM.WithinReadTx(f.admin, func(ctx context.Context) error {
			tx, err := db.Tx(ctx)
			if err != nil {
				return err
			}
			const read = `SELECT COALESCE(sum(seq_scan + idx_scan), 0)::int FROM pg_stat_xact_user_tables WHERE relname IN ('folios', 'folio_items')`
			var before, after int
			if err := tx.QueryRow(ctx, read).Scan(&before); err != nil {
				return err
			}
			if err := fn(ctx); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, read).Scan(&after); err != nil {
				return err
			}
			n = after - before
			return nil
		}))
		return n
	}
	one := scans(func(ctx context.Context) error {
		_, err := f.Folios.StayBalances(ctx, f.tenantID, f.propID, all[:1])
		return err
	})
	many := scans(func(ctx context.Context) error {
		_, err := f.Folios.StayBalances(ctx, f.tenantID, f.propID, all)
		return err
	})
	perStay := scans(func(ctx context.Context) error {
		for _, id := range all {
			if _, err := f.Folios.StayFolios(ctx, f.tenantID, f.propID, id); err != nil {
				return err
			}
		}
		return nil
	})
	t.Logf("scans of folios and folio_items: 1 stay %d, 4 stays %d, one stay at a time %d", one, many, perStay)
	if many != one {
		t.Errorf("the batch scans %d times for 4 stays and %d for 1", many, one)
	}
	if perStay <= many {
		t.Errorf("the counter does not see the difference: per stay %d, batch %d", perStay, many)
	}
}
