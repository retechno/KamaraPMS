-- Read-only lookups of the identifier an audit entry is about, for a use case that has only the id of the thing (see package auditlabel). Plain SELECTs: no FOR UPDATE, no lock.

-- name: RoomNumber :one
SELECT room_number FROM rooms WHERE property_id = @property_id AND id = @id;

-- name: ReservationNumber :one
SELECT confirmation_number FROM reservations WHERE property_id = @property_id AND id = @id;

-- name: ReservationNumberOfLine :one
SELECT r.confirmation_number FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id
WHERE l.property_id = @property_id AND l.id = @id;

-- name: StayNumber :one
SELECT stay_number FROM stays WHERE property_id = @property_id AND id = @id;

-- name: FolioNumber :one
SELECT folio_number FROM folios WHERE property_id = @property_id AND id = @id;

-- A folio line has no number of its own: it is named by the folio it is on.
-- name: FolioNumberOfItem :one
SELECT f.folio_number FROM folio_items i JOIN folios f ON f.id = i.folio_id
WHERE i.property_id = @property_id AND i.id = @id;

-- name: TaskRoomNumber :one
SELECT r.room_number FROM housekeeping_tasks t JOIN rooms r ON r.id = t.room_id
WHERE t.property_id = @property_id AND t.id = @id;

-- name: PropertyCode :one
SELECT code FROM properties WHERE id = @id;

-- name: RatePlanCode :one
SELECT code FROM rate_plans WHERE property_id = @property_id AND id = @id;

-- name: BankStatementPeriod :one
SELECT a.name AS account_name, s.period_from, s.period_to
FROM bank_statements s JOIN bank_accounts a ON a.id = s.bank_account_id
WHERE s.property_id = @property_id AND s.id = @id;
