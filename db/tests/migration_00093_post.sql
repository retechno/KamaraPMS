-- What migration 00093 did to the properties made by migration_00093_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m93_p(text) RETURNS bigint LANGUAGE sql AS $$ SELECT id FROM properties WHERE code = $1 $$;
CREATE FUNCTION pg_temp.m93_a(text, text) RETURNS gl_accounts LANGUAGE sql AS $$ SELECT a FROM gl_accounts a WHERE a.property_id = pg_temp.m93_p($1) AND a.code = $2 $$;
DO $$
DECLARE
    a gl_accounts;
    c fa_categories;
    k text;
BEGIN
    -- FA1: 1590 became a header, the five accumulated accounts sit under it, the others under 1500 and 7500; English names
    a := pg_temp.m93_a('FA1', '1590');
    IF a.is_postable THEN RAISE EXCEPTION 'FAIL 00093 FA1: 1590 is a header when nothing is posted to it'; END IF;
    FOR k IN SELECT unnest(ARRAY['1591:Accumulated depreciation - buildings', '1592:Accumulated depreciation - furniture, fixtures and equipment', '1593:Accumulated amortization - operating equipment',
                                 '1594:Accumulated depreciation - vehicles', '1595:Accumulated depreciation - computers and IT']) LOOP
        a := pg_temp.m93_a('FA1', split_part(k, ':', 1));
        IF a.id IS NULL OR a.name <> split_part(k, ':', 2) OR NOT a.is_postable OR a.normal_side <> 'CREDIT' OR a.account_type <> 'ASSET' OR a.statement_group <> 'FIXED_ASSETS'
           OR a.parent_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '1590')).id THEN
            RAISE EXCEPTION 'FAIL 00093 FA1: the account % (%, %, %)', split_part(k, ':', 1), a.name, a.parent_id, a.normal_side;
        END IF;
    END LOOP;
    a := pg_temp.m93_a('FA1', '1570');
    IF a.id IS NULL OR a.parent_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '1500')).id OR a.name <> 'Computers and IT equipment' OR a.normal_side <> 'DEBIT' THEN RAISE EXCEPTION 'FAIL 00093 FA1: 1570'; END IF;
    FOR k IN SELECT unnest(ARRAY['7540', '7550']) LOOP
        a := pg_temp.m93_a('FA1', k);
        IF a.id IS NULL OR a.parent_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '7500')).id OR a.statement_group <> 'DEPRECIATION' OR a.account_type <> 'EXPENSE' THEN RAISE EXCEPTION 'FAIL 00093 FA1: %', k; END IF;
    END LOOP;
    -- the seven categories, with the accounts of the standard codes
    IF (SELECT count(*) FROM fa_categories WHERE property_id = pg_temp.m93_p('FA1')) <> 7 THEN RAISE EXCEPTION 'FAIL 00093 FA1: seven categories (%)', (SELECT count(*) FROM fa_categories WHERE property_id = pg_temp.m93_p('FA1')); END IF;
    SELECT * INTO c FROM fa_categories WHERE property_id = pg_temp.m93_p('FA1') AND code = 'VEHICLE';
    IF c.cost_account_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '1550')).id OR c.accumulated_account_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '1594')).id
       OR c.expense_account_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '7540')).id OR c.life_months <> 96 OR c.name <> 'Vehicles' OR NOT c.is_depreciable THEN
        RAISE EXCEPTION 'FAIL 00093 FA1: the category of the vehicles';
    END IF;
    SELECT * INTO c FROM fa_categories WHERE property_id = pg_temp.m93_p('FA1') AND code = 'CIP';
    IF c.is_depreciable OR c.cost_account_id IS DISTINCT FROM (pg_temp.m93_a('FA1', '1560')).id THEN RAISE EXCEPTION 'FAIL 00093 FA1: construction in progress is not depreciated'; END IF;

    -- FA2: a journal is posted to 1590, so it stays postable and the five accounts sit under 1500
    a := pg_temp.m93_a('FA2', '1590');
    IF NOT a.is_postable THEN RAISE EXCEPTION 'FAIL 00093 FA2: 1590 stays postable when a journal is posted to it'; END IF;
    FOR k IN SELECT unnest(ARRAY['1591', '1592', '1593', '1594', '1595']) LOOP
        a := pg_temp.m93_a('FA2', k);
        IF a.id IS NULL OR a.parent_id IS DISTINCT FROM (pg_temp.m93_a('FA2', '1500')).id THEN RAISE EXCEPTION 'FAIL 00093 FA2: % sits under 1500', k; END IF;
    END LOOP;
    IF (SELECT count(*) FROM fa_categories WHERE property_id = pg_temp.m93_p('FA2')) <> 7 THEN RAISE EXCEPTION 'FAIL 00093 FA2: seven categories'; END IF;

    -- FA3: 1591 and 7540 are the user's: neither is used nor changed; the new accounts are 1596 and 7541, in Indonesian; the categories point at them
    IF (pg_temp.m93_a('FA3', '1591')).name <> 'Akumulasi lain (milik pengguna)' OR (pg_temp.m93_a('FA3', '7540')).name <> 'Beban lain (milik pengguna)' THEN
        RAISE EXCEPTION 'FAIL 00093 FA3: the accounts of the user are not touched';
    END IF;
    a := pg_temp.m93_a('FA3', '1596');
    IF a.id IS NULL OR a.name <> 'Akumulasi penyusutan – bangunan' THEN RAISE EXCEPTION 'FAIL 00093 FA3: 1596 (%)', a.name; END IF;
    IF (pg_temp.m93_a('FA3', '7541')).name IS DISTINCT FROM 'Penyusutan – kendaraan' THEN RAISE EXCEPTION 'FAIL 00093 FA3: 7541'; END IF;
    SELECT * INTO c FROM fa_categories WHERE property_id = pg_temp.m93_p('FA3') AND code = 'BUILDING';
    IF c.accumulated_account_id IS DISTINCT FROM (pg_temp.m93_a('FA3', '1596')).id OR c.name <> 'Bangunan' THEN RAISE EXCEPTION 'FAIL 00093 FA3: the building category takes 1596'; END IF;
    SELECT * INTO c FROM fa_categories WHERE property_id = pg_temp.m93_p('FA3') AND code = 'VEHICLE';
    IF c.expense_account_id IS DISTINCT FROM (pg_temp.m93_a('FA3', '7541')).id THEN RAISE EXCEPTION 'FAIL 00093 FA3: the vehicle category takes 7541'; END IF;

    -- FA4: no chart: no accounts and no categories, but the settings and the fiscal groups exist
    IF EXISTS (SELECT 1 FROM gl_accounts WHERE property_id = pg_temp.m93_p('FA4')) OR EXISTS (SELECT 1 FROM fa_categories WHERE property_id = pg_temp.m93_p('FA4')) THEN
        RAISE EXCEPTION 'FAIL 00093 FA4: a property with no chart gets no accounts and no categories';
    END IF;

    -- every property has its settings, its six fiscal groups and the two sequences
    IF (SELECT count(*) FROM fa_settings) <> (SELECT count(*) FROM properties) OR (SELECT count(*) FROM fa_fiscal_groups) <> 6 * (SELECT count(*) FROM properties) THEN
        RAISE EXCEPTION 'FAIL 00093: every property has the settings and the fiscal groups';
    END IF;
    IF (SELECT count(*) FROM document_sequences WHERE sequence_type = 'FIXED_ASSET' AND prefix = 'FA-') <> (SELECT count(*) FROM properties)
       OR (SELECT count(*) FROM document_sequences WHERE sequence_type = 'FA_OPENING' AND prefix = 'FAI-') <> (SELECT count(*) FROM properties) THEN
        RAISE EXCEPTION 'FAIL 00093: every property has the sequences FIXED_ASSET and FA_OPENING';
    END IF;
    IF (SELECT sl_rate FROM fa_fiscal_groups WHERE property_id = pg_temp.m93_p('FA1') AND code = 'G2') <> 12.5 THEN RAISE EXCEPTION 'FAIL 00093: the rate of group 2'; END IF;
    RAISE NOTICE 'PASS  00093: the fixed asset accounts for a chart with 1590 unused (header), with a journal on 1590 (under 1500), with 1591 and 7540 taken (1596, 7541), and none without a chart';
END
$$;
