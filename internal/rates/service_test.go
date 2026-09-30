package rates_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

type fixture struct {
	*roomstest.Env
	tenantID    int64
	bali        int64
	admin       context.Context
	dlx, std    int64
	room, exemp int64 // ROOM and ROOM_EXEMPT charge code ids
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin := roomstest.Admin(tn.ID)
	f := fixture{Env: e, tenantID: tn.ID, bali: p.ID, admin: admin,
		dlx: e.RoomType(t, admin, p.ID, "DLX").ID, std: e.RoomType(t, admin, p.ID, "STD").ID}
	f.room, f.exemp = f.codeID(t, p.ID, "ROOM"), f.codeID(t, p.ID, "ROOM_EXEMPT")
	return f
}

func (f fixture) codeID(t *testing.T, propertyID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, propertyID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) plan(t *testing.T, codeName string, roomCode int64) rates.RatePlan {
	t.Helper()
	pl, err := f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{
		Code: codeName, Name: codeName + " plan", MealPlan: "RO", IsRefundable: true, RoomChargeCodeID: roomCode, IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func (f fixture) fill(t *testing.T, planID int64, types []int64, from, to, amount string, weekdays ...string) rates.FillResult {
	t.Helper()
	res, err := f.Rates.FillRates(f.admin, f.bali, rates.FillInput{RatePlanID: planID, RoomTypeIDs: types, From: d(from), To: d(to), Weekdays: weekdays, Amount: amount})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRatePlanRulesAndRoomChargeCode(t *testing.T) {
	f := newFixture(t)
	pl, err := f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{
		Code: " bar ", Name: "Best available", Description: "Flexible", MealPlan: "bb", CancellationPolicy: "Free until 48h", IsRefundable: true,
		RoomChargeCodeID: f.room, IsActive: true,
	})
	if err != nil || pl.Code != "BAR" || pl.MealPlan != "BB" || pl.RoomChargeCode != "ROOM" || pl.PriceMode != "EXCLUSIVE" || !pl.IsActive {
		t.Fatalf("create: %v %+v", err, pl)
	}
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "BAR", Name: "again", MealPlan: "RO", RoomChargeCodeID: f.room})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "bad code", Name: "", MealPlan: "XX", RoomChargeCodeID: 0})
	fields := map[string]bool{}
	for _, fe := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[fe.Field] = true
	}
	for _, name := range []string{"code", "name", "meal_plan", "room_charge_code_id"} {
		if !fields[name] {
			t.Errorf("missing field error %s in %v", name, fields)
		}
	}

	// The room charge code must be an active ROOM code of this property.
	laundry := f.codeID(t, f.bali, "LAUNDRY")
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "LND", Name: "x", MealPlan: "RO", RoomChargeCodeID: laundry, IsActive: true})
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Field != "room_charge_code_id" || fe[0].Code != "CHARGE_CODE_NOT_ROOM" {
		t.Fatalf("non-ROOM code: %v", fe)
	}
	if err := f.Exec(t, `UPDATE charge_codes SET is_active = false WHERE id = $1`, f.exemp); err != nil {
		t.Fatal(err)
	}
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "EX", Name: "x", MealPlan: "RO", RoomChargeCodeID: f.exemp, IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED") // inactive
	if err := f.Exec(t, `UPDATE charge_codes SET is_active = true WHERE id = $1`, f.exemp); err != nil {
		t.Fatal(err)
	}
	jkt := f.Property(t, f.tenantID, "JKT")
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "FOR", Name: "x", MealPlan: "RO", RoomChargeCodeID: f.codeID(t, jkt.ID, "ROOM"), IsActive: true})
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	// The database is the backstop, even for raw SQL.
	if err := f.Exec(t, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'RAW', 'x', $3)`, f.tenantID, f.bali, laundry); err == nil {
		t.Fatal("a rate plan on a non-ROOM code must be rejected by the database")
	}

	// Edits; the code is fixed; deactivation is allowed (existing reservations keep their snapshots).
	name, off := "BAR flexible", false
	upd, err := f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{Name: &name, IsActive: &off, RoomChargeCodeID: &f.exemp})
	if err != nil || upd.Name != name || upd.IsActive || upd.RoomChargeCode != "ROOM_EXEMPT" || upd.Code != "BAR" || upd.Description != "Flexible" {
		t.Fatalf("update: %v %+v", err, upd)
	}
	badMeal := "ZZ"
	_, err = f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{MealPlan: &badMeal})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Rates.UpdateRatePlan(f.admin, f.bali, 999999, rates.RatePlanPatch{Name: &name})
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	list, err := f.Rates.ListRatePlans(f.admin, f.bali, 0, nil, 50)
	if err != nil || len(list) != 1 || list[0].RoomChargeCode != "ROOM_EXEMPT" {
		t.Fatalf("list: %v %+v", err, list)
	}
	active := true
	if l, _ := f.Rates.ListRatePlans(f.admin, f.bali, 0, &active, 50); len(l) != 0 {
		t.Fatalf("active filter: %d", len(l))
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'rate_plan'`); n != 2 {
		t.Fatalf("audit entries: %d", n)
	}
}

