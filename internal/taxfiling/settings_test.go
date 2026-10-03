package taxfiling_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxfiling"
)

func hasField(t *testing.T, err error, field string) {
	t.Helper()
	e := roomstest.Code(t, err, "VALIDATION_FAILED")
	for _, fe := range e.Fields {
		if fe.Field == field {
			return
		}
	}
	t.Errorf("no error on %s: %+v", field, e.Fields)
}

func TestSettingsHistory(t *testing.T) {
	f := setup(t) // business date 30 Sep 2026
	v, err := f.Tax.Settings(f.admin, f.propID)
	must(t, err)
	if len(v.History) != 1 || v.Current.IsPKP || v.Current.InputVATTreatment != "EXPENSE" {
		t.Fatalf("a property starts as not PKP with input VAT as an expense: %+v", v)
	}

	// Validation: PKP needs the NPWP, a property that is not PKP cannot claim input VAT, the treatment is one of three.
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), IsPKP: true})
	hasField(t, err, "npwp")
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), InputVATTreatment: "CREDITABLE"})
	hasField(t, err, "input_vat_treatment")
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), InputVATTreatment: "SOMETIMES"})
	hasField(t, err, "input_vat_treatment")
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{})
	hasField(t, err, "effective_from")

	// A change that begins before the business date needs an approval.
	back := taxfiling.SettingsInput{EffectiveFrom: d("2026-09-15"), IsPKP: true, NPWP: "01.234.567.8-901.000", InputVATTreatment: "DEFERRED", SignerName: "A. Owner"}
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, back)
	wantCode(t, err, "APPROVAL_REQUIRED")
	back.Approval = &iam.ApprovalInput{Email: f.email, Password: "wrong-password"}
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, back)
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	back.Approval = f.approval()
	v, err = f.Tax.ChangeSettings(f.admin, f.propID, back)
	must(t, err)
	if !v.Current.IsPKP || v.Current.InputVATTreatment != "DEFERRED" || v.Current.ApprovedBy == nil || v.Current.SignerName != "A. Owner" || len(v.History) != 2 {
		t.Fatalf("a backdated change is in force: %+v", v)
	}

	// The history only moves forward.
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-09-15"), IsPKP: true, NPWP: "x", Approval: f.approval()})
	wantCode(t, err, "TAX_SETTINGS_NOT_NEWER")
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-09-10"), Approval: f.approval()})
	wantCode(t, err, "TAX_SETTINGS_NOT_NEWER")

	// A change in the future needs no approval and is not in force yet; the default treatment follows the PKP status.
	v, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), IsPKP: true, NPWP: "01.234.567.8-901.000"})
	must(t, err)
	if v.Current.InputVATTreatment != "DEFERRED" || len(v.History) != 3 || v.History[0].InputVATTreatment != "CREDITABLE" || v.History[0].ApprovedBy != nil {
		t.Fatalf("a future change: %+v", v)
	}
	if on, ok := taxfiling.SettingsOn(v.History, d("2026-10-01")); !ok || on.InputVATTreatment != "CREDITABLE" {
		t.Errorf("in force on 1 Oct: %+v", on)
	}
	if on, ok := taxfiling.SettingsOn(v.History, d("2026-09-30")); !ok || on.InputVATTreatment != "DEFERRED" {
		t.Errorf("in force on 30 Sep: %+v", on)
	}
	if on, _ := taxfiling.SettingsOn(v.History, d("2026-09-01")); on.IsPKP {
		t.Errorf("before the first change the property was not PKP: %+v", on)
	}

	// Leaving PKP: the default is an expense.
	v, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2027-01-01")})
	must(t, err)
	if v.History[0].IsPKP || v.History[0].InputVATTreatment != "EXPENSE" {
		t.Fatalf("not PKP again: %+v", v.History[0])
	}

	// The history is append-only in the database.
	_, err = f.Pool.Exec(context.Background(), `UPDATE property_tax_settings SET is_pkp = false WHERE property_id = $1`, f.propID)
	if err == nil {
		t.Error("an update of the history was accepted")
	}
	_, err = f.Pool.Exec(context.Background(), `DELETE FROM property_tax_settings WHERE property_id = $1`, f.propID)
	if err == nil {
		t.Error("a delete of the history was accepted")
	}
	// ... and the rules hold without the service.
	_, err = f.Pool.Exec(context.Background(), `INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, is_pkp, input_vat_treatment) VALUES ($1, $2, '2030-01-01', false, 'CREDITABLE')`, f.tenantID, f.propID)
	if err == nil {
		t.Error("a property that is not PKP claimed input VAT")
	}
}

func TestSettingsPermissionsAndTenants(t *testing.T) {
	f := setup(t)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermTaxView)
	if _, err := f.Tax.Settings(viewer, f.propID); err != nil {
		t.Fatalf("a viewer reads the status: %v", err)
	}
	_, err := f.Tax.ChangeSettings(viewer, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01")})
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermTaxFile)
	_, err = f.Tax.Settings(nobody, f.propID)
	wantCode(t, err, "PERMISSION_DENIED")

	other := f.Tenant(t, "XYZ")
	sg := f.Property(t, other.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, other.ID)
	_, err = f.Tax.Settings(otherAdmin, f.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if v, err := f.Tax.Settings(otherAdmin, sg.ID); err != nil || len(v.History) != 1 {
		t.Fatalf("a new property has its first row: %v %+v", err, v)
	}
}

// Two changes from the same date at once: one wins, the other is told the history moved.
func TestSettingsRace(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-11-01"), IsPKP: i == 0, NPWP: "01.234.567.8-901.000"})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if e := asApp(err); e == nil || e.Code != "TAX_SETTINGS_NOT_NEWER" {
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d changes won, want 1: %v", ok, errs)
	}
	v, err := f.Tax.Settings(f.admin, f.propID)
	must(t, err)
	if len(v.History) != 2 {
		t.Fatalf("history: %+v", v.History)
	}
}

func TestTaxKind(t *testing.T) {
	f := setup(t)
	if f.pb1.TaxKind != "LOCAL" {
		t.Fatalf("a tax is local by default: %+v", f.pb1)
	}
	vat, err := f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "PPN", Name: "VAT", Rate: "11", TaxKind: "vat", IsActive: true})
	must(t, err)
	if vat.TaxKind != "VAT" {
		t.Fatalf("kind: %+v", vat)
	}
	_, err = f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "ODD", Name: "Odd", Rate: "1", TaxKind: "SALES", IsActive: true})
	hasField(t, err, "tax_kind")
	up, err := f.Billing.UpdateTax(f.admin, f.propID, vat.ID, billingconfig.TaxPatch{TaxKind: ptr("OTHER")})
	must(t, err)
	if up.TaxKind != "OTHER" {
		t.Fatalf("patched: %+v", up)
	}
	up, err = f.Billing.UpdateTax(f.admin, f.propID, vat.ID, billingconfig.TaxPatch{Name: ptr("VAT 11%")})
	must(t, err)
	if up.TaxKind != "OTHER" {
		t.Fatalf("an untouched kind stays: %+v", up)
	}
	_, err = f.Billing.UpdateTax(f.admin, f.propID, vat.ID, billingconfig.TaxPatch{TaxKind: ptr("NONE")})
	hasField(t, err, "tax_kind")
}
