-- +goose Up
-- The input side of the monthly VAT return and the VAT credit carried forward (design: docs/architecture/09-pkp-input-vat.md, step 3).
-- A return of the VAT tax claims the input VAT of the bills of the month against the VAT collected:
--     available = credit_brought_forward + input_claimed;  offset = least(tax_amount, available);
--     payable = tax_amount - offset;  credit_carried = available - offset.
-- The credit carried is the starting credit of the next month: the returns of a tax form a chain, and the values are frozen on each
-- return when it is filed.

-- A VAT tax is claimed on by one filing profile of the property.
ALTER TABLE tax_filing_profiles ADD COLUMN claims_input_vat boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX tax_filing_profiles_claims_uk ON tax_filing_profiles (property_id) WHERE claims_input_vat;

-- The credit a hotel starts with (one live per tax, only while the tax has no live return); its journal is Dr INPUT_VAT, Cr opening balance equity.
CREATE TABLE tax_opening_credits (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    tax_id           bigint        NOT NULL,
    as_of            date          NOT NULL,
    amount           numeric(18,3) NOT NULL,
    status           varchar(10)   NOT NULL DEFAULT 'POSTED',
    journal_id       bigint        NOT NULL,
    void_journal_id  bigint,
    voided_at        timestamptz,
    voided_by        bigint REFERENCES users (id),
    void_reason      varchar(500),
    approved_by      bigint REFERENCES users (id),
    created_at       timestamptz   NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    CONSTRAINT tax_opening_credits_property_fk      FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_opening_credits_tax_fk           FOREIGN KEY (property_id, tax_id)          REFERENCES taxes (property_id, id),
    CONSTRAINT tax_opening_credits_journal_fk       FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT tax_opening_credits_void_journal_fk  FOREIGN KEY (property_id, void_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT tax_opening_credits_amount_ck        CHECK (amount > 0),
    CONSTRAINT tax_opening_credits_status_ck        CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT tax_opening_credits_voided_ck        CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT tax_opening_credits_void_reason_ck   CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX tax_opening_credits_live_uk ON tax_opening_credits (property_id, tax_id) WHERE status = 'POSTED';

-- +goose StatementBegin
CREATE FUNCTION tax_opening_credits_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'opening credits cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_opening_credits_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'opening credit % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_opening_credits_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_opening_credits_guard BEFORE UPDATE OR DELETE ON tax_opening_credits FOR EACH ROW EXECUTE FUNCTION tax_opening_credits_guard();
CREATE TRIGGER tax_opening_credits_no_truncate BEFORE TRUNCATE ON tax_opening_credits FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The offset and the credit on a return. Only the input VAT claimed and the credit brought forward are written; the rest follows from
-- them, so a frozen return cannot disagree with itself. A return filed before this step has none of either: it owes what it collected.
ALTER TABLE tax_returns ADD COLUMN input_claimed           numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE tax_returns ADD COLUMN credit_brought_forward  numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE tax_returns ADD COLUMN offset_amount           numeric(18,3) GENERATED ALWAYS AS (LEAST(tax_amount, input_claimed + credit_brought_forward)) STORED;
ALTER TABLE tax_returns ADD COLUMN payable_amount          numeric(18,3) GENERATED ALWAYS AS (tax_amount - LEAST(tax_amount, input_claimed + credit_brought_forward)) STORED;
ALTER TABLE tax_returns ADD COLUMN credit_carried_forward  numeric(18,3) GENERATED ALWAYS AS (input_claimed + credit_brought_forward - LEAST(tax_amount, input_claimed + credit_brought_forward)) STORED;
ALTER TABLE tax_returns ADD COLUMN offset_journal_id       bigint;
ALTER TABLE tax_returns ADD COLUMN offset_void_journal_id  bigint;

ALTER TABLE tax_returns ADD CONSTRAINT tax_returns_offset_journal_fk      FOREIGN KEY (property_id, offset_journal_id)      REFERENCES gl_journals (property_id, id);
ALTER TABLE tax_returns ADD CONSTRAINT tax_returns_offset_void_journal_fk FOREIGN KEY (property_id, offset_void_journal_id) REFERENCES gl_journals (property_id, id);
ALTER TABLE tax_returns ADD CONSTRAINT tax_returns_credit_ck CHECK (credit_brought_forward >= 0);
ALTER TABLE tax_returns ADD CONSTRAINT tax_returns_offset_journal_ck CHECK ((offset_amount = 0) = (offset_journal_id IS NULL) AND (offset_void_journal_id IS NULL OR status = 'VOIDED'));

-- The guard lets a void also set the journal that reverses the offset (the generated columns are not computed yet in a BEFORE trigger).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tax_returns_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tax returns cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_no_delete';
    END IF;
    IF OLD.status <> 'FILED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by', 'offset_void_journal_id', 'offset_amount', 'payable_amount', 'credit_carried_forward'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by', 'offset_void_journal_id', 'offset_amount', 'payable_amount', 'credit_carried_forward']) THEN
        RAISE EXCEPTION 'tax return % may only change from FILED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- The chain: a return starts with the credit the month before carried (or the opening credit when it is the first), and returns are
-- filed in order, so a credit is consumed once.
-- +goose StatementBegin
CREATE FUNCTION tax_returns_credit_chain() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_expected numeric;
BEGIN
    IF NEW.status <> 'FILED' THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM tax_returns r WHERE r.property_id = NEW.property_id AND r.tax_id = NEW.tax_id AND r.status = 'FILED' AND r.period_start > NEW.period_start) THEN
        RAISE EXCEPTION 'a return of a later month is filed already'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_returns_credit_chain';
    END IF;
    SELECT r.credit_carried_forward INTO v_expected FROM tax_returns r
     WHERE r.property_id = NEW.property_id AND r.tax_id = NEW.tax_id AND r.status = 'FILED' AND r.period_start < NEW.period_start
     ORDER BY r.period_start DESC LIMIT 1;
    IF NOT FOUND THEN
        SELECT COALESCE(sum(c.amount), 0) INTO v_expected FROM tax_opening_credits c
         WHERE c.property_id = NEW.property_id AND c.tax_id = NEW.tax_id AND c.status = 'POSTED';
    END IF;
    IF NEW.credit_brought_forward <> v_expected THEN
        RAISE EXCEPTION 'the credit brought forward is % but the month before carried %', NEW.credit_brought_forward, v_expected
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_returns_credit_chain';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_returns_credit_chain BEFORE INSERT ON tax_returns FOR EACH ROW EXECUTE FUNCTION tax_returns_credit_chain();

-- supplier_bill_lines is referenced by (property, bill, line).
ALTER TABLE supplier_bill_lines ADD CONSTRAINT supplier_bill_lines_property_line_uk UNIQUE (property_id, bill_id, line_no);

-- The input side of a return, frozen: one row per bill line claimed, or a negative row that reverses the claim of a bill voided later.
CREATE TABLE tax_return_input_claims (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    return_id         bigint        NOT NULL,
    bill_id           bigint        NOT NULL,
    line_no           int           NOT NULL,
    amount            numeric(18,3) NOT NULL,
    reverses_claim_id bigint,
    released_at       timestamptz,
    CONSTRAINT tax_return_input_claims_property_fk  FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_return_input_claims_return_fk    FOREIGN KEY (property_id, return_id)           REFERENCES tax_returns (property_id, id),
    CONSTRAINT tax_return_input_claims_line_fk      FOREIGN KEY (property_id, bill_id, line_no)    REFERENCES supplier_bill_lines (property_id, bill_id, line_no),
    CONSTRAINT tax_return_input_claims_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT tax_return_input_claims_reverses_fk  FOREIGN KEY (property_id, reverses_claim_id)   REFERENCES tax_return_input_claims (property_id, id),
    CONSTRAINT tax_return_input_claims_sign_ck      CHECK ((reverses_claim_id IS NULL AND amount > 0) OR (reverses_claim_id IS NOT NULL AND amount < 0))
);
-- A bill line is claimed once among the live claims, and a claim is reversed once.
CREATE UNIQUE INDEX tax_return_input_claims_live_uk    ON tax_return_input_claims (property_id, bill_id, line_no) WHERE released_at IS NULL AND reverses_claim_id IS NULL;
CREATE UNIQUE INDEX tax_return_input_claims_reverse_uk ON tax_return_input_claims (property_id, reverses_claim_id) WHERE released_at IS NULL AND reverses_claim_id IS NOT NULL;
CREATE INDEX tax_return_input_claims_return_idx ON tax_return_input_claims (property_id, return_id);

-- A claim is never changed or deleted; only its release (the return was voided) is written, once.
-- +goose StatementBegin
CREATE FUNCTION tax_return_input_claims_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'input VAT claims cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_return_input_claims_no_delete';
    END IF;
    IF OLD.released_at IS NOT NULL OR NEW.released_at IS NULL
       OR (to_jsonb(NEW) - 'released_at') <> (to_jsonb(OLD) - 'released_at') THEN
        RAISE EXCEPTION 'input VAT claim % may only be released', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_return_input_claims_release_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_return_input_claims_guard BEFORE UPDATE OR DELETE ON tax_return_input_claims FOR EACH ROW EXECUTE FUNCTION tax_return_input_claims_guard();
CREATE TRIGGER tax_return_input_claims_no_truncate BEFORE TRUNCATE ON tax_return_input_claims FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A reversal reverses a claim of the same bill line, for the same amount, with the sign changed.
-- +goose StatementBegin
CREATE FUNCTION tax_return_input_claims_reversal_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    o record;
BEGIN
    IF NEW.reverses_claim_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT bill_id, line_no, amount, reverses_claim_id INTO o FROM tax_return_input_claims WHERE property_id = NEW.property_id AND id = NEW.reverses_claim_id;
    IF o.reverses_claim_id IS NOT NULL OR o.bill_id <> NEW.bill_id OR o.line_no <> NEW.line_no OR o.amount <> -NEW.amount THEN
        RAISE EXCEPTION 'a reversal reverses a claim of the same bill line for the same amount'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_return_input_claims_reversal';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_return_input_claims_reversal BEFORE INSERT ON tax_return_input_claims FOR EACH ROW EXECUTE FUNCTION tax_return_input_claims_reversal_check();

-- Deferred check at COMMIT: the claims of a return add up to its input VAT.
-- +goose StatementBegin
CREATE FUNCTION tax_return_claims_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_id    bigint;
    v_input numeric;
    v_sum   numeric;
BEGIN
    IF TG_TABLE_NAME = 'tax_returns' THEN
        v_id := NEW.id;
    ELSE
        v_id := NEW.return_id;
    END IF;
    SELECT input_claimed INTO v_input FROM tax_returns WHERE id = v_id;
    SELECT COALESCE(sum(amount), 0) INTO v_sum FROM tax_return_input_claims WHERE return_id = v_id;
    IF v_input <> v_sum THEN
        RAISE EXCEPTION 'tax return % has claims of % for an input VAT of %', v_id, v_sum, v_input
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_returns_claims_match_input';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER tax_returns_claims_check AFTER INSERT ON tax_returns
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_return_claims_total_check();
CREATE CONSTRAINT TRIGGER tax_return_claims_check AFTER INSERT ON tax_return_input_claims
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_return_claims_total_check();

-- +goose Down
DROP TABLE IF EXISTS tax_return_input_claims;
DROP TRIGGER IF EXISTS tax_returns_claims_check ON tax_returns;
DROP FUNCTION IF EXISTS tax_return_claims_total_check();
DROP FUNCTION IF EXISTS tax_return_input_claims_reversal_check();
DROP FUNCTION IF EXISTS tax_return_input_claims_guard();
ALTER TABLE supplier_bill_lines DROP CONSTRAINT supplier_bill_lines_property_line_uk;
DROP TRIGGER IF EXISTS tax_returns_credit_chain ON tax_returns;
DROP FUNCTION IF EXISTS tax_returns_credit_chain();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tax_returns_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tax returns cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_no_delete';
    END IF;
    IF OLD.status <> 'FILED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RAISE EXCEPTION 'tax return % may only change from FILED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE tax_returns DROP CONSTRAINT tax_returns_offset_journal_ck;
ALTER TABLE tax_returns DROP CONSTRAINT tax_returns_credit_ck;
ALTER TABLE tax_returns DROP CONSTRAINT tax_returns_offset_void_journal_fk;
ALTER TABLE tax_returns DROP CONSTRAINT tax_returns_offset_journal_fk;
ALTER TABLE tax_returns DROP COLUMN offset_void_journal_id;
ALTER TABLE tax_returns DROP COLUMN offset_journal_id;
ALTER TABLE tax_returns DROP COLUMN credit_carried_forward;
ALTER TABLE tax_returns DROP COLUMN payable_amount;
ALTER TABLE tax_returns DROP COLUMN offset_amount;
ALTER TABLE tax_returns DROP COLUMN credit_brought_forward;
ALTER TABLE tax_returns DROP COLUMN input_claimed;
DROP TABLE IF EXISTS tax_opening_credits;
DROP FUNCTION IF EXISTS tax_opening_credits_guard();
DROP INDEX IF EXISTS tax_filing_profiles_claims_uk;
ALTER TABLE tax_filing_profiles DROP COLUMN claims_input_vat;
