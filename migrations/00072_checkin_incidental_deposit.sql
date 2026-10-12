-- +goose Up
-- The incidental deposit the front desk proposes at check-in, per room, on top of the stay estimate. It is a setting of the property, 0 by default (nothing is proposed):
-- the system does not invent an amount. The desk can change it for a guest; it is a proposal, never enforced by the check-in.
ALTER TABLE properties ADD COLUMN checkin_incidental_deposit numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE properties ADD CONSTRAINT properties_incidental_deposit_ck CHECK (checkin_incidental_deposit >= 0);

-- +goose Down
ALTER TABLE properties DROP CONSTRAINT properties_incidental_deposit_ck;
ALTER TABLE properties DROP COLUMN checkin_incidental_deposit;
