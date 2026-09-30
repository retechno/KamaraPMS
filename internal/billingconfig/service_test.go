package billingconfig_test

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
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

type fixture struct {
	*roomstest.Env
	tenantID int64
	bali     int64
	admin    context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI") // IDR, 0 decimals
	return fixture{Env: e, tenantID: tn.ID, bali: p.ID, admin: roomstest.Admin(tn.ID)}
}

func (f fixture) tax(t *testing.T, codeName, rate string, onService bool) billingconfig.Tax {
	t.Helper()
	tx, err := f.Billing.CreateTax(f.admin, f.bali, billingconfig.TaxInput{Code: codeName, Name: codeName, Rate: rate, TaxOnService: onService, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func (f fixture) svc(t *testing.T, codeName, rate string) billingconfig.ServiceCharge {
	t.Helper()
	sc, err := f.Billing.CreateServiceCharge(f.admin, f.bali, billingconfig.ServiceChargeInput{Code: codeName, Name: codeName, Rate: rate, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func (f fixture) codeByName(t *testing.T, name string) billingconfig.ChargeCode {
	t.Helper()
	list, err := f.Billing.ListChargeCodes(f.admin, f.bali, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.Code == name {
			return c
		}
	}
	t.Fatalf("no charge code %s", name)
	return billingconfig.ChargeCode{}
}

func TestNewPropertiesGetTheStandardChargeCodes(t *testing.T) {
	f := newFixture(t)
	list, err := f.Billing.ListChargeCodes(f.admin, f.bali, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil || len(list) != 10 {
		t.Fatalf("seeded codes: %v %d", err, len(list))
	}
	types := map[string]string{}
	for _, c := range list {
		if !c.IsSystem || !c.IsActive || c.PriceMode != "EXCLUSIVE" || len(c.Taxes) != 0 || len(c.ServiceCharges) != 0 {
			t.Errorf("%s must be an active, exclusive system code without rules: %+v", c.Code, c)
		}
		types[c.Code] = c.ChargeType
	}
	for c, want := range map[string]string{"ROOM": "ROOM", "ROOM_EXEMPT": "ROOM", "BREAKFAST": "FOOD_BEVERAGE", "NO_SHOW_FEE": "FEE", "LAUNDRY": "SERVICE", "OTHER": "OTHER"} {
		if types[c] != want {
			t.Errorf("%s has type %q, want %q", c, types[c], want)
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'charge_codes.seeded' AND property_id = $1`, f.bali); n != 1 {
		t.Fatalf("seed audit entries: %d", n)
	}
	// A second property gets its own codes; nothing is shared.
	jkt := f.Property(t, f.tenantID, "JKT")
	if n := f.Count(t, `SELECT count(*) FROM charge_codes WHERE property_id = $1`, jkt.ID); n != 10 {
		t.Fatalf("second property: %d", n)
	}
}

func TestTaxesAndServiceChargesValidationAndRules(t *testing.T) {
	f := newFixture(t)
	vat := f.tax(t, " vat ", "11", true)
	if vat.Code != "VAT" || vat.Rate != "11.0000" || !vat.TaxOnService {
		t.Fatalf("create: %+v", vat)
	}
	_, err := f.Billing.CreateTax(f.admin, f.bali, billingconfig.TaxInput{Code: "VAT", Name: "again", Rate: "1", IsActive: true})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.Billing.CreateTax(f.admin, f.bali, billingconfig.TaxInput{Code: "bad code", Name: "", Rate: "150", IsActive: true})
	fields := map[string]bool{}
	for _, fe := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[fe.Field] = true
	}
	if !fields["code"] || !fields["name"] || !fields["rate"] {
		t.Fatalf("fields: %v", fields)
	}
	if _, err := f.Billing.CreateServiceCharge(f.admin, f.bali, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "100.0001"}); err == nil {
		t.Fatal("rate above 100")
	}
	svc := f.svc(t, "SVC", "10")

	// Rate edits are reported with the open stays they reach (none yet).
	rate := "12.5"
	upd, err := f.Billing.UpdateTax(f.admin, f.bali, vat.ID, billingconfig.TaxPatch{Rate: &rate})
	if err != nil || upd.Rate != "12.5000" || upd.AffectedOpenStays == nil || *upd.AffectedOpenStays != 0 {
		t.Fatalf("rate change: %v %+v", err, upd)
	}
	name := "VAT 12.5%"
	upd, err = f.Billing.UpdateTax(f.admin, f.bali, vat.ID, billingconfig.TaxPatch{Name: &name})
	if err != nil || upd.AffectedOpenStays != nil {
		t.Fatalf("name change has no stay impact: %v %+v", err, upd)
	}

	// Map them, then deactivation is refused while mapped.
	room := f.codeByName(t, "ROOM")
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	off := false
	_, err = f.Billing.UpdateTax(f.admin, f.bali, vat.ID, billingconfig.TaxPatch{IsActive: &off})
	if c := code(t, err, "TAX_IN_USE"); c.Context["charge_code_rules"] != int64(1) {
		t.Fatalf("context: %v", c.Context)
	}
	_, err = f.Billing.UpdateServiceCharge(f.admin, f.bali, svc.ID, billingconfig.ServiceChargePatch{IsActive: &off})
	wantCode(t, err, "SERVICE_CHARGE_IN_USE")
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{}); err != nil {
		t.Fatal(err)
	}
	if u, err := f.Billing.UpdateTax(f.admin, f.bali, vat.ID, billingconfig.TaxPatch{IsActive: &off}); err != nil || u.IsActive {
		t.Fatalf("deactivate unmapped tax: %v %+v", err, u)
	}
	if u, err := f.Billing.UpdateServiceCharge(f.admin, f.bali, svc.ID, billingconfig.ServiceChargePatch{IsActive: &off}); err != nil || u.IsActive {
		t.Fatalf("deactivate unmapped service charge: %v %+v", err, u)
	}
	_, err = f.Billing.UpdateTax(f.admin, f.bali, 999999, billingconfig.TaxPatch{Name: &name})
	wantCode(t, err, "TAX_NOT_FOUND")

	active := true
	taxes, err := f.Billing.ListTaxes(f.admin, f.bali, 0, &active, 50)
	if err != nil || len(taxes) != 0 {
		t.Fatalf("active filter: %v %d", err, len(taxes))
	}
	if all, _ := f.Billing.ListTaxes(f.admin, f.bali, 0, nil, 50); len(all) != 1 {
		t.Fatalf("all: %d", len(all))
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'tax'`); n != 4 {
		t.Fatalf("tax audit entries: %d", n) // created, rate change, rename, deactivation
	}
}

func TestChargeCodeRulesRules(t *testing.T) {
	f := newFixture(t)
	vat := f.tax(t, "VAT", "11", true)
	city := f.tax(t, "CITY", "1", false)
	svc := f.svc(t, "SVC", "10")
	room := f.codeByName(t, "ROOM")

	rules := billingconfig.RulesInput{
		Taxes:          []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}, {TaxID: city.ID, Sequence: 2}},
		ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	}
	got, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, rules)
	if err != nil || len(got.Taxes) != 2 || got.Taxes[0].Code != "VAT" || got.Taxes[1].Code != "CITY" || !got.Taxes[0].TaxOnService ||
		len(got.ServiceCharges) != 1 || got.ServiceCharges[0].Rate != "10.0000" {
		t.Fatalf("replace: %v %+v", err, got)
	}

	// Reordering (swap) never collides with the unique (code, sequence) index.
	rules.Taxes = []billingconfig.TaxRuleInput{{TaxID: city.ID, Sequence: 1}, {TaxID: vat.ID, Sequence: 2}}
	got, err = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, rules)
	if err != nil || got.Taxes[0].Code != "CITY" || got.Taxes[1].Code != "VAT" {
		t.Fatalf("swap: %v %+v", err, got.Taxes)
	}
	// Gaps are fine; unlisted rules are deactivated but kept as history.
	rules.Taxes = []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 7}}
	rules.ServiceCharges = nil
	got, err = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, rules)
	if err != nil || len(got.Taxes) != 1 || got.Taxes[0].Sequence != 7 || len(got.ServiceCharges) != 0 {
		t.Fatalf("shrink: %v %+v", err, got)
	}
	if n := f.Count(t, `SELECT count(*) FROM charge_code_taxes WHERE charge_code_id = $1`, room.ID); n != 2 {
		t.Fatalf("history rows: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM charge_code_taxes WHERE charge_code_id = $1 AND is_active`, room.ID); n != 1 {
		t.Fatalf("active rows: %d", n)
	}
	// Re-adding a removed rule reuses its row.
	rules.Taxes = []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}, {TaxID: city.ID, Sequence: 2}}
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, rules); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM charge_code_taxes WHERE charge_code_id = $1`, room.ID); n != 2 {
		t.Fatalf("rows after re-adding: %d", n)
	}

	// Validation: sequences, duplicates, inactive and foreign references.
	_, err = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}, {TaxID: city.ID, Sequence: 1}}})
	wantCode(t, err, "VALIDATION_FAILED")
	off := false
	if _, err := f.Billing.UpdateTax(f.admin, f.bali, city.ID, billingconfig.TaxPatch{IsActive: &off}); err == nil {
		t.Fatal("city tax is still mapped")
	}
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Billing.UpdateTax(f.admin, f.bali, city.ID, billingconfig.TaxPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: city.ID, Sequence: 1}}})
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Code != "TAX_INACTIVE" || fe[0].Field != "taxes[0].tax_id" {
		t.Fatalf("inactive tax: %v", fe)
	}
	jkt := f.Property(t, f.tenantID, "JKT")
	foreignTax, err := f.Billing.CreateTax(f.admin, jkt.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: foreignTax.ID, Sequence: 1}}})
	wantCode(t, err, "TAX_NOT_FOUND") // another property's tax is invisible
	_, err = f.Billing.ReplaceRules(f.admin, f.bali, 999999, rules2())
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM charge_code_taxes WHERE tax_id = $1`, foreignTax.ID); n != 0 {
		t.Fatalf("a failed replacement left %d mappings", n)
	}
	// The composite FKs are the backstop for cross-property mappings, even through raw SQL.
	err = f.Exec(t, `INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence) VALUES ($1, $2, $3, $4, 1)`, f.tenantID, f.bali, room.ID, foreignTax.ID)
	if err == nil {
		t.Fatal("cross-property mapping must be rejected by the database")
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'charge_code.rules_replaced'`); n < 5 {
		t.Fatalf("rule replacements must be audited: %d", n)
	}
}

func rules2() billingconfig.RulesInput { return billingconfig.RulesInput{} }

func TestChargeCodeEditingAndLocks(t *testing.T) {
	f := newFixture(t)
	created, err := f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{
		Code: "spa", Name: "Spa", ChargeType: "service", PriceMode: "inclusive", DefaultUnitPrice: "250000", IsActive: true,
	})
	if err != nil || created.Code != "SPA" || created.ChargeType != "SERVICE" || created.PriceMode != "INCLUSIVE" || created.IsSystem ||
		created.DefaultUnitPrice == nil || *created.DefaultUnitPrice != "250000" {
		t.Fatalf("create: %v %+v", err, created)
	}
	_, err = f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "SPA", Name: "x", ChargeType: "OTHER", PriceMode: "EXCLUSIVE", IsActive: true})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "X1", Name: "x", ChargeType: "NOPE", PriceMode: "GROSS", DefaultUnitPrice: "100.50", IsActive: true})
	fields := map[string]bool{}
	for _, fe := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[fe.Field] = true
	}
	if !fields["charge_type"] || !fields["price_mode"] || !fields["default_unit_price"] { // IDR has no decimals
		t.Fatalf("fields: %v", fields)
	}

	// Price mode is free while unused, then locked (by the database trigger) once a rate plan uses the code.
	excl := "EXCLUSIVE"
	if u, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{PriceMode: &excl}); err != nil || u.PriceMode != "EXCLUSIVE" {
		t.Fatalf("unused code: %v %+v", err, u)
	}
	room := f.codeByName(t, "ROOM")
	if err := f.Exec(t, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3)`, f.tenantID, f.bali, room.ID); err != nil {
		t.Fatal(err)
	}
	incl := "INCLUSIVE"
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{PriceMode: &incl})
	wantCode(t, err, "PRICE_MODE_LOCKED")
	if same, err := f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{PriceMode: &excl}); err != nil || same.PriceMode != "EXCLUSIVE" {
		t.Fatalf("re-sending the same mode is not a change: %v", err)
	}
	if got := f.codeByName(t, "ROOM"); got.PriceMode != "EXCLUSIVE" {
		t.Fatalf("price mode changed to %s", got.PriceMode)
	}

	// System codes keep their type; deactivation is blocked by active rate plans only.
	fee := "FEE"
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{ChargeType: &fee})
	wantCode(t, err, "SYSTEM_CHARGE_CODE_LOCKED")
	off := false
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{IsActive: &off})
	if c := code(t, err, "CHARGE_CODE_IN_USE"); c.Context["active_rate_plans"] != int64(1) {
		t.Fatalf("context: %v", c.Context)
	}
	if err := f.Exec(t, `UPDATE rate_plans SET is_active = false WHERE property_id = $1`, f.bali); err != nil {
		t.Fatal(err)
	}
	if u, err := f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{IsActive: &off}); err != nil || u.IsActive {
		t.Fatalf("deactivate after the plan is inactive: %v", err)
	}
	// A code used for room revenue stays ROOM (database trigger).
	room2 := f.codeByName(t, "ROOM_EXEMPT")
	if err := f.Exec(t, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'NETT', 'Nett', $3)`, f.tenantID, f.bali, room2.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Exec(t, `UPDATE charge_codes SET charge_type = 'OTHER' WHERE id = $1`, room2.ID); err == nil {
		t.Fatal("room revenue code cannot change type")
	}

	// Default price: set, clear with "", precision.
	price := "300000"
	if u, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{DefaultUnitPrice: &price}); err != nil || *u.DefaultUnitPrice != "300000" {
		t.Fatalf("set price: %v %+v", err, u)
	}
	empty := ""
	if u, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{DefaultUnitPrice: &empty}); err != nil || u.DefaultUnitPrice != nil {
		t.Fatalf("clear price: %v %+v", err, u)
	}
	bad := "10.5"
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{DefaultUnitPrice: &bad})
	wantCode(t, err, "VALIDATION_FAILED")

	got, err := f.Billing.GetChargeCode(f.admin, f.bali, created.ID)
	if err != nil || got.Code != "SPA" {
		t.Fatalf("get: %v", err)
	}
	service := "SERVICE"
	if list, err := f.Billing.ListChargeCodes(f.admin, f.bali, 0, billingconfig.ChargeCodeFilter{ChargeType: &service}, 50); err != nil || len(list) != 3 { // LAUNDRY, EXTRA_BED, SPA
		t.Fatalf("type filter: %v %d", err, len(list))
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'charge_code' AND action = 'charge_code.updated'`); n < 5 {
		t.Fatalf("updates must be audited: %d", n)
	}
}

