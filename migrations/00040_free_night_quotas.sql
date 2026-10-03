-- +goose Up
-- A monthly limit of free nights per property and occupancy kind (complimentary, house use). A kind without a row has
-- no limit. The limit is checked when a free room is booked; going over it takes a manager who knowingly approves it.
CREATE TABLE free_night_quotas (
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    occupancy_kind varchar(14)  NOT NULL,
    monthly_nights integer      NOT NULL,
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    updated_by     bigint REFERENCES users (id),
    PRIMARY KEY (property_id, occupancy_kind),
    CONSTRAINT free_night_quotas_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT free_night_quotas_kind_ck     CHECK (occupancy_kind IN ('COMPLIMENTARY', 'HOUSE_USE')),
    CONSTRAINT free_night_quotas_nights_ck   CHECK (monthly_nights >= 0)
);

-- +goose Down
DROP TABLE free_night_quotas;
