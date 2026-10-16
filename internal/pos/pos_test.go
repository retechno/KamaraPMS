package pos_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/pos"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

type fx struct {
	*roomstest.Env
	svc              *pos.Service
	tenantID, propID int64
	admin            context.Context
	waiter           context.Context // pos.order: reads, sells out, writes nothing else
	nobody           context.Context
	dept             int64
	tax, service     int64
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func ptr[T any](v T) *T            { return &v }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID)}
	f.svc = pos.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Departments)
	f.waiter = e.User(t, tn.ID, p.ID, auth.PermPOSOrder)
	f.nobody = e.User(t, tn.ID, p.ID, auth.PermFolioRead)
	ctx := context.Background()
	must(t, f.Pool.QueryRow(ctx, `SELECT id FROM departments WHERE property_id = $1 AND code = 'FB'`, p.ID).Scan(&f.dept))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO taxes (tenant_id, property_id, code, name, rate, tax_on_service) VALUES ($1, $2, 'PB1', 'PB1', 10, true) RETURNING id`, tn.ID, p.ID).Scan(&f.tax))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO service_charges (tenant_id, property_id, code, name, rate) VALUES ($1, $2, 'SVC', 'Service', 10) RETURNING id`, tn.ID, p.ID).Scan(&f.service))
	return f
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func fieldCode(t *testing.T, err error, field string) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want an *apperr.Error", err)
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return f.Code
		}
	}
	t.Fatalf("no error on %s: %+v", field, e.Fields)
	return ""
}

// kit makes a rule profile, a price list and a category, the things an outlet and a product point to.
type kit struct {
	profile   pos.RuleProfile
	priceList pos.PriceList
	cat       pos.Category
}

func (f *fx) kit(t *testing.T) kit {
	t.Helper()
	prof, err := f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "FB", Name: "Food and beverage", PriceMode: "EXCLUSIVE",
		Rules: []pos.RuleInput{{ServiceChargeID: &f.service}, {TaxID: &f.tax}}})
	must(t, err)
	pl, err := f.svc.CreatePriceList(f.admin, f.propID, pos.PriceListInput{Code: "MAIN", Name: "Main"})
	must(t, err)
	cat, err := f.svc.CreateCategory(f.admin, f.propID, pos.CategoryInput{Code: "MAINS", Name: "Main dishes", SortOrder: 1})
	must(t, err)
	return kit{profile: prof, priceList: pl, cat: cat}
}

func (f *fx) outlet(t *testing.T, k kit, code string) pos.Outlet {
	t.Helper()
	o, err := f.svc.CreateOutlet(f.admin, f.propID, pos.OutletInput{
		Code: code, Name: code + " outlet", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: f.dept, RuleProfileID: k.profile.ID, PriceListID: k.priceList.ID,
		Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, CompTreatment: "PAYMENT_COMP",
	})
	must(t, err)
	return o
}

func (f *fx) chargeCode(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func TestAnOutletIsSetUpWithItsDepartmentProfilePriceListAndDrawer(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "resto")
	if o.Code != "RESTO" || o.OrderMode != "PRINT" || o.DepartmentID != f.dept || o.DepartmentName == "" || o.RuleProfileName != "Food and beverage" || o.PriceListName != "Main" ||
		o.Drawer == nil || *o.Drawer != "DRAWER1" || !o.IsActive || o.Version != 1 || o.CompTreatment != "PAYMENT_COMP" {
		t.Fatalf("outlet: %+v", o)
	}
	var next int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT next_bill_no FROM pos_outlets WHERE id = $1`, o.ID).Scan(&next))
	if next != 1 {
		t.Fatalf("the bill sequence starts at 1: %d", next)
	}
	list, err := f.svc.ListOutlets(f.waiter, f.propID, nil)
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("a waiter reads the outlets: %+v", list)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.outlet_created'`) != 1 {
		t.Fatal("creation is audited")
	}
}

