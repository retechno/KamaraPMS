# Operations (revision 3): Steps 12–14

> BD = the OPEN business date. 🛡 means a DB constraint also enforces the rule.

---

## Step 13: Business Day lifecycle (read this before night audit)

### 13.1 Clocks
| Clock | Source | Used for |
|---|---|---|
| Server time | DB `now()` (UTC). In Go, `platform/clock`, which is injectable. | `*_at` columns: actual check-in and check-out, `transaction_at`, `paid_at`, audit `created_at` |
| Property local time | Server time converted to `properties.timezone` | Display, and the night-audit guard |
| **Business date** | `business_days` row with status OPEN | Every `*_date` with an operational or financial meaning: stay arrival, nights, `folio_items.business_date`, `payments.business_date`, `reservation_date`, no-show eligibility, reports |

**Code rule:** no business decision may call `CURRENT_DATE`, `now()::date` or `time.Now()` to get a date. The only accessor is `BusinessDayService.Current(ctx, propertyID)`, and a linter rule enforces this in the domain packages.

### 13.2 Lifecycle
```
Property created ──▶ INSERT business_days(opening_date, OPEN)      (setup, once)
        │
        ▼
   OPEN (BD) ──night audit (one tx)──▶ CLOSED (BD, summary)  +  INSERT (BD+1, OPEN)
```
| Rule | Enforcement |
|---|---|
| Exactly one OPEN day per property | 🛡 partial UK |
| Days are consecutive, with no gaps | 🛡 insert trigger (`max + 1`) |
| Only night audit closes and opens days | `BusinessDayService.CloseAndOpenNext` is package-private to the `nightaudit` wiring |
| A CLOSED day is immutable and never reopens | 🛡 trigger |
| Financial rows reference a real day | 🛡 FK `(property_id, business_date) → business_days` |
| Writes happen only on the OPEN day | **Application gate:** the service locks the OPEN row `FOR SHARE` and compares dates (`RequireOpenBusinessDay`). 🛡 **Database safety net (migration 00065):** a trigger refuses a ledger or cash row (folio items, payments, room-night postings, city ledger receipts, adjustments and invoices, shift movements and shifts) whose business date is a CLOSED day, reading the day `FOR SHARE` so it is serialised with the night audit (`BUSINESS_DAY_CLOSED`). It covers those tables only: journals, supplier and tax documents and operational records are not covered (audit F-07) |
| No write interleaves with closing | Writers take `FOR SHARE` and night audit takes `FOR UPDATE` |
| BD never runs ahead of reality | **Night-audit time guard:** closing BD is allowed only if `local_date > BD`, **or** `local_date = BD AND local_time ≥ properties.night_audit_earliest_time` (default 20:00) |
| Overdue audit is visible | `GET /business-date` → `night_audit_overdue = local_date > BD + 1` |
| Corrections to closed days | Adjustments on the current BD, never back-posting |

**Example:** the server time is 1 Oct 2026 02:30 local, and the BD is 30 Sep. Payments get `business_date = 30 Sep`. Night audit is allowed, because local date 1 Oct > BD 30 Sep. After it, the BD is 1 Oct. A second audit at 02:30 is **refused**, because local date = BD and 02:30 is earlier than 20:00. The next audit can run from 20:00 on 1 Oct. An audit at 23:00 on 30 Sep (before midnight) is also allowed.

---

## Step 12: Night Audit

### 12.1 Design
`NightAuditService` is an **orchestrator**. It asks registered **checks** for blockers and delegates all posting to `RoomChargePostingService`. It contains no calculation or posting code, and it **never changes guest, reservation or stay status**.

```go
type AuditCheck interface {
    Code() string                                         // e.g. UNRESOLVED_ARRIVALS
    Run(ctx, AuditContext) ([]Finding, error)              // Finding{Severity: BLOCKER|WARNING, Items}
}
```
The checks registered in the MVP (in this order):

