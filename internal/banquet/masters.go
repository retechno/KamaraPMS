package banquet

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// ---------------------------------------------------------------- settings

// GetSettings answers the settings of the property: the defaults when it has none.
func (s *Service) GetSettings(ctx context.Context, propertyID int64) (Settings, error) {
	if _, err := s.canRead(ctx, propertyID, masterReaders...); err != nil {
		return Settings{}, err
	}
	return s.settings(ctx, propertyID)
}

func (s *Service) settings(ctx context.Context, propertyID int64) (Settings, error) {
	r, err := s.q(ctx).GetSettings(ctx, propertyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Settings{TentativeConflictMode: ModeReject}, nil
		}
		return Settings{}, err
	}
	return Settings{TentativeConflictMode: r.TentativeConflictMode, OperatingFrom: r.OperatingFrom, OperatingTo: r.OperatingTo}, nil
}

// PutSettings changes the settings (banquet.setup). The conflict mode applies to the bookings and moves that come after it, never to what exists; every change is audited.
func (s *Service) PutSettings(ctx context.Context, propertyID int64, in SettingsInput) (Settings, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Settings{}, err
	}
	var f []apperr.FieldError
	if in.TentativeConflictMode != ModeReject && in.TentativeConflictMode != ModeQueue {
		f = append(f, fieldErr("tentative_conflict_mode", "INVALID_VALUE", "REJECT or QUEUE"))
	}
	if (in.OperatingFrom == nil) != (in.OperatingTo == nil) {
		f = append(f, fieldErr("operating_to", "INVALID_VALUE", "the hours of operation have a start and an end, or neither"))
	} else if in.OperatingFrom != nil && !in.OperatingFrom.Before(*in.OperatingTo) {
		f = append(f, fieldErr("operating_to", "INVALID_VALUE", "the end is after the start"))
	}
	if len(f) > 0 {
		return Settings{}, invalid("settings", f...)
	}
	var out Settings
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		old, err := s.settings(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := s.q(ctx).UpsertSettings(ctx, banquetdb.UpsertSettingsParams{PropertyID: propertyID, TenantID: p.TenantID, TentativeConflictMode: in.TentativeConflictMode,
			OperatingFrom: in.OperatingFrom, OperatingTo: in.OperatingTo, ActorID: p.ActorID()}); err != nil {
			return err
		}
		out = Settings(in)
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.settings_changed", "banquet_settings", propertyID, "settings", old, out))
	})
	return out, err
}

// ---------------------------------------------------------------- rooms

func (s *Service) rooms(ctx context.Context, tenantID, propertyID int64, id *int64, activeOnly *bool) ([]Room, error) {
	q := s.q(ctx)
	rows, err := q.ListRooms(ctx, banquetdb.ListRoomsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	parts, err := q.ListRoomParts(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	setups, err := q.ListRoomSetups(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]Room, len(rows))
	for i, r := range rows {
		out[i] = Room{ID: r.ID, Code: r.Code, Name: r.Name, AreaM2: r.AreaM2, SetupMinutes: int(r.SetupMinutes), TeardownMinutes: int(r.TeardownMinutes), IsCombo: r.IsCombo,
			IsActive: r.IsActive, SortOrder: int(r.SortOrder), Version: r.Version, Parts: []RoomPart{}, Setups: []RoomSetup{}}
		for _, p := range parts {
			if p.ComboRoomID == r.ID {
				out[i].Parts = append(out[i].Parts, RoomPart{RoomID: p.PartRoomID, Code: p.PartCode, Name: p.PartName})
			}
		}
		for _, st := range setups {
			if st.RoomID == r.ID {
				out[i].Setups = append(out[i].Setups, RoomSetup{SetupStyle: st.SetupStyle, Capacity: int(st.Capacity)})
			}
		}
	}
	return out, nil
}

// ListRooms answers the meeting rooms with their parts and capacities.
func (s *Service) ListRooms(ctx context.Context, propertyID int64, activeOnly *bool) ([]Room, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	return s.rooms(ctx, p.TenantID, propertyID, nil, activeOnly)
}

// GetRoom answers one meeting room.
func (s *Service) GetRoom(ctx context.Context, propertyID, id int64) (Room, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return Room{}, err
	}
	list, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
	if err != nil {
		return Room{}, err
	}
	if len(list) == 0 {
		return Room{}, notFound("BANQUET_ROOM_NOT_FOUND", "meeting room")
	}
	return list[0], nil
}

