-- +goose Up
-- Yield management: rules that adjust the price a night is sold at. The rate grid (`rates`) stays the base price; a rule
-- matches a night by conditions (rate plan, room type, stay dates, weekdays, how full the property is that night, days
-- before arrival, length of stay) and adds a percentage or an amount. Rules apply in priority order, each to the price the
-- one before left. A reservation stores the price it was sold at (`base_rate`, as before) and, new, the grid price and the
-- codes of the rules that moved it, so a booking can always be explained.

CREATE TABLE yield_rules (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    code              varchar(20)   NOT NULL,
    name              varchar(100)  NOT NULL,
    rate_plan_id      bigint,
    room_type_id      bigint,
    stay_from         date,
    stay_to           date,
    weekdays          text[],
    occupancy_from    numeric(5,2),
    occupancy_to      numeric(5,2),
    lead_min          integer,
    lead_max          integer,
    stay_min          integer,
    stay_max          integer,
    adjustment_type   varchar(7)    NOT NULL,
    adjustment_value  numeric(18,4) NOT NULL,
    floor_amount      numeric(18,3),
    cap_amount        numeric(18,3),
    priority          integer       NOT NULL DEFAULT 100,
    is_active         boolean       NOT NULL DEFAULT true,
    created_at        timestamptz   NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    updated_by        bigint REFERENCES users (id),
    CONSTRAINT yield_rules_property_fk      FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT yield_rules_rate_plan_fk     FOREIGN KEY (property_id, rate_plan_id)        REFERENCES rate_plans (property_id, id),
    CONSTRAINT yield_rules_room_type_fk     FOREIGN KEY (property_id, room_type_id)        REFERENCES room_types (property_id, id),
    CONSTRAINT yield_rules_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT yield_rules_type_ck          CHECK (adjustment_type IN ('PERCENT', 'AMOUNT')),
    CONSTRAINT yield_rules_value_ck         CHECK (adjustment_value <> 0 AND (adjustment_type <> 'PERCENT' OR (adjustment_value > -100 AND adjustment_value <= 1000))),
    CONSTRAINT yield_rules_stay_ck          CHECK (stay_from IS NULL OR stay_to IS NULL OR stay_from <= stay_to),
    CONSTRAINT yield_rules_occupancy_ck     CHECK ((occupancy_from IS NULL OR (occupancy_from >= 0 AND occupancy_from <= 100))
                                               AND (occupancy_to IS NULL OR (occupancy_to > 0 AND occupancy_to <= 100))
                                               AND (occupancy_from IS NULL OR occupancy_to IS NULL OR occupancy_from < occupancy_to)),
    CONSTRAINT yield_rules_lead_ck          CHECK ((lead_min IS NULL OR lead_min >= 0) AND (lead_max IS NULL OR lead_max >= 0)
                                               AND (lead_min IS NULL OR lead_max IS NULL OR lead_min <= lead_max)),
    CONSTRAINT yield_rules_los_ck           CHECK ((stay_min IS NULL OR stay_min >= 1) AND (stay_max IS NULL OR stay_max >= 1)
                                               AND (stay_min IS NULL OR stay_max IS NULL OR stay_min <= stay_max)),
    CONSTRAINT yield_rules_weekdays_ck      CHECK (weekdays IS NULL OR (cardinality(weekdays) BETWEEN 1 AND 7
                                               AND weekdays <@ ARRAY['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'])),
    CONSTRAINT yield_rules_bounds_ck        CHECK ((floor_amount IS NULL OR floor_amount >= 0) AND (cap_amount IS NULL OR cap_amount >= 0)
                                               AND (floor_amount IS NULL OR cap_amount IS NULL OR floor_amount <= cap_amount))
);
CREATE INDEX yield_rules_active_idx ON yield_rules (property_id, priority, id) WHERE is_active;
CREATE TRIGGER yield_rules_set_updated_at BEFORE UPDATE ON yield_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE reservation_room_rates
    ADD COLUMN grid_rate   numeric(18,2),
    ADD COLUMN yield_rules text[];

-- +goose Down
ALTER TABLE reservation_room_rates DROP COLUMN yield_rules, DROP COLUMN grid_rate;
DROP TABLE IF EXISTS yield_rules;
