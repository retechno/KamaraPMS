package assets_test

import (
	"testing"

	"kamarapms/internal/assets"
)

// KD-24: the start of the register may move while the register holds no asset (REGISTER_FROM_LOCKED only with assets). The settings say which, so the screen locks on the same rule.
func TestTheSettingsSayWhetherTheRegisterHoldsAssets(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	s, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if !s.Started || s.HasAssets {
		t.Fatalf("started, empty: %+v", s)
	}
	// an empty register: the month may still move (here to the month after the opening balances below)
	moved, err := f.svc.UpdateSettings(f.admin, f.propID, assets.SettingsInput{RegisterFrom: ptr(d("2026-10-01")), CapitalizationThreshold: s.CapitalizationThreshold, StartRule: s.StartRule,
		OEPolicy: s.OEPolicy, OEDefaultMonths: s.OEDefaultMonths, TaxYearStartMonth: s.TaxYearStartMonth, Version: s.Version})
	must(t, err)
	if moved.RegisterFrom.String() != "2026-10-01" || moved.HasAssets {
		t.Fatalf("moved: %+v", moved)
	}
	_, err = f.importRows(t, "2026-09-30", true, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	s, err = f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if !s.HasAssets {
		t.Fatalf("an asset is in the register: %+v", s)
	}
	_, err = f.svc.UpdateSettings(f.admin, f.propID, assets.SettingsInput{RegisterFrom: ptr(d("2026-11-01")), CapitalizationThreshold: s.CapitalizationThreshold, StartRule: s.StartRule,
		OEPolicy: s.OEPolicy, OEDefaultMonths: s.OEDefaultMonths, TaxYearStartMonth: s.TaxYearStartMonth, Version: s.Version})
	wantCode(t, err, "REGISTER_FROM_LOCKED")
}
