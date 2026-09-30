-- +goose Up
CREATE TABLE room_types (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    code            varchar(20)  NOT NULL,
    name            varchar(100) NOT NULL,
    description     text,
    max_adult       smallint     NOT NULL,
    max_child       smallint     NOT NULL,
    max_occupancy   smallint     NOT NULL,
    base_occupancy  smallint     NOT NULL,
    sort_order      int          NOT NULL DEFAULT 0,
    is_active       boolean      NOT NULL DEFAULT true,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT room_types_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT room_types_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT room_types_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT room_types_max_adult_ck     CHECK (max_adult >= 1),
    CONSTRAINT room_types_max_child_ck     CHECK (max_child >= 0),
    CONSTRAINT room_types_max_occ_ck       CHECK (max_occupancy BETWEEN 1 AND max_adult + max_child),
    CONSTRAINT room_types_base_occ_ck      CHECK (base_occupancy BETWEEN 1 AND max_occupancy)
);
CREATE TRIGGER room_types_set_updated_at BEFORE UPDATE ON room_types
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- No occupancy status column: occupancy is derived from stays and reservations.
CREATE TABLE rooms (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    room_type_id  bigint      NOT NULL,
    room_number   varchar(20) NOT NULL,
    floor         varchar(10),
    building      varchar(50),
    is_active     boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT rooms_property_fk        FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT rooms_room_type_fk       FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT rooms_property_number_uk UNIQUE (property_id, room_number),
    CONSTRAINT rooms_property_id_uk     UNIQUE (property_id, id)
);
CREATE INDEX rooms_room_type_idx ON rooms (property_id, room_type_id);
CREATE TRIGGER rooms_set_updated_at BEFORE UPDATE ON rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Current housekeeping state. Kept off the rooms row, which is the lock target for allocation.
CREATE TABLE room_housekeeping (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint      NOT NULL,
    property_id  bigint      NOT NULL,
    room_id      bigint      NOT NULL,
    status       varchar(10) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    CONSTRAINT room_housekeeping_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT room_housekeeping_room_fk     FOREIGN KEY (property_id, room_id)   REFERENCES rooms (property_id, id),
    CONSTRAINT room_housekeeping_room_uk     UNIQUE (room_id),
    CONSTRAINT room_housekeeping_status_ck   CHECK (status IN ('CLEAN', 'DIRTY', 'CLEANING', 'INSPECTED'))
);
CREATE INDEX room_housekeeping_status_idx ON room_housekeeping (property_id, status);
CREATE TRIGGER room_housekeeping_set_updated_at BEFORE UPDATE ON room_housekeeping
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE housekeeping_logs (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    room_id        bigint      NOT NULL,
    from_status    varchar(10) NOT NULL,
    to_status      varchar(10) NOT NULL,
    source         varchar(20) NOT NULL,
    business_date  date        NOT NULL,
    notes          varchar(500),
    changed_at     timestamptz NOT NULL DEFAULT now(),
    changed_by     bigint REFERENCES users (id),
    CONSTRAINT housekeeping_logs_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT housekeeping_logs_room_fk     FOREIGN KEY (property_id, room_id)   REFERENCES rooms (property_id, id),
    CONSTRAINT housekeeping_logs_status_ck   CHECK (from_status IN ('CLEAN', 'DIRTY', 'CLEANING', 'INSPECTED')
                                                AND to_status IN ('CLEAN', 'DIRTY', 'CLEANING', 'INSPECTED')),
    CONSTRAINT housekeeping_logs_change_ck   CHECK (from_status <> to_status),
    CONSTRAINT housekeeping_logs_source_ck   CHECK (source IN ('MANUAL', 'CHECK_OUT', 'ROOM_MOVE', 'NIGHT_AUDIT', 'CHECK_IN_REVERSAL'))
);
CREATE INDEX housekeeping_logs_room_idx ON housekeeping_logs (room_id, changed_at DESC);
CREATE INDEX housekeeping_logs_bd_idx   ON housekeeping_logs (property_id, business_date);
CREATE TRIGGER housekeeping_logs_append_only BEFORE UPDATE OR DELETE ON housekeeping_logs
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- OOO / OOS. Half-open [start_date, end_date). Both make the room unsellable.
CREATE TABLE room_blocks (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    room_id       bigint       NOT NULL,
    block_type    varchar(3)   NOT NULL,
    start_date    date         NOT NULL,
    end_date      date         NOT NULL,
    reason        varchar(500) NOT NULL,
    status        varchar(10)  NOT NULL DEFAULT 'ACTIVE',
    cancelled_at  timestamptz,
    cancelled_by  bigint REFERENCES users (id),
    created_at    timestamptz  NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT room_blocks_property_fk  FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT room_blocks_room_fk      FOREIGN KEY (property_id, room_id)   REFERENCES rooms (property_id, id),
    CONSTRAINT room_blocks_type_ck      CHECK (block_type IN ('OOO', 'OOS')),
    CONSTRAINT room_blocks_dates_ck     CHECK (end_date > start_date),
    CONSTRAINT room_blocks_status_ck    CHECK (status IN ('ACTIVE', 'CANCELLED')),
    CONSTRAINT room_blocks_cancelled_ck CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
    CONSTRAINT room_blocks_no_overlap_ex EXCLUDE USING gist (
        room_id WITH =,
        daterange(start_date, end_date, '[)') WITH &&
    ) WHERE (status = 'ACTIVE')
);
CREATE INDEX room_blocks_property_range_idx ON room_blocks
    USING gist (property_id, daterange(start_date, end_date, '[)')) WHERE status = 'ACTIVE';
CREATE TRIGGER room_blocks_set_updated_at BEFORE UPDATE ON room_blocks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS room_blocks;
DROP TABLE IF EXISTS housekeeping_logs;
DROP TABLE IF EXISTS room_housekeeping;
DROP TABLE IF EXISTS rooms;
DROP TABLE IF EXISTS room_types;
