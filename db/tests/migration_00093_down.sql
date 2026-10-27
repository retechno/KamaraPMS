-- What the Down of migration 00093 left of the properties made by migration_00093_pre.sql.
\set ON_ERROR_STOP on
DO $$
DECLARE
    p int;
BEGIN
    IF to_regclass('fa_assets') IS NOT NULL OR to_regclass('fa_categories') IS NOT NULL OR to_regproc('seed_fa_categories') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00093 down: the module is still there'; END IF;
    -- the accounts the migration made are gone, 1590 is postable again where it was a header, and the accounts of the user stay
    IF EXISTS (SELECT 1 FROM gl_accounts a JOIN properties pr ON pr.id = a.property_id WHERE pr.code IN ('FA1', 'FA2')
                AND a.code IN ('1570', '1591', '1592', '1593', '1594', '1595', '7540', '7550')) THEN RAISE EXCEPTION 'FAIL 00093 down: the new accounts of FA1 and FA2 are deleted'; END IF;
    IF NOT (SELECT is_postable FROM gl_accounts a JOIN properties pr ON pr.id = a.property_id WHERE pr.code = 'FA1' AND a.code = '1590') THEN RAISE EXCEPTION 'FAIL 00093 down: 1590 is postable again'; END IF;
    IF (SELECT count(*) FROM gl_accounts a JOIN properties pr ON pr.id = a.property_id WHERE pr.code = 'FA3' AND a.code IN ('1591', '7540')) <> 2 THEN RAISE EXCEPTION 'FAIL 00093 down: the accounts of the user stay'; END IF;
    IF EXISTS (SELECT 1 FROM gl_accounts a JOIN properties pr ON pr.id = a.property_id WHERE pr.code = 'FA3' AND a.code IN ('1596', '7541')) THEN RAISE EXCEPTION 'FAIL 00093 down: the replacement accounts go'; END IF;
    IF EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type IN ('FIXED_ASSET', 'FA_OPENING')) THEN RAISE EXCEPTION 'FAIL 00093 down: the sequences go'; END IF;
    BEGIN
        INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
        SELECT tenant_id, id, 'JV-ASSET', 'ASSET', '2026-10-31', 'x' FROM properties WHERE code = 'FA1';
        RAISE EXCEPTION 'FAIL 00093 down: the journal type ASSET is accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    RAISE NOTICE 'PASS  00093 down: the module, its accounts and its sequences are gone; 1590 is postable again; the accounts of the user stay';
END
$$;
