-- Properties as they are before migration 00071 (QRIS), for the three cases of its account. Run by scripts/db-test.sh after the schema tests, with the Down of 00071 applied;
-- then the Up of 00071 is applied again and migration_00071_post.sql checks the result.
\set ON_ERROR_STOP on

INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
SELECT t.id, x.code, x.code, 'Asia/Jakarta', 'IDR', 0, '14:00', '12:00'
  FROM tenants t, (VALUES ('Q1'), ('Q2'), ('Q3'), ('Q4')) AS x (code)
 WHERE t.code = 'ABC';

CREATE TEMPORARY TABLE m71 AS SELECT
    (SELECT id FROM tenants WHERE code = 'ABC') AS t,
    (SELECT id FROM properties WHERE code = 'Q1') AS q1,
    (SELECT id FROM properties WHERE code = 'Q2') AS q2,
    (SELECT id FROM properties WHERE code = 'Q3') AS q3,
    (SELECT id FROM properties WHERE code = 'Q4') AS q4;

-- Q1: a chart with no account 1155 (English names). Q2: a chart in Indonesian whose 1155 and 1156 are the user's own. Q3: no chart at all. Q4: 1155 is the user's own and there is no account 1100.
INSERT INTO accounting_settings (tenant_id, property_id, start_date) SELECT t, p, '2026-10-01' FROM m71, LATERAL (VALUES (q1), (q2), (q4)) AS v (p);

INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable, statement_group)
SELECT t, q1, '1100', 'Cash and equivalents', 'ASSET', 'DEBIT', false, 'CASH' FROM m71
UNION ALL SELECT t, q1, '1150', 'Credit card clearing', 'ASSET', 'DEBIT', true, 'CASH' FROM m71
UNION ALL SELECT t, q2, '1100', 'Kas dan setara kas', 'ASSET', 'DEBIT', false, 'CASH' FROM m71
UNION ALL SELECT t, q2, '1150', 'Akun penampung kartu kredit', 'ASSET', 'DEBIT', true, 'CASH' FROM m71
UNION ALL SELECT t, q2, '1155', 'Dompet digital (milik pengguna)', 'ASSET', 'DEBIT', true, 'CASH' FROM m71
UNION ALL SELECT t, q2, '1156', 'Akun lain (milik pengguna)', 'ASSET', 'DEBIT', true, 'CASH' FROM m71
UNION ALL SELECT t, q4, '1150', 'Credit card clearing', 'ASSET', 'DEBIT', true, 'CASH' FROM m71
UNION ALL SELECT t, q4, '1155', 'Voucher (user account)', 'ASSET', 'DEBIT', true, 'CASH' FROM m71;
