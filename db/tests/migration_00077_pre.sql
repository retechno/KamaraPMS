-- Roles as they are before migration 00077 (the right to cancel a whole group). Run by scripts/db-test.sh after migration 00076; then the Up of 00077 is applied and migration_00077_post.sql checks the result.
\set ON_ERROR_STOP on

INSERT INTO roles (tenant_id, name)
SELECT t.id, x.name FROM tenants t, (VALUES ('M77 Supervisor'), ('M77 Cashier'), ('M77 Viewer'), ('M77 Already')) AS x (name) WHERE t.code = 'ABC';
INSERT INTO roles (tenant_id, name) SELECT t.id, 'M77 Supervisor' FROM tenants t WHERE t.code = 'XYZ';

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, x.code
  FROM roles r JOIN tenants t ON t.id = r.tenant_id
  JOIN (VALUES ('M77 Supervisor', 'correction.approve'), ('M77 Supervisor', 'reservation.read'),
               ('M77 Cashier', 'reservation.cancel'), ('M77 Cashier', 'folio.post_charge'), ('M77 Cashier', 'folio.reverse'),
               ('M77 Viewer', 'reservation.read'),
               ('M77 Already', 'correction.approve'), ('M77 Already', 'group.cancel')) AS x (name, code) ON x.name = r.name
 WHERE t.code IN ('ABC', 'XYZ') AND NOT (t.code = 'XYZ' AND r.name <> 'M77 Supervisor');
