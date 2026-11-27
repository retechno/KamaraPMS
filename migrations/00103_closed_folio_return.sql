-- +goose Up
-- A closed folio is balanced (docs/architecture/42-pos-charge-return-after-checkout.md §4 and §7, KD-26).
-- Until now the rule held in the code only: a folio closes at a zero balance (CloseStayFolios, CloseFolio, CloseBillFolio), and every path
-- that posts refuses a CLOSED folio (the list is in §4.1 of the document). The return of an outlet charge after check-out writes to the
-- closed folio of the guest. It writes a balanced set in one transaction: the return pair, then the refund or the balance pair from the
-- master. So the rule becomes the database's, for every path old and new. At commit, a CLOSED folio's items sum to zero.
--
--   folio_items   folios_closed_balanced: deferred, for each new item of a folio that is CLOSED at commit
--   folios        folios_closed_balanced: deferred, when a folio becomes CLOSED

-- The folios closed so far must already be balanced. If one is not, the migration stops and names it: the folio is put right first.
-- +goose StatementBegin
DO $$
DECLARE
    v_bad text;
BEGIN
    SELECT string_agg(format('%s (property %s, balance %s)', f.folio_number, f.property_id, s.balance), ', ' ORDER BY f.property_id, f.id) INTO v_bad
      FROM folios f
      JOIN LATERAL (SELECT COALESCE(sum(i.debit - i.credit), 0) AS balance FROM folio_items i WHERE i.property_id = f.property_id AND i.folio_id = f.id) s ON true
     WHERE f.status = 'CLOSED' AND s.balance <> 0;
    IF v_bad IS NOT NULL THEN
        RAISE EXCEPTION '00103: closed folios that do not balance: %', v_bad;
    END IF;
END $$;
-- +goose StatementEnd

-- One function for both triggers: the folio is read as it is at commit. An OPEN folio is not checked (its balance is free), so a posting to
-- an open folio costs one read of its row by the primary key.
-- +goose StatementBegin
CREATE FUNCTION folio_closed_balanced_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_property bigint := NEW.property_id;
    v_folio    bigint;
    v_status   varchar;
    v_number   varchar;
    v_balance  numeric;
BEGIN
    IF TG_TABLE_NAME = 'folio_items' THEN
        v_folio := NEW.folio_id;
    ELSE
        v_folio := NEW.id;
    END IF;
    SELECT f.status, f.folio_number INTO v_status, v_number FROM folios f WHERE f.property_id = v_property AND f.id = v_folio;
    IF v_status IS DISTINCT FROM 'CLOSED' THEN
        RETURN NULL;
    END IF;
    SELECT COALESCE(sum(i.debit - i.credit), 0) INTO v_balance FROM folio_items i WHERE i.property_id = v_property AND i.folio_id = v_folio;
    IF v_balance <> 0 THEN
        RAISE EXCEPTION 'the closed folio % does not balance: %', v_number, v_balance
            USING ERRCODE = 'check_violation', CONSTRAINT = 'folios_closed_balanced';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER folios_closed_balanced AFTER INSERT ON folio_items
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION folio_closed_balanced_check();
CREATE CONSTRAINT TRIGGER folios_closed_balanced AFTER UPDATE OF status ON folios
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.status = 'CLOSED') EXECUTE FUNCTION folio_closed_balanced_check();

-- +goose Down
DROP TRIGGER folios_closed_balanced ON folios;
DROP TRIGGER folios_closed_balanced ON folio_items;
DROP FUNCTION folio_closed_balanced_check();
