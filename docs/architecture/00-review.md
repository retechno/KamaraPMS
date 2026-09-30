# Architecture Review (revision 3): Steps 1–3

> Scope: the consolidated specification from 2026-09-30 (sections 1–58).
> Outcome: the specification is accepted, with the corrections below. Every correction is checked against the 33 principles in §57 of the specification, and **none of them reintroduces a rejected decision** (no `rooms.status`, no `taxes.is_inclusive`, no `payments.currency_code`, no `properties.business_date`, and no microservices).

## Step 1: Review summary

The specification is internally consistent on its core principles: derived occupancy, configurable tax and service charges, price mode belonging to the charge, snapshots, business days, and centralised posting. The problems are in the **table sketches**. Several of them contradict the principles or each other, mostly around multi-room reservations, the room-charge idempotency key, and where stay dates live.

Legend for impact: **DB** = schema impact, **BL** = business-logic impact.

---

## Step 2 and Step 3: Findings and corrections

### A. Contradictions

#### A1. The reservation header repeats the line data (arrival, departure, adult_count, child_count)
- **Problem:** `reservations` and `reservation_rooms` both carry dates and occupancy. A booking with two rooms on different dates (for example 10–12 Oct and 10–15 Oct) has no correct header value.
- **Why:** this breaks principle 32 (avoid unnecessary denormalisation), and it makes the header's unresolved-arrival test (§33) wrong for groups.
- **Correction:** dates and occupancy live **only on `reservation_rooms`**. The header's arrival, departure and room count are computed (min, max and count) in queries and API responses.
- **DB:** those 4 columns are dropped from `reservations`.
- **BL:** search by arrival date queries the lines. The API still *returns* `arrival_date` and `departure_date` on the header, as derived fields.

#### A2. Header statuses NO_SHOW, CHECKED_IN and COMPLETED can't describe a multi-room booking
- **Problem:** with 3 rooms, one can be checked in, one no-show and one cancelled. §33 defines an unresolved arrival using `reservation.status`, but each *room* arrives separately.
- **Why:** the bulk no-show (§33) and check-in (§49) act on a single room. A header status would either be wrong or have to be recomputed on every line change.
- **Correction:** the full lifecycle `DRAFT, CONFIRMED, CHECKED_IN, COMPLETED, CANCELLED, NO_SHOW` lives on **`reservation_rooms.status`**. The header keeps only the booking-level decision `DRAFT | CONFIRMED | CANCELLED`. The header's *display* status (for example `IN_HOUSE`, `PARTIALLY_ARRIVED`, `NO_SHOW` or `COMPLETED`) is derived. "Unresolved arrival" (§33) is evaluated per line: `reservation_rooms.status = 'CONFIRMED' AND arrival_date <= BD`. `PENDING` is not used anywhere.
- **DB:** the header CHECK has 3 values and the line CHECK has 6.
- **BL:** the check-in and check-out services update the *line* (`CHECKED_IN` → `COMPLETED`), which satisfies §50's "update reservation state".

#### A3. `reservation_rooms.room_rate`, `price_mode`, `discount` and `total_amount` conflict with the nightly rate table (§26, §27)
- **Problem:** if rates vary by night, one `room_rate` is wrong for some nights, and `total_amount` is derived from the nights.
- **Correction:** these columns are dropped. **`reservation_room_rates`** (one row per night) holds the rate, rate plan, charge code, price mode, discount and the agreed amount, which is exactly the list in §27. Totals and estimates are computed by the Charge Calculation Engine and never stored.
- **DB:** 4 columns dropped. Nightly table defined in [02-database-schema.md](02-database-schema.md).
- **BL:** confirmation and amendment rewrite nightly rows. Posting reads them.

#### A4. The suggested idempotency key (property + stay + **stay_room** + business_date + **charge_code**) allows double charges
- **Problem:** there are two ways to post the same night twice:
  - **Same-day room move:** segment 201 closes on 30 Sep and segment 305 opens on 30 Sep. With `stay_room_id` in the key, *both* segments can receive a 30 Sep charge.
  - **Charge code change:** if the rate plan's room code changes (ROOM → ROOM_EXEMPT) between posting runs, the key differs, so the night is charged again.
