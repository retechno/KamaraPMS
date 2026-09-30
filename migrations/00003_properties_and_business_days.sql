-- +goose Up
CREATE TABLE properties (
    id                                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                            bigint       NOT NULL REFERENCES tenants (id),
    code                                 varchar(20)  NOT NULL,
    name                                 varchar(200) NOT NULL,
    address                              varchar(300),
    city                                 varchar(100),
    country_code                         char(2),
    timezone                             varchar(64)  NOT NULL,
    currency_code                        char(3)      NOT NULL,
    currency_decimals                    smallint     NOT NULL,
    check_in_time                        time         NOT NULL,
    check_out_time                       time         NOT NULL,
    require_room_inspection_for_checkin  boolean      NOT NULL DEFAULT false,
    night_audit_marks_occupied_dirty     boolean      NOT NULL DEFAULT true,
    night_audit_earliest_time            time         NOT NULL DEFAULT '20:00',
    status                               varchar(10)  NOT NULL DEFAULT 'ACTIVE',
    created_at                           timestamptz  NOT NULL DEFAULT now(),
    created_by                           bigint REFERENCES users (id),
    updated_at                           timestamptz  NOT NULL DEFAULT now(),
    updated_by                           bigint REFERENCES users (id),
    CONSTRAINT properties_tenant_code_uk   UNIQUE (tenant_id, code),
    CONSTRAINT properties_tenant_id_uk     UNIQUE (tenant_id, id),
    CONSTRAINT properties_currency_ck      CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT properties_decimals_ck      CHECK (currency_decimals BETWEEN 0 AND 3),
    CONSTRAINT properties_country_ck       CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$'),
    CONSTRAINT properties_status_ck        CHECK (status IN ('ACTIVE', 'INACTIVE'))
);
CREATE TRIGGER properties_set_updated_at BEFORE UPDATE ON properties
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- The currency lock trigger is created in 00010 (it needs folio_items).

-- Business day = the ONLY source of truth for the property business date.
-- Exactly one OPEN row per property; closed rows are immutable history.
CREATE TABLE business_days (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    business_date  date        NOT NULL,
    status         varchar(10) NOT NULL DEFAULT 'OPEN',
    opened_at      timestamptz NOT NULL DEFAULT now(),
    opened_by      bigint REFERENCES users (id),
    closed_at      timestamptz,
    closed_by      bigint REFERENCES users (id),
    summary        jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT business_days_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT business_days_property_date_uk UNIQUE (property_id, business_date),
    CONSTRAINT business_days_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT business_days_status_ck        CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT business_days_closed_ck        CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL))
);
CREATE UNIQUE INDEX business_days_one_open_uk ON business_days (property_id) WHERE status = 'OPEN';

-- Guards: new days are OPEN and consecutive; CLOSED days never change; nothing is deleted.
-- +goose StatementBegin
CREATE FUNCTION business_days_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_last date;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'OPEN' THEN
            RAISE EXCEPTION 'a new business day must be OPEN'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'business_days_open_on_insert';
        END IF;
        SELECT max(business_date) INTO v_last FROM business_days WHERE property_id = NEW.property_id;
        IF v_last IS NOT NULL AND NEW.business_date <> v_last + 1 THEN
            RAISE EXCEPTION 'business day % is not consecutive to % for property %',
                NEW.business_date, v_last, NEW.property_id
                USING ERRCODE = 'check_violation', CONSTRAINT = 'business_days_consecutive';
        END IF;
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        IF OLD.status = 'CLOSED' THEN
            RAISE EXCEPTION 'business day % is CLOSED and immutable', OLD.business_date
                USING ERRCODE = 'restrict_violation', CONSTRAINT = 'business_days_closed_immutable';
        END IF;
        IF NEW.business_date <> OLD.business_date
           OR NEW.property_id <> OLD.property_id
           OR NEW.tenant_id <> OLD.tenant_id THEN
            RAISE EXCEPTION 'business day identity cannot change'
                USING ERRCODE = 'restrict_violation', CONSTRAINT = 'business_days_identity_immutable';
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'business days cannot be deleted'
        USING ERRCODE = 'restrict_violation', CONSTRAINT = 'business_days_no_delete';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER business_days_guard BEFORE INSERT OR UPDATE OR DELETE ON business_days
    FOR EACH ROW EXECUTE FUNCTION business_days_guard();
CREATE TRIGGER business_days_set_updated_at BEFORE UPDATE ON business_days
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Gapless, per-property numbering: UPDATE ... SET next_value = next_value + 1 RETURNING next_value - 1
-- inside the business transaction (a rollback leaves no gap).
CREATE TABLE document_sequences (
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    sequence_type  varchar(20) NOT NULL,
    prefix         varchar(10) NOT NULL,
    next_value     bigint      NOT NULL DEFAULT 1,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (property_id, sequence_type),
    CONSTRAINT document_sequences_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT document_sequences_type_ck     CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT')),
    CONSTRAINT document_sequences_next_ck     CHECK (next_value >= 1)
);
CREATE TRIGGER document_sequences_set_updated_at BEFORE UPDATE ON document_sequences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One role per (user, property). Composite FKs make cross-tenant grants impossible.
CREATE TABLE user_properties (
    tenant_id    bigint      NOT NULL,
    user_id      bigint      NOT NULL,
    property_id  bigint      NOT NULL,
    role_id      bigint      NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    PRIMARY KEY (user_id, property_id),
    CONSTRAINT user_properties_user_fk     FOREIGN KEY (tenant_id, user_id)     REFERENCES users (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT user_properties_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT user_properties_role_fk     FOREIGN KEY (tenant_id, role_id)     REFERENCES roles (tenant_id, id)
);
CREATE INDEX user_properties_property_idx ON user_properties (property_id);
CREATE INDEX user_properties_role_idx     ON user_properties (role_id);

-- +goose Down
DROP TABLE IF EXISTS user_properties;
DROP TABLE IF EXISTS document_sequences;
DROP TABLE IF EXISTS business_days;
DROP FUNCTION IF EXISTS business_days_guard();
DROP TABLE IF EXISTS properties;
