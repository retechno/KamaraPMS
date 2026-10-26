-- Banquet, PR 1 (docs/architecture/36-banquet.md): masters, events, sessions, holds, notices. Every query is scoped by tenant and property.

-- ---------------------------------------------------------------- settings
-- name: GetSettings :one
SELECT s.tentative_conflict_mode, s.operating_from, s.operating_to, s.posting_mode, s.default_price_mode, s.max_discount_pct_no_approval, s.warehouse_id, w.name AS warehouse_name
FROM banquet_settings s LEFT JOIN inv_warehouses w ON w.property_id = s.property_id AND w.id = s.warehouse_id
WHERE s.property_id = @property_id;

-- name: UpsertSettings :exec
INSERT INTO banquet_settings (property_id, tenant_id, tentative_conflict_mode, operating_from, operating_to, posting_mode, default_price_mode, max_discount_pct_no_approval, warehouse_id, updated_by)
VALUES (@property_id, @tenant_id, @tentative_conflict_mode, sqlc.narg(operating_from), sqlc.narg(operating_to), @posting_mode, @default_price_mode, @max_discount_pct_no_approval, sqlc.narg(warehouse_id), sqlc.narg(actor_id))
ON CONFLICT (property_id) DO UPDATE
   SET tentative_conflict_mode = EXCLUDED.tentative_conflict_mode, operating_from = EXCLUDED.operating_from, operating_to = EXCLUDED.operating_to,
       posting_mode = EXCLUDED.posting_mode, default_price_mode = EXCLUDED.default_price_mode, max_discount_pct_no_approval = EXCLUDED.max_discount_pct_no_approval,
       warehouse_id = EXCLUDED.warehouse_id, updated_by = EXCLUDED.updated_by, updated_at = now();

-- ---------------------------------------------------------------- rooms
-- name: ListRooms :many
SELECT id, code, name, area_m2, setup_minutes, teardown_minutes, is_combo, is_active, sort_order, version
FROM banquet_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY sort_order, name, id;

-- name: ListRoomParts :many
SELECT p.combo_room_id, p.part_room_id, r.code AS part_code, r.name AS part_name
FROM banquet_room_parts p
JOIN banquet_rooms r ON r.property_id = p.property_id AND r.id = p.part_room_id
WHERE p.property_id = @property_id
ORDER BY p.combo_room_id, r.sort_order, r.id;

-- name: ListRoomSetups :many
SELECT room_id, setup_style, capacity
FROM banquet_room_setups
WHERE property_id = @property_id
ORDER BY room_id, setup_style;

