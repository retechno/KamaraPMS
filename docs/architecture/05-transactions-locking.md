# Transactions and Locking (revision 3): Step 15

## 1. Strategy

- **Isolation:** `READ COMMITTED` with **explicit, ordered row locks**, plus **constraints as a backstop**. We don't use SERIALIZABLE: explicit locks give predictable behaviour and precise errors without retry loops, and hotel-scale contention is low.
- **One transaction per use case.** `TxManager.WithinTx(ctx, fn)` opens it. Services receive the transaction through `ctx`, and orchestrators (check-in, check-out, night audit) compose several services inside one transaction.
- **Audit logs are written in the same transaction** as the change.
- **Constraint violations are mapped** to domain errors:
  - `23P01` (exclusion) → `ROOM_NOT_AVAILABLE`
  - `23505` (unique) on the idempotency key → return the original result
  - `23505` on the open-segment index → `ROOM_OCCUPIED`
- Lock waits are short. Every transaction sets `SET LOCAL lock_timeout = '5s'`, and a timeout returns 409 `RESOURCE_BUSY`, which is retriable.

### 1.1 Global lock order (prevents deadlocks)
| # | Lock | Mode | Taken by |
|---|---|---|---|
| L0 | `pg_try_advisory_xact_lock(hash('night_audit', property_id))` | advisory, try | Night audit only (fails fast) |
| L1 | `business_days` OPEN row | `FOR SHARE` (writers) / `FOR UPDATE` (night audit) | Every business-dated write |
| L2 | `room_types` rows, sorted by id | `FOR UPDATE` | Anything that changes type inventory |
| L3 | `rooms` rows, sorted by id | `FOR UPDATE` | Anything that allocates or blocks a specific room |
| L4 | Aggregate roots, sorted by id: `reservations` → `stays` → `folios` → `payments` → `companies` (44) → `booking_groups` (45) | `FOR UPDATE` + `version` check (a booking joining a group takes `FOR SHARE`) | The aggregates being modified. A city ledger transfer, receipt, invoice or void locks the company; a maintenance request is locked (plain row lock, after the business day and before the rooms it blocks) by every change to it, and a lost item by its hand-back or disposal; a booking or an edit of a group locks the group |
| L5 | `document_sequences` row | `UPDATE … RETURNING` | Number allocation (last, and held briefly) |

A transaction may skip levels, but it may **never take a lower-numbered lock after a higher-numbered one**.

## 2. Operations

