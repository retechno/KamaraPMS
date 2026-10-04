-- +goose Up
-- The fee (MDR) the acquirer is expected to keep from card and e-wallet payments, and when it pays out (design: docs/architecture/11-cashier-budget-cashflow-card.md,
-- part C). A rate is a rule with the date it starts; a payment keeps a snapshot of the rate that applied on its business date, the fee computed from it and the
-- date the payout is expected, so what was expected never moves when a rate changes. Nothing here is booked: the settlement journal still books what the bank paid.

CREATE TABLE card_fee_rules (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL,
    property_id      bigint       NOT NULL,
    payment_method   varchar(15)  NOT NULL,
    mdr_rate         numeric(7,4) NOT NULL,
    settlement_days  smallint     NOT NULL DEFAULT 1,
    effective_from   date         NOT NULL,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    CONSTRAINT cfr_property_fk  FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT cfr_start_uk     UNIQUE (property_id, payment_method, effective_from),
    CONSTRAINT cfr_method_ck    CHECK (payment_method IN ('CARD', 'OTHER')),
    CONSTRAINT cfr_rate_ck      CHECK (mdr_rate BETWEEN 0 AND 100),
    CONSTRAINT cfr_days_ck      CHECK (settlement_days BETWEEN 0 AND 60)
);
-- A rule is never changed: a new rate is a new rule from a later date.
CREATE TRIGGER card_fee_rules_append_only BEFORE UPDATE OR DELETE ON card_fee_rules FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER card_fee_rules_no_truncate BEFORE TRUNCATE ON card_fee_rules FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The snapshot on a card or e-wallet payment (a refund has none) and on a city ledger receipt by card.
ALTER TABLE payments ADD COLUMN mdr_rate numeric(7,4);
ALTER TABLE payments ADD COLUMN mdr_fee numeric(18,3);
ALTER TABLE payments ADD COLUMN expected_settlement_date date;
ALTER TABLE payments ADD CONSTRAINT payments_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_type = 'PAYMENT' AND payment_method IN ('CARD', 'OTHER'))));
ALTER TABLE city_ledger_receipts ADD COLUMN mdr_rate numeric(7,4);
ALTER TABLE city_ledger_receipts ADD COLUMN mdr_fee numeric(18,3);
ALTER TABLE city_ledger_receipts ADD COLUMN expected_settlement_date date;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_method IN ('CARD', 'OTHER'))));

-- What a settlement was expected to cost, from the snapshots of the payments it settled (null for the settlements made before this).
ALTER TABLE card_settlements ADD COLUMN expected_fee numeric(18,3);
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_expected_ck CHECK (expected_fee IS NULL OR expected_fee >= 0);

-- +goose Down
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_expected_ck;
ALTER TABLE card_settlements DROP COLUMN expected_fee;
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_mdr_ck;
ALTER TABLE city_ledger_receipts DROP COLUMN expected_settlement_date;
ALTER TABLE city_ledger_receipts DROP COLUMN mdr_fee;
ALTER TABLE city_ledger_receipts DROP COLUMN mdr_rate;
ALTER TABLE payments DROP CONSTRAINT payments_mdr_ck;
ALTER TABLE payments DROP COLUMN expected_settlement_date;
ALTER TABLE payments DROP COLUMN mdr_fee;
ALTER TABLE payments DROP COLUMN mdr_rate;
DROP TABLE IF EXISTS card_fee_rules;
