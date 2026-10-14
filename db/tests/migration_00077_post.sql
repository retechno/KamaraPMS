-- What migration 00077 granted to the roles made by migration_00077_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m77_has(p_tenant text, p_role text, p_code text) RETURNS boolean LANGUAGE sql AS
    $$ SELECT EXISTS (SELECT 1 FROM role_permissions rp JOIN roles r ON r.id = rp.role_id JOIN tenants t ON t.id = r.tenant_id
                       WHERE t.code = $1 AND r.name = $2 AND rp.permission_code = $3) $$;
DO $$
BEGIN
    IF NOT pg_temp.m77_has('ABC', 'M77 Supervisor', 'group.cancel') THEN RAISE EXCEPTION 'FAIL 00077: a role with correction.approve gets group.cancel'; END IF;
    IF NOT pg_temp.m77_has('ABC', 'M77 Supervisor', 'reservation.read') THEN RAISE EXCEPTION 'FAIL 00077: the rights a role had stay'; END IF;
    IF pg_temp.m77_has('ABC', 'M77 Cashier', 'group.cancel') THEN RAISE EXCEPTION 'FAIL 00077: reservation.cancel and the cashier-level folio rights do not give group.cancel'; END IF;
    IF pg_temp.m77_has('ABC', 'M77 Viewer', 'group.cancel') THEN RAISE EXCEPTION 'FAIL 00077: a role without correction.approve gets nothing'; END IF;
    IF (SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'M77 Already' AND rp.permission_code = 'group.cancel') <> 1 THEN RAISE EXCEPTION 'FAIL 00077: a right a role had already is not doubled'; END IF;
    IF NOT pg_temp.m77_has('XYZ', 'M77 Supervisor', 'group.cancel') THEN RAISE EXCEPTION 'FAIL 00077: another tenant is handled by its own roles'; END IF;
    RAISE NOTICE 'migration 00077: the right to cancel a whole group granted as designed';
END $$;
