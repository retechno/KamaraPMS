-- +goose Up
-- Rate plan = pricing strategy. It selects the ROOM charge code (which owns tax/service rules
-- and the price mode). No tax_id / service_charge_id here by design.
CREATE TABLE rate_plans (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    code                 varchar(20)  NOT NULL,
    name                 varchar(100) NOT NULL,
    description          text,
    meal_plan            varchar(3)   NOT NULL DEFAULT 'RO',
    cancellation_policy  text,
    is_refundable        boolean      NOT NULL DEFAULT true,
    room_charge_code_id  bigint       NOT NULL,
    is_active            boolean      NOT NULL DEFAULT true,
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT rate_plans_property_fk      FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT rate_plans_charge_code_fk   FOREIGN KEY (property_id, room_charge_code_id) REFERENCES charge_codes (property_id, id),
    CONSTRAINT rate_plans_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT rate_plans_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT rate_plans_meal_plan_ck     CHECK (meal_plan IN ('RO', 'BB', 'HB', 'FB', 'AI'))
);
CREATE INDEX rate_plans_charge_code_idx ON rate_plans (room_charge_code_id);
CREATE TRIGGER rate_plans_set_updated_at BEFORE UPDATE ON rate_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementBegin
CREATE FUNCTION rate_plans_room_code_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM assert_room_charge_code(NEW.property_id, NEW.room_charge_code_id);
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER rate_plans_room_code_guard BEFORE INSERT OR UPDATE OF room_charge_code_id ON rate_plans
    FOR EACH ROW EXECUTE FUNCTION rate_plans_room_code_guard();

-- Rate grid: one row per (plan, room type, night), in the room charge code's price mode.
CREATE TABLE rates (
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    rate_plan_id  bigint        NOT NULL,
    room_type_id  bigint        NOT NULL,
    stay_date     date          NOT NULL,
    amount        numeric(18,2) NOT NULL,
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    PRIMARY KEY (rate_plan_id, room_type_id, stay_date),
    CONSTRAINT rates_property_fk  FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT rates_rate_plan_fk FOREIGN KEY (property_id, rate_plan_id) REFERENCES rate_plans (property_id, id),
    CONSTRAINT rates_room_type_fk FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT rates_amount_ck    CHECK (amount >= 0)
);
CREATE INDEX rates_room_type_date_idx ON rates (property_id, room_type_id, stay_date);
CREATE TRIGGER rates_set_updated_at BEFORE UPDATE ON rates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS rates;
DROP TABLE IF EXISTS rate_plans;
DROP FUNCTION IF EXISTS rate_plans_room_code_guard();
