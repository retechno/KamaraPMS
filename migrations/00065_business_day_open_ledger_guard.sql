-- +goose Up
-- Financial rows are written only on the OPEN business day (audit F-07). The services already ask for it: every business-dated write starts with RequireOpenBusinessDay, which
-- share-locks the open day. What the database had was a foreign key to business_days, which accepts a CLOSED day too, so a use case that forgot the call could write a row dated a
-- day whose journal had been made. This is the safety net behind that call, not a replacement for it.
--
-- One reusable function, one trigger for each table whose date column IS the business date a posting is made on (the service sets it from the open day, never from the request):
--   folio_items.business_date, payments.business_date, stay_charge_postings.business_date, city_ledger_receipts.business_date, city_ledger_adjustments.business_date,
--   city_ledger_invoices.invoice_date, cashier_shift_movements.business_date, cashier_shifts.business_date_opened (insert) and cashier_shifts.business_date_closed (set on close).
-- NOT guarded, on purpose: gl_journals.journal_date (a manual journal may be dated earlier in an open period, and the backfill makes journals for CLOSED days), gl_journal_lines (they
-- follow the journal), gl_day_posts (the backfill writes them for closed days), supplier bills, payments and credit notes, tax payments and returns, bank statements and card
-- settlements (their dates are document dates given by the user and checked against the accounting period, not the business day), and the operational records that only
-- carry a business date for reference (housekeeping, maintenance, lost and found, reminders, audit_logs, folios.closed_on). Rows that exist are not examined.
--
-- Race with the night audit: the check reads the day FOR SHARE. The night audit holds it FOR UPDATE until its commit, so a write that arrives while the day is being closed waits for the
-- commit and then sees CLOSED and is refused; a write that got the share lock first makes the night audit wait for its commit, and lands before the close. A service call has taken the
-- same share lock already, so this takes nothing new for it and the lock order (the business day first) is not changed. A date that is not a business day at all is left to the foreign
-- key (BUSINESS_DAY_NOT_FOUND).
-- +goose StatementBegin
CREATE FUNCTION business_day_must_be_open() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_date   date;
    v_status text;
BEGIN
    v_date := (to_jsonb(NEW) ->> TG_ARGV[0])::date; -- the name of the date column is the argument of the trigger
    IF v_date IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT status INTO v_status FROM business_days WHERE property_id = NEW.property_id AND business_date = v_date FOR SHARE;
    IF v_status IS NOT NULL AND v_status <> 'OPEN' THEN
        RAISE EXCEPTION '% % of property % is on business day %, which is not open', TG_TABLE_NAME, TG_ARGV[0], NEW.property_id, v_date
            USING ERRCODE = 'check_violation', CONSTRAINT = 'business_day_must_be_open';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER folio_items_business_day_open BEFORE INSERT ON folio_items FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER payments_business_day_open BEFORE INSERT ON payments FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER stay_charge_postings_business_day_open BEFORE INSERT ON stay_charge_postings FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER city_ledger_receipts_business_day_open BEFORE INSERT ON city_ledger_receipts FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER city_ledger_adjustments_business_day_open BEFORE INSERT ON city_ledger_adjustments FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER city_ledger_invoices_business_day_open BEFORE INSERT ON city_ledger_invoices FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('invoice_date');
CREATE TRIGGER cashier_shift_movements_business_day_open BEFORE INSERT ON cashier_shift_movements FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');
CREATE TRIGGER cashier_shifts_opened_business_day_open BEFORE INSERT ON cashier_shifts FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date_opened');
CREATE TRIGGER cashier_shifts_closed_business_day_open BEFORE UPDATE OF business_date_closed ON cashier_shifts
    FOR EACH ROW WHEN (NEW.business_date_closed IS DISTINCT FROM OLD.business_date_closed) EXECUTE FUNCTION business_day_must_be_open('business_date_closed');

-- +goose Down
DROP TRIGGER cashier_shifts_closed_business_day_open ON cashier_shifts;
DROP TRIGGER cashier_shifts_opened_business_day_open ON cashier_shifts;
DROP TRIGGER cashier_shift_movements_business_day_open ON cashier_shift_movements;
DROP TRIGGER city_ledger_invoices_business_day_open ON city_ledger_invoices;
DROP TRIGGER city_ledger_adjustments_business_day_open ON city_ledger_adjustments;
DROP TRIGGER city_ledger_receipts_business_day_open ON city_ledger_receipts;
DROP TRIGGER stay_charge_postings_business_day_open ON stay_charge_postings;
DROP TRIGGER payments_business_day_open ON payments;
DROP TRIGGER folio_items_business_day_open ON folio_items;
DROP FUNCTION business_day_must_be_open();
