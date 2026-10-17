-- What migration 00086 (movements) did to the properties that existed before it. Run by scripts/db-test.sh after the Up of 00086.
\set ON_ERROR_STOP on
DO $$
DECLARE
    k text;
BEGIN
    -- every property has the four sequences of the movements, with their prefixes, starting at one
    FOR k IN SELECT unnest(ARRAY['INV_SR:SR-', 'INV_TRF:TRF-', 'INV_ISS:ISS-', 'INV_CNT:CNT-']) LOOP
        IF (SELECT count(*) FROM document_sequences WHERE sequence_type = split_part(k, ':', 1) AND prefix = split_part(k, ':', 2) AND next_value = 1) <> (SELECT count(*) FROM properties) THEN
            RAISE EXCEPTION 'FAIL 00086: every property has the sequence %', split_part(k, ':', 1);
        END IF;
    END LOOP;
    -- the tables exist and are empty
    IF (SELECT count(*) FROM inv_requisitions) + (SELECT count(*) FROM inv_transfers) + (SELECT count(*) FROM inv_issues) + (SELECT count(*) FROM inv_counts) + (SELECT count(*) FROM inv_amenity_plans) <> 0 THEN
        RAISE EXCEPTION 'FAIL 00086: the new tables start empty';
    END IF;
    -- the steps and the notices of the chain can name a requisition now
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'inv_approval_steps' AND column_name = 'requisition_id')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'inv_notices' AND column_name = 'requisition_id') THEN
        RAISE EXCEPTION 'FAIL 00086: the steps and the notices have the column requisition_id';
    END IF;
    -- the freeze of a warehouse is a foreign key to the counts
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'inv_warehouses_count_fk') THEN RAISE EXCEPTION 'FAIL 00086: inv_warehouses.count_id is a foreign key'; END IF;
    RAISE NOTICE 'PASS  00086: every property has the sequences of the movements, the tables are empty, the chain knows the requisition, the freeze is a foreign key';
END
$$;
