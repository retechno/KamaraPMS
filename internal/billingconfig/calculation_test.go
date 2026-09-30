package billingconfig_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/auth"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func (f fixture) mapRoomRules(t *testing.T, propertyID int64, codeName string, onService bool) billingconfig.ChargeCode {
	t.Helper()
	ctx := f.admin
	svc, err := f.Billing.CreateServiceCharge(ctx, propertyID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service 10%", Rate: "10", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	vat, err := f.Billing.CreateTax(ctx, propertyID, billingconfig.TaxInput{Code: "VAT", Name: "VAT 11%", Rate: "11", TaxOnService: onService, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	list, err := f.Billing.ListChargeCodes(ctx, propertyID, 0, billingconfig.ChargeCodeFilter{}, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.Code == codeName {
			out, err := f.Billing.ReplaceRules(ctx, propertyID, c.ID, billingconfig.RulesInput{
				Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("no code %s", codeName)
	return billingconfig.ChargeCode{}
}

func TestCalculateUsesTheChargeCodeRules(t *testing.T) {
	f := newFixture(t)
	room := f.mapRoomRules(t, f.bali, "ROOM", true)

	calc := func(ctx context.Context, req billingconfig.ChargeRequest) (chargecalc.Breakdown, error) {
		req.PropertyID = f.bali
		return f.Billing.Calculate(ctx, req)
	}
	// ROOM is EXCLUSIVE: 1,000,000 + 10% service + 11% VAT on net and service = 1,221,000.
	b, err := calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("1000000")})
	if err != nil || !b.TotalAmount.Equal(dec("1221000")) || b.PriceMode != chargecalc.Exclusive || !b.ServiceTotal.Equal(dec("100000")) || !b.TaxTotal.Equal(dec("121000")) {
		t.Fatalf("exclusive: %v %+v", err, b)
	}
	if len(b.ServiceComponents) != 1 || b.ServiceComponents[0].Code != "SVC" || len(b.TaxComponents) != 1 || b.TaxComponents[0].Code != "VAT" {
		t.Fatalf("components: %+v", b)
	}
	// A price-mode override: the same code read as a gross price.
	incl := chargecalc.Inclusive
	b, err = calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("1110000"), PriceMode: &incl})
	if err != nil || !b.TotalAmount.Equal(dec("1110000")) || !b.NetAmount.Equal(dec("909091")) || !b.ServiceTotal.Equal(dec("90909")) {
		t.Fatalf("inclusive override: %v %+v", err, b)
	}
	// Discount, quantity and a credit.
	b, err = calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: room.ID, Quantity: dec("2"), UnitPrice: dec("500000"), Discount: dec("100000")})
	if err != nil || !b.TotalAmount.Equal(dec("1098900")) {
		t.Fatalf("discount: %v %+v", err, b)
	}
	b, err = calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("-1000000")})
	if err != nil || !b.TotalAmount.Equal(dec("-1221000")) {
		t.Fatalf("credit: %v %+v", err, b)
	}
	// Exempt code: no rules.
	var exempt int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM_EXEMPT'`, f.bali).Scan(&exempt); err != nil {
		t.Fatal(err)
	}
	b, err = calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: exempt, Quantity: dec("1"), UnitPrice: dec("1000000")})
	if err != nil || !b.TotalAmount.Equal(dec("1000000")) || len(b.TaxComponents) != 0 {
		t.Fatalf("exempt: %v %+v", err, b)
	}
	// Rule changes apply to the next calculation, nothing is cached.
	if _, err := f.Billing.ReplaceRules(f.admin, f.bali, room.ID, billingconfig.RulesInput{}); err != nil {
		t.Fatal(err)
	}
	b, _ = calc(f.admin, billingconfig.ChargeRequest{ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("1000000")})
	if !b.TotalAmount.Equal(dec("1000000")) {
		t.Fatalf("after removing the rules: %+v", b)
	}
}

func TestCalculateUsesThePropertyCurrencyPrecision(t *testing.T) {
	f := newFixture(t)
	usd := f.PropertyIn(t, f.tenantID, "NYC", "USD", 2)
	room := f.mapRoomRules(t, usd.ID, "ROOM", true)
	incl := chargecalc.Inclusive
	// The documented residual example: inclusive 7.00 USD, service 10%, VAT 11% on service.
	b, err := f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: usd.ID, ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("7.00"), PriceMode: &incl})
	if err != nil || !b.TotalAmount.Equal(dec("7.00")) || !b.RoundingAdjustment.Equal(dec("0.01")) || !b.NetAmount.Equal(dec("5.74")) || b.Decimals != 2 {
		t.Fatalf("USD residual: %v %+v", err, b)
	}
	// The same discount of 0.50 is invalid in IDR (no decimals) but fine in USD.
	if _, err := f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: usd.ID, ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("10"), Discount: dec("0.50")}); err != nil {
		t.Fatalf("USD discount: %v", err)
	}
	idrRoom := f.codeByName(t, "ROOM")
	_, err = f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: f.bali, ChargeCodeID: idrRoom.ID, Quantity: dec("1"), UnitPrice: dec("10"), Discount: dec("0.50")})
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Field != "discount_amount" {
		t.Fatalf("IDR discount: %v", fe)
	}
}

