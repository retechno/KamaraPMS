-- What migration 00071 did to the properties made by migration_00071_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m71_p(text) RETURNS bigint LANGUAGE sql AS $$ SELECT id FROM properties WHERE code = $1 $$;
-- the account the key QRIS points at (found through the key, not through a code)
CREATE FUNCTION pg_temp.m71_acct(text) RETURNS gl_accounts LANGUAGE sql AS
    $$ SELECT a.* FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id WHERE m.property_id = pg_temp.m71_p($1) AND m.map_key = 'QRIS' $$;
DO $$
DECLARE a gl_accounts;
BEGIN
    -- Q1: no 1155: it is created, under 1100, in English
    a := pg_temp.m71_acct('Q1');
    IF a.code IS DISTINCT FROM '1155' OR a.name <> 'QRIS clearing' OR a.account_type <> 'ASSET' OR a.normal_side <> 'DEBIT' OR NOT a.is_postable OR a.statement_group <> 'CASH'
       OR a.parent_id IS DISTINCT FROM (SELECT id FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q1') AND code = '1100') THEN
        RAISE EXCEPTION 'FAIL 00071 Q1: the account 1155 is created and mapped (%, %, %)', a.code, a.name, a.parent_id;
    END IF;

    -- Q2: 1155 and 1156 are the user's: neither is used nor changed, the account is 1157, in Indonesian
    a := pg_temp.m71_acct('Q2');
    IF a.code IS DISTINCT FROM '1157' OR a.name <> 'Akun penampung QRIS' THEN
        RAISE EXCEPTION 'FAIL 00071 Q2: the next free code is used (%, %)', a.code, a.name;
    END IF;
    IF (SELECT name FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q2') AND code = '1155') <> 'Dompet digital (milik pengguna)'
       OR (SELECT name FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q2') AND code = '1156') <> 'Akun lain (milik pengguna)' THEN
        RAISE EXCEPTION 'FAIL 00071 Q2: the accounts of the user are not touched';
    END IF;
    IF (SELECT count(*) FROM gl_account_map WHERE property_id = pg_temp.m71_p('Q2') AND account_id IN (SELECT id FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q2') AND code IN ('1155', '1156'))) <> 0 THEN
        RAISE EXCEPTION 'FAIL 00071 Q2: no key points at the accounts of the user';
    END IF;

    -- Q3: no chart: nothing
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q3')) OR EXISTS (SELECT 1 FROM gl_account_map WHERE property_id = pg_temp.m71_p('Q3')) THEN
        RAISE EXCEPTION 'FAIL 00071 Q3: a property with no chart gets nothing';
    END IF;

    -- Q4: 1155 is the user's and there is no 1100: 1156, with no parent
    a := pg_temp.m71_acct('Q4');
    IF a.code IS DISTINCT FROM '1156' OR a.parent_id IS NOT NULL OR a.name <> 'QRIS clearing' THEN
        RAISE EXCEPTION 'FAIL 00071 Q4: the next free code, with no parent (%, %, %)', a.code, a.name, a.parent_id;
    END IF;
    IF (SELECT name FROM gl_accounts WHERE property_id = pg_temp.m71_p('Q4') AND code = '1155') <> 'Voucher (user account)' THEN
        RAISE EXCEPTION 'FAIL 00071 Q4: the account of the user is not touched';
    END IF;

    -- every property that has a chart has the key
    IF (SELECT count(*) FROM gl_account_map WHERE map_key = 'QRIS') <> (SELECT count(*) FROM accounting_settings) THEN
        RAISE EXCEPTION 'FAIL 00071: every property with a chart has the key QRIS (% of %)', (SELECT count(*) FROM gl_account_map WHERE map_key = 'QRIS'), (SELECT count(*) FROM accounting_settings);
    END IF;

    -- the refund methods accept QRIS now, and what is not a method still is not
    UPDATE properties SET refund_methods = ARRAY['CASH', 'QRIS'] WHERE code = 'Q1';
    UPDATE properties SET refund_methods = ARRAY['CASH'] WHERE code = 'Q1';
    BEGIN
        UPDATE properties SET refund_methods = ARRAY['CASH', 'BITCOIN'] WHERE code = 'Q1';
        RAISE EXCEPTION 'FAIL 00071: a method that does not exist is refused as a refund method';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    RAISE NOTICE 'PASS  00071: QRIS account for a chart without 1155, with a taken 1155 (1157), with a taken 1155 and no 1100 (1156), and none without a chart';
END
$$;