- **Why:** principle 21 (idempotent room charge posting) must hold even when the posting inputs change.
- **Correction:** the business identity of a room night is **(stay, night, charge source)**. It is recorded in a dedicated posting register, **`stay_charge_postings`**, with `UNIQUE (stay_id, service_date, charge_source, COALESCE(source_ref_id, 0)) WHERE status = 'POSTED'`. `stay_room_id` and `charge_code_id` are stored for traceability but are **not** part of the key. `charge_source` is `ROOM_NIGHT` in the MVP; later values are `PACKAGE` and `RECURRING`, with `source_ref_id` pointing to the package or recurring definition.
- **Why a register instead of a unique index on `folio_items`:** folio items are immutable. When a room charge is **reversed** (for example, the wrong rate was posted), the night must become postable again. A register row can move to `REVERSED`, which frees the key, while the ledger stays append-only.
- **DB:** a new table with 1 partial unique index. `folio_items` gains `stay_room_id`.
- **BL:** RoomChargePostingService writes the register row and the folio item in the same transaction. The Expected Charge Engine reads the register to decide `ALREADY_POSTED`.

#### A5. `stays` has no expected departure date, but §34 and §37 depend on one
- **Problem:** "Unresolved departure: departure date ≤ BD" and room-charge eligibility both need the stay's *current* expected departure. The sketch only has `actual_check_in` and `actual_check_out`.
- **Correction:** add `stays.arrival_date` (the business date of check-in) and `stays.departure_date` (the current expected departure, changed by extend or shorten). The line's `departure_date` keeps the *originally booked* date.
- **DB:** 2 date columns plus a CHECK.
- **BL:** extension updates the stay. Eligibility and blockers read the stay.

#### A6. `stays.reservation_id` and `guest_id` versus multi-room bookings
- **Problem:** a 3-room booking produces 3 physical stays. `reservation_id` alone can't tell which room line a stay fulfils, so the nightly rates (keyed by line) can't be found.
- **Correction:** `stays.reservation_room_id NOT NULL` (1 line to at most 1 live stay, 🛡 partial unique index). `reservation_id` is **not** stored, because it's reachable through the line (principle 32). `guest_id` is kept as the primary guest. Walk-ins create a reservation and line first, so there is one path.
- **DB:** the FK changes.
- **BL:** the posting service goes stay → line → nightly rate.

#### A7. `stay_rooms.is_primary` contradicts sequential room moves
- **Problem:** §29 describes stay rooms as a *history* (201 then 305). A stay is never in two rooms at once. "Primary" has no meaning, and it would allow two concurrent open segments.
- **Correction:** remove `is_primary`. 🛡 `UNIQUE (stay_id) WHERE check_out_at IS NULL` and 🛡 `UNIQUE (room_id) WHERE check_out_at IS NULL`. Add `start_business_date` and `end_business_date`, because §38 requires deciding which room a *business date* belongs to, and timestamps alone are ambiguous around midnight and before night audit.
- **Rule:** night *n* belongs to the segment with `start_business_date ≤ n < COALESCE(end_business_date, ∞)`. Segments are contiguous, so exactly one segment matches. In the §38 example (201 `[10 Sep, 30 Sep)` and 305 `[30 Sep, …)`), the night of 30 Sep belongs to **305**.
- **DB:** 1 column dropped, 2 added, plus 2 partial unique indexes.

#### A8. `folios.stay_id` as the only owner: deposits and fees have nowhere to go
- **Problem:** a deposit (before arrival), a cancellation fee or a no-show fee has no stay.
- **Correction:** `folios.reservation_id NOT NULL`, and `stay_id NULL` until check-in. At check-in the reservation's open unlinked folio (if any) is **linked**; otherwise one is created (§49's "Create Folio"). 🛡 There is one GUEST folio per stay, and at most one open unlinked folio per reservation.
- **DB:** 1 column added, 2 partial unique indexes.
- **BL:** deposits, cancellation fees and no-show fees work without special cases.

#### A9. OOS semantics: "OOO/OOS rooms must not be sellable" (§7)
- **Problem:** the earlier design kept OOS in inventory. The specification says neither is sellable.
- **Correction:** adopt the specification. **Both OOO and OOS remove the room from sellable inventory and assignment for the block's nights.** The difference is **statistical**: OOO rooms are excluded from the "available rooms" denominator in occupancy reports, while OOS rooms are still counted.
- **DB:** none.
- **BL:** the availability formula subtracts both types, and the night-audit summary distinguishes them.

#### A10. The engine input includes "charge code" (§17), but the engine must be pure and country-agnostic
- **Correction:** there are two layers under one public name:
  1. `chargecalc.Calculate(Input)` is **pure math** (amounts, price mode, ordered rules, precision).
  2. `ChargeCalculationService.Calculate(ctx, ChargeRequest{ChargeCodeID, …})` resolves the charge code into rules and price mode, then calls (1).

  Every module calls (2). Nothing else calculates tax. See [03-financial-engines.md](03-financial-engines.md).

