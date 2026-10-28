-- Schema integrity tests: prove that the database itself enforces the PMS invariants,
-- independent of any application code. Run by scripts/db-test.sh on a fresh database.
\set ON_ERROR_STOP on
SET client_min_messages = notice;

CREATE SCHEMA pms_test;
SET search_path = pms_test, public;

CREATE TABLE pms_test.results (label text NOT NULL, outcome text NOT NULL);

-- Runs statements in a subtransaction (forcing deferred constraints), always rolls back,
-- and asserts the expected SQLSTATE.
CREATE FUNCTION pms_test.expect_error(p_label text, p_state text, VARIADIC p_stmts text[]) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    s       text;
    v_state text;
    v_msg   text;
BEGIN
    BEGIN
        FOREACH s IN ARRAY p_stmts LOOP
            EXECUTE s;
        END LOOP;
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION USING ERRCODE = 'PT001', MESSAGE = 'no error';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    END;
    IF v_state = 'PT001' THEN
        RAISE EXCEPTION 'FAIL %: expected SQLSTATE %, but the statements succeeded', p_label, p_state;
    ELSIF v_state <> p_state THEN
        RAISE EXCEPTION 'FAIL %: expected SQLSTATE %, got % (%)', p_label, p_state, v_state, v_msg;
    END IF;
    INSERT INTO pms_test.results VALUES (p_label, 'rejected ' || v_state);
    RAISE NOTICE 'PASS  %  [% %]', p_label, v_state, v_msg;
END
$$;

-- Asserts the statements succeed (including deferred constraints), then rolls them back.
CREATE FUNCTION pms_test.expect_ok(p_label text, VARIADIC p_stmts text[]) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    s       text;
    v_state text;
    v_msg   text;
BEGIN
    BEGIN
        FOREACH s IN ARRAY p_stmts LOOP
            EXECUTE s;
        END LOOP;
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION USING ERRCODE = 'PT001', MESSAGE = 'ok';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    END;
    IF v_state <> 'PT001' THEN
        RAISE EXCEPTION 'FAIL %: expected success, got % (%)', p_label, v_state, v_msg;
    END IF;
    INSERT INTO pms_test.results VALUES (p_label, 'accepted');
    RAISE NOTICE 'PASS  %  [accepted]', p_label;
END
$$;

-- Natural-key lookups keep the test statements readable.
CREATE FUNCTION tn(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM tenants WHERE code = $1 $$;
CREATE FUNCTION pr(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM properties WHERE code = $1 $$;
CREATE FUNCTION us(text, text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM users WHERE tenant_id = tn($1) AND email = $2 $$;
CREATE FUNCTION ro(text, text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM roles WHERE tenant_id = tn($1) AND name = $2 $$;
CREATE FUNCTION rt(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM room_types WHERE property_id = pr('BALI') AND code = $1 $$;
CREATE FUNCTION rm(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM rooms WHERE property_id = pr('BALI') AND room_number = $1 $$;
CREATE FUNCTION gu(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM guests WHERE code = $1 $$;
CREATE FUNCTION cc(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM charge_codes WHERE property_id = pr('BALI') AND code = $1 $$;
CREATE FUNCTION tx(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM taxes WHERE code = $1 $$;
CREATE FUNCTION sc(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM service_charges WHERE property_id = pr('BALI') AND code = $1 $$;
CREATE FUNCTION rp(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM rate_plans WHERE property_id = pr('BALI') AND code = $1 $$;
CREATE FUNCTION rs(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM reservations WHERE confirmation_number = $1 $$;
CREATE FUNCTION ln(text, text, date) RETURNS bigint LANGUAGE sql STABLE AS $$
    SELECT id FROM reservation_rooms WHERE reservation_id = rs($1) AND room_id = rm($2) AND arrival_date = $3 $$;
CREATE FUNCTION st(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM stays WHERE stay_number = $1 $$;
CREATE FUNCTION sr(text, text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM stay_rooms WHERE stay_id = st($1) AND room_id = rm($2) $$;
CREATE FUNCTION fo(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM folios WHERE folio_number = $1 $$;
CREATE FUNCTION fi(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM folio_items WHERE idempotency_key = $1 $$;
CREATE FUNCTION pm(text) RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM payments WHERE payment_number = $1 $$;

------------------------------------------------------------------------------------------
-- Tenants, users, roles
------------------------------------------------------------------------------------------
INSERT INTO tenants (code, name, timezone) VALUES
    ('ABC', 'ABC Hotels', 'Asia/Jakarta'),
    ('XYZ', 'XYZ Group', 'Asia/Singapore');

SELECT expect_error('tenant code is upper-case', '23514',
    $q$INSERT INTO tenants (code, name, timezone) VALUES ('abc2', 'x', 'UTC')$q$);

-- The same email in two tenants is allowed.
INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES
    (tn('ABC'), 'admin@hotel.com', 'x', 'ABC Admin'),
    (tn('XYZ'), 'admin@hotel.com', 'x', 'XYZ Admin');

SELECT expect_error('email unique per tenant (case-insensitive)', '23505',
    $q$INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES (tn('ABC'), 'ADMIN@hotel.com', 'x', 'dup')$q$);

INSERT INTO roles (tenant_id, name) VALUES (tn('ABC'), 'Front Desk'), (tn('XYZ'), 'Front Desk');

------------------------------------------------------------------------------------------
-- Properties, grants, business days, sequences
------------------------------------------------------------------------------------------
INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time) VALUES
    (tn('ABC'), 'BALI', 'Hotel Bali', 'Asia/Makassar', 'IDR', 0, '14:00', '12:00'),
    (tn('XYZ'), 'SG',   'Hotel SG',   'Asia/Singapore', 'SGD', 2, '15:00', '11:00');

SELECT expect_error('currency decimals 0..3', '23514',
    $q$INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
       VALUES (tn('ABC'), 'BAD', 'x', 'UTC', 'IDR', 5, '14:00', '12:00')$q$);

INSERT INTO user_properties (tenant_id, user_id, property_id, role_id)
VALUES (tn('ABC'), us('ABC', 'admin@hotel.com'), pr('BALI'), ro('ABC', 'Front Desk'));

SELECT expect_error('grant: user of another tenant cannot access property', '23503',
    $q$INSERT INTO user_properties (tenant_id, user_id, property_id, role_id)
       VALUES (tn('ABC'), us('XYZ', 'admin@hotel.com'), pr('BALI'), ro('ABC', 'Front Desk'))$q$);
SELECT expect_error('grant: role of another tenant cannot be used', '23503',
    $q$INSERT INTO user_properties (tenant_id, user_id, property_id, role_id)
       VALUES (tn('XYZ'), us('XYZ', 'admin@hotel.com'), pr('SG'), ro('ABC', 'Front Desk'))$q$);

INSERT INTO business_days (tenant_id, property_id, business_date) VALUES
    (tn('ABC'), pr('BALI'), '2026-09-30'),
    (tn('XYZ'), pr('SG'),   '2026-09-30');

SELECT expect_error('exactly one OPEN business day per property', '23505',
    $q$INSERT INTO business_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$);
SELECT expect_error('business days are consecutive', '23514',
    $q$INSERT INTO business_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-05')$q$);
SELECT expect_error('a new business day must be OPEN', '23514',
    $q$INSERT INTO business_days (tenant_id, property_id, business_date, status, closed_at)
       VALUES (tn('ABC'), pr('BALI'), '2026-10-01', 'CLOSED', now())$q$);

-- Night audit: close the OPEN day and open the next one in one transaction.
BEGIN;
UPDATE business_days SET status = 'CLOSED', closed_at = now(), summary = '{"occupied": 0}'
 WHERE property_id = pr('BALI') AND status = 'OPEN';
INSERT INTO business_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-01');
COMMIT;

SELECT expect_error('a CLOSED business day is immutable', '23001',
    $q$UPDATE business_days SET summary = '{}' WHERE property_id = pr('BALI') AND business_date = '2026-09-30'$q$);
SELECT expect_error('business days cannot be deleted', '23001',
    $q$DELETE FROM business_days WHERE property_id = pr('BALI') AND business_date = '2026-10-01'$q$);

-- Gapless numbering: a rolled-back allocation leaves no gap.
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) VALUES (tn('ABC'), pr('BALI'), 'RESERVATION', 'R');
BEGIN;
UPDATE document_sequences SET next_value = next_value + 1 WHERE property_id = pr('BALI') AND sequence_type = 'RESERVATION';
ROLLBACK;
DO $$
DECLARE v bigint;
BEGIN
    UPDATE document_sequences SET next_value = next_value + 1
     WHERE property_id = pr('BALI') AND sequence_type = 'RESERVATION' RETURNING next_value - 1 INTO v;
    IF v <> 1 THEN RAISE EXCEPTION 'FAIL gapless sequence: expected 1, got %', v; END IF;
    INSERT INTO pms_test.results VALUES ('document sequence is gapless across rollback', 'accepted');
    RAISE NOTICE 'PASS  document sequence is gapless across rollback';
END $$;

------------------------------------------------------------------------------------------
-- Rooms, housekeeping, blocks
------------------------------------------------------------------------------------------
INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy)
VALUES (tn('ABC'), pr('BALI'), 'DLX', 'Deluxe', 2, 1, 3, 2);

SELECT expect_error('max_occupancy <= max_adult + max_child', '23514',
    $q$INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy)
       VALUES (tn('ABC'), pr('BALI'), 'STD', 'Standard', 2, 0, 3, 2)$q$);

-- every room has a bed type (00057), so the properties get a bed first
INSERT INTO bed_types (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'KING', 'King'), (tn('XYZ'), pr('SG'), 'KING', 'King');
INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, bed_type_id) VALUES
    (tn('ABC'), pr('BALI'), rt('DLX'), '201', (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING')),
    (tn('ABC'), pr('BALI'), rt('DLX'), '305', (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING')),
    (tn('ABC'), pr('BALI'), rt('DLX'), '410', (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'));
SELECT expect_error('a room has a bed type', '23502',
    $q$INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), '999')$q$);

SELECT expect_error('room cannot use a room type of another property', '23503',
    $q$INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, bed_type_id) VALUES (tn('XYZ'), pr('SG'), rt('DLX'), '101', (SELECT id FROM bed_types WHERE property_id = pr('SG') AND code = 'KING'))$q$);
SELECT expect_error('tenant/property pair must match', '23503',
    $q$INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, bed_type_id) VALUES (tn('XYZ'), pr('BALI'), rt('DLX'), '102', (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'))$q$);
SELECT expect_error('rooms has no status column', '42703',
    $q$UPDATE rooms SET status = 'OCCUPIED'$q$);

INSERT INTO room_housekeeping (tenant_id, property_id, room_id, status) VALUES
    (tn('ABC'), pr('BALI'), rm('201'), 'CLEAN'),
    (tn('ABC'), pr('BALI'), rm('305'), 'DIRTY'),
    (tn('ABC'), pr('BALI'), rm('410'), 'INSPECTED');

SELECT expect_error('housekeeping status is CLEAN/DIRTY/CLEANING/INSPECTED', '23514',
    $q$UPDATE room_housekeeping SET status = 'READY' WHERE room_id = rm('201')$q$);
SELECT expect_error('one housekeeping row per room', '23505',
    $q$INSERT INTO room_housekeeping (tenant_id, property_id, room_id, status) VALUES (tn('ABC'), pr('BALI'), rm('201'), 'DIRTY')$q$);

INSERT INTO housekeeping_logs (tenant_id, property_id, room_id, from_status, to_status, source, business_date)
VALUES (tn('ABC'), pr('BALI'), rm('305'), 'CLEAN', 'DIRTY', 'CHECK_OUT', '2026-10-01');
SELECT expect_error('housekeeping log is append-only', '23001',
    $q$UPDATE housekeeping_logs SET notes = 'x'$q$);

INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
VALUES (tn('ABC'), pr('BALI'), rm('201'), 'OOO', '2026-10-10', '2026-10-12', 'AC repair');

SELECT expect_error('overlapping active blocks on one room', '23P01',
    $q$INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
       VALUES (tn('ABC'), pr('BALI'), rm('201'), 'OOS', '2026-10-11', '2026-10-13', 'paint')$q$);
SELECT expect_ok('adjacent block (half-open ranges)',
    $q$INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
       VALUES (tn('ABC'), pr('BALI'), rm('201'), 'OOS', '2026-10-12', '2026-10-14', 'paint')$q$);
SELECT expect_ok('cancelled block may overlap',
    $q$INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason, status, cancelled_at)
       VALUES (tn('ABC'), pr('BALI'), rm('201'), 'OOO', '2026-10-10', '2026-10-12', 'dup', 'CANCELLED', now())$q$);
SELECT expect_error('block end after start', '23514',
    $q$INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
       VALUES (tn('ABC'), pr('BALI'), rm('305'), 'OOO', '2026-10-10', '2026-10-10', 'x')$q$);

------------------------------------------------------------------------------------------
-- Guests (tenant-wide)
------------------------------------------------------------------------------------------
INSERT INTO guests (tenant_id, code, origin_property_id, last_name) VALUES
    (tn('ABC'), 'G1', pr('BALI'), 'Tan'),
    (tn('ABC'), 'G2', pr('BALI'), 'Lee'),
    (tn('XYZ'), 'G9', pr('SG'),   'Lim');

SELECT expect_error('guest origin property must be in the same tenant', '23503',
    $q$INSERT INTO guests (tenant_id, code, origin_property_id, last_name) VALUES (tn('ABC'), 'G3', pr('SG'), 'X')$q$);

SELECT expect_ok('guest is linked to its origin property',
    $q$SELECT 1 / (CASE WHEN guest_linked_to(tn('ABC'), gu('G1'), pr('BALI'), ARRAY[pr('BALI')]) THEN 1 ELSE 0 END)$q$);
SELECT expect_ok('guest is not linked to an unrelated property',
    $q$SELECT 1 / (CASE WHEN NOT guest_linked_to(tn('ABC'), gu('G1'), pr('BALI'), ARRAY[pr('SG')]) THEN 1 ELSE 0 END)$q$);
SELECT expect_ok('an empty property list links nothing',
    $q$SELECT 1 / (CASE WHEN NOT guest_linked_to(tn('ABC'), gu('G1'), pr('BALI'), ARRAY[]::bigint[]) THEN 1 ELSE 0 END)$q$);

INSERT INTO tenant_sequences (tenant_id, sequence_type, prefix, next_value) VALUES (tn('ABC'), 'GUEST', 'GST', 2);
SELECT expect_error('tenant sequence type is a known series', '23514',
    $q$INSERT INTO tenant_sequences (tenant_id, sequence_type, prefix) VALUES (tn('XYZ'), 'BOGUS', 'X')$q$);
SELECT expect_error('tenant sequence next value starts at 1', '23514',
    $q$UPDATE tenant_sequences SET next_value = 0 WHERE tenant_id = tn('ABC')$q$);
SELECT expect_error('one series per tenant and type', '23505',
    $q$INSERT INTO tenant_sequences (tenant_id, sequence_type, prefix) VALUES (tn('ABC'), 'GUEST', 'GST')$q$);

------------------------------------------------------------------------------------------
-- Billing configuration
------------------------------------------------------------------------------------------
INSERT INTO taxes (tenant_id, property_id, code, name, rate, tax_on_service) VALUES
    (tn('ABC'), pr('BALI'), 'VAT',  'VAT 11%',  11.0, true),
    (tn('ABC'), pr('BALI'), 'CITY', 'City tax',  1.0, false),
    (tn('XYZ'), pr('SG'),   'GST',  'GST 9%',    9.0, false);
INSERT INTO service_charges (tenant_id, property_id, code, name, rate)
VALUES (tn('ABC'), pr('BALI'), 'SVC', 'Service 10%', 10.0);

SELECT expect_error('tax rate is a percent 0..100', '23514',
    $q$INSERT INTO taxes (tenant_id, property_id, code, name, rate) VALUES (tn('ABC'), pr('BALI'), 'BAD', 'x', 150)$q$);
SELECT expect_error('taxes have no is_inclusive column', '42703',
    $q$UPDATE taxes SET is_inclusive = true$q$);

INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode) VALUES
    (tn('ABC'), pr('BALI'), 'ROOM',        'Room',        'ROOM',    'EXCLUSIVE'),
    (tn('ABC'), pr('BALI'), 'ROOM_EXEMPT', 'Room exempt', 'ROOM',    'EXCLUSIVE'),
    (tn('ABC'), pr('BALI'), 'NETT',        'Room nett',   'ROOM',    'INCLUSIVE'),
    (tn('ABC'), pr('BALI'), 'LAUNDRY',     'Laundry',     'SERVICE', 'EXCLUSIVE');

SELECT expect_ok('seeding creates the eighteen standard charge codes once (idempotent)',
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('XYZ'), pr('SG'), NULL) = 18 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('XYZ'), pr('SG'), NULL) = 0 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM charge_codes WHERE property_id = pr('SG') AND is_system AND price_mode = 'EXCLUSIVE') = 18 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM charge_code_taxes WHERE property_id = pr('SG')) = 0 THEN 1 ELSE 0 END)$q$);
SELECT expect_ok('seeding only fills gaps and leaves existing codes alone',
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('ABC'), pr('BALI'), NULL) = 15 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT NOT is_system FROM charge_codes WHERE id = cc('ROOM')) THEN 1 ELSE 0 END)$q$);

INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence) VALUES
    (tn('ABC'), pr('BALI'), cc('ROOM'),    tx('VAT'), 1),
    (tn('ABC'), pr('BALI'), cc('NETT'),    tx('VAT'), 1),
    (tn('ABC'), pr('BALI'), cc('LAUNDRY'), tx('VAT'), 1);
INSERT INTO charge_code_service_charges (tenant_id, property_id, charge_code_id, service_charge_id, sequence) VALUES
    (tn('ABC'), pr('BALI'), cc('ROOM'), sc('SVC'), 1),
    (tn('ABC'), pr('BALI'), cc('NETT'), sc('SVC'), 1);

SELECT expect_error('active tax sequence unique per charge code', '23505',
    $q$INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence) VALUES (tn('ABC'), pr('BALI'), cc('ROOM'), tx('CITY'), 1)$q$);
SELECT expect_ok('second tax with its own sequence',
    $q$INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence) VALUES (tn('ABC'), pr('BALI'), cc('ROOM'), tx('CITY'), 2)$q$);
SELECT expect_error('cannot map a tax of another property', '23503',
    $q$INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence) VALUES (tn('ABC'), pr('BALI'), cc('ROOM'), tx('GST'), 3)$q$);
SELECT expect_error('price_mode is EXCLUSIVE/INCLUSIVE', '23514',
    $q$INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode) VALUES (tn('ABC'), pr('BALI'), 'X', 'x', 'OTHER', 'GROSS')$q$);

------------------------------------------------------------------------------------------
-- Rate plans and rates
------------------------------------------------------------------------------------------
INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES
    (tn('ABC'), pr('BALI'), 'BAR',  'Best available', cc('ROOM')),
    (tn('ABC'), pr('BALI'), 'NETT', 'Nett rate',      cc('NETT'));

SELECT expect_error('rate plan room charge code must be charge_type ROOM', '23514',
    $q$INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'BAD', 'x', cc('LAUNDRY'))$q$);

INSERT INTO rates (tenant_id, property_id, rate_plan_id, room_type_id, stay_date, amount)
VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), '2026-10-01', 1000000);
SELECT expect_ok('money columns keep three decimals (KWD, BHD)',
    $q$INSERT INTO rates (tenant_id, property_id, rate_plan_id, room_type_id, stay_date, amount) VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), '2027-03-01', 12.345)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT amount FROM rates WHERE rate_plan_id = rp('BAR') AND stay_date = '2027-03-01') = 12.345 THEN 1 ELSE 0 END)$q$,
    $q$UPDATE charge_codes SET default_unit_price = 7.125 WHERE id = cc('LAUNDRY')$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT default_unit_price FROM charge_codes WHERE id = cc('LAUNDRY')) = 7.125 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT numeric_scale FROM information_schema.columns WHERE table_name = 'folio_items' AND column_name = 'debit') = 3
                         AND (SELECT numeric_scale FROM information_schema.columns WHERE table_name = 'payments' AND column_name = 'amount') = 3
                         AND (SELECT numeric_scale FROM information_schema.columns WHERE table_name = 'folio_item_components' AND column_name = 'amount') = 3
                         AND (SELECT numeric_scale FROM information_schema.columns WHERE table_name = 'reservation_room_rates' AND column_name = 'amount') = 3 THEN 1 ELSE 0 END)$q$);
SELECT expect_error('rate amount >= 0', '23514',
    $q$INSERT INTO rates (tenant_id, property_id, rate_plan_id, room_type_id, stay_date, amount) VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), '2026-10-02', -1)$q$);

------------------------------------------------------------------------------------------
-- Reservations and double-booking prevention
------------------------------------------------------------------------------------------
INSERT INTO reservations (tenant_id, property_id, confirmation_number, guest_id, reservation_date, source, status, confirmed_at)
VALUES (tn('ABC'), pr('BALI'), 'R1', gu('G1'), '2026-10-01', 'PHONE', 'CONFIRMED', now());

SELECT expect_error('confirmed reservation needs a booker', '23514',
    $q$INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status, confirmed_at)
       VALUES (tn('ABC'), pr('BALI'), 'R2', '2026-10-01', 'PHONE', 'CONFIRMED', now())$q$);
SELECT expect_error('reservation status has no CHECKED_IN (lives on lines)', '23514',
    $q$UPDATE reservations SET status = 'CHECKED_IN' WHERE confirmation_number = 'R1'$q$);
SELECT expect_error('booker must belong to the same tenant', '23503',
    $q$INSERT INTO reservations (tenant_id, property_id, confirmation_number, guest_id, reservation_date, source)
       VALUES (tn('ABC'), pr('BALI'), 'R3', gu('G9'), '2026-10-01', 'PHONE')$q$);

INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status) VALUES
    (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rm('201'), rp('BAR'), '2026-10-01', '2026-10-04', 2, 'CONFIRMED'),
    (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rm('305'), rp('NETT'), '2026-10-01', '2026-10-03', 1, 'CONFIRMED');

-- Bed variants (00057): a locked line asks for a bed, the supplement of a variant per rate plan and room type
SELECT expect_error('a locked bed needs a bed type', '23514',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, rate_plan_id, arrival_date, departure_date, adult_count, status, bed_locked)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rp('BAR'), '2026-11-01', '2026-11-03', 2, 'DRAFT', true)$q$);
SELECT expect_ok('a locked bed with its bed type',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, rate_plan_id, arrival_date, departure_date, adult_count, status, bed_locked, requested_bed_type_id)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rp('BAR'), '2026-11-01', '2026-11-03', 2, 'DRAFT', true, (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'))$q$);
SELECT expect_ok('a supplement of an amount for a variant',
    $q$INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'AMOUNT', 50000, '2026-10-01')$q$);
SELECT expect_error('a supplement kind is AMOUNT or PERCENT', '23514',
    $q$INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'FREE', 1, '2026-10-02')$q$);
SELECT expect_error('a percentage supplement is at most 1000 percent and not below -100', '23514',
    $q$INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'PERCENT', -101, '2026-10-02')$q$);
SELECT expect_ok('a supplement may be a percentage below zero (a discount)',
    $q$INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'PERCENT', -10, '2026-10-02')$q$);
SELECT expect_error('a supplement of a rate plan, room type and bed starts once', '23505',
    $q$WITH a AS (INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'AMOUNT', 1, '2026-10-05') RETURNING 1)
       INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'AMOUNT', 2, '2026-10-05')$q$);
SELECT expect_error('a bed type of another property', '23503',
    $q$INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
       VALUES (tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('SG') AND code = 'KING'), 'AMOUNT', 1, '2026-10-06')$q$);
INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from)
SELECT tn('ABC'), pr('BALI'), rp('BAR'), rt('DLX'), (SELECT id FROM bed_types WHERE property_id = pr('BALI') AND code = 'KING'), 'AMOUNT', 50000, '2026-10-01';
SELECT expect_error('a supplement is never changed', '23001', $q$UPDATE rate_plan_bed_adjustments SET amount = 1$q$);
SELECT expect_error('a supplement is never deleted', '23001', $q$DELETE FROM rate_plan_bed_adjustments$q$);

SELECT expect_error('double booking: overlapping CONFIRMED lines on one room', '23P01',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rm('201'), rp('BAR'), '2026-10-03', '2026-10-05', 2, 'CONFIRMED')$q$);
SELECT expect_ok('same-day turnover: departure does not block the next arrival',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rm('201'), rp('BAR'), '2026-10-04', '2026-10-06', 2, 'CONFIRMED')$q$);
SELECT expect_ok('DRAFT lines hold no room',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rm('201'), rp('BAR'), '2026-10-02', '2026-10-03', 2, 'DRAFT')$q$);
SELECT expect_ok('a reservation can carry an idempotency key with its request hash',
    $q$UPDATE reservations SET idempotency_key = 'k-1', idempotency_hash = repeat('a', 64) WHERE confirmation_number = 'R1'$q$);
SELECT expect_error('the idempotency key and its hash come together', '23514',
    $q$UPDATE reservations SET idempotency_key = 'k-2', idempotency_hash = NULL WHERE confirmation_number = 'R1'$q$);
SELECT expect_error('departure after arrival (no day-use)', '23514',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, rate_plan_id, arrival_date, departure_date, adult_count)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), rt('DLX'), rp('BAR'), '2026-10-05', '2026-10-05', 2)$q$);
SELECT expect_error('line occupant must belong to the same tenant', '23503',
    $q$INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, guest_id, room_type_id, rate_plan_id, arrival_date, departure_date, adult_count)
       VALUES (tn('ABC'), pr('BALI'), rs('R1'), gu('G9'), rt('DLX'), rp('BAR'), '2026-10-05', '2026-10-06', 2)$q$);
SELECT expect_error('NO_SHOW requires no_show_at', '23514',
    $q$UPDATE reservation_rooms SET status = 'NO_SHOW' WHERE id = ln('R1', '305', '2026-10-01')$q$);

INSERT INTO reservation_room_rates (tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode, base_rate, amount)
VALUES (tn('ABC'), pr('BALI'), ln('R1', '201', '2026-10-01'), '2026-10-01', rp('BAR'), cc('ROOM'), 'EXCLUSIVE', 1000000, 1000000);

SELECT expect_error('nightly amount = base_rate - discount unless override', '23514',
    $q$INSERT INTO reservation_room_rates (tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode, base_rate, discount_amount, amount)
       VALUES (tn('ABC'), pr('BALI'), ln('R1', '201', '2026-10-01'), '2026-10-02', rp('BAR'), cc('ROOM'), 'EXCLUSIVE', 1000000, 100000, 950000)$q$);
SELECT expect_error('nightly charge code must be a ROOM code', '23514',
    $q$INSERT INTO reservation_room_rates (tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode, amount, is_override)
       VALUES (tn('ABC'), pr('BALI'), ln('R1', '201', '2026-10-01'), '2026-10-02', rp('BAR'), cc('LAUNDRY'), 'EXCLUSIVE', 1, true)$q$);

------------------------------------------------------------------------------------------
-- Stays and derived occupancy guarantees
------------------------------------------------------------------------------------------
INSERT INTO stays (tenant_id, property_id, stay_number, reservation_room_id, guest_id, arrival_date, departure_date, adult_count, actual_check_in_at) VALUES
    (tn('ABC'), pr('BALI'), 'S1', ln('R1', '201', '2026-10-01'), gu('G1'), '2026-10-01', '2026-10-04', 2, now()),
    (tn('ABC'), pr('BALI'), 'S2', ln('R1', '305', '2026-10-01'), gu('G2'), '2026-10-01', '2026-10-03', 1, now());

SELECT expect_error('one live stay per reservation room', '23505',
    $q$INSERT INTO stays (tenant_id, property_id, stay_number, reservation_room_id, guest_id, arrival_date, departure_date, adult_count, actual_check_in_at)
       VALUES (tn('ABC'), pr('BALI'), 'S3', ln('R1', '201', '2026-10-01'), gu('G1'), '2026-10-01', '2026-10-04', 2, now())$q$);
SELECT expect_error('CHECKED_OUT requires actual_check_out_at', '23514',
    $q$UPDATE stays SET status = 'CHECKED_OUT' WHERE stay_number = 'S1'$q$);

INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date) VALUES
    (tn('ABC'), pr('BALI'), st('S1'), rm('201'), now(), '2026-10-01'),
    (tn('ABC'), pr('BALI'), st('S2'), rm('305'), now(), '2026-10-01');

SELECT expect_error('a room holds at most one open stay segment', '23505',
    $q$UPDATE stay_rooms SET room_id = rm('201') WHERE stay_id = st('S2')$q$);
SELECT expect_error('a stay has at most one open segment', '23505',
    $q$INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date)
       VALUES (tn('ABC'), pr('BALI'), st('S1'), rm('410'), now(), '2026-10-01')$q$);

-- Room move on the arrival day: close 201 (zero-night segment), open 410.
BEGIN;
UPDATE stay_rooms SET check_out_at = now(), end_business_date = '2026-10-01'
 WHERE stay_id = st('S1') AND check_out_at IS NULL;
INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date, move_reason)
VALUES (tn('ABC'), pr('BALI'), st('S1'), rm('410'), now(), '2026-10-01', 'Guest request');
COMMIT;

SELECT expect_error('segment end business date >= start', '23514',
    $q$UPDATE stay_rooms SET end_business_date = '2026-09-30' WHERE id = sr('S1', '201')$q$);

------------------------------------------------------------------------------------------
-- Folios
------------------------------------------------------------------------------------------
INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id) VALUES
    (tn('ABC'), pr('BALI'), 'F1', rs('R1'), st('S1')),
    (tn('ABC'), pr('BALI'), 'F2', rs('R1'), st('S2')),
    (tn('ABC'), pr('BALI'), 'F4', rs('R1'), NULL);

SELECT expect_error('one GUEST folio per stay', '23505',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id) VALUES (tn('ABC'), pr('BALI'), 'F3', rs('R1'), st('S1'))$q$);
SELECT expect_error('one open unlinked (deposit) folio per reservation', '23505',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES (tn('ABC'), pr('BALI'), 'F5', rs('R1'))$q$);

------------------------------------------------------------------------------------------
-- Ledger: exclusive room charge 1,000,000 + SVC 10% + VAT 11% on service = 1,221,000
------------------------------------------------------------------------------------------
WITH i AS (
    INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                             stay_id, stay_room_id, description, quantity, unit_price, price_mode, base_amount, net_amount,
                             service_charge_total, tax_total, debit, source, idempotency_key)
    VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('ROOM'),
            st('S1'), sr('S1', '410'), 'Room 410 - 01 Oct 2026', 1, 1000000, 'EXCLUSIVE', 1000000, 1000000,
            100000, 121000, 1221000, 'ROOM_POSTING', 'RC-S1-1001')
    RETURNING id, tenant_id, property_id)
INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, service_charge_id, tax_id,
                                   code, name, rate, tax_on_service, base_amount, amount, sequence)
SELECT i.tenant_id, i.property_id, i.id, c.*
  FROM i, (VALUES ('SERVICE_CHARGE', sc('SVC'), NULL::bigint, 'SVC', 'Service 10%', 10.0, NULL::boolean, 1000000, 100000, 1),
                  ('TAX',            NULL,      tx('VAT'),    'VAT', 'VAT 11%',     11.0, true,          1100000, 121000, 1))
       AS c(component_type, service_charge_id, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence);

-- Inclusive nett price 1,110,000 (SVC 10%, VAT 11% on service): net 909,091 + 90,909 + 110,000.
WITH i AS (
    INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                             stay_id, stay_room_id, description, quantity, unit_price, price_mode, base_amount, net_amount,
                             service_charge_total, tax_total, debit, source, idempotency_key)
    VALUES (tn('ABC'), pr('BALI'), fo('F2'), '2026-10-01', '2026-10-01', 'CHARGE', cc('NETT'),
            st('S2'), sr('S2', '305'), 'Room 305 - 01 Oct 2026', 1, 1110000, 'INCLUSIVE', 1110000, 909091,
            90909, 110000, 1110000, 'ROOM_POSTING', 'RC-S2-1001')
    RETURNING id, tenant_id, property_id)
INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, service_charge_id, tax_id,
                                   code, name, rate, tax_on_service, base_amount, amount, sequence)
SELECT i.tenant_id, i.property_id, i.id, c.*
  FROM i, (VALUES ('SERVICE_CHARGE', sc('SVC'), NULL::bigint, 'SVC', 'Service 10%', 10.0, NULL::boolean, 909091, 90909, 1),
                  ('TAX',            NULL,      tx('VAT'),    'VAT', 'VAT 11%',     11.0, true,          1000000, 110000, 1))
       AS c(component_type, service_charge_id, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence);

SELECT expect_error('item totals must equal component sums (deferred check)', '23514',
    $q$WITH i AS (
         INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                  description, quantity, unit_price, price_mode, base_amount, net_amount, tax_total, debit, source)
         VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('LAUNDRY'),
                 'Laundry', 1, 100000, 'EXCLUSIVE', 100000, 100000, 11000, 111000, 'MANUAL')
         RETURNING id, tenant_id, property_id)
       INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence)
       SELECT tenant_id, property_id, id, 'TAX', tx('VAT'), 'VAT', 'VAT', 11, true, 100000, 10000, 1 FROM i$q$);
SELECT expect_error('components cannot be added to an item later (totals re-checked)', '23514',
    $q$INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence)
       VALUES (tn('ABC'), pr('BALI'), fi('RC-S1-1001'), 'TAX', tx('CITY'), 'CITY', 'City', 1, false, 1000000, 10000, 2)$q$);
SELECT expect_error('debit and credit cannot both be set', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, credit, source, reason)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'ADJUSTMENT', cc('LAUNDRY'),
               'x', 1, 0, 'EXCLUSIVE', 0, 0, 10, 10, 'MANUAL', 'x')$q$);
SELECT expect_ok('a transferred charge has the source TRANSFER (00062)',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source, reference_type, reference_id)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'TRANSFER', 'FOLIO_TRANSFER', '1')$q$);
SELECT expect_error('a source is one of the known ones (00062)', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MAGIC')$q$);
SELECT expect_error('ledger amount = net + service + tax', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 150, 'MANUAL')$q$);
SELECT expect_error('EXCLUSIVE: net = base - discount, no rounding adjustment', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, rounding_adjustment, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 101, 1, 101, 'MANUAL')$q$);
SELECT expect_error('INCLUSIVE: guest pays exactly base - discount', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('NETT'),
               'x', 1, 1110000, 'INCLUSIVE', 1110000, 1110001, 1110001, 'ROOM_POSTING')$q$);
SELECT expect_error('service date cannot be after the business date', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-02', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MANUAL')$q$);
SELECT expect_error('posting business date must be a real business day', '23503',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-12-01', '2026-12-01', 'CHARGE', cc('LAUNDRY'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MANUAL')$q$);
SELECT expect_error('folio items are append-only (update)', '23001',
    $q$UPDATE folio_items SET description = 'edited' WHERE idempotency_key = 'RC-S1-1001'$q$);
SELECT expect_error('folio items are append-only (delete)', '23001',
    $q$DELETE FROM folio_items WHERE idempotency_key = 'RC-S1-1001'$q$);
SELECT expect_error('components are append-only', '23001',
    $q$UPDATE folio_item_components SET amount = 0$q$);

------------------------------------------------------------------------------------------
-- Posting register: room-charge idempotency
------------------------------------------------------------------------------------------
INSERT INTO stay_charge_postings (tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id,
                                  folio_item_id, business_date, posting_trigger)
VALUES (tn('ABC'), pr('BALI'), st('S1'), sr('S1', '410'), '2026-10-01', 'ROOM_NIGHT', cc('ROOM'),
        fi('RC-S1-1001'), '2026-10-01', 'NIGHT_AUDIT');

SELECT expect_error('same stay/night cannot be posted twice, even via another segment and charge code', '23505',
    $q$INSERT INTO stay_charge_postings (tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id,
                                         folio_item_id, business_date, posting_trigger)
       VALUES (tn('ABC'), pr('BALI'), st('S1'), sr('S1', '201'), '2026-10-01', 'ROOM_NIGHT', cc('ROOM_EXEMPT'),
               fi('RC-S2-1001'), '2026-10-01', 'MANUAL')$q$);

-- Same-day reversal of the room charge (negated item + negated components), then flip the register row.
WITH i AS (
    INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                             reverses_item_id, stay_id, stay_room_id, description, quantity, unit_price, price_mode,
                             base_amount, net_amount, service_charge_total, tax_total, credit, source, reason, idempotency_key, approved_by)
    VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'REVERSAL', cc('ROOM'),
            fi('RC-S1-1001'), st('S1'), sr('S1', '410'), 'Reversal: Room 410 - 01 Oct 2026', -1, 1000000, 'EXCLUSIVE',
            -1000000, -1000000, -100000, -121000, 1221000, 'SYSTEM', 'Wrong rate', 'REV-RC-S1-1001', us('ABC', 'admin@hotel.com'))
    RETURNING id, tenant_id, property_id)
INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, service_charge_id, tax_id,
                                   code, name, rate, tax_on_service, base_amount, amount, sequence)
SELECT i.tenant_id, i.property_id, i.id, c.*
  FROM i, (VALUES ('SERVICE_CHARGE', sc('SVC'), NULL::bigint, 'SVC', 'Service 10%', 10.0, NULL::boolean, -1000000, -100000, 1),
                  ('TAX',            NULL,      tx('VAT'),    'VAT', 'VAT 11%',     11.0, true,          -1100000, -121000, 1))
       AS c(component_type, service_charge_id, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence);

SELECT expect_error('an item can be reversed only once', '23505',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                reverses_item_id, description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source, reason, approved_by)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'REVERSAL', cc('ROOM'),
               fi('RC-S1-1001'), 'dup', -1, 1221000, 'EXCLUSIVE', -1221000, -1221000, 1221000, 'SYSTEM', 'dup', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_error('a reversal needs an approver', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                reverses_item_id, description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source, reason)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'REVERSAL', cc('ROOM'),
               fi('RC-S1-1001'), 'x', -1, 1221000, 'EXCLUSIVE', -1221000, -1221000, 1221000, 'SYSTEM', 'x')$q$);
SELECT expect_error('an adjustment needs an approver', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source, reason)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'ADJUSTMENT', cc('ROOM'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MANUAL', 'x')$q$);
SELECT expect_error('an ordinary charge has no approver', '23514',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source, approved_by)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('ROOM'),
               'x', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MANUAL', us('ABC', 'admin@hotel.com'))$q$);

UPDATE stay_charge_postings SET status = 'REVERSED', reversal_item_id = fi('REV-RC-S1-1001')
 WHERE folio_item_id = fi('RC-S1-1001');

SELECT expect_ok('after a reversal the night can be posted again (exactly once)',
    $q$WITH i AS (
         INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                  stay_id, stay_room_id, description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source, idempotency_key)
         VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'CHARGE', cc('ROOM_EXEMPT'),
                 st('S1'), sr('S1', '410'), 'Room 410 (corrected)', 1, 900000, 'EXCLUSIVE', 900000, 900000, 900000, 'ROOM_POSTING', 'RC-S1-1001-B')
         RETURNING id)
       INSERT INTO stay_charge_postings (tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id,
                                         folio_item_id, business_date, posting_trigger)
       SELECT tn('ABC'), pr('BALI'), st('S1'), sr('S1', '410'), '2026-10-01', 'ROOM_NIGHT', cc('ROOM_EXEMPT'), id, '2026-10-01', 'RECOVERY' FROM i$q$);
SELECT expect_error('register rows are immutable except POSTED -> REVERSED', '23001',
    $q$UPDATE stay_charge_postings SET service_date = '2026-09-30'$q$);
SELECT expect_error('register rows cannot be deleted', '23001',
    $q$DELETE FROM stay_charge_postings$q$);

------------------------------------------------------------------------------------------
-- Payments
------------------------------------------------------------------------------------------
INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, idempotency_key)
VALUES (tn('ABC'), pr('BALI'), 'P1', fo('F1'), 'PAYMENT', 'CASH', 500000, '2026-10-01', 'PAY-KEY-1');
INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, payment_id,
                         description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source)
VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'PAYMENT', pm('P1'),
        'Cash payment', 1, 500000, 'EXCLUSIVE', -500000, -500000, 500000, 'MANUAL');

SELECT expect_ok('a payment may be made by QRIS, and keeps the fee snapshot of its method',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, mdr_rate, mdr_fee, expected_settlement_date)
       VALUES (tn('ABC'), pr('BALI'), 'PQR1', fo('F1'), 'PAYMENT', 'QRIS', 100000, '2026-10-01', 0.7, 700, '2026-10-02')$q$,
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, payment_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'PAYMENT', pm('PQR1'),
               'QRIS payment', 1, 100000, 'EXCLUSIVE', -100000, -100000, 100000, 'MANUAL')$q$);
SELECT expect_error('a method that does not exist is no payment method', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'PQR2', fo('F1'), 'PAYMENT', 'PAYPAL', 100, '2026-10-01')$q$);
SELECT expect_error('a bank transfer keeps no fee snapshot', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, mdr_rate, mdr_fee, expected_settlement_date)
       VALUES (tn('ABC'), pr('BALI'), 'PQR3', fo('F1'), 'PAYMENT', 'BANK_TRANSFER', 100, '2026-10-01', 0.7, 1, '2026-10-02')$q$);
SELECT expect_error('one ledger entry per payment', '23505',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, payment_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'PAYMENT', pm('P1'),
               'dup', 1, 500000, 'EXCLUSIVE', -500000, -500000, 500000, 'MANUAL')$q$);
SELECT expect_error('PAYMENT ledger entry must be a credit', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'P3', fo('F1'), 'PAYMENT', 'CARD', 100, '2026-10-01')$q$,
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, payment_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'PAYMENT', pm('P3'),
               'wrong side', 1, 100, 'EXCLUSIVE', 100, 100, 100, 'MANUAL')$q$);
SELECT expect_error('duplicate payment (idempotency key)', '23505',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, idempotency_key)
       VALUES (tn('ABC'), pr('BALI'), 'P2', fo('F1'), 'PAYMENT', 'CASH', 500000, '2026-10-01', 'PAY-KEY-1')$q$);
SELECT expect_error('payment amount > 0', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'P4', fo('F1'), 'PAYMENT', 'CASH', 0, '2026-10-01')$q$);
SELECT expect_error('a refund references its original payment', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'P5', fo('F1'), 'REFUND', 'CASH', 100, '2026-10-01')$q$);
SELECT expect_error('payments have no currency column', '42703',
    $q$UPDATE payments SET currency_code = 'IDR'$q$);
SELECT expect_error('payment amount is immutable', '23001',
    $q$UPDATE payments SET amount = 1 WHERE payment_number = 'P1'$q$);
SELECT expect_error('a void needs an approver', '23514',
    $q$UPDATE payments SET status = 'VOIDED', voided_at = now(), void_reason = 'Wrong folio' WHERE payment_number = 'P1'$q$);
SELECT expect_error('a refund needs an approver', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, refund_of_payment_id)
       VALUES (tn('ABC'), pr('BALI'), 'P6', fo('F1'), 'REFUND', 'CASH', 100, '2026-10-01', pm('P1'))$q$);
SELECT expect_error('a plain payment has no approver', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, approved_by)
       VALUES (tn('ABC'), pr('BALI'), 'P7', fo('F1'), 'PAYMENT', 'CASH', 100, '2026-10-01', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_ok('payment may be voided (POSTED -> VOIDED) with its approver',
    $q$UPDATE payments SET status = 'VOIDED', voided_at = now(), void_reason = 'Wrong folio', approved_by = us('ABC', 'admin@hotel.com') WHERE payment_number = 'P1'$q$);
SELECT expect_error('payments cannot be deleted', '23001',
    $q$DELETE FROM payments WHERE payment_number = 'P1'$q$);

------------------------------------------------------------------------------------------
-- Configuration locks
------------------------------------------------------------------------------------------
SELECT expect_error('property currency locked once transactions exist', '23001',
    $q$UPDATE properties SET currency_code = 'USD' WHERE code = 'BALI'$q$);
SELECT expect_ok('property currency can change before any transaction',
    $q$UPDATE properties SET currency_code = 'USD', currency_decimals = 2 WHERE code = 'SG'$q$);
SELECT expect_error('price_mode of a used charge code is locked', '23001',
    $q$UPDATE charge_codes SET price_mode = 'INCLUSIVE' WHERE id = cc('ROOM')$q$);
SELECT expect_ok('price_mode of an unused charge code can change',
    $q$UPDATE charge_codes SET price_mode = 'INCLUSIVE' WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_error('charge_type of a room revenue code is locked', '23001',
    $q$UPDATE charge_codes SET charge_type = 'OTHER' WHERE id = cc('ROOM')$q$);

------------------------------------------------------------------------------------------
-- GL account codes (accounting-ready ledger): optional text codes, shape checked, never an id
------------------------------------------------------------------------------------------
SELECT expect_ok('a charge code can be mapped to a revenue account',
    $q$UPDATE charge_codes SET gl_account_code = '4-1100' WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_ok('an account code can be cleared',
    $q$UPDATE charge_codes SET gl_account_code = NULL WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_error('an account code with a space is refused', '23514',
    $q$UPDATE charge_codes SET gl_account_code = '4 1100' WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_error('an account code must start with a letter or digit', '23514',
    $q$UPDATE charge_codes SET gl_account_code = '-4100' WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_error('a lower case account code is refused (the service upper-cases it)', '23514',
    $q$UPDATE charge_codes SET gl_account_code = 'rev' WHERE id = cc('ROOM_EXEMPT')$q$);
SELECT expect_error('a tax account code is checked', '23514',
    $q$UPDATE taxes SET gl_account_code = 'a b'$q$);
SELECT expect_error('a service charge account code is checked', '23514',
    $q$UPDATE service_charges SET gl_account_code = 'a b'$q$);
SELECT expect_error('an account code is at most 30 characters', '22001',
    $q$UPDATE taxes SET gl_account_code = repeat('A', 31)$q$);

------------------------------------------------------------------------------------------
-- TRUNCATE is refused on the ledger and the logs (row triggers do not fire for it)
------------------------------------------------------------------------------------------
SELECT expect_error('folio_items cannot be truncated', '23001', $q$TRUNCATE folio_items CASCADE$q$);
SELECT expect_error('folio_item_components cannot be truncated', '23001', $q$TRUNCATE folio_item_components CASCADE$q$);
SELECT expect_error('payments cannot be truncated', '23001', $q$TRUNCATE payments CASCADE$q$);
SELECT expect_error('the room charge register cannot be truncated', '23001', $q$TRUNCATE stay_charge_postings CASCADE$q$);
SELECT expect_error('audit_logs cannot be truncated', '23001', $q$TRUNCATE audit_logs CASCADE$q$);
SELECT expect_error('housekeeping_logs cannot be truncated', '23001', $q$TRUNCATE housekeeping_logs CASCADE$q$);
SELECT expect_error('cascading from a parent table does not get around it', '23001', $q$TRUNCATE folios CASCADE$q$);

------------------------------------------------------------------------------------------
-- E-mail outbox and property contact details
------------------------------------------------------------------------------------------
SELECT expect_ok('a property may propose an incidental deposit at check-in (zero by default)',
    $q$UPDATE properties SET checkin_incidental_deposit = 250000 WHERE code = 'BALI'$q$,
    $q$UPDATE properties SET checkin_incidental_deposit = 0 WHERE code = 'BALI'$q$);
SELECT expect_error('the incidental deposit cannot be negative', '23514',
    $q$UPDATE properties SET checkin_incidental_deposit = -1 WHERE code = 'BALI'$q$);
SELECT expect_ok('a property keeps contact details for its documents',
    $q$UPDATE properties SET phone = '+62 361 1', email = 'info@bali.test', tax_id = '01.234', document_footer = 'Thank you' WHERE code = 'BALI'$q$);
INSERT INTO email_outbox (tenant_id, property_id, kind, reservation_id, to_address)
VALUES (tn('ABC'), pr('BALI'), 'RESERVATION_CONFIRMATION', rs('R1'), 'siti@example.test');
SELECT expect_error('an unknown e-mail kind is refused', '23514',
    $q$INSERT INTO email_outbox (tenant_id, property_id, kind, reservation_id, to_address)
       VALUES (tn('ABC'), pr('BALI'), 'NEWSLETTER', rs('R1'), 'a@b.test')$q$);
SELECT expect_error('an e-mail needs a real reservation of its own property', '23503',
    $q$INSERT INTO email_outbox (tenant_id, property_id, kind, reservation_id, to_address)
       VALUES (tn('ABC'), pr('BALI'), 'RESERVATION_CONFIRMATION', 999999, 'a@b.test')$q$);
SELECT expect_error('an unknown e-mail status is refused', '23514',
    $q$UPDATE email_outbox SET status = 'MAYBE'$q$);
SELECT expect_error('SENT needs a sent time', '23514',
    $q$UPDATE email_outbox SET status = 'SENT'$q$);
SELECT expect_error('a sent time without SENT is refused', '23514',
    $q$UPDATE email_outbox SET sent_at = now()$q$);
SELECT expect_ok('a message is marked sent with its time',
    $q$UPDATE email_outbox SET status = 'SENT', sent_at = now()$q$);

------------------------------------------------------------------------------------------
-- Companies, booking groups and the city ledger
------------------------------------------------------------------------------------------
INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES (tn('ABC'), pr('BALI'), 'ACME', 'Acme Corp', 1000000);
INSERT INTO companies (tenant_id, property_id, code, name) VALUES (tn('XYZ'), pr('SG'), 'ACME', 'Acme SG');
SELECT expect_error('a company code is unique per property', '23505',
    $q$INSERT INTO companies (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'ACME', 'Again')$q$);
SELECT expect_error('a credit limit is not negative', '23514',
    $q$INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES (tn('ABC'), pr('BALI'), 'NEG', 'x', -1)$q$);
SELECT expect_error('a company belongs to a property of its own tenant', '23503',
    $q$INSERT INTO companies (tenant_id, property_id, code, name) VALUES (tn('XYZ'), pr('BALI'), 'X', 'x')$q$);

INSERT INTO booking_groups (tenant_id, property_id, code, name, company_id, arrival_date, departure_date)
VALUES (tn('ABC'), pr('BALI'), 'CONF', 'Conference', (SELECT id FROM companies WHERE code = 'ACME' AND property_id = pr('BALI')), '2026-10-01', '2026-10-05');
SELECT expect_error('a group ends after it starts', '23514',
    $q$INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date)
       VALUES (tn('ABC'), pr('BALI'), 'BAD', 'x', '2026-10-05', '2026-10-05')$q$);
SELECT expect_error('a group code is unique per property', '23505',
    $q$INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date)
       VALUES (tn('ABC'), pr('BALI'), 'CONF', 'x', '2026-10-01', '2026-10-02')$q$);
SELECT expect_error('a group cannot name another property''s company', '23503',
    $q$INSERT INTO booking_groups (tenant_id, property_id, code, name, company_id, arrival_date, departure_date)
       VALUES (tn('ABC'), pr('BALI'), 'XC', 'x', (SELECT id FROM companies WHERE property_id = pr('SG')), '2026-10-01', '2026-10-02')$q$);
SELECT expect_ok('a reservation names a company and a group of its property',
    $q$UPDATE reservations SET company_id = (SELECT id FROM companies WHERE property_id = pr('BALI')),
                               booking_group_id = (SELECT id FROM booking_groups WHERE code = 'CONF') WHERE id = rs('R1')$q$);
SELECT expect_error('a reservation cannot name another property''s company', '23503',
    $q$UPDATE reservations SET company_id = (SELECT id FROM companies WHERE property_id = pr('SG')) WHERE id = rs('R1')$q$);

SELECT expect_ok('a group names the guest made from its contact (a guest of its own tenant)',
    $q$UPDATE booking_groups SET contact_guest_id = (SELECT id FROM guests WHERE code = 'G1') WHERE code = 'CONF'$q$);
SELECT expect_error('a group cannot name a guest that does not exist as its contact', '23503',
    $q$UPDATE booking_groups SET contact_guest_id = 999999 WHERE code = 'CONF'$q$);
SELECT expect_ok('a guest made by a group booking can be a placeholder, and no guest is one by default',
    $q$INSERT INTO guests (tenant_id, code, origin_property_id, last_name, is_placeholder) VALUES (tn('ABC'), 'GP1', pr('BALI'), 'Conference · 1', true)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM guests WHERE is_placeholder AND code <> 'GP1') = 0 THEN 1 ELSE 0 END)$q$);

SELECT expect_error('a CITY_LEDGER payment needs a company', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'PCL0', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01')$q$);
SELECT expect_error('only a CITY_LEDGER payment has a company', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL1', fo('F1'), 'PAYMENT', 'CASH', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$);
SELECT expect_error('a transfer cannot name another property''s company', '23503',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL2', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('SG')))$q$);
SELECT expect_ok('a transfer to a company of the property is a payment',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL3', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$);

INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
VALUES (tn('ABC'), pr('BALI'), 'CLR1', (SELECT id FROM companies WHERE property_id = pr('BALI')), 50, 'CASH', '2026-10-01');
SELECT expect_ok('a company may pay a receipt by QRIS, with the fee snapshot of its method', $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date, mdr_rate, mdr_fee, expected_settlement_date)
    VALUES (tn('ABC'), pr('BALI'), 'CLRQ', (SELECT id FROM companies WHERE property_id = pr('BALI')), 5000, 'QRIS', '2026-10-01', 0.7, 35, '2026-10-02')$q$);
SELECT expect_error('a receipt amount is positive', '23514',
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CLR2', (SELECT id FROM companies WHERE property_id = pr('BALI')), 0, 'CASH', '2026-10-01')$q$);
SELECT expect_error('a receipt is not paid by CITY_LEDGER', '23514',
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CLR3', (SELECT id FROM companies WHERE property_id = pr('BALI')), 5, 'CITY_LEDGER', '2026-10-01')$q$);
SELECT expect_error('a receipt number is unique per property', '23505',
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CLR1', (SELECT id FROM companies WHERE property_id = pr('BALI')), 5, 'CASH', '2026-10-01')$q$);
SELECT expect_error('a receipt needs a business day of its property', '23503',
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CLR4', (SELECT id FROM companies WHERE property_id = pr('BALI')), 5, 'CASH', '2030-01-01')$q$);
SELECT expect_error('a receipt cannot name another property''s company', '23503',
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CLR5', (SELECT id FROM companies WHERE property_id = pr('SG')), 5, 'CASH', '2026-10-01')$q$);
SELECT expect_error('a receipt amount is immutable', '23001', $q$UPDATE city_ledger_receipts SET amount = 1$q$);
SELECT expect_error('a receipt is never deleted', '23001', $q$DELETE FROM city_ledger_receipts$q$);
SELECT expect_error('a voided receipt needs its reason', '23514',
    $q$UPDATE city_ledger_receipts SET status = 'VOIDED', voided_at = now()$q$);
SELECT expect_ok('a receipt can be voided',
    $q$UPDATE city_ledger_receipts SET status = 'VOIDED', voided_at = now(), void_reason = 'wrong', approved_by = us('ABC', 'admin@hotel.com')$q$);
SELECT expect_error('a voided receipt cannot change again', '23001', $q$UPDATE city_ledger_receipts SET status = 'POSTED', voided_at = NULL, void_reason = NULL$q$);
SELECT expect_error('receipts cannot be truncated', '23001', $q$TRUNCATE city_ledger_receipts CASCADE$q$);


-- Invoices to a company (every case builds its own rows: a CITY_LEDGER payment would block the down migration)
SELECT expect_error('an invoice total is positive', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV2', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 0)$q$);
SELECT expect_error('an invoice is not due before its date', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV3', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-09-30', 100)$q$);
SELECT expect_error('an invoice number is unique per property', '23505',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$);
SELECT expect_error('an invoice cannot name another propertys company', '23503',
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV4', (SELECT id FROM companies WHERE property_id = pr('SG')), '2026-10-01', '2026-10-31', 5)$q$);
SELECT expect_error('a transfer is on one live invoice only', '23505',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV6', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV6'), pm('PCL9'), 100)$q$);
SELECT expect_error('an invoice total is immutable', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoices SET total = 1$q$);
SELECT expect_error('an invoice is never deleted', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$DELETE FROM city_ledger_invoices$q$);
SELECT expect_error('an invoice line amount is immutable', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoice_lines SET amount = 1$q$);
SELECT expect_error('an invoice line is never deleted', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$DELETE FROM city_ledger_invoice_lines$q$);
SELECT expect_error('a voided invoice needs its reason', '23514',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoices SET status = 'VOIDED', voided_at = now()$q$);
SELECT expect_ok('a voided invoice releases its line and the transfer can be invoiced again',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoices SET status = 'VOIDED', voided_at = now(), void_reason = 'wrong'$q$,
    $q$UPDATE city_ledger_invoice_lines SET released_at = now()$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV5', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV5'), pm('PCL9'), 100)$q$);
SELECT expect_error('a released line cannot change again', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoices SET status = 'VOIDED', voided_at = now(), void_reason = 'wrong'$q$,
    $q$UPDATE city_ledger_invoice_lines SET released_at = now()$q$,
    $q$UPDATE city_ledger_invoice_lines SET released_at = now() WHERE released_at IS NOT NULL$q$);
SELECT expect_error('a voided invoice cannot change again', '23001',
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, company_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCL9', fo('F1'), 'PAYMENT', 'CITY_LEDGER', 100, '2026-10-01', (SELECT id FROM companies WHERE property_id = pr('BALI')))$q$,
    $q$INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
       VALUES (tn('ABC'), pr('BALI'), 'CINV1', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100)$q$,
    $q$INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINV1'), pm('PCL9'), 100)$q$,
    $q$UPDATE city_ledger_invoices SET status = 'VOIDED', voided_at = now(), void_reason = 'wrong'$q$,
    $q$UPDATE city_ledger_invoice_lines SET released_at = now()$q$,
    $q$UPDATE city_ledger_invoices SET status = 'ISSUED', voided_at = NULL, void_reason = NULL$q$);
SELECT expect_error('invoices cannot be truncated', '23001',
    $q$TRUNCATE city_ledger_invoices CASCADE$q$);

-- Receipt allocations (a receipt pays invoices of its own company)
INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total)
VALUES (tn('ABC'), pr('BALI'), 'CINVA', (SELECT id FROM companies WHERE property_id = pr('BALI')), '2026-10-01', '2026-10-31', 100);
SELECT expect_ok('a receipt pays an invoice of its company',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 40)$q$);
SELECT expect_error('a receipt pays an invoice once', '23505',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 40)$q$,
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 10)$q$);
SELECT expect_error('an allocation is positive', '23514',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 0)$q$);
SELECT expect_error('a receipt cannot pay another company''s invoice', '23503',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('SG')), 5)$q$);
SELECT expect_error('an allocation cannot change', '23001',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 40)$q$,
    $q$UPDATE city_ledger_receipt_allocations SET amount = 1$q$);
SELECT expect_error('an allocation is never deleted', '23001',
    $q$INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM city_ledger_receipts WHERE receipt_number = 'CLR1'), (SELECT id FROM city_ledger_invoices WHERE invoice_number = 'CINVA'), (SELECT id FROM companies WHERE property_id = pr('BALI')), 40)$q$,
    $q$DELETE FROM city_ledger_receipt_allocations$q$);
SELECT expect_error('allocations cannot be truncated', '23001', $q$TRUNCATE city_ledger_receipt_allocations CASCADE$q$);

-- Housekeeping flags and the cleaning list
INSERT INTO room_hk_flags (tenant_id, property_id, room_id, priority, dnd) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), 'HIGH', true);
SELECT expect_error('a room has one set of flags', '23505',
    $q$INSERT INTO room_hk_flags (tenant_id, property_id, room_id) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1))$q$);
SELECT expect_error('a flag priority is NORMAL or HIGH', '23514',
    $q$UPDATE room_hk_flags SET priority = 'URGENT'$q$);
SELECT expect_ok('a task is generated for a room and a date', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$);
SELECT expect_error('one active AUTO task per room and date, whatever the type', '23505', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'CHECKOUT')$q$,
    $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'STAYOVER')$q$);
SELECT expect_ok('a DONE or SKIPPED AUTO task does not count as active', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, started_at, completed_at)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'CHECKOUT', 'DONE', now(), now())$q$,
    $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, status, completed_at, notes)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'STAYOVER', 'SKIPPED', now(), 'guest asked')$q$,
    $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$);
SELECT expect_error('one AUTO task per room, date and type (the same type twice)', '23505', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$);
SELECT expect_ok('a MANUAL task of the same kind can be added', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, source)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY', 'MANUAL')$q$);
SELECT expect_error('a task has a known type', '23514', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'NAP')$q$);
SELECT expect_error('a task is on a business day of its property', '23503', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2030-01-01', 'DIRTY')$q$);
SELECT expect_error('a task cannot name a room of another property', '23503', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('XYZ'), pr('SG'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$);
SELECT expect_error('an assignee is a user of the tenant', '23503', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, assigned_to, assigned_at)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY', 999999, now())$q$);
SELECT expect_error('an assignee has an assignment time', '23514', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, assigned_to)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_error('a finished task has its completion time', '23514', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$UPDATE housekeeping_tasks SET status = 'DONE', started_at = now()$q$);
SELECT expect_error('a skipped task has its reason', '23514', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$UPDATE housekeeping_tasks SET status = 'SKIPPED', completed_at = now()$q$);
SELECT expect_error('a task in progress has its start time', '23514', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$UPDATE housekeeping_tasks SET status = 'IN_PROGRESS'$q$);
SELECT expect_ok('a task can be started, finished and skipped with their times', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 'DIRTY')$q$,
    $q$UPDATE housekeeping_tasks SET status = 'IN_PROGRESS', started_at = now()$q$,
    $q$UPDATE housekeeping_tasks SET status = 'DONE', completed_at = now()$q$);

-- Maintenance requests
SELECT expect_ok('a request about a room',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_ok('a request about a place',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', NULL, 'Lobby', 'AC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_error('a request is about a room or a place', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', NULL, NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_error('a request number is unique per property', '23505',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$,
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_error('a known category', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'MAGIC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_error('a known priority', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'URGE', '2026-10-01')$q$);
SELECT expect_error('a request is on a business day of its property', '23503',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2030-01-01')$q$);
SELECT expect_error('a request cannot name a room of another property', '23503',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('XYZ'), pr('SG'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$);
SELECT expect_error('an assignee has an assignment time', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date, assigned_to)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_error('a block needs a room', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date, room_block_id)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', NULL, 'Lobby', 'AC', 'x', 'NORMAL', '2026-10-01', 1)$q$);
SELECT expect_error('a resolved request has its closing time', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$,
    $q$UPDATE maintenance_requests SET status = 'RESOLVED'$q$);
SELECT expect_error('a cancelled request has its reason', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$,
    $q$UPDATE maintenance_requests SET status = 'CANCELLED', closed_at = now()$q$);
SELECT expect_error('a request in progress has its start time', '23514',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$,
    $q$UPDATE maintenance_requests SET status = 'IN_PROGRESS'$q$);
SELECT expect_ok('a request is worked on and resolved',
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'MNT1', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, 'AC', 'x', 'NORMAL', '2026-10-01')$q$,
    $q$UPDATE maintenance_requests SET status = 'IN_PROGRESS', started_at = now()$q$,
    $q$UPDATE maintenance_requests SET status = 'RESOLVED', closed_at = now(), resolution_note = 'fixed'$q$);

-- Lost and found
SELECT expect_ok('an item found in a room',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$);
SELECT expect_ok('an item found at a place',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', NULL, 'Pool', '2026-10-01')$q$);
SELECT expect_error('an item is from a room or a place', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', NULL, NULL, '2026-10-01')$q$);
SELECT expect_error('an item number is unique per property', '23505',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$);
SELECT expect_error('a known category', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'MAGIC', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$);
SELECT expect_error('an item is found on a business day of its property', '23503',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2030-01-01')$q$);
SELECT expect_error('an item cannot name a room of another property', '23503',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('XYZ'), pr('SG'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$);
SELECT expect_error('a known status', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET status = 'LOST'$q$);
SELECT expect_error('a stored item is not closed', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET closed_at = now(), closed_on = '2026-10-01'$q$);
SELECT expect_error('a closed item has its time', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET status = 'DISPOSED', close_note = 'x'$q$);
SELECT expect_error('a returned item has its claimant', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET status = 'RETURNED', closed_at = now(), closed_on = '2026-10-01'$q$);
SELECT expect_error('a disposed item has its reason', '23514',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET status = 'DISPOSED', closed_at = now(), closed_on = '2026-10-01'$q$);
SELECT expect_ok('an item is handed back',
    $q$INSERT INTO lost_found_items (tenant_id, property_id, item_number, description, category, room_id, location, found_on)
       VALUES (tn('ABC'), pr('BALI'), 'LF1', 'x', 'BAGS', (SELECT id FROM rooms WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), NULL, '2026-10-01')$q$,
    $q$UPDATE lost_found_items SET status = 'RETURNED', closed_at = now(), closed_on = '2026-10-01', claimant_name = 'Siti'$q$);

-- Chart of accounts and the system account map
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('ABC'), pr('BALI'), '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH');
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), pr('SG'), '1110', 'Cash SG', 'ASSET', 'DEBIT', 'CASH');
SELECT expect_ok('the same code in another property',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('XYZ'), pr('SG'), '4110', 'x', 'REVENUE', 'CREDIT', 'REV_ROOMS')$q$);
SELECT expect_error('an account code is unique per property', '23505',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), '1110', 'x', 'ASSET', 'DEBIT', 'CASH')$q$);
SELECT expect_error('a code of the right form', '23514',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), 'bad code', 'x', 'ASSET', 'DEBIT', 'CASH')$q$);
SELECT expect_error('a known type', '23514',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), '1111', 'x', 'MAGIC', 'DEBIT', 'CASH')$q$);
SELECT expect_error('a known side', '23514',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), '1111', 'x', 'ASSET', 'UP', 'CASH')$q$);
SELECT expect_error('a known statement group', '23514',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), '1111', 'x', 'ASSET', 'DEBIT', 'NOPE')$q$);
SELECT expect_error('an account belongs to a property of its own tenant', '23503',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('XYZ'), pr('BALI'), '1111', 'x', 'ASSET', 'DEBIT', 'CASH')$q$);
SELECT expect_error('an account is not its own parent', '23514',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
       VALUES (tn('ABC'), pr('BALI'), '1112', 'x', 'ASSET', 'DEBIT', 'CASH')$q$,
    $q$UPDATE gl_accounts SET parent_id = id WHERE code = '1112'$q$);
