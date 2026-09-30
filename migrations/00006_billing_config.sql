-- +goose Up
-- Tax. `rate` is a percent (11.0000 = 11%). No is_inclusive: inclusivity belongs to the price.
CREATE TABLE taxes (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    code            varchar(20)  NOT NULL,
    name            varchar(100) NOT NULL,
    rate            numeric(7,4) NOT NULL,
    tax_on_service  boolean      NOT NULL DEFAULT false,
    is_active       boolean      NOT NULL DEFAULT true,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT taxes_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT taxes_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT taxes_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT taxes_rate_ck          CHECK (rate BETWEEN 0 AND 100)
);
CREATE TRIGGER taxes_set_updated_at BEFORE UPDATE ON taxes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE service_charges (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint       NOT NULL,
    property_id  bigint       NOT NULL,
    code         varchar(20)  NOT NULL,
    name         varchar(100) NOT NULL,
    rate         numeric(7,4) NOT NULL,
    is_active    boolean      NOT NULL DEFAULT true,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    CONSTRAINT service_charges_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT service_charges_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT service_charges_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT service_charges_rate_ck          CHECK (rate BETWEEN 0 AND 100)
);
CREATE TRIGGER service_charges_set_updated_at BEFORE UPDATE ON service_charges
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Charge code = what is charged. Owns the tax/service rules through the mapping tables.
-- price_mode is a string so a future mode (e.g. MIXED) is a CHECK change, not a redesign.
-- The "price_mode immutable once used" trigger is created in 00010.
CREATE TABLE charge_codes (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    code                varchar(20)   NOT NULL,
    name                varchar(100)  NOT NULL,
    charge_type         varchar(20)   NOT NULL,
    price_mode          varchar(12)   NOT NULL DEFAULT 'EXCLUSIVE',
    default_unit_price  numeric(18,2),
    is_system           boolean       NOT NULL DEFAULT false,
    is_active           boolean       NOT NULL DEFAULT true,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    CONSTRAINT charge_codes_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT charge_codes_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT charge_codes_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT charge_codes_type_ck          CHECK (charge_type IN ('ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER')),
    CONSTRAINT charge_codes_price_mode_ck    CHECK (price_mode IN ('EXCLUSIVE', 'INCLUSIVE')),
    CONSTRAINT charge_codes_price_ck         CHECK (default_unit_price IS NULL OR default_unit_price >= 0)
);
CREATE TRIGGER charge_codes_set_updated_at BEFORE UPDATE ON charge_codes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Ordered, activatable mappings. `sequence` = calculation/display order.
CREATE TABLE charge_code_taxes (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint      NOT NULL,
    property_id     bigint      NOT NULL,
    charge_code_id  bigint      NOT NULL,
    tax_id          bigint      NOT NULL,
    sequence        smallint    NOT NULL,
    is_active       boolean     NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    CONSTRAINT charge_code_taxes_property_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT charge_code_taxes_charge_code_fk FOREIGN KEY (property_id, charge_code_id) REFERENCES charge_codes (property_id, id),
    CONSTRAINT charge_code_taxes_tax_fk         FOREIGN KEY (property_id, tax_id)         REFERENCES taxes (property_id, id),
    CONSTRAINT charge_code_taxes_pair_uk        UNIQUE (charge_code_id, tax_id),
    CONSTRAINT charge_code_taxes_sequence_ck    CHECK (sequence >= 1)
);
CREATE UNIQUE INDEX charge_code_taxes_sequence_uk ON charge_code_taxes (charge_code_id, sequence) WHERE is_active;
CREATE INDEX charge_code_taxes_tax_idx ON charge_code_taxes (tax_id);

CREATE TABLE charge_code_service_charges (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint      NOT NULL,
    property_id        bigint      NOT NULL,
    charge_code_id     bigint      NOT NULL,
    service_charge_id  bigint      NOT NULL,
    sequence           smallint    NOT NULL,
    is_active          boolean     NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    created_by         bigint REFERENCES users (id),
    CONSTRAINT charge_code_service_charges_property_fk    FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT charge_code_service_charges_charge_code_fk FOREIGN KEY (property_id, charge_code_id)    REFERENCES charge_codes (property_id, id),
    CONSTRAINT charge_code_service_charges_service_fk     FOREIGN KEY (property_id, service_charge_id) REFERENCES service_charges (property_id, id),
    CONSTRAINT charge_code_service_charges_pair_uk        UNIQUE (charge_code_id, service_charge_id),
    CONSTRAINT charge_code_service_charges_sequence_ck    CHECK (sequence >= 1)
);
CREATE UNIQUE INDEX charge_code_service_charges_sequence_uk ON charge_code_service_charges (charge_code_id, sequence) WHERE is_active;
CREATE INDEX charge_code_service_charges_service_idx ON charge_code_service_charges (service_charge_id);

-- Shared assertion: a charge code used for room revenue must have charge_type = ROOM.
-- +goose StatementBegin
CREATE FUNCTION assert_room_charge_code(p_property_id bigint, p_charge_code_id bigint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v_type varchar(20);
BEGIN
    SELECT charge_type INTO v_type FROM charge_codes
     WHERE property_id = p_property_id AND id = p_charge_code_id;
    IF v_type IS DISTINCT FROM 'ROOM' THEN
        RAISE EXCEPTION 'charge code % must have charge_type ROOM (found %)', p_charge_code_id, v_type
            USING ERRCODE = 'check_violation', CONSTRAINT = 'room_charge_code_type';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS assert_room_charge_code(bigint, bigint);
DROP TABLE IF EXISTS charge_code_service_charges;
DROP TABLE IF EXISTS charge_code_taxes;
DROP TABLE IF EXISTS charge_codes;
DROP TABLE IF EXISTS service_charges;
DROP TABLE IF EXISTS taxes;
