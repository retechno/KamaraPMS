-- What the Down of migration 00095 left.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('fa_moves') IS NOT NULL OR to_regclass('fa_revisions') IS NOT NULL OR to_regclass('fa_disposals') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00095 down: the tables are still there'; END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'fa_source_links' AND column_name = 'kind') THEN RAISE EXCEPTION 'FAIL 00095 down: fa_source_links.kind is still there'; END IF;
    IF EXISTS (SELECT 1 FROM gl_account_map WHERE map_key = 'ASSET_DISPOSAL') OR EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type = 'FA_DISPOSAL') THEN RAISE EXCEPTION 'FAIL 00095 down: the key or the sequence is still there'; END IF;
    RAISE NOTICE 'PASS  00095 down: the tables, the link kind, the key and the sequence are gone';
END
$$;