func TestCalculateValidationAndScoping(t *testing.T) {
	f := newFixture(t)
	room := f.codeByName(t, "ROOM")
	req := billingconfig.ChargeRequest{PropertyID: f.bali, ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("1000")}

	bad := req
	bad.Quantity = dec("0")
	_, err := f.Billing.Calculate(f.admin, bad)
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Field != "quantity" {
		t.Fatalf("zero quantity: %v", fe)
	}
	bad = req
	bad.Discount = dec("2000")
	_, err = f.Billing.Calculate(f.admin, bad)
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Field != "discount_amount" {
		t.Fatalf("discount above the amount: %v", fe)
	}
	unknown := chargecalc.PriceMode("MIXED")
	bad = req
	bad.PriceMode = &unknown
	_, err = f.Billing.Calculate(f.admin, bad)
	wantCode(t, err, "VALIDATION_FAILED")

	// An inactive code cannot be charged.
	off := false
	if _, err := f.Billing.UpdateChargeCode(f.admin, f.bali, room.ID, billingconfig.ChargeCodePatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.Calculate(f.admin, req)
	wantCode(t, err, "CHARGE_CODE_INACTIVE")

	// Another property's code, unknown codes, and other tenants' properties.
	jkt := f.Property(t, f.tenantID, "JKT")
	var jktRoom int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, jkt.ID).Scan(&jktRoom); err != nil {
		t.Fatal(err)
	}
	other := req
	other.ChargeCodeID = jktRoom
	_, err = f.Billing.Calculate(f.admin, other)
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	other.ChargeCodeID = 999999
	_, err = f.Billing.Calculate(f.admin, other)
	wantCode(t, err, "CHARGE_CODE_NOT_FOUND")
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	foreign := req
	foreign.PropertyID = sg.ID
	_, err = f.Billing.Calculate(f.admin, foreign)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Billing.Calculate(context.Background(), req)
	wantCode(t, err, "UNAUTHENTICATED")

	// Previewing needs property access only, not billing_config.manage.
	if err := f.Exec(t, `UPDATE charge_codes SET is_active = true WHERE id = $1`, room.ID); err != nil {
		t.Fatal(err)
	}
	reader := f.User(t, f.tenantID, f.bali)
	if _, err := f.Billing.Calculate(reader, req); err != nil {
		t.Fatalf("reader: %v", err)
	}
	noGrant := f.User(t, f.tenantID, jkt.ID, auth.PermBillingConfigManage)
	_, err = f.Billing.Calculate(noGrant, req)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'charge_calculation%'`); n != 0 {
		t.Fatalf("a preview writes nothing: %d audit entries", n)
	}
}

// A three-decimal currency (KWD, BHD) keeps its third decimal end to end: the default price is stored and
// returned with three decimals, and the calculation rounds at three.
func TestThreeDecimalCurrency(t *testing.T) {
	f := newFixture(t)
	kwd := f.PropertyIn(t, f.tenantID, "KWT", "KWD", 3)
	cc, err := f.Billing.CreateChargeCode(f.admin, kwd.ID, billingconfig.ChargeCodeInput{
		Code: "SPA", Name: "Spa", ChargeType: "SERVICE", PriceMode: "EXCLUSIVE", DefaultUnitPrice: "12.345", IsActive: true,
	})
	if err != nil || cc.DefaultUnitPrice == nil || *cc.DefaultUnitPrice != "12.345" {
		t.Fatalf("three-decimal default price: %v %+v", err, cc)
	}
	if _, err := f.Billing.CreateChargeCode(f.admin, kwd.ID, billingconfig.ChargeCodeInput{
		Code: "X", Name: "x", ChargeType: "OTHER", PriceMode: "EXCLUSIVE", DefaultUnitPrice: "1.2345", IsActive: true,
	}); err == nil {
		t.Fatal("four decimals")
	}
	if list, _ := f.Billing.ListChargeCodes(f.admin, kwd.ID, 0, billingconfig.ChargeCodeFilter{}, 200); func() bool {
		for _, c := range list {
			if c.Code == "SPA" {
				return *c.DefaultUnitPrice == "12.345"
			}
		}
		return false
	}() == false {
		t.Fatal("the list formats amounts with the property's decimals")
	}
	room := f.mapRoomRules(t, kwd.ID, "ROOM", true)
	incl := chargecalc.Inclusive
	// 10.000 inclusive with 10% service and 11% VAT on service: net0 = 10 / 1.221 = 8.190 (3 decimals).
	b, err := f.Billing.Calculate(f.admin, billingconfig.ChargeRequest{PropertyID: kwd.ID, ChargeCodeID: room.ID, Quantity: dec("1"), UnitPrice: dec("10.000"), PriceMode: &incl})
	if err != nil || b.Decimals != 3 || !b.TotalAmount.Equal(dec("10.000")) || !b.NetAmount.Add(b.ServiceTotal).Add(b.TaxTotal).Equal(dec("10.000")) {
		t.Fatalf("KWD calculation: %v %+v", err, b)
	}
	for _, a := range []decimal.Decimal{b.NetAmount, b.ServiceTotal, b.TaxTotal, b.TaxableAmount} {
		if !a.Equal(a.Round(3)) {
			t.Fatalf("amount %s has more than three decimals", a)
		}
	}
	// net0 = round(10 / 1.221, 3) = 8.190; service 0.819; VAT on 9.009 = 0.991; no residual.
	if !b.NetAmount.Equal(dec("8.190")) || !b.ServiceTotal.Equal(dec("0.819")) || !b.TaxTotal.Equal(dec("0.991")) || !b.RoundingAdjustment.IsZero() {
		t.Fatalf("KWD amounts: %+v", b)
	}
}
