-- +goose Up
-- Banquet, PR 1 (docs/architecture/36-banquet.md, sections 3.2, 4.1 to 4.3, 6): the masters and the schedule, no money.
--
--   banquet_settings        the conflict mode of the options (no row: the defaults) and the hours of operation
--   banquet_rooms           meeting rooms, base or combined, with the buffers; banquet_room_parts the base rooms of a combined room (one level);
--                           banquet_room_setups the capacity of a room for each style of set-up
--   banquet_event_types, banquet_reasons, banquet_items, banquet_packages (+ components): the masters
--   banquet_events          the event and its status; banquet_sessions the days and times of an event
--   banquet_holds           one row for each base room a session covers, with the time of the set-up and the tear-down; the exclusion refuses a second Definite
--   banquet_notices         the bell of the sales (option moved up, option date passed, option lost)
--   document_sequences      BANQUET_EVENT (the number of an event)

-- 1. The settings ---------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_settings (
    property_id             bigint      PRIMARY KEY,
    tenant_id               bigint      NOT NULL,
    tentative_conflict_mode varchar(10) NOT NULL DEFAULT 'REJECT',
    operating_from          time,
    operating_to            time,
    updated_at              timestamptz NOT NULL DEFAULT now(),
    updated_by              bigint REFERENCES users (id),
    CONSTRAINT banquet_settings_property_fk  FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_settings_mode_ck      CHECK (tentative_conflict_mode IN ('REJECT', 'QUEUE')),
    CONSTRAINT banquet_settings_hours_ck     CHECK ((operating_from IS NULL) = (operating_to IS NULL) AND (operating_from IS NULL OR operating_from < operating_to))
);

-- 2. The meeting rooms ----------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_rooms (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    code             varchar(20)   NOT NULL,
    name             varchar(100)  NOT NULL,
    area_m2          numeric(8,1),
    setup_minutes    int           NOT NULL DEFAULT 0,
    teardown_minutes int           NOT NULL DEFAULT 0,
    is_combo         boolean       NOT NULL DEFAULT false,
    is_active        boolean       NOT NULL DEFAULT true,
    sort_order       int           NOT NULL DEFAULT 0,
    version          int           NOT NULL DEFAULT 1,
    created_at       timestamptz   NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    CONSTRAINT banquet_rooms_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_rooms_code_uk          UNIQUE (property_id, code),
    CONSTRAINT banquet_rooms_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT banquet_rooms_area_ck          CHECK (area_m2 IS NULL OR area_m2 > 0),
    CONSTRAINT banquet_rooms_buffers_ck       CHECK (setup_minutes BETWEEN 0 AND 1440 AND teardown_minutes BETWEEN 0 AND 1440)
);
CREATE TRIGGER banquet_rooms_set_updated_at BEFORE UPDATE ON banquet_rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE banquet_room_parts (
    property_id   bigint NOT NULL,
    combo_room_id bigint NOT NULL,
    part_room_id  bigint NOT NULL,
    PRIMARY KEY (combo_room_id, part_room_id),
    CONSTRAINT banquet_room_parts_combo_fk  FOREIGN KEY (property_id, combo_room_id) REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT banquet_room_parts_part_fk   FOREIGN KEY (property_id, part_room_id)  REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT banquet_room_parts_differ_ck CHECK (combo_room_id <> part_room_id)
);
CREATE INDEX banquet_room_parts_part_idx ON banquet_room_parts (property_id, part_room_id);

-- One level only: the combined room is a combined room, its part is not (and a room that has parts or is a part cannot change what it is).
-- +goose StatementBegin
CREATE FUNCTION banquet_room_parts_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    v_combo boolean;
    v_part  boolean;
BEGIN
    SELECT is_combo INTO v_combo FROM banquet_rooms WHERE property_id = NEW.property_id AND id = NEW.combo_room_id;
    SELECT is_combo INTO v_part  FROM banquet_rooms WHERE property_id = NEW.property_id AND id = NEW.part_room_id;
    IF NOT v_combo OR v_part THEN
        RAISE EXCEPTION 'a combined room is made of base rooms: room % is %, room % is %', NEW.combo_room_id, CASE WHEN v_combo THEN 'combined' ELSE 'a base room' END,
            NEW.part_room_id, CASE WHEN v_part THEN 'combined' ELSE 'a base room' END
            USING ERRCODE = 'check_violation', CONSTRAINT = 'banquet_room_parts_one_level';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER banquet_room_parts_check BEFORE INSERT OR UPDATE ON banquet_room_parts
    FOR EACH ROW EXECUTE FUNCTION banquet_room_parts_check();