func checkRoom(in RoomInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	f = append(f, checkSort(in.SortOrder)...)
	if in.SetupMinutes < 0 || in.SetupMinutes > 1440 {
		f = append(f, fieldErr("setup_minutes", "INVALID_VALUE", "0 to 1440 minutes"))
	}
	if in.TeardownMinutes < 0 || in.TeardownMinutes > 1440 {
		f = append(f, fieldErr("teardown_minutes", "INVALID_VALUE", "0 to 1440 minutes"))
	}
	if in.AreaM2 != nil && (!in.AreaM2.IsPositive() || in.AreaM2.Exponent() < -1 || in.AreaM2.GreaterThan(decimal.NewFromInt(1_000_000))) {
		f = append(f, fieldErr("area_m2", "INVALID_VALUE", "above zero, one decimal"))
	}
	return f
}

// CreateRoom adds a meeting room (banquet.setup).
func (s *Service) CreateRoom(ctx context.Context, propertyID int64, in RoomInput) (Room, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Room{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := checkRoom(in, true); len(f) > 0 {
		return Room{}, invalid("meeting room", f...)
	}
	var out Room
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertRoom(ctx, banquetdb.InsertRoomParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, AreaM2: in.AreaM2,
			SetupMinutes: int32(in.SetupMinutes), TeardownMinutes: int32(in.TeardownMinutes), IsCombo: in.IsCombo, IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		list, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.room_created", "banquet_room", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateRoom changes a meeting room (banquet.setup). A room that has parts stays combined and a part of a combined room stays a base room (the database refuses the change).
func (s *Service) UpdateRoom(ctx context.Context, propertyID, id int64, in RoomInput) (Room, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Room{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkRoom(in, false); len(f) > 0 {
		return Room{}, invalid("meeting room", f...)
	}
	var out Room
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.BanquetRooms, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return err
		}
		old, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(old) == 0 {
			return notFound("BANQUET_ROOM_NOT_FOUND", "meeting room")
		}
		n, err := s.q(ctx).UpdateRoom(ctx, banquetdb.UpdateRoomParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, AreaM2: in.AreaM2,
			SetupMinutes: int32(in.SetupMinutes), TeardownMinutes: int32(in.TeardownMinutes), IsCombo: in.IsCombo, IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.Conflict("VERSION_CONFLICT", "the meeting room was changed by someone else").WithContext("current_version", old[0].Version)
		}
		list, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.room_updated", "banquet_room", id, out.Code, old[0], out))
	})
	return out, err
}

// SetRoomParts sets the base rooms of a combined room (banquet.setup): two or more, none combined itself.
func (s *Service) SetRoomParts(ctx context.Context, propertyID, id int64, in PartsInput) (Room, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Room{}, err
	}
	ids := slices.Clone(in.RoomIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var out Room
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.BanquetRooms, db.ForUpdate, propertyID, append(slices.Clone(ids), id)); err != nil {
			return err
		}
		old, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(old) == 0 {
			return notFound("BANQUET_ROOM_NOT_FOUND", "meeting room")
		}
		if !old[0].IsCombo {
			return invalidCode("ROOM_COMBINATION_INVALID", "room_ids", "only a combined room has parts")
		}
		if len(ids) < 2 {
			return invalidCode("ROOM_COMBINATION_INVALID", "room_ids", "a combined room is made of two or more base rooms")
		}
		q := s.q(ctx)
		// a combined room that a live hold uses keeps its parts: changing them would leave a booking on rooms it does not cover
		if used, err := q.RoomUsedByLiveHolds(ctx, banquetdb.RoomUsedByLiveHoldsParams{PropertyID: propertyID, RoomID: id}); err != nil {
			return err
		} else if used {
			return apperr.Conflict("BANQUET_ROOM_IN_USE", "the room is held by an event: its parts cannot change").WithContext("room_id", id)
		}
		if err := q.DeleteRoomParts(ctx, banquetdb.DeleteRoomPartsParams{PropertyID: propertyID, ComboRoomID: id}); err != nil {
			return err
		}
		for _, part := range ids {
			if err := q.InsertRoomPart(ctx, banquetdb.InsertRoomPartParams{PropertyID: propertyID, ComboRoomID: id, PartRoomID: part}); err != nil {
				return err
			}
		}
		if err := q.BumpRoom(ctx, banquetdb.BumpRoomParams{PropertyID: propertyID, ID: id, ActorID: p.ActorID()}); err != nil {
			return err
		}
		list, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.room_parts_set", "banquet_room", id, out.Code, old[0].Parts, out.Parts))
	})
	return out, err
}

