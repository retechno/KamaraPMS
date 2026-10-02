-- +goose Up
-- A maintenance request: something is broken in a room or somewhere in the hotel. It can be assigned to a technician,
-- worked on and resolved, and a request about a room can take the room out of sale through a room block (OOO/OOS).
ALTER TABLE room_blocks ADD CONSTRAINT room_blocks_property_id_uk UNIQUE (property_id, id);

CREATE TABLE maintenance_requests (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    request_number   varchar(20)   NOT NULL,
    room_id          bigint,
    location         varchar(150),
    category         varchar(10)   NOT NULL,
    description      varchar(1000) NOT NULL,
    priority         varchar(6)    NOT NULL DEFAULT 'NORMAL',
    status           varchar(11)   NOT NULL DEFAULT 'OPEN',
    business_date    date          NOT NULL,
    reported_by      bigint REFERENCES users (id),
    reported_at      timestamptz   NOT NULL DEFAULT now(),
    assigned_to      bigint,
    assigned_at      timestamptz,
    started_at       timestamptz,
    closed_at        timestamptz,
    closed_by        bigint REFERENCES users (id),
    resolution_note  varchar(1000),
    room_block_id    bigint,
    created_at       timestamptz   NOT NULL DEFAULT now(),
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT mr_property_fk     FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT mr_room_fk         FOREIGN KEY (property_id, room_id)      REFERENCES rooms (property_id, id),
    CONSTRAINT mr_block_fk        FOREIGN KEY (property_id, room_block_id) REFERENCES room_blocks (property_id, id),
    CONSTRAINT mr_assignee_fk     FOREIGN KEY (tenant_id, assigned_to)    REFERENCES users (tenant_id, id),
    CONSTRAINT mr_business_day_fk FOREIGN KEY (property_id, business_date) REFERENCES business_days (property_id, business_date),
    CONSTRAINT mr_number_uk       UNIQUE (property_id, request_number),
    CONSTRAINT mr_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT mr_category_ck     CHECK (category IN ('PLUMBING', 'ELECTRICAL', 'AC', 'FURNITURE', 'APPLIANCE', 'OTHER')),
    CONSTRAINT mr_priority_ck     CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    CONSTRAINT mr_status_ck       CHECK (status IN ('OPEN', 'IN_PROGRESS', 'RESOLVED', 'CANCELLED')),
    CONSTRAINT mr_where_ck        CHECK (room_id IS NOT NULL OR location IS NOT NULL),
    CONSTRAINT mr_block_room_ck   CHECK (room_block_id IS NULL OR room_id IS NOT NULL),
    CONSTRAINT mr_assigned_ck     CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
    CONSTRAINT mr_started_ck      CHECK (status <> 'IN_PROGRESS' OR started_at IS NOT NULL),
    CONSTRAINT mr_closed_ck       CHECK ((status IN ('RESOLVED', 'CANCELLED')) = (closed_at IS NOT NULL)),
    CONSTRAINT mr_cancel_note_ck  CHECK (status <> 'CANCELLED' OR resolution_note IS NOT NULL)
);
CREATE INDEX mr_open_idx     ON maintenance_requests (property_id, status, priority);
CREATE INDEX mr_room_idx     ON maintenance_requests (property_id, room_id) WHERE room_id IS NOT NULL;
CREATE INDEX mr_assignee_idx ON maintenance_requests (property_id, assigned_to) WHERE assigned_to IS NOT NULL;
CREATE TRIGGER maintenance_requests_set_updated_at BEFORE UPDATE ON maintenance_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'MAINTENANCE', 'MNT' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type = 'MAINTENANCE';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE'));
DROP TABLE IF EXISTS maintenance_requests;
ALTER TABLE room_blocks DROP CONSTRAINT room_blocks_property_id_uk;
