package assets_test

import (
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/auth"
)

func TestUpdateFiscalGroup(t *testing.T) {
	f := newFx(t)
	groups, err := f.svc.ListFiscalGroups(f.admin, f.propID)
	must(t, err)
	if len(groups) != 6 {
		t.Fatalf("%d fiscal groups, want 6", len(groups))
	}

	g, err := f.svc.UpdateFiscalGroup(f.admin, f.propID, "G1", assets.FiscalGroupInput{LifeYears: 4, SLRate: dec("25"), DBRate: ptr(dec("45"))})
	must(t, err)
	if !g.DBRate.Equal(dec("45")) {
		t.Fatalf("db rate %v", g.DBRate)
	}
	groups, err = f.svc.ListFiscalGroups(f.admin, f.propID)
	must(t, err)
	for _, x := range groups {
		if x.Code == "G1" && (x.DBRate == nil || !x.DBRate.Equal(dec("45"))) {
			t.Fatalf("the change was not kept: %+v", x)
		}
	}

	// a building has the straight line only; the rates must make sense
	_, err = f.svc.UpdateFiscalGroup(f.admin, f.propID, "BLD_PERMANENT", assets.FiscalGroupInput{LifeYears: 20, SLRate: dec("5"), DBRate: ptr(dec("10"))})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.UpdateFiscalGroup(f.admin, f.propID, "G2", assets.FiscalGroupInput{LifeYears: 8, SLRate: dec("0")})
	wantCode(t, err, "VALIDATION_FAILED")

	// asset.setup is the permission; reading is not enough
	_, err = f.svc.UpdateFiscalGroup(f.user(t, auth.PermAssetRead), f.propID, "G1", assets.FiscalGroupInput{LifeYears: 4, SLRate: dec("25")})
	wantCode(t, err, "PERMISSION_DENIED")
}
