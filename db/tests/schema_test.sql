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
                             base_amount, net_amount, service_charge_total, tax_total, credit, source, reason, idempotency_key)
    VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'REVERSAL', cc('ROOM'),
            fi('RC-S1-1001'), st('S1'), sr('S1', '410'), 'Reversal: Room 410 - 01 Oct 2026', -1, 1000000, 'EXCLUSIVE',
            -1000000, -1000000, -100000, -121000, 1221000, 'SYSTEM', 'Wrong rate', 'REV-RC-S1-1001')
    RETURNING id, tenant_id, property_id)
INSERT INTO folio_item_components (tenant_id, property_id, folio_item_id, component_type, service_charge_id, tax_id,
                                   code, name, rate, tax_on_service, base_amount, amount, sequence)
SELECT i.tenant_id, i.property_id, i.id, c.*
  FROM i, (VALUES ('SERVICE_CHARGE', sc('SVC'), NULL::bigint, 'SVC', 'Service 10%', 10.0, NULL::boolean, -1000000, -100000, 1),
                  ('TAX',            NULL,      tx('VAT'),    'VAT', 'VAT 11%',     11.0, true,          -1100000, -121000, 1))
       AS c(component_type, service_charge_id, tax_id, code, name, rate, tax_on_service, base_amount, amount, sequence);

SELECT expect_error('an item can be reversed only once', '23505',
    $q$INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id,
                                reverses_item_id, description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source, reason)
       VALUES (tn('ABC'), pr('BALI'), fo('F1'), '2026-10-01', '2026-10-01', 'REVERSAL', cc('ROOM'),
               fi('RC-S1-1001'), 'dup', -1, 1221000, 'EXCLUSIVE', -1221000, -1221000, 1221000, 'SYSTEM', 'dup')$q$);

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
SELECT expect_ok('payment may be voided (POSTED -> VOIDED)',
    $q$UPDATE payments SET status = 'VOIDED', voided_at = now(), void_reason = 'Wrong folio' WHERE payment_number = 'P1'$q$);
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
