-- +goose Up
-- The currency lock of a property (design: docs/architecture/18-architecture-decisions.md, decision 3). Until now the lock looked at the folio items only, so a property
-- that had posted journals, supplier bills or bank statements but no guest charge could still change its currency or its decimals. One definition of "the property has
-- financial data" now serves the trigger below and the service (tenancy.UpdateProperty): a row in any table that holds an amount of money recorded in the currency.
-- Configuration that holds amounts (rates, yield floors, credit limits, fees) is not on the list: it does not lock the currency and it is never converted.

-- +goose StatementBegin
CREATE FUNCTION property_has_financial_data(p_property_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM folio_items            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM payments               WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM gl_journals            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_bills         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_payments      WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_credit_notes  WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_receipts   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_invoices   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM cashier_shifts         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM bank_statements        WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_returns            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_payments           WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_opening_credits    WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM budgets                WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

-- The trigger keeps its name and its place. It now takes the property row FOR UPDATE before it looks: every financial table has a foreign key to the property, so a row being
-- written holds a key-share lock on it until its transaction ends, and the update waits for that write instead of racing it. Whoever commits first wins, and the other sees it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION properties_currency_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.currency_code IS DISTINCT FROM OLD.currency_code OR NEW.currency_decimals IS DISTINCT FROM OLD.currency_decimals THEN
        PERFORM 1 FROM properties WHERE id = OLD.id FOR UPDATE;
        IF property_has_financial_data(OLD.id) THEN
            RAISE EXCEPTION 'currency of property % is locked: financial data exists', OLD.id
                USING ERRCODE = 'restrict_violation', CONSTRAINT = 'properties_currency_lock';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION properties_currency_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.currency_code IS DISTINCT FROM OLD.currency_code
        OR NEW.currency_decimals IS DISTINCT FROM OLD.currency_decimals)
       AND EXISTS (SELECT 1 FROM folio_items WHERE property_id = OLD.id) THEN
        RAISE EXCEPTION 'currency of property % is locked: financial transactions exist', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'properties_currency_lock';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP FUNCTION property_has_financial_data(bigint);
