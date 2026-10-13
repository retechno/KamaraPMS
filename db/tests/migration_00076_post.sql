-- What migration 00076 granted to the roles made by migration_00076_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m76_has(p_tenant text, p_role text, p_code text) RETURNS boolean LANGUAGE sql AS
    $$ SELECT EXISTS (SELECT 1 FROM role_permissions rp JOIN roles r ON r.id = rp.role_id JOIN tenants t ON t.id = r.tenant_id
                       WHERE t.code = $1 AND r.name = $2 AND rp.permission_code = $3) $$;
DO $$
BEGIN
    IF NOT pg_temp.m76_has('ABC', 'M76 Supervisor', 'folio.master_manage') THEN RAISE EXCEPTION 'FAIL 00076: a role with correction.approve gets folio.master_manage'; END IF;
    IF NOT pg_temp.m76_has('ABC', 'M76 Supervisor', 'folio.master_close') THEN RAISE EXCEPTION 'FAIL 00076: a role with correction.approve gets folio.master_close'; END IF;
    IF NOT pg_temp.m76_has('ABC', 'M76 Supervisor', 'folio.read') THEN RAISE EXCEPTION 'FAIL 00076: the rights a role had stay'; END IF;
    IF pg_temp.m76_has('ABC', 'M76 Cashier', 'folio.master_close') OR pg_temp.m76_has('ABC', 'M76 Cashier', 'folio.master_manage') THEN RAISE EXCEPTION 'FAIL 00076: folio.post_charge and folio.reverse (cashier level) give neither right'; END IF;
    IF pg_temp.m76_has('ABC', 'M76 Viewer', 'folio.master_manage') OR pg_temp.m76_has('ABC', 'M76 Viewer', 'folio.master_close') THEN RAISE EXCEPTION 'FAIL 00076: a role with neither right gets nothing'; END IF;
    IF NOT (pg_temp.m76_has('ABC', 'M76 Both', 'folio.master_manage') AND pg_temp.m76_has('ABC', 'M76 Both', 'folio.master_close')) THEN RAISE EXCEPTION 'FAIL 00076: a role with both rights gets both'; END IF;
    IF NOT pg_temp.m76_has('XYZ', 'M76 Supervisor', 'folio.master_manage') THEN RAISE EXCEPTION 'FAIL 00076: another tenant is handled by its own roles'; END IF;
    IF EXISTS (SELECT 1 FROM role_permissions WHERE permission_code = 'folio.transfer_any' AND role_id NOT IN (SELECT role_id FROM role_permissions rp2 JOIN roles r ON r.id = rp2.role_id WHERE r.name = 'M76 Admin transfer')) THEN RAISE EXCEPTION 'FAIL 00076: folio.transfer_any is given to nobody'; END IF;
    IF pg_temp.m76_has('ABC', 'M76 Admin transfer', 'folio.master_manage') THEN RAISE EXCEPTION 'FAIL 00076: a role with only folio.transfer_any gets nothing else'; END IF;
    IF (SELECT count(*) FROM role_permissions WHERE permission_code = 'folio.master_close' AND role_id IN (SELECT id FROM roles WHERE name = 'M76 Admin transfer')) <> 1 THEN RAISE EXCEPTION 'FAIL 00076: a right a role had already is not doubled'; END IF;
    RAISE NOTICE 'migration 00076: rights of the master folio granted as designed';
END $$;
