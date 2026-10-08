-- +goose Up
-- A CANCELLED stay has no OPEN folio (audit F-06). A stay is cancelled only by reversing its check-in, which unlinks the guest folio (it goes back to being the reservation's deposit folio)
-- and closes the company folios of the stay, so what is left on a cancelled stay is a CLOSED folio kept as history. This is the safety net behind that service logic, not a replacement
-- for it: the check runs at COMMIT, both when a stay becomes CANCELLED and when a folio of a cancelled stay is OPEN (inserted, or changed). Rows that existed before are not examined.
-- +goose StatementBegin
CREATE FUNCTION cancelled_stay_open_folio_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_stay bigint;
BEGIN
    IF TG_TABLE_NAME = 'stays' THEN
        v_stay := NEW.id;
    ELSE
        v_stay := NEW.stay_id;
    END IF;
    IF v_stay IS NOT NULL
       AND EXISTS (SELECT 1 FROM stays s WHERE s.property_id = NEW.property_id AND s.id = v_stay AND s.status = 'CANCELLED')
       AND EXISTS (SELECT 1 FROM folios f WHERE f.property_id = NEW.property_id AND f.stay_id = v_stay AND f.status = 'OPEN') THEN
        RAISE EXCEPTION 'stay % is cancelled and still has an open folio', v_stay
            USING ERRCODE = 'check_violation', CONSTRAINT = 'stays_cancelled_no_open_folio';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER stays_cancelled_no_open_folio AFTER UPDATE OF status ON stays
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.status = 'CANCELLED') EXECUTE FUNCTION cancelled_stay_open_folio_check();
CREATE CONSTRAINT TRIGGER folios_not_open_on_cancelled_stay AFTER INSERT OR UPDATE OF status, stay_id ON folios
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.status = 'OPEN' AND NEW.stay_id IS NOT NULL) EXECUTE FUNCTION cancelled_stay_open_folio_check();

-- +goose Down
DROP TRIGGER folios_not_open_on_cancelled_stay ON folios;
DROP TRIGGER stays_cancelled_no_open_folio ON stays;
DROP FUNCTION cancelled_stay_open_folio_check();