// The grid is read in the room charge code's price mode, so a plan with rates cannot swap to a code with
// another mode: the amounts would silently be repriced.
func TestPlanCannotSwitchPriceModeOnceItHasRates(t *testing.T) {
	f := newFixture(t)
	nett, err := f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "ROOM_NETT", Name: "Room nett", ChargeType: "ROOM", PriceMode: "INCLUSIVE", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	pl := f.plan(t, "BAR", f.room)
	// Without rates the plan may move to an inclusive code.
	if u, err := f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{RoomChargeCodeID: &nett.ID}); err != nil || u.PriceMode != "INCLUSIVE" {
		t.Fatalf("no rates yet: %v %+v", err, u)
	}
	f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-01", "2026-10-03", "1110000")
	_, err = f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{RoomChargeCodeID: &f.room})
	c := code(t, err, "RATE_PLAN_PRICE_MODE_MISMATCH")
	if c.Context["rates"] != int64(2) || c.Context["current_price_mode"] != "INCLUSIVE" || c.Context["new_price_mode"] != "EXCLUSIVE" {
		t.Fatalf("context: %v", c.Context)
	}
	// Same mode is fine (both exclusive: ROOM to ROOM_EXEMPT on another plan).
	pl2 := f.plan(t, "NETT", f.room)
	f.fill(t, pl2.ID, []int64{f.dlx}, "2026-10-01", "2026-10-03", "1000000")
	if u, err := f.Rates.UpdateRatePlan(f.admin, f.bali, pl2.ID, rates.RatePlanPatch{RoomChargeCodeID: &f.exemp}); err != nil || u.RoomChargeCode != "ROOM_EXEMPT" {
		t.Fatalf("same price mode: %v %+v", err, u)
	}
}

