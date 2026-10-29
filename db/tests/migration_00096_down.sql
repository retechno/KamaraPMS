-- What the Down of migration 00096 left.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('fa_audits') IS NOT NULL OR to_regclass('fa_audit_lines') IS NOT NULL OR to_regclass('fa_proposals') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00096 down: the tables are still there'; END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'maintenance_requests' AND column_name = 'asset_id') THEN RAISE EXCEPTION 'FAIL 00096 down: maintenance_requests.asset_id is still there'; END IF;
    IF EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type = 'FA_AUDIT') THEN RAISE EXCEPTION 'FAIL 00096 down: the sequence is still there'; END IF;
    RAISE NOTICE 'PASS  00096 down: the tables, the column of the request and the sequence are gone';
END
$$;