func TestResolverReturnsActiveOrderedRules(t *testing.T) {
	f := newFixture(t)
	vat := f.tax(t, "VAT", "11", true)
	city := f.tax(t, "CITY", "1.5", false)
	svc := f.svc(t, "SVC", "10")
	room := f.codeByName(t, "ROOM")
	exempt := f.codeByName(t, "ROOM_EXEMPT")
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{
		Taxes:          []billingconfig.TaxRuleInput{{TaxID: city.ID, Sequence: 2}, {TaxID: vat.ID, Sequence: 1}},
		ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := f.Billing.ResolveRules(context.Background(), f.tenantID, f.bali, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "ROOM" || got.PriceMode != "EXCLUSIVE" || len(got.Taxes) != 2 || got.Taxes[0].Code != "VAT" || got.Taxes[1].Code != "CITY" ||
		!got.Taxes[0].Rate.Equal(decimal.NewFromInt(11)) || !got.Taxes[1].Rate.Equal(decimal.RequireFromString("1.5")) || !got.Taxes[0].OnService || got.Taxes[1].OnService ||
		len(got.ServiceCharges) != 1 || !got.ServiceCharges[0].Rate.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("resolved: %+v", got)
	}
	if ex, err := f.Billing.ResolveRules(context.Background(), f.tenantID, f.bali, exempt.ID); err != nil || len(ex.Taxes) != 0 || len(ex.ServiceCharges) != 0 {
		t.Fatalf("a code without rules has none: %v %+v", err, ex)
	}
	// A mapping only applies while the tax itself is active (even if someone bypassed the service).
	if err := f.Exec(t, `UPDATE taxes SET is_active = false WHERE id = $1`, city.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.Billing.ResolveRules(context.Background(), f.tenantID, f.bali, room.ID)
	if len(got.Taxes) != 1 || got.Taxes[0].Code != "VAT" {
		t.Fatalf("inactive tax must not apply: %+v", got.Taxes)
	}
	// Scoped by property and tenant.
	jkt := f.Property(t, f.tenantID, "JKT")
	_, err = f.Billing.ResolveRules(context.Background(), f.tenantID, jkt.ID, room.ID)
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	xyz := f.Tenant(t, "XYZ")
	_, err = f.Billing.ResolveRules(context.Background(), xyz.ID, f.bali, room.ID)
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
}

func TestRateChangeReportsAffectedOpenStays(t *testing.T) {
	f := newFixture(t)
	vat := f.tax(t, "VAT", "11", true)
	room := f.codeByName(t, "ROOM")
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}}); err != nil {
		t.Fatal(err)
	}
	dlx := f.RoomType(t, f.admin, f.bali, "DLX")
	r1 := f.Room(t, f.admin, f.bali, dlx.ID, "101")
	r2 := f.Room(t, f.admin, f.bali, dlx.ID, "102")
	for _, r := range []int64{r1.ID, r2.ID} {
		stay := f.Stay(t, f.tenantID, f.bali, dlx.ID, r, "2026-09-30", "2026-10-03")
		err := f.Exec(t, `INSERT INTO reservation_room_rates (tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode, base_rate, amount)
			SELECT $1, $2, s.reservation_room_id, '2026-10-01', (SELECT id FROM rate_plans WHERE property_id = $2 AND code = 'BAR'), $3, 'EXCLUSIVE', 1000000, 1000000
			  FROM stays s WHERE s.id = $4`, f.tenantID, f.bali, room.ID, stay)
		if err != nil {
			t.Fatal(err)
		}
	}
	// One stay is already checked out: it is not affected.
	if err := f.Exec(t, `UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = now() WHERE id = (SELECT max(id) FROM stays)`); err != nil {
		t.Fatal(err)
	}
	rate := "10"
	u, err := f.Billing.UpdateTax(f.admin, f.bali, vat.ID, billingconfig.TaxPatch{Rate: &rate})
	if err != nil || u.AffectedOpenStays == nil || *u.AffectedOpenStays != 1 {
		t.Fatalf("affected open stays: %v %+v", err, u)
	}
}