func TestBulkFillCountsAndWeekdays(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(t, "BAR", f.room)

	// 2 room types x 14 nights.
	res := f.fill(t, pl.ID, []int64{f.dlx, f.std}, "2026-10-01", "2026-10-15", "1000000")
	if res.UpdatedNights != 28 || res.CreatedNights != 28 {
		t.Fatalf("first fill: %+v", res)
	}
	// Overwrite only the Friday and Saturday nights of one type: 4 nights written, none new.
	res = f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-01", "2026-10-15", "1500000", "FRI", "SAT")
	if res.UpdatedNights != 4 || res.CreatedNights != 0 {
		t.Fatalf("weekend overwrite: %+v", res)
	}
	// A wider range: overlapping nights are overwritten, the rest are created.
	res = f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-10", "2026-10-20", "1200000")
	if res.UpdatedNights != 10 || res.CreatedNights != 5 {
		t.Fatalf("extension: %+v", res)
	}
	if n := f.Count(t, `SELECT count(*) FROM rates WHERE rate_plan_id = $1`, pl.ID); n != 28+5 {
		t.Fatalf("grid rows: %d", n)
	}

	grid, err := f.Rates.Rates(f.admin, f.bali, pl.ID, nil, d("2026-10-01"), d("2026-10-04"))
	if err != nil || grid.PriceMode != "EXCLUSIVE" || grid.RoomChargeCode != "ROOM" || len(grid.Rates) != 6 {
		t.Fatalf("grid: %v %+v", err, grid)
	}
	amounts := map[string]string{}
	for _, c := range grid.Rates {
		amounts[fmt.Sprintf("%d/%s", c.RoomTypeID, c.StayDate)] = c.Amount
	}
	// 2026-10-02 (Fri) and 10-03 (Sat) of DLX were raised; 10-01 (Thu) and STD were not.
	if amounts[fmt.Sprintf("%d/2026-10-02", f.dlx)] != "1500000" || amounts[fmt.Sprintf("%d/2026-10-01", f.dlx)] != "1000000" || amounts[fmt.Sprintf("%d/2026-10-02", f.std)] != "1000000" {
		t.Fatalf("amounts: %v", amounts)
	}
	if grid.Rates[0].StayDate != d("2026-10-01") || grid.Rates[0].RoomTypeID > grid.Rates[1].RoomTypeID {
		t.Fatalf("ordered by night then room type: %+v", grid.Rates[:2])
	}
	// Half-open ends and the room type filter.
	one, _ := f.Rates.Rates(f.admin, f.bali, pl.ID, &f.std, d("2026-10-14"), d("2026-10-15"))
	if len(one.Rates) != 1 || one.Rates[0].StayDate != d("2026-10-14") {
		t.Fatalf("single night: %+v", one.Rates)
	}
	empty, err := f.Rates.Rates(f.admin, f.bali, pl.ID, nil, d("2027-01-01"), d("2027-01-10"))
	if err != nil || len(empty.Rates) != 0 || empty.Rates == nil {
		t.Fatalf("empty window must be an empty list: %v %+v", err, empty)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'rates.filled'`); n != 3 {
		t.Fatalf("each fill is audited: %d", n)
	}
}

func TestFillValidationAndAtomicity(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(t, "BAR", f.room)
	base := rates.FillInput{RatePlanID: pl.ID, RoomTypeIDs: []int64{f.dlx}, From: d("2026-10-01"), To: d("2026-10-05"), Amount: "1000000"}
	try := func(edit func(*rates.FillInput)) error {
		in := base
		edit(&in)
		_, err := f.Rates.FillRates(f.admin, f.bali, in)
		return err
	}

	wantCode(t, try(func(in *rates.FillInput) { in.Amount = "-1" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *rates.FillInput) { in.Amount = "1000.50" }), "VALIDATION_FAILED") // IDR has no decimals
	wantCode(t, try(func(in *rates.FillInput) { in.Amount = "" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *rates.FillInput) { in.To = in.From }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *rates.FillInput) { in.To = in.From.AddDays(731) }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *rates.FillInput) { in.Weekdays = []string{"XYZ"} }), "VALIDATION_FAILED")
	if fe := code(t, try(func(in *rates.FillInput) { in.To = d("2026-10-02"); in.Weekdays = []string{"MON"} }), "VALIDATION_FAILED").Fields; fe[0].Code != "NO_NIGHTS" {
		t.Fatalf("no matching nights: %v", fe)
	}
	wantCode(t, try(func(in *rates.FillInput) { in.RatePlanID = 999999 }), "RATE_PLAN_NOT_FOUND")
	wantCode(t, try(func(in *rates.FillInput) { in.RoomTypeIDs = []int64{f.dlx, 999999} }), "ROOM_TYPE_NOT_FOUND")
	// A room type of another property is invisible.
	jkt := f.Property(t, f.tenantID, "JKT")
	foreign := f.RoomType(t, f.admin, jkt.ID, "DLX")
	wantCode(t, try(func(in *rates.FillInput) { in.RoomTypeIDs = []int64{f.dlx, foreign.ID} }), "ROOM_TYPE_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM rates`); n != 0 {
		t.Fatalf("failed requests wrote %d rows (a fill is all or nothing)", n)
	}
	// Rates can be prepared for an inactive plan and for the past, and 730 days is the limit.
	off := false
	if _, err := f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	if res, err := f.Rates.FillRates(f.admin, f.bali, rates.FillInput{RatePlanID: pl.ID, RoomTypeIDs: []int64{f.dlx}, From: d("2026-01-01"), To: d("2026-01-01").AddDays(730), Amount: "900000"}); err != nil || res.UpdatedNights != 730 {
		t.Fatalf("730 nights: %v %+v", err, res)
	}
	// Two-decimal currency, and a three-decimal one is limited to what the column stores.
	usd := f.PropertyIn(t, f.tenantID, "NYC", "USD", 2)
	usdPlan, err := f.Rates.CreateRatePlan(f.admin, usd.ID, rates.RatePlanInput{Code: "BAR", Name: "BAR", MealPlan: "RO", RoomChargeCodeID: f.codeID(t, usd.ID, "ROOM"), IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	usdType := f.RoomType(t, f.admin, usd.ID, "KING")
	if _, err := f.Rates.FillRates(f.admin, usd.ID, rates.FillInput{RatePlanID: usdPlan.ID, RoomTypeIDs: []int64{usdType.ID}, From: d("2026-10-01"), To: d("2026-10-02"), Amount: "150.50"}); err != nil {
		t.Fatal(err)
	}
	g, _ := f.Rates.Rates(f.admin, usd.ID, usdPlan.ID, nil, d("2026-10-01"), d("2026-10-02"))
	if g.Rates[0].Amount != "150.50" {
		t.Fatalf("USD amounts keep their decimals: %v", g.Rates)
	}
	_, err = f.Rates.FillRates(f.admin, usd.ID, rates.FillInput{RatePlanID: usdPlan.ID, RoomTypeIDs: []int64{usdType.ID}, From: d("2026-10-01"), To: d("2026-10-02"), Amount: "150.505"})
	wantCode(t, err, "VALIDATION_FAILED")
	// The CHECK constraint is the backstop for negative amounts.
	if err := f.Exec(t, `UPDATE rates SET amount = -1 WHERE rate_plan_id = $1`, usdPlan.ID); err == nil {
		t.Fatal("a negative rate must be rejected by the database")
	}
}

func TestNightlyPriceLookup(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(t, "BAR", f.room)
	f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-01", "2026-10-04", "1000000")
	f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-02", "2026-10-03", "1300000")
	ctx := context.Background()

	got, err := f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-01"), d("2026-10-04"))
	if err != nil || got.PriceMode != "EXCLUSIVE" || got.RoomChargeCode != "ROOM" || got.RoomChargeCodeID != f.room || len(got.Nights) != 3 {
		t.Fatalf("lookup: %v %+v", err, got)
	}
	want := []string{"1000000", "1300000", "1000000"}
	for i, n := range got.Nights {
		if n.Date != d("2026-10-01").AddDays(i) || !n.Amount.Equal(decimal.RequireFromString(want[i])) {
			t.Fatalf("night %d: %+v", i, n)
		}
	}
	// The departure night is not priced.
	if one, err := f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-03"), d("2026-10-04")); err != nil || len(one.Nights) != 1 {
		t.Fatalf("one night: %v", err)
	}

	// Missing nights are reported, never guessed.
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-03"), d("2026-10-08"))
	c := code(t, err, "RATE_NOT_SET")
	nights, _ := c.Context["nights"].([]string)
	if len(nights) != 4 || nights[0] != "2026-10-04" || c.Context["missing_nights"] != 4 {
		t.Fatalf("missing nights: %v", c.Context)
	}
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.std, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "RATE_NOT_SET") // another room type has its own grid

	off := false
	if _, err := f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "RATE_PLAN_INACTIVE")
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, 999999, f.dlx, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-02"), d("2026-10-02"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Rates.NightlyPrices(ctx, f.tenantID, f.bali, pl.ID, f.dlx, d("2026-10-01"), d("2026-10-01").AddDays(366))
	wantCode(t, err, "VALIDATION_FAILED")
	xyz := f.Tenant(t, "XYZ")
	_, err = f.Rates.NightlyPrices(ctx, xyz.ID, f.bali, pl.ID, f.dlx, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "RATE_PLAN_NOT_FOUND") // scoped by tenant
}

