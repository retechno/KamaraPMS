-- What the Down of migration 00094 left of the journals made by migration_00094_pre.sql.
\set ON_ERROR_STOP on
DO $$
BEGIN
    IF to_regclass('fa_source_links') IS NOT NULL OR to_regclass('fa_source_dismissals') IS NOT NULL OR to_regclass('fa_depr_runs') IS NOT NULL OR to_regclass('fa_depr_lines') IS NOT NULL THEN
        RAISE EXCEPTION 'FAIL 00094 down: the tables are still there';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'gl_journal_lines' AND column_name = 'source_line') THEN RAISE EXCEPTION 'FAIL 00094 down: gl_journal_lines.source_line is still there'; END IF;
    IF (SELECT count(*) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number IN ('JV-94A', 'JV-94B', 'JV-94M')) <> 8 THEN RAISE EXCEPTION 'FAIL 00094 down: the journal lines stay'; END IF;
    IF NOT (SELECT tgenabled = 'O' FROM pg_trigger WHERE tgname = 'gl_journal_lines_append_only') THEN RAISE EXCEPTION 'FAIL 00094 down: the journal lines are append-only again'; END IF;
    RAISE NOTICE 'PASS  00094 down: the tables and the line number are gone, the journals stay';
END
$$;