| # | Check | Severity | Definition | Staff resolution |
|---|---|---|---|---|
| 1 | `BUSINESS_DAY_CONSISTENCY` | Blocker | There's one OPEN day, it equals the requested date, the previous day is CLOSED, and the time guard (§13.2) passes | Wait, or refresh |
| 2 | `UNRESOLVED_ARRIVALS` | Blocker | `reservation_rooms.status = CONFIRMED AND arrival_date ≤ BD` | Check in, cancel, amend dates, or **"Mark remaining as no-show"** |
| 3 | `UNRESOLVED_DEPARTURES` | Blocker | `stays.status = OPEN AND departure_date ≤ BD` | Check out, extend, or correct the departure date |
| 4 | `EXPECTED_CHARGES` | Blocker if any are `ERROR` | ExpectedChargeEngine over all OPEN stays up to BD | Fix the cause (add a nightly rate, fix the charge code, open a folio) |
| 5 | `MISSING_CHARGES` | Informational before the run; **blocker after the posting step** | `READY` items (`N < BD` = missing, `N = BD` = tonight) | "Post missing charges", or let the run post them |
| 6 | `INVALID_CHARGES` | Blocker | ExpectedChargeEngine `FindInvalid` (posted nights outside the stay, or on a cancelled stay) | Reverse or adjust |
| 7 | `CASHIER_SESSIONS` | none in the MVP | A no-op hook; `cashier_shifts` comes later | none |
| 8 | Warnings | Warning | Stale DRAFT lines with `arrival ≤ BD`; OPEN folios with a non-zero balance on cancelled or no-show reservations; blocks ending at BD | Optional |

Duplicate charges can't exist, because of 🛡 the register's partial UK. Check 6 covers the remaining "invalid" cases.

### 12.2 Preview (read-only)
`GET {P}/night-audit/preview` runs every check and a dry run of the room charges. It returns the blockers, warnings and `can_run`. For example: *"2 unresolved arrivals: RES-003, RES-004"* and *"Unposted room charge for Stay STY-001 / Room 201 (night 29 Sep)"*.

### 12.3 Bulk "Mark remaining as no-show" (an explicit staff action)
- The request carries the **exact `reservation_room_ids` shown to the staff** and `confirm: true`.
- It's one transaction:
  1. Lock the BD `FOR SHARE`.
  2. Lock the lines `FOR UPDATE` (sorted).
  3. Check that every id is still `CONFIRMED` with `arrival_date ≤ BD`. If **any** id isn't, the whole request fails with 409 `NO_SHOW_SET_CHANGED` and lists the ones that changed.
  4. Set each line to `NO_SHOW` (with `no_show_at` and `no_show_by`).
  5. Write the audit logs.
- It **never** selects "all remaining" on the server. Fees are not posted automatically.

### 12.4 Post missing charges (an explicit staff action, or a step inside the run)
`POST {P}/night-audit/room-charges` → `RoomChargePostingService.Post(trigger = MANUAL)` in its own transaction. After the commit, the response contains the re-run `EXPECTED_CHARGES` and `INVALID_CHARGES` results (§43, step 9).

### 12.5 Run (one transaction)
```
POST {P}/night-audit/run { business_date }
 1. pg_try_advisory_xact_lock(hash('night_audit', property_id))   → else 409 NIGHT_AUDIT_IN_PROGRESS
 2. SELECT business_days … status = OPEN FOR UPDATE               → must equal the request (409 BUSINESS_DATE_MISMATCH); time guard → else 409 NIGHT_AUDIT_TOO_EARLY
 3. Checks 1, 2, 3                                               → any blocker: 409 NIGHT_AUDIT_BLOCKED
 4. Expected charge analysis (check 4)                           → any ERROR: 409 NIGHT_AUDIT_BLOCKED
 5. RoomChargePostingService.Post(trigger = NIGHT_AUDIT, all stays, BD)
 6. Revalidate: ExpectedChargeEngine must return no READY or ERROR items up to BD
 7. Check 6 (invalid charges), check 7 (cashier hook)
 8. Housekeeping: if night_audit_marks_occupied_dirty, rooms with an open segment → DIRTY
    (source NIGHT_AUDIT). This is housekeeping, not guest or stay status.
 9. Daily closing calculations → summary jsonb:
    rooms total / OOO / OOS / sellable / occupied, arrivals, departures, no-shows,
    room revenue {net, service, tax} and revenue by charge_type, payments by method,
    occupancy % (occupied / (total − OOO − house use)), ADR (room revenue / paid room nights), RevPAR (room revenue / available).
    Complimentary rooms are occupied but not sold; house use rooms are not occupied, not sellable and not available, and
    their nights (like complimentary ones) post as zero, so they do not dilute the ADR or lower the occupancy
10. BusinessDayService.CloseAndOpenNext: CLOSED (closed_at, closed_by, summary), INSERT BD+1 OPEN
11. audit_logs: night_audit.completed
COMMIT
```
Any blocker at any step rolls back the **whole** run, including the charges posted in step 5. The preview and "post missing charges" let staff settle everything before running.

