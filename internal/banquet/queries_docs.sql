-- Banquet, PR 3: the BEO, its revisions and acknowledgements, the recipients of a section, the documents and their e-mails (docs/architecture/36-banquet.md 3.4, 4.6).
-- Every query is scoped by property_id (and tenant_id where the table has it).

-- ---------------------------------------------------------------- the texts of the BEO
-- name: ListBeoSections :many
SELECT session_id, section, body, updated_at, updated_by FROM banquet_beo_sections
WHERE property_id = @property_id AND session_id = ANY(@session_ids::bigint[]);

-- name: UpsertBeoSection :exec
INSERT INTO banquet_beo_sections (tenant_id, property_id, session_id, section, body, updated_by)
VALUES (@tenant_id, @property_id, @session_id, @section, @body, sqlc.narg(actor_id))
ON CONFLICT (session_id, section) DO UPDATE SET body = EXCLUDED.body, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: ListRunOfShow :many
SELECT session_id, seq, at_time, body FROM banquet_run_of_show
WHERE property_id = @property_id AND session_id = ANY(@session_ids::bigint[])
ORDER BY session_id, seq;

-- name: DeleteRunOfShow :exec
DELETE FROM banquet_run_of_show WHERE property_id = @property_id AND session_id = @session_id;

-- name: InsertRunOfShow :exec
INSERT INTO banquet_run_of_show (tenant_id, property_id, session_id, seq, at_time, body)
VALUES (@tenant_id, @property_id, @session_id, @seq, @at_time, @body);

-- ---------------------------------------------------------------- the revisions
-- name: LastRevision :one
SELECT revision_no, snapshot, created_at FROM banquet_beo_revisions
WHERE property_id = @property_id AND event_id = @event_id
ORDER BY revision_no DESC LIMIT 1;

-- name: InsertRevision :one
INSERT INTO banquet_beo_revisions (tenant_id, property_id, event_id, revision_no, note, changes, snapshot, notified, created_by)
VALUES (@tenant_id, @property_id, @event_id,
        (SELECT COALESCE(max(r.revision_no), 0) + 1 FROM banquet_beo_revisions r WHERE r.property_id = @property_id AND r.event_id = @event_id),
        sqlc.narg(note), @changes::jsonb, @snapshot::jsonb, @notified, sqlc.narg(actor_id))
RETURNING id, revision_no, created_at;

-- name: ListRevisions :many
SELECT r.revision_no, r.note, r.changes, r.notified, r.created_by, COALESCE(u.full_name, '')::text AS created_by_name, r.created_at
FROM banquet_beo_revisions r LEFT JOIN users u ON u.id = r.created_by
WHERE r.property_id = @property_id AND r.event_id = @event_id
ORDER BY r.revision_no DESC;

-- name: GetRevision :one
SELECT revision_no, snapshot, created_at FROM banquet_beo_revisions
WHERE property_id = @property_id AND event_id = @event_id AND revision_no = @revision_no;

-- ---------------------------------------------------------------- who read which section
-- name: UpsertAck :exec
INSERT INTO banquet_beo_ack (tenant_id, property_id, event_id, session_id, section, user_id, revision_no)
VALUES (@tenant_id, @property_id, @event_id, @session_id, @section, @user_id, @revision_no)
ON CONFLICT (session_id, section, user_id) DO UPDATE SET revision_no = EXCLUDED.revision_no, read_at = now();

-- name: ListAcks :many
SELECT a.session_id, a.section, a.user_id, u.full_name AS user_name, a.revision_no, a.read_at
FROM banquet_beo_ack a JOIN users u ON u.tenant_id = a.tenant_id AND u.id = a.user_id
WHERE a.property_id = @property_id AND a.event_id = @event_id
ORDER BY a.session_id, a.section, a.read_at DESC;