func TestAnOutletIsValidated(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	base := pos.OutletInput{Code: "RS", Name: "Room service", OutletType: "ROOM_SERVICE", OrderMode: "PRINT", DepartmentID: f.dept, RuleProfileID: k.profile.ID, PriceListID: k.priceList.ID, CompTreatment: "PAYMENT_COMP"}
	for name, c := range map[string]struct {
		mut   func(*pos.OutletInput)
		field string
		code  string
	}{
		"a code another document uses": {func(i *pos.OutletInput) { i.Code = "FOL" }, "code", "RESERVED"},
		"a code with digits":           {func(i *pos.OutletInput) { i.Code = "R1" }, "code", "INVALID_FORMAT"},
		"a type that does not exist":   {func(i *pos.OutletInput) { i.OutletType = "CASINO" }, "outlet_type", "INVALID_VALUE"},
		"a mode that does not exist":   {func(i *pos.OutletInput) { i.OrderMode = "FAX" }, "order_mode", "INVALID_VALUE"},
		"a treatment that is unknown":  {func(i *pos.OutletInput) { i.CompTreatment = "FREE" }, "comp_treatment", "INVALID_VALUE"},
		"a rounding of 250":            {func(i *pos.OutletInput) { i.RoundingUnit = 250 }, "rounding_unit", "INVALID_VALUE"},
		"a department that is missing": {func(i *pos.OutletInput) { i.DepartmentID = 0 }, "department_id", "REQUIRED"},
	} {
		in := base
		c.mut(&in)
		_, err := f.svc.CreateOutlet(f.admin, f.propID, in)
		if got := fieldCode(t, err, c.field); got != c.code {
			t.Errorf("%s: %s, want %s", name, got, c.code)
		}
	}
	in := base
	in.DepartmentID = 999999
	_, err := f.svc.CreateOutlet(f.admin, f.propID, in)
	if fieldCode(t, err, "department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("a department of the property")
	}
	_, err = f.svc.CreateOutlet(f.admin, f.propID, base)
	must(t, err)
	_, err = f.svc.CreateOutlet(f.admin, f.propID, base)
	wantCode(t, err, "CODE_TAKEN")
	if _, err := f.svc.CreateOutlet(f.waiter, f.propID, base); err == nil {
		t.Fatal("a waiter does not set up outlets")
	}
}

