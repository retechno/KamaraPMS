-- +goose Up
-- One paid rate plan per property can be the reference plan: the grid price of that plan is what a complimentary or house
-- use night would have cost, which is how the report of free rooms puts a value on them.
ALTER TABLE rate_plans
    ADD COLUMN is_reference boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT rate_plans_reference_paid_ck CHECK (NOT is_reference OR occupancy_kind = 'PAID');
CREATE UNIQUE INDEX rate_plans_reference_uk ON rate_plans (property_id) WHERE is_reference;

-- +goose Down
DROP INDEX rate_plans_reference_uk;
ALTER TABLE rate_plans DROP CONSTRAINT rate_plans_reference_paid_ck, DROP COLUMN is_reference;
