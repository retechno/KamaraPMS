-- Room charge posting register (sqlc). Only the room charge posting service writes stay_charge_postings.

-- name: InsertPosting :one
INSERT INTO stay_charge_postings (
    tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id, folio_item_id, business_date, posting_trigger, created_by
) VALUES (
    @tenant_id, @property_id, @stay_id, @stay_room_id, @service_date, 'ROOM_NIGHT', @charge_code_id, @folio_item_id, @business_date, @posting_trigger, sqlc.narg(actor_id)
)
RETURNING id;
