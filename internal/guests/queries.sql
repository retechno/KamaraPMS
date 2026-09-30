-- Guests queries (sqlc). Guests are tenant-wide: every query is scoped by tenant_id.
-- Visibility is always decided in SQL with guest_linked_to (migration 00013), so a guest that
-- the caller may not see is indistinguishable from one that does not exist.

-- Gapless, tenant-wide numbering. The first call creates the series (number 1).
-- name: NextGuestNumber :one
INSERT INTO tenant_sequences (tenant_id, sequence_type, prefix, next_value)
VALUES (@tenant_id, 'GUEST', 'GST', 2)
ON CONFLICT (tenant_id, sequence_type) DO UPDATE SET next_value = tenant_sequences.next_value + 1
RETURNING prefix, (next_value - 1)::bigint AS number;

-- name: CreateGuest :one
INSERT INTO guests (
    tenant_id, code, origin_property_id, first_name, last_name, email, phone, nationality, country_code,
    date_of_birth, gender, id_type, id_number, address, city, notes, created_by, updated_by
) VALUES (
    @tenant_id, @code, @origin_property_id, sqlc.narg(first_name), @last_name, sqlc.narg(email), sqlc.narg(phone),
    sqlc.narg(nationality), sqlc.narg(country_code), sqlc.narg(date_of_birth), sqlc.narg(gender), sqlc.narg(id_type),
    sqlc.narg(id_number), sqlc.narg(address), sqlc.narg(city), sqlc.narg(notes), sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetVisibleGuest :one
SELECT * FROM guests g
WHERE g.tenant_id = @tenant_id AND g.id = @id
  AND (@all_access::boolean OR guest_linked_to(g.tenant_id, g.id, g.origin_property_id, @property_ids::bigint[]));

-- name: GetGuestForUpdate :one
SELECT * FROM guests WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- Is the guest linked to any of the given properties? (write access: guest.write at a linked property)
-- name: GuestLinkedTo :one
SELECT COALESCE(guest_linked_to(g.tenant_id, g.id, g.origin_property_id, @property_ids::bigint[]), false)::boolean AS linked
FROM guests g WHERE g.tenant_id = @tenant_id AND g.id = @id;

-- name: UpdateGuest :one
UPDATE guests SET
    first_name = sqlc.narg(first_name),
    last_name = @last_name,
    email = sqlc.narg(email),
    phone = sqlc.narg(phone),
    nationality = sqlc.narg(nationality),
    country_code = sqlc.narg(country_code),
    date_of_birth = sqlc.narg(date_of_birth),
    gender = sqlc.narg(gender),
    id_type = sqlc.narg(id_type),
    id_number = sqlc.narg(id_number),
    address = sqlc.narg(address),
    city = sqlc.narg(city),
    notes = sqlc.narg(notes),
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- Every token must match the start of the first name, last name, email, id number or guest code, or
-- appear in the phone number's digits (tokens with at least 4 digits). Alphabetical keyset paging.
-- name: SearchGuests :many
SELECT sqlc.embed(g), lower(g.last_name) AS sort_last, lower(coalesce(g.first_name, '')) AS sort_first
FROM guests g
WHERE g.tenant_id = @tenant_id
  AND (@all_access::boolean OR guest_linked_to(g.tenant_id, g.id, g.origin_property_id, @property_ids::bigint[]))
  AND NOT EXISTS (
      SELECT 1 FROM unnest(@tokens::text[]) AS t(tok)
      WHERE NOT (
          lower(coalesce(g.first_name, '')) LIKE t.tok || '%' ESCAPE '\'
          OR lower(g.last_name) LIKE t.tok || '%' ESCAPE '\'
          OR lower(coalesce(g.email, '')) LIKE t.tok || '%' ESCAPE '\'
          OR lower(coalesce(g.id_number, '')) LIKE t.tok || '%' ESCAPE '\'
          OR lower(g.code) LIKE t.tok || '%' ESCAPE '\'
          OR (length(regexp_replace(t.tok, '\D', '', 'g')) >= 4
              AND regexp_replace(coalesce(g.phone, ''), '\D', '', 'g') LIKE '%' || regexp_replace(t.tok, '\D', '', 'g') || '%')
      )
  )
  AND (sqlc.narg(after_id)::bigint IS NULL
       OR (lower(g.last_name), lower(coalesce(g.first_name, '')), g.id)
          > (sqlc.narg(after_last)::text, sqlc.narg(after_first)::text, sqlc.narg(after_id)::bigint))
ORDER BY lower(g.last_name), lower(coalesce(g.first_name, '')), g.id
LIMIT @row_limit;

-- Candidate duplicates of a profile (same email, same phone digits, same ID document, or same name and
-- birth date), with whether the caller may see each one.
-- name: FindGuestDuplicates :many
SELECT sqlc.embed(g),
       (@all_access::boolean OR guest_linked_to(g.tenant_id, g.id, g.origin_property_id, @property_ids::bigint[]))::boolean AS visible
FROM guests g
WHERE g.tenant_id = @tenant_id AND g.id <> @exclude_id
  AND (
      (@email::text <> '' AND lower(g.email) = lower(@email::text))
      OR (@phone_digits::text <> '' AND regexp_replace(coalesce(g.phone, ''), '\D', '', 'g') = @phone_digits::text)
      OR (@id_number::text <> '' AND g.id_number = @id_number::text AND coalesce(g.id_type, '') = @id_type::text)
      OR (sqlc.narg(date_of_birth)::date IS NOT NULL AND g.date_of_birth = sqlc.narg(date_of_birth)::date
          AND lower(g.last_name) = lower(@last_name::text) AND lower(coalesce(g.first_name, '')) = lower(@first_name::text))
  )
ORDER BY g.id
LIMIT 20;

-- Reservations (as booker or occupant) and stays (as primary or accompanying guest) of one guest, newest
-- first, in the given properties (or all when @all_properties).
-- name: ListGuestHistory :many
WITH items AS (
    SELECT 'RESERVATION'::text AS kind, r.id AS id, r.property_id AS property_id, r.confirmation_number AS number,
           r.status::text AS status,
           coalesce((SELECT min(rr.arrival_date) FROM reservation_rooms rr WHERE rr.reservation_id = r.id), r.reservation_date)::date AS arrival_date,
           coalesce((SELECT max(rr.departure_date) FROM reservation_rooms rr WHERE rr.reservation_id = r.id), r.reservation_date)::date AS departure_date,
           r.reservation_date AS sort_date,
           (CASE WHEN r.guest_id = @guest_id THEN 'BOOKER' ELSE 'OCCUPANT' END)::text AS role
    FROM reservations r
    WHERE r.tenant_id = @tenant_id
      AND (r.guest_id = @guest_id
           OR EXISTS (SELECT 1 FROM reservation_rooms x WHERE x.reservation_id = r.id AND x.tenant_id = @tenant_id AND x.guest_id = @guest_id))
    UNION ALL
    SELECT 'STAY'::text, s.id, s.property_id, s.stay_number, s.status::text, s.arrival_date, s.departure_date, s.arrival_date,
           (CASE WHEN s.guest_id = @guest_id THEN 'PRIMARY' ELSE 'ACCOMPANYING' END)::text
    FROM stays s
    WHERE s.tenant_id = @tenant_id
      AND (s.guest_id = @guest_id
           OR EXISTS (SELECT 1 FROM stay_guests g WHERE g.stay_id = s.id AND g.tenant_id = @tenant_id AND g.guest_id = @guest_id))
)
SELECT i.kind, i.id, i.property_id, p.code AS property_code, p.name AS property_name, i.number, i.status,
       i.arrival_date, i.departure_date, i.sort_date, i.role,
       (@all_properties::boolean OR i.property_id = ANY (@property_ids::bigint[]))::boolean AS visible
FROM items i
JOIN properties p ON p.id = i.property_id AND p.tenant_id = @tenant_id
WHERE (@all_properties::boolean OR i.property_id = ANY (@property_ids::bigint[]))
ORDER BY i.sort_date DESC, i.kind, i.id DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: CountGuestHistory :one
WITH items AS (
    SELECT r.property_id FROM reservations r
    WHERE r.tenant_id = @tenant_id
      AND (r.guest_id = @guest_id
           OR EXISTS (SELECT 1 FROM reservation_rooms x WHERE x.reservation_id = r.id AND x.tenant_id = @tenant_id AND x.guest_id = @guest_id))
    UNION ALL
    SELECT s.property_id FROM stays s
    WHERE s.tenant_id = @tenant_id
      AND (s.guest_id = @guest_id
           OR EXISTS (SELECT 1 FROM stay_guests g WHERE g.stay_id = s.id AND g.tenant_id = @tenant_id AND g.guest_id = @guest_id))
)
SELECT count(*) FILTER (WHERE i.property_id = ANY (@property_ids::bigint[]))::bigint AS visible_count,
       count(*) FILTER (WHERE NOT (i.property_id = ANY (@property_ids::bigint[])))::bigint AS hidden_count
FROM items i;