SELECT expect_error('a parent of another property', '23503',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group, parent_id)
       VALUES (tn('ABC'), pr('BALI'), '1113', 'x', 'ASSET', 'DEBIT', 'CASH', (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1110'))$q$);
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'CASH', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'));
SELECT expect_error('a known system key', '23514',
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'PETTY', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_error('a system key is mapped once', '23505',
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'CASH', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_ok('the system key QRIS can be mapped', $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'QRIS', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_error('a key that is not a system key cannot be mapped', '23514', $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'PAYPAL', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_error('the map names an account of its property', '23503',
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'CARD', (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1110'))$q$);
SELECT expect_error('an account in the map cannot be deleted', '23503',
    $q$DELETE FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'$q$);
INSERT INTO accounting_settings (tenant_id, property_id, start_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-01');
SELECT expect_error('one settings row per property', '23505',
    $q$INSERT INTO accounting_settings (tenant_id, property_id, start_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$);
SELECT expect_error('a fiscal year starts in a month of the year', '23514',
    $q$UPDATE accounting_settings SET fiscal_year_start_month = 13$q$);

-- General ledger: journals, day posts, periods
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('ABC'), pr('BALI'), '4110', 'Room revenue', 'REVENUE', 'CREDIT', 'REV_ROOMS');
WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000001', 'DAY_CLOSE', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr);
SELECT expect_ok('a balanced journal', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000002', 'MANUAL', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a journal number is unique per property', '23505', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000001', 'MANUAL', '2026-10-02', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('one day close journal per date', '23505', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000003', 'DAY_CLOSE', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_ok('a manual journal on a day close date', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000004', 'MANUAL', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('debits equal credits at commit', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000005', 'MANUAL', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 90)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a journal has lines', '23514',
    $q$INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JV000006', 'MANUAL', '2026-10-01', 'x')$q$);
SELECT expect_error('a known journal type', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000007', 'OTHER', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a reversal names the journal it reverses', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV000008', 'REVERSAL', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('only a reversal names a journal', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reverses_journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'JV000009', 'MANUAL', '2026-10-01', 'x', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a reversal has a reason', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reverses_journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'JV000010', 'REVERSAL', '2026-10-01', 'x', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_ok('a reversal with its reason', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reverses_journal_id, reason)
       VALUES (tn('ABC'), pr('BALI'), 'JV000011', 'REVERSAL', '2026-10-01', 'x', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a journal is reversed once', '23505',
    $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reverses_journal_id, reason)
       VALUES (tn('ABC'), pr('BALI'), 'JV000012', 'REVERSAL', '2026-10-01', 'x', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$,
    $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reverses_journal_id, reason)
       VALUES (tn('ABC'), pr('BALI'), 'JV000013', 'REVERSAL', '2026-10-01', 'x', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a line is a debit or a credit', '23514',
    $q$INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) SELECT tenant_id, property_id, id, 9, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 5, 5 FROM gl_journals WHERE journal_number = 'JV000001'$q$);
SELECT expect_error('a line is not zero', '23514',
    $q$INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id) SELECT tenant_id, property_id, id, 9, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110') FROM gl_journals WHERE journal_number = 'JV000001'$q$);
SELECT expect_error('a line names an account of its property', '23503',
    $q$INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit) SELECT tenant_id, property_id, id, 9, (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1110'), 5 FROM gl_journals WHERE journal_number = 'JV000001'$q$);
SELECT expect_error('an account with entries cannot be deleted', '23503', $q$DELETE FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'$q$);
SELECT expect_error('journals are append-only (update)', '23001', $q$UPDATE gl_journals SET description = 'y'$q$);
SELECT expect_error('journals are append-only (delete)', '23001', $q$DELETE FROM gl_journals$q$);
SELECT expect_error('journal lines are append-only', '23001', $q$UPDATE gl_journal_lines SET debit = debit + 1$q$);
SELECT expect_error('journals cannot be truncated', '23001', $q$TRUNCATE gl_journals CASCADE$q$);
INSERT INTO gl_day_posts (tenant_id, property_id, business_date, journal_id) VALUES (tn('ABC'), pr('BALI'), '2026-10-01', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'));
SELECT expect_error('a day is posted once', '23505', $q$INSERT INTO gl_day_posts (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$);
SELECT expect_error('a day post is for a business day', '23503', $q$INSERT INTO gl_day_posts (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), '2026-12-01')$q$);
SELECT expect_error('day posts are append-only', '23001', $q$DELETE FROM gl_day_posts$q$);
SELECT expect_ok('a period starts on the first of a month', $q$INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$);
SELECT expect_error('a period starts on the first of a month (other day)', '23514', $q$INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES (tn('ABC'), pr('BALI'), '2026-10-02')$q$);
SELECT expect_error('a period exists once', '23505',
    $q$INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$,
    $q$INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES (tn('ABC'), pr('BALI'), '2026-10-01')$q$);
SELECT expect_error('a period has a known status', '23514', $q$INSERT INTO gl_periods (tenant_id, property_id, period_start, status) VALUES (tn('ABC'), pr('BALI'), '2026-11-01', 'LOCKED')$q$);

-- Year-end closing
SELECT expect_ok('a closing journal is flagged', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, is_closing)
       VALUES (tn('ABC'), pr('BALI'), 'JV900001', 'CLOSING', '2026-10-01', 'x', true) RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('a closing journal must be flagged', '23514', $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description)
       VALUES (tn('ABC'), pr('BALI'), 'JV900002', 'CLOSING', '2026-10-01', 'x') RETURNING id)
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit)
SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '4110'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_ok('a fiscal year', $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end) VALUES (tn('ABC'), pr('BALI'), '2025-10-01', '2026-09-30')$q$);
SELECT expect_error('a fiscal year starts on the first of a month', '23514', $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end) VALUES (tn('ABC'), pr('BALI'), '2025-10-02', '2026-09-30')$q$);
SELECT expect_error('a fiscal year ends after it starts', '23514', $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end) VALUES (tn('ABC'), pr('BALI'), '2025-10-01', '2025-10-01')$q$);
SELECT expect_error('a fiscal year exists once', '23505',
    $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end) VALUES (tn('ABC'), pr('BALI'), '2025-10-01', '2026-09-30')$q$,
    $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end) VALUES (tn('ABC'), pr('BALI'), '2025-10-01', '2026-09-30')$q$);
SELECT expect_error('a fiscal year has a known status', '23514', $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end, status) VALUES (tn('ABC'), pr('BALI'), '2026-10-01', '2027-09-30', 'LOCKED')$q$);
SELECT expect_error('a fiscal year names a journal of its property', '23503', $q$INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end, closing_journal_id) VALUES (tn('ABC'), pr('BALI'), '2026-10-01', '2027-09-30', 999999)$q$);
SELECT expect_error('retained earnings is a known system key', '23514', $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'RETAINED', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);

-- Payables: suppliers, bills, payments
INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'S1', 'Supplier one');
INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'S2', 'Supplier two');
SELECT expect_error('a supplier code is unique per property', '23505', $q$INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'S1', 'x')$q$);
INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES (tn('XYZ'), pr('SG'), 'S1', 'Supplier one of SG');
SELECT expect_error('a supplier code of the right form', '23514', $q$INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'bad code', 'x')$q$);
SELECT expect_error('payment terms of a year at most', '23514', $q$INSERT INTO suppliers (tenant_id, property_id, code, name, payment_terms_days) VALUES (tn('ABC'), pr('BALI'), 'S3', 'x', 400)$q$);
SELECT expect_error('a default account of the property', '23503', $q$INSERT INTO suppliers (tenant_id, property_id, code, name, default_account_id) VALUES (tn('ABC'), pr('BALI'), 'S3', 'x', (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1110'))$q$);
WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL1', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-1', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b;
SELECT expect_error('a bill number is unique per property', '23505', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL1', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-2', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_error('a supplier invoice is entered once', '23505', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL2', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-1', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_ok('the same invoice number of another supplier', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL2', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S2'), 'INV-1', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_ok('a voided invoice may be entered again', $q$UPDATE supplier_bills SET status = 'VOIDED', voided_at = now(), void_reason = 'x' WHERE bill_number = 'BL1'$q$, $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL3', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-1', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_error('the lines of a bill add up to its total', '23514', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL4', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-4', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 900 FROM b$q$);
SELECT expect_error('a bill has a positive total', '23514', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL4', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-4', '2026-10-01', '2026-10-31', 0, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 0 FROM b$q$);
SELECT expect_error('a due date is not before the bill date', '23514', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL4', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-4', '2026-10-01', '2026-09-01', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_error('a bill has lines', '23514', $q$INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id) VALUES (tn('ABC'), pr('BALI'), 'BL5', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), 'INV-5', '2026-10-01', '2026-10-31', 100, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a bill is for a supplier of its property', '23503', $q$WITH b AS (INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'BL6', (SELECT id FROM suppliers WHERE property_id = pr('SG') AND code = 'S1'), 'INV-6', '2026-10-01', '2026-10-31', 1000, (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id)
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) SELECT tn('ABC'), pr('BALI'), b.id, 1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 1000 FROM b$q$);
SELECT expect_error('a voided bill needs a reason', '23514', $q$UPDATE supplier_bills SET status = 'VOIDED', voided_at = now() WHERE bill_number = 'BL1'$q$);
SELECT expect_error('a bill only goes from posted to voided', '23001', $q$UPDATE supplier_bills SET total = 1 WHERE bill_number = 'BL1'$q$);
SELECT expect_error('bills are not deleted', '23001', $q$DELETE FROM supplier_bills$q$);
SELECT expect_error('bill lines are append-only', '23001', $q$UPDATE supplier_bill_lines SET amount = 1$q$);
SELECT expect_error('bills cannot be truncated', '23001', $q$TRUNCATE supplier_bills CASCADE$q$);
WITH x AS (INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'SP1', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), '2026-10-01', 400, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id, supplier_id)
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
SELECT tn('ABC'), pr('BALI'), x.supplier_id, x.id, (SELECT id FROM supplier_bills WHERE bill_number = 'BL1'), 400 FROM x;
SELECT expect_error('a payment settles bills for its whole amount', '23514', $q$WITH x AS (INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'SP2', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), '2026-10-01', 400, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id, supplier_id)
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
SELECT tn('ABC'), pr('BALI'), x.supplier_id, x.id, (SELECT id FROM supplier_bills WHERE bill_number = 'BL1'), 300 FROM x$q$);
SELECT expect_error('a payment settles bills of its own supplier', '23503', $q$WITH x AS (INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'SP2', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S2'), '2026-10-01', 400, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id, supplier_id)
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
SELECT tn('ABC'), pr('BALI'), x.supplier_id, x.id, (SELECT id FROM supplier_bills WHERE bill_number = 'BL1'), 400 FROM x$q$);
SELECT expect_error('a payment is a positive amount', '23514', $q$INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'SP3', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), '2026-10-01', 0, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a payment method of the list', '23514', $q$WITH x AS (INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'SP3', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), '2026-10-01', 400, 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id, supplier_id)
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
SELECT tn('ABC'), pr('BALI'), x.supplier_id, x.id, (SELECT id FROM supplier_bills WHERE bill_number = 'BL1'), 400 FROM x$q$);
SELECT expect_error('payment numbers are unique', '23505', $q$WITH x AS (INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'SP1', (SELECT id FROM suppliers WHERE property_id = pr('BALI') AND code = 'S1'), '2026-10-01', 400, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001')) RETURNING id, supplier_id)
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
SELECT tn('ABC'), pr('BALI'), x.supplier_id, x.id, (SELECT id FROM supplier_bills WHERE bill_number = 'BL1'), 400 FROM x$q$);
SELECT expect_error('a payment only goes from posted to voided', '23001', $q$UPDATE supplier_payments SET amount = 1$q$);
SELECT expect_error('payments are not deleted', '23001', $q$DELETE FROM supplier_payments$q$);
SELECT expect_error('allocations are append-only', '23001', $q$UPDATE supplier_payment_allocations SET amount = 1$q$);
SELECT expect_error('allocations are not deleted', '23001', $q$DELETE FROM supplier_payment_allocations$q$);
SELECT expect_error('accounts payable is a known system key', '23505', $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'ACCOUNTS_PAYABLE', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110')), (tn('ABC'), pr('BALI'), 'ACCOUNTS_PAYABLE', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_error('a payables journal type exists, an unknown one does not', '23514', $q$INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JVX', 'PAYMENTS', '2026-10-01', 'x')$q$);

-- Bank reconciliation
INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 'Main bank');
SELECT expect_error('an account of the books is registered once', '23505', $q$INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'), 'Again')$q$);
SELECT expect_error('a bank account is on an account of its property', '23503', $q$INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1110'), 'Other')$q$);
INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance, note) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), '2026-10-01', '2026-10-31', 0, 100, 'S1');
INSERT INTO bank_statement_lines (tenant_id, property_id, statement_id, line_no, line_date, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_statements WHERE note = 'S1'), 1, '2026-10-05', 100);
SELECT expect_error('a statement ends after it starts', '23514', $q$INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance, note) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), '2026-12-31', '2026-12-01', 0, 0, 'S2')$q$);
SELECT expect_error('a statement has a known status', '23514', $q$INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance, note, status) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), '2026-11-01', '2026-11-30', 0, 0, 'S2', 'DONE')$q$);
SELECT expect_error('a reconciled statement says when', '23514', $q$INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance, note, status) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), '2026-11-01', '2026-11-30', 0, 0, 'S2', 'RECONCILED')$q$);
SELECT expect_error('a statement is of a bank account of its property', '23503', $q$INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance) VALUES (tn('XYZ'), pr('SG'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), '2026-11-01', '2026-11-30', 0, 0)$q$);
SELECT expect_error('a statement line number is used once', '23505', $q$INSERT INTO bank_statement_lines (tenant_id, property_id, statement_id, line_no, line_date, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_statements WHERE note = 'S1'), 1, '2026-10-06', 5)$q$);
SELECT expect_error('a statement line has an amount', '23514', $q$INSERT INTO bank_statement_lines (tenant_id, property_id, statement_id, line_no, line_date, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_statements WHERE note = 'S1'), 2, '2026-10-06', 0)$q$);
INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), (SELECT id FROM bank_statement_lines WHERE statement_id = (SELECT id FROM bank_statements WHERE note = 'S1') AND line_no = 1), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number = 'JV000001' AND l.account_id = (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110')), 100);
SELECT expect_error('a journal line is cleared within its amount', '23514', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_number = 'JV000001' AND l.account_id = (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110')), 100)$q$);
SELECT expect_error('a clearing is of a statement of its bank account', '23503', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), 999999, (SELECT id FROM bank_statements WHERE note = 'S1'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), 1)$q$);
SELECT expect_error('a clearing has an amount', '23514', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), 0)$q$);
SELECT expect_error('a journal line is cleared on its side', '23514', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), -50)$q$);
SELECT expect_error('a statement line is cleared by a journal line once', '23505', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), (SELECT id FROM bank_statement_lines WHERE statement_id = (SELECT id FROM bank_statements WHERE note = 'S1') AND line_no = 1), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), 10)$q$);
SELECT expect_error('a credit line is cleared on its own side', '23514', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), 40)$q$);
SELECT expect_ok('a credit line is cleared in parts', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), -40)$q$, $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), -60)$q$);
SELECT expect_error('a credit line is not cleared for more than it holds', '23514', $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), -40)$q$, $q$INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), (SELECT id FROM bank_statements WHERE note = 'S1'), NULL, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), -70)$q$);
SELECT expect_ok('a card settlement pays the gross as net plus commission', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20)$q$);
SELECT expect_ok('a settlement without commission', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'OTHER_PAYMENT', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$);
SELECT expect_error('the net and the commission make the gross', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 30)$q$);
SELECT expect_error('a settlement is of card or e-wallet payments', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$);
SELECT expect_error('a settlement pays something', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 0, 0, 0)$q$);
SELECT expect_error('a settlement has its own journal', '23505', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$, $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$);
SELECT expect_ok('a settled line', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$, $q$INSERT INTO card_settlement_items (tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount) SELECT tn('ABC'), pr('BALI'), c.id, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), 100 FROM card_settlements c$q$);
SELECT expect_error('a line is settled once', '23505', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$, $q$INSERT INTO card_settlement_items (tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount) SELECT tn('ABC'), pr('BALI'), c.id, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), 100 FROM card_settlements c$q$, $q$INSERT INTO card_settlement_items (tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount) SELECT tn('ABC'), pr('BALI'), c.id, (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '1110'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id WHERE j.journal_number = 'JV000001' AND a.code = '4110'), 100 FROM card_settlements c$q$);
SELECT expect_error('settlements are append-only', '23001', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$, $q$UPDATE card_settlements SET net = 1$q$);
SELECT expect_error('settlements are not deleted', '23001', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 100, 100, 0)$q$, $q$DELETE FROM card_settlements$q$);
SELECT expect_error('settlement tables cannot be truncated', '23001', $q$TRUNCATE card_settlements CASCADE$q$);
SELECT expect_ok('an open statement is deleted with its lines and clearings', $q$DELETE FROM bank_clearings$q$, $q$DELETE FROM bank_statement_lines$q$, $q$DELETE FROM bank_statements$q$);
SELECT expect_error('a clearing cannot be updated', '23001', $q$UPDATE bank_clearings SET amount = 1$q$);
SELECT expect_error('a reconciled statement takes no more clearings', '23001', $q$UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = now() WHERE id = (SELECT id FROM bank_statements WHERE note = 'S1')$q$, $q$DELETE FROM bank_clearings$q$);
SELECT expect_error('a reconciled statement takes no more lines', '23001', $q$UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = now() WHERE id = (SELECT id FROM bank_statements WHERE note = 'S1')$q$, $q$INSERT INTO bank_statement_lines (tenant_id, property_id, statement_id, line_no, line_date, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_statements WHERE note = 'S1'), 9, '2026-10-06', 5)$q$);
SELECT expect_error('a reconciled statement is not deleted', '23001', $q$UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = now() WHERE id = (SELECT id FROM bank_statements WHERE note = 'S1')$q$, $q$DELETE FROM bank_statements$q$);
SELECT expect_error('a reconciled statement does not change', '23001', $q$UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = now() WHERE id = (SELECT id FROM bank_statements WHERE note = 'S1')$q$, $q$UPDATE bank_statements SET note = 'x'$q$);
SELECT expect_error('a statement keeps its figures', '23001', $q$UPDATE bank_statements SET closing_balance = 5$q$);
SELECT expect_ok('a reconciled statement can be reopened', $q$UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = now() WHERE id = (SELECT id FROM bank_statements WHERE note = 'S1')$q$, $q$UPDATE bank_statements SET status = 'OPEN', reconciled_at = NULL, reopen_reason = 'x'$q$);
SELECT expect_error('statement tables cannot be truncated', '23001', $q$TRUNCATE bank_statements CASCADE$q$);
SELECT expect_error('a bank journal type exists, an unknown one does not', '23514', $q$INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JVY', 'BANKING', '2026-10-01', 'x')$q$);

-- Tax filing
INSERT INTO tax_filing_profiles (tenant_id, property_id, tax_id, authority) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), 'Bapenda');
SELECT expect_error('a tax is filed with one profile', '23505', $q$INSERT INTO tax_filing_profiles (tenant_id, property_id, tax_id, authority) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), 'again')$q$);
SELECT expect_error('a profile is for a tax of its property', '23503', $q$INSERT INTO tax_filing_profiles (tenant_id, property_id, tax_id, authority) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM taxes WHERE property_id = pr('SG') ORDER BY id LIMIT 1), 'x')$q$);
SELECT expect_error('a due day within the month', '23514', $q$UPDATE tax_filing_profiles SET due_day = 31$q$);

-- PKP settings and the kind of a tax
SELECT expect_error('a tax is VAT, LOCAL or OTHER', '23514', $q$UPDATE taxes SET tax_kind = 'SALES'$q$);
SELECT expect_error('every property has its first tax status', '23505', $q$INSERT INTO property_tax_settings (tenant_id, property_id, effective_from) VALUES (tn('ABC'), pr('BALI'), '2000-01-01')$q$);
SELECT expect_error('a property that is not PKP cannot claim input VAT', '23514', $q$INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, is_pkp, input_vat_treatment) VALUES (tn('ABC'), pr('BALI'), '2027-01-01', false, 'CREDITABLE')$q$);
SELECT expect_error('a PKP property has its tax number', '23514', $q$INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, is_pkp, input_vat_treatment) VALUES (tn('ABC'), pr('BALI'), '2027-01-01', true, 'CREDITABLE')$q$);
SELECT expect_error('an input VAT treatment is one of three', '23514', $q$INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, input_vat_treatment) VALUES (tn('ABC'), pr('BALI'), '2027-01-01', 'SOMETIMES')$q$);
INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, is_pkp, npwp, input_vat_treatment) VALUES (tn('ABC'), pr('BALI'), '2027-01-01', true, '01.234.567.8-901.000', 'CREDITABLE');
SELECT expect_error('the tax status history is append-only', '23001', $q$UPDATE property_tax_settings SET is_pkp = false$q$);

-- The offset of the input VAT and the credit carried forward
SELECT expect_error('a return starts with the credit the month before carried', '23514',
    $q$INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on, credit_brought_forward)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9005', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2027-03-01', '2027-03-31', '2027-04-15', 100, 10, '2027-04-02', 5)$q$);
SELECT expect_error('an opening credit is above zero', '23514',
    $q$INSERT INTO tax_opening_credits (tenant_id, property_id, tax_id, as_of, amount, journal_id)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', 0, 1)$q$);
SELECT expect_error('a claim is positive and a reversal negative', '23514',
    $q$INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, bill_id, line_no, amount)
       VALUES (tn('ABC'), pr('BALI'), 1, 1, 1, -5)$q$);

-- Tax invoices
SELECT expect_error('a tax invoice is for exactly one source', '23514',
    $q$INSERT INTO tax_invoices (tenant_id, property_id, invoice_ref, issue_date, source_type, seller_name, seller_npwp, buyer_name, buyer_npwp, taxable_base, vat_amount)
       VALUES (tn('ABC'), pr('BALI'), 'TXI9001', '2026-10-01', 'FOLIO', 'Hotel', '012345678901000', 'Buyer', '023456789012000', 100, 10)$q$);
SELECT expect_error('the buyer has a tax number of 15 or 16 digits', '23514',
    $q$INSERT INTO tax_invoices (tenant_id, property_id, invoice_ref, issue_date, source_type, folio_id, seller_name, seller_npwp, buyer_name, buyer_npwp, taxable_base, vat_amount)
       VALUES (tn('ABC'), pr('BALI'), 'TXI9002', '2026-10-01', 'FOLIO', 1, 'Hotel', '012345678901000', 'Buyer', '12345', 100, 10)$q$);
SELECT expect_error('a tax invoice has VAT above zero', '23514',
    $q$INSERT INTO tax_invoices (tenant_id, property_id, invoice_ref, issue_date, source_type, folio_id, seller_name, seller_npwp, buyer_name, buyer_npwp, taxable_base, vat_amount)
       VALUES (tn('ABC'), pr('BALI'), 'TXI9003', '2026-10-01', 'FOLIO', 1, 'Hotel', '012345678901000', 'Buyer', '023456789012000', 100, 0)$q$);

-- Credit notes and write-offs of the city ledger
SELECT expect_error('a credit note is against an invoice or a transfer, not neither', '23514',
    $q$INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, amount, business_date, reason, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'CN9001', 'CREDIT_NOTE', 1, 100, '2026-10-01', 'x', 1)$q$);
SELECT expect_error('a write-off says the account that bears it', '23514',
    $q$INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, invoice_id, amount, business_date, reason, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'WO9001', 'WRITE_OFF', 1, 1, 100, '2026-10-01', 'x', 1)$q$);
SELECT expect_error('an adjustment has an amount above zero', '23514',
    $q$INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, invoice_id, amount, business_date, reason, journal_id)
       VALUES (tn('ABC'), pr('BALI'), 'CN9002', 'CREDIT_NOTE', 1, 1, 0, '2026-10-01', 'x', 1)$q$);
WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9001', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', '2026-10-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r;
SELECT expect_error('a return number is unique per property', '23505', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9001', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-11-01', '2026-11-30', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r$q$);
SELECT expect_error('a month of a tax is filed once', '23505', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9002', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', '2026-10-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r$q$);
SELECT expect_ok('a voided return makes room for a new one', $q$UPDATE tax_returns SET status = 'VOIDED', voided_at = now(), void_reason = 'x'$q$, $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9002', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-10-01', '2026-10-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r$q$);
SELECT expect_error('the lines of a return add up to its tax', '23514', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9003', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-12-01', '2026-12-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 90 FROM r$q$);
SELECT expect_error('a return starts on the first of a month', '23514', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9003', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-12-02', '2026-12-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r$q$);
SELECT expect_error('the tax of a return is not negative', '23514', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9003', (SELECT id FROM taxes WHERE property_id = pr('BALI') ORDER BY id LIMIT 1), '2026-12-01', '2026-12-31', '2026-11-15', 1000, -5, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, -5 FROM r$q$);
SELECT expect_error('a return is of a tax of its property', '23503', $q$WITH r AS (INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on)
       VALUES (tn('ABC'), pr('BALI'), 'TXR9003', (SELECT id FROM taxes WHERE property_id = pr('SG') ORDER BY id LIMIT 1), '2026-12-01', '2026-12-31', '2026-11-15', 1000, 100, '2026-11-02') RETURNING id)
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount)
SELECT tn('ABC'), pr('BALI'), r.id, 1, 'ROOM', 10, 3, 1000, 100 FROM r$q$);
SELECT expect_error('a voided return needs a reason', '23514', $q$UPDATE tax_returns SET status = 'VOIDED', voided_at = now()$q$);
SELECT expect_error('a return only goes from filed to voided', '23001', $q$UPDATE tax_returns SET tax_amount = 1$q$);
SELECT expect_error('returns are not deleted', '23001', $q$DELETE FROM tax_returns$q$);
SELECT expect_error('the worksheet of a return does not change', '23001', $q$UPDATE tax_return_lines SET tax_amount = 1$q$);
SELECT expect_error('returns cannot be truncated', '23001', $q$TRUNCATE tax_returns CASCADE$q$);
INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'TXP9001', (SELECT id FROM tax_returns WHERE return_number = 'TXR9001'), '2026-11-03', 50, 0, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'));
SELECT expect_error('a payment number is unique per property', '23505', $q$INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'TXP9001', (SELECT id FROM tax_returns WHERE return_number = 'TXR9001'), '2026-11-03', 50, 0, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a payment is above zero', '23514', $q$INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'TXP9002', (SELECT id FROM tax_returns WHERE return_number = 'TXR9001'), '2026-11-03', 0, 0, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a penalty is not negative', '23514', $q$INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'TXP9002', (SELECT id FROM tax_returns WHERE return_number = 'TXR9001'), '2026-11-03', 50, -1, 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a payment method of the list', '23514', $q$INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, journal_id) VALUES (tn('ABC'), pr('BALI'), 'TXP9002', (SELECT id FROM tax_returns WHERE return_number = 'TXR9001'), '2026-11-03', 50, 0, 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'))$q$);
SELECT expect_error('a payment only goes from posted to voided', '23001', $q$UPDATE tax_payments SET amount = 1$q$);
SELECT expect_error('payments are not deleted', '23001', $q$DELETE FROM tax_payments$q$);
SELECT expect_ok('a payment is voided with its reason', $q$UPDATE tax_payments SET status = 'VOIDED', voided_at = now(), void_reason = 'x'$q$);
SELECT expect_error('a tax journal type exists, an unknown one does not', '23514', $q$INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JVZ', 'TAXES', '2026-10-01', 'x')$q$);

-- Refund methods of a property
SELECT expect_ok('a property refunds in cash by default', $q$SELECT 1 FROM properties WHERE refund_methods = ARRAY['CASH']$q$);
SELECT expect_ok('a property may allow refunds by QRIS', $q$UPDATE properties SET refund_methods = ARRAY['CASH', 'QRIS']$q$);
SELECT expect_error('a property keeps at least one refund method', '23514', $q$UPDATE properties SET refund_methods = '{}'$q$);
SELECT expect_error('a refund method is one of the list', '23514', $q$UPDATE properties SET refund_methods = ARRAY['CASH', 'BITCOIN']$q$);
SELECT expect_error('a refund is not made on the city ledger', '23514', $q$UPDATE properties SET refund_methods = ARRAY['CITY_LEDGER']$q$);
SELECT expect_ok('a property can refund by several methods', $q$UPDATE properties SET refund_methods = ARRAY['CASH', 'BANK_TRANSFER']$q$);

-- Yield rules
INSERT INTO yield_rules (tenant_id, property_id, code, name, occupancy_from, adjustment_type, adjustment_value)
VALUES (tn('ABC'), pr('BALI'), 'BUSY', 'Busy nights', 70, 'PERCENT', 20);
SELECT expect_error('a yield rule code is unique per property', '23505', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'BUSY', 'again', 'PERCENT', 5)$q$);
SELECT expect_ok('the same code in another property', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('XYZ'), pr('SG'), 'BUSY', 'Busy nights', 'PERCENT', 5)$q$);
SELECT expect_error('an adjustment is a percentage or an amount', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 'FIXED', 5)$q$);
SELECT expect_error('an adjustment changes the price', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 'AMOUNT', 0)$q$);
SELECT expect_error('a percentage cannot take the price to nothing', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 'PERCENT', -100)$q$);
SELECT expect_error('a percentage is at most 1000', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 'PERCENT', 1000.5)$q$);
SELECT expect_error('stay dates are in order', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, stay_from, stay_to, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', '2026-12-01', '2026-11-01', 'PERCENT', 5)$q$);
SELECT expect_error('an occupancy range is in order', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, occupancy_from, occupancy_to, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 80, 60, 'PERCENT', 5)$q$);
SELECT expect_error('an occupancy is at most 100', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, occupancy_to, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 101, 'PERCENT', 5)$q$);
SELECT expect_error('lead days are in order', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, lead_min, lead_max, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 5, 2, 'PERCENT', 5)$q$);
SELECT expect_error('a stay is at least one night', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, stay_min, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 0, 'PERCENT', 5)$q$);
SELECT expect_error('weekdays are of the list', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, weekdays, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', ARRAY['FUNDAY'], 'PERCENT', 5)$q$);
SELECT expect_error('a floor is not above the cap', '23514', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, floor_amount, cap_amount, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 900, 800, 'PERCENT', 5)$q$);
SELECT expect_error('a rule names a rate plan of its property', '23503', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, rate_plan_id, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 999999, 'PERCENT', 5)$q$);
SELECT expect_error('a rule names a room type of its property', '23503', $q$INSERT INTO yield_rules (tenant_id, property_id, code, name, room_type_id, adjustment_type, adjustment_value) VALUES (tn('ABC'), pr('BALI'), 'Y1', 'x', 999999, 'PERCENT', 5)$q$);

