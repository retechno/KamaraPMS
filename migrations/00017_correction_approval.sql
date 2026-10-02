-- +goose Up
-- Every correction is approved (docs/architecture/06-api.md §14.1): the approver's user id is recorded on
-- the row that corrects the ledger. The password is verified by the application and never stored.
ALTER TABLE folio_items ADD COLUMN approved_by bigint REFERENCES users (id);
ALTER TABLE payments    ADD COLUMN approved_by bigint REFERENCES users (id);

-- ADJUSTMENT and REVERSAL items carry an approver; nothing else does.
ALTER TABLE folio_items ADD CONSTRAINT folio_items_approval_ck
    CHECK ((transaction_type IN ('ADJUSTMENT', 'REVERSAL')) = (approved_by IS NOT NULL));
-- REFUND payments and VOIDED payments carry an approver; nothing else does.
ALTER TABLE payments ADD CONSTRAINT payments_approval_ck
    CHECK ((payment_type = 'REFUND' OR status = 'VOIDED') = (approved_by IS NOT NULL));

-- A void records who approved it: approved_by joins the columns that may change with POSTED -> VOIDED.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RAISE EXCEPTION 'payment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason']) THEN
        RAISE EXCEPTION 'payment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE payments DROP CONSTRAINT payments_approval_ck;
ALTER TABLE folio_items DROP CONSTRAINT folio_items_approval_ck;
ALTER TABLE payments DROP COLUMN approved_by;
ALTER TABLE folio_items DROP COLUMN approved_by;
