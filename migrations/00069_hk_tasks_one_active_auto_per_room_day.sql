-- +goose Up
-- One ACTIVE generated (AUTO) cleaning task per room and day. The unique index of migration 00025 was on (room, date, TYPE), so a
-- room whose stay was extended after the list was made could hold a CHECKOUT and a STAYOVER task for the same day. From now on the
-- generator corrects the kind of a task that has not been started (and adds none when the room's task is started, done or skipped),
-- and this index is the backstop when two runs meet. Only PENDING and IN_PROGRESS count: a DONE or SKIPPED task is history, and the
-- generator itself adds nothing to a room that has one (that is its rule, not the index's). MANUAL tasks are not covered: a
-- supervisor may add as many as the work needs.
--
-- The groups that already break the rule are put right first, and no row is deleted:
--   * the task that stays is the IN_PROGRESS one (the newest if there are several), else the newest PENDING one (the kind the room needs now);
--   * the other PENDING tasks become SKIPPED with a note;
--   * another IN_PROGRESS task (work that was really started) becomes a MANUAL task with a note, so the work stays on the list;
--   * a DONE task is never touched.
-- The number of rows it changed is reported as a NOTICE.

-- +goose StatementBegin
DO $$
DECLARE
    v_skipped bigint;
    v_manual  bigint;
BEGIN
    CREATE TEMPORARY TABLE hk_dup_tasks ON COMMIT DROP AS
    SELECT id, status
    FROM (
        SELECT id, status,
               row_number() OVER (PARTITION BY property_id, room_id, task_date ORDER BY (status = 'IN_PROGRESS') DESC, id DESC) AS rn
        FROM housekeeping_tasks
        WHERE source = 'AUTO' AND status IN ('PENDING', 'IN_PROGRESS')
    ) r
    WHERE rn > 1;

    UPDATE housekeeping_tasks t
    SET status = 'SKIPPED', completed_at = now(),
        notes = 'Digabung: tugas ganda (migrasi 00069)' || COALESCE(' · ' || left(t.notes, 400), '')
    FROM hk_dup_tasks d
    WHERE d.id = t.id AND d.status = 'PENDING';
    GET DIAGNOSTICS v_skipped = ROW_COUNT;

    UPDATE housekeeping_tasks t
    SET source = 'MANUAL',
        notes = 'Tugas ganda yang sudah dimulai (migrasi 00069)' || COALESCE(' · ' || left(t.notes, 400), '')
    FROM hk_dup_tasks d
    WHERE d.id = t.id AND d.status = 'IN_PROGRESS';
    GET DIAGNOSTICS v_manual = ROW_COUNT;

    RAISE NOTICE '00069: % duplicate PENDING task(s) skipped, % duplicate IN_PROGRESS task(s) made MANUAL', v_skipped, v_manual;
END
$$;
-- +goose StatementEnd

DROP INDEX IF EXISTS hk_tasks_auto_uk;
DROP INDEX IF EXISTS hk_tasks_auto_room_day_uk;
CREATE UNIQUE INDEX hk_tasks_auto_room_day_uk ON housekeeping_tasks (property_id, room_id, task_date)
    WHERE source = 'AUTO' AND status IN ('PENDING', 'IN_PROGRESS');

-- +goose Down
-- The rows that the Up changed stay as they are (a skipped duplicate stays skipped, a task made MANUAL stays MANUAL).
DROP INDEX IF EXISTS hk_tasks_auto_room_day_uk;
CREATE UNIQUE INDEX hk_tasks_auto_uk ON housekeeping_tasks (property_id, room_id, task_date, task_type) WHERE source = 'AUTO';