------------------------------------------------------------------------------------------
-- City ledger reminders and the late fee
------------------------------------------------------------------------------------------
SELECT expect_error('a reminder is of level 1 to 3', '23514',
    $q$INSERT INTO city_ledger_reminders (tenant_id, property_id, reminder_number, company_id, level, reminder_date, total_outstanding)
       VALUES (tn('ABC'), pr('BALI'), 'REM9001', 1, 4, '2026-10-01', 100)$q$);
SELECT expect_error('a reminder asks something that is owed', '23514',
    $q$INSERT INTO city_ledger_reminders (tenant_id, property_id, reminder_number, company_id, level, reminder_date, total_outstanding)
       VALUES (tn('ABC'), pr('BALI'), 'REM9002', 1, 1, '2026-10-01', 0)$q$);
SELECT expect_error('a late fee is a percent from 0 to 100', '23514',
    $q$INSERT INTO city_ledger_late_fee_settings (tenant_id, property_id, monthly_rate) VALUES (tn('ABC'), pr('BALI'), 101)$q$);
SELECT expect_error('the days of grace are at most 365', '23514',
    $q$INSERT INTO city_ledger_late_fee_settings (tenant_id, property_id, monthly_rate, grace_days) VALUES (tn('ABC'), pr('BALI'), 3, 400)$q$);

------------------------------------------------------------------------------------------
-- Cashier shifts
------------------------------------------------------------------------------------------
INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened, opening_float)
VALUES (tn('ABC'), pr('BALI'), 'SHF9001', (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')), 'FRONT', now(), '2026-10-01', 100000);
SELECT expect_error('a shift number is unique per property', '23505',
    $q$INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened) VALUES (tn('ABC'), pr('BALI'), 'SHF9001', (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')), 'BACK', now(), '2026-10-01')$q$);
SELECT expect_error('one shift is open per cashier', '23505',
    $q$INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened) VALUES (tn('ABC'), pr('BALI'), 'SHF9002', (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')), 'BACK', now(), '2026-10-01')$q$);
SELECT expect_error('one shift is open per drawer', '23505',
    $q$INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened) VALUES (tn('ABC'), pr('BALI'), 'SHF9003', (SELECT max(id) FROM users WHERE tenant_id = tn('ABC')) + 0, 'FRONT', now(), '2026-10-01')$q$);
SELECT expect_error('a float is not negative', '23514',
    $q$INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened, opening_float) VALUES (tn('ABC'), pr('BALI'), 'SHF9004', (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')), 'X', now(), '2026-10-01', -1)$q$);
SELECT expect_error('a closed shift has its count', '23514',
    $q$UPDATE cashier_shifts SET status = 'CLOSED' WHERE shift_number = 'SHF9001'$q$);
SELECT expect_error('a shift is never deleted', '23001', $q$DELETE FROM cashier_shifts WHERE shift_number = 'SHF9001'$q$);
SELECT expect_error('a movement is above zero', '23514',
    $q$INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, reason, business_date) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM cashier_shifts WHERE shift_number = 'SHF9001'), 'DROP', 0, 'x', '2026-10-01')$q$);
SELECT expect_error('a pay-in has an account and a journal', '23514',
    $q$INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, reason, business_date) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM cashier_shifts WHERE shift_number = 'SHF9001'), 'PAY_IN', 5, 'x', '2026-10-01')$q$);
INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, reason, business_date)
VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM cashier_shifts WHERE shift_number = 'SHF9001'), 'DROP', 5000, 'to the safe', '2026-10-01');
SELECT expect_error('movements are append-only', '23001', $q$UPDATE cashier_shift_movements SET amount = 1$q$);
SELECT expect_error('the variance limit is not negative', '23514', $q$UPDATE property_cashier_settings SET max_variance = -1$q$);

------------------------------------------------------------------------------------------
-- Card fee rules
------------------------------------------------------------------------------------------
INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'CARD', 2, 1, '2026-09-01');
SELECT expect_error('a rule starts once per method and date', '23505',
    $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'CARD', 3, 1, '2026-09-01')$q$);
SELECT expect_error('a rule is for card or e-wallet', '23514',
    $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'CASH', 3, 1, '2026-09-02')$q$);
SELECT expect_error('a rate is a percent', '23514',
    $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'CARD', 101, 1, '2026-09-03')$q$);
SELECT expect_error('the days to pay out are at most 60', '23514',
    $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'CARD', 2, 61, '2026-09-04')$q$);
SELECT expect_ok('a rule takes the VAT rate of the commission', $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, vat_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'OTHER', 0.7, 11, 1, '2026-09-05')$q$);
SELECT expect_error('the VAT rate of a rule is a percentage', '23514',
    $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, vat_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'OTHER', 0.7, 101, 1, '2026-09-06')$q$);
-- The VAT of the commission of a settlement (00056): the identity fee = MDR + VAT, the treatment frozen with the VAT, the claim of a settlement
SELECT expect_ok('a fee rule for QRIS, with the VAT of its commission', $q$INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, vat_rate, settlement_days, effective_from) VALUES (tn('ABC'), pr('BALI'), 'QRIS', 0.7, 11, 1, '2026-09-07')$q$);
SELECT expect_ok('a QRIS settlement pays the gross as net plus commission', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'QRIS', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 993, 7)$q$);
SELECT expect_error('a settlement clears a clearing account only', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CASH', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20)$q$);
SELECT expect_ok('a settlement with the VAT of its commission', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_amount, vat_treatment, expected_mdr, expected_vat, proposed_vat, mdr_rate, vat_rate)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 978, 22, 2, 'CREDITABLE', 20, 2, 2, 2, 11)$q$);
SELECT expect_error('the VAT of a settlement is not more than the deduction', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_amount, vat_treatment)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 21, 'CREDITABLE')$q$);
SELECT expect_error('VAT needs its treatment', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_amount)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 2)$q$);
SELECT expect_error('a treatment needs VAT', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_treatment)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 'EXPENSE')$q$);
SELECT expect_error('the treatment is one of three', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_amount, vat_treatment)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 2, 'MAYBE')$q$);
SELECT expect_error('an expected VAT needs an expected MDR', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, expected_vat)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 2)$q$);
SELECT expect_error('the rates of a settlement are percentages', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, vat_rate)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 101)$q$);
SELECT expect_error('the proposed VAT is not more than the deduction', '23514', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, proposed_vat)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 21)$q$);
SELECT expect_error('the MDR of a settlement is generated', '428C9', $q$INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, mdr_amount)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM bank_accounts WHERE property_id = pr('BALI') AND name = 'Main bank'), 'CARD', (SELECT id FROM gl_journals WHERE journal_number = 'JV000001'), 1000, 980, 20, 20)$q$);
SELECT expect_error('a claim of a settlement has no reversal', '23514', $q$INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, settlement_id, amount, reverses_claim_id)
    VALUES (tn('ABC'), pr('BALI'), 1, 999999, -5, 999999)$q$);
SELECT expect_error('a claim names one source', '23514', $q$INSERT INTO tax_return_input_claims (tenant_id, property_id, return_id, bill_id, line_no, settlement_id, amount)
    VALUES (tn('ABC'), pr('BALI'), 1, 1, 1, 999999, 5)$q$);
SELECT expect_error('a rule is never changed', '23001', $q$UPDATE card_fee_rules SET mdr_rate = 1$q$);
SELECT expect_error('a rule is never deleted', '23001', $q$DELETE FROM card_fee_rules$q$);

------------------------------------------------------------------------------------------
-- Budgets
------------------------------------------------------------------------------------------
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('ABC'), pr('BALI'), '6110', 'Salaries', 'EXPENSE', 'DEBIT', 'UND_AG');
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, is_postable) VALUES (tn('ABC'), pr('BALI'), '6000', 'Expenses', 'EXPENSE', 'DEBIT', false);
INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('ABC'), pr('BALI'), '2026-01-01', 1, 'Budget 2026');
SELECT expect_error('a budget version is unique per year', '23505',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('ABC'), pr('BALI'), '2026-01-01', 1, 'Again')$q$);
SELECT expect_error('a fiscal year starts on the first of a month', '23514',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('ABC'), pr('BALI'), '2026-01-15', 2, 'Odd')$q$);
SELECT expect_error('a budget has a name', '23514',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('ABC'), pr('BALI'), '2026-01-01', 2, '  ')$q$);
SELECT expect_error('a budget status is known', '23514',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name, status) VALUES (tn('ABC'), pr('BALI'), '2026-01-01', 2, 'X', 'OPEN')$q$);
SELECT expect_error('a budget of another tenant property is impossible', '23503',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('XYZ'), pr('BALI'), '2026-01-01', 3, 'Cross')$q$);
SELECT expect_error('an active budget has its approval', '23514',
    $q$UPDATE budgets SET status = 'ACTIVE', activated_at = now() WHERE name = 'Budget 2026'$q$);
INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), 1, 1000000);
SELECT expect_error('an account has one figure per month', '23505',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), 1, 5)$q$);
SELECT expect_error('a month is from 1 to 12', '23514',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), 13, 5)$q$);
SELECT expect_error('a budget does not cover a balance sheet account', '23514',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '1110' AND property_id = pr('BALI')), 1, 5)$q$);
SELECT expect_error('a budget does not cover a header account', '23514',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '6000' AND property_id = pr('BALI')), 1, 5)$q$);
SELECT expect_error('a budget line of an account of another property is impossible', '23503',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), (SELECT id FROM gl_accounts WHERE code = '1110' AND property_id = pr('SG')), 1, 5)$q$);
UPDATE budgets SET status = 'ACTIVE', activated_at = now(), approved_by = (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')) WHERE name = 'Budget 2026';
INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('ABC'), pr('BALI'), '2026-01-01', 2, 'Budget 2026 v2');
SELECT expect_error('one version is active per year', '23505',
    $q$UPDATE budgets SET status = 'ACTIVE', activated_at = now(), approved_by = (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')) WHERE name = 'Budget 2026 v2'$q$);
SELECT expect_error('an active budget is not edited', '23001',
    $q$UPDATE budgets SET name = 'Renamed' WHERE name = 'Budget 2026'$q$);
SELECT expect_error('an active budget is not deleted', '23001', $q$DELETE FROM budgets WHERE name = 'Budget 2026'$q$);
SELECT expect_error('the lines of an active budget do not change', '23001',
    $q$UPDATE budget_lines SET amount = 1 WHERE budget_id = (SELECT id FROM budgets WHERE name = 'Budget 2026')$q$);
SELECT expect_error('the lines of an active budget are not deleted', '23001',
    $q$DELETE FROM budget_lines WHERE budget_id = (SELECT id FROM budgets WHERE name = 'Budget 2026')$q$);
SELECT expect_error('the year of a budget never changes', '23001',
    $q$UPDATE budgets SET year_start = '2027-01-01' WHERE name = 'Budget 2026 v2'$q$);
SELECT expect_error('an active budget does not go back to draft', '23001',
    $q$UPDATE budgets SET status = 'DRAFT', activated_at = NULL, approved_by = NULL WHERE name = 'Budget 2026'$q$);
SELECT expect_ok('an active budget is archived when another takes its place',
    $q$UPDATE budgets SET status = 'ARCHIVED', archived_at = now() WHERE name = 'Budget 2026'$q$,
    $q$UPDATE budgets SET status = 'ACTIVE', activated_at = now(), approved_by = (SELECT min(id) FROM users WHERE tenant_id = tn('ABC')) WHERE name = 'Budget 2026 v2'$q$);
SELECT expect_ok('a draft is deleted with its lines',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '6110' AND property_id = pr('BALI')), 2, 250000)$q$,
    $q$DELETE FROM budgets WHERE name = 'Budget 2026 v2' AND status = 'DRAFT'$q$);

INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr)
VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 9, 300, 210, 1000000);
SELECT expect_error('a month has one set of statistics', '23505',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 9, 10, 5, 1)$q$);
SELECT expect_error('the statistics month is from 1 to 12', '23514',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 13, 10, 5, 1)$q$);
SELECT expect_error('the rooms sold are not above the rooms available', '23514',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 10, 10, 11, 1)$q$);
SELECT expect_error('the ADR is not negative', '23514',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 10, 10, 5, -1)$q$);
SELECT expect_error('statistics of a budget of another property are impossible', '23503',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('XYZ'), pr('SG'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), 10, 10, 5, 1)$q$);
SELECT expect_error('the statistics of an active budget are not added', '23001',
    $q$INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026'), 10, 10, 5, 1)$q$);
SELECT expect_ok('a draft is deleted with its statistics',
    $q$DELETE FROM budgets WHERE name = 'Budget 2026 v2' AND status = 'DRAFT'$q$);

------------------------------------------------------------------------------------------
-- Departments
------------------------------------------------------------------------------------------
INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'FB', 'Food and beverage');
INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'ROOMS', 'Rooms');
INSERT INTO departments (tenant_id, property_id, parent_id, code, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('BALI')), 'REST', 'Restaurant');
INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('XYZ'), pr('SG'), 'FB', 'Food and beverage');
SELECT expect_error('a department code is unique per property', '23505',
    $q$INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'FB', 'Again')$q$);
SELECT expect_error('a department code has a shape', '23514',
    $q$INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'no way', 'x')$q$);
SELECT expect_error('a department has a name', '23514',
    $q$INSERT INTO departments (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'X1', '  ')$q$);
SELECT expect_error('a sub-department has no sub-departments', '23514',
    $q$INSERT INTO departments (tenant_id, property_id, parent_id, code, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM departments WHERE code = 'REST' AND property_id = pr('BALI')), 'PIZZA', 'Pizza')$q$);
SELECT expect_error('a parent of another property is impossible', '23503',
    $q$INSERT INTO departments (tenant_id, property_id, parent_id, code, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('SG')), 'BAR', 'Bar')$q$);
SELECT expect_error('the code of a department never changes', '23001', $q$UPDATE departments SET code = 'FNB' WHERE code = 'FB' AND property_id = pr('BALI')$q$);
SELECT expect_error('the parent of a department never changes', '23001',
    $q$UPDATE departments SET parent_id = (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')) WHERE code = 'REST' AND property_id = pr('BALI')$q$);
SELECT expect_error('a department does not become a sub-department', '23001',
    $q$UPDATE departments SET parent_id = (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('BALI')) WHERE code = 'ROOMS' AND property_id = pr('BALI')$q$);
SELECT expect_ok('a department is renamed and switched off', $q$UPDATE departments SET name = 'F and B', is_active = false WHERE code = 'REST' AND property_id = pr('BALI')$q$);
SELECT expect_error('a department of another property cannot be put on a charge code', '23503',
    $q$UPDATE charge_codes SET department_id = (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('SG')) WHERE property_id = pr('BALI') AND code = 'ROOM'$q$);
UPDATE charge_codes SET department_id = (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')) WHERE property_id = pr('BALI') AND code = 'ROOM';
SELECT expect_error('a department a charge code points to is not deleted', '23503', $q$DELETE FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')$q$);
SELECT expect_error('a department with sub-departments is not deleted', '23503', $q$DELETE FROM departments WHERE code = 'FB' AND property_id = pr('BALI')$q$);
SELECT expect_ok('a department nothing points to is deleted', $q$DELETE FROM departments WHERE code = 'REST' AND property_id = pr('BALI')$q$);

-- The budget by department: a line is an account, a department (or none) and a month.
INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, department_id, month, amount)
VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')),
        (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('BALI')), 1, 100);
INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), 1, 50);
SELECT expect_error('a department has one figure per account and month', '23505',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, department_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('BALI')), 1, 5)$q$);
SELECT expect_error('no department is one department of its own', '23505',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), 1, 7)$q$);
SELECT expect_ok('the same account and month for another department',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, department_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')), 1, 5)$q$);
SELECT expect_error('the department of a budget line is one of the property', '23503',
    $q$INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, department_id, month, amount) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM budgets WHERE name = 'Budget 2026 v2'), (SELECT id FROM gl_accounts WHERE code = '4110' AND property_id = pr('BALI')), (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('SG')), 2, 5)$q$);
SELECT expect_error('the department of a budget line never changes', '23001',
    $q$UPDATE budget_lines SET department_id = (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')) WHERE department_id = (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('BALI')) AND month = 1$q$);
SELECT expect_error('a department a budget line points to is not deleted', '23503', $q$DELETE FROM departments WHERE code = 'FB' AND property_id = pr('BALI')$q$);

-- The department rule of an account (00054)
SELECT expect_error('the department requirement is NONE, OPTIONAL or REQUIRED', '23514',
    $q$UPDATE gl_accounts SET department_requirement = 'MAYBE' WHERE code = '1110' AND property_id = pr('BALI')$q$);
SELECT expect_ok('an account may require a department and have a default',
    $q$UPDATE gl_accounts SET department_requirement = 'REQUIRED', default_department_id = (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')) WHERE code = '1110' AND property_id = pr('BALI')$q$);
SELECT expect_error('an account that takes no department has no default', '23514',
    $q$UPDATE gl_accounts SET department_requirement = 'NONE', default_department_id = (SELECT id FROM departments WHERE code = 'ROOMS' AND property_id = pr('BALI')) WHERE code = '1110' AND property_id = pr('BALI')$q$);
SELECT expect_error('the default department of an account is one of the property', '23503',
    $q$UPDATE gl_accounts SET default_department_id = (SELECT id FROM departments WHERE code = 'FB' AND property_id = pr('SG')) WHERE code = '1110' AND property_id = pr('BALI')$q$);

------------------------------------------------------------------------------------------
-- Audit log
------------------------------------------------------------------------------------------
INSERT INTO audit_logs (tenant_id, property_id, business_date, action, entity_type, entity_id, new_data)
VALUES (tn('ABC'), pr('BALI'), '2026-10-01', 'stay.checked_in', 'stay', st('S1'), '{"room": "201"}');
SELECT expect_error('audit log is append-only', '23001',
    $q$UPDATE audit_logs SET action = 'x'$q$);
-- The label of an entry (00066): the identifier of what it is about, cut by the writer at 120 characters (the database refuses a longer one), found by a part of it through the trigram index.
SELECT expect_ok('an entry keeps the label of what it is about',
    $q$INSERT INTO audit_logs (tenant_id, property_id, business_date, action, entity_type, entity_id, entity_label, new_data)
       VALUES (tn('ABC'), pr('BALI'), '2026-10-01', 'reservation.created', 'reservation', 1, 'RES000012', '{}')$q$);
SELECT expect_error('a label longer than 120 characters is refused', '22001',
    $q$INSERT INTO audit_logs (tenant_id, property_id, action, entity_type, entity_id, entity_label) VALUES (tn('ABC'), pr('BALI'), 'x', 'reservation', 1, repeat('a', 121))$q$);
SELECT expect_ok('a label is found by a part of it, in any case, and the trigram index is there',
    $q$DO $d$ BEGIN
        INSERT INTO audit_logs (tenant_id, property_id, action, entity_type, entity_id, entity_label) VALUES (tn('ABC'), pr('BALI'), 'x', 'reservation', 1, 'RES000012');
        IF NOT EXISTS (SELECT 1 FROM audit_logs WHERE entity_label ILIKE '%es0000%') THEN RAISE EXCEPTION 'the label was not found'; END IF;
        IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'audit_logs_label_trgm_idx' AND indexdef LIKE '%gin_trgm_ops%') THEN RAISE EXCEPTION 'the trigram index is missing'; END IF;
    END $d$$q$);
SELECT expect_error('the label of an entry cannot be changed afterwards', '23001',
    $q$UPDATE audit_logs SET entity_label = 'x'$q$);

------------------------------------------------------------------------------------------
-- A stay with a folio for each payer (00060)
------------------------------------------------------------------------------------------
SELECT expect_ok('a stay has a guest folio and a company folio',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_ok('a stay has a folio for each company',
    $q$INSERT INTO companies (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'OTHER', 'Other Corp')$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC2', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'OTHER'))$q$);
SELECT expect_error('a company has one folio of a stay', '23505',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC2', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('a company folio names its company', '23514',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'), 'COMPANY')$q$);
SELECT expect_error('a guest folio names no company', '23514',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S2'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('the company of a folio is one of the property', '23503',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('SG') AND code = 'ACME'))$q$);
SELECT expect_error('a master folio is not built yet', '23514',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), NULL, 'MASTER')$q$);
SELECT expect_error('there is still one guest folio per stay', '23505',
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id) VALUES (tn('ABC'), pr('BALI'), 'FC1', rs('R1'), st('S1'))$q$);

------------------------------------------------------------------------------------------
-- Billing instructions of a reservation line (00061)
------------------------------------------------------------------------------------------
SELECT expect_ok('an instruction names a company and a scope',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ALL', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ROOM', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'CHARGE_CODE', cc('ROOM_EXEMPT'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'CHARGE_CODE', cc('LAUNDRY'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('one instruction for each scope of a line', '23505',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ALL', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ALL', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('one instruction for each charge code of a line', '23505',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'CHARGE_CODE', cc('ROOM_EXEMPT'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$,
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'CHARGE_CODE', cc('ROOM_EXEMPT'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('a charge code instruction names its code', '23514',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'CHARGE_CODE', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('only a charge code instruction names a code', '23514',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ROOM', cc('ROOM_EXEMPT'), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('a scope is one of three', '23514',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'EVERYTHING', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_error('the company of an instruction is one of the property', '23503',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('ABC'), pr('BALI'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ALL', (SELECT id FROM companies WHERE property_id = pr('SG') AND code = 'ACME'))$q$);
SELECT expect_error('the line of an instruction is one of the property', '23503',
    $q$INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, company_id) VALUES (tn('XYZ'), pr('SG'), (SELECT min(id) FROM reservation_rooms WHERE reservation_id = rs('R1')), 'ALL', (SELECT id FROM companies WHERE property_id = pr('SG') AND code = 'ACME'))$q$);

------------------------------------------------------------------------------------------
-- Transaction groups (00063): a presentation dimension of a folio, in a table of its own so that the append-only ledger is not touched
------------------------------------------------------------------------------------------
SELECT expect_ok('a ledger line can be put in a group, and moved to another: the group table is not append-only',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'B')$q$,
    $q$UPDATE folio_item_groups SET group_code = 'C' WHERE folio_item_id = (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001')$q$);
SELECT expect_error('a line has one group', '23505',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'B')$q$,
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'D')$q$);
SELECT expect_error('a group is one capital letter (lower case)', '23514', $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'b')$q$);
SELECT expect_error('a group is one capital letter (two letters)', '23514', $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'AB')$q$);
SELECT expect_error('a group is one capital letter (a digit)', '23514', $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), '1')$q$);
SELECT expect_error('a group is not empty', '23514', $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), '')$q$);
SELECT expect_error('moving a line to a bad group is refused too', '23514',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'B')$q$,
    $q$UPDATE folio_item_groups SET group_code = 'x'$q$);
SELECT expect_error('the line of a group is one of the property', '23503',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('XYZ'), pr('SG'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'B')$q$);
SELECT expect_error('the property of a group belongs to its tenant', '23503',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('XYZ'), pr('BALI'), (SELECT id FROM folio_items WHERE idempotency_key = 'RC-S1-1001'), 'B')$q$);
SELECT expect_error('a group of a line that does not exist', '23503',
    $q$INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code) VALUES (tn('ABC'), pr('BALI'), 999999999, 'B')$q$);
SELECT expect_error('the ledger line itself is still append-only', '23001',
    $q$UPDATE folio_items SET description = 'x' WHERE idempotency_key = 'RC-S1-1001'$q$);

------------------------------------------------------------------------------------------
-- A cancelled stay has no open folio (00064, audit F-06)
------------------------------------------------------------------------------------------
SELECT expect_error('a stay cannot be cancelled while a folio of it is open', '23514',
    $q$UPDATE stays SET status = 'CANCELLED' WHERE id = st('S1')$q$);
SELECT expect_ok('a stay is cancelled once its folio is unlinked (the guest folio goes back to being the deposit folio of the reservation) or closed',
    $q$UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE folio_number = 'F4'$q$, -- the reservation has one open folio without a stay
    $q$UPDATE folios SET stay_id = NULL WHERE folio_number = 'F1'$q$,
    $q$UPDATE stays SET status = 'CANCELLED' WHERE id = st('S1')$q$);
SELECT expect_ok('or closed, and stays linked to the cancelled stay as history',
    $q$UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE folio_number = 'F1'$q$,
    $q$UPDATE stays SET status = 'CANCELLED' WHERE id = st('S1')$q$);
SELECT expect_error('a folio of a cancelled stay cannot be opened again',
    '23514',
    $q$UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE folio_number = 'F1'$q$,
    $q$UPDATE stays SET status = 'CANCELLED' WHERE id = st('S1')$q$,
    $q$UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE folio_number = 'F1'$q$);
SELECT expect_error('nor can a new open folio be linked to a cancelled stay', '23514',
    $q$UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE folio_number = 'F1'$q$,
    $q$UPDATE stays SET status = 'CANCELLED' WHERE id = st('S1')$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'F9', rs('R1'), st('S1'), 'COMPANY', (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_ok('an open folio of an open stay is as before',
    $q$UPDATE folios SET stay_id = st('S1') WHERE folio_number = 'F1'$q$);

------------------------------------------------------------------------------------------
-- Sales restrictions (00059)
------------------------------------------------------------------------------------------
SELECT expect_ok('the four scopes of one date live side by side: the property, a plan, a room type, a room type and a plan',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, stop_sell) VALUES (tn('ABC'), pr('BALI'), NULL, NULL, '2026-12-24', true)$q$,
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, min_stay) VALUES (tn('ABC'), pr('BALI'), NULL, rp('BAR'), '2026-12-24', 2)$q$,
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, closed_to_arrival) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), NULL, '2026-12-24', true)$q$,
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, stop_sell, max_stay) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), rp('BAR'), '2026-12-24', false, 7)$q$);
SELECT expect_ok('an explicit FALSE is a restriction row of its own (it opens what a wider row closes)',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, closed_to_departure) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), rp('BAR'), '2026-12-25', false)$q$);
SELECT expect_error('a restriction says something', '23514',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), NULL, '2026-12-24')$q$);
SELECT expect_error('the minimum stay is at least one night', '23514',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, min_stay) VALUES (tn('ABC'), pr('BALI'), '2026-12-24', 0)$q$);
SELECT expect_error('the maximum stay is at most 365 nights', '23514',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, max_stay) VALUES (tn('ABC'), pr('BALI'), '2026-12-24', 366)$q$);
SELECT expect_error('the minimum stay is not above the maximum', '23514',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, min_stay, max_stay) VALUES (tn('ABC'), pr('BALI'), '2026-12-24', 5, 3)$q$);
SELECT expect_error('a scope has one row a date (the whole property)', '23505',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, stop_sell) VALUES (tn('ABC'), pr('BALI'), '2026-12-24', true)$q$,
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, closed_to_arrival) VALUES (tn('ABC'), pr('BALI'), '2026-12-24', true)$q$);
SELECT expect_error('a scope has one row a date (a room type and a plan)', '23505',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, stop_sell) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), rp('BAR'), '2026-12-24', true)$q$,
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, rate_plan_id, stay_date, min_stay) VALUES (tn('ABC'), pr('BALI'), rt('DLX'), rp('BAR'), '2026-12-24', 2)$q$);
SELECT expect_error('the room type is one of the property', '23503',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, stay_date, stop_sell) VALUES (tn('XYZ'), pr('SG'), rt('DLX'), '2026-12-24', true)$q$);
SELECT expect_error('the rate plan is one of the property', '23503',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, rate_plan_id, stay_date, stop_sell) VALUES (tn('XYZ'), pr('SG'), rp('BAR'), '2026-12-24', true)$q$);
SELECT expect_error('the property is one of the tenant', '23503',
    $q$INSERT INTO rate_restrictions (tenant_id, property_id, stay_date, stop_sell) VALUES (tn('XYZ'), pr('BALI'), '2026-12-24', true)$q$);

------------------------------------------------------------------------------------------
-- The currency lock (00058): one definition of "the property has financial data"
------------------------------------------------------------------------------------------
-- A property of its own, so that these cases start from nothing. Every case runs in a subtransaction that is rolled back.
INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
VALUES (tn('XYZ'), 'CUR', 'Currency test', 'Asia/Singapore', 'SGD', 2, '15:00', '11:00');
CREATE FUNCTION cu() RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM properties WHERE code = 'CUR' $$;

SELECT expect_ok('a property with no financial data can change its currency and decimals',
    $q$UPDATE properties SET currency_code = 'IDR', currency_decimals = 0 WHERE code = 'CUR'$q$);
SELECT expect_ok('the definition is false for a property with nothing',
    $q$DO $d$ BEGIN IF property_has_financial_data(cu()) THEN RAISE EXCEPTION 'expected false'; END IF; END $d$$q$);
SELECT expect_ok('configuration does not lock the currency: accounts, a bank account, a company',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), cu(), '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH')$q$,
    $q$INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES (tn('XYZ'), cu(), (SELECT id FROM gl_accounts WHERE property_id = cu() AND code = '1110'), 'Bank')$q$,
    $q$INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES (tn('XYZ'), cu(), 'ACME', 'Acme', 1000000)$q$,
    $q$UPDATE properties SET currency_code = 'IDR', currency_decimals = 0 WHERE code = 'CUR'$q$);
SELECT expect_error('a journal locks the currency', '23001',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), cu(), '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH')$q$,
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), cu(), '3100', 'Capital', 'EQUITY', 'CREDIT', 'EQUITY')$q$,
    $q$INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('XYZ'), cu(), 'J1', 'MANUAL', '2026-10-01', 'Opening')$q$,
    $q$INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) SELECT tn('XYZ'), cu(), (SELECT id FROM gl_journals WHERE property_id = cu()), 1, id, 100, 0 FROM gl_accounts WHERE property_id = cu() AND code = '1110'$q$,
    $q$INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) SELECT tn('XYZ'), cu(), (SELECT id FROM gl_journals WHERE property_id = cu()), 2, id, 0, 100 FROM gl_accounts WHERE property_id = cu() AND code = '3100'$q$,
    $q$UPDATE properties SET currency_decimals = 3 WHERE code = 'CUR'$q$);
SELECT expect_error('bank data locks the currency (a statement)', '23001',
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), cu(), '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH')$q$,
    $q$INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES (tn('XYZ'), cu(), (SELECT id FROM gl_accounts WHERE property_id = cu() AND code = '1110'), 'Bank')$q$,
    $q$INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance) VALUES (tn('XYZ'), cu(), (SELECT id FROM bank_accounts WHERE property_id = cu()), '2026-10-01', '2026-10-31', 0, 0)$q$,
    $q$UPDATE properties SET currency_code = 'USD' WHERE code = 'CUR'$q$);
SELECT expect_error('a city ledger receipt locks the currency', '23001',
    $q$INSERT INTO business_days (tenant_id, property_id, business_date) VALUES (tn('XYZ'), cu(), '2026-10-01')$q$,
    $q$INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES (tn('XYZ'), cu(), 'ACME', 'Acme', 1000000)$q$,
    $q$INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date) VALUES (tn('XYZ'), cu(), 'R1', (SELECT id FROM companies WHERE property_id = cu()), 10, 'CASH', '2026-10-01')$q$,
    $q$UPDATE properties SET currency_code = 'USD' WHERE code = 'CUR'$q$);