---

## Step 14: Reservation, stay and availability rules

### 14.1 Availability engine
Notation: `eff_dep(S) = GREATEST(S.departure_date, BD + 1)` for OPEN stays (an overstay keeps holding the room). `eff_type(L)` = the assigned room's type if a room is assigned, otherwise the booked type.

```
sellable(T, n)  = active rooms of type T
                − rooms of type T with an ACTIVE OOO **or OOS** block where start_date ≤ n < end_date
demand(T, n)    = |{ L ∈ reservation_rooms : L.status = CONFIRMED, eff_type(L) = T, L.arrival_date ≤ n < L.departure_date }|
                + |{ S ∈ stays : S.status = OPEN, current room type = T, BD ≤ n < eff_dep(S) }|
available(T, n) = sellable(T, n) − demand(T, n)
Request [a, d) for k rooms of T is available ⇔ ∀ n ∈ [a, d): available(T, n) ≥ k      (n ≥ BD)
```
- Overlap is always `a1 < d2 AND a2 < d1`, so a departure date doesn't block the next arrival.
- These don't consume inventory: `DRAFT`, `CANCELLED`, `NO_SHOW`, `CHECKED_IN` and `COMPLETED` lines. A CHECKED_IN line is counted through its stay, so it's never counted twice.
- Room moves: only the **current** segment counts, and closed segments are history.
- There's no overbooking: no operation may make `available < 0`. A restriction hook (min stay, closed to arrival, stop sell) is reserved for later.

**Specific room R free for `[a, d)`** requires all of these:
- R is active,
- there's no ACTIVE block overlapping,
- there's no other CONFIRMED line assigned to R overlapping 🛡, and
- there's no open segment on R with `[BD, eff_dep)` overlapping.

**Derived room status for date D** (default BD):
- `OCCUPIED` if R has an open segment (for a future D: the stay's `eff_dep > D`),
- otherwise `RESERVED` if a CONFIRMED line is assigned to R covering D,
- otherwise `VACANT`.

This is combined with the housekeeping status and the active block. Nothing is stored.

### 14.2 Reservation rules
| Operation | Rules |
|---|---|
| **Create** (draft) | Every line has `arrival ≥ BD` and `departure > arrival` 🛡 (≤ 365 nights). The type is active. Occupancy is within `max_adult`, `max_child` and `max_occupancy`. The plan is active with a room charge code. Every night has a grid price, or an override (with permission). Nightly rows snapshot the rate plan, charge code, price mode, base rate, discount and amount. `reservation_date = BD`. The confirmation number comes from the sequence. Drafts hold **no** inventory. |
| **Confirm** | A booker is set. All lines are DRAFT with `arrival ≥ BD`. Under locks, type availability holds for every night, and assigned rooms pass the specific-room check. It's all or nothing: the header and lines become CONFIRMED. |
| **Amend line** | Only CONFIRMED or DRAFT lines. Availability is re-checked excluding the line itself. Surviving nights keep their snapshot. New nights are priced from the grid. |
| **Assign room** | The line is CONFIRMED. The room's type equals the booked type, or `upgrade` is set (with permission, and the physical type's inventory is checked). The specific-room check passes 🛡. |
| **Cancel** (line or header) | The line is DRAFT or CONFIRMED, and a reason is given. The header becomes CANCELLED when no active line remains. A deposit folio stays OPEN until resolved (fee or refund). |
| **No-show** | The line is CONFIRMED with `arrival ≤ BD`. A staff action only (individual, or the bulk action §12.3). Releases inventory. A fee is optional and posted explicitly. |
| **Reinstate** | CANCELLED → CONFIRMED, with permission. Needs `arrival ≥ BD` and a fresh availability check. |

