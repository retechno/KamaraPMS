-- Duplicate AUTO tasks as they could exist before migration 00069 (the old index was per type). Run by scripts/db-test.sh after the
-- schema tests, with the new index dropped; then the Up of 00069 is applied again and migration_00069_post.sql checks the result.
\set ON_ERROR_STOP on
DROP INDEX hk_tasks_auto_room_day_uk;
DELETE FROM housekeeping_tasks;

CREATE TEMPORARY TABLE m69 AS SELECT
    (SELECT id FROM tenants WHERE code = 'ABC') AS t,
    (SELECT id FROM properties WHERE code = 'BALI') AS p;
CREATE FUNCTION pg_temp.m69_room(text) RETURNS bigint LANGUAGE sql AS
    $$ SELECT id FROM rooms WHERE property_id = (SELECT p FROM m69) AND room_number = $1 $$;

-- A (201, 30 Sep): a CHECKOUT task and, made later, a STAYOVER task, both PENDING.
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source)
SELECT t, p, pg_temp.m69_room('201'), '2026-09-30', x, 'PENDING', 'AUTO' FROM m69, unnest(ARRAY['CHECKOUT', 'STAYOVER']) WITH ORDINALITY AS u(x, n) ORDER BY n;
-- B (305, 30 Sep): two IN_PROGRESS tasks (different kinds) and a PENDING one.
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source, started_at)
SELECT t, p, pg_temp.m69_room('305'), '2026-09-30', x, 'IN_PROGRESS', 'AUTO', now() FROM m69, unnest(ARRAY['DIRTY', 'STAYOVER']) WITH ORDINALITY AS u(x, n) ORDER BY n;
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source)
SELECT t, p, pg_temp.m69_room('305'), '2026-09-30', 'CHECKOUT', 'PENDING', 'AUTO' FROM m69;
-- C (410, 30 Sep): a DONE task and a PENDING one (the index does not count DONE: nothing changes).
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source, started_at, completed_at)
SELECT t, p, pg_temp.m69_room('410'), '2026-09-30', 'CHECKOUT', 'DONE', 'AUTO', now(), now() FROM m69;
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source)
SELECT t, p, pg_temp.m69_room('410'), '2026-09-30', 'STAYOVER', 'PENDING', 'AUTO' FROM m69;
-- D (201, 1 Oct): a MANUAL task and a PENDING AUTO task (MANUAL is never counted).
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, source)
SELECT t, p, pg_temp.m69_room('201'), '2026-10-01', x, 'PENDING', y FROM m69, (VALUES ('DEEP', 'MANUAL'), ('DIRTY', 'AUTO')) AS v(x, y);
