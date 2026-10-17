-- What the Down of migration 00087 left (it ran on a database that held no POS deduction data).
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('inv_consumption_queue') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00087 down: the queue is dropped'; END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_outlets' AND column_name IN ('warehouse_id', 'void_waste_after_minutes'))
       OR EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_categories' AND column_name = 'fb_class') THEN
        RAISE EXCEPTION 'FAIL 00087 down: the columns of the outlet and of the category are dropped';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_recipe_lines' AND column_name = 'unit')
       OR EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_recipe_lines' AND column_name = 'unit_id') THEN
        RAISE EXCEPTION 'FAIL 00087 down: the recipe line has its unit code again';
    END IF;
    IF to_regclass('inv_consumption_queue') IS NULL AND to_regclass('inv_requisitions') IS NULL THEN RAISE EXCEPTION 'FAIL 00087 down: the tables of 00086 stay'; END IF;
    RAISE NOTICE 'PASS  00087 down: the queue and the columns are gone, the recipe line is as 00080 made it';
END
$$;
