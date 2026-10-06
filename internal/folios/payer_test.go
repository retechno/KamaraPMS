package folios_test

import (
	"context"
	"strings"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/migrate"
)

// stayWithFolios: a checked-in stay with its guest folio (attached the way the check-in does it) and a company.
type payerFx struct {
	*fx
	stay, guestFolio, company int64
	principal                 auth.Principal
}

func setupPayer(t *testing.T) *payerFx {
	t.Helper()
	f := setup(t)
	p, err := auth.Require(f.admin)
	must(t, err)
	var typeID, roomID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT room_type_id, id FROM rooms WHERE property_id = $1 AND room_number = '101'`, f.propID).Scan(&typeID, &roomID))
	stay := f.Stay(t, f.tenantID, f.propID, typeID, roomID, "2026-10-01", "2026-10-03")
	// the reservation of the stay's line (the folio fixture has a reservation of its own)
	must(t, f.Pool.QueryRow(context.Background(), `SELECT rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&f.reservation))
	px := &payerFx{fx: f, stay: stay, principal: p}
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		sf, err := f.Folios.AttachStayFolio(ctx, p, f.propID, f.reservation, stay, 0)
		px.guestFolio = sf.ID
		return err
	}))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme Corp', 1000000) RETURNING id`, f.tenantID, f.propID).Scan(&px.company))
	return px
}

func (p *payerFx) companyFolio(t *testing.T, number string) int64 {
	t.Helper()
	var id int64
	must(t, p.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES ($1, $2, $3, $4, $5, 'COMPANY', $6) RETURNING id`,
		p.tenantID, p.propID, number, p.reservation, p.stay, p.company).Scan(&id))
	return id
}

func TestResolveTargetIsTheGuestFolio(t *testing.T) {
	p := setupPayer(t)
	got, err := p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, p.stay, folios.RoomRoute)
	must(t, err)
	if got.FolioID != p.guestFolio || got.Routed {
		t.Fatalf("target %+v, want guest folio %d", got, p.guestFolio)
	}
	// a company folio does not change the default routing
	p.companyFolio(t, "FC1")
	got, err = p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, p.stay, folios.Route{ChargeCodeID: p.minibar})
	must(t, err)
	if got.FolioID != p.guestFolio {
		t.Fatalf("with a company folio: target %+v, want %d", got, p.guestFolio)
	}
	m, err := p.Folios.ResolveRoomTargets(p.admin, p.tenantID, p.propID, []int64{p.stay, 999999})
	must(t, err)
	if len(m) != 2 || m[p.stay].Default.FolioID != p.guestFolio || m[999999].Default.FolioID != 0 {
		t.Fatalf("batch: %v", m)
	}
}

func TestResolveTargetWithoutAnOpenFolio(t *testing.T) {
	p := setupPayer(t)
	must(t, p.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, p.guestFolio))
	got, err := p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, p.stay, folios.RoomRoute)
	must(t, err)
	if got.FolioID != 0 {
		t.Fatal("a closed guest folio must not be a target")
	}
}

func TestStayHasOneFolioPerPayer(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	list, err := p.Folios.StayFolios(p.admin, p.tenantID, p.propID, p.stay)
	must(t, err)
	if len(list) != 2 || list[0].ID != p.guestFolio || list[0].FolioType != "GUEST" || list[0].BillToCompanyID != nil ||
		list[1].ID != cf || list[1].FolioType != "COMPANY" || list[1].BillToCompanyID == nil || *list[1].BillToCompanyID != p.company {
		t.Fatalf("stay folios: %+v", list)
	}
	fo, err := p.Folios.GetFolio(p.admin, p.propID, cf)
	must(t, err)
	if fo.BillToCompanyName != "Acme Corp" || fo.BillToCompanyID == nil || *fo.BillToCompanyID != p.company {
		t.Fatalf("folio: %+v", fo)
	}
	// the same payer twice is refused with a stable code
	err = p.Exec(t, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES ($1, $2, 'FC2', $3, $4, 'COMPANY', $5)`,
		p.tenantID, p.propID, p.reservation, p.stay, p.company)
	if err == nil || !strings.Contains(err.Error(), "folios_stay_payer_uk") {
		t.Fatalf("second folio of the same company: %v", err)
	}
}

func TestCheckOutNeedsEveryFolioOfTheStayBalanced(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	_, err := p.Folios.PostCharge(p.admin, p.propID, cf, "k1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	err = p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		_, err := p.Folios.CloseStayFolios(ctx, p.principal, p.propID, p.stay)
		return err
	})
	e := code(t, err, "FOLIO_NOT_BALANCED")
	if !strings.Contains(e.Error()+strings.Join(contextFolioNumbers(e.Context), ","), "FC1") {
		t.Fatalf("the unbalanced company folio is not named: %v", e.Context)
	}
	if n := p.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND status = 'OPEN'`, p.stay); n != 2 {
		t.Fatalf("nothing may be closed, open folios: %d", n)
	}
}

func TestDetachRefusedWhenACompanyFolioHoldsACharge(t *testing.T) {
	p := setupPayer(t)
	cf := p.companyFolio(t, "FC1")
	_, err := p.Folios.PostCharge(p.admin, p.propID, cf, "k1", folios.ChargeInput{ChargeCodeID: p.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	err = p.TxM.WithinTx(p.admin, func(ctx context.Context) error {
		_, err := p.Folios.DetachStayFolio(ctx, p.principal, p.propID, p.stay)
		return err
	})
	wantCode(t, err, "CHECK_IN_HAS_CHARGES")
}

// The migration over data: folios that exist before 00060 stay guest folios with no payer and still resolve; the way down is refused
// while a company folio exists and works once it is gone.
func TestMigration00060OverData(t *testing.T) {
	p := setupPayer(t)
	ctx := context.Background()
	defer func() {
		if _, err := migrate.Up(ctx, p.Pool); err != nil {
			t.Fatalf("restoring the schema: %v", err)
		}
	}()

	if _, err := migrate.DownTo(ctx, p.Pool, 59); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(ctx, p.Pool); err != nil {
		t.Fatal(err)
	}
	var typ string
	var payer *int64
	must(t, p.Pool.QueryRow(ctx, `SELECT folio_type, bill_to_company_id FROM folios WHERE id = $1`, p.guestFolio).Scan(&typ, &payer))
	if typ != "GUEST" || payer != nil {
		t.Fatalf("existing folio after the round trip: %s %v", typ, payer)
	}
	got, err := p.Folios.ResolveTarget(p.admin, p.tenantID, p.propID, p.stay, folios.RoomRoute)
	must(t, err)
	if got.FolioID != p.guestFolio {
		t.Fatalf("resolve after the round trip: %+v", got)
	}

	cf := p.companyFolio(t, "FC1")
	if _, err := migrate.DownTo(ctx, p.Pool, 59); err == nil || !strings.Contains(err.Error(), "COMPANY folios exist") {
		t.Fatalf("the way down must be refused with a company folio: %v", err)
	}
	if v, err := migrate.Version(ctx, p.Pool); err != nil || v < 60 {
		t.Fatalf("version after the refused way down: %d %v", v, err)
	}
	must(t, p.Exec(t, `DELETE FROM folios WHERE id = $1`, cf))
	if _, err := migrate.DownTo(ctx, p.Pool, 59); err != nil {
		t.Fatalf("down without a company folio: %v", err)
	}
}

func contextFolioNumbers(c map[string]any) []string {
	var out []string
	if l, ok := c["folios"].([]map[string]any); ok {
		for _, m := range l {
			if n, ok := m["folio_number"].(string); ok {
				out = append(out, n)
			}
		}
	}
	return out
}
