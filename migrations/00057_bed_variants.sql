-- +goose Up
-- Bed variants (design: docs/architecture/16-bed-variants.md): the bed type of a room becomes required, a reservation line can keep ("lock") the bed it asks for, and a rate plan can add a
-- supplement per room type and bed type.

-- 1. Every room has a bed type. The rooms that have none take the first active bed type of their property (by the sort order of the catalogue; a property that has no bed type at all
-- first gets the standard catalogue), and each of them gets an audit entry, so the owner can find them and correct them.
INSERT INTO bed_types (tenant_id, property_id, code, name, sort_order)
SELECT p.tenant_id, p.id, d.code, d.name, d.sort_order
FROM properties p
CROSS JOIN (VALUES ('KING', 'King', 10), ('QUEEN', 'Queen', 20), ('DOUBLE', 'Double', 30), ('TWIN', 'Twin', 40), ('SINGLE', 'Single', 50)) AS d (code, name, sort_order)
WHERE NOT EXISTS (SELECT 1 FROM bed_types b WHERE b.property_id = p.id);

WITH filled AS (
    UPDATE rooms r
       SET bed_type_id = (SELECT b.id FROM bed_types b WHERE b.property_id = r.property_id ORDER BY b.is_active DESC, b.sort_order, b.id LIMIT 1)
     WHERE r.bed_type_id IS NULL
    RETURNING r.id, r.tenant_id, r.property_id, r.room_number, r.bed_type_id
)
INSERT INTO audit_logs (tenant_id, property_id, action, entity_type, entity_id, new_data)
SELECT f.tenant_id, f.property_id, 'room.bed_type_defaulted', 'room', f.id, jsonb_build_object('room_number', f.room_number, 'bed_type_id', f.bed_type_id, 'reason', 'the bed type became required')
FROM filled f;

DROP INDEX rooms_bed_type_idx;
ALTER TABLE rooms ALTER COLUMN bed_type_id SET NOT NULL;
CREATE INDEX rooms_bed_type_idx ON rooms (property_id, bed_type_id);

-- 2. A reservation line that asks for a bed can keep it: a locked line takes a room with that bed and uses the stock of the variant. Every request that exists stays a soft request.
ALTER TABLE reservation_rooms ADD COLUMN bed_locked boolean NOT NULL DEFAULT false;
ALTER TABLE reservation_rooms ADD CONSTRAINT reservation_rooms_bed_locked_ck CHECK (NOT bed_locked OR requested_bed_type_id IS NOT NULL);

-- 3. The supplement of a variant: per rate plan, room type and bed type, an amount or a percentage of the nightly price, from a date. A row is never changed: a new figure is a new row from a
-- later date. No row, or 0, is the same price. A night of a locked line keeps the supplement it was priced with (reservation_room_rates.bed_adjustment).
CREATE TABLE rate_plan_bed_adjustments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    rate_plan_id   bigint        NOT NULL,
    room_type_id   bigint        NOT NULL,
    bed_type_id    bigint        NOT NULL,
    adjust_kind    varchar(7)    NOT NULL,
    amount         numeric(18,3) NOT NULL,
    effective_from date          NOT NULL,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    CONSTRAINT rpba_property_fk   FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT rpba_plan_fk       FOREIGN KEY (property_id, rate_plan_id) REFERENCES rate_plans (property_id, id),
    CONSTRAINT rpba_type_fk       FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT rpba_bed_fk        FOREIGN KEY (property_id, bed_type_id)  REFERENCES bed_types (property_id, id),
    CONSTRAINT rpba_start_uk      UNIQUE (rate_plan_id, room_type_id, bed_type_id, effective_from),
    CONSTRAINT rpba_kind_ck       CHECK (adjust_kind IN ('AMOUNT', 'PERCENT')),
    CONSTRAINT rpba_percent_ck    CHECK (adjust_kind <> 'PERCENT' OR amount BETWEEN -100 AND 1000)
);
CREATE INDEX rpba_plan_idx ON rate_plan_bed_adjustments (property_id, rate_plan_id, room_type_id, bed_type_id, effective_from DESC);
CREATE TRIGGER rate_plan_bed_adjustments_append_only BEFORE UPDATE OR DELETE ON rate_plan_bed_adjustments FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER rate_plan_bed_adjustments_no_truncate BEFORE TRUNCATE ON rate_plan_bed_adjustments FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

ALTER TABLE reservation_room_rates ADD COLUMN bed_adjustment numeric(18,3) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE reservation_room_rates DROP COLUMN bed_adjustment;
DROP TABLE rate_plan_bed_adjustments;
ALTER TABLE reservation_rooms DROP CONSTRAINT reservation_rooms_bed_locked_ck, DROP COLUMN bed_locked;
DROP INDEX rooms_bed_type_idx;
ALTER TABLE rooms ALTER COLUMN bed_type_id DROP NOT NULL;
CREATE INDEX rooms_bed_type_idx ON rooms (property_id, bed_type_id) WHERE bed_type_id IS NOT NULL;
