package billingconfig_test

import (
	"testing"

	"kamarapms/internal/billingconfig"
)

// KD-15: the system code POS_ROUNDING has the charge type ROUNDING, which no person may choose. A change of its name, account or department sends the type it has, and must be accepted;
// changing the type stays refused, and no other code may take it.
func TestTheRoundingCodeCanBeChanged(t *testing.T) {
	f := newFixture(t)
	rounding := f.codeByName(t, "POS_ROUNDING")
	if rounding.ChargeType != "ROUNDING" || !rounding.IsSystem {
		t.Fatalf("POS_ROUNDING: %+v", rounding)
	}
	name, kind := "Pembulatan kasir", "ROUNDING"
	changed, err := f.Billing.UpdateChargeCode(f.admin, f.bali, rounding.ID, billingconfig.ChargeCodePatch{Name: &name, ChargeType: &kind})
	if err != nil {
		t.Fatalf("a change of the name with the type it has: %v", err)
	}
	if changed.Name != name || changed.ChargeType != "ROUNDING" {
		t.Fatalf("changed: %+v", changed)
	}
	off := false
	if _, err := f.Billing.UpdateChargeCode(f.admin, f.bali, rounding.ID, billingconfig.ChargeCodePatch{IsActive: &off}); err != nil {
		t.Fatalf("a patch without the type: %v", err)
	}

	fee := "FEE"
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, rounding.ID, billingconfig.ChargeCodePatch{ChargeType: &fee})
	wantCode(t, err, "SYSTEM_CHARGE_CODE_LOCKED")

	_, err = f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "ROUND2", Name: "Rounding", ChargeType: "ROUNDING", PriceMode: "EXCLUSIVE", IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED")
	other := f.codeByName(t, "OTHER")
	_, err = f.Billing.UpdateChargeCode(f.admin, f.bali, other.ID, billingconfig.ChargeCodePatch{ChargeType: &kind})
	wantCode(t, err, "VALIDATION_FAILED")
}
