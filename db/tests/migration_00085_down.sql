-- What the Down of migration 00085 left (it ran on a database that held no purchasing data).
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM document_sequences WHERE sequence_type IN ('INV_PR', 'INV_PO', 'INV_GR', 'INV_RET')) THEN RAISE EXCEPTION 'FAIL 00085 down: the sequences of purchasing are removed'; END IF;
    IF to_regclass('inv_requests') IS NOT NULL OR to_regclass('inv_receipts') IS NOT NULL OR to_regclass('inv_bill_matches') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00085 down: the tables are dropped'; END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'departments' AND column_name IN ('head_user_id', 'deputy_user_id')) THEN RAISE EXCEPTION 'FAIL 00085 down: the columns of departments are dropped'; END IF;
    IF to_regclass('inv_movements') IS NULL THEN RAISE EXCEPTION 'FAIL 00085 down: the tables of 00084 stay'; END IF;
    RAISE NOTICE 'PASS  00085 down: the tables, the columns and the sequences are gone and the inventory of 00084 is as it was';
END
$$;
