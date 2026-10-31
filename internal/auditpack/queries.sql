-- Night audit pack queries (sqlc). Every query is scoped by tenant_id and property_id.

-- The closed day a pack is made for: the summary the close stored, who closed it and when.
-- name: GetDay :one
SELECT d.id, d.status, d.closed_at, d.summary, COALESCE(u.full_name, '')::text AS closed_by_name
FROM business_days d LEFT JOIN users u ON u.id = d.closed_by
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.business_date = @bd::date;

-- Whether the property has any cashier shift: without one the section of the Z reports is off.
-- name: PropertyHasShifts :one
SELECT EXISTS (SELECT 1 FROM cashier_shifts WHERE tenant_id = @tenant_id AND property_id = @property_id) AS has_shifts;

-- The rooms out of order or out of service on a business date: an ACTIVE block whose half-open range [start_date, end_date) holds the date.
-- name: ListRoomBlocksOn :many
SELECT b.id AS block_id, r.room_number, t.code AS room_type_code, t.name AS room_type_name, b.block_type, b.start_date, b.end_date, b.reason
FROM room_blocks b
JOIN rooms r ON r.property_id = b.property_id AND r.id = b.room_id
JOIN room_types t ON t.property_id = r.property_id AND t.id = r.room_type_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'ACTIVE' AND b.start_date <= @bd::date AND b.end_date > @bd::date
ORDER BY r.room_number, b.id;

-- The covers of the bills closed on a business date, for each outlet (the guests counted on the bill; the bills with no count are counted apart).
-- name: OutletCoversOn :many
SELECT o.id AS outlet_id, COALESCE(sum(b.guest_count), 0)::int AS covers, count(*)::int AS bills, count(*) FILTER (WHERE b.guest_count IS NULL)::int AS bills_without_covers
FROM pos_bills b JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED' AND b.closed_business_date = @bd::date
GROUP BY o.id;

-- name: InsertPack :one
INSERT INTO nap_packs (tenant_id, property_id, business_date, format_version, status, built_at, built_by, build_ms, built_late)
VALUES (@tenant_id, @property_id, @bd::date, @format_version, @status, @built_at, @built_by, @build_ms, @built_late)
RETURNING id;

-- name: InsertSection :exec
INSERT INTO nap_pack_sections (pack_id, tenant_id, property_id, section, status, format_version, data, error, built_late)
VALUES (@pack_id, @tenant_id, @property_id, @section, @status, @format_version, @data, @error, @built_late);

-- name: GetPackByDate :one
SELECT p.id, p.business_date, p.format_version, p.status, p.built_at, COALESCE(u.full_name, '')::text AS built_by_name, p.build_ms, p.built_late
FROM nap_packs p LEFT JOIN users u ON u.id = p.built_by
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date = @bd::date;

-- name: ListPackSections :many
SELECT section, status, format_version, data, error, built_late, COALESCE(octet_length(data::text), 0)::int AS size_bytes
FROM nap_pack_sections WHERE tenant_id = @tenant_id AND property_id = @property_id AND pack_id = @pack_id
ORDER BY section;

-- The archive: newest day first, a page after the cursor (the business date of the last row of the page before).
-- name: ListPacks :many
SELECT p.id, p.business_date, p.format_version, p.status, p.built_at, p.build_ms, p.built_late,
       (SELECT count(*) FROM nap_pack_sections s WHERE s.pack_id = p.id AND s.status = 'OK')::int AS sections_ok,
       (SELECT count(*) FROM nap_pack_sections s WHERE s.pack_id = p.id AND s.status = 'FAILED')::int AS sections_failed
FROM nap_packs p
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date BETWEEN @from_date::date AND @to_date::date
  AND (sqlc.narg('before')::date IS NULL OR p.business_date < sqlc.narg('before')::date)
ORDER BY p.business_date DESC
LIMIT @page_size;

-- A section that failed and is built again: its new status, JSON or reason (the trigger refuses the change of any section that did not fail).
-- name: RebuildSection :execrows
UPDATE nap_pack_sections SET status = @status, data = @data, error = @error, built_late = true
WHERE tenant_id = @tenant_id AND property_id = @property_id AND pack_id = @pack_id AND section = @section AND status = 'FAILED';

-- name: SetPackStatus :exec
UPDATE nap_packs SET status = @status WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @pack_id;