// SetRoomSetups sets the capacity of a room in each style of set-up it offers (banquet.setup).
func (s *Service) SetRoomSetups(ctx context.Context, propertyID, id int64, in SetupsInput) (Room, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Room{}, err
	}
	seen := map[string]bool{}
	var f []apperr.FieldError
	for _, st := range in.Setups {
		if !slices.Contains(SetupStyles, st.SetupStyle) {
			f = append(f, fieldErr("setups", "INVALID_VALUE", "an unknown style of set-up: "+st.SetupStyle))
		}
		if st.Capacity < 1 || st.Capacity > maxPax {
			f = append(f, fieldErr("setups", "INVALID_VALUE", "a capacity is above zero"))
		}
		if seen[st.SetupStyle] {
			f = append(f, fieldErr("setups", "DUPLICATE", "a style is listed once"))
		}
		seen[st.SetupStyle] = true
	}
	if len(f) > 0 {
		return Room{}, invalid("set-ups", f...)
	}
	var out Room
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.BanquetRooms, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return err
		}
		old, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(old) == 0 {
			return notFound("BANQUET_ROOM_NOT_FOUND", "meeting room")
		}
		q := s.q(ctx)
		if err := q.DeleteRoomSetups(ctx, banquetdb.DeleteRoomSetupsParams{PropertyID: propertyID, RoomID: id}); err != nil {
			return err
		}
		for _, st := range in.Setups {
			if err := q.InsertRoomSetup(ctx, banquetdb.InsertRoomSetupParams{PropertyID: propertyID, RoomID: id, SetupStyle: st.SetupStyle, Capacity: int32(st.Capacity)}); err != nil { //nolint:gosec // G115: validated
				return err
			}
		}
		if err := q.BumpRoom(ctx, banquetdb.BumpRoomParams{PropertyID: propertyID, ID: id, ActorID: p.ActorID()}); err != nil {
			return err
		}
		list, err := s.rooms(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.room_setups_set", "banquet_room", id, out.Code, old[0].Setups, out.Setups))
	})
	return out, err
}

// ---------------------------------------------------------------- event types and reasons

// ListEventTypes answers the kinds of event.
func (s *Service) ListEventTypes(ctx context.Context, propertyID int64, activeOnly *bool) ([]EventType, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListEventTypes(ctx, banquetdb.ListEventTypesParams{TenantID: p.TenantID, PropertyID: propertyID, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]EventType, len(rows))
	for i, r := range rows {
		out[i] = EventType{ID: r.ID, Code: r.Code, Name: r.Name, IsActive: r.IsActive}
	}
	return out, nil
}

