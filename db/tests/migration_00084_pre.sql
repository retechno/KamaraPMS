-- Properties as they are before migration 00084 (inventory), for the cases of its accounts. Run by scripts/db-test.sh after the schema tests, with the Down of 00084 applied;
-- then the Up of 00084 is applied again and migration_00084_post.sql checks the result.
\set ON_ERROR_STOP on

INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
SELECT t.id, x.code, x.code, 'Asia/Jakarta', 'IDR', 0, '14:00', '12:00'
  FROM tenants t, (VALUES ('I1'), ('I2'), ('I3')) AS x (code)
 WHERE t.code = 'ABC';

CREATE TEMPORARY TABLE m84 AS SELECT
    (SELECT id FROM tenants WHERE code = 'ABC') AS t,
    (SELECT id FROM properties WHERE code = 'I1') AS i1,
    (SELECT id FROM properties WHERE code = 'I2') AS i2,
    (SELECT id FROM properties WHERE code = 'I3') AS i3;

-- I1: a chart in English with the headers and the cost accounts the keys point at, and none of the new accounts.
-- I2: a chart in Indonesian whose 2130 and 5290 are the user's own (the new accounts take 2131 and 5291; 5295 is free).
-- I3: no chart at all.
INSERT INTO accounting_settings (tenant_id, property_id, start_date) SELECT t, p, '2026-10-01' FROM m84, LATERAL (VALUES (i1), (i2)) AS v (p);

INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group)
SELECT t, p, c.code, CASE WHEN p = i2 THEN c.name_id ELSE c.name_en END, c.account_type, c.normal_side, c.postable, c.grp
  FROM m84, LATERAL (VALUES (i1), (i2)) AS v (p),
       (VALUES ('1150', 'Credit card clearing', 'Akun penampung kartu kredit', 'ASSET', 'DEBIT', true, 'CASH'),
               ('2100', 'Payables', 'Utang usaha', 'LIABILITY', 'CREDIT', false, 'PAYABLES'),
               ('5200', 'Food and beverage department expenses', 'Beban departemen makanan dan minuman', 'EXPENSE', 'DEBIT', false, 'EXP_FB'),
               ('5210', 'Cost of food sales', 'HPP makanan', 'EXPENSE', 'DEBIT', true, 'EXP_FB'),
               ('5220', 'Cost of beverage sales', 'HPP minuman', 'EXPENSE', 'DEBIT', true, 'EXP_FB'),
               ('5240', 'Employee benefits', 'Tunjangan karyawan', 'EXPENSE', 'DEBIT', true, 'EXP_FB'),
               ('6170', 'Travel and entertainment', 'Perjalanan dan entertainment', 'EXPENSE', 'DEBIT', true, 'UND_AG')) AS c (code, name_en, name_id, account_type, normal_side, postable, grp);

-- the user's own accounts on the standard codes of I2
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group)
SELECT t, i2, '2130', 'Utang lain-lain (milik pengguna)', 'LIABILITY', 'CREDIT', true, 'PAYABLES' FROM m84
UNION ALL SELECT t, i2, '5290', 'Beban lain (milik pengguna)', 'EXPENSE', 'DEBIT', true, 'EXP_FB' FROM m84;