func TestChangingTheCompTreatmentIsAuditedWithTheOldAndTheNewAndNeedsTheVersion(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "RESTO")
	in := pos.OutletInput{Name: o.Name, OutletType: o.OutletType, OrderMode: o.OrderMode, DepartmentID: o.DepartmentID, RuleProfileID: o.RuleProfileID, PriceListID: o.PriceListID,
		Drawer: o.Drawer, UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "DISCOUNT_100", Version: o.Version}
	got, err := f.svc.UpdateOutlet(f.admin, f.propID, o.ID, in)
	must(t, err)
	if got.CompTreatment != "DISCOUNT_100" || got.Version != 2 || got.Code != "RESTO" {
		t.Fatalf("updated: %+v", got)
	}
	var old, updated string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT old_data->>'comp_treatment', new_data->>'comp_treatment' FROM audit_logs WHERE action = 'pos.outlet_comp_treatment_changed'`).Scan(&old, &updated))
	if old != "PAYMENT_COMP" || updated != "DISCOUNT_100" {
		t.Fatalf("audit: %s -> %s", old, updated)
	}
	_, err = f.svc.UpdateOutlet(f.admin, f.propID, o.ID, in) // the old version
	wantCode(t, err, "VERSION_CONFLICT")
	in.Version = 0
	_, err = f.svc.UpdateOutlet(f.admin, f.propID, o.ID, in)
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestAProfileReplacesTheRulesOfTheChargeCodeInTheCalculation(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	bar := f.chargeCode(t, "RESTAURANT")
	prof := k.profile
	q := func(profile *int64) decimal.Decimal {
		b, err := f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: f.propID, ChargeCodeID: bar, Quantity: dec("2"), UnitPrice: dec("50000"), RuleProfileID: profile})
		must(t, err)
		return b.TotalAmount
	}
	withoutProfile := q(nil)
	// service 10% on 100,000 = 10,000; tax 10% on 110,000 = 11,000
	if got := q(&prof.ID); !got.Equal(dec("121000")) {
		t.Fatalf("the profile of service 10%% and tax 10%% on service: %s", got)
	}
	// a profile with a tax only
	taxOnly, err := f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "TAXONLY", Name: "Tax only", PriceMode: "EXCLUSIVE", Rules: []pos.RuleInput{{TaxID: &f.tax}}})
	must(t, err)
	if got := q(&taxOnly.ID); !got.Equal(dec("110000")) {
		t.Fatalf("tax only: %s", got)
	}
	// an inclusive profile: the price already contains them
	incl, err := f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "INCL", Name: "Inclusive", PriceMode: "INCLUSIVE", Rules: []pos.RuleInput{{TaxID: &f.tax}}})
	must(t, err)
	if got := q(&incl.ID); !got.Equal(dec("100000")) {
		t.Fatalf("inclusive: the total is the price: %s", got)
	}
	// the transfer code ignores a profile (it has no rules)
	trf := f.chargeCode(t, "TRF")
	b, err := f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: f.propID, ChargeCodeID: trf, Quantity: dec("1"), UnitPrice: dec("5000"), RuleProfileID: &prof.ID})
	must(t, err)
	if !b.TotalAmount.Equal(dec("5000")) || len(b.TaxComponents) != 0 || len(b.ServiceComponents) != 0 {
		t.Fatalf("TRF: %+v", b)
	}
	_ = withoutProfile
	// an unknown profile is refused
	bad := int64(999999)
	_, err = f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: f.propID, ChargeCodeID: bar, Quantity: dec("1"), UnitPrice: dec("1"), RuleProfileID: &bad})
	wantCode(t, err, "RULE_PROFILE_NOT_FOUND")
	// a rule switched off does not count
	must(t, f.Exec(t, `UPDATE service_charges SET is_active = false WHERE id = $1`, f.service))
	if got := q(&prof.ID); !got.Equal(dec("110000")) {
		t.Fatalf("a switched-off service charge is left out: %s", got)
	}
}

func TestProfilesTakeOnlyActiveRulesOfTheProperty(t *testing.T) {
	f := setup(t)
	_, err := f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "X", Name: "X", PriceMode: "EXCLUSIVE", Rules: []pos.RuleInput{{TaxID: ptr(int64(999999))}}})
	if fieldCode(t, err, "rules[0].tax_id") != "NOT_FOUND" {
		t.Fatal("a tax of the property")
	}
	_, err = f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "X", Name: "X", PriceMode: "EXCLUSIVE", Rules: []pos.RuleInput{{TaxID: &f.tax, ServiceChargeID: &f.service}}})
	if fieldCode(t, err, "rules[0].tax_id") != "INVALID_VALUE" {
		t.Fatal("one rule is a tax or a service charge")
	}
	_, err = f.svc.CreateRuleProfile(f.admin, f.propID, pos.RuleProfileInput{Code: "X", Name: "X", PriceMode: "EXCLUSIVE", Rules: []pos.RuleInput{{TaxID: &f.tax}, {TaxID: &f.tax}}})
	if fieldCode(t, err, "rules[1].tax_id") != "DUPLICATE" {
		t.Fatal("a rule once")
	}
	k := f.kit(t)
	got, err := f.svc.UpdateRuleProfile(f.admin, f.propID, k.profile.ID, pos.RuleProfileInput{Name: "F&B 2", PriceMode: "INCLUSIVE", Rules: []pos.RuleInput{{TaxID: &f.tax}}})
	must(t, err)
	if got.Code != "FB" || got.Name != "F&B 2" || got.PriceMode != "INCLUSIVE" || len(got.Rules) != 1 || got.Rules[0].RuleType != "TAX" || !got.Rules[0].Rate.Equal(dec("10")) {
		t.Fatalf("profile: %+v", got)
	}
}

func TestAProductHasVariantsAddOnsAndAPricePerList(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	food := f.chargeCode(t, "RESTAURANT")
	in := pos.ProductInput{
		Code: "NASGOR", Name: "Nasi Goreng Kampung", ShortName: ptr("Nasgor"), CategoryID: k.cat.ID, ChargeCodeID: food,
		OptionGroups: []pos.OptionGroup{
			{Name: "Portion", Kind: "VARIANT", MinSelect: 1, MaxSelect: 1, Options: []pos.Option{{Name: "Regular", MaxQty: 1, IsDefault: true}, {Name: "Jumbo", PriceDelta: dec("20000"), MaxQty: 1}}},
			{Name: "Extras", Kind: "MODIFIER", MinSelect: 0, MaxSelect: 3, Options: []pos.Option{{Name: "Fried egg", PriceDelta: dec("8000"), MaxQty: 2}, {Name: "No fried onion", MaxQty: 1}}},
		},
		Prices: []pos.ProductPriceIn{{PriceListID: k.priceList.ID, Price: dec("85000"), IsSold: true}},
	}
	p, err := f.svc.CreateProduct(f.admin, f.propID, in)
	must(t, err)
	if p.Code != "NASGOR" || len(p.OptionGroups) != 2 || p.OptionGroups[0].Kind != "VARIANT" || len(p.OptionGroups[0].Options) != 2 || !p.OptionGroups[0].Options[1].PriceDelta.Equal(dec("20000")) ||
		len(p.Prices) != 1 || !p.Prices[0].Price.Equal(dec("85000")) || p.Version != 1 {
		t.Fatalf("product: %+v", p)
	}
	// replace it: a variant group goes, a price changes
	in.Version = p.Version
	in.OptionGroups = in.OptionGroups[1:]
	in.Prices[0].Price = dec("90000")
	q, err := f.svc.UpdateProduct(f.admin, f.propID, p.ID, in)
	must(t, err)
	if q.Version != 2 || len(q.OptionGroups) != 1 || !q.Prices[0].Price.Equal(dec("90000")) || q.Code != "NASGOR" {
		t.Fatalf("replaced: %+v", q)
	}
	_, err = f.svc.UpdateProduct(f.admin, f.propID, p.ID, in) // the old version
	wantCode(t, err, "VERSION_CONFLICT")
	// validation
	for name, c := range map[string]struct {
		mut   func(*pos.ProductInput)
		field string
	}{
		"a variant that is pick two": {func(i *pos.ProductInput) {
			i.OptionGroups = []pos.OptionGroup{{Name: "V", Kind: "VARIANT", MinSelect: 1, MaxSelect: 2, Options: []pos.Option{{Name: "a", MaxQty: 1}}}}
		}, "option_groups[0].max_select"},
		"an add-on with a negative price": {func(i *pos.ProductInput) {
			i.OptionGroups = []pos.OptionGroup{{Name: "M", Kind: "MODIFIER", MinSelect: 0, MaxSelect: 1, Options: []pos.Option{{Name: "a", PriceDelta: dec("-1"), MaxQty: 1}}}}
		}, "option_groups[0].options[0].price_delta"},
		"a group without choices": {func(i *pos.ProductInput) {
			i.OptionGroups = []pos.OptionGroup{{Name: "M", Kind: "MODIFIER", MinSelect: 0, MaxSelect: 1}}
		}, "option_groups[0].options"},
		"a price with too many decimals": {func(i *pos.ProductInput) { i.Prices[0].Price = dec("10.5") }, "prices[0].price"},
		"a negative price":               {func(i *pos.ProductInput) { i.Prices[0].Price = dec("-1") }, "prices[0].price"},
		"a short name too long to print": {func(i *pos.ProductInput) { i.ShortName = ptr(strings.Repeat("x", 25)) }, "short_name"},
		"two prices for one list":        {func(i *pos.ProductInput) { i.Prices = append(i.Prices, i.Prices[0]) }, "prices[1].price_list_id"},
	} {
		bad := in
		bad.Code = "OTHER"
		bad.Prices = append([]pos.ProductPriceIn(nil), in.Prices...)
		c.mut(&bad)
		_, err := f.svc.CreateProduct(f.admin, f.propID, bad)
		if _, ok := apperr.As(err); !ok {
			t.Errorf("%s: %v", name, err)
			continue
		}
		fieldCode(t, err, c.field)
	}
	// a room code is not for sale in an outlet
	room := in
	room.Code, room.ChargeCodeID = "ROOMS", f.chargeCode(t, "ROOM")
	_, err = f.svc.CreateProduct(f.admin, f.propID, room)
	if fieldCode(t, err, "charge_code_id") != "INVALID_VALUE" {
		t.Fatal("a room code")
	}
	_, err = f.svc.CreateProduct(f.admin, f.propID, in)
	wantCode(t, err, "CODE_TAKEN")
}

func TestPricesPerOutletSoldOutTodayAndTheList(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "RESTO")
	food := f.chargeCode(t, "RESTAURANT")
	mk := func(code string) pos.Product {
		p, err := f.svc.CreateProduct(f.admin, f.propID, pos.ProductInput{Code: code, Name: code, CategoryID: k.cat.ID, ChargeCodeID: food})
		must(t, err)
		return p
	}
	a, b := mk("A"), mk("B")
	items, err := f.svc.SetPriceItems(f.admin, f.propID, k.priceList.ID, []pos.PriceItemInput{{ProductID: a.ID, Price: dec("25000"), IsSold: true}, {ProductID: b.ID, Price: dec("30000"), IsSold: false}})
	must(t, err)
	if len(items) != 2 {
		t.Fatalf("items: %+v", items)
	}
	list, err := f.svc.ListProducts(f.waiter, f.propID, &o.ID, nil)
	must(t, err)
	byCode := map[string]pos.Product{}
	for _, p := range list {
		byCode[p.Code] = p
	}
	if byCode["A"].Price == nil || !byCode["A"].Price.Equal(dec("25000")) || !*byCode["A"].IsSold || *byCode["B"].IsSold {
		t.Fatalf("the price and whether it is sold at the outlet: %+v", byCode)
	}
	must(t, f.svc.SetSoldOut(f.waiter, f.propID, o.ID, a.ID, true))
	ids, err := f.svc.ListSoldOut(f.waiter, f.propID, o.ID)
	must(t, err)
	if len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("sold out today: %v", ids)
	}
	// the mark belongs to the business day it was made: when the day changes it is not there
	must(t, f.Exec(t, `UPDATE pos_outlet_sold_out SET business_date = business_date - 1`))
	ids, err = f.svc.ListSoldOut(f.waiter, f.propID, o.ID)
	must(t, err)
	if len(ids) != 0 {
		t.Fatalf("it clears itself with the day: %v", ids)
	}
	must(t, f.svc.SetSoldOut(f.waiter, f.propID, o.ID, a.ID, true))
	must(t, f.svc.SetSoldOut(f.waiter, f.propID, o.ID, a.ID, false))
	ids, _ = f.svc.ListSoldOut(f.waiter, f.propID, o.ID)
	if len(ids) != 0 {
		t.Fatal("cleared")
	}
	// a price list that an active outlet uses is not switched off
	_, err = f.svc.UpdatePriceList(f.admin, f.propID, k.priceList.ID, pos.PriceListInput{Name: "Main", IsActive: ptr(false)})
	wantCode(t, err, "PRICE_LIST_IN_USE")
	if _, err := f.svc.SetPriceItems(f.waiter, f.propID, k.priceList.ID, nil); err == nil {
		t.Fatal("a waiter does not set prices")
	}
}

func TestTheFloorPlanIsSavedWholeWithAVersionAndTablesSwapCodes(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "RESTO")
	area, err := f.svc.CreateArea(f.admin, f.propID, o.ID, pos.AreaInput{Name: "Indoors", SortOrder: 1})
	must(t, err)
	if area.GridW != 40 || area.GridH != 24 || area.LayoutVersion != 1 {
		t.Fatalf("area: %+v", area)
	}
	in := pos.LayoutInput{Name: "Indoors", SortOrder: 1, GridW: 40, GridH: 24, LayoutVersion: area.LayoutVersion,
		Tables: []pos.Table{
			{Code: "1", Capacity: 2, Shape: "SQUARE", X: 2, Y: 2, W: 4, H: 4},
			{Code: "2", Capacity: 4, Shape: "ROUND", X: 8, Y: 2, W: 5, H: 5},
		},
		Marks: []pos.Mark{{Kind: "DOOR", Label: "Entrance", X: 0, Y: 20, W: 6, H: 2}}}
	saved, err := f.svc.SaveLayout(f.admin, f.propID, area.ID, in)
	must(t, err)
	if saved.LayoutVersion != 2 || len(saved.Tables) != 2 || len(saved.Marks) != 1 || saved.Marks[0].Kind != "DOOR" {
		t.Fatalf("saved: %+v", saved)
	}
	// the same version again is a conflict: someone saved in between
	_, err = f.svc.SaveLayout(f.admin, f.propID, area.ID, in)
	wantCode(t, err, "VERSION_CONFLICT")
	// swap the codes of the two tables in one save, and drop nothing
	next := pos.LayoutInput{Name: "Indoors", SortOrder: 1, GridW: 40, GridH: 24, LayoutVersion: saved.LayoutVersion,
		Tables: []pos.Table{
			{ID: saved.Tables[0].ID, Code: "2", Capacity: 2, Shape: "SQUARE", X: 2, Y: 2, W: 4, H: 4},
			{ID: saved.Tables[1].ID, Code: "1", Capacity: 4, Shape: "ROUND", X: 8, Y: 2, W: 5, H: 5},
		}}
	swapped, err := f.svc.SaveLayout(f.admin, f.propID, area.ID, next)
	must(t, err)
	codes := map[int64]string{}
	for _, tb := range swapped.Tables {
		codes[tb.ID] = tb.Code
	}
	if codes[saved.Tables[0].ID] != "2" || codes[saved.Tables[1].ID] != "1" || len(swapped.Marks) != 0 {
		t.Fatalf("swapped: %+v", swapped)
	}
	// a table that is not in the list is switched off, not deleted
	next.LayoutVersion = swapped.LayoutVersion
	next.Tables = next.Tables[:1]
	dropped, err := f.svc.SaveLayout(f.admin, f.propID, area.ID, next)
	must(t, err)
	active := 0
	for _, tb := range dropped.Tables {
		if tb.IsActive {
			active++
		}
	}
	if len(dropped.Tables) != 2 || active != 1 {
		t.Fatalf("one table is off: %+v", dropped.Tables)
	}
	// validation: outside the plan, a duplicate code, a bad shape
	bad := next
	bad.LayoutVersion = dropped.LayoutVersion
	bad.Tables = []pos.Table{{Code: "9", Capacity: 2, Shape: "SQUARE", X: 38, Y: 2, W: 4, H: 4}}
	_, err = f.svc.SaveLayout(f.admin, f.propID, area.ID, bad)
	if fieldCode(t, err, "tables[0].x") != "OUT_OF_PLAN" {
		t.Fatal("inside the plan")
	}
	bad.Tables = []pos.Table{{Code: "7", Capacity: 2, Shape: "SQUARE", X: 1, Y: 1, W: 2, H: 2}, {Code: "7", Capacity: 2, Shape: "TRIANGLE", X: 5, Y: 1, W: 2, H: 2}}
	_, err = f.svc.SaveLayout(f.admin, f.propID, area.ID, bad)
	if fieldCode(t, err, "tables[1].code") != "DUPLICATE" || fieldCode(t, err, "tables[1].shape") != "INVALID_VALUE" {
		t.Fatal("a code once, a shape that exists")
	}
	// a code is unique in the outlet, across its areas
	other, err := f.svc.CreateArea(f.admin, f.propID, o.ID, pos.AreaInput{Name: "Terrace"})
	must(t, err)
	_, err = f.svc.SaveLayout(f.admin, f.propID, other.ID, pos.LayoutInput{Name: "Terrace", GridW: 40, GridH: 24, LayoutVersion: other.LayoutVersion,
		Tables: []pos.Table{{Code: "2", Capacity: 2, Shape: "SQUARE", X: 1, Y: 1, W: 2, H: 2}}})
	wantCode(t, err, "CODE_TAKEN")
	plan, err := f.svc.GetFloorPlan(f.waiter, f.propID, o.ID)
	must(t, err)
	if len(plan.Areas) != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	if _, err := f.svc.SaveLayout(f.waiter, f.propID, area.ID, next); err == nil {
		t.Fatal("a waiter does not draw the plan")
	}
}

func TestTheCSVImportIsAllOrNothingAndUpdatesByCode(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	kitchen, err := f.svc.CreateStation(f.admin, f.propID, pos.StationInput{Code: "KITCHEN", Name: "Main kitchen", LateAfterMinutes: 15})
	must(t, err)
	_ = kitchen
	csv := "code,name,category,charge_code,station,price_list,price\n" +
		"NASGOR,Nasi Goreng,MAINS,RESTAURANT,KITCHEN,MAIN,85000\n" +
		"SOTO,Soto Ayam,MAINS,RESTAURANT,KITCHEN,MAIN,75000\n"
	dry, err := f.svc.ImportProducts(f.admin, f.propID, csv, true)
	must(t, err)
	if !dry.DryRun || dry.Rows != 2 || dry.Created != 2 || len(dry.Errors) != 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	if f.Count(t, `SELECT count(*) FROM pos_products`) != 0 {
		t.Fatal("a dry run writes nothing")
	}
	res, err := f.svc.ImportProducts(f.admin, f.propID, csv, false)
	must(t, err)
	if res.Created != 2 || res.Updated != 0 || f.Count(t, `SELECT count(*) FROM pos_products`) != 2 || f.Count(t, `SELECT count(*) FROM pos_price_list_items`) != 2 {
		t.Fatalf("imported: %+v", res)
	}
	// again with a new price and a name: updates by code
	again := "code;name;category;charge_code;price_list;price\nNASGOR;Nasi Goreng Special;MAINS;RESTAURANT;MAIN;90000\n"
	res, err = f.svc.ImportProducts(f.admin, f.propID, again, false)
	must(t, err)
	if res.Updated != 1 || res.Created != 0 {
		t.Fatalf("update: %+v", res)
	}
	var name, price string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT p.name, i.price::text FROM pos_products p JOIN pos_price_list_items i ON i.product_id = p.id WHERE p.code = 'NASGOR'`).Scan(&name, &price))
	if name != "Nasi Goreng Special" || price != "90000.000" {
		t.Fatalf("updated: %s %s", name, price)
	}
	// one wrong row stops everything, and the wrong rows are listed
	bad := "code,name,category,charge_code,station,price_list,price\n" +
		"OK1,Fine,MAINS,RESTAURANT,KITCHEN,MAIN,1000\n" +
		"BAD1,No category,NOPE,RESTAURANT,,,\n" +
		"BAD2,Room code,MAINS,ROOM,,,\n" +
		"BAD3,Price alone,MAINS,RESTAURANT,,,5\n"
	res, err = f.svc.ImportProducts(f.admin, f.propID, bad, false)
	must(t, err)
	if len(res.Errors) != 3 || res.Created != 0 || f.Count(t, `SELECT count(*) FROM pos_products WHERE code = 'OK1'`) != 0 {
		t.Fatalf("all or nothing: %+v", res)
	}
	rows := map[int]string{}
	for _, e := range res.Errors {
		rows[e.Row] = e.Field
	}
	if rows[3] != "category" || rows[4] != "charge_code" || rows[5] != "price" {
		t.Fatalf("rows named: %+v", res.Errors)
	}
	_, err = f.svc.ImportProducts(f.admin, f.propID, "name,category\nA,B\n", false)
	wantCode(t, err, "VALIDATION_FAILED")
	if _, err := f.svc.ImportProducts(f.waiter, f.propID, csv, false); err == nil {
		t.Fatal("a waiter does not import")
	}
	_ = k
}

