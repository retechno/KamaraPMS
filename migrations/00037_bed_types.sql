-- +goose Up
-- Bed types: a catalogue per property (King, Twin, ...), the bed type of each room, and the bed type a guest asks for on
-- a reservation line. A room type groups rooms that can be sold as the same thing; the bed type tells the rooms of one
-- type apart (a Deluxe room with a King bed or with two Twins). It is a request, not inventory: availability is still
-- counted per room type, and a room with another bed type than the one asked for can still be assigned.

CREATE TABLE bed_types (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(60)  NOT NULL,
    sort_order  integer      NOT NULL DEFAULT 0,
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT bed_types_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT bed_types_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT bed_types_property_id_uk   UNIQUE (property_id, id)
);
CREATE TRIGGER bed_types_set_updated_at BEFORE UPDATE ON bed_types
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE rooms
    ADD COLUMN bed_type_id bigint,
    ADD CONSTRAINT rooms_bed_type_fk FOREIGN KEY (property_id, bed_type_id) REFERENCES bed_types (property_id, id);
CREATE INDEX rooms_bed_type_idx ON rooms (property_id, bed_type_id) WHERE bed_type_id IS NOT NULL;

ALTER TABLE reservation_rooms
    ADD COLUMN requested_bed_type_id bigint,
    ADD CONSTRAINT reservation_rooms_bed_type_fk FOREIGN KEY (property_id, requested_bed_type_id) REFERENCES bed_types (property_id, id);

-- The standard catalogue for every property that exists. New properties get it from the property creation hook.
INSERT INTO bed_types (tenant_id, property_id, code, name, sort_order)
SELECT p.tenant_id, p.id, d.code, d.name, d.sort_order
FROM properties p
CROSS JOIN (VALUES ('KING', 'King', 10), ('QUEEN', 'Queen', 20), ('DOUBLE', 'Double', 30), ('TWIN', 'Twin', 40), ('SINGLE', 'Single', 50)) AS d (code, name, sort_order);

-- +goose Down
ALTER TABLE reservation_rooms DROP CONSTRAINT reservation_rooms_bed_type_fk, DROP COLUMN requested_bed_type_id;
DROP INDEX rooms_bed_type_idx;
ALTER TABLE rooms DROP CONSTRAINT rooms_bed_type_fk, DROP COLUMN bed_type_id;
DROP TABLE bed_types;