#### A11. The price mode of a charge code must not change once used
- **Problem:** rate-grid amounts are interpreted in the room charge code's price mode (§13: price_mode is on the charge code). Flipping it from EXCLUSIVE to INCLUSIVE would silently reprice every future night in the grid.
- **Correction:** `charge_codes.price_mode` is **immutable** once the code is referenced by a rate plan or any posted item. To change it, create a new code (for example `ROOM_NETT`). Existing reservations are safe anyway, because the nightly rows snapshot the price mode and charge code (§27).
- **DB:** a trigger and service check.

#### A12. `folio_items` needs more than `debit`, `credit` and `net_amount` to reconstruct a calculation (§19)
- **Problem:** §19 requires the base amount, discount, service, tax, rates, taxable base, total and price mode to be reconstructable. The sketch has no base, discount or rounding adjustment.
- **Correction:** add `base_amount`, `discount_amount`, `rounding_adjustment`, `service_charge_total` and `tax_total`. Per-rule detail lives in `folio_item_components` (rate, taxable base, amount, sequence, and `tax_on_service` for taxes). **Debit and credit are kept** as the ledger sides (§47 "debit/credit rules").
  - 🛡 CHECK `debit − credit = net_amount + service_charge_total + tax_total`
  - 🛡 A deferred trigger checks that the totals equal the component sums
- **Why keep totals if components exist?** They give a row-level CHECK against the ledger amount and fast reporting. The components remain the authoritative breakdown, so nothing "relies on a single tax_amount column" (§21).

#### A13. `folio_items.reference_type` and `reference_id` (a polymorphic reference) can't have foreign keys
- **Correction:** core links are **explicit FKs**: `charge_code_id`, `payment_id`, `reverses_item_id`, `stay_id` and `stay_room_id`. `reference_type` and `reference_id` are kept **only for external or future sources** (for example `POS_CHECK`, `12345`), with 🛡 `CHECK ((reference_type IS NULL) = (reference_id IS NULL))`.

#### A14. Naming of timestamps versus business dates
- **Problem:** `folio_items.transaction_date` and `payments.payment_date` sit next to `business_date`, which invites mixing up server time and business date (principle 14).
- **Correction:** a naming rule: **`*_date` is always a `date`** (business or calendar), and **`*_at` is always a `timestamptz`**. So they become `transaction_at` and `paid_at`, and `stays.actual_check_in` becomes `actual_check_in_at`.
- **DB:** renames only.

#### A15. The same concept is spelled `status` in some master tables and `is_active` in others
- **Correction:** master data (room types, rate plans, taxes, service charges, charge codes, rooms) uses **`is_active boolean`**. `status` is reserved for lifecycles with more than two states or with meaningful non-active states (tenants and properties: `ACTIVE` or `SUSPENDED`/`INACTIVE`, plus reservations, stays, folios, payments, blocks and business days).

### B. Missing entities and fields

| # | Missing | Why | Correction |
|---|---|---|---|
| B1 | Posting register | This is the idempotency key for room charges (A4) | `stay_charge_postings` |
| B2 | `reservation_room_rates` | §27 asks for it | It holds `charge_code_id`, `price_mode`, `base_rate`, `discount_amount`, `amount` and `rate_plan_id` per night |
| B3 | `stay_guests` | Accompanying guests' registration | Kept from revision 2 |
| B4 | `document_sequences` | Gapless confirmation, stay, folio and payment numbers | Kept |
| B5 | `user_sessions`, `roles`, `role_permissions` | §3 permissions, and token revocation | Kept |
| B6 | `housekeeping_logs` | History of housekeeping transitions, needed for audit | Kept |
| B7 | `reservations.reservation_date` | The specification includes it. It isn't redundant with `created_at`, because it's the **business date** of the booking (pickup reports), which differs from the calendar date | Kept, with meaning = BD at creation |
| B8 | `room_types.max_occupancy` | `max_adult` + `max_child` alone would allow 2 adults + 2 children in a room for 3 people | Added (along with `base_occupancy`, from the specification) |
| B9 | `guests.origin_property_id` | Guest visibility rules (tenant-wide profile, property-based access) | Kept |
| B10 | Cashier sessions (§32.7) | They're not in the MVP | The night-audit **check hook** exists and returns no blockers. A later `cashier_shifts` table plugs in. |
| B11 | Packages and recurring charges (§32.6, §41) | `rate_plans.meal_plan` is informational in the MVP | The Expected Charge Engine has a `ChargeSource` interface. The MVP implements `ROOM_NIGHT`, and `PACKAGE` and `RECURRING` plug in later using the same register (`charge_source`, `source_ref_id`). **No schema change to the register is needed.** |
| B12 | Availability "other configured restrictions" (§8) | Min stay, closed to arrival, stop sell | Future `rate_restrictions`. The availability engine exposes a restriction hook. |

