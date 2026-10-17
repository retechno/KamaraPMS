-- What the Down of migration 00086 left (it ran on a database that held no movements data).
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type IN ('INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT')) THEN RAISE EXCEPTION 'FAIL 00086 down: the sequences of the movements are removed'; END IF;
    IF to_regclass('inv_requisitions') IS NOT NULL OR to_regclass('inv_transfers') IS NOT NULL OR to_regclass('inv_issues') IS NOT NULL OR to_regclass('inv_counts') IS NOT NULL
       OR to_regclass('inv_amenity_runs') IS NOT NULL THEN
        RAISE EXCEPTION 'FAIL 00086 down: the tables are dropped';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'inv_approval_steps' AND column_name = 'requisition_id') THEN RAISE EXCEPTION 'FAIL 00086 down: the column of the steps is dropped'; END IF;
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'inv_warehouses_count_fk') THEN RAISE EXCEPTION 'FAIL 00086 down: the foreign key of the freeze is dropped'; END IF;
    IF to_regclass('inv_requests') IS NULL OR to_regclass('inv_approval_steps') IS NULL THEN RAISE EXCEPTION 'FAIL 00086 down: the tables of 00085 stay'; END IF;
    RAISE NOTICE 'PASS  00086 down: the tables, the column and the sequences are gone and purchasing is as it was';
END
$$;
