-- The data step of migration 00095: the sequence of the disposals and the account of the gain or loss on disposal, on the properties that exist (made by the earlier migration tests).
\set ON_ERROR_STOP on
DO $$
DECLARE
    v_props bigint;
    v_seq   bigint;
    v_wrong bigint;
    v_with  bigint;
    v_keys  bigint;
BEGIN
    SELECT count(*) INTO v_props FROM properties;
    SELECT count(*) INTO v_seq FROM document_sequences WHERE sequence_type = 'FA_DISPOSAL' AND prefix = 'FAD-';
    IF v_props = 0 OR v_seq <> v_props THEN RAISE EXCEPTION 'FAIL 00095 up: every property has the sequence FA_DISPOSAL (% of %)', v_seq, v_props; END IF;
    -- the key points at 7420 and at nothing else; a property without 7420 has no key and chooses the account itself
    SELECT count(*) INTO v_wrong FROM gl_account_map m JOIN gl_accounts a ON a.id = m.account_id WHERE m.map_key = 'ASSET_DISPOSAL' AND a.code <> '7420';
    SELECT count(*) INTO v_with FROM gl_accounts a WHERE a.code = '7420' AND a.account_type = 'EXPENSE' AND a.is_postable;
    SELECT count(*) INTO v_keys FROM gl_account_map WHERE map_key = 'ASSET_DISPOSAL';
    IF v_wrong <> 0 THEN RAISE EXCEPTION 'FAIL 00095 up: the key ASSET_DISPOSAL points at an account other than 7420'; END IF;
    IF v_keys <> v_with THEN RAISE EXCEPTION 'FAIL 00095 up: a property with 7420 has the key (% keys, % properties with 7420)', v_keys, v_with; END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'fa_source_links' AND column_name = 'kind' AND column_default LIKE '%UNIT%') THEN RAISE EXCEPTION 'FAIL 00095 up: fa_source_links.kind (default UNIT) is missing'; END IF;
    RAISE NOTICE 'PASS  00095 up: % properties have FA_DISPOSAL; % have the key ASSET_DISPOSAL on 7420', v_props, v_keys;
END
$$;
