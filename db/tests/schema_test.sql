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

INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number) VALUES
    (tn('ABC'), pr('BALI'), rt('DLX'), '201'),
    (tn('ABC'), pr('BALI'), rt('DLX'), '305'),
    (tn('ABC'), pr('BALI'), rt('DLX'), '410');

SELECT expect_error('room cannot use a room type of another property', '23503',
    $q$INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number) VALUES (tn('XYZ'), pr('SG'), rt('DLX'), '101')$q$);
SELECT expect_error('tenant/property pair must match', '23503',
    $q$INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number) VALUES (tn('XYZ'), pr('BALI'), rt('DLX'), '102')$q$);
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

SELECT expect_ok('seeding creates the ten standard charge codes once (idempotent)',
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('XYZ'), pr('SG'), NULL) = 10 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('XYZ'), pr('SG'), NULL) = 0 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM charge_codes WHERE property_id = pr('SG') AND is_system AND price_mode = 'EXCLUSIVE') = 10 THEN 1 ELSE 0 END)$q$,
    $q$SELECT 1 / (CASE WHEN (SELECT count(*) FROM charge_code_taxes WHERE property_id = pr('SG')) = 0 THEN 1 ELSE 0 END)$q$);
SELECT expect_ok('seeding only fills gaps and leaves existing codes alone',
    $q$SELECT 1 / (CASE WHEN seed_charge_codes(tn('ABC'), pr('BALI'), NULL) = 7 THEN 1 ELSE 0 END)$q$,
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
SELECT expect_error('one AUTO task per room, date and type', '23505', $q$INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type)
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
SELECT expect_error('a rule is never changed', '23001', $q$UPDATE card_fee_rules SET mdr_rate = 1$q$);
SELECT expect_error('a rule is never deleted', '23001', $q$DELETE FROM card_fee_rules$q$);

------------------------------------------------------------------------------------------
-- Audit log
------------------------------------------------------------------------------------------
INSERT INTO audit_logs (tenant_id, property_id, business_date, action, entity_type, entity_id, new_data)
VALUES (tn('ABC'), pr('BALI'), '2026-10-01', 'stay.checked_in', 'stay', st('S1'), '{"room": "201"}');
SELECT expect_error('audit log is append-only', '23001',
    $q$UPDATE audit_logs SET action = 'x'$q$);

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