-- name: InsertRoom :one
INSERT INTO banquet_rooms (tenant_id, property_id, code, name, area_m2, setup_minutes, teardown_minutes, is_combo, is_active, sort_order, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(area_m2), @setup_minutes, @teardown_minutes, @is_combo, @is_active, @sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateRoom :execrows
UPDATE banquet_rooms SET name = @name, area_m2 = sqlc.narg(area_m2), setup_minutes = @setup_minutes, teardown_minutes = @teardown_minutes, is_combo = @is_combo,
       is_active = @is_active, sort_order = @sort_order, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version;

-- name: RoomVersion :one
SELECT version FROM banquet_rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: BumpRoom :exec
UPDATE banquet_rooms SET version = version + 1, updated_by = sqlc.narg(actor_id) WHERE property_id = @property_id AND id = @id;

-- name: DeleteRoomParts :exec
DELETE FROM banquet_room_parts WHERE property_id = @property_id AND combo_room_id = @combo_room_id;

-- name: InsertRoomPart :exec
INSERT INTO banquet_room_parts (property_id, combo_room_id, part_room_id) VALUES (@property_id, @combo_room_id, @part_room_id);

-- name: DeleteRoomSetups :exec
DELETE FROM banquet_room_setups WHERE property_id = @property_id AND room_id = @room_id;

-- name: InsertRoomSetup :exec
INSERT INTO banquet_room_setups (property_id, room_id, setup_style, capacity) VALUES (@property_id, @room_id, @setup_style, @capacity);

-- name: RoomUsedByLiveHolds :one
SELECT EXISTS (SELECT 1 FROM banquet_holds WHERE property_id = @property_id AND atom_room_id = @room_id);

-- name: RoomCapacity :one
SELECT capacity FROM banquet_room_setups WHERE property_id = @property_id AND room_id = @room_id AND setup_style = @setup_style;

-- name: AtomRooms :many
SELECT p.part_room_id AS atom_room_id FROM banquet_room_parts p WHERE p.property_id = @property_id AND p.combo_room_id = @room_id
UNION ALL
SELECT r.id FROM banquet_rooms r WHERE r.property_id = @property_id AND r.id = @room_id AND NOT r.is_combo
ORDER BY 1;

-- ---------------------------------------------------------------- event types and reasons
-- name: ListEventTypes :many
SELECT id, code, name, is_active FROM banquet_event_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY name, id;

-- name: InsertEventType :one
INSERT INTO banquet_event_types (tenant_id, property_id, code, name, is_active) VALUES (@tenant_id, @property_id, @code, @name, @is_active) RETURNING id;

-- name: UpdateEventType :execrows
UPDATE banquet_event_types SET name = @name, is_active = @is_active WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListReasons :many
SELECT id, kind, code, name, is_active FROM banquet_reasons
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY kind, name, id;

-- name: InsertReason :one
INSERT INTO banquet_reasons (tenant_id, property_id, kind, code, name, is_active) VALUES (@tenant_id, @property_id, @kind, @code, @name, @is_active) RETURNING id;

-- name: UpdateReason :execrows
UPDATE banquet_reasons SET name = @name, is_active = @is_active WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetReason :one
SELECT kind, is_active FROM banquet_reasons WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- items and packages
-- name: ListItems :many
SELECT i.id, i.code, i.name, i.kind, i.unit_basis, i.charge_code_id, c.code AS charge_code, c.name AS charge_code_name, c.price_mode, i.list_price, i.fb_class, i.pos_product_id,
       i.description, i.is_active
FROM banquet_items i
JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR i.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR i.is_active = sqlc.narg(active)::boolean)
ORDER BY i.kind, i.name, i.id;

-- name: InsertItem :one
INSERT INTO banquet_items (tenant_id, property_id, code, name, kind, unit_basis, charge_code_id, list_price, fb_class, pos_product_id, description, is_active)
VALUES (@tenant_id, @property_id, @code, @name, @kind, @unit_basis, @charge_code_id, @list_price, sqlc.narg(fb_class), sqlc.narg(pos_product_id), sqlc.narg(description), @is_active)
RETURNING id;

-- name: UpdateItem :execrows
UPDATE banquet_items SET name = @name, kind = @kind, unit_basis = @unit_basis, charge_code_id = @charge_code_id, list_price = @list_price, fb_class = sqlc.narg(fb_class),
       pos_product_id = sqlc.narg(pos_product_id), description = sqlc.narg(description), is_active = @is_active
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListPackages :many
SELECT id, code, name, price_per_pax, description, is_active FROM banquet_packages
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY name, id;

-- name: ListPackageComponents :many
SELECT c.package_id, c.item_id, i.code AS item_code, i.name AS item_name, i.kind AS item_kind, cc.price_mode AS item_price_mode, c.qty_per_pax, c.sort_order
FROM banquet_package_components c
JOIN banquet_items i ON i.property_id = c.property_id AND i.id = c.item_id
JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
WHERE c.property_id = @property_id AND (sqlc.narg(package_id)::bigint IS NULL OR c.package_id = sqlc.narg(package_id)::bigint)
ORDER BY c.package_id, c.sort_order, c.item_id;

-- name: InsertPackage :one
INSERT INTO banquet_packages (tenant_id, property_id, code, name, price_per_pax, description, is_active)
VALUES (@tenant_id, @property_id, @code, @name, @price_per_pax, sqlc.narg(description), @is_active) RETURNING id;

-- name: UpdatePackage :execrows
UPDATE banquet_packages SET name = @name, price_per_pax = @price_per_pax, description = sqlc.narg(description), is_active = @is_active
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeletePackageComponents :exec
DELETE FROM banquet_package_components WHERE property_id = @property_id AND package_id = @package_id;

-- name: InsertPackageComponent :exec
INSERT INTO banquet_package_components (property_id, package_id, item_id, qty_per_pax, sort_order) VALUES (@property_id, @package_id, @item_id, @qty_per_pax, @sort_order);

