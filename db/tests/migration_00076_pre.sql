-- Roles as they are before migration 00076 (rights of the master folio). Run by scripts/db-test.sh after the schema tests; then the Up of 00076 is applied and migration_00076_post.sql checks the result.
\set ON_ERROR_STOP on

INSERT INTO roles (tenant_id, name)
SELECT t.id, x.name FROM tenants t, (VALUES ('M76 Supervisor'), ('M76 Cashier'), ('M76 Viewer'), ('M76 Both'), ('M76 Admin transfer')) AS x (name) WHERE t.code = 'ABC';
INSERT INTO roles (tenant_id, name) SELECT t.id, 'M76 Supervisor' FROM tenants t WHERE t.code = 'XYZ';

INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, x.code
  FROM roles r JOIN tenants t ON t.id = r.tenant_id
  JOIN (VALUES ('M76 Supervisor', 'correction.approve'), ('M76 Supervisor', 'folio.read'),
               ('M76 Cashier', 'folio.post_charge'), ('M76 Cashier', 'folio.reverse'), ('M76 Cashier', 'payment.post'),
               ('M76 Viewer', 'folio.read'),
               ('M76 Both', 'correction.approve'), ('M76 Both', 'folio.post_charge'),
               ('M76 Admin transfer', 'folio.transfer_any'), ('M76 Admin transfer', 'folio.master_close')) AS x (name, code) ON x.name = r.name
 WHERE t.code IN ('ABC', 'XYZ') AND NOT (t.code = 'XYZ' AND r.name <> 'M76 Supervisor');
