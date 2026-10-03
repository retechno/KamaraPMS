-- +goose Up
-- A rate plan has an occupancy kind: PAID (the normal case), COMPLIMENTARY (a guest who does not pay) or HOUSE_USE
-- (a room used by the hotel itself). A line on a non-PAID plan is priced at zero and carries the reason it was given
-- free. The kind is fixed when the plan is created, so the history of a plan never changes meaning.
ALTER TABLE rate_plans
    ADD COLUMN occupancy_kind varchar(14) NOT NULL DEFAULT 'PAID',
    ADD CONSTRAINT rate_plans_occupancy_kind_ck CHECK (occupancy_kind IN ('PAID', 'COMPLIMENTARY', 'HOUSE_USE'));

ALTER TABLE reservation_rooms ADD COLUMN occupancy_reason varchar(500);

-- +goose Down
ALTER TABLE reservation_rooms DROP COLUMN occupancy_reason;
ALTER TABLE rate_plans DROP CONSTRAINT rate_plans_occupancy_kind_ck, DROP COLUMN occupancy_kind;
