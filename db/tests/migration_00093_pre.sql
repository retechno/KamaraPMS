-- Properties as they are before migration 00093 (fixed assets), for the cases of its accounts. Run by scripts/db-test.sh with the Down of 00093 applied;
-- then the Up of 00093 is applied again and migration_00093_post.sql checks the result.
\set ON_ERROR_STOP on

INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
SELECT t.id, x.code, x.code, 'Asia/Jakarta', 'IDR', 0, '14:00', '12:00'
  FROM tenants t, (VALUES ('FA1'), ('FA2'), ('FA3'), ('FA4')) AS x (code)
 WHERE t.code = 'ABC';

CREATE TEMPORARY TABLE m93 AS SELECT
    (SELECT id FROM tenants WHERE code = 'ABC') AS t,
    (SELECT id FROM properties WHERE code = 'FA1') AS f1,
    (SELECT id FROM properties WHERE code = 'FA2') AS f2,
    (SELECT id FROM properties WHERE code = 'FA3') AS f3,
    (SELECT id FROM properties WHERE code = 'FA4') AS f4;

-- FA1: the old chart of the assets: 1590 postable with nothing posted to it.
-- FA2: the same, but a journal posted to 1590 (a hotel that booked its accumulated depreciation by hand).
-- FA3: the old chart, in Indonesian, where 1591 and 7540 are the user's own accounts (the new ones take 1596 and 7541).
-- FA4: no chart at all.
INSERT INTO accounting_settings (tenant_id, property_id, start_date) SELECT t, p, '2026-10-01' FROM m93, LATERAL (VALUES (f1), (f2), (f3)) AS v (p);

INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group)
SELECT t, p, c.code, CASE WHEN p = f3 THEN c.name_id ELSE c.name_en END, c.account_type, c.normal_side, c.postable, c.grp
  FROM m93, LATERAL (VALUES (f1), (f2), (f3)) AS v (p),
       (VALUES ('1150', 'Credit card clearing', 'Akun penampung kartu kredit', 'ASSET', 'DEBIT', true, 'CASH'),
               ('1500', 'Property and equipment', 'Aset tetap', 'ASSET', 'DEBIT', false, 'FIXED_ASSETS'),
               ('1510', 'Land', 'Tanah', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1520', 'Buildings', 'Bangunan', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1530', 'Furniture, fixtures and equipment', 'Furnitur, perlengkapan, dan peralatan', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1540', 'Operating equipment (china, glass, linen)', 'Peralatan operasional (porselen, gelas, linen)', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1550', 'Vehicles', 'Kendaraan', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1560', 'Construction in progress', 'Aset dalam penyelesaian', 'ASSET', 'DEBIT', true, 'FIXED_ASSETS'),
               ('1590', 'Accumulated depreciation', 'Akumulasi penyusutan', 'ASSET', 'CREDIT', true, 'FIXED_ASSETS'),
               ('7500', 'Depreciation and amortization', 'Penyusutan dan amortisasi', 'EXPENSE', 'DEBIT', false, 'DEPRECIATION'),
               ('7510', 'Depreciation - buildings', 'Penyusutan – bangunan', 'EXPENSE', 'DEBIT', true, 'DEPRECIATION'),
               ('7520', 'Depreciation - furniture, fixtures and equipment', 'Penyusutan – furnitur, perlengkapan, dan peralatan', 'EXPENSE', 'DEBIT', true, 'DEPRECIATION'),
               ('7530', 'Amortization', 'Amortisasi', 'EXPENSE', 'DEBIT', true, 'DEPRECIATION')) AS c (code, name_en, name_id, account_type, normal_side, postable, grp);

-- the user's own accounts on two standard codes of FA3
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group)
SELECT t, f3, '1591', 'Akumulasi lain (milik pengguna)', 'ASSET', 'CREDIT', true, 'FIXED_ASSETS' FROM m93
UNION ALL SELECT t, f3, '7540', 'Beban lain (milik pengguna)', 'EXPENSE', 'DEBIT', true, 'DEPRECIATION' FROM m93;

-- FA2: a journal that credits 1590
WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
           SELECT t, f2, 'JV-M93', 'MANUAL', '2026-10-01', 'accumulated depreciation by hand' FROM m93 RETURNING id, tenant_id, property_id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT j.tenant_id, j.property_id, j.id, v.n, (SELECT id FROM gl_accounts a WHERE a.property_id = j.property_id AND a.code = v.code), v.dr, v.cr
  FROM j, (VALUES (1, '7520', 100, 0), (2, '1590', 0, 100)) AS v (n, code, dr, cr);
