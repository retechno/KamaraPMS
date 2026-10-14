-- name: CreateGroup :one
INSERT INTO booking_groups (
    tenant_id, property_id, code, name, company_id, contact_name, contact_email, contact_phone,
    arrival_date, departure_date, notes, is_active, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @code, @name, sqlc.narg(company_id), sqlc.narg(contact_name), sqlc.narg(contact_email), sqlc.narg(contact_phone),
    @arrival_date, @departure_date, sqlc.narg(notes), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetGroup :one
SELECT g.*, c.name AS company_name,
       (SELECT count(*) FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status <> 'CANCELLED')::int AS reservation_count,
       (SELECT count(*) FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status = 'CANCELLED')::int AS cancelled_reservation_count,
       (SELECT count(*) FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id
         WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND l.status <> 'CANCELLED')::int AS room_count
FROM booking_groups g
LEFT JOIN companies c ON c.property_id = g.property_id AND c.id = g.company_id
WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.id = @id;

-- name: GetGroupForUpdate :one
SELECT * FROM booking_groups WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListGroups :many
SELECT g.*, c.name AS company_name,
       (SELECT count(*) FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status <> 'CANCELLED')::int AS reservation_count,
       (SELECT count(*) FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status = 'CANCELLED')::int AS cancelled_reservation_count,
       (SELECT count(*) FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id
         WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND l.status <> 'CANCELLED')::int AS room_count
FROM booking_groups g
LEFT JOIN companies c ON c.property_id = g.property_id AND c.id = g.company_id
WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.id < @before_id
  AND (sqlc.narg(active)::boolean IS NULL OR g.is_active = sqlc.narg(active)::boolean)
  -- a group whose reservations are all cancelled is not active, whatever its flag says (it has no status of its own; it is derived): the active list leaves it out
  AND (sqlc.narg(active)::boolean IS DISTINCT FROM true OR NOT (
        EXISTS (SELECT 1 FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status = 'CANCELLED')
        AND NOT EXISTS (SELECT 1 FROM reservations r WHERE r.property_id = g.property_id AND r.booking_group_id = g.id AND r.status <> 'CANCELLED')))
  AND (sqlc.narg(company_id)::bigint IS NULL OR g.company_id = sqlc.narg(company_id)::bigint)
  AND (sqlc.narg(q)::text IS NULL OR g.code ILIKE '%' || sqlc.narg(q)::text || '%' OR g.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY g.id DESC
LIMIT @row_limit;

-- name: UpdateGroup :one
UPDATE booking_groups SET
    name = @name, company_id = sqlc.narg(company_id), contact_name = sqlc.narg(contact_name), contact_email = sqlc.narg(contact_email),
    contact_phone = sqlc.narg(contact_phone), arrival_date = @arrival_date, departure_date = @departure_date, notes = sqlc.narg(notes),
    is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Lines of the group that would fall outside a new date window.
-- name: CountLinesOutside :one
SELECT count(*)::int FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id
WHERE r.property_id = @property_id AND r.booking_group_id = @group_id AND l.status <> 'CANCELLED'
  AND (l.arrival_date < @arrival_date OR l.departure_date > @departure_date);

-- Reservations of the group that bill another company than the one the group is moved to.
-- name: CountReservationsOfOtherCompany :one
SELECT count(*)::int FROM reservations r
WHERE r.property_id = @property_id AND r.booking_group_id = @group_id AND r.status <> 'CANCELLED'
  AND r.company_id IS DISTINCT FROM sqlc.narg(company_id)::bigint;

-- name: ListMembers :many
SELECT r.id, r.confirmation_number, r.status, r.company_id, r.version,
       -- the status staff see: DisplayStatus (reservations/model.go) in SQL, as the list of reservations has it
       (CASE WHEN r.status = 'DRAFT' THEN 'DRAFT'
             WHEN r.status = 'CANCELLED' THEN 'CANCELLED'
             WHEN agg.n_checked_in > 0 THEN 'IN_HOUSE'
             WHEN agg.n_waiting > 0 THEN 'CONFIRMED'
             WHEN agg.n_completed > 0 THEN 'CHECKED_OUT'
             WHEN agg.n_no_show > 0 THEN 'NO_SHOW'
             ELSE 'CANCELLED' END)::text AS display_status,
       agg.arrival_date::date AS arrival_date, agg.departure_date::date AS departure_date, agg.room_count::int AS room_count,
       (agg.n_waiting)::int AS rooms_cancellable, (agg.n_checked_in + agg.n_completed)::int AS rooms_in_house_or_done, agg.n_no_show::int AS rooms_no_show, agg.n_cancelled::int AS rooms_cancelled,
       hold.held AS held_balance,
       COALESCE(NULLIF(trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, '')), ''), '')::text AS guest_name
FROM reservations r
CROSS JOIN LATERAL (
    SELECT COALESCE(min(l.arrival_date) FILTER (WHERE l.status <> 'CANCELLED'), min(l.arrival_date)) AS arrival_date,
           COALESCE(max(l.departure_date) FILTER (WHERE l.status <> 'CANCELLED'), max(l.departure_date)) AS departure_date,
           count(*) FILTER (WHERE l.status <> 'CANCELLED') AS room_count,
           count(*) FILTER (WHERE l.status = 'CHECKED_IN') AS n_checked_in,
           count(*) FILTER (WHERE l.status IN ('CONFIRMED', 'DRAFT')) AS n_waiting,
           count(*) FILTER (WHERE l.status = 'COMPLETED') AS n_completed,
           count(*) FILTER (WHERE l.status = 'NO_SHOW') AS n_no_show,
           count(*) FILTER (WHERE l.status = 'CANCELLED') AS n_cancelled
    FROM reservation_rooms l WHERE l.reservation_id = r.id
) agg
-- what the open folios of the reservation that have no stay hold: a deposit is a credit (negative), a fee or a charge a debit. A cancelled reservation that still holds something is what the follow-up is about.
CROSS JOIN LATERAL (
    SELECT COALESCE(sum(i.debit - i.credit), 0)::numeric AS held
    FROM folios f JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id
    WHERE f.property_id = r.property_id AND f.reservation_id = r.id AND f.stay_id IS NULL AND f.status = 'OPEN'
) hold
LEFT JOIN guests gu ON gu.tenant_id = r.tenant_id AND gu.id = r.guest_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.booking_group_id = @group_id
ORDER BY r.id;

-- name: CompanyActive :one
SELECT is_active FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The decimals of the currency of the property, for the amounts the group reads show.
-- name: CurrencyDecimals :one
SELECT currency_decimals::int FROM properties WHERE tenant_id = @tenant_id AND id = @property_id;
