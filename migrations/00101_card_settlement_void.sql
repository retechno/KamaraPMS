-- +goose Up
-- Voiding a card, e-wallet or QRIS settlement (docs/architecture/40-card-settlement-void.md, KD-05). A wrong settlement is voided
-- whole: its journal is reversed, its payment lines are released so they can be settled again, and its VAT claim, when a return
-- has claimed it, is taken back by the next return.
--
--   card_settlements             status POSTED | VOIDED and the void (when, who, why, the approver, the reversal journal); the
--                                append-only trigger becomes a narrow one: POSTED -> VOIDED with the void columns, nothing else
--   card_settlement_items        released_at: a payment line is settled once at a time (the unique key becomes a partial one), and
--                                an item is only released, when its settlement is voided
--   tax_return_input_claims      a settlement claim may now be reversed (a negative claim that names the claim it reverses)

ALTER TABLE card_settlements ADD COLUMN status varchar(10) NOT NULL DEFAULT 'POSTED';
ALTER TABLE card_settlements ADD COLUMN voided_at timestamptz;
ALTER TABLE card_settlements ADD COLUMN voided_by bigint REFERENCES users (id);
ALTER TABLE card_settlements ADD COLUMN void_reason varchar(500);
ALTER TABLE card_settlements ADD COLUMN void_approved_by bigint REFERENCES users (id);
ALTER TABLE card_settlements ADD COLUMN void_journal_id bigint;
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_status_ck CHECK (status IN ('POSTED', 'VOIDED'));
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_voided_ck CHECK (
    (status = 'VOIDED') = (voided_at IS NOT NULL) AND (status = 'VOIDED') = (void_journal_id IS NOT NULL) AND (status = 'VOIDED') = (void_reason IS NOT NULL));
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_void_journal_fk FOREIGN KEY (property_id, void_journal_id) REFERENCES gl_journals (property_id, id);
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_void_journal_uk UNIQUE (void_journal_id);

-- A settlement is never deleted and changes once, from POSTED to VOIDED with the columns of the void. The generated mdr_amount (fee - vat_amount) is left out of the
-- comparison: a BEFORE trigger sees it before it is computed, and fee and vat_amount are compared anyway.
-- +goose StatementBegin
CREATE FUNCTION card_settlements_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'card settlements cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'card_settlements_void_only';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_approved_by', 'void_journal_id', 'mdr_amount'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_approved_by', 'void_journal_id', 'mdr_amount']) THEN
        RAISE EXCEPTION 'card settlement % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'card_settlements_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP TRIGGER card_settlements_append_only ON card_settlements;
CREATE TRIGGER card_settlements_void_only BEFORE UPDATE OR DELETE ON card_settlements FOR EACH ROW EXECUTE FUNCTION card_settlements_guard();

-- A payment line is settled by one live item at a time; a voided settlement releases its items.
ALTER TABLE card_settlement_items ADD COLUMN released_at timestamptz;
ALTER TABLE card_settlement_items DROP CONSTRAINT card_settlement_items_settled_uk;
CREATE UNIQUE INDEX card_settlement_items_live_uk ON card_settlement_items (settled_line_id) WHERE released_at IS NULL;

-- An item is never deleted and changes once: released, when its settlement is voided (in the same transaction, the settlement first).
-- +goose StatementBegin
CREATE FUNCTION card_settlement_items_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'card settlement items cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'card_settlement_items_release_only';
    END IF;
    IF OLD.released_at IS NOT NULL OR NEW.released_at IS NULL
       OR (to_jsonb(NEW) - 'released_at') <> (to_jsonb(OLD) - 'released_at')
       OR NOT EXISTS (SELECT 1 FROM card_settlements s WHERE s.property_id = OLD.property_id AND s.id = OLD.settlement_id AND s.status = 'VOIDED') THEN
        RAISE EXCEPTION 'card settlement item % is only released, when its settlement is voided', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'card_settlement_items_release_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP TRIGGER card_settlement_items_append_only ON card_settlement_items;