// CreateEventType adds a kind of event (banquet.setup).
func (s *Service) CreateEventType(ctx context.Context, propertyID int64, in EventTypeInput) (EventType, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return EventType{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := append(checkCode("code", in.Code), checkName("name", in.Name)...); len(f) > 0 {
		return EventType{}, invalid("event type", f...)
	}
	var out EventType
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertEventType(ctx, banquetdb.InsertEventTypeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		out = EventType{ID: id, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_type_created", "banquet_event_type", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateEventType renames or switches off a kind of event (banquet.setup).
func (s *Service) UpdateEventType(ctx context.Context, propertyID, id int64, in EventTypeInput) (EventType, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return EventType{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkName("name", in.Name); len(f) > 0 {
		return EventType{}, invalid("event type", f...)
	}
	var out EventType
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		n, err := s.q(ctx).UpdateEventType(ctx, banquetdb.UpdateEventTypeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("EVENT_TYPE_NOT_FOUND", "event type")
		}
		out = EventType{ID: id, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_type_updated", "banquet_event_type", id, in.Name, nil, out))
	})
	return out, err
}

// ListReasons answers the reasons for losing or cancelling an event.
func (s *Service) ListReasons(ctx context.Context, propertyID int64, kind *string, activeOnly *bool) ([]Reason, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListReasons(ctx, banquetdb.ListReasonsParams{TenantID: p.TenantID, PropertyID: propertyID, Kind: kind, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]Reason, len(rows))
	for i, r := range rows {
		out[i] = Reason{ID: r.ID, Kind: r.Kind, Code: r.Code, Name: r.Name, IsActive: r.IsActive}
	}
	return out, nil
}

// CreateReason adds a reason (banquet.setup).
func (s *Service) CreateReason(ctx context.Context, propertyID int64, in ReasonInput) (Reason, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Reason{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	f := append(checkCode("code", in.Code), checkName("name", in.Name)...)
	if in.Kind != ReasonLost && in.Kind != ReasonCancel {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "LOST or CANCEL"))
	}
	if len(f) > 0 {
		return Reason{}, invalid("reason", f...)
	}
	var out Reason
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		id, err := s.q(ctx).InsertReason(ctx, banquetdb.InsertReasonParams{TenantID: p.TenantID, PropertyID: propertyID, Kind: in.Kind, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		out = Reason{ID: id, Kind: in.Kind, Code: in.Code, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.reason_created", "banquet_reason", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateReason renames or switches off a reason (banquet.setup).
func (s *Service) UpdateReason(ctx context.Context, propertyID, id int64, in ReasonInput) (Reason, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Reason{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkName("name", in.Name); len(f) > 0 {
		return Reason{}, invalid("reason", f...)
	}
	var out Reason
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		n, err := s.q(ctx).UpdateReason(ctx, banquetdb.UpdateReasonParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("REASON_NOT_FOUND", "reason")
		}
		out = Reason{ID: id, Name: in.Name, IsActive: active(in.IsActive)}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.reason_updated", "banquet_reason", id, in.Name, nil, out))
	})
	return out, err
}

// ---------------------------------------------------------------- items and packages

func (s *Service) items(ctx context.Context, tenantID, propertyID int64, id *int64, activeOnly *bool) ([]Item, error) {
	rows, err := s.q(ctx).ListItems(ctx, banquetdb.ListItemsParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]Item, len(rows))
	for i, r := range rows {
		out[i] = Item{ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, UnitBasis: r.UnitBasis, ChargeCodeID: r.ChargeCodeID, ChargeCode: r.ChargeCode, ChargeCodeName: r.ChargeCodeName,
			ListPrice: r.ListPrice, FbClass: r.FbClass, PosProductID: r.PosProductID, Description: r.Description, IsActive: r.IsActive}
	}
	return out, nil
}

// ListItems answers the items that can be sold to an event.
func (s *Service) ListItems(ctx context.Context, propertyID int64, activeOnly *bool) ([]Item, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	return s.items(ctx, p.TenantID, propertyID, nil, activeOnly)
}

// decimals is the currency precision of the property: prices have no more decimals than the currency.
func (s *Service) decimals(ctx context.Context, tenantID, propertyID int64) (int32, error) {
	var d int32
	err := s.txm.DB(ctx).QueryRow(ctx, "SELECT currency_decimals FROM properties WHERE tenant_id = $1 AND id = $2", tenantID, propertyID).Scan(&d)
	return d, err
}

func checkPrice(f string, v decimal.Decimal, decimals int32) []apperr.FieldError {
	switch {
	case v.IsNegative():
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "not below zero")}
	case v.Exponent() < -decimals:
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "more decimals than the currency has")}
	case v.GreaterThan(decimal.NewFromInt(1_000_000_000_000)):
		return []apperr.FieldError{fieldErr(f, "INVALID_VALUE", "too large")}
	}
	return nil
}

func checkItem(in ItemInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	if !slices.Contains(itemKinds, in.Kind) {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "an unknown kind of item"))
	}
	if !slices.Contains(unitBasis, in.UnitBasis) {
		f = append(f, fieldErr("unit_basis", "INVALID_VALUE", "an unknown basis of the price"))
	}
	if in.FbClass != nil && !slices.Contains(fbClasses, *in.FbClass) {
		f = append(f, fieldErr("fb_class", "INVALID_VALUE", "FOOD, BEVERAGE or OTHER"))
	}
	if in.ChargeCodeID < 1 {
		f = append(f, fieldErr("charge_code_id", "REQUIRED", "the charge code the item is posted to"))
	}
	return f
}