func TestTheSetupCheckSaysWhatAnOutletLacks(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "RESTO")
	issues, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	has := func(code string) bool {
		for _, i := range issues {
			if i.Code == code && i.Outlet == "RESTO" {
				return true
			}
		}
		return false
	}
	if !has("OUTLET_NO_PRODUCTS") || !has("OUTLET_NO_TABLES") || !has("OUTLET_NO_RECEIPT_PRINTER") {
		t.Fatalf("a new outlet lacks products, tables and a printer: %+v", issues)
	}
	food := f.chargeCode(t, "RESTAURANT")
	st, err := f.svc.CreateStation(f.admin, f.propID, pos.StationInput{Code: "KITCHEN", Name: "Kitchen", LateAfterMinutes: 15})
	must(t, err)
	p, err := f.svc.CreateProduct(f.admin, f.propID, pos.ProductInput{Code: "A", Name: "A", CategoryID: k.cat.ID, ChargeCodeID: food, StationID: &st.ID,
		Prices: []pos.ProductPriceIn{{PriceListID: k.priceList.ID, Price: dec("1000"), IsSold: true}}})
	must(t, err)
	_ = p
	issues, err = f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	found := false
	for _, i := range issues {
		if i.Code == "PRODUCT_STATION_NO_OUTPUT" && i.Product == "A" && i.Station == "KITCHEN" {
			found = true
		}
		if i.Code == "OUTLET_NO_PRODUCTS" {
			t.Fatalf("it has a product now: %+v", i)
		}
	}
	if !found {
		t.Fatalf("a PRINT outlet whose product's station has no printer: %+v", issues)
	}
	pr, err := f.svc.CreatePrinter(f.admin, f.propID, pos.PrinterInput{Name: "Kitchen printer", Kind: "KITCHEN", PaperWidthMM: 80, Codepage: "CP437", ConnectionInfo: "192.168.1.50:9100"})
	must(t, err)
	_, err = f.svc.UpdateStation(f.admin, f.propID, st.ID, pos.StationInput{Name: "Kitchen", PrinterID: &pr.ID, LateAfterMinutes: 15})
	must(t, err)
	issues, _ = f.svc.SetupCheck(f.admin, f.propID)
	for _, i := range issues {
		if strings.HasPrefix(i.Code, "PRODUCT_") {
			t.Fatalf("fixed: %+v", i)
		}
	}
	_ = o
	if _, err := f.svc.SetupCheck(f.waiter, f.propID); err == nil {
		t.Fatal("the check is for who manages")
	}
}