-- +goose StatementBegin
CREATE FUNCTION banquet_rooms_kind_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.is_combo AND EXISTS (SELECT 1 FROM banquet_room_parts WHERE property_id = NEW.property_id AND part_room_id = NEW.id) THEN
        RAISE EXCEPTION 'room % is a part of a combined room and cannot be combined itself', NEW.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'banquet_room_parts_one_level';
    END IF;
    IF NOT NEW.is_combo AND EXISTS (SELECT 1 FROM banquet_room_parts WHERE property_id = NEW.property_id AND combo_room_id = NEW.id) THEN
        RAISE EXCEPTION 'room % has parts and stays a combined room', NEW.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'banquet_room_parts_one_level';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER banquet_rooms_kind_check BEFORE UPDATE OF is_combo ON banquet_rooms
    FOR EACH ROW EXECUTE FUNCTION banquet_rooms_kind_check();

CREATE TABLE banquet_room_setups (
    property_id bigint      NOT NULL,
    room_id     bigint      NOT NULL,
    setup_style varchar(12) NOT NULL,
    capacity    int         NOT NULL,
    PRIMARY KEY (room_id, setup_style),
    CONSTRAINT banquet_room_setups_room_fk  FOREIGN KEY (property_id, room_id) REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT banquet_room_setups_style_ck CHECK (setup_style IN ('THEATER', 'CLASSROOM', 'U_SHAPE', 'ROUND_TABLE', 'COCKTAIL', 'BOARDROOM')),
    CONSTRAINT banquet_room_setups_cap_ck   CHECK (capacity > 0)
);

-- 3. The other masters ----------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_event_types (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    is_active   boolean      NOT NULL DEFAULT true,
    CONSTRAINT banquet_event_types_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_event_types_code_uk        UNIQUE (property_id, code),
    CONSTRAINT banquet_event_types_property_id_uk UNIQUE (property_id, id)
);

CREATE TABLE banquet_reasons (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    kind        varchar(6)   NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    is_active   boolean      NOT NULL DEFAULT true,
    CONSTRAINT banquet_reasons_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_reasons_code_uk        UNIQUE (property_id, code),
    CONSTRAINT banquet_reasons_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_reasons_kind_ck        CHECK (kind IN ('LOST', 'CANCEL'))
);

