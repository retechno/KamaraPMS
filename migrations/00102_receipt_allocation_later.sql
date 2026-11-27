-- +goose Up
-- Allocating a company receipt after it is recorded, and releasing a wrong allocation (docs/architecture/41-receipt-allocation.md, KD-27).
-- An allocation is a matching inside the city ledger: it says which invoice the money of a receipt paid. It posts no journal (the day close
-- journals the receipt itself), so allocating later moves no money.
--
--   city_ledger_receipt_allocations   allocated_on (the business date it was made on), created_by, idempotency_key (an allocation made after its
--                                     receipt), and the release (when, the business date, who, why, the approver). A receipt may pay the same
--                                     invoice in more than one allocation (clra_pair_uk goes). The append-only trigger becomes a narrow one: a
--                                     live allocation may be released, nothing else changes. At commit, the live allocations of a receipt are at
--                                     most its amount.

-- The old trigger refuses every update, the back-fill below included.
DROP TRIGGER city_ledger_receipt_allocations_guard ON city_ledger_receipt_allocations;
DROP FUNCTION city_ledger_receipt_allocations_guard();

ALTER TABLE city_ledger_receipt_allocations ADD COLUMN allocated_on date;
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN created_by bigint REFERENCES users (id);
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN idempotency_key varchar(100);
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN released_at timestamptz;
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN released_on date;
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN released_by bigint REFERENCES users (id);
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN release_reason varchar(500);
ALTER TABLE city_ledger_receipt_allocations ADD COLUMN release_approved_by bigint REFERENCES users (id);

-- Every allocation so far was made with its receipt, on the receipt's business date, by whoever recorded it.
UPDATE city_ledger_receipt_allocations a SET allocated_on = r.business_date, created_by = r.created_by
FROM city_ledger_receipts r WHERE r.property_id = a.property_id AND r.id = a.receipt_id;
ALTER TABLE city_ledger_receipt_allocations ALTER COLUMN allocated_on SET NOT NULL;

ALTER TABLE city_ledger_receipt_allocations ADD CONSTRAINT clra_allocated_on_fk FOREIGN KEY (property_id, allocated_on) REFERENCES business_days (property_id, business_date);
ALTER TABLE city_ledger_receipt_allocations ADD CONSTRAINT clra_released_on_fk FOREIGN KEY (property_id, released_on) REFERENCES business_days (property_id, business_date);
ALTER TABLE city_ledger_receipt_allocations ADD CONSTRAINT clra_released_ck CHECK (
    (released_at IS NULL) = (released_on IS NULL) AND (released_at IS NULL) = (released_by IS NULL)
    AND (released_at IS NULL) = (release_reason IS NULL) AND (released_at IS NULL) = (release_approved_by IS NULL));
ALTER TABLE city_ledger_receipt_allocations DROP CONSTRAINT clra_pair_uk;
CREATE UNIQUE INDEX clra_idempotency_uk ON city_ledger_receipt_allocations (property_id, idempotency_key, invoice_id) WHERE idempotency_key IS NOT NULL;

-- An allocation is never deleted and changes once: released, with the five columns of the release; everything else stays as it was.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_receipt_allocations_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_release text[] := ARRAY['released_at', 'released_on', 'released_by', 'release_reason', 'release_approved_by'];
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger receipt allocations cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clra_release_only';
    END IF;
    IF OLD.released_at IS NOT NULL OR NEW.released_at IS NULL OR (to_jsonb(NEW) - v_release) <> (to_jsonb(OLD) - v_release) THEN
        RAISE EXCEPTION 'city ledger receipt allocation % may only be released, once', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clra_release_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_receipt_allocations_guard BEFORE UPDATE OR DELETE ON city_ledger_receipt_allocations
    FOR EACH ROW EXECUTE FUNCTION city_ledger_receipt_allocations_guard();

-- At COMMIT: the live allocations of a receipt are at most its amount, however many requests added to them.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_receipt_allocations_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_amount    numeric;
    v_allocated numeric;
BEGIN
    SELECT amount INTO v_amount FROM city_ledger_receipts WHERE property_id = NEW.property_id AND id = NEW.receipt_id;
    SELECT COALESCE(sum(amount), 0) INTO v_allocated FROM city_ledger_receipt_allocations
     WHERE property_id = NEW.property_id AND receipt_id = NEW.receipt_id AND released_at IS NULL;
    IF v_allocated > v_amount THEN
        RAISE EXCEPTION 'receipt % has % allocated for an amount of %', NEW.receipt_id, v_allocated, v_amount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'clra_exceeds_receipt';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER city_ledger_receipt_allocations_total_check AFTER INSERT ON city_ledger_receipt_allocations
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION city_ledger_receipt_allocations_total_check();

-- Nothing is allocated or released on a day that is not open (the safety net of RequireOpenBusinessDay, 00065).
CREATE TRIGGER city_ledger_receipt_allocations_business_day_open BEFORE INSERT ON city_ledger_receipt_allocations
    FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('allocated_on');
CREATE TRIGGER city_ledger_receipt_allocations_released_business_day_open BEFORE UPDATE OF released_on ON city_ledger_receipt_allocations
    FOR EACH ROW WHEN (NEW.released_on IS DISTINCT FROM OLD.released_on) EXECUTE FUNCTION business_day_must_be_open('released_on');

-- +goose Down
-- A release, or a second allocation of the same invoice by the same receipt, cannot be undone by a rollback: the old trigger and unique key could not hold them.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM city_ledger_receipt_allocations WHERE released_at IS NOT NULL)
       OR EXISTS (SELECT 1 FROM city_ledger_receipt_allocations GROUP BY receipt_id, invoice_id HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'migration 00102 cannot be rolled back: an allocation has been released, or a receipt pays an invoice in two allocations';
    END IF;
END
$$;
-- +goose StatementEnd
DROP TRIGGER city_ledger_receipt_allocations_released_business_day_open ON city_ledger_receipt_allocations;
DROP TRIGGER city_ledger_receipt_allocations_business_day_open ON city_ledger_receipt_allocations;
DROP TRIGGER city_ledger_receipt_allocations_total_check ON city_ledger_receipt_allocations;
DROP FUNCTION city_ledger_receipt_allocations_total_check();
DROP TRIGGER city_ledger_receipt_allocations_guard ON city_ledger_receipt_allocations;
DROP FUNCTION city_ledger_receipt_allocations_guard();
DROP INDEX clra_idempotency_uk;
ALTER TABLE city_ledger_receipt_allocations ADD CONSTRAINT clra_pair_uk UNIQUE (receipt_id, invoice_id);
ALTER TABLE city_ledger_receipt_allocations DROP CONSTRAINT clra_released_ck;
ALTER TABLE city_ledger_receipt_allocations DROP CONSTRAINT clra_released_on_fk;
ALTER TABLE city_ledger_receipt_allocations DROP CONSTRAINT clra_allocated_on_fk;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN release_approved_by;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN release_reason;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN released_by;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN released_on;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN released_at;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN idempotency_key;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN created_by;
ALTER TABLE city_ledger_receipt_allocations DROP COLUMN allocated_on;
-- +goose StatementBegin
CREATE FUNCTION city_ledger_receipt_allocations_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'city ledger receipt allocations cannot be changed or deleted'
        USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clra_immutable';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_receipt_allocations_guard BEFORE UPDATE OR DELETE ON city_ledger_receipt_allocations
    FOR EACH ROW EXECUTE FUNCTION city_ledger_receipt_allocations_guard();