SELECT expect_error('a budget locks the currency', '23001',
    $q$INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES (tn('XYZ'), cu(), '2027-01-01', 1, 'Budget')$q$,
    $q$UPDATE properties SET currency_decimals = 0 WHERE code = 'CUR'$q$);
SELECT expect_error('a cashier shift locks the currency', '23001',
    $q$INSERT INTO business_days (tenant_id, property_id, business_date) VALUES (tn('XYZ'), cu(), '2026-10-01')$q$,
    $q$INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, opened_at, business_date_opened) VALUES (tn('XYZ'), cu(), 'S1', us('XYZ', 'admin@hotel.com'), now(), '2026-10-01')$q$,
    $q$UPDATE properties SET currency_decimals = 0 WHERE code = 'CUR'$q$);
SELECT expect_error('a property with folio items stays locked (BALI)', '23001',
    $q$UPDATE properties SET currency_decimals = 2 WHERE code = 'BALI'$q$);
SELECT expect_ok('an update that leaves the currency as it is passes even for a locked property',
    $q$UPDATE properties SET currency_code = currency_code, currency_decimals = currency_decimals, name = 'Hotel Bali' WHERE code = 'BALI'$q$);

-- Every table that holds an amount of money recorded in the currency is either one the definition looks at, or explained here. A new table with a money
-- column breaks this test until it is classified: that is how a table cannot be forgotten.
CREATE TABLE pms_test.currency_lock_classes (table_name text PRIMARY KEY, class text NOT NULL, why text NOT NULL);
INSERT INTO pms_test.currency_lock_classes VALUES
    ('folio_items',             'ROOT',   'the guest ledger'),
    ('payments',                'ROOT',   'payments'),
    ('gl_journals',             'ROOT',   'the general ledger'),
    ('supplier_bills',          'ROOT',   'payables'),
    ('supplier_payments',       'ROOT',   'payables'),
    ('supplier_credit_notes',   'ROOT',   'payables'),
    ('city_ledger_receipts',    'ROOT',   'receivables'),
    ('city_ledger_invoices',    'ROOT',   'receivables'),
    ('city_ledger_adjustments', 'ROOT',   'receivables'),
    ('cashier_shifts',          'ROOT',   'the cash drawer'),
    ('bank_statements',         'ROOT',   'the bank'),
    ('tax_returns',             'ROOT',   'tax'),
    ('tax_payments',            'ROOT',   'tax'),
    ('tax_opening_credits',     'ROOT',   'tax'),
    ('budgets',                 'ROOT',   'budgets'),
    ('inv_movements',           'ROOT',   'the stock ledger'),
    ('fa_assets',               'ROOT',   'the fixed asset register'),
    ('fa_ledger',               'ROOT',   'the fixed asset register ledger');
-- A child row cannot exist without a root row above: it never needs its own test.
INSERT INTO pms_test.currency_lock_classes VALUES
    ('folio_item_components', 'CHILD', 'of folio_items'), ('stay_charge_postings', 'CHILD', 'of folio_items'),
    ('gl_journal_lines', 'CHILD', 'of gl_journals'), ('gl_day_posts', 'CHILD', 'of gl_journals'),
    ('supplier_bill_lines', 'CHILD', 'of supplier_bills'), ('supplier_payment_allocations', 'CHILD', 'of supplier_payments'),
    ('supplier_credit_note_lines', 'CHILD', 'of supplier_credit_notes'), ('supplier_credit_allocations', 'CHILD', 'of supplier_credit_notes'),
    ('city_ledger_invoice_lines', 'CHILD', 'of city_ledger_invoices'), ('city_ledger_receipt_allocations', 'CHILD', 'of city_ledger_receipts'),
    ('city_ledger_credit_note_lines', 'CHILD', 'of city_ledger_adjustments'), ('city_ledger_adjustment_invoices', 'CHILD', 'of city_ledger_adjustments'),
    ('city_ledger_reminders', 'CHILD', 'of city_ledger_invoices'), ('city_ledger_reminder_items', 'CHILD', 'of city_ledger_invoices'),
    ('cashier_shift_movements', 'CHILD', 'of cashier_shifts'), ('cashier_shift_counts', 'CHILD', 'of cashier_shifts'),
    ('bank_statement_lines', 'CHILD', 'of bank_statements'), ('bank_clearings', 'CHILD', 'of bank_statements'),
    ('card_settlements', 'CHILD', 'of gl_journals'), ('card_settlement_items', 'CHILD', 'of gl_journals'),
    ('tax_return_lines', 'CHILD', 'of tax_returns'), ('tax_return_input_claims', 'CHILD', 'of tax_returns'),
    ('tax_invoices', 'CHILD', 'of folio_items or city_ledger_invoices'), ('tax_invoice_lines', 'CHILD', 'of tax_invoices'),
    ('tax_invoice_exports', 'CHILD', 'of tax_invoices'), ('tax_invoice_export_items', 'CHILD', 'of tax_invoices'),
    ('budget_lines', 'CHILD', 'of budgets'), ('budget_statistics', 'CHILD', 'of budgets'),
    ('folio_transfers', 'CHILD', 'of folio_items'), ('pos_room_charges', 'CHILD', 'of folio_items'),
    ('inv_openings', 'CHILD', 'of inv_movements'), ('inv_opening_lines', 'CHILD', 'of inv_movements'), ('inv_balances', 'CHILD', 'of inv_movements'),
    ('inv_layers', 'CHILD', 'of inv_movements'), ('inv_layer_uses', 'CHILD', 'of inv_movements'), ('inv_shortfalls', 'CHILD', 'of inv_movements'),
    ('inv_receipts', 'CHILD', 'of inv_movements: a posted receipt has its movements'), ('inv_receipt_lines', 'CHILD', 'of inv_movements'),
    ('inv_returns', 'CHILD', 'of inv_movements'), ('inv_return_lines', 'CHILD', 'of inv_movements'), ('inv_bill_matches', 'CHILD', 'of supplier_bills'),
    ('inv_transfer_lines', 'CHILD', 'of inv_movements: the pair of movements of a transfer'), ('inv_issue_lines', 'CHILD', 'of inv_movements: the movement of a line of an issue'),
    ('inv_counts', 'CHILD', 'of inv_movements: the variance of an approved count'), ('inv_count_lines', 'CHILD', 'of inv_movements'),
    ('inv_amenity_runs', 'CHILD', 'of inv_movements: the cost of the night'), ('inv_amenity_run_lines', 'CHILD', 'of inv_movements'),
    ('inv_balance_days', 'CHILD', 'of inv_movements: the balance at the end of a day'), ('inv_cost_days', 'CHILD', 'of inv_movements: the cost of a day'),
    ('inv_fb_cost_days', 'CHILD', 'of inv_movements: the food and beverage cost of a day beside the POS revenue'),
    ('fa_opening_imports', 'CHILD', 'of fa_assets: the totals of an import, whose assets are in the register'),
    ('fa_source_links', 'CHILD', 'of fa_assets: the cost of each unit made from a journal line, which the journal already holds'),
    ('fa_depr_runs', 'CHILD', 'of fa_ledger: the total of a month of depreciation, whose journal and ledger rows hold the money'),
    ('fa_depr_lines', 'CHILD', 'of fa_ledger: the amount of an asset in a run'),
    ('fa_disposals', 'CHILD', 'of fa_ledger: the amounts of a disposal, whose journal and ledger rows hold the money'),
    ('fa_disposal_requests', 'CHILD', 'of fa_disposals: what was asked of a disposal that waits for an approver; the money is in the disposal once it is approved');
-- Configuration or plans: amounts that are not records of money received, paid or owed. They never lock the currency and are never converted.
INSERT INTO pms_test.currency_lock_classes VALUES
    ('rates', 'CONFIG', 'the rate grid'), ('rate_plan_bed_adjustments', 'CONFIG', 'bed supplements'), ('yield_rules', 'CONFIG', 'floors and caps'),
    ('companies', 'CONFIG', 'credit limit'), ('city_ledger_late_fee_settings', 'CONFIG', 'late fee'), ('card_fee_rules', 'CONFIG', 'fees'),
    ('property_cashier_settings', 'CONFIG', 'variance limit'), ('free_night_quotas', 'CONFIG', 'quotas'), ('property_approval_rules', 'CONFIG', 'limits of the approval rules'), ('property_tax_settings', 'CONFIG', 'tax settings'),
    ('tax_filing_profiles', 'CONFIG', 'filing settings'), ('charge_code_taxes', 'CONFIG', 'tax mapping'), ('service_charges', 'CONFIG', 'service charge'),
    ('taxes', 'CONFIG', 'tax rates'), ('charge_codes', 'CONFIG', 'charge codes'),
    ('reservation_room_rates', 'PLAN', 'the nightly price of a booking: money to be charged, kept as a snapshot, not a transaction'),
    ('reservation_rooms', 'PLAN', 'bookings'), ('reservations', 'PLAN', 'bookings'), ('stays', 'PLAN', 'stays'), ('stay_rooms', 'PLAN', 'stays'),
    ('pos_options', 'CONFIG', 'add-on and variant price differences'), ('pos_price_list_items', 'CONFIG', 'prices of an outlet'), ('folio_outlet_locks', 'CONFIG', 'the limit of outlet charges of a folio'),
    ('pos_room_charge_attempts', 'PLAN', 'a request of an outlet to charge a room, kept as a trail; the money is in the ledger'),
    ('pos_bill_lines', 'PLAN', 'the line of an order with its price snapshot; the money is the ledger row it posts when it is sent'),
    ('banquet_items', 'CONFIG', 'the list price of an item'), ('banquet_packages', 'CONFIG', 'the price per guest of a package'), ('inv_items', 'CONFIG', 'the reference cost of an item'), ('inv_levels', 'CONFIG', 'minimum and maximum quantities'), ('inventory_settings', 'CONFIG', 'tolerances and the receipt limit'), ('fa_settings', 'CONFIG', 'the capitalization threshold'),
    ('inv_supplier_items', 'CONFIG', 'the last price of a supplier for an item'), ('inv_approval_levels', 'CONFIG', 'the amount a level of a chain applies above'),
    ('inv_requests', 'PLAN', 'a purchase request: an estimate of what will be bought'), ('inv_request_lines', 'PLAN', 'the estimate of a line'),
    ('inv_approval_steps', 'PLAN', 'the snapshot of a level and the total the approver saw'), ('inv_approval_edits', 'PLAN', 'what an approver changed: a quantity or an estimated price'),
    ('inv_purchase_orders', 'PLAN', 'an order to a supplier: money to be paid, not yet owed'), ('inv_po_lines', 'PLAN', 'the price and the quantity ordered'),
    ('inv_requisitions', 'PLAN', 'a store requisition: an estimate of the value asked'), ('inv_requisition_lines', 'PLAN', 'the quantity asked and sent'),
    ('inv_amenity_plan_lines', 'CONFIG', 'the quantities of the amenities plan'),
    ('banquet_events', 'PLAN', 'the contract value estimate of an event: an estimate kept for the list, the money is in the ledger'), ('banquet_event_lines', 'PLAN', 'what is sold in a session with its frozen price: money to be charged when the session is posted'),
    ('banquet_line_components', 'PLAN', 'the split of a package line over its components, frozen with the line'), ('banquet_deposit_schedule', 'PLAN', 'the deposits asked of the customer: a schedule, the payments are in the ledger');
DO $$
DECLARE v_missing text;
BEGIN
    SELECT string_agg(DISTINCT c.table_name, ', ' ORDER BY c.table_name) INTO v_missing
      FROM information_schema.columns c
      JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
     WHERE c.table_schema = 'public' AND c.data_type = 'numeric' AND c.numeric_precision = 18
       AND EXISTS (SELECT 1 FROM information_schema.columns p WHERE p.table_schema = c.table_schema AND p.table_name = c.table_name AND p.column_name = 'property_id')
       AND c.table_name NOT IN (SELECT table_name FROM pms_test.currency_lock_classes);
    IF v_missing IS NOT NULL THEN
        RAISE EXCEPTION 'FAIL currency lock: tables with a money column that the currency lock does not classify: %', v_missing;
    END IF;
    INSERT INTO pms_test.results VALUES ('every money table is classified for the currency lock', 'accepted');
    RAISE NOTICE 'PASS  every table with a money column is classified for the currency lock';
END $$;
DO $$
DECLARE v_def text; v_t text; v_bad text; v_trg text;
BEGIN
    SELECT pg_get_functiondef('public.property_has_financial_data(bigint)'::regprocedure) INTO v_def;
    FOR v_t IN SELECT table_name FROM pms_test.currency_lock_classes WHERE class = 'ROOT' LOOP
        IF v_def NOT LIKE '%FROM ' || v_t || ' %' AND v_def NOT LIKE '%FROM public.' || v_t || ' %' THEN
            v_bad := coalesce(v_bad || ', ', '') || v_t;
        END IF;
    END LOOP;
    IF v_bad IS NOT NULL THEN RAISE EXCEPTION 'FAIL currency lock: the definition does not look at: %', v_bad; END IF;
    FOR v_t IN SELECT table_name FROM pms_test.currency_lock_classes WHERE class IN ('CONFIG', 'PLAN') LOOP
        IF v_def LIKE '%FROM ' || v_t || ' %' OR v_def LIKE '%FROM public.' || v_t || ' %' THEN
            RAISE EXCEPTION 'FAIL currency lock: % is configuration or a plan and must not lock the currency', v_t;
        END IF;
    END LOOP;
    -- the trigger asks the same function, and nothing else
    SELECT pg_get_functiondef('public.properties_currency_lock()'::regprocedure) INTO v_trg;
    IF v_trg NOT LIKE '%property_has_financial_data(OLD.id)%' OR v_trg ~* 'FROM\s+folio_items' THEN
        RAISE EXCEPTION 'FAIL currency lock: the trigger must use the one definition';
    END IF;
    INSERT INTO pms_test.results VALUES ('the lock definition looks at every root table and no configuration, and the trigger uses it', 'accepted');
    RAISE NOTICE 'PASS  the lock definition looks at every root table and no configuration, and the trigger uses it';
END $$;

------------------------------------------------------------------------------------------
-- Financial rows are written only on the OPEN business day (00065, audit F-07)
------------------------------------------------------------------------------------------
-- The helper takes a row that exists in the table (made by the tests above, so it is a valid one), closes the OPEN business day of its property (or takes a closed one) and inserts
-- a copy dated that closed day, with the columns of the row as they are (the identity column is left to its default). The trigger runs before the unique and foreign key checks, so
-- what the test asks is only whether the database lets a ledger row be dated a closed day. It is a direct insert: no service, no RequireOpenBusinessDay.
CREATE FUNCTION pms_test.insert_on_closed_day(p_table text, p_date_col text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v_row    jsonb;
    v_prop   bigint;
    v_day    date;
    v_cols   text;
BEGIN
    EXECUTE format('SELECT to_jsonb(t) FROM %I t ORDER BY id LIMIT 1', p_table) INTO v_row;
    IF v_row IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = 'PT404', MESSAGE = format('no row in %s to copy', p_table);
    END IF;
    v_prop := (v_row ->> 'property_id')::bigint;
    SELECT business_date INTO v_day FROM business_days WHERE property_id = v_prop AND status = 'OPEN';
    IF v_day IS NOT NULL THEN
        UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = v_prop AND business_date = v_day;
    ELSE
        SELECT max(business_date) INTO v_day FROM business_days WHERE property_id = v_prop AND status = 'CLOSED';
    END IF;
    SELECT string_agg(format('%I', column_name), ', ' ORDER BY ordinal_position) INTO v_cols
      FROM information_schema.columns WHERE table_schema = 'public' AND table_name = p_table AND is_identity = 'NO' AND is_generated = 'NEVER';
    v_row := (v_row - 'id') || jsonb_build_object(p_date_col, v_day);
    EXECUTE format('INSERT INTO %1$I (%2$s) SELECT %2$s FROM jsonb_populate_record(NULL::%1$I, $1)', p_table, v_cols) USING v_row;
END
$$;
SELECT expect_error('folio_items: a ledger line cannot be dated a closed business day, whoever inserts it', '23514', $q$SELECT pms_test.insert_on_closed_day('folio_items', 'business_date')$q$);
SELECT expect_error('payments: a payment cannot be dated a closed business day', '23514', $q$SELECT pms_test.insert_on_closed_day('payments', 'business_date')$q$);
SELECT expect_error('stay_charge_postings: a posted room night cannot be dated a closed business day', '23514', $q$SELECT pms_test.insert_on_closed_day('stay_charge_postings', 'business_date')$q$);
SELECT expect_error('city_ledger_receipts: a receipt cannot be dated a closed business day', '23514', $q$SELECT pms_test.insert_on_closed_day('city_ledger_receipts', 'business_date')$q$);
-- the other five tables (adjustments, invoices, shift movements, shifts opened and closed) have no row in this fixture; the Go tests of cityledger and shifts copy a real row of each
-- onto a closed day. What is checked here for all ten is that the trigger is there, on the right column, in the right moment.
DO $$
DECLARE v_missing text;
BEGIN
    SELECT string_agg(e.t || '.' || e.c, ', ') INTO v_missing
      FROM (VALUES ('folio_items', 'business_date'), ('payments', 'business_date'), ('stay_charge_postings', 'business_date'), ('city_ledger_receipts', 'business_date'),
                   ('city_ledger_adjustments', 'business_date'), ('city_ledger_invoices', 'invoice_date'), ('cashier_shift_movements', 'business_date'),
                   ('cashier_shifts', 'business_date_opened'), ('cashier_shifts', 'business_date_closed'), ('pos_room_charges', 'business_date')) AS e (t, c)
     WHERE NOT EXISTS (SELECT 1 FROM pg_trigger g JOIN pg_class c ON c.oid = g.tgrelid
                        WHERE c.relname = e.t AND NOT g.tgisinternal AND g.tgfoid = 'public.business_day_must_be_open'::regproc
                          AND (g.tgtype & 2) = 2 -- BEFORE
                          AND (string_to_array(encode(g.tgargs, 'escape'), '\000'))[1] = e.c);
    IF v_missing IS NOT NULL THEN RAISE EXCEPTION 'FAIL business day guard: no BEFORE trigger on %', v_missing; END IF;
    INSERT INTO pms_test.results VALUES ('the ten ledger and cash date columns each have the business-day guard', 'accepted');
    RAISE NOTICE 'PASS  the ten ledger and cash date columns each have the business-day guard';
END $$;

------------------------------------------------------------------------------------------
-- POS masters and the lock of a folio for outlet charges (migration 00080, docs/architecture/34-pos.md)
------------------------------------------------------------------------------------------
-- Makes the rows an outlet points to, and the outlet T1 (inside the subtransaction of the test: it is rolled back).
CREATE FUNCTION pms_test.pos_base() RETURNS void LANGUAGE plpgsql AS $$
DECLARE v_dept bigint; v_profile bigint; v_list bigint;
BEGIN
    SELECT id INTO v_dept FROM departments WHERE property_id = pr('BALI') AND code = 'FB';
    INSERT INTO pos_rule_profiles (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'P1', 'Profile') RETURNING id INTO v_profile;
    INSERT INTO pos_price_lists (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'L1', 'List') RETURNING id INTO v_list;
    INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
    VALUES (tn('ABC'), pr('BALI'), 'T', 'Outlet', 'RESTAURANT', 'PRINT', v_dept, v_profile, v_list);
    INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode) VALUES (tn('ABC'), pr('BALI'), 'POSX', 'POS test code', 'FOOD_BEVERAGE', 'EXCLUSIVE');
END $$;
CREATE FUNCTION pms_test.pos_outlet() RETURNS bigint LANGUAGE sql STABLE AS $$ SELECT id FROM pos_outlets WHERE property_id = pr('BALI') AND code = 'T' $$;

SELECT expect_ok('an outlet with its department, profile and price list is accepted', $q$SELECT pms_test.pos_base()$q$);
SELECT expect_error('the code of an outlet is letters, 1 to 6', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET code = 'T1' WHERE code = 'T'$q$);
SELECT expect_error('the code of an outlet is not the prefix of another document', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET code = 'FOL' WHERE code = 'T'$q$);
SELECT expect_error('the order mode of an outlet is PRINT, KDS or DIRECT', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET order_mode = 'FAX' WHERE code = 'T'$q$);
SELECT expect_error('the complimentary treatment of an outlet is one of two', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET comp_treatment = 'GIVE_AWAY' WHERE code = 'T'$q$);
SELECT expect_error('an outlet code is unique in the property', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
       SELECT tenant_id, property_id, code, 'Again', outlet_type, order_mode, department_id, rule_profile_id, price_list_id FROM pos_outlets WHERE code = 'T'$q$);
SELECT expect_error('the department of an outlet is one of the property', '23503', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET department_id = (SELECT id FROM departments WHERE property_id <> pr('BALI') LIMIT 1) WHERE code = 'T'$q$);
SELECT expect_error('a variant group picks exactly one', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_categories (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'C', 'Cat')$q$,
    $q$INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'P', 'Product', (SELECT id FROM pos_categories WHERE code = 'C'), cc('POSX'))$q$,
    $q$INSERT INTO pos_option_groups (tenant_id, property_id, product_id, name, kind, min_select, max_select) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_products WHERE code = 'P'), 'Portion', 'VARIANT', 1, 2)$q$);
SELECT expect_error('the minimum of a group is not above its maximum', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_categories (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'C', 'Cat')$q$,
    $q$INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'P', 'Product', (SELECT id FROM pos_categories WHERE code = 'C'), cc('POSX'))$q$,
    $q$INSERT INTO pos_option_groups (tenant_id, property_id, product_id, name, kind, min_select, max_select) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_products WHERE code = 'P'), 'Extras', 'MODIFIER', 3, 2)$q$);
SELECT expect_error('a rule of a profile is a tax or a service charge, not both and not none', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_rule_profile_rules (tenant_id, property_id, profile_id, tax_id, service_charge_id, sequence)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_rule_profiles WHERE code = 'P1'), NULL, NULL, 1)$q$);
SELECT expect_error('a price is not negative', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_categories (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'C', 'Cat')$q$,
    $q$INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'P', 'Product', (SELECT id FROM pos_categories WHERE code = 'C'), cc('POSX'))$q$,
    $q$INSERT INTO pos_price_list_items (tenant_id, property_id, price_list_id, product_id, price) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_price_lists WHERE code = 'L1'), (SELECT id FROM pos_products WHERE code = 'P'), -1)$q$);
SELECT expect_error('a table is inside its sizes', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Indoors')$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, x, y, w, h) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'Indoors'), '1', 0, 0, 0, 4)$q$);
SELECT expect_error('a table code is unique in the outlet, across its areas', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Indoors'), (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Terrace')$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, x, y) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'Indoors'), '5', 0, 0)$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, x, y) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'Terrace'), '5', 0, 0)$q$);
SELECT expect_ok('the same table code in two outlets is accepted', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
       SELECT tenant_id, property_id, 'U', 'Other', outlet_type, order_mode, department_id, rule_profile_id, price_list_id FROM pos_outlets WHERE code = 'T'$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Indoors'), (tn('ABC'), pr('BALI'), (SELECT id FROM pos_outlets WHERE code = 'U'), 'Indoors')$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, x, y) SELECT tn('ABC'), pr('BALI'), id, '5', 0, 0 FROM pos_areas$q$);

-- the lock of a folio
SELECT expect_error('an open lock has a limit above zero', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folio_outlet_locks (folio_id, tenant_id, property_id, status, limit_amount, valid_mode) VALUES (fo('F1'), tn('ABC'), pr('BALI'), 'OPEN', 0, 'UNTIL_CLOSE')$q$);
SELECT expect_error('a lock of one business day has its day, and only that one', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folio_outlet_locks (folio_id, tenant_id, property_id, status, limit_amount, valid_mode, valid_on) VALUES (fo('F1'), tn('ABC'), pr('BALI'), 'OPEN', 1000, 'TODAY', NULL)$q$);
SELECT expect_error('a locked lock says why, an open one does not', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folio_outlet_locks (folio_id, tenant_id, property_id, status, limit_amount, valid_mode) VALUES (fo('F1'), tn('ABC'), pr('BALI'), 'LOCKED', 1000, 'UNTIL_CLOSE')$q$);
SELECT expect_ok('an open lock with its outlets is accepted', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folio_outlet_locks (folio_id, tenant_id, property_id, status, limit_amount, valid_mode) VALUES (fo('F1'), tn('ABC'), pr('BALI'), 'OPEN', 1000, 'UNTIL_CLOSE')$q$,
    $q$INSERT INTO folio_outlet_lock_outlets (tenant_id, property_id, folio_id, outlet_id) VALUES (tn('ABC'), pr('BALI'), fo('F1'), pms_test.pos_outlet())$q$);
SELECT expect_error('the lock events are append only', '23001', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folio_outlet_lock_events (tenant_id, property_id, folio_id, kind, business_date) VALUES (tn('ABC'), pr('BALI'), fo('F1'), 'OPENED', (SELECT max(business_date) FROM business_days WHERE property_id = pr('BALI')))$q$,
    $q$UPDATE folio_outlet_lock_events SET reason = 'MANUAL'$q$);
SELECT expect_error('a refused attempt carries its code, an accepted one does not', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_room_charge_attempts (tenant_id, property_id, source, folio_id, outlet_id, amount, result, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'CHARGE', fo('F1'), pms_test.pos_outlet(), 1000, 'REJECTED', (SELECT max(business_date) FROM business_days WHERE property_id = pr('BALI')))$q$);
SELECT expect_error('an outlet charge is above zero', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_room_charges (tenant_id, property_id, outlet_id, to_folio_id, amount, lock_period_no, business_date)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), fo('F1'), 0, 1, (SELECT max(business_date) FROM business_days WHERE property_id = pr('BALI')))$q$);

------------------------------------------------------------------------------------------
-- POS bills and the ledger of an outlet (migration 00081, docs/architecture/34-pos.md)
------------------------------------------------------------------------------------------
SELECT expect_ok('the folio of an outlet bill has an outlet and no reservation, stay, group or company',
    $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0001', 'OUTLET', pms_test.pos_outlet())$q$);
SELECT expect_error('an OUTLET folio names its outlet', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type) VALUES (tn('ABC'), pr('BALI'), 'T-0002', 'OUTLET')$q$);
SELECT expect_error('only an OUTLET folio names an outlet', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0003', rs('R1'), st('S2'), pms_test.pos_outlet())$q$);
SELECT expect_error('an OUTLET folio belongs to no reservation', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0004', rs('R1'), 'OUTLET', pms_test.pos_outlet())$q$);
SELECT expect_error('an OUTLET folio is billed to no company', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id, bill_to_company_id) VALUES (tn('ABC'), pr('BALI'), 'T-0005', 'OUTLET', pms_test.pos_outlet(), (SELECT id FROM companies WHERE property_id = pr('BALI') AND code = 'ACME'))$q$);
SELECT expect_ok('a ledger row may come from the point of sale (source POS)', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0006', 'OUTLET', pms_test.pos_outlet())$q$,
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id, reference_type, reference_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('T-0006'), '2026-10-01', '2026-10-01', 'CHARGE', cc('POSX'), 'POS_BILL_LINE', 'c0a80101-0000-4000-8000-000000000001',
               'Nasi goreng', 1, 50000, 'EXCLUSIVE', 50000, 50000, 50000, 'POS')$q$);
SELECT expect_error('a line of a bill is posted once', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0007', 'OUTLET', pms_test.pos_outlet())$q$,
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id, reference_type, reference_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('T-0007'), '2026-10-01', '2026-10-01', 'CHARGE', cc('POSX'), 'POS_BILL_LINE', 'c0a80101-0000-4000-8000-000000000002',
               'Nasi goreng', 1, 50000, 'EXCLUSIVE', 50000, 50000, 50000, 'POS')$q$,
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id, reference_type, reference_id,
                                description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
       VALUES (tn('ABC'), pr('BALI'), fo('T-0007'), '2026-10-01', '2026-10-01', 'CHARGE', cc('POSX'), 'POS_BILL_LINE', 'c0a80101-0000-4000-8000-000000000002',
               'Nasi goreng again', 1, 50000, 'EXCLUSIVE', 50000, 50000, 50000, 'POS')$q$);
SELECT expect_ok('a complimentary payment is a payment', $q$SELECT 1$q$,
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'PCOMP', fo('F1'), 'PAYMENT', 'COMP', 1000, '2026-10-01')$q$);
SELECT expect_error('a complimentary payment is not refunded', '23514', $q$SELECT 1$q$,
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, refund_of_payment_id)
       VALUES (tn('ABC'), pr('BALI'), 'PCOMP2', fo('F1'), 'REFUND', 'COMP', 1000, '2026-10-01', pm('P1'))$q$);
SELECT expect_error('a complimentary payment keeps no fee snapshot', '23514', $q$SELECT 1$q$,
    $q$INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date, mdr_rate, mdr_fee, expected_settlement_date)
       VALUES (tn('ABC'), pr('BALI'), 'PCOMP3', fo('F1'), 'PAYMENT', 'COMP', 1000, '2026-10-01', 0.7, 7, '2026-10-02')$q$);
SELECT expect_ok('the system key COMP can be mapped', $q$DELETE FROM gl_account_map WHERE property_id = pr('BALI') AND map_key = 'COMP'$q$,
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (tn('ABC'), pr('BALI'), 'COMP', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1110'))$q$);
SELECT expect_error('a bill made by the client is made once', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b1', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01')$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b1', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01')$q$);
SELECT expect_error('an open bill has no closing time', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened, closed_at)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b3', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01', now())$q$);
SELECT expect_error('only a bill to eat in has a table', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name, sort_order, grid_w, grid_h) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Indoor', 0, 40, 24)$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, capacity, shape, x, y, w, h) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'Indoor'), 'A1', 2, 'SQUARE', 0, 0, 1, 1)$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, table_id, comp_treatment, business_date_opened)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b2', 'TAKEAWAY', (SELECT id FROM pos_tables WHERE code = 'A1'), 'PAYMENT_COMP', '2026-10-01')$q$);
SELECT expect_ok('a draft line is edited and deleted freely', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_categories (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'C1', 'Cat')$q$,
    $q$INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'PX', 'Product', (SELECT id FROM pos_categories WHERE code = 'C1'), cc('POSX'))$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b4', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01')$q$,
    $q$INSERT INTO pos_bill_lines (tenant_id, property_id, bill_id, client_uuid, product_id, product_name, charge_code_id, quantity, unit_price)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_bills WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000b4'), 'c0a80101-0000-4000-8000-0000000000c1',
               (SELECT id FROM pos_products WHERE code = 'PX'), 'Product', cc('POSX'), 1, 10000)$q$,
    $q$UPDATE pos_bill_lines SET quantity = 2 WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000c1'$q$,
    $q$DELETE FROM pos_bill_lines WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000c1'$q$);
SELECT expect_error('a line is sent with its ledger row', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_categories (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'C1', 'Cat')$q$,
    $q$INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES (tn('ABC'), pr('BALI'), 'PX', 'Product', (SELECT id FROM pos_categories WHERE code = 'C1'), cc('POSX'))$q$,
    $q$INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000b5', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01')$q$,
    $q$INSERT INTO pos_bill_lines (tenant_id, property_id, bill_id, client_uuid, product_id, product_name, charge_code_id, quantity, unit_price, status)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_bills WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000b5'), 'c0a80101-0000-4000-8000-0000000000c2',
               (SELECT id FROM pos_products WHERE code = 'PX'), 'Product', cc('POSX'), 1, 10000, 'SENT')$q$);
SELECT expect_error('a transfer between guests needs an approver, only an outlet charge does not', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0010', 'OUTLET', pms_test.pos_outlet())$q$,
    $q$INSERT INTO folio_transfer_batches (tenant_id, property_id, kind, from_folio_id, to_folio_id, scope, reason, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'BALANCE', fo('T-0010'), fo('F1'), 'ANY', 'x', '2026-10-01')$q$);
SELECT expect_ok('the charge of a bill to a room is a transfer with no approver', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id) VALUES (tn('ABC'), pr('BALI'), 'T-0011', 'OUTLET', pms_test.pos_outlet())$q$,
    $q$INSERT INTO folio_transfer_batches (tenant_id, property_id, kind, from_folio_id, to_folio_id, scope, reason, business_date)
       VALUES (tn('ABC'), pr('BALI'), 'BALANCE', fo('T-0011'), fo('F1'), 'OUTLET_CHARGE', 'outlet charge', '2026-10-01')$q$);
