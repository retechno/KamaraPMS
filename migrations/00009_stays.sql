-- +goose Up
-- Stay = physical occupancy of one reservation room. Occupancy is derived from open stay_rooms.
CREATE TABLE stays (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint      NOT NULL,
    property_id          bigint      NOT NULL,
    stay_number          varchar(20) NOT NULL,
    reservation_room_id  bigint      NOT NULL,
    guest_id             bigint      NOT NULL,
    arrival_date         date        NOT NULL,
    departure_date       date        NOT NULL,
    adult_count          smallint    NOT NULL,
    child_count          smallint    NOT NULL DEFAULT 0,
    status               varchar(12) NOT NULL DEFAULT 'OPEN',
    actual_check_in_at   timestamptz NOT NULL,
    actual_check_in_by   bigint REFERENCES users (id),
    actual_check_out_at  timestamptz,
    actual_check_out_by  bigint REFERENCES users (id),
    remarks              text,
    version              int         NOT NULL DEFAULT 1,
    created_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT stays_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT stays_line_fk        FOREIGN KEY (property_id, reservation_room_id) REFERENCES reservation_rooms (property_id, id),
    CONSTRAINT stays_guest_fk       FOREIGN KEY (tenant_id, guest_id)              REFERENCES guests (tenant_id, id),
    CONSTRAINT stays_number_uk      UNIQUE (property_id, stay_number),
    CONSTRAINT stays_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT stays_dates_ck       CHECK (departure_date > arrival_date),
    CONSTRAINT stays_adults_ck      CHECK (adult_count >= 1),
    CONSTRAINT stays_children_ck    CHECK (child_count >= 0),
    CONSTRAINT stays_status_ck      CHECK (status IN ('OPEN', 'CHECKED_OUT', 'CANCELLED')),
    CONSTRAINT stays_checked_out_ck CHECK ((status = 'CHECKED_OUT') = (actual_check_out_at IS NOT NULL)),
    CONSTRAINT stays_check_out_after_in_ck CHECK (actual_check_out_at IS NULL OR actual_check_out_at >= actual_check_in_at),
    CONSTRAINT stays_version_ck     CHECK (version >= 1)
);
-- A reservation room produces at most one live stay (a reversed check-in is CANCELLED).
CREATE UNIQUE INDEX stays_live_line_uk ON stays (reservation_room_id) WHERE status <> 'CANCELLED';
CREATE INDEX stays_open_departure_idx ON stays (property_id, departure_date) WHERE status = 'OPEN';
CREATE INDEX stays_guest_idx          ON stays (tenant_id, guest_id);
CREATE TRIGGER stays_set_updated_at BEFORE UPDATE ON stays
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Sequential room segments (room moves). Night N belongs to the segment with
-- start_business_date <= N < COALESCE(end_business_date, infinity).
CREATE TABLE stay_rooms (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint      NOT NULL,
    property_id          bigint      NOT NULL,
    stay_id              bigint      NOT NULL,
    room_id              bigint      NOT NULL,
    check_in_at          timestamptz NOT NULL,
    check_out_at         timestamptz,
    start_business_date  date        NOT NULL,
    end_business_date    date,
    move_reason          varchar(500),
    created_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT stay_rooms_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT stay_rooms_stay_fk        FOREIGN KEY (property_id, stay_id)   REFERENCES stays (property_id, id),
    CONSTRAINT stay_rooms_room_fk        FOREIGN KEY (property_id, room_id)   REFERENCES rooms (property_id, id),
    CONSTRAINT stay_rooms_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT stay_rooms_times_ck       CHECK (check_out_at IS NULL OR check_out_at >= check_in_at),
    CONSTRAINT stay_rooms_dates_ck       CHECK (end_business_date IS NULL OR end_business_date >= start_business_date),
    CONSTRAINT stay_rooms_closed_ck      CHECK ((check_out_at IS NULL) = (end_business_date IS NULL))
);
-- Physical double occupancy is impossible: one open segment per room and per stay.
CREATE UNIQUE INDEX stay_rooms_open_room_uk ON stay_rooms (room_id) WHERE check_out_at IS NULL;
CREATE UNIQUE INDEX stay_rooms_open_stay_uk ON stay_rooms (stay_id) WHERE check_out_at IS NULL;
CREATE INDEX stay_rooms_stay_idx         ON stay_rooms (stay_id);
CREATE INDEX stay_rooms_room_history_idx ON stay_rooms (room_id, start_business_date);
CREATE TRIGGER stay_rooms_set_updated_at BEFORE UPDATE ON stay_rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Accompanying guests (the primary guest is stays.guest_id).
CREATE TABLE stay_guests (
    tenant_id    bigint      NOT NULL,
    property_id  bigint      NOT NULL,
    stay_id      bigint      NOT NULL,
    guest_id     bigint      NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    PRIMARY KEY (stay_id, guest_id),
    CONSTRAINT stay_guests_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT stay_guests_stay_fk     FOREIGN KEY (property_id, stay_id)   REFERENCES stays (property_id, id),
    CONSTRAINT stay_guests_guest_fk    FOREIGN KEY (tenant_id, guest_id)    REFERENCES guests (tenant_id, id)
);
CREATE INDEX stay_guests_guest_idx ON stay_guests (tenant_id, guest_id);

-- +goose Down
DROP TABLE IF EXISTS stay_guests;
DROP TABLE IF EXISTS stay_rooms;
DROP TABLE IF EXISTS stays;
