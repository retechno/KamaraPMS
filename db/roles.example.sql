-- Example: run the API as a role that cannot rewrite history, even by mistake or through a bug.
-- The migrations are applied by the owner role (the one in PMS_DATABASE_URL of `cmd/migrate`); the API connects as
-- pms_app. The append-only triggers already refuse UPDATE, DELETE and TRUNCATE on the ledger for every role, owner
-- included; these grants are the second lock on the same door.
--
--   psql "$OWNER_DATABASE_URL" -v app_password="'change-me'" -f db/roles.example.sql
--
-- Re-run it after each migration that adds tables (it grants on the tables that exist).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pms_app') THEN
        CREATE ROLE pms_app LOGIN;
    END IF;
END $$;
ALTER ROLE pms_app PASSWORD :app_password;

GRANT USAGE ON SCHEMA public TO pms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO pms_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO pms_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO pms_app;

-- The ledger and the logs: insert and read only.
REVOKE UPDATE, DELETE, TRUNCATE ON folio_items, folio_item_components, audit_logs, housekeeping_logs FROM pms_app;
-- Payments change only POSTED -> VOIDED (a trigger checks the columns) and are never deleted.
REVOKE DELETE, TRUNCATE ON payments FROM pms_app;
-- The room charge register flips POSTED -> REVERSED and is never deleted.
REVOKE DELETE, TRUNCATE ON stay_charge_postings FROM pms_app;
-- Business days are never deleted (a closed day is immutable).
REVOKE DELETE, TRUNCATE ON business_days FROM pms_app;
-- The API never changes the schema or the migration history.
REVOKE ALL ON goose_db_version FROM pms_app;