| # | Operation | Locks | Steps inside the transaction | Backstops |
|---|---|---|---|---|
| 1 | **Confirm reservation** | L1 share · L2 (every eff_type of its lines) · L3 (assigned rooms) · L4 reservation | Version check → per line and night: `available(T, n) ≥ 1` (counting other lines only) → specific-room check for assigned lines → header and lines CONFIRMED → audit | EXCLUDE on assigned lines |
| 2 | **Assign room** | L1 · L2 (booked type and the room's type) · L3 room · L4 reservation | Line CONFIRMED → type rule or upgrade inventory → specific-room check → set `room_id` → audit | EXCLUDE (`23P01` → `ROOM_NOT_AVAILABLE`) |
| 3 | **Check in** | L1 · L2 (the room's type) · L3 room · L4 reservation, then any folio | Line and date checks → room free, no block, housekeeping rule → sequence (STAY, FOLIO if new) → INSERT stay, stay_room, stay_guests → link or create the folio → line CHECKED_IN → audit | UK open segment per room and per stay · UK live stay per line · UK GUEST folio per stay |
| 4 | **Room move** | L1 · L2 (old and new type, if different) · L3 (both rooms, sorted) · L4 stay | Target free for `[BD, eff_dep)` + cleanliness + type inventory → close the segment → insert the new segment → old room DIRTY + log → audit | UK open segment per room and per stay |
| 5 | **Post charge** | L1 share · L4 folio | Validate → ChargeCalculationService → INSERT the item and components → audit | Idempotency UK · append-only trigger · deferred totals trigger · CHECK constraints |
| 6 | **Post payment** | L1 share · L4 folio (+ original payment for a refund) | Idempotency lookup → sequence → INSERT payment → FolioPostingService.PostPaymentEntry → audit | UK idempotency key · UK `folio_items.payment_id` · CHECK `amount > 0` |
| 7 | **Check out** | L1 share · L4 stay → its folios (sorted) | Departure handling → **RoomChargePostingService** (CHECK_OUT) → no READY or ERROR remain → balance = 0 for each folio → close the folios → close the segment → stay CHECKED_OUT → line COMPLETED → housekeeping DIRTY → audit | Register UK · CHECKs |
| 8 | **Post missing charges** | L1 share · L4 stays (sorted) → folios | Expected charges (after locks) → for each READY: post the charge and insert a register row → audit summary | **Register partial UK** |
| 9 | **Close business day** | L0 · L1 **update** · L4 stays (via posting) | Checks → RoomChargePostingService → revalidate → invalid charges → housekeeping → summary → status CLOSED | UK one OPEN · immutability trigger |
| 10 | **Open next business day** | Same transaction as #9 | INSERT BD+1 OPEN | 🛡 UK one OPEN · consecutive-insert trigger · UK `(property, business_date)` |
| – | Bulk no-show | L1 · L4 lines (sorted) | Every id still unresolved, otherwise the whole request fails → NO_SHOW → audit | none |
| – | Create or extend OOO/OOS block | L1 · L2 · L3 room | Conflicts → inventory ≥ 0 → insert or update | EXCLUDE on blocks |
| – | Extend stay | L1 · L2 · L3 current room · L4 stay | Room free for `[old, new)` + inventory → add nightly rows → update the departure | none |

## 3. Race conditions and how each is prevented

| Race | Scenario | Prevention |
|---|---|---|
| **Double booking (type level)** | Two clerks confirm the last DLX for 10–12 Oct at the same time | Both need L2 on DLX. The second waits, then counts the first's committed line, sees `available = 0`, and gets 409. |
| **Double booking (room level)** | Two clerks assign room 305 for overlapping dates | L3 on room 305 serialises them. The second fails the specific-room check. 🛡 The EXCLUDE constraint rejects it even if the code has a bug. |
| **Concurrent check-in to the same room** | Two stays check into room 305 | L3 serialises them. 🛡 `UNIQUE (room_id) WHERE check_out_at IS NULL` rejects the second. |
| **Same line checked in twice** (a double click) | Two check-in requests for one line | L4 reservation lock + line status check. 🛡 UK live stay per line. The Idempotency-Key replays the first response. |
| **Room move vs. check-in** into the same target room | A move and a check-in both want room 410 | Both take L3 on 410 in sorted order. 🛡 The open-segment UK. |
| **Block vs. assignment** | OOO created while a clerk assigns the same room | Both take L3 on the room. Whoever is second sees the other's row and fails. |
| **Duplicate room charge** | Night audit and "post missing charges" run together, or a retried request | L4 stay locks serialise them. The re-evaluation after locking sees ALREADY_POSTED. 🛡 The register's partial UK. |
| **Room charge posted twice after a same-day room move** | Segments 201 and 305 both touch 30 Sep | The register key excludes `stay_room_id` (review A4). The night belongs to exactly one segment. |
| **Duplicate payment** | Network retry | 🛡 `UNIQUE (property_id, idempotency_key)`, which returns the original. |
| **Over-refund** | Two refunds of the same payment at once | Refund locks the original payment row (L4). The sum of refunds is checked after the lock. |
| **Posting while the day closes** | A payment at 23:59:59 during night audit | The writer holds L1 `FOR SHARE`. Night audit's `FOR UPDATE` waits for it, or the writer waits and then sees the new BD. |
| **Concurrent night audit** | Two tabs click Run | L0 advisory try-lock → 409 `NIGHT_AUDIT_IN_PROGRESS`. L1 `FOR UPDATE`. The request's `business_date` must match. |
| **No-show vs. late check-in** | Staff mark no-show while the guest is checked in at another desk | Both lock the line (L4). The bulk action verifies the exact id set, and any change fails the whole request. |
| **Lost update on a reservation edit** | Two browser tabs edit the same reservation | `version` check → 409 `VERSION_CONFLICT`. |
| **Closing a folio while a charge posts** | Check-out while the restaurant posts a charge | Both lock the folio (L4). A charge after the close sees CLOSED and is rejected. |
| **Currency change after transactions** | An admin edits the currency | 🛡 The currency-lock trigger. |

## 4. Testing the guarantees
- Integration tests run against real PostgreSQL (testcontainers). Every race above has a test that starts two goroutines with a barrier and asserts exactly one success and a defined error for the other.
- Constraint tests insert violating rows directly through SQL, to prove the backstops work without the application.
- Night audit is tested for idempotency (run twice, and run after a partial manual posting) and for rollback (a blocker in step 7 leaves no postings behind).