CREATE TABLE banquet_items (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    code           varchar(20)   NOT NULL,
    name           varchar(100)  NOT NULL,
    kind           varchar(14)   NOT NULL,
    unit_basis     varchar(11)   NOT NULL,
    charge_code_id bigint        NOT NULL,
    list_price     numeric(18,3) NOT NULL DEFAULT 0,
    fb_class       varchar(8),
    pos_product_id bigint,
    description    text,
    is_active      boolean       NOT NULL DEFAULT true,
    CONSTRAINT banquet_items_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_items_code_uk        UNIQUE (property_id, code),
    CONSTRAINT banquet_items_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_items_charge_code_fk FOREIGN KEY (property_id, charge_code_id)  REFERENCES charge_codes (property_id, id),
    CONSTRAINT banquet_items_product_fk     FOREIGN KEY (property_id, pos_product_id)  REFERENCES pos_products (property_id, id),
    CONSTRAINT banquet_items_kind_ck        CHECK (kind IN ('MENU', 'DRINK_PACKAGE', 'ROOM_RENTAL', 'EQUIPMENT', 'DECOR', 'OTHER')),
    CONSTRAINT banquet_items_basis_ck       CHECK (unit_basis IN ('PER_PAX', 'PER_SESSION', 'PER_HOUR', 'PER_UNIT')),
    CONSTRAINT banquet_items_price_ck       CHECK (list_price >= 0),
    CONSTRAINT banquet_items_class_ck       CHECK (fb_class IS NULL OR fb_class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);

CREATE TABLE banquet_packages (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    code          varchar(20)   NOT NULL,
    name          varchar(100)  NOT NULL,
    price_per_pax numeric(18,3) NOT NULL DEFAULT 0,
    description   text,
    is_active     boolean       NOT NULL DEFAULT true,
    CONSTRAINT banquet_packages_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_packages_code_uk        UNIQUE (property_id, code),
    CONSTRAINT banquet_packages_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_packages_price_ck       CHECK (price_per_pax >= 0)
);

CREATE TABLE banquet_package_components (
    property_id bigint        NOT NULL,
    package_id  bigint        NOT NULL,
    item_id     bigint        NOT NULL,
    qty_per_pax numeric(10,3) NOT NULL,
    sort_order  int           NOT NULL DEFAULT 0,
    PRIMARY KEY (package_id, item_id),
    CONSTRAINT banquet_package_components_package_fk FOREIGN KEY (property_id, package_id) REFERENCES banquet_packages (property_id, id),
    CONSTRAINT banquet_package_components_item_fk    FOREIGN KEY (property_id, item_id)    REFERENCES banquet_items (property_id, id),
    CONSTRAINT banquet_package_components_qty_ck     CHECK (qty_per_pax > 0)
);

-- 4. Events and sessions --------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_events (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint       NOT NULL,
    property_id       bigint       NOT NULL,
    number            varchar(20)  NOT NULL,
    status            varchar(10)  NOT NULL DEFAULT 'INQUIRY',
    name              varchar(150) NOT NULL,
    event_type_id     bigint,
    booker_guest_id   bigint,
    booker_company_id bigint,
    contact_name      varchar(100),
    contact_phone     varchar(40),
    contact_email     varchar(150),
    sales_user_id     bigint       NOT NULL,
    option_until      date,
    reason_id         bigint,
    reason_note       text,
    booking_group_id  bigint,
    notes             text,
    inquiry_at        timestamptz  NOT NULL DEFAULT now(),
    definite_at       timestamptz,
    closed_at         timestamptz,
    version           int          NOT NULL DEFAULT 1,
    created_by        bigint REFERENCES users (id),
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    updated_by        bigint REFERENCES users (id),
    CONSTRAINT banquet_events_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_events_number_uk      UNIQUE (property_id, number),
    CONSTRAINT banquet_events_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_events_type_fk        FOREIGN KEY (property_id, event_type_id)       REFERENCES banquet_event_types (property_id, id),
    CONSTRAINT banquet_events_guest_fk       FOREIGN KEY (tenant_id, booker_guest_id)       REFERENCES guests (tenant_id, id),
    CONSTRAINT banquet_events_company_fk     FOREIGN KEY (property_id, booker_company_id)   REFERENCES companies (property_id, id),
    CONSTRAINT banquet_events_sales_fk       FOREIGN KEY (tenant_id, sales_user_id)         REFERENCES users (tenant_id, id),
    CONSTRAINT banquet_events_reason_fk      FOREIGN KEY (property_id, reason_id)           REFERENCES banquet_reasons (property_id, id),
    CONSTRAINT banquet_events_group_fk       FOREIGN KEY (property_id, booking_group_id)    REFERENCES booking_groups (property_id, id),
    CONSTRAINT banquet_events_status_ck      CHECK (status IN ('INQUIRY', 'TENTATIVE', 'DEFINITE', 'COMPLETED', 'CANCELLED', 'LOST')),
    CONSTRAINT banquet_events_booker_ck      CHECK ((booker_guest_id IS NULL) <> (booker_company_id IS NULL)),
    CONSTRAINT banquet_events_option_ck      CHECK (status <> 'TENTATIVE' OR option_until IS NOT NULL),
    CONSTRAINT banquet_events_reason_ck      CHECK (status NOT IN ('CANCELLED', 'LOST') OR reason_id IS NOT NULL),
    CONSTRAINT banquet_events_definite_ck    CHECK (status NOT IN ('DEFINITE', 'COMPLETED') OR definite_at IS NOT NULL),
    CONSTRAINT banquet_events_closed_ck      CHECK ((status IN ('COMPLETED', 'CANCELLED', 'LOST')) = (closed_at IS NOT NULL))
);
CREATE INDEX banquet_events_status_idx ON banquet_events (property_id, status);
CREATE INDEX banquet_events_sales_idx  ON banquet_events (property_id, sales_user_id);
CREATE TRIGGER banquet_events_set_updated_at BEFORE UPDATE ON banquet_events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE banquet_sessions (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    event_id       bigint       NOT NULL,
    session_no     int          NOT NULL,
    name           varchar(150) NOT NULL,
    session_date   date         NOT NULL,
    start_time     time,
    end_time       time,
    ends_next_day  boolean      NOT NULL DEFAULT false,
    room_id        bigint,
    setup_style    varchar(12),
    pax_estimated  int          NOT NULL,
    pax_guaranteed int,
    pax_actual     int,
    status         varchar(10)  NOT NULL DEFAULT 'ACTIVE',
    notes          text,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT banquet_sessions_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_sessions_event_fk       FOREIGN KEY (property_id, event_id)     REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_sessions_room_fk        FOREIGN KEY (property_id, room_id)      REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT banquet_sessions_no_uk          UNIQUE (event_id, session_no),
    CONSTRAINT banquet_sessions_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_sessions_status_ck      CHECK (status IN ('ACTIVE', 'CANCELLED')),
    CONSTRAINT banquet_sessions_style_ck       CHECK (setup_style IS NULL OR setup_style IN ('THEATER', 'CLASSROOM', 'U_SHAPE', 'ROUND_TABLE', 'COCKTAIL', 'BOARDROOM')),
    CONSTRAINT banquet_sessions_times_ck       CHECK ((start_time IS NULL) = (end_time IS NULL)),
    CONSTRAINT banquet_sessions_span_ck        CHECK (start_time IS NULL OR (ends_next_day AND start_time <> end_time) OR (NOT ends_next_day AND end_time > start_time)),
    CONSTRAINT banquet_sessions_pax_ck         CHECK (pax_estimated > 0 AND (pax_guaranteed IS NULL OR pax_guaranteed > 0) AND (pax_actual IS NULL OR pax_actual > 0))
);
CREATE INDEX banquet_sessions_date_idx ON banquet_sessions (property_id, session_date);
CREATE TRIGGER banquet_sessions_set_updated_at BEFORE UPDATE ON banquet_sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 5. The holds ------------------------------------------------------------------------------------------------------------------------------------
-- One row for each base room a session covers. "during" is local time, from the start of the set-up to the end of the tear-down. OPTION holds queue (their rank is
-- derived from the order of the ids); only a DEFINITE hold is exclusive.
CREATE TABLE banquet_holds (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint      NOT NULL,
    property_id  bigint      NOT NULL,
    session_id   bigint      NOT NULL,
    event_id     bigint      NOT NULL,
    atom_room_id bigint      NOT NULL,
    during       tsrange     NOT NULL,
    hold_kind    varchar(10) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banquet_holds_session_fk       FOREIGN KEY (property_id, session_id)    REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_holds_event_fk         FOREIGN KEY (property_id, event_id)      REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_holds_room_fk          FOREIGN KEY (property_id, atom_room_id)  REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT banquet_holds_session_room_uk  UNIQUE (session_id, atom_room_id),
    CONSTRAINT banquet_holds_kind_ck          CHECK (hold_kind IN ('OPTION', 'DEFINITE')),
    CONSTRAINT banquet_holds_range_ck         CHECK (NOT isempty(during) AND NOT lower_inf(during) AND NOT upper_inf(during)),
    CONSTRAINT banquet_holds_no_overlap_ex    EXCLUDE USING gist (atom_room_id WITH =, during WITH &&) WHERE (hold_kind = 'DEFINITE')
);
CREATE INDEX banquet_holds_room_idx  ON banquet_holds USING gist (atom_room_id, during);
CREATE INDEX banquet_holds_event_idx ON banquet_holds (property_id, event_id);

-- The guard behind the service: a DEFINITE event with an active session that lacks a DEFINITE hold on a base room it covers cannot be committed.
-- +goose StatementBegin
CREATE FUNCTION banquet_holds_missing_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    v_event bigint;
    v_prop  bigint;
    v_bad   bigint;
BEGIN
    IF TG_TABLE_NAME = 'banquet_events' THEN
        v_event := NEW.id; v_prop := NEW.property_id;
    ELSIF TG_OP = 'DELETE' THEN
        v_event := OLD.event_id; v_prop := OLD.property_id;
    ELSE
        v_event := NEW.event_id; v_prop := NEW.property_id;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM banquet_events WHERE property_id = v_prop AND id = v_event AND status = 'DEFINITE') THEN
        RETURN NULL;
    END IF;
    SELECT s.id INTO v_bad
      FROM banquet_sessions s
      JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
     CROSS JOIN LATERAL (SELECT p.part_room_id AS atom FROM banquet_room_parts p WHERE p.combo_room_id = r.id
                          UNION ALL
                         SELECT r.id WHERE NOT r.is_combo) a
     WHERE s.property_id = v_prop AND s.event_id = v_event AND s.status = 'ACTIVE'
       AND NOT EXISTS (SELECT 1 FROM banquet_holds h WHERE h.session_id = s.id AND h.atom_room_id = a.atom AND h.hold_kind = 'DEFINITE')
     LIMIT 1;
    IF v_bad IS NULL AND EXISTS (SELECT 1 FROM banquet_sessions s WHERE s.property_id = v_prop AND s.event_id = v_event AND s.status = 'ACTIVE' AND (s.room_id IS NULL OR s.start_time IS NULL)) THEN
        SELECT s.id INTO v_bad FROM banquet_sessions s WHERE s.property_id = v_prop AND s.event_id = v_event AND s.status = 'ACTIVE' AND (s.room_id IS NULL OR s.start_time IS NULL) LIMIT 1;
    END IF;
    IF v_bad IS NOT NULL THEN
        RAISE EXCEPTION 'event % is definite but session % has no hold on its room', v_event, v_bad
            USING ERRCODE = 'check_violation', CONSTRAINT = 'banquet_holds_missing';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER banquet_events_holds_check AFTER UPDATE ON banquet_events
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION banquet_holds_missing_check();
CREATE CONSTRAINT TRIGGER banquet_sessions_holds_check AFTER INSERT OR UPDATE ON banquet_sessions
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION banquet_holds_missing_check();
CREATE CONSTRAINT TRIGGER banquet_holds_holds_check AFTER UPDATE OR DELETE ON banquet_holds
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION banquet_holds_missing_check();

