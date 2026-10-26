-- E-mail outbox queries (sqlc). Every query is scoped by tenant_id and property_id except the worker's claim, which
-- works across properties and re-enters each one as its tenant.

-- The e-mail address of the booker of a reservation ('' when there is none).
-- name: BookerEmail :one
SELECT COALESCE(g.email, '')::text AS email
FROM reservations r LEFT JOIN guests g ON g.tenant_id = r.tenant_id AND g.id = r.guest_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: InsertOutbox :one
INSERT INTO email_outbox (tenant_id, property_id, kind, reservation_id, to_address, next_attempt_at, created_at, created_by)
VALUES (@tenant_id, @property_id, @kind, @reservation_id, @to_address, @now::timestamptz, @now::timestamptz, sqlc.narg(actor_id))
RETURNING *;

-- A worker takes due messages and pushes their next attempt out (a lease), so a second worker, or a crash, never
-- sends a message twice at once.
-- name: ClaimDue :many
UPDATE email_outbox SET next_attempt_at = @lease_until::timestamptz
WHERE id IN (
    SELECT id FROM email_outbox WHERE status = 'QUEUED' AND next_attempt_at <= @now::timestamptz
    ORDER BY next_attempt_at, id LIMIT @row_limit FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: MarkSent :exec
UPDATE email_outbox SET status = 'SENT', sent_at = @now::timestamptz, attempts = attempts + 1, last_error = NULL WHERE id = @id;

-- name: MarkRetry :exec
UPDATE email_outbox SET attempts = attempts + 1, next_attempt_at = @next_attempt_at::timestamptz, last_error = @last_error WHERE id = @id;

-- name: MarkFailed :exec
UPDATE email_outbox SET status = 'FAILED', attempts = attempts + 1, last_error = @last_error WHERE id = @id;

-- name: MarkSkipped :exec
UPDATE email_outbox SET status = 'SKIPPED', last_error = @last_error WHERE id = @id;

-- name: ListOutboxForReservation :many
SELECT * FROM email_outbox
WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_id = @reservation_id
ORDER BY id DESC
LIMIT 20;

-- The e-mails of the banquet (36 F8, B12): a proposal or a contract of an event, with the document it carries and the language it is printed in.
-- name: InsertEventEmail :one
INSERT INTO email_outbox (tenant_id, property_id, kind, banquet_event_id, document_id, language, to_address, next_attempt_at, created_at, created_by)
VALUES (@tenant_id, @property_id, @kind, @event_id, @document_id, @language, @to_address, @now::timestamptz, @now::timestamptz, sqlc.narg(actor_id))
RETURNING *;

-- name: ListEventEmails :many
SELECT o.id, o.kind, o.document_id, d.number AS document_number, d.revision_no, o.language, o.to_address, o.status, o.attempts, o.last_error, o.sent_at, o.created_at
FROM email_outbox o JOIN banquet_documents d ON d.property_id = o.property_id AND d.id = o.document_id
WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.banquet_event_id = @event_id
ORDER BY o.id DESC
LIMIT 50;

-- name: EventEmailQueued :one
SELECT EXISTS (SELECT 1 FROM email_outbox WHERE property_id = @property_id AND banquet_event_id = @event_id AND kind = @kind AND to_address = @to_address AND status = 'QUEUED');
