package assets_test

import (
	"testing"

	"kamarapms/internal/assets"
)

// KD-23: the switch of the screen sends only is_active, as AssetLocationPatch allows (the name is optional). The location keeps its name; a name that is sent is still checked.
func TestALocationIsSwitchedOffWithoutItsName(t *testing.T) {
	f := newFx(t)
	loc := f.location(t, "KOLAM")
	off, on := false, true
	got, err := f.svc.UpdateLocation(f.admin, f.propID, loc.ID, assets.LocationPatch{IsActive: &off})
	if err != nil {
		t.Fatalf("switch off without a name: %v", err)
	}
	if got.IsActive || got.Name != loc.Name {
		t.Fatalf("switched off: %+v", got)
	}
	if got, err = f.svc.UpdateLocation(f.admin, f.propID, loc.ID, assets.LocationPatch{IsActive: &on}); err != nil || !got.IsActive || got.Name != loc.Name {
		t.Fatalf("switch on: %v %+v", err, got)
	}
	name := "Kolam renang"
	if got, err = f.svc.UpdateLocation(f.admin, f.propID, loc.ID, assets.LocationPatch{Name: &name}); err != nil || got.Name != name || !got.IsActive {
		t.Fatalf("rename: %v %+v", err, got)
	}
	blank := "  "
	_, err = f.svc.UpdateLocation(f.admin, f.propID, loc.ID, assets.LocationPatch{Name: &blank})
	wantCode(t, err, "VALIDATION_FAILED")
}
