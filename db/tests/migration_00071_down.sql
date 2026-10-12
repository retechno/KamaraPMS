-- What the Down of migration 00071 did to the properties of migration_00071_pre.sql, after the account of Q4 was renamed by its user (so it is no longer the account the migration made).
\set ON_ERROR_STOP on
DO $$
DECLARE
    p bigint;
BEGIN
    IF EXISTS (SELECT 1 FROM gl_account_map WHERE map_key = 'QRIS') THEN RAISE EXCEPTION 'FAIL 00071 down: no key QRIS is left'; END IF;
    SELECT id INTO p FROM properties WHERE code = 'Q1';
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code = '1155') THEN RAISE EXCEPTION 'FAIL 00071 down Q1: the account the migration made, untouched and unused, is deleted'; END IF;
    SELECT id INTO p FROM properties WHERE code = 'Q2';
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code = '1157') THEN RAISE EXCEPTION 'FAIL 00071 down Q2: the replacement account is deleted too'; END IF;
    IF (SELECT count(*) FROM gl_accounts WHERE property_id = p AND code IN ('1155', '1156')) <> 2 THEN RAISE EXCEPTION 'FAIL 00071 down Q2: the accounts of the user stay'; END IF;
    SELECT id INTO p FROM properties WHERE code = 'Q4';
    IF NOT EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = p AND code = '1156' AND name = 'Akun e-money (diubah pengguna)') THEN RAISE EXCEPTION 'FAIL 00071 down Q4: an account its user changed stays, as an ordinary account'; END IF;
    IF (SELECT name FROM gl_accounts WHERE property_id = p AND code = '1155') <> 'Voucher (user account)' THEN RAISE EXCEPTION 'FAIL 00071 down Q4: the account of the user stays'; END IF;
    RAISE NOTICE 'PASS  00071 down: the accounts the migration made are deleted, the others stay, and no key is left';
END
$$;
