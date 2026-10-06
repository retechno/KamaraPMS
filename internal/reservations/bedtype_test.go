package reservations_test

import (
	"testing"

	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
)

func (f *fx) bed(t *testing.T, code string) rooms.BedType {
	t.Helper()
	list, err := f.Rooms.ListBedTypes(f.admin, f.propID, nil)
	must(t, err)
	for _, b := range list {
		if b.Code == code {
			return b
		}
	}
	t.Fatalf("no bed type %s", code)
	return rooms.BedType{}
}

// A line can ask for a bed type; the request is shown with its code, can be changed and taken off, and does not touch inventory.
func TestRequestedBedType(t *testing.T) {
	f := setup(t)
	king, twin := f.bed(t, "KING"), f.bed(t, "TWIN")

	in := f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID = &king.ID
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)
	line := res.Rooms[0]
	if line.BedTypeID == nil || *line.BedTypeID != king.ID || line.BedTypeCode != "KING" || line.BedTypeName != "King" {
		t.Fatalf("the request is shown: %+v", line)
	}
	if got := f.available(t, f.dlx, "2026-10-02"); got.Demand != 1 {
		t.Fatalf("a bed type request still counts one room of the type: %+v", got)
	}

	// A line without a request has no bed type in its view.
	plain := f.book(t, f.std, "2026-10-02", "2026-10-03")
	if plain.Rooms[0].BedTypeID != nil || plain.Rooms[0].BedTypeCode != "" {
		t.Fatalf("no request: %+v", plain.Rooms[0])
	}

	// Change it, keep it when it is not mentioned, take it off with 0.
	got, err := f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: res.Version, BedTypeID: &twin.ID})
	must(t, err)
	if got.Rooms[0].BedTypeCode != "TWIN" {
		t.Fatalf("changed: %+v", got.Rooms[0])
	}
	adults := 1
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Adults: &adults})
	must(t, err)
	if got.Rooms[0].BedTypeCode != "TWIN" {
		t.Fatalf("kept: %+v", got.Rooms[0])
	}
	zero := int64(0)
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, BedTypeID: &zero})
	must(t, err)
	if got.Rooms[0].BedTypeID != nil {
		t.Fatalf("taken off: %+v", got.Rooms[0])
	}

	// A room is added with a request too; the audit entry of an amendment names the bed type.
	added, err := f.Res.AddLine(f.admin, f.propID, res.ID, got.Version, func() reservations.LineInput {
		l := f.line(f.dlx, "2026-10-02", "2026-10-03")
		l.BedTypeID = &king.ID
		return l
	}())
	must(t, err)
	if len(added.Rooms) != 2 || added.Rooms[1].BedTypeCode != "KING" {
		t.Fatalf("added room: %+v", added.Rooms)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.room_amended' AND new_data ? 'requested_bed_type_id'`); n != 3 {
		t.Fatalf("audit entries that name the bed type: %d", n)
	}
}

func TestRequestedBedTypeValidation(t *testing.T) {
	f := setup(t)
	king := f.bed(t, "KING")

	// A bed type of another property does not exist for this one.
	other := f.Property(t, f.tenantID, "JKT")
	otherBeds, err := f.Rooms.ListBedTypes(f.admin, other.ID, nil)
	must(t, err)
	foreign := otherBeds[0].ID
	in := f.input(false, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID = &foreign
	_, err = f.Res.Create(f.admin, f.propID, "", in)
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Field != "rooms[0].bed_type_id" || c.Fields[0].Code != "BED_TYPE_NOT_FOUND" {
		t.Fatalf("fields: %+v", c.Fields)
	}

	// A bed type that is switched off cannot be asked for, but a request made before stays valid.
	in = f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID = &king.ID
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)
	_, err = f.Rooms.UpdateBedType(f.admin, f.propID, king.ID, rooms.BedTypePatch{IsActive: ptrBool(false)})
	must(t, err)
	adults := 1
	if _, err := f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: res.Version, Adults: &adults}); err != nil {
		t.Fatalf("a request that was on offer when it was made survives an amendment: %v", err)
	}
	in = f.input(false, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID = &king.ID
	_, err = f.Res.Create(f.admin, f.propID, "", in)
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Code != "BED_TYPE_INACTIVE" {
		t.Fatalf("fields: %+v", c.Fields)
	}
}

func ptrBool(b bool) *bool { return &b }

// A line can lock the bed it asks for: the lock needs a bed type, is kept when it is not mentioned and falls away with the request.
func TestBedLock(t *testing.T) {
	f := setup(t)
	king, twin := f.twinForRoom102(t) // a twin room exists, so a kept twin can be had

	// A lock without a bed type is refused, on create and on adding a room.
	bad := f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	bad.Rooms[0].BedLocked = true
	_, err := f.Res.Create(f.admin, f.propID, "", bad)
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Code != "BED_LOCK_NEEDS_BED_TYPE" {
		t.Fatalf("create: %+v", c.Fields)
	}

	in := f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID, in.Rooms[0].BedLocked = &king.ID, true
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)
	line := res.Rooms[0]
	if !line.BedLocked {
		t.Fatalf("the lock is shown: %+v", line)
	}

	// Changing the bed keeps the lock; unlocking keeps the request.
	got, err := f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: res.Version, BedTypeID: &twin.ID})
	must(t, err)
	if !got.Rooms[0].BedLocked || got.Rooms[0].BedTypeCode != "TWIN" {
		t.Fatalf("kept: %+v", got.Rooms[0])
	}
	off := false
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, BedLocked: &off})
	must(t, err)
	if got.Rooms[0].BedLocked || got.Rooms[0].BedTypeCode != "TWIN" {
		t.Fatalf("unlocked: %+v", got.Rooms[0])
	}

	// Taking the request off with a lock asked for is refused; without one it only clears the lock.
	on, zero := true, int64(0)
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, BedLocked: &on})
	must(t, err)
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, BedTypeID: &zero})
	must(t, err)
	if got.Rooms[0].BedLocked || got.Rooms[0].BedTypeID != nil {
		t.Fatalf("no request, no lock: %+v", got.Rooms[0])
	}
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, BedLocked: &on})
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Code != "BED_LOCK_NEEDS_BED_TYPE" {
		t.Fatalf("amend: %+v", c.Fields)
	}

	// A room is added with a locked bed.
	added, err := f.Res.AddLine(f.admin, f.propID, res.ID, got.Version, func() reservations.LineInput {
		l := f.line(f.dlx, "2026-10-02", "2026-10-03")
		l.BedTypeID, l.BedLocked = &king.ID, true
		return l
	}())
	must(t, err)
	if !added.Rooms[1].BedLocked {
		t.Fatalf("added room: %+v", added.Rooms[1])
	}
}