-- name: ChargeCodeActive :one
SELECT is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- events
-- name: ListEvents :many
SELECT e.id, e.number, e.status, e.name, e.event_type_id, t.name AS event_type_name, e.booker_guest_id, e.booker_company_id,
       COALESCE(c.name, NULLIF(trim(concat_ws(' ', g.first_name, g.last_name)), ''), '')::text AS booker_name,
       e.sales_user_id, u.full_name AS sales_name, e.option_until, e.version,
       COALESCE(sa.first_date, '1900-01-01'::date)::date AS first_date, COALESCE(sa.last_date, '1900-01-01'::date)::date AS last_date,
       COALESCE(sa.sessions, 0)::int AS sessions, COALESCE(sa.pax, 0)::int AS pax_estimated, COALESCE(sa.pax_counted, 0)::int AS pax, COALESCE(sa.all_guaranteed, false)::boolean AS pax_guaranteed
FROM banquet_events e
LEFT JOIN banquet_event_types t ON t.property_id = e.property_id AND t.id = e.event_type_id
LEFT JOIN companies c ON c.property_id = e.property_id AND c.id = e.booker_company_id
LEFT JOIN guests g ON g.tenant_id = e.tenant_id AND g.id = e.booker_guest_id
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
LEFT JOIN LATERAL (SELECT min(s.session_date) AS first_date, max(s.session_date) AS last_date, count(*) AS sessions, sum(s.pax_estimated) AS pax, sum(COALESCE(s.pax_guaranteed, s.pax_estimated)) AS pax_counted, bool_and(s.pax_guaranteed IS NOT NULL) AS all_guaranteed
                     FROM banquet_sessions s WHERE s.property_id = e.property_id AND s.event_id = e.id AND s.status = 'ACTIVE') sa ON true
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR e.id = sqlc.narg(id)::bigint)
  AND (cardinality(@statuses::text[]) = 0 OR e.status = ANY(@statuses::text[]))
  AND (sqlc.narg(sales_user_id)::bigint IS NULL OR e.sales_user_id = sqlc.narg(sales_user_id)::bigint)
  AND (sqlc.narg(event_type_id)::bigint IS NULL OR e.event_type_id = sqlc.narg(event_type_id)::bigint)
  AND (sqlc.narg(date_from)::date IS NULL OR sa.last_date >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date IS NULL OR sa.first_date <= sqlc.narg(date_to)::date)
  AND (NOT @option_overdue::boolean OR (e.status = 'TENTATIVE' AND e.option_until < @today::date))
  AND (sqlc.narg(q)::text IS NULL OR e.number ILIKE '%' || sqlc.narg(q)::text || '%' OR e.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY COALESCE(sa.first_date, '2999-12-31'::date), e.id DESC
LIMIT @row_limit;

-- name: GetEvent :one
SELECT e.id, e.number, e.status, e.name, e.event_type_id, t.name AS event_type_name, e.booker_guest_id, e.booker_company_id,
       COALESCE(c.name, NULLIF(trim(concat_ws(' ', g.first_name, g.last_name)), ''), '')::text AS booker_name,
       e.contact_name, e.contact_phone, e.contact_email, e.sales_user_id, u.full_name AS sales_name, e.option_until, e.reason_id, r.name AS reason_name, e.reason_note,
       e.booking_group_id, bg.name AS booking_group_name, e.notes, e.inquiry_at, e.definite_at, e.closed_at, e.version
FROM banquet_events e
LEFT JOIN banquet_event_types t ON t.property_id = e.property_id AND t.id = e.event_type_id
LEFT JOIN companies c ON c.property_id = e.property_id AND c.id = e.booker_company_id
LEFT JOIN guests g ON g.tenant_id = e.tenant_id AND g.id = e.booker_guest_id
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
LEFT JOIN banquet_reasons r ON r.property_id = e.property_id AND r.id = e.reason_id
LEFT JOIN booking_groups bg ON bg.property_id = e.property_id AND bg.id = e.booking_group_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.id = @id;

-- name: EventHeader :one
SELECT status, booking_group_id, version FROM banquet_events WHERE property_id = @property_id AND id = @id;

-- name: InsertEvent :one
INSERT INTO banquet_events (tenant_id, property_id, number, status, name, event_type_id, booker_guest_id, booker_company_id, contact_name, contact_phone, contact_email,
                            sales_user_id, booking_group_id, notes, created_by, updated_by)
VALUES (@tenant_id, @property_id, @number, 'INQUIRY', @name, sqlc.narg(event_type_id), sqlc.narg(booker_guest_id), sqlc.narg(booker_company_id), sqlc.narg(contact_name),
        sqlc.narg(contact_phone), sqlc.narg(contact_email), @sales_user_id, sqlc.narg(booking_group_id), sqlc.narg(notes), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateEvent :execrows
UPDATE banquet_events SET name = @name, event_type_id = sqlc.narg(event_type_id), booker_guest_id = sqlc.narg(booker_guest_id), booker_company_id = sqlc.narg(booker_company_id),
       contact_name = sqlc.narg(contact_name), contact_phone = sqlc.narg(contact_phone), contact_email = sqlc.narg(contact_email), sales_user_id = @sales_user_id,
       booking_group_id = sqlc.narg(booking_group_id), notes = sqlc.narg(notes), option_until = sqlc.narg(option_until), version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version;

-- name: SetEventStatus :exec
UPDATE banquet_events SET status = @status, option_until = sqlc.narg(option_until), reason_id = sqlc.narg(reason_id), reason_note = sqlc.narg(reason_note),
       definite_at = COALESCE(sqlc.narg(definite_at), definite_at), closed_at = sqlc.narg(closed_at), version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- sessions
-- name: ListSessions :many
SELECT s.id, s.event_id, s.session_no, s.name, s.session_date, s.start_time, s.end_time, s.ends_next_day, s.room_id, r.code AS room_code, r.name AS room_name,
       s.setup_style, s.pax_estimated, s.pax_guaranteed, s.pax_actual, s.status, s.notes
FROM banquet_sessions s
LEFT JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.event_id = @event_id
  AND (sqlc.narg(id)::bigint IS NULL OR s.id = sqlc.narg(id)::bigint)
ORDER BY s.session_date, s.start_time NULLS LAST, s.session_no;

-- name: NextSessionNo :one
SELECT (COALESCE(max(session_no), 0) + 1)::int FROM banquet_sessions WHERE property_id = @property_id AND event_id = @event_id;

-- name: InsertSession :one
INSERT INTO banquet_sessions (tenant_id, property_id, event_id, session_no, name, session_date, start_time, end_time, ends_next_day, room_id, setup_style,
                              pax_estimated, pax_guaranteed, notes)
VALUES (@tenant_id, @property_id, @event_id, @session_no, @name, @session_date, sqlc.narg(start_time), sqlc.narg(end_time), @ends_next_day, sqlc.narg(room_id),
        sqlc.narg(setup_style), @pax_estimated, sqlc.narg(pax_guaranteed), sqlc.narg(notes))
RETURNING id;

-- name: UpdateSession :execrows
UPDATE banquet_sessions SET name = @name, session_date = @session_date, start_time = sqlc.narg(start_time), end_time = sqlc.narg(end_time), ends_next_day = @ends_next_day,
       room_id = sqlc.narg(room_id), setup_style = sqlc.narg(setup_style), pax_estimated = @pax_estimated, pax_guaranteed = sqlc.narg(pax_guaranteed), notes = sqlc.narg(notes)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND event_id = @event_id AND id = @id AND status = 'ACTIVE';

-- name: CancelSession :execrows
UPDATE banquet_sessions SET status = 'CANCELLED' WHERE tenant_id = @tenant_id AND property_id = @property_id AND event_id = @event_id AND id = @id AND status = 'ACTIVE';

-- ---------------------------------------------------------------- holds
-- name: InsertSessionHolds :exec
-- The hold of a session: one row for each base room of its room, from the start of the set-up to the end of the tear-down of the room that is booked (36 4.2).
INSERT INTO banquet_holds (tenant_id, property_id, session_id, event_id, atom_room_id, during, hold_kind)
SELECT s.tenant_id, s.property_id, s.id, s.event_id, a.atom_room_id,
       tsrange(s.session_date + s.start_time - r.setup_minutes * interval '1 minute',
               s.session_date + s.end_time + CASE WHEN s.ends_next_day THEN interval '1 day' ELSE interval '0' END + r.teardown_minutes * interval '1 minute', '[)'),
       @hold_kind::text
FROM banquet_sessions s
JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
CROSS JOIN LATERAL (SELECT p.part_room_id AS atom_room_id FROM banquet_room_parts p WHERE p.property_id = s.property_id AND p.combo_room_id = r.id
                    UNION ALL
                    SELECT r.id WHERE NOT r.is_combo) a
WHERE s.property_id = @property_id AND s.id = @session_id AND s.status = 'ACTIVE' AND s.start_time IS NOT NULL;

-- name: DeleteSessionHolds :exec
DELETE FROM banquet_holds WHERE property_id = @property_id AND session_id = @session_id;

-- name: DeleteEventHolds :exec
DELETE FROM banquet_holds WHERE property_id = @property_id AND event_id = @event_id;

-- name: SetEventHoldKind :exec
UPDATE banquet_holds SET hold_kind = @hold_kind WHERE property_id = @property_id AND event_id = @event_id;

-- name: EventAtomRooms :many
-- The base rooms an event holds or would hold: the rooms of its active sessions, expanded.
SELECT DISTINCT a.atom_room_id::bigint AS atom_room_id
FROM banquet_sessions s
JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
CROSS JOIN LATERAL (SELECT p.part_room_id AS atom_room_id FROM banquet_room_parts p WHERE p.property_id = s.property_id AND p.combo_room_id = r.id
                    UNION ALL
                    SELECT r.id WHERE NOT r.is_combo) a
WHERE s.property_id = @property_id AND s.event_id = @event_id AND s.status = 'ACTIVE'
ORDER BY 1;

-- name: EventsBehind :many
-- The other events that hold one of the given base rooms at a time that overlaps a hold of the event (the queue behind it, or the ones it queues behind).
SELECT DISTINCT g.event_id::bigint AS event_id
FROM banquet_holds h
JOIN banquet_holds g ON g.property_id = h.property_id AND g.atom_room_id = h.atom_room_id AND g.during && h.during AND g.event_id <> h.event_id
WHERE h.property_id = @property_id AND h.event_id = @event_id
ORDER BY 1;

-- name: ConflictingHolds :many
-- The live holds that a session at this room and time would meet: the holds of the base rooms it covers that overlap its range, except the ones of the session itself.
SELECT g.id AS hold_id, g.session_id, g.event_id, e.number AS event_number, e.name AS event_name, e.status AS event_status, g.hold_kind, g.atom_room_id, br.code AS atom_room_code,
       s.name AS session_name
FROM banquet_rooms r
CROSS JOIN LATERAL (SELECT p.part_room_id AS atom_room_id FROM banquet_room_parts p WHERE p.property_id = r.property_id AND p.combo_room_id = r.id
                    UNION ALL
                    SELECT r.id WHERE NOT r.is_combo) a
CROSS JOIN LATERAL (SELECT tsrange(sqlc.arg(session_date)::date + sqlc.arg(start_time)::time - r.setup_minutes * interval '1 minute',
                                   sqlc.arg(session_date)::date + sqlc.arg(end_time)::time + CASE WHEN sqlc.arg(ends_next_day)::boolean THEN interval '1 day' ELSE interval '0' END + r.teardown_minutes * interval '1 minute', '[)') AS rng) x
JOIN banquet_holds g ON g.property_id = r.property_id AND g.atom_room_id = a.atom_room_id AND g.during && x.rng AND g.session_id <> sqlc.arg(exclude_session_id)::bigint
JOIN banquet_events e ON e.property_id = g.property_id AND e.id = g.event_id
JOIN banquet_sessions s ON s.property_id = g.property_id AND s.id = g.session_id
JOIN banquet_rooms br ON br.property_id = g.property_id AND br.id = g.atom_room_id
WHERE r.property_id = sqlc.arg(property_id) AND r.id = sqlc.arg(room_id)
ORDER BY g.id;

-- name: SessionRanks :many
-- The rank of a session in the queue of options: 1 and the number of sessions of other events with a live hold that is older than one of its holds and overlaps it on a base room.
-- room_taken: an option whose slot a definite hold of another event covers.
SELECT s.id::bigint AS session_id,
       (1 + COALESCE(c.n, 0))::int AS rank,
       (COALESCE(t.taken, false))::boolean AS room_taken
FROM unnest(@ids::bigint[]) AS s(id)
LEFT JOIN LATERAL (SELECT count(DISTINCT g.session_id) AS n
                     FROM banquet_holds h
                     JOIN banquet_holds g ON g.property_id = h.property_id AND g.atom_room_id = h.atom_room_id AND g.during && h.during AND g.id < h.id AND g.event_id <> h.event_id
                    WHERE h.property_id = @property_id AND h.session_id = s.id) c ON true
LEFT JOIN LATERAL (SELECT true AS taken
                     FROM banquet_holds h
                     JOIN banquet_holds g ON g.property_id = h.property_id AND g.atom_room_id = h.atom_room_id AND g.during && h.during AND g.event_id <> h.event_id AND g.hold_kind = 'DEFINITE'
                    WHERE h.property_id = @property_id AND h.session_id = s.id AND h.hold_kind = 'OPTION' LIMIT 1) t ON true;

-- name: EarlierHolders :many
-- Who holds the slot of a session ahead of it (the holders named when an option cannot become definite).
SELECT DISTINCT g.event_id::bigint AS event_id, e.number AS event_number, e.name AS event_name, e.status AS event_status
FROM banquet_holds h
JOIN banquet_holds g ON g.property_id = h.property_id AND g.atom_room_id = h.atom_room_id AND g.during && h.during AND g.id < h.id AND g.event_id <> h.event_id
JOIN banquet_events e ON e.property_id = g.property_id AND e.id = g.event_id
WHERE h.property_id = @property_id AND h.session_id = @session_id
ORDER BY 1;

-- name: SessionsBehind :many
-- The sessions of other events that hold a slot overlapping a hold of this session (they queue behind it or ahead of it).
SELECT DISTINCT g.session_id::bigint AS session_id, g.event_id::bigint AS event_id
FROM banquet_holds h
JOIN banquet_holds g ON g.property_id = h.property_id AND g.atom_room_id = h.atom_room_id AND g.during && h.during AND g.event_id <> h.event_id
WHERE h.property_id = @property_id AND h.event_id = @event_id AND (sqlc.narg(session_id)::bigint IS NULL OR h.session_id = sqlc.narg(session_id)::bigint)
ORDER BY 1;

-- name: SessionEventSales :many
SELECT s.id::bigint AS session_id, e.id::bigint AS event_id, e.sales_user_id, e.status
FROM banquet_sessions s JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
WHERE s.property_id = @property_id AND s.id = ANY(@ids::bigint[]);

-- name: EventHoldRanges :many
-- The hold of each session of an event (the first base room) as local timestamps, for the calendar and the event page.
SELECT h.session_id, min(lower(h.during))::timestamp AS hold_from, max(upper(h.during))::timestamp AS hold_to, min(h.hold_kind)::text AS hold_kind
FROM banquet_holds h
WHERE h.property_id = @property_id AND h.event_id = @event_id
GROUP BY h.session_id;

-- ---------------------------------------------------------------- calendar
-- name: CalendarSessions :many
SELECT s.id AS session_id, s.event_id, e.number AS event_number, e.name AS event_name, e.status AS event_status, e.sales_user_id, e.option_until,
       s.name AS session_name, s.session_date, s.start_time, s.end_time, s.ends_next_day, s.room_id, r.code AS room_code, r.name AS room_name,
       r.setup_minutes, r.teardown_minutes, s.setup_style, s.pax_estimated,
       COALESCE(t.name, '') AS event_type_name,
       COALESCE(c.name, NULLIF(trim(concat_ws(' ', g.first_name, g.last_name)), ''), '')::text AS booker_name
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
LEFT JOIN banquet_event_types t ON t.property_id = e.property_id AND t.id = e.event_type_id
LEFT JOIN companies c ON c.property_id = e.property_id AND c.id = e.booker_company_id
LEFT JOIN guests g ON g.tenant_id = e.tenant_id AND g.id = e.booker_guest_id
WHERE s.tenant_id = sqlc.arg(tenant_id) AND s.property_id = sqlc.arg(property_id) AND s.status = 'ACTIVE' AND s.start_time IS NOT NULL
  AND e.status IN ('INQUIRY', 'TENTATIVE', 'DEFINITE', 'COMPLETED')
  AND s.session_date BETWEEN sqlc.arg(date_from)::date - 1 AND sqlc.arg(date_to)::date
ORDER BY s.session_date, s.start_time, s.id;

-- name: CalendarLocks :many
-- The holds that lock a room other than the one the session is booked in: a combined room's session holds its parts, and a part's session holds the combined rooms it belongs to (36 5).
-- One row for each room and session, the hold with its set-up and tear-down as local timestamps.
WITH atoms AS (
    SELECT r.id AS room_id, r.id AS atom_id FROM banquet_rooms r WHERE r.tenant_id = sqlc.arg(tenant_id) AND r.property_id = sqlc.arg(property_id) AND NOT r.is_combo
    UNION ALL
    SELECT p.combo_room_id, p.part_room_id FROM banquet_room_parts p WHERE p.property_id = sqlc.arg(property_id)
)
SELECT a.room_id, s.id AS session_id, e.id AS event_id, e.number AS event_number, e.name AS event_name, e.status AS event_status,
       min(h.hold_kind)::text AS hold_kind, s.room_id AS via_room_id, vr.code AS via_room_code, vr.name AS via_room_name,
       min(lower(h.during))::timestamp AS hold_from, max(upper(h.during))::timestamp AS hold_to
FROM atoms a
JOIN banquet_holds h ON h.property_id = sqlc.arg(property_id) AND h.atom_room_id = a.atom_id
JOIN banquet_sessions s ON s.property_id = h.property_id AND s.id = h.session_id AND s.room_id <> a.room_id
JOIN banquet_events e ON e.property_id = h.property_id AND e.id = h.event_id
JOIN banquet_rooms vr ON vr.property_id = s.property_id AND vr.id = s.room_id
WHERE h.during && tsrange((sqlc.arg(date_from)::date - 1)::timestamp, (sqlc.arg(date_to)::date + 2)::timestamp)
GROUP BY a.room_id, s.id, e.id, vr.id
ORDER BY hold_from, s.id, a.room_id;

-- name: OverdueOptions :many
SELECT e.id, e.number, e.name, e.option_until, e.sales_user_id, u.full_name AS sales_name
FROM banquet_events e JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status = 'TENTATIVE' AND e.option_until < @today::date
ORDER BY e.option_until, e.id;

-- ---------------------------------------------------------------- notices
-- name: InsertNotice :exec
INSERT INTO banquet_notices (tenant_id, property_id, user_id, kind, event_id, session_id, notice_date)
VALUES (@tenant_id, @property_id, @user_id, @kind, @event_id, sqlc.narg(session_id), @notice_date);

-- name: InsertDueNotices :many
-- The day close: one reminder for each tentative event whose option date has come, once for the event, the salesperson and the day.
INSERT INTO banquet_notices (tenant_id, property_id, user_id, kind, event_id, notice_date)
SELECT e.tenant_id, e.property_id, e.sales_user_id, 'OPTION_DUE', e.id, @notice_date::date
FROM banquet_events e
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status = 'TENTATIVE' AND e.option_until <= @notice_date::date
ON CONFLICT (property_id, event_id, user_id, notice_date) WHERE kind = 'OPTION_DUE' DO NOTHING
RETURNING event_id;

-- name: ListNotices :many
SELECT n.id, n.kind, n.event_id, e.number AS event_number, e.name AS event_name, n.session_id, n.notice_date, n.revision_no, n.created_at, n.seen_at
FROM banquet_notices n JOIN banquet_events e ON e.property_id = n.property_id AND e.id = n.event_id
WHERE n.tenant_id = @tenant_id AND n.property_id = @property_id AND n.user_id = @user_id AND (NOT @unseen_only::boolean OR n.seen_at IS NULL)
ORDER BY n.id DESC
LIMIT @row_limit;

-- name: MarkNoticesSeen :execrows
UPDATE banquet_notices SET seen_at = now()
WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id AND seen_at IS NULL AND (cardinality(@ids::bigint[]) = 0 OR id = ANY(@ids::bigint[]));
