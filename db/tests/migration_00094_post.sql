-- The journals of the bills made by migration_00094_pre.sql, after the Up of migration 00094.
\set ON_ERROR_STOP on
DO $$
DECLARE
    v_a text;
    v_b text;
    v_m text;
BEGIN
    SELECT string_agg(coalesce(l.source_line::text, '-'), ',' ORDER BY l.line_no) INTO v_a FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number = 'JV-94A';
    SELECT string_agg(coalesce(l.source_line::text, '-'), ',' ORDER BY l.line_no) INTO v_b FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number = 'JV-94B';
    SELECT string_agg(coalesce(l.source_line::text, '-'), ',' ORDER BY l.line_no) INTO v_m FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number = 'JV-94M';
    IF v_a <> '1,2,-' THEN RAISE EXCEPTION 'FAIL 00094 up: the cost lines of bill A are numbered 1 and 2, got %', v_a; END IF;
    IF v_b <> '1,-,-' THEN RAISE EXCEPTION 'FAIL 00094 up: the cost line of bill B is numbered 1 and its VAT line has no number, got %', v_b; END IF;
    IF v_m <> '-,-' THEN RAISE EXCEPTION 'FAIL 00094 up: a manual journal has no line numbers, got %', v_m; END IF;
    IF NOT (SELECT tgenabled = 'O' FROM pg_trigger WHERE tgname = 'gl_journal_lines_append_only') THEN RAISE EXCEPTION 'FAIL 00094 up: the journal lines are append-only again'; END IF;
    RAISE NOTICE 'PASS  00094 up: the lines of the bills posted before it carry their bill line number, the others none, and the journal lines are append-only again';
END
$$;
