-- What the Down of migration 00088 left (it ran on a database that held no snapshot and no target).
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('inv_snapshot_days') IS NOT NULL OR to_regclass('inv_balance_days') IS NOT NULL OR to_regclass('inv_cost_days') IS NOT NULL
       OR to_regclass('inv_fb_cost_days') IS NOT NULL OR to_regclass('inv_cost_targets') IS NOT NULL THEN
        RAISE EXCEPTION 'FAIL 00088 down: the tables of the reports are dropped';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'inventory_settings' AND column_name = 'engineering_department_id') THEN
        RAISE EXCEPTION 'FAIL 00088 down: the engineering department is dropped from the settings';
    END IF;
    RAISE NOTICE 'PASS  00088 down: the tables and the setting are gone';
END
$$;
