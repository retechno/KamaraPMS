-- What migration 00085 (purchasing) did to the properties that existed before it (the ones made by migration_00084_pre.sql and the schema tests). Run by scripts/db-test.sh after the Up of 00085.
\set ON_ERROR_STOP on
DO $$
DECLARE
    k text;
BEGIN
    -- every property has the four sequences of purchasing, with their prefixes, starting at one
    FOR k IN SELECT unnest(ARRAY['INV_PR:PR-', 'INV_PO:PO-', 'INV_GR:GR-', 'INV_RET:RET-']) LOOP
        IF (SELECT count(*) FROM document_sequences WHERE sequence_type = split_part(k, ':', 1) AND prefix = split_part(k, ':', 2) AND next_value = 1) <> (SELECT count(*) FROM properties) THEN
            RAISE EXCEPTION 'FAIL 00085: every property has the sequence %', split_part(k, ':', 1);
        END IF;
    END LOOP;
    -- no department has a head yet
    IF EXISTS (SELECT 1 FROM departments WHERE head_user_id IS NOT NULL OR deputy_user_id IS NOT NULL) THEN RAISE EXCEPTION 'FAIL 00085: no department has a head or a deputy'; END IF;
    -- the tables exist and are empty (a chain is made when a property first uses it, not by the migration)
    IF (SELECT count(*) FROM inv_approval_levels) + (SELECT count(*) FROM inv_approval_settings) + (SELECT count(*) FROM inv_requests) <> 0 THEN RAISE EXCEPTION 'FAIL 00085: the new tables start empty'; END IF;
    RAISE NOTICE 'PASS  00085: every property has the sequences of purchasing, no department has a head, the tables are empty';
END
$$;
