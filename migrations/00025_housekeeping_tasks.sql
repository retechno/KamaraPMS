-- +goose Up
-- Housekeeping flags of a room (priority, do-not-disturb, make-up request, a note). Kept off room_housekeeping, whose
-- updated_at means "in this status since": changing a flag must not restart that clock.
CREATE TABLE room_hk_flags (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint       NOT NULL,
    property_id        bigint       NOT NULL,
    room_id            bigint       NOT NULL,
    priority           varchar(6)   NOT NULL DEFAULT 'NORMAL',
    dnd                boolean      NOT NULL DEFAULT false,
    make_up_requested  boolean      NOT NULL DEFAULT false,
    note               varchar(500),
    updated_at         timestamptz  NOT NULL DEFAULT now(),
    updated_by         bigint REFERENCES users (id),
    CONSTRAINT room_hk_flags_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT room_hk_flags_room_fk     FOREIGN KEY (property_id, room_id)   REFERENCES rooms (property_id, id),
    CONSTRAINT room_hk_flags_room_uk     UNIQUE (room_id),
    CONSTRAINT room_hk_flags_priority_ck CHECK (priority IN ('NORMAL', 'HIGH'))
);
CREATE TRIGGER room_hk_flags_set_updated_at BEFORE UPDATE ON room_hk_flags
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The daily cleaning list. AUTO tasks are generated from stays and reservations for a business date (at most one per
-- room, date and type); MANUAL tasks are added by a supervisor. Finishing a task moves the room's housekeeping status.
CREATE TABLE housekeeping_tasks (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    room_id        bigint       NOT NULL,
    task_date      date         NOT NULL,
    task_type      varchar(10)  NOT NULL,
    status         varchar(11)  NOT NULL DEFAULT 'PENDING',
    priority       varchar(6)   NOT NULL DEFAULT 'NORMAL',
    source         varchar(6)   NOT NULL DEFAULT 'AUTO',
    assigned_to    bigint,
    assigned_at    timestamptz,
    notes          varchar(500),
    started_at     timestamptz,
    completed_at   timestamptz,
    completed_by   bigint REFERENCES users (id),
    created_at     timestamptz  NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT hk_tasks_property_fk     FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT hk_tasks_room_fk         FOREIGN KEY (property_id, room_id)    REFERENCES rooms (property_id, id),
    CONSTRAINT hk_tasks_business_day_fk FOREIGN KEY (property_id, task_date) REFERENCES business_days (property_id, business_date),
    CONSTRAINT hk_tasks_assignee_fk     FOREIGN KEY (tenant_id, assigned_to)  REFERENCES users (tenant_id, id),
    CONSTRAINT hk_tasks_type_ck         CHECK (task_type IN ('CHECKOUT', 'STAYOVER', 'ARRIVAL', 'DIRTY', 'DEEP', 'OTHER')),
    CONSTRAINT hk_tasks_status_ck       CHECK (status IN ('PENDING', 'IN_PROGRESS', 'DONE', 'SKIPPED')),
    CONSTRAINT hk_tasks_priority_ck     CHECK (priority IN ('NORMAL', 'HIGH')),
    CONSTRAINT hk_tasks_source_ck       CHECK (source IN ('AUTO', 'MANUAL')),
    CONSTRAINT hk_tasks_assigned_ck     CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
    CONSTRAINT hk_tasks_started_ck      CHECK (status = 'PENDING' OR status = 'SKIPPED' OR started_at IS NOT NULL),
    CONSTRAINT hk_tasks_done_ck         CHECK ((status IN ('DONE', 'SKIPPED')) = (completed_at IS NOT NULL)),
    CONSTRAINT hk_tasks_skip_note_ck    CHECK (status <> 'SKIPPED' OR notes IS NOT NULL)
);
-- Generating the list twice (or two supervisors at once) never doubles a task.
CREATE UNIQUE INDEX hk_tasks_auto_uk ON housekeeping_tasks (property_id, room_id, task_date, task_type) WHERE source = 'AUTO';
CREATE INDEX hk_tasks_date_idx     ON housekeeping_tasks (property_id, task_date, status);
CREATE INDEX hk_tasks_assignee_idx ON housekeeping_tasks (property_id, assigned_to, task_date) WHERE assigned_to IS NOT NULL;
CREATE TRIGGER housekeeping_tasks_set_updated_at BEFORE UPDATE ON housekeeping_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS housekeeping_tasks;
DROP TABLE IF EXISTS room_hk_flags;
