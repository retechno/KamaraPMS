-- What the Down of migration 00099 left.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('nap_recipients') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00099 down: the table of the recipients is still there'; END IF;
    IF to_regprocedure('nap_sections_valid(text[])') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00099 down: the function of the sections is still there'; END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'email_outbox' AND column_name LIKE 'nap\_%') THEN RAISE EXCEPTION 'FAIL 00099 down: a column of the pack is still in the outbox'; END IF;
    IF pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conname = 'email_outbox_kind_ck')) LIKE '%NIGHT_AUDIT_PACK%' THEN RAISE EXCEPTION 'FAIL 00099 down: the kind of the pack is still allowed'; END IF;
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname IN ('email_outbox_nap_sent_ck', 'email_outbox_pack_fk')) THEN RAISE EXCEPTION 'FAIL 00099 down: a constraint of the pack is still there'; END IF;
    IF to_regclass('email_outbox_pack_queued_uk') IS NOT NULL OR to_regclass('email_outbox_pack_idx') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00099 down: an index of the pack is still there'; END IF;
    RAISE NOTICE 'PASS  00099 down: the recipients, the columns, the kind and the indexes of the pack are gone';
END
$$;
