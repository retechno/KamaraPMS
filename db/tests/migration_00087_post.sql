-- What migration 00087 (recipes and the POS deduction) did to the properties that existed before it. Run by scripts/db-test.sh after the Up of 00087.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_outlets' AND column_name = 'warehouse_id')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_outlets' AND column_name = 'void_waste_after_minutes')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_categories' AND column_name = 'fb_class') THEN
        RAISE EXCEPTION 'FAIL 00087: the outlet has a warehouse and a void-as-waste setting, the category has a class';
    END IF;
    IF (SELECT count(*) FROM pos_outlets WHERE warehouse_id IS NOT NULL OR void_waste_after_minutes <> 0) <> 0 OR (SELECT count(*) FROM pos_categories WHERE fb_class IS NOT NULL) <> 0 THEN
        RAISE EXCEPTION 'FAIL 00087: the outlets and the categories that existed are left as they were (no warehouse, no class, void-as-waste off)';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_recipe_lines' AND column_name = 'unit')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'pos_recipe_lines' AND column_name = 'unit_id') THEN
        RAISE EXCEPTION 'FAIL 00087: the unit of a recipe line is a unit_id';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pos_recipe_lines_item_unit_fk') THEN RAISE EXCEPTION 'FAIL 00087: the unit of a recipe line is a unit of its item (composite foreign key)'; END IF;
    IF (SELECT count(*) FROM inv_consumption_queue) <> 0 THEN RAISE EXCEPTION 'FAIL 00087: the queue starts empty'; END IF;
    RAISE NOTICE 'PASS  00087: the outlet, the category and the recipe line have their new columns and keys, the queue is empty';
END
$$;