### C. Edge cases (and the rule that handles each)

| # | Edge case | Rule |
|---|---|---|
| C1 | **Check-out after midnight, before night audit**: local date is 1 Oct, BD is 30 Sep, departure is 1 Oct | The guest has *consumed* the night of 30 Sep. Check-out keeps `departure_date = BD + 1` when local date > BD, so night BD is **required** and is posted during check-out through RoomChargePostingService (`trigger = CHECK_OUT`). If local date = BD, it's an early departure and `departure_date = BD`, so night BD is not charged. |
| C2 | **Check-in after midnight, before audit**: local date is 1 Oct at 02:00, BD is 30 Sep | `arrival_date = BD` (30 Sep), so the night of 30 Sep is charged. This is standard behaviour. If the hotel chooses not to charge it, staff amend the nightly amount to 0 (with permission); it's never skipped silently. |
| C3 | **Room move on arrival day** | This creates a zero-night segment `[BD, BD)` for the first room. 🛡 `end_business_date ≥ start_business_date`. The night belongs to the second room. |
| C4 | **Reversing a room charge** | The register row becomes `REVERSED`, so the night becomes `READY` again and **will be reposted** by the next posting run. To *waive* a night, set its nightly amount to 0 (which posts a zero charge) or post an ADJUSTMENT. A reversal means "posted wrongly, repost correctly". |
| C5 | **Stay shortened after nights were posted** | Posted nights outside the new `[arrival, departure)` are **invalid charges**, and night audit blocks until they're reversed or adjusted. Shortening itself refuses a departure earlier than a night that has already been posted, unless the request also reverses those nights. |
| C6 | **Editing the nightly rate of a night that has already been posted** | Refused. Staff must reverse the posted charge first, then edit, then repost. This keeps the snapshot and the ledger consistent. |
| C7 | **Tax rate changed mid-stay** (for example 11% → 12%) | Posted nights keep their snapshot. Tonight's posting resolves the rules at posting time. That's correct for the tax point, which is the date of supply. |
| C8 | **Two night-audit runs at once** (two browser tabs) | `pg_try_advisory_xact_lock(property)` makes the second run fail fast with 409 `NIGHT_AUDIT_IN_PROGRESS`, and the `business_days` row is locked `FOR UPDATE`. |
| C9 | **Posting to a day that night audit is closing** | Every business-dated write locks the OPEN day `FOR SHARE`, and night audit locks it `FOR UPDATE`. The write waits, then sees the new day. |
| C10 | **Overstay** (OPEN stay with departure ≤ BD) | It's an unresolved departure, so night audit blocks. It's never checked out automatically (§34). Availability treats the room as held until `BD + 1`. |
| C11 | **A folio that contains both a deposit and charges** is linked at check-in | Allowed. The deposit credit simply sits on the stay folio. |
| C12 | **Price mode is INCLUSIVE and there's a discount** | The discount is taken off the gross amount, and then the components are extracted. So the guest pays exactly the quoted price minus the discount. |

### D. Architectural risks

| Risk | Mitigation |
|---|---|
| Room-charge logic creeping into NightAudit, CheckOut or controllers | **One** `RoomChargePostingService`. The generic charge endpoint **rejects** charge codes with `charge_type = ROOM`, so the only way to post room revenue is through the room-charge service (principle 20). |
| Tax math duplicated in the frontend for estimates | The frontend never computes tax. It calls `POST /charge-calculations` or reads the estimates returned by the API. |
| Long night-audit transaction on large properties | A single transaction is fine for up to a few hundred rooms. If a hotel grows, room charges can be posted in advance through "Post room charges" (idempotent), which leaves the audit transaction with only the validation and close steps. |
| Lock contention on `room_types` | One lock per room type per booking is negligible at hotel scale. `pg_advisory_xact_lock` keys are an alternative if needed. |
| Snapshot drift (a master changes after posting) | Components snapshot the code, name, rate, `tax_on_service`, base and amount. Reports read the snapshots, never the masters. |
| Tenant data leaks | Composite FKs (`(property_id, x_id)` and `(tenant_id, guest_id)`), scope middleware, and RLS as a later defence in depth. |

### E. Project structure: a deliberate deviation from the suggested layout

The suggested layout is layer-first (`domain/`, `application/`, `infrastructure/`, `interfaces/`), which scatters one module across four trees. **We keep a module-first modular monolith** (permitted by §56), with the layers *inside* each module. The specification's named services (`CheckInService`, `RoomChargePostingService`, `FolioPostingService`, `PaymentService`, `NightAuditService`, `ChargeCalculationEngine`, `ExpectedChargeEngine`) map one-to-one onto packages. See [01-domain-model.md](01-domain-model.md) §6.