-- 6. The notices ----------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_notices (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    user_id     bigint      NOT NULL,
    kind        varchar(15) NOT NULL,
    event_id    bigint      NOT NULL,
    session_id  bigint,
    notice_date date        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    seen_at     timestamptz,
    CONSTRAINT banquet_notices_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_notices_user_fk     FOREIGN KEY (tenant_id, user_id)      REFERENCES users (tenant_id, id),
    CONSTRAINT banquet_notices_event_fk    FOREIGN KEY (property_id, event_id)   REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_notices_session_fk  FOREIGN KEY (property_id, session_id) REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_notices_kind_ck     CHECK (kind IN ('OPTION_PROMOTED', 'OPTION_DUE', 'OPTION_LOST'))
);
CREATE INDEX banquet_notices_user_idx ON banquet_notices (property_id, user_id, created_at DESC);
-- the day close writes the reminder of an option date once for an event, a person and a day (36-banquet.md F9)
CREATE UNIQUE INDEX banquet_notices_due_uk ON banquet_notices (property_id, event_id, user_id, notice_date) WHERE kind = 'OPTION_DUE';

-- 7. The number of an event -----------------------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'BANQUET_EVENT', 'BQ-' FROM properties;

-- +goose Down
-- Refused while the banquet holds anything: a room, an event or a master.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM banquet_events) + (SELECT count(*) FROM banquet_rooms) + (SELECT count(*) FROM banquet_items)
         + (SELECT count(*) FROM banquet_packages) + (SELECT count(*) FROM banquet_event_types) + (SELECT count(*) FROM banquet_reasons) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00089: the banquet holds % row(s) (rooms, masters, events)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM document_sequences WHERE sequence_type = 'BANQUET_EVENT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT'));

DROP TABLE banquet_notices;
DROP TABLE banquet_holds;
DROP TABLE banquet_sessions;
DROP TABLE banquet_events;
DROP FUNCTION banquet_holds_missing_check();
DROP TABLE banquet_package_components;
DROP TABLE banquet_packages;
DROP TABLE banquet_items;
DROP TABLE banquet_reasons;
DROP TABLE banquet_event_types;
DROP TABLE banquet_room_setups;
DROP TABLE banquet_room_parts;
DROP FUNCTION banquet_room_parts_check();
DROP TABLE banquet_rooms;
DROP FUNCTION banquet_rooms_kind_check();
DROP TABLE banquet_settings;