-- ---------------------------------------------------------------- the department of a section, and its people
-- name: SectionPeople :many
-- The head and the deputy of the department of each section; a department with no head of its own takes the ones of its parent, as the approvals of the inventory do. 0 is nobody.
WITH x AS (
    SELECT r.tenant_id, r.section, d.id AS department_id, d.code AS department_code, d.name AS department_name,
           COALESCE(CASE WHEN d.head_user_id IS NOT NULL THEN d.head_user_id ELSE pd.head_user_id END, 0)::bigint AS head_user_id,
           COALESCE(CASE WHEN d.head_user_id IS NOT NULL THEN d.deputy_user_id ELSE pd.deputy_user_id END, 0)::bigint AS deputy_user_id
    FROM banquet_beo_recipients r
    JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
    LEFT JOIN departments pd ON pd.property_id = d.property_id AND pd.id = d.parent_id
    WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
)
SELECT x.section, x.department_id, x.department_code, x.department_name, x.head_user_id, hu.full_name AS head_name, x.deputy_user_id, du.full_name AS deputy_name
FROM x
LEFT JOIN users hu ON hu.tenant_id = x.tenant_id AND hu.id = x.head_user_id
LEFT JOIN users du ON du.tenant_id = x.tenant_id AND du.id = x.deputy_user_id
ORDER BY x.section;

-- name: UpsertRecipient :exec
INSERT INTO banquet_beo_recipients (tenant_id, property_id, section, department_id, updated_by)
VALUES (@tenant_id, @property_id, @section, @department_id, sqlc.narg(actor_id))
ON CONFLICT (property_id, section) DO UPDATE SET department_id = EXCLUDED.department_id, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteRecipient :exec
DELETE FROM banquet_beo_recipients WHERE property_id = @property_id AND section = @section;

-- name: UpsertRevisionNotice :exec
-- A revision tells a person once while they have not seen the last notice of the event: the unseen row is brought up to date instead of a second one being written (36 4.6).
INSERT INTO banquet_notices (tenant_id, property_id, user_id, kind, event_id, session_id, notice_date, revision_no)
VALUES (@tenant_id, @property_id, @user_id, 'BEO_REVISED', @event_id, sqlc.narg(session_id), @notice_date, @revision_no)
ON CONFLICT (property_id, event_id, user_id) WHERE kind = 'BEO_REVISED' AND seen_at IS NULL
DO UPDATE SET revision_no = EXCLUDED.revision_no, session_id = EXCLUDED.session_id, notice_date = EXCLUDED.notice_date, created_at = now();

-- ---------------------------------------------------------------- the documents
-- name: InsertDocument :one
INSERT INTO banquet_documents (tenant_id, property_id, event_id, kind, number, revision_no, snapshot, created_by)
VALUES (@tenant_id, @property_id, @event_id, @kind, sqlc.narg(number), sqlc.narg(revision_no), sqlc.narg(snapshot)::jsonb, sqlc.narg(actor_id))
RETURNING id, created_at;

-- name: ContractOfRevision :one
SELECT id, number, revision_no, created_at FROM banquet_documents
WHERE property_id = @property_id AND event_id = @event_id AND kind = 'CONTRACT' AND revision_no = @revision_no;

-- name: GetDocument :one
SELECT id, event_id, kind, number, revision_no, snapshot, created_at FROM banquet_documents
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListDocuments :many
SELECT d.id, d.kind, d.number, d.revision_no, d.created_at, COALESCE(u.full_name, '')::text AS created_by_name
FROM banquet_documents d LEFT JOIN users u ON u.id = d.created_by
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.event_id = @event_id
ORDER BY d.id DESC;

-- ---------------------------------------------------------------- completing an event
-- name: UnpostedActiveSessions :one
-- The sessions that still stop the event from being completed: active and not posted.
SELECT count(*)::int FROM banquet_sessions s
WHERE s.property_id = @property_id AND s.event_id = @event_id AND s.status = 'ACTIVE'
  AND NOT EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = s.property_id AND p.session_id = s.id);