func TestEveryReadAndWriteAsksForItsPermission(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	o := f.outlet(t, k, "RESTO")
	for name, call := range map[string]func(ctx context.Context) error{
		"list outlets":  func(c context.Context) error { _, err := f.svc.ListOutlets(c, f.propID, nil); return err },
		"list products": func(c context.Context) error { _, err := f.svc.ListProducts(c, f.propID, nil, nil); return err },
		"floor plan":    func(c context.Context) error { _, err := f.svc.GetFloorPlan(c, f.propID, o.ID); return err },
		"list printers": func(c context.Context) error { _, err := f.svc.ListPrinters(c, f.propID); return err },
		"profiles":      func(c context.Context) error { _, err := f.svc.ListRuleProfiles(c, f.propID); return err },
	} {
		if err := call(f.nobody); err == nil {
			t.Errorf("%s: a user with no POS permission read it", name)
		}
		if err := call(f.waiter); err != nil {
			t.Errorf("%s: a waiter reads it: %v", name, err)
		}
	}
	// the front desk, which needs the outlets for the lock of a folio, reads the outlets and nothing else
	fd := f.User(t, f.tenantID, f.propID, auth.PermFolioOutletChargeOpen)
	if _, err := f.svc.ListOutlets(fd, f.propID, nil); err != nil {
		t.Fatalf("the front desk reads the outlets: %v", err)
	}
	if _, err := f.svc.ListProducts(fd, f.propID, nil, nil); err == nil {
		t.Fatal("but not the catalogue")
	}
	// another tenant's property is not found
	other := f.Tenant(t, "OTH")
	stranger := roomstest.Admin(other.ID)
	_, err := f.svc.ListOutlets(stranger, f.propID, nil)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// The rule is of the order mode of the outlet: a PRINT outlet needs a printer on the station and a KDS outlet needs the screen, whatever else the station has. The check says which and why.
func TestTheSetupCheckNamesTheReasonAStationDoesNotReachTheKitchen(t *testing.T) {
	f := setup(t)
	k := f.kit(t)
	f.outlet(t, k, "RESTO")
	_, err := f.svc.CreateOutlet(f.admin, f.propID, pos.OutletInput{Code: "BARK", Name: "Bar", OutletType: "BAR", OrderMode: "KDS", DepartmentID: f.dept, RuleProfileID: k.profile.ID,
		PriceListID: k.priceList.ID, CompTreatment: "PAYMENT_COMP"})
	must(t, err)
	food := f.chargeCode(t, "RESTAURANT")
	st, err := f.svc.CreateStation(f.admin, f.propID, pos.StationInput{Code: "BAR", Name: "Bar", KDSEnabled: true, LateAfterMinutes: 10})
	must(t, err)
	_, err = f.svc.CreateProduct(f.admin, f.propID, pos.ProductInput{Code: "TEA", Name: "Tea", CategoryID: k.cat.ID, ChargeCodeID: food, StationID: &st.ID,
		Prices: []pos.ProductPriceIn{{PriceListID: k.priceList.ID, Price: dec("1000"), IsSold: true}}})
	must(t, err)
	find := func(outlet string) *pos.SetupIssue {
		issues, err := f.svc.SetupCheck(f.admin, f.propID)
		must(t, err)
		for i := range issues {
			if strings.HasPrefix(issues[i].Code, "PRODUCT_") && issues[i].Outlet == outlet {
				return &issues[i]
			}
		}
		return nil
	}
	// a station with a screen and no printer: fine for the KDS outlet, not for the PRINT outlet, and the issue says the station has a screen
	if i := find("BARK"); i != nil {
		t.Fatalf("a KDS outlet is served by a station with a screen: %+v", i)
	}
	i := find("RESTO")
	if i == nil || i.Reason != "NO_PRINTER" || i.OrderMode != "PRINT" || i.OtherOutput != "KDS" || i.Station != "BAR" {
		t.Fatalf("a PRINT outlet needs a printer, the station has only a screen: %+v", i)
	}
	// the other way round: a printer and no screen
	pr, err := f.svc.CreatePrinter(f.admin, f.propID, pos.PrinterInput{Name: "Bar printer", Kind: "KITCHEN", PaperWidthMM: 80, Codepage: "CP437", ConnectionInfo: "192.168.1.60:9100"})
	must(t, err)
	_, err = f.svc.UpdateStation(f.admin, f.propID, st.ID, pos.StationInput{Name: "Bar", PrinterID: &pr.ID, KDSEnabled: false, LateAfterMinutes: 10})
	must(t, err)
	if i := find("RESTO"); i != nil {
		t.Fatalf("a PRINT outlet is served by a station with a printer: %+v", i)
	}
	i = find("BARK")
	if i == nil || i.Reason != "NO_KDS" || i.OrderMode != "KDS" || i.OtherOutput != "PRINTER" {
		t.Fatalf("a KDS outlet needs the screen, the station has only a printer: %+v", i)
	}
}
