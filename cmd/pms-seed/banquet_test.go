package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

func TestSeedBanquet(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	svc := banquet.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, nil, nil, nil)

	deps := departments.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy)
	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedBanquetWith(ctx, svc, deps, e.Billing, p.ID, dry, &out)
		if err != nil {
			t.Fatal(err)
		}
		return n, out.String()
	}
	standardDepartments(t, e, tn.ID, p.ID)

	n, out := run(true)
	if n == 0 || !strings.Contains(out, "would create") {
		t.Fatalf("dry run: %d %q", n, out)
	}
	if rooms, _ := svc.ListRooms(ctx, p.ID, nil); len(rooms) != 0 {
		t.Fatalf("a dry run created %d rooms", len(rooms))
	}

	created, _ := run(false)
	if created != n {
		t.Fatalf("created %d, the dry run said %d", created, n)
	}
	rooms, err := svc.ListRooms(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != len(bqRooms) {
		t.Fatalf("%d rooms, want %d", len(rooms), len(bqRooms))
	}
	for _, r := range rooms {
		if r.Code == "BALL" && (!r.IsCombo || len(r.Parts) != 2 || len(r.Setups) == 0) {
			t.Fatalf("the ballroom is not made of the two halls: %+v", r)
		}
	}
	pk, err := svc.ListPackages(ctx, p.ID, nil)
	if err != nil || len(pk) != 1 || pk[0].Name != "Fullday Meeting" || len(pk[0].Components) != 4 || !pk[0].PricePerPax.Equal(decimal.RequireFromString("363000")) {
		t.Fatalf("package: %+v %v", pk, err)
	}

	tpls, err := svc.ListPolicyTemplates(ctx, p.ID, nil)
	if err != nil || len(tpls) != 1 || !tpls[0].IsDefault || len(tpls[0].Deposits) != 3 || len(tpls[0].Tiers) != 4 {
		t.Fatalf("policy template: %+v %v", tpls, err)
	}

	// the sections of the BEO are tied to the standard departments
	rec, err := svc.ListRecipients(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rec {
		if r.DepartmentID == nil || r.DepartmentCode == nil || *r.DepartmentCode != bqSectionDepartment[r.Section] {
			t.Fatalf("BEO recipient %+v", r)
		}
	}

	again, out := run(false)
	if again != 0 || !strings.Contains(out, "created 0 banquet rows") {
		t.Fatalf("a second run created %d: %s", again, out)
	}

	// the items belong to the charge codes of the banquet by code, and those carry the service charge and the tax of the demo; BQT_FORFEIT carries none
	items, err := svc.ListItems(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := map[string]banquet.Item{}
	for _, it := range items {
		item[it.Code] = it
	}
	for code, want := range map[string]string{"LUNCH": "BQT_FB", "COFFEEBRK": "BQT_FB", "ROOMPAX": "BQT_ROOM", "EQUIPPAX": "BQT_EQUIPMENT", "FLOWERS": "BQT_DECOR", "PROJECTOR": "BQT_EQUIPMENT"} {
		if item[code].ChargeCode != want {
			t.Fatalf("item %s has the charge code %s, want %s", code, item[code].ChargeCode, want)
		}
	}
	codes, err := e.Billing.ListChargeCodes(ctx, p.ID, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]billingconfig.ChargeCode{}
	for _, c := range codes {
		byCode[c.Code] = c
	}
	for _, code := range bqRuleCodes {
		if c := byCode[code]; len(c.ServiceCharges) != 1 || c.ServiceCharges[0].Code != "SVC" || len(c.Taxes) != 1 || c.Taxes[0].Code != "PB1" {
			t.Fatalf("%s rules: %+v %+v", code, c.ServiceCharges, c.Taxes)
		}
	}
	if c := byCode["BQT_FORFEIT"]; len(c.ServiceCharges) != 0 || len(c.Taxes) != 0 {
		t.Fatalf("BQT_FORFEIT has rules: %+v %+v", c.ServiceCharges, c.Taxes)
	}

	// a property seeded by the first version: the lunch still has the first food and beverage code and the old price, the sound system was moved by a person, the package is the old one
	var legacyFB int64
	for _, c := range codes {
		if legacyFB == 0 && c.IsActive && c.ChargeType == billingconfig.TypeFoodBeverage {
			legacyFB = c.ID
		}
	}
	active := true
	lunch := item["LUNCH"]
	if _, err := svc.UpdateItem(ctx, p.ID, lunch.ID, banquet.ItemInput{Code: lunch.Code, Name: lunch.Name, Kind: lunch.Kind, UnitBasis: lunch.UnitBasis, ChargeCodeID: legacyFB, ListPrice: decimal.RequireFromString("135000"), FbClass: lunch.FbClass, IsActive: &active}); err != nil {
		t.Fatal(err)
	}
	sound := item["SOUND"]
	if _, err := svc.UpdateItem(ctx, p.ID, sound.ID, banquet.ItemInput{Code: sound.Code, Name: sound.Name, Kind: sound.Kind, UnitBasis: sound.UnitBasis, ChargeCodeID: byCode["BQT_DECOR"].ID, ListPrice: decimal.RequireFromString(sound.ListPrice.String()), IsActive: &active}); err != nil {
		ae, _ := apperr.As(err)
		t.Fatalf("%v %+v", err, ae.Fields)
	}
	oldPk, _ := svc.ListPackages(ctx, p.ID, nil)
	if _, err := svc.UpdatePackage(ctx, p.ID, oldPk[0].ID, banquet.PackageInput{Code: "FULLDAY", Name: "Fullday Meeting", PricePerPax: decimal.RequireFromString("285000"), IsActive: &active, Components: []banquet.ComponentInput{
		{ItemID: item["COFFEEBRK"].ID, QtyPerPax: decimal.RequireFromString("2")}, {ItemID: item["LUNCH"].ID, QtyPerPax: decimal.RequireFromString("1")}, {ItemID: item["SOFTDRINK"].ID, QtyPerPax: decimal.RequireFromString("1")}}}); err != nil {
		t.Fatal(err)
	}
	fixes, out := run(true)
	if fixes != 2 || !strings.Contains(out, "item LUNCH exists: to fix") || !strings.Contains(out, "item SOUND exists: warning, charge code BQT_DECOR kept") {
		t.Fatalf("dry run of the repair: %d %s", fixes, out)
	}
	if n, _ := run(false); n != 2 {
		t.Fatalf("the repair changed %d rows, want 2", n)
	}
	if pk, _ := svc.ListPackages(ctx, p.ID, nil); len(pk) != 1 || len(pk[0].Components) != 4 || !pk[0].PricePerPax.Equal(decimal.RequireFromString("363000")) {
		t.Fatalf("the package was not repaired: %+v", pk)
	}
	items, _ = svc.ListItems(ctx, p.ID, nil)
	for _, it := range items {
		switch it.Code {
		case "LUNCH":
			if it.ChargeCode != "BQT_FB" || !it.ListPrice.Equal(decimal.RequireFromString("150000")) {
				t.Fatalf("the lunch was not repaired: %+v", it)
			}
		case "SOUND":
			if it.ChargeCode != "BQT_DECOR" {
				t.Fatalf("an item a person changed was overwritten: %+v", it)
			}
		}
	}
	if n, _ := run(false); n != 0 {
		t.Fatalf("the repair is not idempotent: %d", n)
	}
}

func TestSeedBanquetBeoFillsTheDefiniteEventsThatHaveNone(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	svc := banquet.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Folios, e.Billing, e.IAM)
	deps := departments.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy)
	run := func(dry bool) (int, string) {
		var out bytes.Buffer
		n, err := seedBanquetWith(ctx, svc, deps, e.Billing, p.ID, dry, &out)
		if err != nil {
			t.Fatal(err)
		}
		return n, out.String()
	}
	standardDepartments(t, e, tn.ID, p.ID)
	run(false)

	rooms, err := svc.ListRooms(ctx, p.ID, nil)
	if err != nil || len(rooms) == 0 {
		t.Fatalf("rooms: %v %v", rooms, err)
	}
	sales, err := auth.Require(e.User(t, tn.ID, p.ID, auth.PermBanquetManage))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := svc.CreateEvent(ctx, p.ID, banquet.EventInput{Name: "Gala", BookerGuestID: guestOf(t, e, tn.ID, p.ID), SalesUserID: &sales.UserID})
	if err != nil {
		t.Fatal(err)
	}
	start, end := civil.MustParseTimeOfDay("12:00"), civil.MustParseTimeOfDay("16:00")
	if _, err = svc.AddSession(ctx, p.ID, ev.ID, banquet.SessionInput{Name: "Dinner", SessionDate: civil.MustParseDate("2026-11-07"), StartTime: &start, EndTime: &end, RoomID: &rooms[0].ID, PaxEstimated: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.MakeTentative(ctx, p.ID, ev.ID, banquet.TentativeInput{OptionUntil: civil.MustParseDate("2026-10-20")}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Confirm(ctx, p.ID, ev.ID, banquet.ConfirmInput{}); err != nil {
		t.Fatal(err)
	}

	n, out := run(true)
	if n != 1 || !strings.Contains(out, "session 1: to create") {
		t.Fatalf("the dry run counts the session: %d %s", n, out)
	}
	if v, _ := svc.Beo(ctx, p.ID, ev.ID); v.Revision != 1 {
		t.Fatalf("a dry run saved a BEO: revision %d", v.Revision)
	}
	if n, _ = run(false); n != 1 {
		t.Fatalf("created %d, the dry run said 1", n)
	}
	v, err := svc.Beo(ctx, p.ID, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Revision != 2 || len(v.Sessions[0].RunOfShow) != 1 {
		t.Fatalf("the sample BEO is one revision: %+v", v.Revision)
	}
	for _, x := range v.Sessions[0].Sections {
		if strings.TrimSpace(x.Body) == "" {
			t.Fatalf("section %s has no text", x.Section)
		}
	}
	if n, _ = run(false); n != 0 {
		t.Fatalf("the seed is not idempotent: %d", n)
	}
	if v2, _ := svc.Beo(ctx, p.ID, ev.ID); v2.Revision != 2 {
		t.Fatalf("a second run made a revision: %d", v2.Revision)
	}
}

// standardDepartments are the departments every property has once it is set up (a property made by the tests starts without them).
func standardDepartments(t *testing.T, e *roomstest.Env, tenantID, propertyID int64) {
	t.Helper()
	for _, code := range []string{"ROOMS", "FB", "POM", "AG"} {
		if _, err := e.Pool.Exec(context.Background(), `INSERT INTO departments (tenant_id, property_id, code, name) VALUES ($1, $2, $3, $3) ON CONFLICT DO NOTHING`, tenantID, propertyID, code); err != nil {
			t.Fatal(err)
		}
	}
}

func guestOf(t *testing.T, e *roomstest.Env, tenantID, propertyID int64) *int64 {
	t.Helper()
	var id int64
	if err := e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Wijaya', $2) RETURNING id`, tenantID, propertyID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return &id
}
