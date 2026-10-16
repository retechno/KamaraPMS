-- What migration 00084 did to the properties made by migration_00084_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m84_p(text) RETURNS bigint LANGUAGE sql AS $$ SELECT id FROM properties WHERE code = $1 $$;
-- the account a key points at (found through the key, not through a code)
CREATE FUNCTION pg_temp.m84_acct(text, text) RETURNS gl_accounts LANGUAGE sql AS
    $$ SELECT a.* FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id WHERE m.property_id = pg_temp.m84_p($1) AND m.map_key = $2 $$;
DO $$
DECLARE
    a gl_accounts;
    k text;
BEGIN
    -- I1: the four accounts are created on their standard codes, under their parents, in English
    FOR k IN SELECT unnest(ARRAY['INV_GRNI:2130:Goods received not invoiced:2100:PAYABLES', 'INV_WASTE:5290:F&B stock waste and spoilage:5200:EXP_FB',
                                  'INV_COUNT_VARIANCE:5295:Stock count variance:5200:EXP_FB', 'INV_PPV:5299:Purchase price variance:5200:EXP_FB']) LOOP
        a := pg_temp.m84_acct('I1', split_part(k, ':', 1));
        IF a.code IS DISTINCT FROM split_part(k, ':', 2) OR a.name <> split_part(k, ':', 3) OR NOT a.is_postable OR a.statement_group <> split_part(k, ':', 5)
           OR a.parent_id IS DISTINCT FROM (SELECT id FROM gl_accounts WHERE property_id = pg_temp.m84_p('I1') AND code = split_part(k, ':', 4)) THEN
            RAISE EXCEPTION 'FAIL 00084 I1: the account of the key % (%, %, %)', split_part(k, ':', 1), a.code, a.name, a.parent_id;
        END IF;
    END LOOP;
    -- the keys that point at accounts of the chart that were there
    FOR k IN SELECT unnest(ARRAY['INV_COGS_FOOD:5210', 'INV_COGS_BEVERAGE:5220', 'INV_STAFF_MEAL:5240', 'INV_ENTERTAINMENT:6170']) LOOP
        a := pg_temp.m84_acct('I1', split_part(k, ':', 1));
        IF a.code IS DISTINCT FROM split_part(k, ':', 2) THEN RAISE EXCEPTION 'FAIL 00084 I1: the key % points at %', split_part(k, ':', 1), a.code; END IF;
    END LOOP;

    -- I2: 2130 and 5290 are the user's: neither is used nor changed; the new accounts are 2131 and 5291, in Indonesian; 5295 and 5299 are free
    a := pg_temp.m84_acct('I2', 'INV_GRNI');
    IF a.code IS DISTINCT FROM '2131' OR a.name <> 'Barang diterima belum ditagih' THEN RAISE EXCEPTION 'FAIL 00084 I2: the next free code for GRNI (%, %)', a.code, a.name; END IF;
    a := pg_temp.m84_acct('I2', 'INV_WASTE');
    IF a.code IS DISTINCT FROM '5291' OR a.name <> 'Susut dan kerusakan stok F&B' THEN RAISE EXCEPTION 'FAIL 00084 I2: the next free code for waste (%, %)', a.code, a.name; END IF;
    a := pg_temp.m84_acct('I2', 'INV_COUNT_VARIANCE');
    IF a.code IS DISTINCT FROM '5295' OR a.name <> 'Selisih opname stok' THEN RAISE EXCEPTION 'FAIL 00084 I2: the standard code for the count variance (%, %)', a.code, a.name; END IF;
    IF (SELECT name FROM gl_accounts WHERE property_id = pg_temp.m84_p('I2') AND code = '2130') <> 'Utang lain-lain (milik pengguna)'
       OR (SELECT name FROM gl_accounts WHERE property_id = pg_temp.m84_p('I2') AND code = '5290') <> 'Beban lain (milik pengguna)' THEN
        RAISE EXCEPTION 'FAIL 00084 I2: the accounts of the user are not touched';
    END IF;
    IF EXISTS (SELECT 1 FROM gl_account_map m JOIN gl_accounts a2 ON a2.property_id = m.property_id AND a2.id = m.account_id
                WHERE m.property_id = pg_temp.m84_p('I2') AND a2.code IN ('2130', '5290')) THEN
        RAISE EXCEPTION 'FAIL 00084 I2: no key points at the accounts of the user';
    END IF;

    -- I3: no chart: nothing
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = pg_temp.m84_p('I3')) OR EXISTS (SELECT 1 FROM gl_account_map WHERE property_id = pg_temp.m84_p('I3')) THEN
        RAISE EXCEPTION 'FAIL 00084 I3: a property with no chart gets nothing';
    END IF;

    -- the properties with the whole standard chart (I1 and I2) have the eight keys; one with only part of it (the properties of the 00071 test) gets the keys of the accounts it has
    IF (SELECT count(*) FROM gl_account_map WHERE map_key LIKE 'INV\_%' AND property_id IN (pg_temp.m84_p('I1'), pg_temp.m84_p('I2'))) <> 16 THEN
        RAISE EXCEPTION 'FAIL 00084: the eight keys of I1 and I2 (% keys)', (SELECT count(*) FROM gl_account_map WHERE map_key LIKE 'INV\_%' AND property_id IN (pg_temp.m84_p('I1'), pg_temp.m84_p('I2')));
    END IF;
    -- every property has the sequence of the openings
    IF (SELECT count(*) FROM document_sequences WHERE sequence_type = 'INV_OPENING') <> (SELECT count(*) FROM properties) THEN
        RAISE EXCEPTION 'FAIL 00084: every property has the sequence INV_OPENING';
    END IF;
    RAISE NOTICE 'PASS  00084: the inventory accounts for a chart without them, with 2130 and 5290 taken (2131, 5291), and none without a chart';
END
$$;
