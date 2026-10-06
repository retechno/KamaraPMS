package rooms_test

import (
	"context"
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func bedTypeByCode(t *testing.T, list []rooms.BedType, code string) rooms.BedType {
	t.Helper()
	for _, b := range list {
		if b.Code == code {
			return b
		}
	}
	t.Fatalf("no bed type %s in %+v", code, list)
	return rooms.BedType{}
}

func TestBedTypeCatalogue(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)

	// A new property starts with the standard catalogue, in its sort order.
	list, err := e.Rooms.ListBedTypes(ctx, p.ID, nil)
	if err != nil || len(list) != 5 || list[0].Code != "KING" || list[4].Code != "SINGLE" {
		t.Fatalf("seed: %v %+v", err, list)
	}
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bed_types.seeded'`); n != 1 {
		t.Fatalf("seed audit entries: %d", n)
	}

	// Create: the code is upper-cased, unique per property, and checked.
	sofa, err := e.Rooms.CreateBedType(ctx, p.ID, rooms.BedTypeInput{Code: " sofa ", Name: "Sofa bed", SortOrder: 60, IsActive: true})
	if err != nil || sofa.Code != "SOFA" || !sofa.IsActive {
		t.Fatalf("create: %v %+v", err, sofa)
	}
	_, err = e.Rooms.CreateBedType(ctx, p.ID, rooms.BedTypeInput{Code: "KING", Name: "Again", IsActive: true})
	wantCode(t, err, "CODE_TAKEN")
	_, err = e.Rooms.CreateBedType(ctx, p.ID, rooms.BedTypeInput{Code: "bad code", Name: ""})
	fields := map[string]bool{}
	for _, f := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[f.Field] = true
	}
	if !fields["code"] || !fields["name"] {
		t.Fatalf("field errors: %v", fields)
	}

	// Edit: the code is fixed; a name, an order and the active flag change.
	name := "Sofa bed (pull-out)"
	upd, err := e.Rooms.UpdateBedType(ctx, p.ID, sofa.ID, rooms.BedTypePatch{Name: &name, SortOrder: ptr(int32(5))})
	if err != nil || upd.Name != name || upd.Code != "SOFA" || upd.SortOrder != 5 {
		t.Fatalf("update: %v %+v", err, upd)
	}
	if list, _ := e.Rooms.ListBedTypes(ctx, p.ID, nil); list[0].Code != "SOFA" {
		t.Fatalf("sort order is not followed: %+v", list)
	}
	_, err = e.Rooms.UpdateBedType(ctx, p.ID, 999999, rooms.BedTypePatch{Name: &name})
	wantCode(t, err, "BED_TYPE_NOT_FOUND")
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'bed_type'`); n != 2 {
		t.Fatalf("audit entries: %d, want created + updated", n)
	}

	// The catalogue belongs to its property.
	other := e.Property(t, tn.ID, "JKT")
	otherList, err := e.Rooms.ListBedTypes(ctx, other.ID, nil)
	if err != nil || len(otherList) != 5 {
		t.Fatalf("other property catalogue: %v %d", err, len(otherList))
	}
	_, err = e.Rooms.UpdateBedType(ctx, other.ID, sofa.ID, rooms.BedTypePatch{Name: &name})
	wantCode(t, err, "BED_TYPE_NOT_FOUND")

	// Only room.manage may change it; reading needs access to the property.
	clerk := e.User(t, tn.ID, p.ID, auth.PermReservationRead)
	if _, err := e.Rooms.ListBedTypes(clerk, p.ID, nil); err != nil {
		t.Fatalf("a reader lists: %v", err)
	}
	_, err = e.Rooms.CreateBedType(clerk, p.ID, rooms.BedTypeInput{Code: "X", Name: "X", IsActive: true})
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestRoomBedType(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	other := e.Property(t, tn.ID, "JKT")
	ctx := roomstest.Admin(tn.ID)
	list, _ := e.Rooms.ListBedTypes(ctx, p.ID, nil)
	king, twin := bedTypeByCode(t, list, "KING"), bedTypeByCode(t, list, "TWIN")
	otherKing := bedTypeByCode(t, mustBeds(t, e, ctx, other.ID), "KING")
	rt := e.RoomType(t, ctx, p.ID, "DLX")

	// A room is created with a bed type; without one it is refused.
	with, err := e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: rt.ID, RoomNumber: "201", BedTypeID: &king.ID, IsActive: true}})
	if err != nil || with.BedTypeID != king.ID {
		t.Fatalf("create with: %v %+v", err, with)
	}
	_, err = e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: rt.ID, RoomNumber: "299", IsActive: true}})
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Field != "bed_type_id" || c.Fields[0].Code != "REQUIRED" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	without := e.Room(t, ctx, p.ID, rt.ID, "202") // the test setup gives the first bed type

	// A bed type of another property is refused, whether on create or on update.
	_, err = e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: rt.ID, RoomNumber: "203", BedTypeID: &otherKing.ID, IsActive: true}})
	wantCode(t, err, "BED_TYPE_NOT_FOUND")
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, without.ID, rooms.RoomPatch{BedTypeID: &otherKing.ID})
	wantCode(t, err, "BED_TYPE_NOT_FOUND")

	// Update sets it and keeps it when it is not mentioned; 0 is refused.
	set, err := e.Rooms.UpdateRoom(ctx, p.ID, without.ID, rooms.RoomPatch{BedTypeID: &twin.ID})
	if err != nil || set.BedTypeID != twin.ID {
		t.Fatalf("set: %v %+v", err, set)
	}
	kept, err := e.Rooms.UpdateRoom(ctx, p.ID, without.ID, rooms.RoomPatch{Floor: ptr("2")})
	if err != nil || kept.BedTypeID != twin.ID {
		t.Fatalf("kept: %v %+v", err, kept)
	}
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, without.ID, rooms.RoomPatch{BedTypeID: ptr(int64(0))}) // a room always has one
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Field != "bed_type_id" {
		t.Fatalf("zero: %+v", c.Fields)
	}

	// A bed type that is switched off is not offered for a new choice, but the rooms that have it keep it.
	if _, err := e.Rooms.UpdateBedType(ctx, p.ID, king.ID, rooms.BedTypePatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, without.ID, rooms.RoomPatch{BedTypeID: &king.ID})
	if c := code(t, err, "VALIDATION_FAILED"); len(c.Fields) != 1 || c.Fields[0].Code != "BED_TYPE_INACTIVE" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	still, err := e.Rooms.UpdateRoom(ctx, p.ID, with.ID, rooms.RoomPatch{Floor: ptr("2")})
	if err != nil || still.BedTypeID != king.ID {
		t.Fatalf("a room keeps its inactive bed type: %v %+v", err, still)
	}
	if active, _ := e.Rooms.ListBedTypes(ctx, p.ID, ptr(true)); len(active) != 4 {
		t.Fatalf("active filter: %d", len(active))
	}
}

func mustBeds(t *testing.T, e *roomstest.Env, ctx context.Context, propertyID int64) []rooms.BedType {
	t.Helper()
	list, err := e.Rooms.ListBedTypes(ctx, propertyID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return list
}
