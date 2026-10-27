-- Bills posted before migration 00094 (their journal lines have no line number), for the backfill of gl_journal_lines.source_line. Run by scripts/db-test.sh with the Down of 00094 applied on the
-- property FA1 that migration_00093_pre.sql made; then the Up of 00094 is applied and migration_00094_post.sql checks the result.
\set ON_ERROR_STOP on

CREATE TEMPORARY TABLE m94 AS SELECT (SELECT id FROM tenants WHERE code = 'ABC') AS t, (SELECT id FROM properties WHERE code = 'FA1') AS p;

INSERT INTO suppliers (tenant_id, property_id, code, name) SELECT t, p, 'SUP94', 'Supplier 94' FROM m94;

BEGIN;
-- bill A: two cost lines (1530, 1510) and the payable (the account 1590 stands in for it here): the cost lines are numbered 1 and 2
INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) SELECT t, p, 'JV-94A', 'PAYABLES', DATE '2026-10-05', 'Bill A' FROM m94;
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, description, source_type, source_ref)
SELECT m.t, m.p, (SELECT id FROM gl_journals WHERE property_id = m.p AND journal_number = 'JV-94A'), v.n, (SELECT id FROM gl_accounts WHERE property_id = m.p AND code = v.acc), v.dr, v.cr, v.d, 'AP_BILL', 'BILL94A'
  FROM m94 m, (VALUES (1, '1530', 100, 0, 'Chair'), (2, '1510', 200, 0, 'Table'), (3, '1590', 0, 300, 'Invoice INV-A')) AS v (n, acc, dr, cr, d);
INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
SELECT m.t, m.p, 'BILL94A', (SELECT id FROM suppliers WHERE property_id = m.p AND code = 'SUP94'), 'INV-A', DATE '2026-10-05', DATE '2026-11-05', 300, (SELECT id FROM gl_journals WHERE property_id = m.p AND journal_number = 'JV-94A') FROM m94 m;
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, description, amount)
SELECT m.t, m.p, (SELECT id FROM supplier_bills WHERE property_id = m.p AND bill_number = 'BILL94A'), v.n, (SELECT id FROM gl_accounts WHERE property_id = m.p AND code = v.acc), v.d, v.a
  FROM m94 m, (VALUES (1, '1530', 'Chair', 100), (2, '1510', 'Table', 200)) AS v (n, acc, d, a);

-- bill B: one cost line with creditable VAT (the VAT line is "VAT ..." and has no number)
INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) SELECT t, p, 'JV-94B', 'PAYABLES', DATE '2026-10-06', 'Bill B' FROM m94;
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, description, source_type, source_ref)
SELECT m.t, m.p, (SELECT id FROM gl_journals WHERE property_id = m.p AND journal_number = 'JV-94B'), v.n, (SELECT id FROM gl_accounts WHERE property_id = m.p AND code = v.acc), v.dr, v.cr, v.d, 'AP_BILL', 'BILL94B'
  FROM m94 m, (VALUES (1, '1530', 100, 0, 'Sofa'), (2, '1520', 11, 0, 'VAT Sofa'), (3, '1590', 0, 111, 'Invoice INV-B')) AS v (n, acc, dr, cr, d);
INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
SELECT m.t, m.p, 'BILL94B', (SELECT id FROM suppliers WHERE property_id = m.p AND code = 'SUP94'), 'INV-B', DATE '2026-10-06', DATE '2026-11-06', 100, (SELECT id FROM gl_journals WHERE property_id = m.p AND journal_number = 'JV-94B') FROM m94 m;
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, description, amount)
SELECT m.t, m.p, (SELECT id FROM supplier_bills WHERE property_id = m.p AND bill_number = 'BILL94B'), 1, (SELECT id FROM gl_accounts WHERE property_id = m.p AND code = '1530'), 'Sofa', 100 FROM m94 m;

-- a manual journal that mentions no bill: no number
INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) SELECT t, p, 'JV-94M', 'MANUAL', DATE '2026-10-07', 'Manual' FROM m94;
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, description)
SELECT m.t, m.p, (SELECT id FROM gl_journals WHERE property_id = m.p AND journal_number = 'JV-94M'), v.n, (SELECT id FROM gl_accounts WHERE property_id = m.p AND code = v.acc), v.dr, v.cr, 'Manual'
  FROM m94 m, (VALUES (1, '1530', 50, 0), (2, '1590', 0, 50)) AS v (n, acc, dr, cr);
COMMIT;
