-- What migration 00069 did to the duplicates made by migration_00069_pre.sql.
\set ON_ERROR_STOP on
CREATE FUNCTION pg_temp.m69_row(p_room text, p_date date, p_type text) RETURNS housekeeping_tasks LANGUAGE sql AS
    $$ SELECT t.* FROM housekeeping_tasks t JOIN rooms r ON r.id = t.room_id WHERE r.room_number = $1 AND t.task_date = $2 AND t.task_type = $3 $$;
DO $$
DECLARE r housekeeping_tasks;
BEGIN
    r := pg_temp.m69_row('201', '2026-09-30', 'STAYOVER');
    IF r.status <> 'PENDING' OR r.source <> 'AUTO' THEN RAISE EXCEPTION 'FAIL 00069 A: the newest PENDING task stays (%, %)', r.status, r.source; END IF;
    r := pg_temp.m69_row('201', '2026-09-30', 'CHECKOUT');
    IF r.status <> 'SKIPPED' OR r.completed_at IS NULL OR r.notes NOT LIKE 'Digabung: tugas ganda (migrasi 00069)%' THEN RAISE EXCEPTION 'FAIL 00069 A: the older PENDING task is skipped with a note (%, %)', r.status, r.notes; END IF;

    r := pg_temp.m69_row('305', '2026-09-30', 'STAYOVER');
    IF r.status <> 'IN_PROGRESS' OR r.source <> 'AUTO' THEN RAISE EXCEPTION 'FAIL 00069 B: the newest IN_PROGRESS task stays (%, %)', r.status, r.source; END IF;
    r := pg_temp.m69_row('305', '2026-09-30', 'DIRTY');
    IF r.status <> 'IN_PROGRESS' OR r.source <> 'MANUAL' OR r.notes NOT LIKE 'Tugas ganda yang sudah dimulai (migrasi 00069)%' THEN RAISE EXCEPTION 'FAIL 00069 B: the other IN_PROGRESS task becomes MANUAL with a note (%, %, %)', r.status, r.source, r.notes; END IF;
    r := pg_temp.m69_row('305', '2026-09-30', 'CHECKOUT');
    IF r.status <> 'SKIPPED' THEN RAISE EXCEPTION 'FAIL 00069 B: the PENDING task next to started work is skipped (%)', r.status; END IF;

    r := pg_temp.m69_row('410', '2026-09-30', 'CHECKOUT');
    IF r.status <> 'DONE' OR r.source <> 'AUTO' OR r.notes IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00069 C: a DONE task is not touched (%, %, %)', r.status, r.source, r.notes; END IF;
    r := pg_temp.m69_row('410', '2026-09-30', 'STAYOVER');
    IF r.status <> 'PENDING' THEN RAISE EXCEPTION 'FAIL 00069 C: the PENDING task next to a DONE one stays (%)', r.status; END IF;

    r := pg_temp.m69_row('201', '2026-10-01', 'DEEP');
    IF r.status <> 'PENDING' OR r.source <> 'MANUAL' OR r.notes IS NOT NULL THEN RAISE EXCEPTION 'FAIL 00069 D: a MANUAL task is not touched'; END IF;
    r := pg_temp.m69_row('201', '2026-10-01', 'DIRTY');
    IF r.status <> 'PENDING' OR r.source <> 'AUTO' THEN RAISE EXCEPTION 'FAIL 00069 D: the AUTO task next to a MANUAL one stays'; END IF;

    BEGIN
        INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source)
        SELECT tenant_id, property_id, room_id, task_date, 'OTHER', 'PENDING', 'AUTO' FROM housekeeping_tasks WHERE id = r.id;
        RAISE EXCEPTION 'FAIL 00069: the new index accepted a second active AUTO task';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    RAISE NOTICE 'PASS  migration 00069 puts duplicate AUTO tasks right and the new index holds';
END
$$;
DELETE FROM housekeeping_tasks;