SELECT expect_ok('the table of a party is held for a name', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name, sort_order, grid_w, grid_h) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'Indoor', 0, 40, 24)$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, capacity, shape, x, y, w, h) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'Indoor'), 'A1', 2, 'SQUARE', 0, 0, 1, 1)$q$,
    $q$INSERT INTO pos_table_holds (tenant_id, property_id, table_id, business_date, from_time, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_tables WHERE code = 'A1'), '2026-10-01', '19:00', 'Santoso')$q$);

-- A bill, a round and one ticket (of a station when asked) in the given status, with no time of its own: the ticket checks are for the caller to provoke.
CREATE FUNCTION pms_test.pos_ticket(p_status text, p_station boolean) RETURNS void LANGUAGE plpgsql AS $$
DECLARE v_bill bigint; v_round bigint; v_station bigint;
BEGIN
    INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, order_type, comp_treatment, business_date_opened)
    VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'c0a80101-0000-4000-8000-0000000000e1', 'TAKEAWAY', 'PAYMENT_COMP', '2026-10-01') RETURNING id INTO v_bill;
    INSERT INTO pos_rounds (tenant_id, property_id, bill_id, round_no, client_uuid) VALUES (tn('ABC'), pr('BALI'), v_bill, 1, 'c0a80101-0000-4000-8000-0000000000e2') RETURNING id INTO v_round;
    IF p_station THEN
        INSERT INTO pos_stations (tenant_id, property_id, code, name, kds_enabled) VALUES (tn('ABC'), pr('BALI'), 'EVK', 'Kitchen', true) RETURNING id INTO v_station;
    END IF;
    INSERT INTO pos_tickets (tenant_id, property_id, round_id, bill_id, station_id, kind, status) VALUES (tn('ABC'), pr('BALI'), v_round, v_bill, v_station, 'NEW', p_status);
END $$;

------------------------------------------------------------------------------------------
-- POS realtime, the kitchen display and the queue of print jobs (migration 00082, docs/architecture/34-pos.md)
------------------------------------------------------------------------------------------
SELECT expect_error('an event names a type that exists', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_events (tenant_id, property_id, channel, type, entity_id) VALUES (tn('ABC'), pr('BALI'), 'outlet:1', 'bill.exploded', 1)$q$);
SELECT expect_error('an event names a channel of a known shape', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_events (tenant_id, property_id, channel, type, entity_id) VALUES (tn('ABC'), pr('BALI'), 'outlet:abc', 'bill.updated', 1)$q$);
SELECT expect_error('an event belongs to a property of its tenant', '23503', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_events (tenant_id, property_id, channel, type, entity_id) VALUES (tn('XYZ'), pr('BALI'), 'frontdesk', 'folio_lock.changed', 1)$q$);
SELECT expect_ok('an event carries the transaction that wrote it', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_events (tenant_id, property_id, channel, type, entity_id) VALUES (tn('ABC'), pr('BALI'), 'printers', 'print_job.updated', 1)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT txid FROM pos_events ORDER BY id DESC LIMIT 1) = pg_current_xact_id()::text::bigint THEN 1 ELSE 0 END)$q$);

-- the triggers: a change of a table hold is an event of the outlet, written once in the transaction however often it changes
SELECT expect_ok('a change of a hold is one event of the outlet, written by the transaction that made it', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name, sort_order, grid_w, grid_h) VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'EvIndoor', 0, 40, 24)$q$,
    $q$INSERT INTO pos_tables (tenant_id, property_id, area_id, code, capacity, shape, x, y, w, h) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_areas WHERE name = 'EvIndoor'), 'E1', 2, 'SQUARE', 0, 0, 1, 1)$q$,
    $q$INSERT INTO pos_table_holds (tenant_id, property_id, table_id, business_date, from_time, name) VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_tables WHERE code = 'E1'), '2026-10-01', '19:00', 'Santoso')$q$,
    $q$UPDATE pos_table_holds SET note = 'window' WHERE name = 'Santoso'$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM pos_events WHERE type = 'table.updated' AND channel = 'outlet:' || pms_test.pos_outlet()
                              AND entity_id = (SELECT id FROM pos_tables WHERE code = 'E1')) = 1 THEN 1 ELSE 0 END)$q$);

-- the status of a ticket and its times agree
SELECT expect_error('a ticket that is served has the time it was served', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$SELECT pms_test.pos_ticket('SERVED', false)$q$);
SELECT expect_error('a ticket that is cooking has the time it was started', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$SELECT pms_test.pos_ticket('COOKING', false)$q$);
SELECT expect_ok('a ticket goes through its statuses with their times', $q$SELECT pms_test.pos_base()$q$,
    $q$SELECT pms_test.pos_ticket('NEW', false)$q$,
    $q$UPDATE pos_tickets SET status = 'COOKING', started_at = now() WHERE bill_id = (SELECT id FROM pos_bills WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000e1')$q$,
    $q$UPDATE pos_tickets SET status = 'READY', ready_at = now() WHERE bill_id = (SELECT id FROM pos_bills WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000e1')$q$,
    $q$UPDATE pos_tickets SET status = 'SERVED', served_at = now() WHERE bill_id = (SELECT id FROM pos_bills WHERE client_uuid = 'c0a80101-0000-4000-8000-0000000000e1')$q$);
SELECT expect_ok('a ticket is an event of its station and of its outlet', $q$SELECT pms_test.pos_base()$q$,
    $q$SELECT pms_test.pos_ticket('NEW', true)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM pos_events WHERE type = 'ticket.created' AND channel LIKE 'station:%') = 1
                         AND (SELECT count(*) FROM pos_events WHERE type = 'ticket.updated' AND channel LIKE 'outlet:%') = 1
                         AND (SELECT count(*) FROM pos_events WHERE type = 'bill.updated' AND channel LIKE 'outlet:%') >= 1 THEN 1 ELSE 0 END)$q$);

-- print jobs: one for a document
SELECT expect_ok('a print job is queued for a printer', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_printers (tenant_id, property_id, name, kind) VALUES (tn('ABC'), pr('BALI'), 'Kitchen', 'KITCHEN')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_printers WHERE name = 'Kitchen'), 'TICKET', 'TICKET', 1, 'TICKET:1:1')$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT status FROM pos_print_jobs WHERE dedupe_key = 'TICKET:1:1') = 'QUEUED' THEN 1 ELSE 0 END)$q$);
SELECT expect_error('a document is queued once for a printer', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_printers (tenant_id, property_id, name, kind) VALUES (tn('ABC'), pr('BALI'), 'Kitchen', 'KITCHEN')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_printers WHERE name = 'Kitchen'), 'TICKET', 'TICKET', 1, 'TICKET:1:1')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_printers WHERE name = 'Kitchen'), 'TICKET', 'TICKET', 1, 'TICKET:1:1')$q$);
SELECT expect_error('a print job has a status that exists', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_printers (tenant_id, property_id, name, kind) VALUES (tn('ABC'), pr('BALI'), 'Kitchen', 'KITCHEN')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, status)
       VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_printers WHERE name = 'Kitchen'), 'TICKET', 'TICKET', 1, 'TICKET:2:1', 'LOST')$q$);
SELECT expect_error('a print job is for a printer of its property', '23503', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key)
       VALUES (tn('ABC'), pr('BALI'), 999999, 'TICKET', 'TICKET', 1, 'TICKET:3:1')$q$);

------------------------------------------------------------------------------------------
-- POS print agent: devices, enrolments, the claim of print jobs, receipt settings, drawer opens (migration 00083, docs/architecture/34-pos.md 20)
------------------------------------------------------------------------------------------
CREATE FUNCTION pms_test.pos_dev(p_name text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix)
    VALUES (tn('ABC'), pr('BALI'), p_name, md5(p_name) || md5(p_name || 'x'), 'kpd_' || left(md5(p_name), 8)) $$;
CREATE FUNCTION pms_test.pos_prn(p_name text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO pos_printers (tenant_id, property_id, name, kind) VALUES (tn('ABC'), pr('BALI'), p_name, 'KITCHEN') $$;
CREATE FUNCTION pms_test.pos_job(p_key text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key)
    VALUES (tn('ABC'), pr('BALI'), (SELECT id FROM pos_printers WHERE name = 'K1'), 'TICKET', 'TICKET', 1, p_key) $$;

SELECT expect_ok('a device holds the hash of its token', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$);
SELECT expect_error('the token of a device is a SHA-256 in hex', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix) VALUES (tn('ABC'), pr('BALI'), 'PC', 'kpd_the_token_itself', 'kpd_the_')$q$);
SELECT expect_error('two devices never share a token', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix)
       SELECT tenant_id, property_id, 'Other', token_hash, token_prefix FROM pos_devices WHERE name = 'PC'$q$);
SELECT expect_error('a device is active or revoked', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$UPDATE pos_devices SET status = 'LOST' WHERE name = 'PC'$q$);
SELECT expect_error('a revoked device says when', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$UPDATE pos_devices SET status = 'REVOKED' WHERE name = 'PC'$q$);
SELECT expect_ok('a device is revoked with its time', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$UPDATE pos_devices SET status = 'REVOKED', revoked_at = now() WHERE name = 'PC'$q$);
SELECT expect_error('a device belongs to a property of its tenant', '23503', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix) VALUES (tn('XYZ'), pr('BALI'), 'PC', repeat('a', 64), 'kpd_aaaa')$q$);

SELECT expect_ok('an enrolment code is used once by a device', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at, used_at, device_id)
       VALUES (tn('ABC'), pr('BALI'), repeat('b', 64), now() + interval '15 minutes', now(), (SELECT id FROM pos_devices WHERE name = 'PC'))$q$);
SELECT expect_error('an enrolment that was used names its device', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at, used_at) VALUES (tn('ABC'), pr('BALI'), repeat('b', 64), now() + interval '15 minutes', now())$q$);
SELECT expect_error('an enrolment code is unique', '23505', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at) VALUES (tn('ABC'), pr('BALI'), repeat('c', 64), now() + interval '15 minutes')$q$,
    $q$INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at) VALUES (tn('ABC'), pr('BALI'), repeat('c', 64), now() + interval '15 minutes')$q$);
SELECT expect_error('an enrolment code is a hash, never the code', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at) VALUES (tn('ABC'), pr('BALI'), 'K7QM-4TXD', now() + interval '15 minutes')$q$);

SELECT expect_ok('a printer is served by a device of its property', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$UPDATE pos_printers SET device_id = (SELECT id FROM pos_devices WHERE name = 'PC') WHERE name = 'K1'$q$);
SELECT expect_error('a printer cannot be served by a device of another property', '23503', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix) VALUES (tn('XYZ'), pr('SG'), 'Other', repeat('d', 64), 'kpd_dddd')$q$,
    $q$UPDATE pos_printers SET device_id = (SELECT id FROM pos_devices WHERE name = 'Other') WHERE name = 'K1'$q$);
SELECT expect_error('a printer reports a status that exists', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$UPDATE pos_printers SET last_status = 'ON_FIRE', status_reported_at = now() WHERE name = 'K1'$q$);
SELECT expect_error('a reported status has its time', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$UPDATE pos_printers SET last_status = 'ONLINE' WHERE name = 'K1'$q$);
SELECT expect_ok('a change of the status of a printer is an event of the printers channel, once', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$UPDATE pos_printers SET last_status = 'ONLINE', status_reported_at = now() WHERE name = 'K1'$q$,
    $q$UPDATE pos_printers SET last_status = 'PAPER_OUT', status_reported_at = now() WHERE name = 'K1'$q$,
    $q$UPDATE pos_printers SET status_reported_at = now() WHERE name = 'K1'$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM pos_events WHERE type = 'printer.updated' AND channel = 'printers'
                              AND entity_id = (SELECT id FROM pos_printers WHERE name = 'K1')) = 1 THEN 1 ELSE 0 END)$q$);
SELECT expect_ok('a device that enrols is an event of the printers channel', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM pos_events WHERE type = 'device.updated') = 1 THEN 1 ELSE 0 END)$q$);

-- the claim of a job
SELECT expect_ok('a job is claimed by a device with a lease', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET status = 'CLAIMED', claimed_by = (SELECT id FROM pos_devices WHERE name = 'PC'), lease_until = now() + interval '60 seconds', attempts = 1$q$);
SELECT expect_error('a claimed job names the device that holds it', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET status = 'CLAIMED', lease_until = now() + interval '60 seconds'$q$);
SELECT expect_error('a claimed job has a lease', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_dev('PC')$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET status = 'CLAIMED', claimed_by = (SELECT id FROM pos_devices WHERE name = 'PC')$q$);
SELECT expect_error('a job that is printed says when', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET status = 'PRINTED'$q$);
SELECT expect_error('a job that failed says why', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET status = 'FAILED', attempts = 3$q$);
SELECT expect_error('a job has no more attempts than its maximum', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET attempts = 4$q$);
SELECT expect_error('the bytes of a job are at most 64 KB', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET payload = decode(repeat('00', 65537), 'hex')$q$);
SELECT expect_ok('a payload of 64 KB is accepted', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$UPDATE pos_print_jobs SET payload = decode(repeat('00', 65536), 'hex')$q$);
SELECT expect_error('a reprint has a reason', '23514', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, copy_no, reprint_of)
       SELECT tenant_id, property_id, printer_id, kind, source_type, source_id, 'T:1:2', 2, id FROM pos_print_jobs$q$);
SELECT expect_ok('a reprint points to its original with its reason', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$, $q$SELECT pms_test.pos_job('T:1:1')$q$,
    $q$INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, copy_no, reprint_of, reprint_reason)
       SELECT tenant_id, property_id, printer_id, kind, source_type, source_id, 'T:1:2', 2, id, 'paper jam' FROM pos_print_jobs$q$);
SELECT expect_ok('the jobs of a printer have an order of their own', $q$SELECT pms_test.pos_base()$q$, $q$SELECT pms_test.pos_prn('K1')$q$,
    $q$SELECT pms_test.pos_job('T:1:1')$q$, $q$SELECT pms_test.pos_job('T:2:1')$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT seq FROM pos_print_jobs WHERE dedupe_key = 'T:1:1') < (SELECT seq FROM pos_print_jobs WHERE dedupe_key = 'T:2:1') THEN 1 ELSE 0 END)$q$);

-- the paper of an outlet and of a station
SELECT expect_error('an outlet prints 0 to 5 copies of a receipt', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET receipt_copies = 6 WHERE code = 'T'$q$);
SELECT expect_ok('an outlet may print no copy: the browser prints', $q$SELECT pms_test.pos_base()$q$,
    $q$UPDATE pos_outlets SET receipt_copies = 0, receipt_footer = 'Terima kasih' WHERE code = 'T'$q$);
SELECT expect_error('a station prints 1 to 5 copies of a ticket', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_stations (tenant_id, property_id, code, name, ticket_copies) VALUES (tn('ABC'), pr('BALI'), 'S1', 'Kitchen', 0)$q$);

-- a drawer opened without a sale
SELECT expect_ok('a drawer open has a reason and a person', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_drawer_opens (tenant_id, property_id, outlet_id, drawer, business_date, reason, opened_by)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'D1', '2026-10-01', 'change', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_error('a drawer open without a reason is refused', '23514', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_drawer_opens (tenant_id, property_id, outlet_id, drawer, business_date, reason, opened_by)
       VALUES (tn('ABC'), pr('BALI'), pms_test.pos_outlet(), 'D1', '2026-10-01', '  ', us('ABC', 'admin@hotel.com'))$q$);
SELECT expect_error('a drawer open is for an outlet of its property', '23503', $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO pos_drawer_opens (tenant_id, property_id, outlet_id, drawer, business_date, reason, opened_by)
       VALUES (tn('ABC'), pr('BALI'), 999999, 'D1', '2026-10-01', 'change', us('ABC', 'admin@hotel.com'))$q$);

------------------------------------------------------------------------------------------
-- Ordered lists: the database must have the ICU collation (the order of the guest names depends on it) and the indexes of the orders a screen opens with
------------------------------------------------------------------------------------------
DO $$
DECLARE v_order text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_collation WHERE collname = 'und-x-icu') THEN
        RAISE EXCEPTION 'FAIL the ICU collation "und-x-icu" is missing: PostgreSQL must be built with ICU (the guest list is ordered with it)';
    END IF;
    -- an accent stands beside its letter, and a capital beside a small one: not the order of the code points ("Eva" < "Zed" < "adi" < "Éric")
    SELECT string_agg(n, ' < ' ORDER BY n COLLATE "und-x-icu") INTO v_order FROM (VALUES ('Zed'), ('Éric'), ('adi'), ('Eva')) AS v (n);
    IF v_order <> 'adi < Éric < Eva < Zed' THEN RAISE EXCEPTION 'FAIL the ICU collation orders names as "%"', v_order; END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'guests_display_name_idx' AND indexdef LIKE '%und-x-icu%')
       OR NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'payments_paid_at_idx') THEN
        RAISE EXCEPTION 'FAIL the indexes of the ordered lists (guests_display_name_idx, payments_paid_at_idx) are missing';
    END IF;
    INSERT INTO pms_test.results VALUES ('the ICU collation orders names as an alphabet does, and the indexes of the ordered lists exist', 'accepted');
    RAISE NOTICE 'PASS  the ICU collation orders names as an alphabet does, and the indexes of the ordered lists exist';
END $$;

------------------------------------------------------------------------------------------
-- Inventory, PR 1 (migration 00084, docs/architecture/35-inventory.md): masters, ledger, balances, layers, shortfalls, opening stock
------------------------------------------------------------------------------------------
-- The rows a movement points to: the accounts, a unit, a category, an item, a warehouse and the settings of BALI (inside the subtransaction of the test: it is rolled back).
CREATE FUNCTION pms_test.inv_base() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
    VALUES (tn('ABC'), pr('BALI'), '1310', 'Food inventory', 'ASSET', 'DEBIT', 'INVENTORIES'),
           (tn('ABC'), pr('BALI'), '5210', 'Cost of food sales', 'EXPENSE', 'DEBIT', 'EXP_FB'),
           (tn('ABC'), pr('BALI'), '3900', 'Opening balance equity', 'EQUITY', 'CREDIT', 'EQUITY');
    INSERT INTO inventory_settings (tenant_id, property_id) VALUES (tn('ABC'), pr('BALI'));
    INSERT INTO inv_units (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'kg', 'Kilogram'), (tn('ABC'), pr('BALI'), 'g', 'Gram');
    INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id, cogs_account_id)
    VALUES (tn('ABC'), pr('BALI'), 'FOOD', 'Food', 'FOOD', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1310'), (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '5210'));
    INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id)
    VALUES (tn('ABC'), pr('BALI'), 'RICE', 'Rice', (SELECT id FROM inv_categories WHERE property_id = pr('BALI') AND code = 'FOOD'), (SELECT id FROM inv_units WHERE property_id = pr('BALI') AND code = 'kg'));
    INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind) VALUES (tn('ABC'), pr('BALI'), 'MAIN', 'Main store', 'MAIN');
END $$;
-- A movement of the item RICE in the warehouse MAIN on the open day of BALI.
CREATE FUNCTION pms_test.inv_move(p_kind text, p_qty numeric, p_value numeric, p_line int DEFAULT 1) RETURNS bigint LANGUAGE sql AS $$
    INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, unit_cost, business_date, source_type, source_id, source_line, item_class, cost_class)
    SELECT tn('ABC'), pr('BALI'), w.id, i.id, p_kind, p_qty, p_value, CASE WHEN p_qty = 0 THEN 0 ELSE abs(p_value / p_qty) END,
           (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'TEST', 1, p_line, 'FOOD', 'FOOD'
      FROM inv_warehouses w, inv_items i WHERE w.property_id = pr('BALI') AND w.code = 'MAIN' AND i.property_id = pr('BALI') AND i.code = 'RICE'
    RETURNING id
$$;

SELECT expect_ok('the masters of the module are accepted', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$);
SELECT expect_error('the costing method is AVERAGE or FIFO', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inventory_settings SET costing_method = 'LIFO'$q$);
SELECT expect_ok('the costing method can change before the first movement', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inventory_settings SET costing_method = 'FIFO'$q$, $q$UPDATE inventory_settings SET costing_method = 'AVERAGE'$q$);
SELECT expect_ok('the first movement stamps the date the method is locked from, and a later movement keeps it',
    $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$DO $d$ BEGIN IF (SELECT costing_locked_on FROM inventory_settings WHERE property_id = pr('BALI')) IS DISTINCT FROM (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN') THEN RAISE EXCEPTION 'not stamped'; END IF; END $d$;$q$,
    $q$SELECT pms_test.inv_move('ISSUE', -1, -10000, 2)$q$);
SELECT expect_error('the costing method is locked by the first movement', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$UPDATE inventory_settings SET costing_method = 'FIFO'$q$);
SELECT expect_error('the date of the lock cannot be cleared', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$UPDATE inventory_settings SET costing_locked_on = NULL$q$);
SELECT expect_ok('the other settings change after the lock', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$UPDATE inventory_settings SET price_tolerance_pct = 2.5, count_blind = false$q$);
SELECT expect_error('the tolerances are percentages', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inventory_settings SET price_tolerance_pct = 101$q$);
SELECT expect_error('the inventory account of a category is an asset of the group INVENTORIES', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id) VALUES (tn('ABC'), pr('BALI'), 'BAD', 'Bad', 'FOOD', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '5210'))$q$);
SELECT expect_error('the inventory account of a category is of the group INVENTORIES (an asset of another group is refused)', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('ABC'), pr('BALI'), '1111', 'Cash 2', 'ASSET', 'DEBIT', 'CASH')$q$,
    $q$INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id) VALUES (tn('ABC'), pr('BALI'), 'BAD', 'Bad', 'FOOD', (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1111'))$q$);
SELECT expect_error('the cost accounts of a category are expenses', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id, waste_account_id) VALUES (tn('ABC'), pr('BALI'), 'BAD', 'Bad', 'FOOD',
         (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1310'), (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '3900'))$q$);
SELECT expect_error('the kind of a category is one of four', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inv_categories SET kind = 'MEAT'$q$);
SELECT expect_error('the account of a category is one of the property (the trigger looks in the property)', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), pr('SG'), '1310', 'Food SG', 'ASSET', 'DEBIT', 'INVENTORIES')$q$,
    $q$UPDATE inv_categories SET inventory_account_id = (SELECT id FROM gl_accounts WHERE property_id = pr('SG') AND code = '1310')$q$);
SELECT expect_error('a unit code is unique in the property', '23505', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_units (tenant_id, property_id, code, name) VALUES (tn('ABC'), pr('BALI'), 'kg', 'Again')$q$);
SELECT expect_error('an item code is unique in the property', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id) SELECT tenant_id, property_id, code, 'Again', category_id, stock_unit_id FROM inv_items$q$);
SELECT expect_error('the category of an item is one of the property', '23503', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id) SELECT tenant_id, property_id, 'X', 'X', 99999, stock_unit_id FROM inv_items$q$);
SELECT expect_error('a barcode is unique in the property', '23505', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inv_items SET barcode = '123'$q$,
    $q$INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id, barcode) SELECT tenant_id, property_id, 'R2', 'R2', category_id, stock_unit_id, '123' FROM inv_items$q$);
SELECT expect_ok('a unit conversion is a positive fraction', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den, is_recipe) SELECT i.tenant_id, i.property_id, i.id, u.id, 1, 1000, true FROM inv_items i, inv_units u WHERE u.code = 'g' AND u.property_id = i.property_id$q$);
SELECT expect_error('a unit conversion has a positive numerator', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den) SELECT i.tenant_id, i.property_id, i.id, u.id, 0, 1 FROM inv_items i, inv_units u WHERE u.code = 'g' AND u.property_id = i.property_id$q$);
SELECT expect_error('a unit conversion has a positive denominator', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den) SELECT i.tenant_id, i.property_id, i.id, u.id, 1, 0 FROM inv_items i, inv_units u WHERE u.code = 'g' AND u.property_id = i.property_id$q$);
SELECT expect_error('an item has one default purchase unit', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den, is_purchase, is_default_purchase) SELECT i.tenant_id, i.property_id, i.id, u.id, 1, 1, true, true FROM inv_items i, inv_units u WHERE u.property_id = i.property_id$q$);
SELECT expect_error('the kind of a warehouse is MAIN, OUTLET or DEPARTMENT', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inv_warehouses SET kind = 'CELLAR'$q$);
SELECT expect_error('a warehouse of an outlet names the outlet', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inv_warehouses SET kind = 'OUTLET'$q$);
SELECT expect_error('the cost class of a warehouse is FOOD, BEVERAGE or OTHER', '23514', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inv_warehouses SET cost_class = 'WINE'$q$);
SELECT expect_error('at most one active warehouse per outlet', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, outlet_id) SELECT tenant_id, property_id, 'O1', 'O1', 'OUTLET', id FROM pos_outlets WHERE code = 'T'$q$,
    $q$INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, outlet_id) SELECT tenant_id, property_id, 'O2', 'O2', 'OUTLET', id FROM pos_outlets WHERE code = 'T'$q$);
SELECT expect_ok('an inactive warehouse of an outlet makes room for another', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.pos_base()$q$,
    $q$INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, outlet_id, is_active) SELECT tenant_id, property_id, 'O1', 'O1', 'OUTLET', id, false FROM pos_outlets WHERE code = 'T'$q$,
    $q$INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, outlet_id) SELECT tenant_id, property_id, 'O2', 'O2', 'OUTLET', id FROM pos_outlets WHERE code = 'T'$q$);
SELECT expect_error('the maximum level is not below the minimum', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_levels (tenant_id, property_id, warehouse_id, item_id, min_qty, max_qty) SELECT w.tenant_id, w.property_id, w.id, i.id, 10, 5 FROM inv_warehouses w, inv_items i$q$);

-- the ledger
SELECT expect_error('the ledger is append-only: an update is refused', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$, $q$UPDATE inv_movements SET value_delta = 1$q$);
SELECT expect_error('the ledger is append-only: a delete is refused', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$, $q$DELETE FROM inv_movements$q$);
SELECT expect_error('the ledger cannot be truncated', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$, $q$TRUNCATE inv_movements CASCADE$q$);
SELECT expect_error('inv_movements: a movement cannot be dated a closed business day, whoever inserts it', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$SELECT pms_test.insert_on_closed_day('inv_movements', 'business_date')$q$);
SELECT expect_error('the kind of a movement is one of the list', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('GIFT', 10, 100000)$q$);
SELECT expect_error('an entry that adds goods does not take value out', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('RECEIPT', 10, -1)$q$);
SELECT expect_error('an entry that takes goods out does not add value', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('ISSUE', -10, 1)$q$);
SELECT expect_error('a movement of zero quantity is only a cost adjustment, a settlement or a waste of cost', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('ISSUE', 0, 5)$q$);
SELECT expect_ok('a cost adjustment moves value without quantity, in either direction', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('COST_ADJUST', 0, 5)$q$, $q$SELECT pms_test.inv_move('COST_ADJUST', 0, -5, 2)$q$);
SELECT expect_error('a document line is posted once', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$);
SELECT expect_error('a movement is reversed once', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('RECEIPT', 10, 100)$q$,
    $q$INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, business_date, source_type, source_id, source_line, item_class, cost_class, reverses_id)
       SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, 'RECEIPT_REVERSAL', -10, -100, m.business_date, 'TEST', 2, 1, 'FOOD', 'FOOD', m.id FROM inv_movements m$q$,
    $q$INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, business_date, source_type, source_id, source_line, item_class, cost_class, reverses_id)
       SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, 'RECEIPT_REVERSAL', -10, -100, m.business_date, 'TEST', 3, 1, 'FOOD', 'FOOD', m.id FROM inv_movements m WHERE kind = 'RECEIPT'$q$);
SELECT expect_error('a movement has both accounts or neither', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, business_date, source_type, source_id, item_class, cost_class, gl_debit_account_id, gl_amount)
       SELECT tn('ABC'), pr('BALI'), w.id, i.id, 'ISSUE', -1, -10, (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'TEST', 1, 'FOOD', 'FOOD',
              (SELECT id FROM gl_accounts WHERE code = '5210' AND property_id = pr('BALI')), 10 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('the accounts of a movement are accounts of the property', '23503', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES (tn('XYZ'), pr('SG'), '5210', 'Cost SG', 'EXPENSE', 'DEBIT', 'EXP_FB')$q$,
    $q$INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, business_date, source_type, source_id, item_class, cost_class, gl_debit_account_id, gl_credit_account_id, gl_amount)
       SELECT tn('ABC'), pr('BALI'), w.id, i.id, 'ISSUE', -1, -10, (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'TEST', 1, 'FOOD', 'FOOD',
              (SELECT id FROM gl_accounts WHERE code = '5210' AND property_id = pr('SG')), (SELECT id FROM gl_accounts WHERE code = '1310' AND property_id = pr('BALI')), 10 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('the class of a movement is FOOD, BEVERAGE or OTHER', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, business_date, source_type, source_id, item_class, cost_class)
       SELECT tn('ABC'), pr('BALI'), w.id, i.id, 'OPENING', 1, 1, (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'TEST', 1, 'FOOD', 'WINE' FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('an inventory movement locks the currency', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$, $q$UPDATE properties SET currency_decimals = 2 WHERE code = 'BALI'$q$);

-- balances, layers, shortfalls
SELECT expect_error('a balance has no value on an empty shelf', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id, qty, value) SELECT w.tenant_id, w.property_id, w.id, i.id, 0, 5 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('a balance has the same sign for quantity and value', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id, qty, value) SELECT w.tenant_id, w.property_id, w.id, i.id, 5, -5 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_ok('a balance can go below zero (stock sold before the goods came in)', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id, qty, value) SELECT w.tenant_id, w.property_id, w.id, i.id, -5, -5000 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('a balance is unique per warehouse and item', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id, qty, value) SELECT w.tenant_id, w.property_id, w.id, i.id, 5, 5 FROM inv_warehouses w, inv_items i$q$,
    $q$INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id, qty, value) SELECT w.tenant_id, w.property_id, w.id, i.id, 6, 6 FROM inv_warehouses w, inv_items i$q$);
SELECT expect_error('a layer never has more left than it came in with', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('RECEIPT', 10, 100)$q$,
    $q$INSERT INTO inv_layers (tenant_id, property_id, warehouse_id, item_id, movement_id, received_on, qty_in, qty_remaining, value_remaining) SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, m.business_date, 10, 11, 110 FROM inv_movements m$q$);
SELECT expect_error('a layer with nothing left has no value left', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('RECEIPT', 10, 100)$q$,
    $q$INSERT INTO inv_layers (tenant_id, property_id, warehouse_id, item_id, movement_id, received_on, qty_in, qty_remaining, value_remaining) SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, m.business_date, 10, 0, 5 FROM inv_movements m$q$);
SELECT expect_error('the uses of layers are append-only', '23001', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('RECEIPT', 10, 100)$q$,
    $q$INSERT INTO inv_layers (tenant_id, property_id, warehouse_id, item_id, movement_id, received_on, qty_in, qty_remaining, value_remaining) SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, m.business_date, 10, 10, 100 FROM inv_movements m$q$,
    $q$INSERT INTO inv_layer_uses (tenant_id, property_id, movement_id, layer_id, qty, value) SELECT l.tenant_id, l.property_id, l.movement_id, l.id, 1, 10 FROM inv_layers l$q$,
    $q$DELETE FROM inv_layer_uses$q$);
SELECT expect_error('a shortfall is open exactly while quantity is open', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('POS_SALE', -5, -50)$q$,
    $q$INSERT INTO inv_shortfalls (tenant_id, property_id, warehouse_id, item_id, movement_id, qty_open, qty_original, est_unit_cost, est_value_open, source_type, cost_class, business_date, status)
       SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, 0, 5, 10, 0, 'POS_SALE', 'FOOD', m.business_date, 'OPEN' FROM inv_movements m$q$);
SELECT expect_error('a shortfall belongs to one movement', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('POS_SALE', -5, -50)$q$,
    $q$INSERT INTO inv_shortfalls (tenant_id, property_id, warehouse_id, item_id, movement_id, qty_open, qty_original, est_unit_cost, est_value_open, source_type, cost_class, business_date) SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, 5, 5, 10, 50, 'POS_SALE', 'FOOD', m.business_date FROM inv_movements m$q$,
    $q$INSERT INTO inv_shortfalls (tenant_id, property_id, warehouse_id, item_id, movement_id, qty_open, qty_original, est_unit_cost, est_value_open, source_type, cost_class, business_date) SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, m.id, 5, 5, 10, 50, 'POS_SALE', 'FOOD', m.business_date FROM inv_movements m$q$);

-- opening stock
SELECT expect_error('one opening per item and warehouse', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_move('OPENING', 10, 100000)$q$,
    $q$INSERT INTO inv_openings (tenant_id, property_id, opening_number, client_uuid, warehouse_id, business_date, total_value) SELECT m.tenant_id, m.property_id, 'OPN-1', gen_random_uuid(), m.warehouse_id, m.business_date, 100000 FROM inv_movements m$q$,
    $q$INSERT INTO inv_opening_lines (tenant_id, property_id, opening_id, line_no, warehouse_id, item_id, qty_stock, unit_cost, value, movement_id) SELECT m.tenant_id, m.property_id, o.id, 1, m.warehouse_id, m.item_id, 10, 10000, 100000, m.id FROM inv_movements m, inv_openings o$q$,
    $q$INSERT INTO inv_opening_lines (tenant_id, property_id, opening_id, line_no, warehouse_id, item_id, qty_stock, unit_cost, value, movement_id) SELECT m.tenant_id, m.property_id, o.id, 2, m.warehouse_id, m.item_id, 10, 10000, 100000, m.id FROM inv_movements m, inv_openings o$q$);
SELECT expect_error('an opening number is unique in the property', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_openings (tenant_id, property_id, opening_number, client_uuid, warehouse_id, business_date, total_value) SELECT w.tenant_id, w.property_id, 'OPN-1', gen_random_uuid(), w.id, '2026-10-01', 0 FROM inv_warehouses w$q$,
    $q$INSERT INTO inv_openings (tenant_id, property_id, opening_number, client_uuid, warehouse_id, business_date, total_value) SELECT w.tenant_id, w.property_id, 'OPN-1', gen_random_uuid(), w.id, '2026-10-01', 0 FROM inv_warehouses w$q$);

-- the system accounts and the standard chart
SELECT expect_ok('the eight inventory keys are accepted in the account map', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) SELECT a.tenant_id, a.property_id, k, a.id FROM gl_accounts a, unnest(ARRAY['INV_GRNI','INV_PPV','INV_WASTE','INV_COUNT_VARIANCE','INV_COGS_FOOD','INV_COGS_BEVERAGE','INV_STAFF_MEAL','INV_ENTERTAINMENT']) k WHERE a.code = '5210' AND a.property_id = pr('BALI')$q$);
SELECT expect_error('an unknown key is refused in the account map', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) SELECT a.tenant_id, a.property_id, 'INV_OTHER', a.id FROM gl_accounts a WHERE a.code = '5210' AND a.property_id = pr('BALI')$q$);
SELECT expect_ok('the standard chart has the four inventory accounts, in both languages',
    $q$DO $d$ BEGIN
        IF (SELECT count(*) FROM usali_chart() WHERE code IN ('2130', '5290', '5295', '5299')) <> 4 THEN RAISE EXCEPTION 'missing accounts'; END IF;
        IF (SELECT count(*) FROM usali_chart_names('id') WHERE code IN ('2130', '5290', '5295', '5299')) <> 4 THEN RAISE EXCEPTION 'missing names'; END IF;
    END $d$;$q$);

