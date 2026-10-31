-- What the Down of migration 00098 left.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('nap_packs') IS NOT NULL OR to_regclass('nap_pack_sections') IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00098 down: the tables are still there'; END IF;
    IF to_regprocedure('nap_packs_day_closed()') IS NOT NULL OR to_regprocedure('nap_packs_status_only()') IS NOT NULL OR to_regprocedure('nap_pack_sections_failed_only()') IS NOT NULL THEN
        RAISE EXCEPTION 'FAIL 00098 down: a guard function is still there';
    END IF;
    IF pg_get_functiondef('public.property_has_financial_data(bigint)'::regprocedure) LIKE '%nap_packs%' THEN RAISE EXCEPTION 'FAIL 00098 down: the currency lock still looks at the packs'; END IF;
    RAISE NOTICE 'PASS  00098 down: the tables, the guards and the look of the currency lock are gone';
END
$$;
