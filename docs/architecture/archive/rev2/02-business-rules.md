# PMS Business Rules (revision 2)

> Every rule is enforced in the service layer. 🛡 means a database constraint also enforces it.
> "BD" means the property's current **business date** (its OPEN `business_days` row).

---

## 0. Business date and day management

### 0.1 Two clocks
| Clock | Source | Used for |
|---|---|---|
| **Server time** | `now()` in UTC (Go: `platform/clock`, injectable for tests) | The moment things physically happened: `checked_in_at`, `posted_at`, `created_at`, session expiry, audit timestamps |
| **Property local time** | Server time converted to `properties.timezone` | Display, and the night-audit guard (§19.4) |
| **Business date (BD)** | `business_days WHERE property_id = ? AND status = 'OPEN'` | Everything operational or financial: stay arrival, nights, room charges, `folio_items.business_date`, `payments.business_date`, no-shows, revenue, cashier and occupancy reports, void windows |

Example: the server time is `2026-09-30T19:30Z`, which is 1 Oct 02:30 in Jakarta, but the BD is **30 Sep 2026** because night audit hasn't run yet. A payment taken now has `posted_at = 2026-09-30T19:30Z` and `business_date = 2026-09-30`.

**The application never uses `CURRENT_DATE` or `time.Now().Date()` for any business decision.** A lint rule or code review enforces this. Only the `tenancy.BusinessDate(ctx, propertyID)` accessor is allowed.

### 0.2 Day-management rules
1. When a property is created, the admin chooses its **opening business date**. One `business_days` row is inserted with `status = OPEN`.
2. 🛡 There is exactly one OPEN row per property (partial unique index).
3. The BD changes **only** through night audit. Night audit closes the OPEN day and inserts `BD + 1` as OPEN, in one transaction. Dates never skip or go backwards. 🛡 UNIQUE `(property_id, business_date)`.
4. A CLOSED day is **immutable**. Nothing can be posted to it, and it never reopens. Corrections are made as adjustments on the current BD.
5. 🛡 Financial rows (`folio_items`, `payments`) have an FK `(property_id, business_date) → business_days`. The service additionally requires that day to be OPEN.
6. Every business-dated write takes `SELECT … FROM business_days WHERE property_id = ? AND status = 'OPEN' FOR SHARE` **first**. Night audit takes the same row `FOR UPDATE`. So a posting either finishes before the audit or waits and then sees the new BD. No posting can land on a day being closed.
7. `GET /business-date` returns the BD, server time, property local time and a `night_audit_overdue` flag (true when the local date > BD + 1). The UI shows a banner when the flag is set.

---

## 1. Room availability

Night *n*, room type *T*, and `eff_dep(stay) = GREATEST(stay.departure_date, BD + 1)` (overstays keep holding the room).