------------------------------------------------------------------------------------------
-- Inventory, PR 2 (migration 00085, docs/architecture/35-inventory.md): the approval chain, requests, orders, receipts, returns
------------------------------------------------------------------------------------------
CREATE FUNCTION pms_test.pur_base() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pms_test.inv_base();
    INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den, is_purchase, is_default_purchase)
    SELECT i.tenant_id, i.property_id, i.id, i.stock_unit_id, 1, 1, true, true FROM inv_items i WHERE i.property_id = pr('BALI') AND i.code = 'RICE';
    INSERT INTO inv_requests (tenant_id, property_id, request_number, client_uuid, requester_id, status)
    VALUES (tn('ABC'), pr('BALI'), 'PR-000001', gen_random_uuid(), (SELECT id FROM users WHERE email = 'admin@hotel.com' AND tenant_id = tn('ABC')), 'DRAFT');
    INSERT INTO inv_request_lines (tenant_id, property_id, request_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock)
    SELECT r.tenant_id, r.property_id, r.id, 1, i.id, i.stock_unit_id, 5, 5 FROM inv_requests r, inv_items i WHERE r.property_id = pr('BALI') AND i.property_id = pr('BALI') AND i.code = 'RICE';
END $$;
CREATE FUNCTION pms_test.pur_step(p_level int, p_status text, p_by text DEFAULT NULL) RETURNS void LANGUAGE sql AS $$
    INSERT INTO inv_approval_steps (tenant_id, property_id, request_id, cycle, level_no, name, approver_kind, condition_kind, edit_allowed, status, decided_by, decided_at)
    SELECT r.tenant_id, r.property_id, r.id, 1, p_level, 'Level ' || p_level, 'DEPT_HEAD', 'ALWAYS', true, p_status,
           (SELECT id FROM users WHERE email = p_by AND tenant_id = r.tenant_id), CASE WHEN p_by IS NULL THEN NULL ELSE now() END
      FROM inv_requests r WHERE r.property_id = pr('BALI') $$;
CREATE FUNCTION pms_test.pur_edit(p_field text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO inv_approval_edits (tenant_id, property_id, step_id, line_no, field, old_value, new_value, edited_by)
    SELECT tenant_id, property_id, id, 1, p_field, 5, 4, (SELECT id FROM users LIMIT 1) FROM inv_approval_steps $$;
CREATE FUNCTION pms_test.pur_order(p_number text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO inv_purchase_orders (tenant_id, property_id, po_number, client_uuid, supplier_id, warehouse_id, order_date)
    SELECT w.tenant_id, w.property_id, p_number, gen_random_uuid(), s.id, w.id, CURRENT_DATE
      FROM inv_warehouses w, suppliers s WHERE w.property_id = pr('BALI') AND w.code = 'MAIN' AND s.property_id = pr('BALI') AND s.code = 'S1' $$;
CREATE FUNCTION pms_test.pur_receipt(p_status text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO inv_receipts (tenant_id, property_id, receipt_number, client_uuid, supplier_id, warehouse_id, received_on, status, total_value)
    SELECT w.tenant_id, w.property_id, 'GR-1', gen_random_uuid(), s.id, w.id, CURRENT_DATE, p_status, 0
      FROM inv_warehouses w, suppliers s WHERE w.property_id = pr('BALI') AND w.code = 'MAIN' AND s.property_id = pr('BALI') AND s.code = 'S1' $$;

SELECT expect_ok('a request, its line and a level of the chain are accepted', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$);
SELECT expect_error('a request is DRAFT, SUBMITTED, APPROVED, RETURNED, CONVERTED or CANCELLED', '23514', $q$SELECT pms_test.pur_base()$q$, $q$UPDATE inv_requests SET status = 'DONE'$q$);
SELECT expect_error('a submitted request names its department', '23514', $q$SELECT pms_test.pur_base()$q$, $q$UPDATE inv_requests SET status = 'SUBMITTED'$q$);
SELECT expect_error('a returned request carries the note of the return', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE inv_requests SET status = 'RETURNED', department_id = (SELECT id FROM departments WHERE property_id = pr('BALI') LIMIT 1)$q$);
SELECT expect_error('a request number is unique in the property', '23505', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_requests (tenant_id, property_id, request_number, client_uuid, requester_id) SELECT tenant_id, property_id, request_number, gen_random_uuid(), requester_id FROM inv_requests$q$);
SELECT expect_error('an item is once on a request', '23505', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_request_lines (tenant_id, property_id, request_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock) SELECT tenant_id, property_id, request_id, 2, item_id, purchase_unit_id, 1, 1 FROM inv_request_lines$q$);
SELECT expect_error('a line asks for a positive quantity', '23514', $q$SELECT pms_test.pur_base()$q$, $q$UPDATE inv_request_lines SET qty_purchase = 0$q$);
SELECT expect_error('a line is in a unit the item is bought in (a unit that is not one of the item is refused)', '23503', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE inv_request_lines SET purchase_unit_id = (SELECT id FROM inv_units WHERE property_id = pr('BALI') AND code = 'g')$q$);
SELECT expect_error('the requester is a user of the tenant', '23503', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE inv_requests SET requester_id = (SELECT id FROM users WHERE tenant_id = tn('XYZ') LIMIT 1)$q$);
SELECT expect_error('a level is approved by the head or by one permission, not both', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_approval_levels (tenant_id, property_id, doc_type, level_no, name, approver_kind, permission, condition_kind) VALUES (tn('ABC'), pr('BALI'), 'PR', 1, 'Head', 'DEPT_HEAD', 'inventory.cost_control', 'ALWAYS')$q$);
SELECT expect_error('a level above an amount names the amount', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_approval_levels (tenant_id, property_id, doc_type, level_no, name, approver_kind, condition_kind) VALUES (tn('ABC'), pr('BALI'), 'PR', 1, 'Head', 'DEPT_HEAD', 'TOTAL_ABOVE')$q$);
SELECT expect_error('a chain has at most five levels', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_approval_levels (tenant_id, property_id, doc_type, level_no, name, approver_kind, condition_kind) VALUES (tn('ABC'), pr('BALI'), 'PR', 6, 'Head', 'DEPT_HEAD', 'ALWAYS')$q$);
SELECT expect_error('a level of a request is once in a round', '23505', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'WAITING')$q$, $q$SELECT pms_test.pur_step(1, 'WAITING')$q$);
SELECT expect_error('one level of a request is active at a time', '23505', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$, $q$SELECT pms_test.pur_step(2, 'ACTIVE')$q$);
SELECT expect_error('one person approves one level of a document', '23505', $q$SELECT pms_test.pur_base()$q$,
    $q$SELECT pms_test.pur_step(1, 'APPROVED', 'admin@hotel.com')$q$, $q$SELECT pms_test.pur_step(2, 'APPROVED', 'admin@hotel.com')$q$);
SELECT expect_error('a decided level says who decided and when', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'APPROVED')$q$);
SELECT expect_error('a skipped level says why', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'SKIPPED')$q$);
SELECT expect_ok('an approver edit is recorded', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$, $q$SELECT pms_test.pur_edit('QTY')$q$);
SELECT expect_error('an approver changes the quantity or the estimated price, nothing else', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$, $q$SELECT pms_test.pur_edit('NOTE')$q$);
SELECT expect_error('what an approver changed cannot be changed', '23001', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$, $q$SELECT pms_test.pur_edit('QTY')$q$,
    $q$UPDATE inv_approval_edits SET new_value = 3$q$);
SELECT expect_error('what an approver changed cannot be deleted', '23001', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_step(1, 'ACTIVE')$q$, $q$SELECT pms_test.pur_edit('QTY')$q$,
    $q$DELETE FROM inv_approval_edits$q$);
SELECT expect_error('a notice has one of four kinds', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_notices (tenant_id, property_id, user_id, kind) SELECT tenant_id, property_id, requester_id, 'HELLO' FROM inv_requests$q$);
SELECT expect_error('the head and the deputy of a department are two people', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE departments SET head_user_id = (SELECT id FROM users WHERE tenant_id = tn('ABC') LIMIT 1), deputy_user_id = (SELECT id FROM users WHERE tenant_id = tn('ABC') LIMIT 1) WHERE property_id = pr('BALI')$q$);
SELECT expect_error('a deputy needs a head', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE departments SET deputy_user_id = (SELECT id FROM users WHERE tenant_id = tn('ABC') LIMIT 1) WHERE property_id = pr('BALI')$q$);
SELECT expect_error('the head of a department is a user of the tenant', '23503', $q$SELECT pms_test.pur_base()$q$,
    $q$UPDATE departments SET head_user_id = (SELECT id FROM users WHERE tenant_id = tn('XYZ') LIMIT 1) WHERE property_id = pr('BALI')$q$);
SELECT expect_ok('an order is accepted', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_order('PO-1')$q$);
SELECT expect_error('an order number is unique in the property', '23505', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_order('PO-1')$q$, $q$SELECT pms_test.pur_order('PO-1')$q$);
SELECT expect_error('a line of a request is on one order only', '23505', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_order('PO-1')$q$, $q$SELECT pms_test.pur_order('PO-2')$q$,
    $q$INSERT INTO inv_po_lines (tenant_id, property_id, po_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock, unit_price, amount, request_id, request_line_no)
       SELECT o.tenant_id, o.property_id, o.id, 1, l.item_id, l.purchase_unit_id, 5, 5, 100, 500, l.request_id, l.line_no FROM inv_purchase_orders o, inv_request_lines l$q$);
SELECT expect_error('an order line has a positive quantity', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_order('PO-1')$q$,
    $q$INSERT INTO inv_po_lines (tenant_id, property_id, po_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock, unit_price, amount)
       SELECT o.tenant_id, o.property_id, o.id, 1, l.item_id, l.purchase_unit_id, 0, 0, 100, 0 FROM inv_purchase_orders o, inv_request_lines l$q$);
SELECT expect_error('a receipt is POSTED or REVERSED', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_receipt('LOST')$q$);
SELECT expect_error('a reversed receipt says when it was reversed', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_receipt('REVERSED')$q$);
SELECT expect_ok('a posted receipt is accepted', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_receipt('POSTED')$q$);
SELECT expect_error('a return gives its reason', '23514', $q$SELECT pms_test.pur_base()$q$, $q$SELECT pms_test.pur_receipt('POSTED')$q$,
    $q$INSERT INTO inv_returns (tenant_id, property_id, return_number, client_uuid, supplier_id, warehouse_id, receipt_id, reason, total_value) SELECT tenant_id, property_id, 'RET-1', gen_random_uuid(), supplier_id, warehouse_id, id, '  ', 0 FROM inv_receipts$q$);
SELECT expect_error('the price of a supplier for an item is not negative', '23514', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_supplier_items (tenant_id, property_id, supplier_id, item_id, purchase_unit_id, last_price) SELECT i.tenant_id, i.property_id, s.id, i.id, i.stock_unit_id, -1 FROM inv_items i, suppliers s WHERE i.property_id = pr('BALI') AND s.property_id = pr('BALI') AND s.code = 'S1'$q$);
SELECT expect_error('a match names a receipt line and a bill that exist', '23503', $q$SELECT pms_test.pur_base()$q$,
    $q$INSERT INTO inv_bill_matches (tenant_id, property_id, receipt_id, receipt_line_no, bill_id, bill_line_no, qty_stock, bill_cost, receipt_value, variance) VALUES (tn('ABC'), pr('BALI'), 1, 1, 1, 1, 1, 0, 0, 0)$q$);

------------------------------------------------------------------------------------------
-- The language of the seeded data: the names of the chart of accounts, departments and charge codes exist in en and id, for exactly the same codes
------------------------------------------------------------------------------------------
DO $$
DECLARE v_bad text;
BEGIN
    SELECT string_agg(f.fn || ': ' || f.n_en || ' en, ' || f.n_id || ' id, ' || f.n_diff || ' codes differ', '; ') INTO v_bad
      FROM (
        SELECT 'usali_chart_names' AS fn, (SELECT count(*) FROM usali_chart_names('en')) AS n_en, (SELECT count(*) FROM usali_chart_names('id')) AS n_id,
               (SELECT count(*) FROM (SELECT code FROM usali_chart_names('en') EXCEPT SELECT code FROM usali_chart_names('id')) x) + (SELECT count(*) FROM (SELECT code FROM usali_chart_names('id') EXCEPT SELECT code FROM usali_chart_names('en')) y) AS n_diff
        UNION ALL
        SELECT 'department_names', (SELECT count(*) FROM department_names('en')), (SELECT count(*) FROM department_names('id')),
               (SELECT count(*) FROM (SELECT code FROM department_names('en') EXCEPT SELECT code FROM department_names('id')) x) + (SELECT count(*) FROM (SELECT code FROM department_names('id') EXCEPT SELECT code FROM department_names('en')) y)
        UNION ALL
        SELECT 'charge_code_names', (SELECT count(*) FROM charge_code_names('en')), (SELECT count(*) FROM charge_code_names('id')),
               (SELECT count(*) FROM (SELECT code FROM charge_code_names('en') EXCEPT SELECT code FROM charge_code_names('id')) x) + (SELECT count(*) FROM (SELECT code FROM charge_code_names('id') EXCEPT SELECT code FROM charge_code_names('en')) y)
      ) f
     WHERE f.n_en <> f.n_id OR f.n_diff <> 0 OR f.n_en = 0;
    IF v_bad IS NOT NULL THEN RAISE EXCEPTION 'FAIL the names of the seeded data differ between the languages: %', v_bad; END IF;
    IF (SELECT count(*) FROM usali_chart_names('en')) <> (SELECT count(*) FROM usali_chart()) THEN RAISE EXCEPTION 'FAIL usali_chart_names(en) is not usali_chart()'; END IF;
    BEGIN
        PERFORM seed_language_check('fr');
        RAISE EXCEPTION 'FAIL a language without names was accepted';
    EXCEPTION WHEN sqlstate '22023' THEN NULL;
    END;
    INSERT INTO pms_test.results VALUES ('the names of the seeded data exist in en and id for the same codes', 'accepted');
    RAISE NOTICE 'PASS  the names of the seeded data exist in en and id for the same codes';
END $$;

------------------------------------------------------------------------------------------
-- Inventory, PR 5 (00088): the snapshots of the day close and the targets
------------------------------------------------------------------------------------------
CREATE FUNCTION pms_test.inv_cost_day(p_kind text, p_amount numeric) RETURNS void LANGUAGE sql AS $$
    INSERT INTO inv_cost_days (tenant_id, property_id, business_date, cost_kind, amount)
    VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), p_kind, p_amount)
$$;
SELECT expect_ok('a day stores a cost of each kind, and a gain or a favourable variance is negative', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_cost_day('WASTE', 1000)$q$, $q$SELECT pms_test.inv_cost_day('PRICE_VARIANCE', -250)$q$);
SELECT expect_error('a day stores a kind of cost once for a department and an outlet', '23505', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_cost_day('WASTE', 1000)$q$, $q$SELECT pms_test.inv_cost_day('WASTE', 2000)$q$);
SELECT expect_error('a kind of cost is one of the ten', '23514', $q$SELECT pms_test.inv_base()$q$, $q$SELECT pms_test.inv_cost_day('LUNCH', 1000)$q$);
SELECT expect_error('a cost is stored for a day that exists', '23503', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_cost_days (tenant_id, property_id, business_date, cost_kind, amount) VALUES (tn('ABC'), pr('BALI'), DATE '2001-01-01', 'WASTE', 1)$q$);
SELECT expect_ok('the food and beverage figures of a day: one row for each outlet (or none) and class', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, class, revenue_net, sale_cost) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'FOOD', 1000000, 300000)$q$,
    $q$INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, class, revenue_net, sale_cost) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'BEVERAGE', 500000, 100000)$q$);
SELECT expect_error('the food and beverage figures of a day are one row for an outlet and a class', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, class, revenue_net) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'FOOD', 1)$q$,
    $q$INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, class, revenue_net) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'FOOD', 2)$q$);
SELECT expect_error('the class of the figures of a day is one of three', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, class, revenue_net) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 'DRINK', 1)$q$);
SELECT expect_ok('a day taken by the close is marked once', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_snapshot_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'))$q$);
SELECT expect_error('a day is marked once', '23505', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_snapshot_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'))$q$,
    $q$INSERT INTO inv_snapshot_days (tenant_id, property_id, business_date) VALUES (tn('ABC'), pr('BALI'), (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'))$q$);
SELECT expect_ok('the balance of an item at the end of a day', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balance_days (tenant_id, property_id, warehouse_id, item_id, business_date, qty, value) SELECT tn('ABC'), pr('BALI'), w.id, i.id, (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 10, 100000 FROM inv_warehouses w, inv_items i WHERE w.property_id = pr('BALI') AND i.property_id = pr('BALI')$q$);
SELECT expect_error('a balance with no quantity has no value', '23514', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_balance_days (tenant_id, property_id, warehouse_id, item_id, business_date, qty, value) SELECT tn('ABC'), pr('BALI'), w.id, i.id, (SELECT business_date FROM business_days WHERE property_id = pr('BALI') AND status = 'OPEN'), 0, 5 FROM inv_warehouses w, inv_items i WHERE w.property_id = pr('BALI') AND i.property_id = pr('BALI')$q$);
SELECT expect_ok('a target for the property and one for an outlet side by side', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'FOOD', 30)$q$,
    $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'BEVERAGE', 22.5)$q$);
SELECT expect_error('a target is set once for the property and a class', '23505', $q$SELECT pms_test.inv_base()$q$,
    $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'FOOD', 30)$q$,
    $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'FOOD', 31)$q$);
SELECT expect_error('a target is above zero', '23514', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'FOOD', 0)$q$);
SELECT expect_error('a target is at most 100', '23514', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'FOOD', 100.5)$q$);
SELECT expect_error('a target is for food or beverage', '23514', $q$SELECT pms_test.inv_base()$q$, $q$INSERT INTO inv_cost_targets (tenant_id, property_id, class, target_pct) VALUES (tn('ABC'), pr('BALI'), 'OTHER', 30)$q$);
SELECT expect_error('the engineering department is a department of the property', '23503', $q$SELECT pms_test.inv_base()$q$, $q$UPDATE inventory_settings SET engineering_department_id = 999999$q$);

------------------------------------------------------------------------------------------
-- Fixed assets, PR 1 (migration 00093, docs/architecture/37-fixed-assets.md): the masters, the register, the ledger, the chart
------------------------------------------------------------------------------------------
CREATE FUNCTION pms_test.fa_base() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group)
    VALUES (tn('ABC'), pr('BALI'), '1530', 'FF&E', 'ASSET', 'DEBIT', 'FIXED_ASSETS'),
           (tn('ABC'), pr('BALI'), '1510', 'Land', 'ASSET', 'DEBIT', 'FIXED_ASSETS'),
           (tn('ABC'), pr('BALI'), '1592', 'Accumulated depreciation - FF&E', 'ASSET', 'CREDIT', 'FIXED_ASSETS'),
           (tn('ABC'), pr('BALI'), '7520', 'Depreciation - FF&E', 'EXPENSE', 'DEBIT', 'DEPRECIATION');
    INSERT INTO fa_settings (tenant_id, property_id) VALUES (tn('ABC'), pr('BALI'));
    INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id, accumulated_account_id, expense_account_id, method, life_months, fiscal_group, fiscal_method)
    SELECT tn('ABC'), pr('BALI'), 'FFE', 'FF&E', 'FFE', c.id, a.id, e.id, 'SL', 96, 'G2', 'SL'
      FROM gl_accounts c, gl_accounts a, gl_accounts e
     WHERE c.property_id = pr('BALI') AND c.code = '1530' AND a.property_id = pr('BALI') AND a.code = '1592' AND e.property_id = pr('BALI') AND e.code = '7520';
    INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id)
    SELECT tn('ABC'), pr('BALI'), 'LAND', 'Land', 'LAND', c.id FROM gl_accounts c WHERE c.property_id = pr('BALI') AND c.code = '1510';
END $$;
-- An asset of the category FFE: cost 36,000,000, life 96 months. The columns that a test changes are parameters.
CREATE FUNCTION pms_test.fa_asset(p_number text DEFAULT 'FA-000001', p_category text DEFAULT 'FFE', p_residual numeric DEFAULT 0, p_accum numeric DEFAULT 0, p_months int DEFAULT 0, p_label text DEFAULT 'AAAAAAAAAAAA') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_assets (tenant_id, property_id, number, name, category_id, source, acquisition_date, in_service_date, start_rule, first_dep_month, cost, residual_value, life_months, method,
                           accum_depr, dep_months_done, last_dep_month, label_code)
    SELECT tn('ABC'), pr('BALI'), p_number, 'Lobby sofa', c.id, 'JOURNAL', DATE '2026-01-10', DATE '2026-01-10', 'IN_SERVICE_MONTH',
           CASE WHEN c.is_depreciable THEN DATE '2026-01-01' END, 36000000, p_residual,
           CASE WHEN c.is_depreciable THEN 96 END, CASE WHEN c.is_depreciable THEN 'SL' END,
           p_accum, p_months, CASE WHEN p_months > 0 THEN DATE '2026-03-01' END, p_label
      FROM fa_categories c WHERE c.property_id = pr('BALI') AND c.code = p_category
$$;

SELECT expect_ok('the masters of the register are accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$);
SELECT expect_ok('a depreciable category says so, and land and construction in progress do not', $q$SELECT pms_test.fa_base()$q$,
    $q$DO $d$ BEGIN
        IF NOT (SELECT is_depreciable FROM fa_categories WHERE code = 'FFE' AND property_id = pr('BALI')) THEN RAISE EXCEPTION 'FFE is depreciable'; END IF;
        IF (SELECT is_depreciable FROM fa_categories WHERE code = 'LAND' AND property_id = pr('BALI')) THEN RAISE EXCEPTION 'land is not depreciable'; END IF;
    END $d$;$q$);
SELECT expect_error('a category code is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id) SELECT tn('ABC'), pr('BALI'), 'LAND', 'Land again', 'LAND', cost_account_id FROM fa_categories WHERE code = 'LAND'$q$);
SELECT expect_error('the cost account of a category is an asset account', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id) SELECT tn('ABC'), pr('BALI'), 'X', 'X', 'OTHER', id FROM gl_accounts WHERE code = '7520' AND property_id = pr('BALI')$q$);
SELECT expect_error('the expense account of a category is an expense account', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$UPDATE fa_categories SET expense_account_id = (SELECT id FROM gl_accounts WHERE code = '1530' AND property_id = pr('BALI')) WHERE code = 'FFE'$q$);
SELECT expect_error('the accumulated account of a category is an asset account', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$UPDATE fa_categories SET accumulated_account_id = (SELECT id FROM gl_accounts WHERE code = '7520' AND property_id = pr('BALI')) WHERE code = 'FFE'$q$);
SELECT expect_error('the four fields of depreciation come together', '23514', $q$SELECT pms_test.fa_base()$q$, $q$UPDATE fa_categories SET life_months = NULL WHERE code = 'FFE'$q$);
SELECT expect_error('land is not depreciated', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$UPDATE fa_categories SET accumulated_account_id = (SELECT id FROM gl_accounts WHERE code = '1592' AND property_id = pr('BALI')), expense_account_id = (SELECT id FROM gl_accounts WHERE code = '7520' AND property_id = pr('BALI')),
              method = 'SL', life_months = 10 WHERE code = 'LAND'$q$);
SELECT expect_error('a building is straight line for the tax', '23514', $q$SELECT pms_test.fa_base()$q$, $q$UPDATE fa_categories SET fiscal_group = 'BLD_PERMANENT', fiscal_method = 'DB' WHERE code = 'FFE'$q$);
SELECT expect_error('a category is of a known kind', '23514', $q$SELECT pms_test.fa_base()$q$, $q$UPDATE fa_categories SET kind = 'BOAT' WHERE code = 'FFE'$q$);
SELECT expect_error('the life of a category is 1 to 1200 months', '23514', $q$SELECT pms_test.fa_base()$q$, $q$UPDATE fa_categories SET life_months = 0 WHERE code = 'FFE'$q$);

-- locations
SELECT expect_ok('a free area is a location with no reference', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby', 'AREA', 'LLLLLLLLLLLL')$q$);
SELECT expect_error('a location of a room names the room', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'R101', 'Room 101', 'ROOM', 'LLLLLLLLLLLL')$q$);
SELECT expect_error('a free area names no room', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, warehouse_id, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby', 'AREA', 1, 'LLLLLLLLLLLL')$q$);
SELECT expect_error('a location code is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby', 'AREA', 'LLLLLLLLLLLL')$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby 2', 'AREA', 'MMMMMMMMMMMM')$q$);
SELECT expect_error('a label code is unique', '23505', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby', 'AREA', 'LLLLLLLLLLLL')$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'POOL', 'Pool', 'AREA', 'LLLLLLLLLLLL')$q$);
SELECT expect_error('a label code is 12 letters and digits', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), 'LOBBY', 'Lobby', 'AREA', 'SHORT')$q$);

-- assets
SELECT expect_error('an asset number is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001')$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 0, 0, 'BBBBBBBBBBBB')$q$);
SELECT expect_error('a label code of an asset is unique', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001')$q$, $q$SELECT pms_test.fa_asset('FA-000002')$q$);
SELECT expect_error('the residual value is not above the cost', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 40000000)$q$);
SELECT expect_error('the accumulated depreciation does not pass cost less residual', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 6000000, 31000000, 10)$q$);
SELECT expect_ok('an asset that is fully depreciated is accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 6000000, 30000000, 96)$q$);
SELECT expect_error('an asset cannot be depreciated more months than its life', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 100, 97)$q$);
SELECT expect_error('months depreciated come with the last month depreciated', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 0, 0)$q$,
    $q$UPDATE fa_assets SET dep_months_done = 2$q$);
SELECT expect_error('land carries no accumulated depreciation', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'LAND', 0, 500)$q$);
SELECT expect_ok('land is an asset with no life', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'LAND')$q$);
SELECT expect_error('a disposed asset has its date, and no other asset does', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$UPDATE fa_assets SET status = 'DISPOSED'$q$);
SELECT expect_error('an opening asset names its import', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$UPDATE fa_assets SET source = 'OPENING'$q$);
SELECT expect_error('the fiscal accumulation comes with its date', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$UPDATE fa_assets SET fiscal_accum_opening = 100$q$);
SELECT expect_ok('the fiscal state of an old asset is kept with the asset', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$UPDATE fa_assets SET fiscal_accum_opening = 36000000, fiscal_opening_as_of = DATE '2026-09-30', fiscal_months_done_opening = 48, fiscal_opening_estimated = true, fiscal_group = 'G1', fiscal_method = 'SL'$q$);
SELECT expect_error('a category in use cannot be deleted', '23503', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$DELETE FROM fa_categories WHERE code = 'FFE'$q$);

-- the start of the depreciation is locked once the asset has been depreciated (A8)
SELECT expect_ok('the start of an asset that was never depreciated can change', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$UPDATE fa_assets SET start_rule = 'NEXT_MONTH', first_dep_month = DATE '2026-02-01', in_service_date = DATE '2026-01-20'$q$);
SELECT expect_error('the start of a depreciated asset is locked: the first month', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 1000, 1)$q$,
    $q$UPDATE fa_assets SET first_dep_month = DATE '2026-02-01'$q$);
SELECT expect_error('the start of a depreciated asset is locked: the rule', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 1000, 1)$q$,
    $q$UPDATE fa_assets SET start_rule = 'NEXT_MONTH'$q$);
SELECT expect_error('the start of a depreciated asset is locked: the date in service', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 1000, 1)$q$,
    $q$UPDATE fa_assets SET in_service_date = DATE '2026-01-20'$q$);
SELECT expect_ok('a depreciated asset can still change its name and its location', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 1000, 1)$q$,
    $q$UPDATE fa_assets SET name = 'Lobby sofa, grey', serial_no = 'SN-1'$q$);
SELECT expect_ok('the start is free again when the months depreciated are back to zero', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset('FA-000001', 'FFE', 0, 1000, 1)$q$,
    $q$UPDATE fa_assets SET accum_depr = 0, dep_months_done = 0, last_dep_month = NULL$q$, $q$UPDATE fa_assets SET first_dep_month = DATE '2026-02-01'$q$);

-- the register ledger is append-only (3.2)
CREATE FUNCTION pms_test.fa_ledger_row(p_kind text DEFAULT 'ACQUIRE', p_line int DEFAULT 1, p_reverses bigint DEFAULT NULL) RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_ledger (tenant_id, property_id, asset_id, category_id, kind, event_date, cost_delta, source_type, source_id, source_line, reverses_id)
    SELECT a.tenant_id, a.property_id, a.id, a.category_id, p_kind, DATE '2026-01-10', CASE WHEN p_reverses IS NULL THEN 36000000 ELSE -36000000 END, 'TEST', 1, p_line, p_reverses FROM fa_assets a