CREATE TRIGGER card_settlement_items_release_only BEFORE UPDATE OR DELETE ON card_settlement_items FOR EACH ROW EXECUTE FUNCTION card_settlement_items_guard();

-- The claim of a settlement can now be reversed by a later return, when the settlement is voided: a negative claim that names it.
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_settlement_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_settlement_ck CHECK (
    settlement_id IS NULL OR (reverses_claim_id IS NULL AND amount > 0) OR (reverses_claim_id IS NOT NULL AND amount < 0));

-- A reversal reverses a claim of the same source (bill line or settlement), for the same amount, with the sign changed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tax_return_input_claims_reversal_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    o record;
BEGIN
    IF NEW.reverses_claim_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT bill_id, line_no, settlement_id, amount, reverses_claim_id INTO o FROM tax_return_input_claims WHERE property_id = NEW.property_id AND id = NEW.reverses_claim_id;
    IF o.reverses_claim_id IS NOT NULL OR o.bill_id <> NEW.bill_id OR o.line_no <> NEW.line_no OR o.amount <> -NEW.amount
       OR o.settlement_id IS DISTINCT FROM NEW.settlement_id THEN
        RAISE EXCEPTION 'a reversal reverses a claim of the same bill line for the same amount'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_return_input_claims_reversal';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- A void cannot be undone by a rollback: the old unique key and the old checks could not hold a voided settlement, a released
-- item or a reversed settlement claim.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM card_settlements WHERE status = 'VOIDED')
       OR EXISTS (SELECT 1 FROM card_settlement_items WHERE released_at IS NOT NULL)
       OR EXISTS (SELECT 1 FROM tax_return_input_claims WHERE settlement_id IS NOT NULL AND reverses_claim_id IS NOT NULL) THEN
        RAISE EXCEPTION 'migration 00101 cannot be rolled back: a card settlement has been voided';
    END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tax_return_input_claims_reversal_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    o record;
BEGIN
    IF NEW.reverses_claim_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT bill_id, line_no, amount, reverses_claim_id INTO o FROM tax_return_input_claims WHERE property_id = NEW.property_id AND id = NEW.reverses_claim_id;
    IF o.reverses_claim_id IS NOT NULL OR o.bill_id <> NEW.bill_id OR o.line_no <> NEW.line_no OR o.amount <> -NEW.amount THEN
        RAISE EXCEPTION 'a reversal reverses a claim of the same bill line for the same amount'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_return_input_claims_reversal';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_settlement_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_settlement_ck CHECK (settlement_id IS NULL OR (reverses_claim_id IS NULL AND amount > 0));
DROP TRIGGER card_settlement_items_release_only ON card_settlement_items;
DROP FUNCTION card_settlement_items_guard();
CREATE TRIGGER card_settlement_items_append_only BEFORE UPDATE OR DELETE ON card_settlement_items FOR EACH ROW EXECUTE FUNCTION forbid_modification();
DROP INDEX card_settlement_items_live_uk;
ALTER TABLE card_settlement_items ADD CONSTRAINT card_settlement_items_settled_uk UNIQUE (settled_line_id);
ALTER TABLE card_settlement_items DROP COLUMN released_at;
DROP TRIGGER card_settlements_void_only ON card_settlements;
DROP FUNCTION card_settlements_guard();
CREATE TRIGGER card_settlements_append_only BEFORE UPDATE OR DELETE ON card_settlements FOR EACH ROW EXECUTE FUNCTION forbid_modification();
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_void_journal_uk;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_void_journal_fk;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_voided_ck;
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_status_ck;
ALTER TABLE card_settlements DROP COLUMN void_journal_id;
ALTER TABLE card_settlements DROP COLUMN void_approved_by;
ALTER TABLE card_settlements DROP COLUMN void_reason;
ALTER TABLE card_settlements DROP COLUMN voided_by;
ALTER TABLE card_settlements DROP COLUMN voided_at;
ALTER TABLE card_settlements DROP COLUMN status;