```
sellable(T, n) = active rooms of type T
               − rooms of type T with an ACTIVE OOO block where start_date ≤ n < end_date

demand(T, n)   = reservation_rooms with status = CONFIRMED,
                   effective_type = T, and arrival_date ≤ n < departure_date
               + stays with status = IN_HOUSE, current room type = T, and BD ≤ n < eff_dep(stay)

available(T, n) = sellable(T, n) − demand(T, n)
```
- `effective_type` = the assigned room's type if a room is assigned, otherwise the booked type. Upgrades consume the physical type.
- Nights with `n < BD` are never checked (they're the past).
- `DRAFT`, `CANCELLED`, `NO_SHOW` and `CHECKED_IN` lines consume nothing. A CHECKED_IN line is counted through its stay.
- OOS doesn't reduce `sellable`, but the room can't be assigned.
- **No overbooking:** no operation may produce `available(T, n) < 0`.

**A specific room is free for `[a, d)`** iff all of these hold:
- the room is active,
- there is no ACTIVE block (OOO or OOS) overlapping `[a, d)`,
- there is no *other* CONFIRMED line assigned to it that overlaps `[a, d)`, and
- there is no open stay segment on it with `[BD, eff_dep)` overlapping `[a, d)`.

Overlap is always `a1 < d2 AND a2 < d1`. A departure and an arrival on the same date do **not** overlap. The physical conflict ("the previous guest is still in the room") is caught at check-in by the one-open-segment rule. Full algorithm: Phase 7.

## 2. Reservation creation
- The user has `reservation.create` at the property. The property is active.
- The booker guest is optional for a draft and required to confirm. It must belong to the same tenant 🛡.
- For each line:
  - `arrival_date ≥ BD`, and `departure_date > arrival_date` 🛡. Maximum length is 365 nights.
  - The room type is active and in the same property 🛡.
  - `adult_count ≥ 1` 🛡, `child_count ≥ 0` 🛡, and occupancy ≤ the room type's limits.
  - The rate plan is active and has a price for every night. Without one, manual nightly amounts are required, along with `reservation.override_rate`.
- The nightly rows `reservation_room_rates` are snapshotted: `base_amount` (grid), `amount` (agreed) and `price_mode` (from the plan).
- It's created as header `DRAFT` with lines `DRAFT`. **Drafts hold no inventory.** Availability is only a warning at this stage.
- `confirmation_number` comes from `document_sequences`.
- The **estimated total** shown to the user comes from the engine: nightly amounts + the plan's room charge code rules. It's never stored.
- **Amending** a CONFIRMED line (dates, type, occupancy, rate plan) re-runs the full availability check, excluding the line itself, under locks. Changing dates keeps the agreed prices for surviving nights and prices new nights from the grid.

## 3. Reservation confirmation
- The header is DRAFT with at least one DRAFT line. The booker is set. Every line has `arrival_date ≥ BD`, so a stale draft must be amended first.
- After taking locks (§18), every line passes type availability for all its nights. Any assigned room passes the specific-room check.
- It's all or nothing. The header and all its DRAFT lines become CONFIRMED, and `confirmed_at` and `confirmed_by` are set.
- A line added to a CONFIRMED reservation is created directly as CONFIRMED, under the same checks.

## 4. Reservation cancellation
- **Line:** the status is DRAFT or CONFIRMED. A reason is required. If the last non-cancelled line is cancelled, the header becomes CANCELLED.
- **Header:** cancels all DRAFT and CONFIRMED lines. It's rejected if any line is CHECKED_IN.
- Inventory is released immediately. `room_id` is kept for history, and the exclusion constraint ignores non-CONFIRMED lines.
- A deposit on the folio doesn't block cancellation. The folio stays OPEN until staff post a cancellation fee (charge code `CANCEL_FEE`) or a refund. Cancelled reservations with a non-zero open folio appear in a "to resolve" list.
- **Reinstate** (CANCELLED → CONFIRMED) needs `reservation.reinstate`, `arrival ≥ BD` and a fresh availability check.

## 5. No-show
- The line is CONFIRMED and `arrival_date ≤ BD`.
- It can be applied individually at any time on or after the arrival date, or in bulk from the night-audit screen (§19.3).
- Effect: the line becomes `NO_SHOW` and all its nights are released. A no-show fee is an **explicit** optional charge (`NO_SHOW_FEE`), never automatic in the MVP. Any deposit stays on the folio to be applied or refunded.

## 6. Room assignment
- The line is CONFIRMED (a draft can't hold a room). The room is active and in the same property 🛡.
- The room type must equal the booked type, **or** the request sets `upgrade = true` (with `reservation.upgrade`). In that case the physical type's inventory must be available for every night.
- The room passes the specific-room check for `[arrival, departure)` 🛡 (exclusion constraint).
- Housekeeping status isn't checked here. Unassign is allowed until check-in.

## 7. Check-in
Preconditions:
- The line is CONFIRMED, the header is CONFIRMED, and `arrival_date = BD`. Early or late arrivals are amended first (§2).
- The assigned room (or the one given in the request) passes the specific-room check for `[BD, departure)`.
- 🛡 The room has no open stay segment.
- There is no ACTIVE OOO or OOS block covering BD.
- **Cleanliness:**
  - If `require_room_inspection_for_checkin = false` (the default), the housekeeping status must be `CLEAN` or `INSPECTED`.
  - If it's `true`, the status must be `INSPECTED`.
  - Overriding this needs `frontdesk.checkin_unready_room` plus a reason, and it's audited.
- The primary guest belongs to the tenant, and the registration fields required by the property are present.

Effect (one transaction):
1. Create a stay (IN_HOUSE, `arrival_date = BD`, `checked_in_at = now()`, `stay_number`).
2. Open a stay segment (`started_at = now()`, `start_business_date = BD`).
3. Add accompanying guests.
4. Link the reservation's OPEN unlinked folio if one exists (a deposit folio); otherwise create a folio.
5. The line becomes CHECKED_IN.
6. Write the audit log.

**Reverse check-in:** allowed on the same BD only, with no postings other than payments. The stay becomes CANCELLED, the segment is closed, the line goes back to CONFIRMED, the folio is unlinked, and the room is marked DIRTY.

## 8. Walk-in
In one transaction: create the reservation (`source_code = WALK_IN`) and its line with `arrival_date = BD`, confirm it (running the availability checks), assign the room, and check in under the §7 rules. It's a single endpoint and a single code path through the same services.

## 9. Room move
- The stay is IN_HOUSE. The target room is different, passes the specific-room check for `[BD, eff_dep)`, has no open segment 🛡, and passes the same **cleanliness rule as check-in**.
- If the type changes, the target type's inventory must be available for `[BD, departure)`.
- Effect: close the current segment (`end_business_date = BD`), open a new one, mark the old room DIRTY (`source = ROOM_MOVE`), and audit with a reason.
- The rate doesn't change unless new nightly amounts are supplied, which needs `frontdesk.rate_change`.
- A move on arrival day gives a valid zero-night segment. 🛡 `end_business_date ≥ start_business_date`.

## 10. Stay extension and shortening
- **Extend** (`new > departure_date`):
  - The current room must be free for `[old, new)`. If not, the error suggests a room move.
  - The type must have inventory for those nights.
  - New nights are priced from the line's plan grid (or manually, with permission) and inserted into `reservation_room_rates` with the plan's `price_mode`.
  - `stays.departure_date` is updated. `reservation_rooms.departure_date` keeps the original booking.
- **Shorten:** `new ≥ BD + 1` (for today, use check-out). Unposted rate rows with `stay_date ≥ new` are deleted, and this is audited.

## 11. Charge posting
- The folio is OPEN. The stay is IN_HOUSE, or the folio is open after check-out (which needs `folio.post_after_checkout`).
- The charge code is active and in the same property 🛡. `quantity > 0`, `unit_price ≥ 0`, and `0 ≤ discount ≤ base`.
- `price_mode` comes from the request if given; otherwise it's the charge code's `default_price_mode`.
- The breakdown is produced **only** by the Charge Calculation Engine, with rules from `ChargeRuleResolver(charge_code)` as of now. See [04-charge-calculation.md](04-charge-calculation.md).
- The item gets `business_date = BD` (it's never back-dated). `service_date` may be earlier than BD, but not later.
- It persists a `folio_items` row and one `folio_item_components` row per applied tax or service charge. 🛡 Totals equal the component sums (deferred constraint trigger).
- **Corrections:** items are never updated or deleted 🛡 (trigger and grants).
  - A **reversal** is allowed on the same BD only. It's an exact negation, including negated component rows, and an item can be reversed only once 🛡.
  - An **ADJUSTMENT** is used on any later BD. It's a negative amount calculated by the engine with the charge code's current rules, and it needs `folio.adjust` and a reason.
- 🛡 `idempotency_key` is unique per property. A retry returns the original item.

## 12. Payment posting
- The folio is OPEN and `amount > 0` 🛡. The currency is implicitly the property currency. The method is `CASH`, `CARD`, `BANK_TRANSFER` or `OTHER`.
- In one transaction: insert a `payments` row (POSTED, `business_date = BD`) and a `folio_items` row (PAYMENT, `amount = −payment.amount`, no components).
- Overpayment is allowed and is resolved by a refund.
- **Void:** only on the same BD as the payment, only if the folio is OPEN, and with a reason. The payment becomes VOIDED and a reversal item is posted.
- **Refund:** a new payment with `payment_type = REFUND` and `refund_of_payment_id` set. The ledger item is positive. The refund amount must be ≤ the original minus earlier refunds, and the original must be POSTED.
- 🛡 `idempotency_key` is unique per property.

## 13. Deposit
- A deposit is a payment on the reservation's folio before check-in. The reservation is DRAFT or CONFIRMED. The folio is created on first use (`stay_id NULL`).
- At check-in, the folio is linked to the first stay of the reservation that checks in.

## 14. Check-out
Preconditions:
- The stay is IN_HOUSE.
- Either `departure_date ≤ BD`, or it's an **early departure**. In that case `departure_date` is set to BD and the unposted future rate rows are deleted (audited).
- Every OPEN folio linked to the stay has a **balance of exactly 0**.

Effect (one transaction): close the segment (`end_business_date = BD`), set the stay to CHECKED_OUT (`checked_out_at = now()`), close the folios, set the room to DIRTY (`source = CHECKOUT`) and write the audit log.

## 15. Room becomes dirty
The room is set to DIRTY with a log entry on check-out, room move (the vacated room), reverse check-in, night audit (occupied rooms, if `night_audit_marks_occupied_dirty`, which defaults to true), and manual changes.

## 16. Housekeeping completion
- Allowed transitions: `DIRTY→CLEANING`, `CLEANING→CLEAN`, `DIRTY→CLEAN`, `CLEAN→INSPECTED`, and `any→DIRTY`. A no-op is rejected.
- `→INSPECTED` needs `housekeeping.inspect`.
- Housekeeping is independent of occupancy and blocks. Every change writes `room_housekeeping` + `housekeeping_logs` together.

## 17. OOO / OOS
- `start_date ≥ BD` and `end_date > start_date` 🛡. 🛡 No overlapping ACTIVE block on the same room.
- There must be no conflicting open stay segment (`[BD, eff_dep)`) or CONFIRMED assigned line in `[start, end)`. If there is, the API returns the conflicts so they can be unassigned or moved first.
- **OOO only:** the room type must stay at `available ≥ 0` for every night. Otherwise the request is rejected with the oversold nights.
- Early release means moving `end_date` (≥ BD, > start). Extending re-checks the added nights. Cancel means `CANCELLED`. A reason is required and everything is audited.

## 18. Double-booking prevention and lock protocol

**Layer 1: constraints.**
- EXCLUDE on overlapping CONFIRMED assigned lines per room.
- EXCLUDE on overlapping ACTIVE blocks per room.
- UNIQUE open segment per room and per stay.
- UNIQUE live stay per line.
- Composite FKs.

**Layer 2: lock order.** Every inventory- or allocation-changing write takes these locks in order:
1. `business_days` OPEN row, `FOR SHARE` (`FOR UPDATE` for night audit).
2. `room_types` rows `FOR UPDATE`, sorted by id: every type whose inventory changes.
3. `rooms` rows `FOR UPDATE`, sorted by id: every physical room being allocated or blocked.
4. Aggregate roots (`reservations`, `stays`, `folios`) `FOR UPDATE`, plus a `version` check.

The availability queries run **after** the locks are held. Always taking them in the same order prevents deadlocks.

**Layer 3: service validation.** It returns precise errors: which night, which reservation, which block.

Isolation level is READ COMMITTED with explicit locks. Postings that don't touch inventory (charges, payments) only take step 1 and the `folios` row.

## 19. Night audit

### 19.1 Blockers (all must be zero to run)
| Blocker | Definition | How staff resolve it |
|---|---|---|
| **Unresolved arrivals** | Lines with `status = CONFIRMED` and `arrival_date ≤ BD` | Check in, cancel, amend dates, or no-show (individually or in bulk) |
| **Unresolved departures** | Stays with `status = IN_HOUSE` and `departure_date ≤ BD` | Check out, or extend |
| **Missing room rates** | IN_HOUSE stays with no `reservation_room_rates` row for night BD | Set a rate |

**Warnings** (they don't block): DRAFT lines with `arrival_date ≤ BD` (stale drafts), open folios on cancelled or no-show reservations, and rooms still OOO whose block ends at BD.

### 19.2 Preview
`GET /night-audit/preview` returns the BD, local time, blockers with the affected items (confirmation number, guest, room), warnings, and whether the local-time guard passes. For example: *"2 unresolved arrivals: RES-003, RES-004."*

### 19.3 Bulk "Mark remaining as no-show" (an explicit staff action)
- `POST /night-audit/no-shows` with **the exact `reservation_room_ids` the staff saw** and a confirmation.
- It's all or nothing. Each id must still be CONFIRMED with `arrival_date ≤ BD`. If any id changed in the meantime (for example, the guest arrived and was checked in), the whole request fails with 409 and the UI refreshes the preview. **The server never selects "all remaining" on its own**, so staff always confirm exactly what gets no-showed.
- It requires `nightaudit.no_show` and applies the §5 effects to each line. One audit log entry is written per line, plus one for the bulk action.

### 19.4 Run
`POST /night-audit/run` with `{ business_date }` (the date the user believes they're closing, which guards against double clicks and stale screens). In one transaction:
1. Lock the OPEN `business_days` row `FOR UPDATE`. It must equal the requested date, otherwise 409.
2. **Local-time guard:** `BD + 1 ≤ property local date + 1`, which means BD ≤ the local date. This stops the audit being run twice in one night and pushing the BD ahead of the real calendar. Running it before midnight (closing *today*) is allowed.
3. Re-evaluate the blockers inside the lock. If there are any, return 409 with the list.
4. For every IN_HOUSE stay: post a ROOM charge for night BD. The inputs are the nightly amount and `price_mode` from `reservation_room_rates`, and the charge code is the one on that night's rate plan (`room_charge_code_id`). The rules come from the resolver and the breakdown from the engine, with `source = NIGHT_AUDIT` and `service_date = BD`. It goes to the stay's OPEN folio (the oldest one, if there are several). 🛡 There's a partial unique index on `(stay_id, service_date)` for night-audit room charges.
5. If `night_audit_marks_occupied_dirty` is set, mark rooms with an open segment DIRTY (`source = NIGHT_AUDIT`).
6. Close the day: `status = CLOSED`, `closed_at`, `closed_by`, and a `summary` (occupied rooms, arrivals, departures, no-shows, room revenue net, service, tax, and payments by method).
7. Insert `BD + 1` as OPEN.
8. Write the audit log.

It's a single transaction, so an audit either fully happens or doesn't happen at all. For MVP-scale hotels (up to a few hundred rooms) this runs in well under a second.

## 20. Guest data access (tenant-wide profiles)
| Capability | Requirement |
|---|---|
| Search or view guests linked to my properties (`origin_property_id`, or any reservation or stay there) | `guest.read` at any of those properties |
| Search the **whole tenant's** guest profiles (for reuse across hotels) | `guest.search_all` (in the default front-desk role) |
| Create or edit a profile | `guest.write` at the property it's used from. A new profile gets `origin_property_id` set. |
| See reservations and stays **at properties I can't access** | `guest.history_all_properties`. Without it, history is filtered to the user's properties. |
| Tenant admin | Everything |

Reservations, stays, folios and payments are still only readable with a grant at their own property.

## 21. Users and access
- 🛡 `UNIQUE (tenant_id, lower(email))`. Login needs `tenant_code` + email + password.
- A user reaches a property only through `user_properties` (one role per property) or `is_tenant_admin`.
- Every property-scoped request checks: the property belongs to the token's tenant **and** the user has access. After that, permission checks use the role's codes.
- A deactivated user's sessions are revoked immediately.

## 22. Billing configuration rules
- Taxes and service charges: `0 ≤ rate_percent ≤ 100` 🛡. The code is unique per property 🛡. Once referenced by a posted component they can't be deleted, only deactivated.
- **Changing a rate** affects only future postings, because posted items keep their snapshot. It's audited, and the UI warns that in-house guests' next room charge will use the new rate.
- A charge code's mappings may reference only **active** taxes and service charges of the same property 🛡 (composite FK, and the service checks they're active). Deactivating a tax that is still mapped is rejected until it's unmapped.
- There must be at least one active ROOM-category charge code before a rate plan can be created.
- The property's currency and decimals can't be changed once `folio_items` exist for it.