$$;
SELECT expect_ok('a row of the register ledger is accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$);
SELECT expect_error('the register ledger cannot be updated', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$, $q$UPDATE fa_ledger SET cost_delta = 1$q$);
SELECT expect_error('the register ledger cannot be deleted from', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$, $q$DELETE FROM fa_ledger$q$);
SELECT expect_error('the register ledger cannot be truncated', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$, $q$TRUNCATE fa_ledger$q$);
SELECT expect_error('a register row is posted once for its source and kind', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$, $q$SELECT pms_test.fa_ledger_row()$q$);
SELECT expect_error('only a reversal row names the row it reverses', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$,
    $q$SELECT pms_test.fa_ledger_row('DEPRECIATE', 2, (SELECT id FROM fa_ledger LIMIT 1))$q$);
SELECT expect_ok('a reversal row negates another', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$,
    $q$SELECT pms_test.fa_ledger_row('REVERSAL', 2, (SELECT id FROM fa_ledger LIMIT 1))$q$);
SELECT expect_error('a register row is reversed once', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row()$q$,
    $q$SELECT pms_test.fa_ledger_row('REVERSAL', 2, (SELECT id FROM fa_ledger LIMIT 1))$q$, $q$SELECT pms_test.fa_ledger_row('REVERSAL', 3, (SELECT id FROM fa_ledger WHERE kind = 'ACQUIRE'))$q$);
SELECT expect_error('a register row is of a known kind', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_ledger_row('SPEND')$q$);

-- the opening import, the sequences, the journal type, the currency lock
SELECT expect_ok('an opening import with and without a journal', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, asset_count, total_cost, total_accum) VALUES (tn('ABC'), pr('BALI'), 'FAI-000001', DATE '2026-09-30', 'REGISTER_ONLY', 2, 100, 40)$q$);
SELECT expect_error('the as-of date of an import is a month end', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, asset_count, total_cost, total_accum) VALUES (tn('ABC'), pr('BALI'), 'FAI-000001', DATE '2026-09-15', 'REGISTER_ONLY', 2, 100, 40)$q$);
SELECT expect_error('an import with a journal names it', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, asset_count, total_cost, total_accum) VALUES (tn('ABC'), pr('BALI'), 'FAI-000001', DATE '2026-09-30', 'WITH_JOURNAL', 2, 100, 40)$q$);
SELECT expect_error('an import number is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, asset_count, total_cost, total_accum) VALUES (tn('ABC'), pr('BALI'), 'FAI-000001', DATE '2026-09-30', 'REGISTER_ONLY', 2, 100, 40)$q$,
    $q$INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, asset_count, total_cost, total_accum) VALUES (tn('ABC'), pr('BALI'), 'FAI-000001', DATE '2026-10-31', 'REGISTER_ONLY', 1, 100, 40)$q$);
SELECT expect_ok('the sequences of the register are accepted', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) VALUES (tn('ABC'), pr('BALI'), 'FIXED_ASSET', 'FA-'), (tn('ABC'), pr('BALI'), 'FA_OPENING', 'FAI-')$q$);
SELECT expect_ok('a journal of the register has the type ASSET', $q$SELECT pms_test.fa_base()$q$,
    $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JV-FA1', 'ASSET', DATE '2026-10-31', 'Depreciation') RETURNING id) INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '7520'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1592'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_error('an unknown journal type is still refused', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JV-FA1', 'ASSETS', DATE '2026-10-31', 'Depreciation') RETURNING id) INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr FROM j, (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '7520'), 100, 0), (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1592'), 0, 100)) AS v(n, acc, dr, cr)$q$);
SELECT expect_ok('an asset and a row of the register ledger are financial data: the currency of the property is locked',
    $q$DO $d$ BEGIN IF position('fa_assets' IN pg_get_functiondef('property_has_financial_data(bigint)'::regprocedure)) = 0
                      OR position('fa_ledger' IN pg_get_functiondef('property_has_financial_data(bigint)'::regprocedure)) = 0 THEN RAISE EXCEPTION 'the currency lock does not read the register'; END IF; END $d$;$q$);

-- the fiscal groups
SELECT expect_ok('the fiscal groups are data of the property', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate) VALUES (tn('ABC'), pr('BALI'), 'G1', 4, 25, 50), (tn('ABC'), pr('BALI'), 'BLD_PERMANENT', 20, 5, NULL)$q$);
SELECT expect_error('a building has no declining balance rate', '23514', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate) VALUES (tn('ABC'), pr('BALI'), 'BLD_PERMANENT', 20, 5, 10)$q$);
SELECT expect_error('a fiscal group is one row for a property', '23505', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate) VALUES (tn('ABC'), pr('BALI'), 'G1', 4, 25, 50)$q$,
    $q$INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate) VALUES (tn('ABC'), pr('BALI'), 'G1', 4, 26, 50)$q$);

-- the acquisition from a journal line and the depreciation runs (migration 00094)
CREATE FUNCTION pms_test.fa_line(p_number text DEFAULT 'JV-L1', p_debit numeric DEFAULT 36000000, p_type text DEFAULT 'MANUAL', p_source_line int DEFAULT NULL) RETURNS void LANGUAGE sql AS $$
    WITH j AS (INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), p_number, p_type, DATE '2026-01-10', 'Purchase') RETURNING id)
    INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, source_line)
    SELECT tn('ABC'), pr('BALI'), j.id, v.n, v.acc, v.dr, v.cr, CASE WHEN v.n = 1 THEN p_source_line END FROM j,
           (VALUES (1, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1530'), p_debit, 0::numeric),
                   (2, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '7520'), 0::numeric, p_debit)) AS v(n, acc, dr, cr)
$$;
CREATE FUNCTION pms_test.fa_link(p_unit int DEFAULT 1, p_cost numeric DEFAULT 36000000, p_number text DEFAULT 'FA-000001', p_journal text DEFAULT 'JV-L1') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_source_links (tenant_id, property_id, journal_line_id, unit_no, asset_id, cost)
    SELECT tn('ABC'), pr('BALI'), (SELECT l.id FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.property_id = pr('BALI') AND j.journal_number = p_journal AND l.line_no = 1), p_unit, a.id, p_cost
      FROM fa_assets a WHERE a.property_id = pr('BALI') AND a.number = p_number
$$;
SELECT expect_ok('a unit made from a journal line is linked to it', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_link()$q$);
SELECT expect_error('the live units of a line add up to its debit', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_link(1, 30000000)$q$);
SELECT expect_ok('two units add up to the debit of the line', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset('FA-000001')$q$, $q$SELECT pms_test.fa_asset('FA-000002', 'FFE', 0, 0, 0, 'BBBBBBBBBBBB')$q$,
    $q$SELECT pms_test.fa_link(1, 18000000, 'FA-000001')$q$, $q$SELECT pms_test.fa_link(2, 18000000, 'FA-000002')$q$);
SELECT expect_error('a unit of a line is made once', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset('FA-000001')$q$, $q$SELECT pms_test.fa_asset('FA-000002', 'FFE', 0, 0, 0, 'BBBBBBBBBBBB')$q$,
    $q$SELECT pms_test.fa_link(1, 36000000, 'FA-000001')$q$, $q$SELECT pms_test.fa_link(1, 36000000, 'FA-000002')$q$);
SELECT expect_error('an asset comes from one line only', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_line('JV-L2')$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$SELECT pms_test.fa_link()$q$, $q$SELECT pms_test.fa_link(1, 36000000, 'FA-000001', 'JV-L2')$q$);
SELECT expect_ok('a released link frees the line for a new asset', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset('FA-000001')$q$, $q$SELECT pms_test.fa_asset('FA-000002', 'FFE', 0, 0, 0, 'BBBBBBBBBBBB')$q$,
    $q$SELECT pms_test.fa_link(1, 36000000, 'FA-000001')$q$, $q$UPDATE fa_source_links SET released_at = now()$q$, $q$SELECT pms_test.fa_link(1, 36000000, 'FA-000002')$q$);
SELECT expect_error('a link is kept: it cannot be deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_link()$q$, $q$DELETE FROM fa_source_links$q$);
SELECT expect_error('a link keeps its cost', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_link()$q$, $q$UPDATE fa_source_links SET cost = 1$q$);
SELECT expect_error('a released link is not written again', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_link()$q$,
    $q$UPDATE fa_source_links SET released_at = now()$q$, $q$UPDATE fa_source_links SET released_at = now() + interval '1 day'$q$);
SELECT expect_error('a bill line names its bill and its number together', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO fa_source_links (tenant_id, property_id, journal_line_id, unit_no, asset_id, cost, bill_id) SELECT tn('ABC'), pr('BALI'), l.id, 1, (SELECT id FROM fa_assets LIMIT 1), 36000000, 1 FROM gl_journal_lines l WHERE l.line_no = 1$q$);
SELECT expect_error('only a line of a bill has a line number', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-L1', 100, 'MANUAL', 1)$q$);
SELECT expect_ok('a dismissal hides the line once, and the restored one is kept', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$,
    $q$INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason) SELECT tn('ABC'), pr('BALI'), id, 'a repair' FROM gl_journal_lines WHERE line_no = 1$q$,
    $q$UPDATE fa_source_dismissals SET restored_at = now()$q$,
    $q$INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason) SELECT tn('ABC'), pr('BALI'), id, 'a repair again' FROM gl_journal_lines WHERE line_no = 1$q$);
SELECT expect_error('a line is dismissed once at a time', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$,
    $q$INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason) SELECT tn('ABC'), pr('BALI'), id, 'a repair' FROM gl_journal_lines WHERE line_no = 1$q$,
    $q$INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason) SELECT tn('ABC'), pr('BALI'), id, 'again' FROM gl_journal_lines WHERE line_no = 1$q$);
SELECT expect_error('a dismissal has a reason', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line()$q$,
    $q$INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason) SELECT tn('ABC'), pr('BALI'), id, '  ' FROM gl_journal_lines WHERE line_no = 1$q$);
CREATE FUNCTION pms_test.fa_run(p_month date DEFAULT DATE '2026-01-01', p_journal text DEFAULT 'JV-R1') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_depr_runs (tenant_id, property_id, period_month, journal_id, total, asset_count)
    SELECT tn('ABC'), pr('BALI'), p_month, j.id, 375000, 1 FROM gl_journals j WHERE j.property_id = pr('BALI') AND j.journal_number = p_journal
$$;
CREATE FUNCTION pms_test.fa_run_line(p_months int DEFAULT 1) RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_depr_lines (tenant_id, property_id, run_id, asset_id, category_id, expense_account_id, months, from_month, amount, accum_after, carrying_after, months_done_after)
    SELECT tn('ABC'), pr('BALI'), r.id, a.id, a.category_id, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '7520'), p_months, DATE '2026-01-01', 375000, 375000, 35625000, 1
      FROM fa_depr_runs r, fa_assets a
$$;
SELECT expect_ok('a run of a month and its line', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_run()$q$, $q$SELECT pms_test.fa_run_line()$q$);
SELECT expect_error('a month has one live run', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_line('JV-R2')$q$, $q$SELECT pms_test.fa_run()$q$, $q$SELECT pms_test.fa_run(DATE '2026-01-01', 'JV-R2')$q$);
SELECT expect_error('a run is for a whole month', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_run(DATE '2026-01-15')$q$);
SELECT expect_ok('a reversed run lets the month be run again', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_line('JV-R2')$q$, $q$SELECT pms_test.fa_run()$q$,
    $q$UPDATE fa_depr_runs SET status = 'REVERSED', reversal_journal_id = (SELECT id FROM gl_journals WHERE journal_number = 'JV-R2'), reversed_at = now(), reversal_reason = 'a mistake'$q$,
    $q$SELECT pms_test.fa_run(DATE '2026-01-01', 'JV-R2')$q$);
SELECT expect_error('a reversed run names its journal and its reason', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_run()$q$, $q$UPDATE fa_depr_runs SET status = 'REVERSED'$q$);
SELECT expect_error('a run is not deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_run()$q$, $q$DELETE FROM fa_depr_runs$q$);
SELECT expect_error('a run keeps its total', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_run()$q$, $q$UPDATE fa_depr_runs SET total = 1$q$);
SELECT expect_error('the lines of a run are append-only', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_run()$q$,
    $q$SELECT pms_test.fa_run_line()$q$, $q$UPDATE fa_depr_lines SET amount = 1$q$);
SELECT expect_error('an asset is once in a run', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_run()$q$,
    $q$SELECT pms_test.fa_run_line()$q$, $q$SELECT pms_test.fa_run_line()$q$);
SELECT expect_error('a line of a run is for one month or more', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_line('JV-R1')$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_run()$q$,
    $q$SELECT pms_test.fa_run_line(0)$q$);

-- the standard chart of a new property, and the seed of the register
SELECT expect_ok('the standard chart has the accounts of the register, in both languages, and 1590 is a header',
    $q$DO $d$ BEGIN
        IF (SELECT count(*) FROM usali_chart() WHERE code IN ('1570', '1591', '1592', '1593', '1594', '1595', '7540', '7550')) <> 8 THEN RAISE EXCEPTION 'missing accounts'; END IF;
        IF (SELECT count(*) FROM usali_chart_names('id') WHERE code IN ('1570', '1591', '1592', '1593', '1594', '1595', '7540', '7550')) <> 8 THEN RAISE EXCEPTION 'missing names'; END IF;
        IF (SELECT count(*) FROM usali_chart() WHERE code = '1590') <> 1 OR (SELECT is_postable FROM usali_chart() WHERE code = '1590') THEN RAISE EXCEPTION '1590 is one header'; END IF;
        IF (SELECT count(*) FROM usali_chart() WHERE parent_code = '1590') <> 5 THEN RAISE EXCEPTION 'five accounts under 1590'; END IF;
        IF (SELECT count(*) FROM usali_chart() WHERE code IN ('1591', '1592', '1593', '1594', '1595') AND (normal_side <> 'CREDIT' OR statement_group <> 'FIXED_ASSETS')) <> 0 THEN RAISE EXCEPTION 'accumulated accounts are credit accounts of the fixed assets'; END IF;
        IF (SELECT count(*) FROM usali_chart() WHERE code IN ('7540', '7550') AND statement_group <> 'DEPRECIATION') <> 0 THEN RAISE EXCEPTION 'the depreciation is added back in the cash flow'; END IF;
    END $d$;$q$);
SELECT expect_ok('a new property gets its settings, its fiscal groups and the seven default categories, once',
    $q$SELECT seed_chart_of_accounts(tn('ABC'), pr('BALI'), NULL, DATE '2026-01-01', 'id')$q$, $q$SELECT seed_fa_categories(tn('ABC'), pr('BALI'), 'id')$q$,
    $q$DO $d$ BEGIN
        IF (SELECT count(*) FROM fa_categories WHERE property_id = pr('BALI')) <> 7 THEN RAISE EXCEPTION 'seven categories'; END IF;
        IF (SELECT count(*) FROM fa_categories WHERE property_id = pr('BALI') AND is_depreciable) <> 5 THEN RAISE EXCEPTION 'five depreciable categories'; END IF;
        IF (SELECT name FROM fa_categories WHERE property_id = pr('BALI') AND code = 'LAND') <> 'Tanah' THEN RAISE EXCEPTION 'Indonesian names'; END IF;
        IF (SELECT count(*) FROM fa_fiscal_groups WHERE property_id = pr('BALI')) <> 6 THEN RAISE EXCEPTION 'six fiscal groups'; END IF;
        IF (SELECT count(*) FROM fa_settings WHERE property_id = pr('BALI')) <> 1 THEN RAISE EXCEPTION 'one settings row'; END IF;
        IF (SELECT count(*) FROM fa_categories k JOIN gl_accounts a ON a.id = k.accumulated_account_id AND a.property_id = k.property_id
             WHERE k.property_id = pr('BALI') AND a.parent_id = (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = '1590')) <> 5 THEN RAISE EXCEPTION 'the accumulated accounts sit under 1590'; END IF;
        IF seed_fa_categories(tn('ABC'), pr('BALI'), 'id') <> 0 THEN RAISE EXCEPTION 'the seed is idempotent'; END IF;
    END $d$;$q$);
SELECT expect_error('a language without names is refused by the seed of the register', '22023', $q$SELECT seed_fa_categories(tn('ABC'), pr('BALI'), 'fr')$q$);

-- 00095: moves, revisions, disposals
CREATE FUNCTION pms_test.fa_loc(p_code text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, label_code) VALUES (tn('ABC'), pr('BALI'), p_code, p_code, 'AREA', rpad(p_code, 12, 'X'))
$$;
CREATE FUNCTION pms_test.fa_move(p_date date, p_from text, p_to text, p_reason text DEFAULT 'because') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_moves (tenant_id, property_id, asset_id, effective_on, from_location_id, to_location_id, reason)
    SELECT tn('ABC'), pr('BALI'), (SELECT id FROM fa_assets WHERE property_id = pr('BALI') AND number = 'FA-000001'),
           p_date, (SELECT id FROM fa_locations WHERE property_id = pr('BALI') AND code = p_from), (SELECT id FROM fa_locations WHERE property_id = pr('BALI') AND code = p_to), p_reason
$$;
CREATE FUNCTION pms_test.fa_journal() RETURNS void LANGUAGE plpgsql AS $$
DECLARE v_id bigint;
BEGIN
    INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES (tn('ABC'), pr('BALI'), 'JV-FA95', 'MANUAL', DATE '2026-10-05', 'Disposal') RETURNING id INTO v_id;
    INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, description)
    SELECT tn('ABC'), pr('BALI'), v_id, v.n, (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = v.acc), v.dr, v.cr, 'x'
      FROM (VALUES (1, '1530', 100, 0), (2, '1592', 0, 100)) AS v (n, acc, dr, cr);
END $$;
CREATE FUNCTION pms_test.fa_disposal(p_kind text DEFAULT 'WRITE_OFF', p_cost numeric DEFAULT 1000, p_accum numeric DEFAULT 400, p_proceeds numeric DEFAULT 0, p_gain numeric DEFAULT -600,
                                     p_reason text DEFAULT 'DAMAGED', p_account text DEFAULT NULL, p_number text DEFAULT 'FAD-000001') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_disposals (tenant_id, property_id, number, asset_id, kind, disposal_date, cost_removed, accum_removed, carrying_removed, proceeds, proceeds_account_id, gain_loss, reason_code, journal_id)
    SELECT tn('ABC'), pr('BALI'), p_number, (SELECT id FROM fa_assets WHERE property_id = pr('BALI') AND number = 'FA-000001'), p_kind, DATE '2026-10-05', p_cost, p_accum, p_cost - p_accum, p_proceeds,
           (SELECT id FROM gl_accounts WHERE property_id = pr('BALI') AND code = p_account), p_gain, p_reason, (SELECT id FROM gl_journals WHERE property_id = pr('BALI') AND journal_number = 'JV-FA95')
$$;
SELECT expect_ok('a move changes the location', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$, $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'B')$q$);
SELECT expect_error('a move moves something', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'A')$q$);
SELECT expect_error('a move has a reason', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$, $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'B', '  ')$q$);
SELECT expect_error('a move is never backdated before the last one', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$,
    $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'B')$q$, $q$SELECT pms_test.fa_move(DATE '2026-10-04', 'B', 'A')$q$);
SELECT expect_ok('two moves on the same day are accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$,
    $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'B')$q$, $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'B', 'A')$q$);
SELECT expect_error('a move is kept', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$,
    $q$SELECT pms_test.fa_move(DATE '2026-10-05', 'A', 'B')$q$, $q$DELETE FROM fa_moves$q$);
SELECT expect_error('a revision is of a known kind', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO fa_revisions (tenant_id, property_id, asset_id, kind, effective_on, old_value, new_value, reason) SELECT tn('ABC'), pr('BALI'), id, 'COLOUR', DATE '2026-10-05', '{}', '{}', 'x' FROM fa_assets$q$);
SELECT expect_ok('a revision of the life is accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO fa_revisions (tenant_id, property_id, asset_id, kind, effective_on, old_value, new_value, reason) SELECT tn('ABC'), pr('BALI'), id, 'LIFE', DATE '2026-10-05', '{"life_months":96}', '{"life_months":120}', 'x' FROM fa_assets$q$);
SELECT expect_error('only a value added comes from a journal line', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO fa_revisions (tenant_id, property_id, asset_id, kind, effective_on, old_value, new_value, reason) SELECT tn('ABC'), pr('BALI'), id, 'CAPITALIZE', DATE '2026-10-05', '{}', '{}', 'x' FROM fa_assets$q$);
SELECT expect_error('a revision is kept', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO fa_revisions (tenant_id, property_id, asset_id, kind, effective_on, old_value, new_value, reason) SELECT tn('ABC'), pr('BALI'), id, 'LIFE', DATE '2026-10-05', '{}', '{}', 'x' FROM fa_assets$q$,
    $q$UPDATE fa_revisions SET reason = 'y'$q$);
SELECT expect_ok('a write-off is accepted', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$, $q$SELECT pms_test.fa_disposal()$q$);
SELECT expect_ok('a sale is received into an account', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('SALE', 1000, 400, 700, 100, 'SOLD', '1530')$q$);
SELECT expect_error('the gain is the proceeds less the carrying value', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('SALE', 1000, 400, 700, 99, 'SOLD', '1530')$q$);
SELECT expect_error('a sale names the account it is received into', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('SALE', 1000, 400, 700, 100, 'SOLD', NULL)$q$);
SELECT expect_error('a write-off brings in nothing', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('WRITE_OFF', 1000, 400, 50, -550, 'DAMAGED', '1530')$q$);
SELECT expect_error('a sale has the reason sold, and only a sale', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('WRITE_OFF', 1000, 400, 0, -600, 'SOLD')$q$);
SELECT expect_error('the accumulated amount removed is not above the cost removed', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal('WRITE_OFF', 1000, 1100, 0, 100)$q$);
SELECT expect_error('a disposal number is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$SELECT pms_test.fa_disposal()$q$);
SELECT expect_error('a disposal is never deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$DELETE FROM fa_disposals$q$);
SELECT expect_error('the facts of a disposal never change', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$UPDATE fa_disposals SET proceeds = 1, gain_loss = -599$q$);
SELECT expect_error('a reversed disposal says when and why', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$UPDATE fa_disposals SET status = 'REVERSED'$q$);
SELECT expect_ok('a posted disposal is reversed once', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$UPDATE fa_disposals SET status = 'REVERSED', reversal_journal_id = journal_id, reversed_at = now(), reversal_reason = 'wrong asset'$q$);
SELECT expect_error('a reversed disposal is kept as it is', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_journal()$q$,
    $q$SELECT pms_test.fa_disposal()$q$, $q$UPDATE fa_disposals SET status = 'REVERSED', reversal_journal_id = journal_id, reversed_at = now(), reversal_reason = 'wrong asset'$q$,
    $q$UPDATE fa_disposals SET reversal_reason = 'other'$q$);

CREATE FUNCTION pms_test.fa_dreq(p_status text DEFAULT 'PENDING', p_kind text DEFAULT 'WRITE_OFF', p_note text DEFAULT NULL, p_decided boolean DEFAULT false) RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_disposal_requests (tenant_id, property_id, asset_id, kind, disposal_date, reason_code, approval_amount, status, decided_at, decision_note)
    SELECT tn('ABC'), pr('BALI'), (SELECT id FROM fa_assets WHERE property_id = pr('BALI') AND number = 'FA-000001'), p_kind, DATE '2026-10-05', CASE WHEN p_kind = 'SALE' THEN 'SOLD' ELSE 'DAMAGED' END, 100,
           p_status, CASE WHEN p_decided THEN now() END, p_note
$$;
SELECT expect_ok('a disposal request waits', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq()$q$);
SELECT expect_error('one request waits for an asset at a time', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq()$q$, $q$SELECT pms_test.fa_dreq()$q$);
SELECT expect_error('a request waiting has no decision time', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq('PENDING', 'WRITE_OFF', NULL, true)$q$);
SELECT expect_error('a rejected request says why', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq('REJECTED', 'WRITE_OFF', NULL, true)$q$);
SELECT expect_ok('a rejected request is followed by another', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq('REJECTED', 'WRITE_OFF', 'not now', true)$q$, $q$SELECT pms_test.fa_dreq()$q$);
SELECT expect_error('an approved request points to its disposal', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq('APPROVED', 'WRITE_OFF', NULL, true)$q$);
SELECT expect_error('a sale request names the account it is received into', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq('PENDING', 'SALE')$q$);
SELECT expect_error('a request is never deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_dreq()$q$, $q$DELETE FROM fa_disposal_requests$q$);

-- 00096: the stock-take, its proposals, and a request of Maintenance that names an asset
CREATE FUNCTION pms_test.fa_audit(p_loc text DEFAULT NULL, p_number text DEFAULT 'FAO-000001', p_status text DEFAULT 'IN_PROGRESS') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_audits (tenant_id, property_id, number, location_id, status, closed_at)
    SELECT tn('ABC'), pr('BALI'), p_number, (SELECT id FROM fa_locations WHERE property_id = pr('BALI') AND code = p_loc), p_status, CASE WHEN p_status = 'CLOSED' THEN now() END
$$;
CREATE FUNCTION pms_test.fa_aline(p_result text DEFAULT 'FOUND', p_found text DEFAULT NULL, p_note text DEFAULT NULL, p_scanned boolean DEFAULT true, p_number text DEFAULT 'FAO-000001') RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_audit_lines (tenant_id, property_id, audit_id, asset_id, result, found_location_id, note, scanned_at)
    SELECT tn('ABC'), pr('BALI'), (SELECT id FROM fa_audits WHERE property_id = pr('BALI') AND number = p_number),
           (SELECT id FROM fa_assets WHERE property_id = pr('BALI') AND number = 'FA-000001'), p_result,
           (SELECT id FROM fa_locations WHERE property_id = pr('BALI') AND code = p_found), p_note, CASE WHEN p_scanned THEN now() END
$$;
CREATE FUNCTION pms_test.fa_proposal(p_kind text DEFAULT 'WRITE_OFF', p_to text DEFAULT NULL, p_status text DEFAULT 'PENDING', p_note text DEFAULT NULL, p_decided boolean DEFAULT false,
                                     p_result boolean DEFAULT false) RETURNS void LANGUAGE sql AS $$
    INSERT INTO fa_proposals (tenant_id, property_id, audit_id, asset_id, kind, to_location_id, status, decided_at, decision_note, result_type, result_id)
    SELECT tn('ABC'), pr('BALI'), a.id, (SELECT id FROM fa_assets WHERE property_id = pr('BALI') AND number = 'FA-000001'), p_kind,
           (SELECT id FROM fa_locations WHERE property_id = pr('BALI') AND code = p_to), p_status, CASE WHEN p_decided THEN now() END, p_note,
           CASE WHEN p_result THEN 'DISPOSAL' END, CASE WHEN p_result THEN 1 END
      FROM fa_audits a WHERE a.property_id = pr('BALI') AND a.number = 'FAO-000001'
$$;
SELECT expect_ok('an audit of a location is opened', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_audit('A')$q$);
SELECT expect_ok('an audit of the whole property is opened', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$);
SELECT expect_error('one audit of a place is open at a time', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_audit('A')$q$,
    $q$SELECT pms_test.fa_audit('A', 'FAO-000002')$q$);
SELECT expect_error('one audit of the whole property is open at a time', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_audit(NULL, 'FAO-000002')$q$);
SELECT expect_ok('a new audit of a place follows the closed one', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_audit('A', 'FAO-000001', 'CLOSED')$q$,
    $q$SELECT pms_test.fa_audit('A', 'FAO-000002')$q$);
SELECT expect_error('an audit number is unique in the property', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_loc('A')$q$, $q$SELECT pms_test.fa_loc('B')$q$,
    $q$SELECT pms_test.fa_audit('A')$q$, $q$SELECT pms_test.fa_audit('B')$q$);
SELECT expect_error('an audit is closed with its time', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$UPDATE fa_audits SET status = 'CLOSED'$q$);
SELECT expect_ok('an audit is closed once', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$UPDATE fa_audits SET status = 'CLOSED', closed_at = now()$q$);
SELECT expect_error('a closed audit is kept', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$UPDATE fa_audits SET status = 'CLOSED', closed_at = now()$q$,
    $q$UPDATE fa_audits SET note = 'again'$q$);
SELECT expect_error('an audit is never deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$DELETE FROM fa_audits$q$);
SELECT expect_ok('an asset found where it belongs is a line', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline()$q$);
SELECT expect_ok('an asset not scanned is missing', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$);
SELECT expect_ok('an asset found elsewhere says where', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('B')$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MOVED', 'B')$q$);
SELECT expect_error('an asset found is scanned', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('FOUND', NULL, NULL, false)$q$);
SELECT expect_error('an asset missing was not scanned', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('MISSING')$q$);
SELECT expect_error('an asset found elsewhere says where', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('MOVED')$q$);
SELECT expect_error('a damaged asset says what is wrong', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('DAMAGED')$q$);
SELECT expect_ok('a damaged asset with its note', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('DAMAGED', NULL, 'screen cracked')$q$);
SELECT expect_error('a result is of a known kind', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline('STOLEN')$q$);
SELECT expect_error('an asset is one row in an audit', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline()$q$,
    $q$SELECT pms_test.fa_aline()$q$);
SELECT expect_error('the lines of a closed audit are kept', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$UPDATE fa_audits SET status = 'CLOSED', closed_at = now()$q$, $q$SELECT pms_test.fa_aline()$q$);
SELECT expect_error('a line is never deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_aline()$q$,
    $q$DELETE FROM fa_audit_lines$q$);
SELECT expect_ok('a write-off is proposed for a missing asset', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal()$q$);
SELECT expect_ok('a move is proposed for an asset found elsewhere', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('B')$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MOVED', 'B')$q$, $q$SELECT pms_test.fa_proposal('MOVE', 'B')$q$);
SELECT expect_error('a move proposal names the place', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal('MOVE')$q$);
SELECT expect_error('a write-off proposal names no place', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_loc('B')$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal('WRITE_OFF', 'B')$q$);
SELECT expect_error('an asset has one proposal in an audit', '23505', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal()$q$, $q$SELECT pms_test.fa_proposal()$q$);
SELECT expect_error('a proposal comes from a line of its audit', '23503', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$, $q$SELECT pms_test.fa_proposal()$q$);
SELECT expect_error('a proposal waiting has no decision time', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal('WRITE_OFF', NULL, 'PENDING', NULL, true)$q$);
SELECT expect_error('a rejected proposal says why', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal('WRITE_OFF', NULL, 'REJECTED', NULL, true)$q$);
SELECT expect_error('an applied proposal says what it made', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal('WRITE_OFF', NULL, 'APPLIED', NULL, true)$q$);
SELECT expect_ok('a proposal is applied once', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal()$q$,
    $q$UPDATE fa_proposals SET status = 'APPLIED', decided_at = now(), result_type = 'DISPOSAL', result_id = 1$q$);
SELECT expect_error('a decided proposal is kept', '23514', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal()$q$,
    $q$UPDATE fa_proposals SET status = 'REJECTED', decided_at = now(), decision_note = 'no'$q$, $q$UPDATE fa_proposals SET decision_note = 'other'$q$);
SELECT expect_error('a proposal is never deleted', '23001', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$, $q$SELECT pms_test.fa_audit()$q$,
    $q$SELECT pms_test.fa_aline('MISSING', NULL, NULL, false)$q$, $q$SELECT pms_test.fa_proposal()$q$, $q$DELETE FROM fa_proposals$q$);
SELECT expect_ok('a request of Maintenance names an asset', $q$SELECT pms_test.fa_base()$q$, $q$SELECT pms_test.fa_asset()$q$,
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date, asset_id)
       SELECT tn('ABC'), pr('BALI'), 'MNT96', NULL, 'Lobby', 'FURNITURE', 'x', 'NORMAL', '2026-10-01', id FROM fa_assets$q$);
SELECT expect_error('a request of Maintenance names an asset of its own property', '23503', $q$SELECT pms_test.fa_base()$q$,
    $q$INSERT INTO maintenance_requests (tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date, asset_id)
       VALUES (tn('ABC'), pr('BALI'), 'MNT96', NULL, 'Lobby', 'FURNITURE', 'x', 'NORMAL', '2026-10-01', 999999)$q$);

------------------------------------------------------------------------------------------
-- Summary
------------------------------------------------------------------------------------------
DO $$
DECLARE v_total int; v_rejected int;
BEGIN
    SELECT count(*), count(*) FILTER (WHERE outcome LIKE 'rejected%') INTO v_total, v_rejected FROM pms_test.results;
    RAISE NOTICE '==> ALL % SCHEMA TESTS PASSED (% violations rejected, % valid cases accepted)',
        v_total, v_rejected, v_total - v_rejected;
END $$;
