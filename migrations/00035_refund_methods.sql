-- +goose Up
-- The methods a refund may leave by, set per property. Front office refunds in cash by default whatever the guest
-- paid with; a property can allow bank transfer, card or other as well.
ALTER TABLE properties ADD COLUMN refund_methods text[] NOT NULL DEFAULT ARRAY['CASH'];
ALTER TABLE properties ADD CONSTRAINT properties_refund_methods_ck
    CHECK (cardinality(refund_methods) >= 1 AND refund_methods <@ ARRAY['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER']);

-- +goose Down
ALTER TABLE properties DROP CONSTRAINT properties_refund_methods_ck;
ALTER TABLE properties DROP COLUMN refund_methods;
