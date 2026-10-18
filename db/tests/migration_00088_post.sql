-- What migration 00088 (the reports and the snapshots) did to the properties that existed before it. Run by scripts/db-test.sh after the Up of 00088.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('inv_snapshot_days') IS NULL OR to_regclass('inv_balance_days') IS NULL OR to_regclass('inv_cost_days') IS NULL
       OR to_regclass('inv_fb_cost_days') IS NULL OR to_regclass('inv_cost_targets') IS NULL THEN
        RAISE EXCEPTION 'FAIL 00088: the five tables of the reports exist';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'inventory_settings' AND column_name = 'engineering_department_id') THEN
        RAISE EXCEPTION 'FAIL 00088: the settings name the engineering department';
    END IF;
    IF (SELECT count(*) FROM inv_snapshot_days) + (SELECT count(*) FROM inv_cost_days) + (SELECT count(*) FROM inv_fb_cost_days) + (SELECT count(*) FROM inv_cost_targets) <> 0 THEN
        RAISE EXCEPTION 'FAIL 00088: the snapshots and the targets start empty (the days that closed before are computed from the ledger)';
    END IF;
    -- the balance of every pair at the end of its last day is what inv_balances holds now
    IF EXISTS (SELECT 1 FROM inv_balances b
                WHERE EXISTS (SELECT 1 FROM inv_movements m WHERE m.warehouse_id = b.warehouse_id AND m.item_id = b.item_id)
                  AND NOT EXISTS (SELECT 1 FROM (SELECT DISTINCT ON (d.warehouse_id, d.item_id) d.qty, d.value FROM inv_balance_days d
                                                  WHERE d.warehouse_id = b.warehouse_id AND d.item_id = b.item_id ORDER BY d.warehouse_id, d.item_id, d.business_date DESC) last
                                   WHERE last.qty = b.qty AND last.value = b.value)) THEN
        RAISE EXCEPTION 'FAIL 00088: the backfilled balance of the last day of a pair equals its balance';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'inv_cost_days_uk') OR NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'inv_fb_cost_days_uk') OR NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'inv_cost_targets_uk') THEN
        RAISE EXCEPTION 'FAIL 00088: the keys of the day and of the target';
    END IF;
    RAISE NOTICE 'PASS  00088: the tables of the reports exist and are empty, the setting is there, the backfilled balances agree with the balances';
END
$$;