### 14.3 Stay rules
| Operation | Rules |
|---|---|
| **Check-in** | The line and header are CONFIRMED and `arrival_date = BD`. The room is free for `[BD, departure)`. 🛡 The room has no open segment. There's no active OOO/OOS on BD. **Cleanliness:** with `require_room_inspection_for_checkin = false`, CLEAN or INSPECTED is required; with `true`, INSPECTED is required. An override needs `frontdesk.checkin_unready_room` plus a reason. Effects: the stay (OPEN, `arrival_date = BD`, `actual_check_in_at = now()`), an open segment (`start_bd = BD`), accompanying guests, the folio (link the open unlinked folio or create one), the line becomes `CHECKED_IN`, and an audit entry. Occupancy becomes OCCUPIED because of the open segment. **Nothing else is written.** |
| **Walk-in** | Create, confirm, assign and check in, in one transaction, through the same services (`source = WALK_IN`, `arrival = BD`). |
| **Room move** | The stay is OPEN. The target is a different room, free for `[BD, eff_dep)`, has no open segment 🛡, and passes the same cleanliness rule. If the type differs, the target type's inventory is checked for `[BD, departure)`. Effects: the current segment is closed (`check_out_at = now()`, `end_bd = BD`), a new segment opens (`start_bd = BD`), the old room becomes DIRTY, and an audit entry is written with the reason. The nightly rates are unchanged unless new amounts are supplied (with permission) for **unposted** nights only. Tonight's room charge follows the segment that covers BD (the new room). |
| **Extend** | The new departure is later than the current one. The current room is free for `[old, new)`, otherwise 409 with a suggestion to move rooms. Type inventory is available. Nightly rows are added (grid or override). `stays.departure_date` is updated, and the line keeps its original dates. |
| **Shorten or correct departure** | The new departure is ≥ BD + 1, and it isn't earlier than or equal to a night that's already been posted (reverse those first). Unposted nightly rows beyond the new date are deleted. |
| **Check-out** | The stay is OPEN. **Departure handling:** if the local date = BD, set `departure_date = min(departure_date, BD)` (an early departure needs confirmation). If the local date > BD (after midnight, before the audit), set `departure_date = min(departure_date, BD + 1)`, so the night of BD was consumed and must be charged. **Required charges:** `RoomChargePostingService.Post(trigger = CHECK_OUT, stay)`. After that, the engine must show no READY or ERROR items for the stay, otherwise 409. **Folio policy (MVP):** the balance of every OPEN folio of the stay is exactly 0. Effects: folios CLOSED, segment closed (`end_bd = BD`), stay CHECKED_OUT (`actual_check_out_at`), line COMPLETED, housekeeping DIRTY (`source = CHECK_OUT`), audit. **Occupancy becomes VACANT** because no open segment remains. |
| **Reverse check-in** | Same BD only. No CHARGE items are posted. Stay → CANCELLED, segment closed, line → CONFIRMED, folio unlinked, room → DIRTY. |

### 14.4 Housekeeping and room blocks
- **Housekeeping transitions:** `DIRTY→CLEANING→CLEAN→INSPECTED`, `DIRTY→CLEAN` and `any→DIRTY`. `INSPECTED` needs `housekeeping.inspect`. Each change writes the current row and a log row.
- **Room becomes DIRTY** on check-out, the vacated room of a move, reverse check-in, night audit (if configured), and manual changes.
- **Room blocks:** `start ≥ BD`, `end > start` 🛡. 🛡 No overlapping ACTIVE block on a room. There must be no conflicting open segment or CONFIRMED assigned line (the API lists the conflicts). **Both OOO and OOS** must keep `available(T, n) ≥ 0`. Early release moves `end_date` to ≥ BD. A reason is required and everything is audited.

### 14.5 Charges and payments (summary)
- Manual charges use `FolioPostingService.PostCharge`, and `charge_type = ROOM` is **rejected** there.
- Room charges go only through `RoomChargePostingService`.
- Payments, deposits, voids and refunds go only through `PaymentService`.
- Reversals are same-day. Later corrections are ADJUSTMENTs with a reason.
