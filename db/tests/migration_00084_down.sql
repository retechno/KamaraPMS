-- What the Down of migration 00084 did to the properties of migration_00084_pre.sql, after the account of I2 for waste was renamed by its user (so it is no longer the account the migration made).
\set ON_ERROR_STOP on
DO $$
DECLARE
    p bigint;
BEGIN
    IF EXISTS (SELECT 1 FROM gl_account_map WHERE map_key LIKE 'INV\_%') THEN RAISE EXCEPTION 'FAIL 00084 down: no key of the stock is left'; END IF;
    SELECT id INTO p FROM properties WHERE code = 'I1';
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code IN ('2130', '5290', '5295', '5299')) THEN
        RAISE EXCEPTION 'FAIL 00084 down I1: the accounts the migration made, untouched and unused, are deleted';
    END IF;
    IF (SELECT count(*) FROM gl_accounts WHERE property_id = p AND code IN ('5210', '5220', '5240', '6170')) <> 4 THEN RAISE EXCEPTION 'FAIL 00084 down I1: the accounts of the chart stay'; END IF;
    SELECT id INTO p FROM properties WHERE code = 'I2';
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code IN ('2131', '5295')) THEN RAISE EXCEPTION 'FAIL 00084 down I2: the replacement account and the standard one are deleted'; END IF;
    IF NOT EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code = '5291' AND name = 'Beban susut (diubah pengguna)') THEN RAISE EXCEPTION 'FAIL 00084 down I2: an account its user changed stays, as an ordinary account'; END IF;
    IF (SELECT count(*) FROM gl_accounts WHERE property_id = p AND code IN ('2130', '5290')) <> 2 THEN RAISE EXCEPTION 'FAIL 00084 down I2: the accounts of the user stay'; END IF;
    IF EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type = 'INV_OPENING') THEN RAISE EXCEPTION 'FAIL 00084 down: the sequence of the openings is removed'; END IF;
    IF to_regclass('inv_movements') IS NOT NULL OR to_regclass('inventory_settings') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00084 down: the tables are dropped'; END IF;
    RAISE NOTICE 'PASS  00084 down: the accounts the migration made are deleted, the others stay, and no key, table or sequence is left';
END
$$;