func TestPermissionsAndIsolation(t *testing.T) {
	f := newFixture(t)
	jkt := f.Property(t, f.tenantID, "JKT")
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	pl := f.plan(t, "BAR", f.room)
	f.fill(t, pl.ID, []int64{f.dlx}, "2026-10-01", "2026-10-03", "1000000")

	reader := f.User(t, f.tenantID, f.bali)
	manager := f.User(t, f.tenantID, f.bali, auth.PermRateManage)
	in := rates.FillInput{RatePlanID: pl.ID, RoomTypeIDs: []int64{f.dlx}, From: d("2026-10-01"), To: d("2026-10-02"), Amount: "1"}
	planIn := rates.RatePlanInput{Code: "NEW", Name: "New", MealPlan: "RO", RoomChargeCodeID: f.room, IsActive: true}

	if _, err := f.Rates.ListRatePlans(reader, f.bali, 0, nil, 10); err != nil {
		t.Fatalf("read needs property access only: %v", err)
	}
	if _, err := f.Rates.Rates(reader, f.bali, pl.ID, nil, d("2026-10-01"), d("2026-10-03")); err != nil {
		t.Fatal(err)
	}
	_, err := f.Rates.FillRates(reader, f.bali, in)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Rates.CreateRatePlan(reader, f.bali, planIn)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Rates.UpdateRatePlan(reader, f.bali, pl.ID, rates.RatePlanPatch{Name: new(string)})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.Rates.FillRates(manager, f.bali, in); err != nil {
		t.Fatalf("rate.manage: %v", err)
	}
	if n := f.Count(t, `SELECT amount::int FROM rates WHERE rate_plan_id = $1 AND stay_date = '2026-10-01'`, pl.ID); n != 1 {
		t.Fatalf("the manager's fill was not applied: %d", n)
	}
	// No grant at a property, and other tenants' properties: 404.
	for _, pid := range []int64{jkt.ID, sg.ID} {
		_, err = f.Rates.ListRatePlans(manager, pid, 0, nil, 10)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
		_, err = f.Rates.FillRates(manager, pid, in)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
	}
	_, err = f.Rates.Rates(f.admin, sg.ID, pl.ID, nil, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// A plan of another property never resolves through an accessible one.
	other := f.PropertyIn(t, f.tenantID, "SBY", "IDR", 0)
	otherPlan, err := f.Rates.CreateRatePlan(f.admin, other.ID, rates.RatePlanInput{Code: "BAR", Name: "BAR", MealPlan: "RO", RoomChargeCodeID: f.codeID(t, other.ID, "ROOM"), IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Rates.Rates(f.admin, f.bali, otherPlan.ID, nil, d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	in.RatePlanID = otherPlan.ID
	_, err = f.Rates.FillRates(f.admin, f.bali, in)
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	_, err = f.Rates.Rates(f.admin, f.bali, pl.ID, new(int64), d("2026-10-01"), d("2026-10-02"))
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
	_, err = f.Rates.ListRatePlans(context.Background(), f.bali, 0, nil, 10)
	wantCode(t, err, "UNAUTHENTICATED")
}

// Concurrent fills of the same nights never fail or leave a mixed state: the last writer wins per night.
func TestConcurrentFillsOfTheSameNights(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(t, "BAR", f.room)
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = f.Rates.FillRates(f.admin, f.bali, rates.FillInput{
				RatePlanID: pl.ID, RoomTypeIDs: []int64{f.dlx, f.std}, From: d("2026-10-01"), To: d("2026-11-01"), Amount: fmt.Sprint(1000000 + i),
			})
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c := f.Count(t, `SELECT count(*) FROM rates WHERE rate_plan_id = $1`, pl.ID); c != 62 {
		t.Fatalf("rows: %d, want 2 types x 31 nights", c)
	}
}

// A fill and a switch of the plan's price mode at the same moment: the switch either waits for the fill and
// is then refused, or wins before any rate exists. Rates are never left under a mode they were not set for.
func TestFillVersusPriceModeSwitchRace(t *testing.T) {
	f := newFixture(t)
	nett, err := f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "ROOM_NETT", Name: "Room nett", ChargeType: "ROOM", PriceMode: "INCLUSIVE", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	for round := range 6 {
		pl := f.plan(t, fmt.Sprintf("P%d", round), f.room)
		var fillErr, switchErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, fillErr = f.Rates.FillRates(f.admin, f.bali, rates.FillInput{RatePlanID: pl.ID, RoomTypeIDs: []int64{f.dlx}, From: d("2026-10-01"), To: d("2026-10-10"), Amount: "1000000"})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, switchErr = f.Rates.UpdateRatePlan(f.admin, f.bali, pl.ID, rates.RatePlanPatch{RoomChargeCodeID: &nett.ID})
		}()
		close(start)
		wg.Wait()
		if fillErr != nil {
			t.Fatalf("round %d: the fill must always succeed: %v", round, fillErr)
		}
		if switchErr != nil && !apperr.IsCode(switchErr, "RATE_PLAN_PRICE_MODE_MISMATCH") {
			t.Fatalf("round %d: unexpected switch error: %v", round, switchErr)
		}
		if f.Count(t, `SELECT count(*) FROM rates WHERE rate_plan_id = $1`, pl.ID) != 9 {
			t.Fatalf("round %d: the fill was lost", round)
		}
	}
}