func (s *Service) checkItemRefs(ctx context.Context, tenantID, propertyID int64, in ItemInput) error {
	dec, err := s.decimals(ctx, tenantID, propertyID)
	if err != nil {
		return err
	}
	if f := checkPrice("list_price", in.ListPrice, dec); len(f) > 0 {
		return invalid("item", f...)
	}
	act, err := s.q(ctx).ChargeCodeActive(ctx, banquetdb.ChargeCodeActiveParams{TenantID: tenantID, PropertyID: propertyID, ID: in.ChargeCodeID})
	if err != nil {
		return orNotFound(err, invalid("item", fieldErr("charge_code_id", "CHARGE_CODE_NOT_FOUND", "no such charge code in this property")))
	}
	if !act {
		return invalid("item", fieldErr("charge_code_id", "CHARGE_CODE_INACTIVE", "the charge code is switched off"))
	}
	return nil
}

// CreateItem adds an item (banquet.setup). The price is the list price; an event line freezes its own copy in PR 2.
func (s *Service) CreateItem(ctx context.Context, propertyID int64, in ItemInput) (Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Item{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := checkItem(in, true); len(f) > 0 {
		return Item{}, invalid("item", f...)
	}
	var out Item
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := s.checkItemRefs(ctx, p.TenantID, propertyID, in); err != nil {
			return err
		}
		id, err := s.q(ctx).InsertItem(ctx, banquetdb.InsertItemParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Kind: in.Kind, UnitBasis: in.UnitBasis,
			ChargeCodeID: in.ChargeCodeID, ListPrice: in.ListPrice, FbClass: in.FbClass, PosProductID: in.PosProductID, Description: in.Description, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		list, err := s.items(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.item_created", "banquet_item", id, in.Code, nil, out))
	})
	return out, err
}

// UpdateItem changes an item (banquet.setup).
func (s *Service) UpdateItem(ctx context.Context, propertyID, id int64, in ItemInput) (Item, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Item{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkItem(in, false); len(f) > 0 {
		return Item{}, invalid("item", f...)
	}
	var out Item
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		old, err := s.items(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(old) == 0 {
			return notFound("ITEM_NOT_FOUND", "item")
		}
		if err := s.checkItemRefs(ctx, p.TenantID, propertyID, in); err != nil {
			return err
		}
		if _, err := s.q(ctx).UpdateItem(ctx, banquetdb.UpdateItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, Kind: in.Kind, UnitBasis: in.UnitBasis,
			ChargeCodeID: in.ChargeCodeID, ListPrice: in.ListPrice, FbClass: in.FbClass, PosProductID: in.PosProductID, Description: in.Description, IsActive: active(in.IsActive)}); err != nil {
			return err
		}
		list, err := s.items(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.item_updated", "banquet_item", id, out.Code, old[0], out))
	})
	return out, err
}

func (s *Service) packages(ctx context.Context, tenantID, propertyID int64, id *int64, activeOnly *bool) ([]Package, error) {
	q := s.q(ctx)
	rows, err := q.ListPackages(ctx, banquetdb.ListPackagesParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	comps, err := q.ListPackageComponents(ctx, banquetdb.ListPackageComponentsParams{PropertyID: propertyID, PackageID: id})
	if err != nil {
		return nil, err
	}
	out := make([]Package, len(rows))
	for i, r := range rows {
		out[i] = Package{ID: r.ID, Code: r.Code, Name: r.Name, PricePerPax: r.PricePerPax, Description: r.Description, IsActive: r.IsActive, Components: []PackageComponent{}}
		for _, c := range comps {
			if c.PackageID == r.ID {
				out[i].Components = append(out[i].Components, PackageComponent{ItemID: c.ItemID, ItemCode: c.ItemCode, ItemName: c.ItemName, ItemKind: c.ItemKind, QtyPerPax: c.QtyPerPax, SortOrder: int(c.SortOrder)})
			}
		}
	}
	return out, nil
}

// ListPackages answers the packages with their components.
func (s *Service) ListPackages(ctx context.Context, propertyID int64, activeOnly *bool) ([]Package, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	return s.packages(ctx, p.TenantID, propertyID, nil, activeOnly)
}

// GetPackage answers one package.
func (s *Service) GetPackage(ctx context.Context, propertyID, id int64) (Package, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return Package{}, err
	}
	list, err := s.packages(ctx, p.TenantID, propertyID, &id, nil)
	if err != nil {
		return Package{}, err
	}
	if len(list) == 0 {
		return Package{}, notFound("PACKAGE_NOT_FOUND", "package")
	}
	return list[0], nil
}

func checkPackage(in PackageInput, create bool) []apperr.FieldError {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	seen := map[int64]bool{}
	for _, c := range in.Components {
		if c.ItemID < 1 || !c.QtyPerPax.IsPositive() || c.QtyPerPax.Exponent() < -3 || c.QtyPerPax.GreaterThan(decimal.NewFromInt(100000)) {
			f = append(f, fieldErr("components", "INVALID_VALUE", "an item and a quantity for each guest above zero, up to 3 decimals"))
		}
		if seen[c.ItemID] {
			f = append(f, fieldErr("components", "DUPLICATE", "an item is listed once"))
		}
		seen[c.ItemID] = true
	}
	return f
}

func (s *Service) savePackage(ctx context.Context, propertyID, id int64, in PackageInput) error {
	q := s.q(ctx)
	if err := q.DeletePackageComponents(ctx, banquetdb.DeletePackageComponentsParams{PropertyID: propertyID, PackageID: id}); err != nil {
		return err
	}
	for i, c := range in.Components {
		if err := q.InsertPackageComponent(ctx, banquetdb.InsertPackageComponentParams{PropertyID: propertyID, PackageID: id, ItemID: c.ItemID, QtyPerPax: c.QtyPerPax, SortOrder: int32(i + 1)}); err != nil { //nolint:gosec // G115: bounded by the request size
			return err
		}
	}
	return nil
}

// CreatePackage adds a package (banquet.setup).
func (s *Service) CreatePackage(ctx context.Context, propertyID int64, in PackageInput) (Package, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Package{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	if f := checkPackage(in, true); len(f) > 0 {
		return Package{}, invalid("package", f...)
	}
	var out Package
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if f := checkPrice("price_per_pax", in.PricePerPax, dec); len(f) > 0 {
			return invalid("package", f...)
		}
		id, err := s.q(ctx).InsertPackage(ctx, banquetdb.InsertPackageParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, PricePerPax: in.PricePerPax,
			Description: in.Description, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		if err := s.savePackage(ctx, propertyID, id, in); err != nil {
			return err
		}
		list, err := s.packages(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.package_created", "banquet_package", id, in.Code, nil, out))
	})
	return out, err
}

// UpdatePackage changes a package; the components replace the ones it has (banquet.setup).
func (s *Service) UpdatePackage(ctx context.Context, propertyID, id int64, in PackageInput) (Package, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return Package{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkPackage(in, false); len(f) > 0 {
		return Package{}, invalid("package", f...)
	}
	var out Package
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		old, err := s.packages(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		if len(old) == 0 {
			return notFound("PACKAGE_NOT_FOUND", "package")
		}
		dec, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if f := checkPrice("price_per_pax", in.PricePerPax, dec); len(f) > 0 {
			return invalid("package", f...)
		}
		if _, err := s.q(ctx).UpdatePackage(ctx, banquetdb.UpdatePackageParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, PricePerPax: in.PricePerPax,
			Description: in.Description, IsActive: active(in.IsActive)}); err != nil {
			return err
		}
		if err := s.savePackage(ctx, propertyID, id, in); err != nil {
			return err
		}
		list, err := s.packages(ctx, p.TenantID, propertyID, &id, nil)
		if err != nil {
			return err
		}
		out = list[0]
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.package_updated", "banquet_package", id, out.Code, old[0], out))
	})
	return out, err
}
