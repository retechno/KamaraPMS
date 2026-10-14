-- +goose Up
-- The release of a deposit against the charges it pays (docs/architecture/31-deposit-release.md). A setting for each property: EVERY_DAY (the default) releases at each day close,
-- folio by folio, what the deposit of the folio pays of its charges (DEPOSIT_APPLIED: debit 2310, credit 1210); ON_CLOSE is what the books did until now (DEPOSIT_RELEASE when the
-- folio closes). The setting is locked, by the database, as soon as the property has its first folio item (a payment and a deposit are folio items).
ALTER TABLE properties
    ADD COLUMN deposit_release_mode            varchar(12) NOT NULL DEFAULT 'EVERY_DAY',
    ADD COLUMN deposit_release_locked_on       date,
    -- the first day close after this migration carries the adjustment of the deposits that were held whole until now (see 31 section 6), and clears this flag
    ADD COLUMN deposit_release_adjust_pending  boolean     NOT NULL DEFAULT false,
    ADD CONSTRAINT properties_deposit_release_mode_ck CHECK (deposit_release_mode IN ('EVERY_DAY', 'ON_CLOSE'));

-- What a folio holds as a deposit and what the deposit can pay, as of a business date (the same view as the journal, so the day close and the reconciliation read the same numbers):
--   held    = the deposit payments less what was refunded of them (a refund of a deposit is a debit);
--   payable = what is charged and not yet paid by a settlement (never below zero);
--   applied = what the deposit has paid by that date: the least of the two while the folio is open, all of it once the folio is closed (a closed folio has a zero balance).
-- Only folios that are not closed before the date are read: a folio closed earlier was settled by its own close (applied = held, journaled by DEPOSIT_RELEASE or DEPOSIT_APPLIED).
-- +goose StatementBegin
CREATE FUNCTION folio_deposit_state(p_property bigint, p_date date)
RETURNS TABLE (folio_id bigint, folio_number varchar, held numeric, payable numeric, closed boolean, applied numeric)
LANGUAGE sql STABLE AS $$
    SELECT s.folio_id, s.folio_number, s.held, s.payable, s.closed,
           CASE WHEN s.closed THEN GREATEST(s.held, 0) ELSE LEAST(GREATEST(s.held, 0), s.payable) END
      FROM (SELECT f.id AS folio_id, f.folio_number,
                   COALESCE(-sum(g.signed_amount) FILTER (WHERE g.kind = 'PAYMENT' AND g.is_deposit), 0) AS held,
                   GREATEST(0, COALESCE(sum(g.signed_amount) FILTER (WHERE g.kind = 'CHARGE'), 0) + COALESCE(sum(g.signed_amount) FILTER (WHERE g.kind = 'PAYMENT' AND NOT g.is_deposit), 0)) AS payable,
                   (f.closed_on IS NOT NULL AND f.closed_on <= p_date) AS closed
              FROM folios f
              JOIN folio_item_gl g ON g.property_id = f.property_id AND g.folio_id = f.id AND g.business_date <= p_date
             WHERE f.property_id = p_property AND (f.closed_on IS NULL OR f.closed_on >= p_date)
             GROUP BY f.id, f.folio_number, f.closed_on) s
$$;
-- +goose StatementEnd

-- The lock: the first folio item of a property sets the date. The UPDATE has a WHERE that a locked property does not match, so after the lock a posting takes no row lock on the property
-- and writes nothing to it; before it, the first posting takes the lock once and a second posting at the same moment waits for it and finds the date set.
-- +goose StatementBegin
CREATE FUNCTION lock_deposit_release() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE properties SET deposit_release_locked_on = NEW.business_date WHERE id = NEW.property_id AND deposit_release_locked_on IS NULL;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER folio_items_lock_deposit_release AFTER INSERT ON folio_items FOR EACH ROW EXECUTE FUNCTION lock_deposit_release();

-- +goose StatementBegin
CREATE FUNCTION deposit_release_mode_is_locked() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deposit_release_mode IS DISTINCT FROM OLD.deposit_release_mode AND OLD.deposit_release_locked_on IS NOT NULL THEN
        RAISE EXCEPTION 'the release of deposits of property % is locked since %', OLD.id, OLD.deposit_release_locked_on
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'deposit_release_mode_locked';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER properties_deposit_release_mode_locked BEFORE UPDATE OF deposit_release_mode ON properties FOR EACH ROW EXECUTE FUNCTION deposit_release_mode_is_locked();

-- The properties that exist (demo data) move to EVERY_DAY (the column default) and are locked at the date of their first item. They carry the adjustment of the deposits held whole until now
-- on their first day close from here (no journal is written by this migration: a journal dated in the past would change a day that was posted and reported).
UPDATE properties p
   SET deposit_release_locked_on = (SELECT min(i.business_date) FROM folio_items i WHERE i.property_id = p.id),
       deposit_release_adjust_pending = EXISTS (SELECT 1 FROM payments y WHERE y.property_id = p.id)
 WHERE EXISTS (SELECT 1 FROM folio_items i WHERE i.property_id = p.id);

-- +goose Down
-- The old books know no DEPOSIT_APPLIED line: once a day close has written one, the reconciliation of the old code would not agree, and the rows cannot be un-posted. So this refuses
-- from then on; before the first close from here it drops what it added.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM gl_journal_lines WHERE source_type = 'DEPOSIT_APPLIED') THEN
        RAISE EXCEPTION '00078 cannot be rolled back: day close lines DEPOSIT_APPLIED exist (the old books do not know them)';
    END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER properties_deposit_release_mode_locked ON properties;
DROP FUNCTION deposit_release_mode_is_locked();
DROP TRIGGER folio_items_lock_deposit_release ON folio_items;
DROP FUNCTION lock_deposit_release();
DROP FUNCTION folio_deposit_state(bigint, date);
ALTER TABLE properties
    DROP CONSTRAINT properties_deposit_release_mode_ck,
    DROP COLUMN deposit_release_adjust_pending,
    DROP COLUMN deposit_release_locked_on,
    DROP COLUMN deposit_release_mode;
