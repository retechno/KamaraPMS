-- The data step of migration 00096: the sequence of the stock-takes on the properties that exist (made by the earlier migration tests), and the column that ties a request of Maintenance to an asset.
\set ON_ERROR_STOP on
DO $$
DECLARE
    v_props bigint;
    v_seq   bigint;
BEGIN
    SELECT count(*) INTO v_props FROM properties;
    SELECT count(*) INTO v_seq FROM document_sequences WHERE sequence_type = 'FA_AUDIT' AND prefix = 'FAO-';
    IF v_props = 0 OR v_seq <> v_props THEN RAISE EXCEPTION 'FAIL 00096 up: every property has the sequence FA_AUDIT (% of %)', v_seq, v_props; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'maintenance_requests' AND column_name = 'asset_id') THEN RAISE EXCEPTION 'FAIL 00096 up: maintenance_requests.asset_id is missing'; END IF;
    IF to_regclass('fa_audits') IS NULL OR to_regclass('fa_audit_lines') IS NULL OR to_regclass('fa_proposals') IS NULL THEN RAISE EXCEPTION 'FAIL 00096 up: the stock-take tables are missing'; END IF;
    RAISE NOTICE 'PASS  00096 up: % properties have FA_AUDIT, the stock-take tables and the asset of a request are there', v_props;
END
$$;
