-- +goose Up
-- Sales restrictions (design: docs/architecture/18-architecture-decisions.md, decision 2): a daily grid per room type and rate plan (each optional: NULL is "all") that says whether a night
-- can be sold (stop sell), whether a date can be an arrival (closed to arrival) or a departure (closed to departure), and the shortest and longest stay that may start on a date.
-- An attribute that is NULL has no opinion at that level: the most specific row that has a value for it decides (see availability.Resolve). A restriction is configuration, not a
-- financial record: the table is a mutable grid like `rates`, and its history is the audit trail.
CREATE TABLE rate_restrictions (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    room_type_id         bigint,
    rate_plan_id         bigint,
    stay_date            date         NOT NULL,
    stop_sell            boolean,
    closed_to_arrival    boolean,
    closed_to_departure  boolean,
    min_stay             smallint,
    max_stay             smallint,
    note                 varchar(200),
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT rr_property_fk FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT rr_type_fk     FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT rr_plan_fk     FOREIGN KEY (property_id, rate_plan_id) REFERENCES rate_plans (property_id, id),
    CONSTRAINT rr_some_ck     CHECK (stop_sell IS NOT NULL OR closed_to_arrival IS NOT NULL OR closed_to_departure IS NOT NULL OR min_stay IS NOT NULL OR max_stay IS NOT NULL),
    CONSTRAINT rr_min_ck      CHECK (min_stay IS NULL OR min_stay BETWEEN 1 AND 365),
    CONSTRAINT rr_max_ck      CHECK (max_stay IS NULL OR max_stay BETWEEN 1 AND 365),
    CONSTRAINT rr_range_ck    CHECK (min_stay IS NULL OR max_stay IS NULL OR min_stay <= max_stay)
);
-- One row per scope and date. A NULL scope is "all", so it is coalesced into the key (a plain unique constraint treats NULLs as different).
CREATE UNIQUE INDEX rr_scope_uk ON rate_restrictions (property_id, COALESCE(room_type_id, 0), COALESCE(rate_plan_id, 0), stay_date);
CREATE INDEX rr_date_idx ON rate_restrictions (property_id, stay_date);
CREATE TRIGGER rate_restrictions_set_updated_at BEFORE UPDATE ON rate_restrictions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE rate_restrictions;
