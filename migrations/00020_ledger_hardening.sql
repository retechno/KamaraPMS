-- +goose Up
-- The ledger and the logs are append-only. Row triggers already refuse UPDATE and DELETE for everybody, the owner
-- included; TRUNCATE does not fire row triggers, so it gets a statement trigger of its own. A test harness that must
-- empty the tables sets the transaction-local setting pms.allow_truncate = 'on'; the application never does.
-- +goose StatementBegin
CREATE FUNCTION forbid_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF coalesce(current_setting('pms.allow_truncate', true), '') = 'on' THEN
        RETURN NULL;
    END IF;
    RAISE EXCEPTION '% cannot be truncated', TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation', CONSTRAINT = TG_TABLE_NAME || '_no_truncate';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER folio_items_no_truncate            BEFORE TRUNCATE ON folio_items            FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER folio_item_components_no_truncate  BEFORE TRUNCATE ON folio_item_components  FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER payments_no_truncate               BEFORE TRUNCATE ON payments               FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER stay_charge_postings_no_truncate   BEFORE TRUNCATE ON stay_charge_postings   FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER audit_logs_no_truncate             BEFORE TRUNCATE ON audit_logs             FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER housekeeping_logs_no_truncate      BEFORE TRUNCATE ON housekeeping_logs      FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- +goose Down
DROP TRIGGER IF EXISTS housekeeping_logs_no_truncate ON housekeeping_logs;
DROP TRIGGER IF EXISTS audit_logs_no_truncate ON audit_logs;
DROP TRIGGER IF EXISTS stay_charge_postings_no_truncate ON stay_charge_postings;
DROP TRIGGER IF EXISTS payments_no_truncate ON payments;
DROP TRIGGER IF EXISTS folio_item_components_no_truncate ON folio_item_components;
DROP TRIGGER IF EXISTS folio_items_no_truncate ON folio_items;
DROP FUNCTION IF EXISTS forbid_truncate();