func TestPermissionsAndIsolation(t *testing.T) {
	f := newFixture(t)
	jkt := f.Property(t, f.tenantID, "JKT")
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	vat := f.tax(t, "VAT", "11", false)

	reader := f.User(t, f.tenantID, f.bali) // a grant without permissions
	manager := f.User(t, f.tenantID, f.bali, auth.PermBillingConfigManage)
	in := billingconfig.TaxInput{Code: "CITY", Name: "City", Rate: "1", IsActive: true}

	if _, err := f.Billing.ListTaxes(reader, f.bali, 0, nil, 10); err != nil {
		t.Fatalf("reading needs property access only: %v", err)
	}
	if _, err := f.Billing.ListChargeCodes(reader, f.bali, 0, billingconfig.ChargeCodeFilter{}, 10); err != nil {
		t.Fatal(err)
	}
	_, err := f.Billing.CreateTax(reader, f.bali, in)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Billing.UpdateTax(reader, f.bali, vat.ID, billingconfig.TaxPatch{Name: new(string)})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Billing.CreateServiceCharge(reader, f.bali, billingconfig.ServiceChargeInput{Code: "S", Name: "S", Rate: "1"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Billing.CreateChargeCode(reader, f.bali, billingconfig.ChargeCodeInput{Code: "X", Name: "X", ChargeType: "OTHER", PriceMode: "EXCLUSIVE"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Billing.ReplaceRules(reader, f.bali, f.codeByName(t, "ROOM").ID, billingconfig.RulesInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.Billing.CreateTax(manager, f.bali, in); err != nil {
		t.Fatalf("billing_config.manage: %v", err)
	}

	// A property without a grant, and another tenant's property, are both 404.
	for _, pid := range []int64{jkt.ID, sg.ID} {
		_, err = f.Billing.ListTaxes(manager, pid, 0, nil, 10)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
		_, err = f.Billing.CreateTax(manager, pid, in)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
	}
	_, err = f.Billing.ListChargeCodes(f.admin, sg.ID, 0, billingconfig.ChargeCodeFilter{}, 10)
	wantCode(t, err, "PROPERTY_NOT_FOUND") // tenant administrators stay inside their tenant

	// Ids of another property never resolve through an accessible one.
	jktTax, err := f.Billing.CreateTax(f.admin, jkt.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.UpdateTax(f.admin, f.bali, jktTax.ID, billingconfig.TaxPatch{Name: new(string)})
	wantCode(t, err, "TAX_NOT_FOUND")
	var jktRoom int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, jkt.ID).Scan(&jktRoom); err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.GetChargeCode(f.admin, f.bali, jktRoom)
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	_, err = f.Billing.ListTaxes(context.Background(), f.bali, 0, nil, 10)
	wantCode(t, err, "UNAUTHENTICATED")
}

// Mapping a tax and deactivating it at the same moment: never both succeed.
func TestMappingVersusDeactivationRace(t *testing.T) {
	f := newFixture(t)
	room := f.codeByName(t, "ROOM")
	for round := range 8 {
		tx := f.tax(t, fmt.Sprintf("T%d", round), "5", false)
		var mapErr, offErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, mapErr = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{Taxes: []billingconfig.TaxRuleInput{{TaxID: tx.ID, Sequence: 1}}})
		}()
		go func() {
			defer wg.Done()
			<-start
			off := false
			_, offErr = f.Billing.UpdateTax(f.admin, f.bali, tx.ID, billingconfig.TaxPatch{IsActive: &off})
		}()
		close(start)
		wg.Wait()
		switch {
		case mapErr == nil && offErr == nil:
			t.Fatalf("round %d: a tax was mapped and deactivated at the same time", round)
		case mapErr != nil && !apperr.IsCode(mapErr, "VALIDATION_FAILED"):
			t.Fatalf("round %d: unexpected mapping error %v", round, mapErr)
		case offErr != nil && !apperr.IsCode(offErr, "TAX_IN_USE"):
			t.Fatalf("round %d: unexpected deactivation error %v", round, offErr)
		}
		if n := f.Count(t, `SELECT count(*) FROM charge_code_taxes m JOIN taxes t ON t.id = m.tax_id WHERE m.is_active AND NOT t.is_active`); n != 0 {
			t.Fatalf("round %d: %d active mappings of inactive taxes", round, n)
		}
		if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{}); err != nil {
			t.Fatal(err)
		}
	}
}

// Concurrent edits of one charge code's rules serialise; none fails on the sequence index.
func TestConcurrentRuleReplacementsSerialise(t *testing.T) {
	f := newFixture(t)
	vat := f.tax(t, "VAT", "11", true)
	city := f.tax(t, "CITY", "1", false)
	room := f.codeByName(t, "ROOM")
	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			first, second := vat.ID, city.ID
			if i%2 == 1 {
				first, second = city.ID, vat.ID // half of them swap the order
			}
			_, errs[i] = f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{
				Taxes: []billingconfig.TaxRuleInput{{TaxID: first, Sequence: 1}, {TaxID: second, Sequence: 2}},
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
	if c := f.Count(t, `SELECT count(*) FROM charge_code_taxes WHERE charge_code_id = $1 AND is_active`, room.ID); c != 2 {
		t.Fatalf("active rules: %d", c)
	}
	if c := f.Count(t, `SELECT count(DISTINCT sequence) FROM charge_code_taxes WHERE charge_code_id = $1 AND is_active`, room.ID); c != 2 {
		t.Fatalf("distinct sequences: %d", c)
	}
}
