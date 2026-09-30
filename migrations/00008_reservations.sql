-- +goose Up
-- Reservation = booking container. Dates, occupancy and the arrival lifecycle live on the lines.
CREATE TABLE reservations (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint      NOT NULL,
    property_id          bigint      NOT NULL,
    confirmation_number  varchar(20) NOT NULL,
    guest_id             bigint,
    reservation_date     date        NOT NULL,
    source               varchar(20) NOT NULL,
    market               varchar(30),
    status               varchar(10) NOT NULL DEFAULT 'DRAFT',
    special_request      text,
    remarks              text,
    confirmed_at         timestamptz,
    confirmed_by         bigint REFERENCES users (id),
    cancelled_at         timestamptz,
    cancelled_by         bigint REFERENCES users (id),
    cancellation_reason  varchar(500),
    version              int         NOT NULL DEFAULT 1,
    created_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT reservations_property_fk         FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT reservations_guest_fk            FOREIGN KEY (tenant_id, guest_id)    REFERENCES guests (tenant_id, id),
    CONSTRAINT reservations_confirmation_uk     UNIQUE (property_id, confirmation_number),
    CONSTRAINT reservations_property_id_uk      UNIQUE (property_id, id),
    CONSTRAINT reservations_source_ck           CHECK (source IN ('WALK_IN', 'PHONE', 'EMAIL', 'WEBSITE', 'OTA', 'AGENT', 'OTHER')),
    CONSTRAINT reservations_status_ck           CHECK (status IN ('DRAFT', 'CONFIRMED', 'CANCELLED')),
    CONSTRAINT reservations_booker_ck           CHECK (status = 'DRAFT' OR guest_id IS NOT NULL),
    CONSTRAINT reservations_confirmed_ck        CHECK (status = 'DRAFT' OR status = 'CANCELLED' OR confirmed_at IS NOT NULL),
    CONSTRAINT reservations_cancelled_ck        CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
    CONSTRAINT reservations_version_ck          CHECK (version >= 1)
);
CREATE INDEX reservations_guest_idx ON reservations (tenant_id, guest_id);
CREATE INDEX reservations_date_idx  ON reservations (property_id, reservation_date);
CREATE TRIGGER reservations_set_updated_at BEFORE UPDATE ON reservations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One booked room unit. Only CONFIRMED lines consume inventory.
CREATE TABLE reservation_rooms (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint      NOT NULL,
    property_id          bigint      NOT NULL,
    reservation_id       bigint      NOT NULL,
    guest_id             bigint,
    room_type_id         bigint      NOT NULL,
    room_id              bigint,
    rate_plan_id         bigint      NOT NULL,
    arrival_date         date        NOT NULL,
    departure_date       date        NOT NULL,
    adult_count          smallint    NOT NULL,
    child_count          smallint    NOT NULL DEFAULT 0,
    status               varchar(12) NOT NULL DEFAULT 'DRAFT',
    cancelled_at         timestamptz,
    cancelled_by         bigint REFERENCES users (id),
    cancellation_reason  varchar(500),
    no_show_at           timestamptz,
    no_show_by           bigint REFERENCES users (id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT reservation_rooms_property_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT reservation_rooms_reservation_fk FOREIGN KEY (property_id, reservation_id) REFERENCES reservations (property_id, id),
    CONSTRAINT reservation_rooms_guest_fk       FOREIGN KEY (tenant_id, guest_id)         REFERENCES guests (tenant_id, id),
    CONSTRAINT reservation_rooms_room_type_fk   FOREIGN KEY (property_id, room_type_id)   REFERENCES room_types (property_id, id),
    CONSTRAINT reservation_rooms_room_fk        FOREIGN KEY (property_id, room_id)        REFERENCES rooms (property_id, id),
    CONSTRAINT reservation_rooms_rate_plan_fk   FOREIGN KEY (property_id, rate_plan_id)   REFERENCES rate_plans (property_id, id),
    CONSTRAINT reservation_rooms_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT reservation_rooms_dates_ck       CHECK (departure_date > arrival_date),
    CONSTRAINT reservation_rooms_adults_ck      CHECK (adult_count >= 1),
    CONSTRAINT reservation_rooms_children_ck    CHECK (child_count >= 0),
    CONSTRAINT reservation_rooms_status_ck      CHECK (status IN ('DRAFT', 'CONFIRMED', 'CHECKED_IN', 'COMPLETED', 'CANCELLED', 'NO_SHOW')),
    CONSTRAINT reservation_rooms_cancelled_ck   CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
    CONSTRAINT reservation_rooms_no_show_ck     CHECK ((status = 'NO_SHOW') = (no_show_at IS NOT NULL)),
    -- Double-booking backstop: two CONFIRMED lines can never hold the same room on overlapping nights.
    CONSTRAINT reservation_rooms_no_overlap_ex EXCLUDE USING gist (
        room_id WITH =,
        daterange(arrival_date, departure_date, '[)') WITH &&
    ) WHERE (status = 'CONFIRMED' AND room_id IS NOT NULL)
);
CREATE INDEX reservation_rooms_reservation_idx  ON reservation_rooms (reservation_id);
CREATE INDEX reservation_rooms_availability_idx ON reservation_rooms
    USING gist (property_id, room_type_id, daterange(arrival_date, departure_date, '[)')) WHERE status = 'CONFIRMED';
CREATE INDEX reservation_rooms_arrivals_idx     ON reservation_rooms (property_id, arrival_date) WHERE status = 'CONFIRMED';
CREATE INDEX reservation_rooms_guest_idx        ON reservation_rooms (tenant_id, guest_id) WHERE guest_id IS NOT NULL;
CREATE TRIGGER reservation_rooms_set_updated_at BEFORE UPDATE ON reservation_rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Nightly pricing snapshot: what was agreed for each night, and how it must be posted.
CREATE TABLE reservation_room_rates (
    tenant_id            bigint        NOT NULL,
    property_id          bigint        NOT NULL,
    reservation_room_id  bigint        NOT NULL,
    stay_date            date          NOT NULL,
    rate_plan_id         bigint        NOT NULL,
    charge_code_id       bigint        NOT NULL,
    price_mode           varchar(12)   NOT NULL,
    base_rate            numeric(18,2),
    discount_amount      numeric(18,2) NOT NULL DEFAULT 0,
    amount               numeric(18,2) NOT NULL,
    is_override          boolean       NOT NULL DEFAULT false,
    created_at           timestamptz   NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz   NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    PRIMARY KEY (reservation_room_id, stay_date),
    CONSTRAINT reservation_room_rates_property_fk FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT reservation_room_rates_line_fk     FOREIGN KEY (property_id, reservation_room_id) REFERENCES reservation_rooms (property_id, id) ON DELETE CASCADE,
    CONSTRAINT reservation_room_rates_plan_fk     FOREIGN KEY (property_id, rate_plan_id)        REFERENCES rate_plans (property_id, id),
    CONSTRAINT reservation_room_rates_code_fk     FOREIGN KEY (property_id, charge_code_id)      REFERENCES charge_codes (property_id, id),
    CONSTRAINT reservation_room_rates_mode_ck     CHECK (price_mode IN ('EXCLUSIVE', 'INCLUSIVE')),
    CONSTRAINT reservation_room_rates_base_ck     CHECK (base_rate IS NULL OR base_rate >= 0),
    CONSTRAINT reservation_room_rates_discount_ck CHECK (discount_amount >= 0),
    CONSTRAINT reservation_room_rates_amount_ck   CHECK (amount >= 0),
    CONSTRAINT reservation_room_rates_derived_ck  CHECK (is_override OR (base_rate IS NOT NULL AND amount = base_rate - discount_amount))
);
CREATE INDEX reservation_room_rates_code_idx ON reservation_room_rates (charge_code_id);
CREATE TRIGGER reservation_room_rates_set_updated_at BEFORE UPDATE ON reservation_room_rates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementBegin
CREATE FUNCTION reservation_room_rates_code_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM assert_room_charge_code(NEW.property_id, NEW.charge_code_id);
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER reservation_room_rates_code_guard BEFORE INSERT OR UPDATE OF charge_code_id ON reservation_room_rates
    FOR EACH ROW EXECUTE FUNCTION reservation_room_rates_code_guard();

-- +goose Down
DROP TABLE IF EXISTS reservation_room_rates;
DROP FUNCTION IF EXISTS reservation_room_rates_code_guard();
DROP TABLE IF EXISTS reservation_rooms;
DROP TABLE IF EXISTS reservations;
