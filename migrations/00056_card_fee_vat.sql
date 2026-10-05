-- +goose Up
-- The VAT on the commission (MDR) of the acquirer (design: docs/architecture/15-card-fee-vat.md). The rule says the VAT rate the acquirer charges on the MDR; a card payment keeps a snapshot of it and of the VAT it expects;
-- a settlement keeps the final MDR and the final VAT as they were booked, the VAT treatment frozen on the date of the bank line, and the expected figures, the rates and the proposal for the audit. Nothing here is
-- booked by itself: the settlement journal books it.

-- The rate of the VAT on the MDR, with the rule (a rule is never changed: a new rate is a new rule from a later date).
ALTER TABLE card_fee_rules ADD COLUMN vat_rate numeric(7,4) NOT NULL DEFAULT 0;
ALTER TABLE card_fee_rules ADD CONSTRAINT cfr_vat_rate_ck CHECK (vat_rate BETWEEN 0 AND 100);

-- The snapshot on a card or e-wallet payment and on a city ledger receipt by card: the VAT rate of the rule in force and the VAT expected on the MDR fee of the payment.
-- A payment taken before this keeps both null: it is "without rate" (nothing is guessed for it). A payment under a rule with a VAT rate of 0 keeps rate 0, which is not "without rate".
ALTER TABLE payments ADD COLUMN mdr_vat_rate numeric(7,4);
ALTER TABLE payments ADD COLUMN mdr_vat numeric(18,3);
ALTER TABLE payments ADD CONSTRAINT payments_mdr_vat_ck CHECK ((mdr_vat_rate IS NULL) = (mdr_vat IS NULL)
    AND (mdr_vat_rate IS NULL OR (mdr_rate IS NOT NULL AND mdr_vat_rate BETWEEN 0 AND 100 AND mdr_vat >= 0)));
ALTER TABLE city_ledger_receipts ADD COLUMN mdr_vat_rate numeric(7,4);
ALTER TABLE city_ledger_receipts ADD COLUMN mdr_vat numeric(18,3);
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_mdr_vat_ck CHECK ((mdr_vat_rate IS NULL) = (mdr_vat IS NULL)
    AND (mdr_vat_rate IS NULL OR (mdr_rate IS NOT NULL AND mdr_vat_rate BETWEEN 0 AND 100 AND mdr_vat >= 0)));

-- The settlement, frozen when it is posted (the table is append-only). `fee` stays the whole deduction of the bank (gross - net); it is the MDR plus the VAT:
-- `mdr_amount` is generated (fee - vat_amount), so the identity fee = mdr_amount + vat_amount holds by construction, also for the settlements made before this (VAT 0).
ALTER TABLE card_settlements RENAME COLUMN expected_fee TO expected_mdr;
ALTER TABLE card_settlements ADD COLUMN vat_amount numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE card_settlements ADD COLUMN mdr_amount numeric(18,3) GENERATED ALWAYS AS (fee - vat_amount) STORED;
ALTER TABLE card_settlements ADD COLUMN vat_treatment varchar(10);
ALTER TABLE card_settlements ADD COLUMN expected_vat numeric(18,3);
ALTER TABLE card_settlements ADD COLUMN proposed_vat numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE card_settlements ADD COLUMN mdr_rate numeric(7,4);
ALTER TABLE card_settlements ADD COLUMN vat_rate numeric(7,4);
ALTER TABLE card_settlements ADD COLUMN payments_without_vat_rate int NOT NULL DEFAULT 0;
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_split_ck CHECK (vat_amount >= 0 AND vat_amount <= fee);
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_vat_treatment_ck CHECK ((vat_amount = 0) = (vat_treatment IS NULL) AND (vat_treatment IS NULL OR vat_treatment IN ('CREDITABLE', 'EXPENSE', 'DEFERRED')));
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_expected_vat_ck CHECK ((expected_vat IS NULL OR (expected_vat >= 0 AND expected_mdr IS NOT NULL)) AND proposed_vat >= 0 AND proposed_vat <= fee AND payments_without_vat_rate >= 0);
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_rates_ck CHECK ((mdr_rate IS NULL OR mdr_rate BETWEEN 0 AND 100) AND (vat_rate IS NULL OR vat_rate BETWEEN 0 AND 100));

-- The input VAT of a settlement (CREDITABLE) is claimed on the VAT return like that of a bill: one positive claim per settlement. A settlement is final, so its claim has no reversal.
-- A claim now names a bill line, a credit note line or a settlement, exactly one of them.
ALTER TABLE tax_return_input_claims ADD COLUMN settlement_id bigint;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_settlement_fk FOREIGN KEY (property_id, settlement_id) REFERENCES card_settlements (property_id, id);
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_source_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_source_ck CHECK (
    (bill_id IS NOT NULL AND line_no IS NOT NULL AND credit_id IS NULL AND settlement_id IS NULL)
    OR (bill_id IS NULL AND line_no IS NULL AND credit_id IS NOT NULL AND settlement_id IS NULL)
    OR (bill_id IS NULL AND line_no IS NULL AND credit_id IS NULL AND settlement_id IS NOT NULL));
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_settlement_ck CHECK (settlement_id IS NULL OR (reverses_claim_id IS NULL AND amount > 0));
CREATE UNIQUE INDEX tax_return_input_claims_live_settlement_uk ON tax_return_input_claims (property_id, settlement_id) WHERE released_at IS NULL AND reverses_claim_id IS NULL AND settlement_id IS NOT NULL;

-- +goose Down
DROP INDEX tax_return_input_claims_live_settlement_uk;
DELETE FROM tax_return_input_claims WHERE settlement_id IS NOT NULL;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_settlement_ck;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_source_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_source_ck CHECK ((bill_id IS NOT NULL AND line_no IS NOT NULL AND credit_id IS NULL) OR (bill_id IS NULL AND line_no IS NULL AND credit_id IS NOT NULL));
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_settlement_fk;
ALTER TABLE tax_return_input_claims DROP COLUMN settlement_id;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_rates_ck;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_expected_vat_ck;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_vat_treatment_ck;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_split_ck;
ALTER TABLE card_settlements DROP COLUMN payments_without_vat_rate;
ALTER TABLE card_settlements DROP COLUMN vat_rate;
ALTER TABLE card_settlements DROP COLUMN mdr_rate;
ALTER TABLE card_settlements DROP COLUMN proposed_vat;
ALTER TABLE card_settlements DROP COLUMN expected_vat;
ALTER TABLE card_settlements DROP COLUMN vat_treatment;
ALTER TABLE card_settlements DROP COLUMN mdr_amount;
ALTER TABLE card_settlements DROP COLUMN vat_amount;
ALTER TABLE card_settlements RENAME COLUMN expected_mdr TO expected_fee;
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_mdr_vat_ck;
ALTER TABLE city_ledger_receipts DROP COLUMN mdr_vat;
ALTER TABLE city_ledger_receipts DROP COLUMN mdr_vat_rate;
ALTER TABLE payments DROP CONSTRAINT payments_mdr_vat_ck;
ALTER TABLE payments DROP COLUMN mdr_vat;
ALTER TABLE payments DROP COLUMN mdr_vat_rate;
ALTER TABLE card_fee_rules DROP CONSTRAINT cfr_vat_rate_ck;
ALTER TABLE card_fee_rules DROP COLUMN vat_rate;
