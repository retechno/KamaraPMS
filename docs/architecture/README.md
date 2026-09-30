# KamaraPMS: Architecture (revision 3)

| Step | Document | Content |
|---|---|---|
| 1–3 | [00-review.md](00-review.md) | Review, contradictions, missing entities, edge cases, risks and corrections |
| 4 | [01-domain-model.md](01-domain-model.md) | Final domain model, aggregates, source vs derived, state machines, Go structure |
| 5–6 | [02-database-schema.md](02-database-schema.md) | Mermaid ERD + 34-table PostgreSQL schema (PK/FK/UK/CHECK/IDX) |
| 7–11 | [03-financial-engines.md](03-financial-engines.md) | Charge Calculation Engine, Expected Charge Engine, RoomChargePostingService, FolioPostingService, PaymentService |
| 12–14 | [04-operations.md](04-operations.md) | Night audit, business day lifecycle, reservation, stay and availability rules |
| 15 | [05-transactions-locking.md](05-transactions-locking.md) | Transaction and locking strategy, race analysis |
| 16 | [06-api.md](06-api.md) | REST contracts, including transaction requirements |
| 17 | [07-milestones.md](07-milestones.md) | MVP milestones M0–M15 |

Status: **approved**. The DDL is in [`/migrations`](../../migrations) (goose, 11 files, 34 tables). `scripts/db-test.sh` checks it with 80 schema tests (`db/tests/schema_test.sql`) plus an up/down/up cycle, and passes on PostgreSQL 16 and 18.
**Implementation:**
- **M0** (foundation) is complete: the platform kernel, the Go and Vue shells, CI and the lint guards. The runtime lock-order enforcement is `db.LockRows` / `db.EnterLockLevel`.
- **M1** (tenancy and business day) is complete: property setup, `BusinessDayService` (the only source of the business date, including the night-audit time guard and `CloseAndOpenNext` for M13), gapless document numbers, the audit writer, and sqlc.
  - Charge-code seeding on property creation moved to M5 (done there).
- **M2** (identity and access) is complete. JWT access tokens (15 minutes, in memory) and rotating refresh tokens in an httpOnly `SameSite=Strict` cookie.
  - Refresh-token reuse ends all of a user's sessions. Migration 00012 adds `user_sessions.revoked_reason`, so detection only fires for rotated tokens, not for a stale tab after logout. There is a 30-second grace window for tabs that refresh at the same moment.
  - Sessions are re-checked on every request, so revocation is immediate.
  - Passwords use argon2id (OWASP parameters). Login gives one generic error, has timing equalisation and is rate-limited (in-memory per instance, Redis later).
  - Authorization is per property: 404 without access, 403 without the permission. Users, roles and property creation are for tenant administrators.
  - The M1 development header login was removed.

- **M3** (rooms, blocks, housekeeping) is complete. No migration: the tables already exist in 00004.
  - `internal/rooms` owns room types, rooms and OOO/OOS blocks. `internal/housekeeping` owns the current status, its append-only log and the board; `housekeeping.Service.MarkDirty` is the entry point M10/M12/M13 use for system-driven changes (source CHECK_OUT, ROOM_MOVE, NIGHT_AUDIT, CHECK_IN_REVERSAL).
  - Conflict checks already read the M8/M10 tables: a block, a room type change or a room deactivation is rejected (409 `ROOM_BLOCK_CONFLICT` / `ROOM_IN_USE`, with `context.conflicts`) while an open stay segment or a CONFIRMED assigned line holds the room. An overstay holds the room through tonight (`GREATEST(departure, BD + 1)`).
  - The type-level `available ≥ 0` check (`INVENTORY_OVERSOLD`) on blocks, block extensions, type changes and deactivation landed with the availability engine in M8. Room type deactivation only checks active rooms and future CONFIRMED lines of the type, as specified.
  - Room type codes are immutable (PATCH rejects `code`). `GET {P}/room-status` (the full derived board) stays in M10; the housekeeping board already carries derived occupancy and the active block.
  - The housekeeping board is not paginated (one row per active room, capped at 2,000).

- **M4** (guests) is complete. Migration 00013 adds `tenant_sequences` (gapless tenant-wide guest numbers) and the SQL function `guest_linked_to`, the single definition of "a guest is linked to a property".
  - `auth.Authorizer.PropertiesWith(perm)` lists the caller's properties holding a permission. Tenant-wide resources use it to derive visibility; the visibility, write and history rules are in [06-api.md §8](06-api.md).
  - Duplicate hints never block creation; look-alikes at properties the caller cannot see are counted, not shown.
  - Search is prefix and token based on the existing btree indexes. `pg_trgm` (fuzzy matching) is still later. Phone numbers are compared by digits only, with no country-code normalisation.
  - Merging duplicate profiles is not part of M4.

- **M5** (billing configuration) is complete. Migration 00014 adds the SQL function `seed_charge_codes` (the single definition of the ten standard charge codes): `CreateProperty` runs it through the `tenancy.Service.OnPropertyCreated` hook in its own transaction, and the migration backfills properties that existed before.
  - `internal/billingconfig` owns taxes, service charges, charge codes and the ordered rules, plus the `ChargeRuleResolver` (`ResolveRules`) that M6's calculation service reads. It computes no amounts.
  - `price_mode` and charge-type locks are the database triggers from migration 00010 (mapped to `PRICE_MODE_LOCKED`, `CHARGE_TYPE_LOCKED`); the service adds the clearer system-code and in-use checks.
  - Rule replacement deactivates then re-activates rows inside one transaction (the unique `(charge code, sequence)` index only covers active rows), and share-locks the mapped taxes so deactivation cannot race it.
  - sqlc now maps `numeric` to `decimal.Decimal` (nullable: `*decimal.Decimal`).
  - `POST {P}/charge-calculations` (the preview endpoint) belongs to M6.

- **M6** (Charge Calculation Engine) is complete; money and posting code may now build on it.
  - `internal/chargecalc` is pure (no database, clock or country knowledge): `Calculate`, `Validate` and `Verify`. Its tests are the worked examples of Step 7 plus property-based tests (fixed seeds, 30,000 random inputs each): an inclusive total always equals the quoted price, an exclusive net is base minus discount, negation symmetry, every component is exactly `round(base × rate)` of its own base, no amount exceeds the currency's decimals, the rounding adjustment stays within a few units of the smallest currency unit, and the result depends on the rules' sequence, not their list order. Mutation checks (bank rounding, unsigned discount, residual in tax, truncation, compounding) are each caught by these tests.
  - `billingconfig.Service.Calculate` is the `ChargeCalculationService`; `POST {P}/charge-calculations` previews it. The folio posting service (M9) and the reservation estimate (M8) must go through `Calculate`; nothing else multiplies by a rate.
  - The charge code screen has a calculator that previews the saved rules.

- **M7** (rate plans and rates) is complete. No migration: the tables exist in 00007.
  - `internal/rates` owns rate plans, the rate grid (bulk upsert by range, weekdays and room types) and the price lookup `NightlyPrices` that M8 uses to snapshot `reservation_room_rates`.
  - A plan that has rates cannot switch to a room charge code with another price mode (it would silently reprice the grid); this is enforced under a share lock on the plan, so a fill and a switch cannot interleave.
  - **Money precision (decided):** a property may use 0 to 3 currency decimals (IDR, USD, KWD). Migration 00015 widened every money column from `numeric(18,2)` to `numeric(18,3)` (keeping existing values exactly; its Down narrows and rounds, so only run it on data without a third decimal). The application rejects amounts with more decimals than the property's currency and amounts of 10^15 or more; API amounts are formatted with the currency's decimals.

- **M8** (availability and reservations) is complete. One migration, 00016 (`reservations.idempotency_key` and `idempotency_hash`, unique per property); the booking tables exist in 00008.
  - `internal/availability` is the inventory engine of 04-operations §14.1: `sellable` (active rooms minus active OOO/OOS blocks), `demand` (CONFIRMED lines by effective type plus open stays through tonight), `available = sellable - demand`. It also answers specific-room questions (`RoomIssues`: inactive, blocked, reserved, occupied) and lists free rooms. `rooms` uses it for the `INVENTORY_OVERSOLD` check.
  - `internal/reservations` implements create (draft, optionally confirmed at once), confirm, add and amend room, assign and unassign room, cancel (room or whole), reinstate, no-show, header edit, list and detail. Every mutation locks in the order business day (share) -> room types -> rooms -> reservation and lines, re-checks the `version`, bumps it, and audits. Sequences are taken last.
  - Nightly snapshots (`reservation_room_rates`) come from `rates.PriceNights`; overrides need `reservation.override_rate`. Estimates go through `billingconfig.Estimate`, which runs `chargecalc` per night.
  - **Decisions and deviations from 06-api.md §11-12:**
    - `POST {P}/reservations` needs the `Idempotency-Key` header. The key and a hash of the body are stored on the reservation: same key and body replays it, same key with another body is 422 `IDEMPOTENCY_KEY_REUSED`, and concurrent requests with one key converge on one reservation (unique index).
    - `unassign-room` takes the room type locks too (spec: `T[L4]` only): an upgraded line goes back to consuming its booked type, which must have the inventory.
    - Reinstate brings back only the rooms cancelled together with the reservation (same `cancelled_at`); a room cancelled on its own earlier stays cancelled. Cancelling clears the room assignment.
    - Cancel responses are `{reservation, folio_balance, requires_folio_resolution}`. The folio fields are computed from `folios` and `folio_items` (empty until M9).
    - Availability search lists a plan with incomplete grid as `missing_nights` and no estimate instead of failing.
    - The tape chart has its own read-only endpoint `GET {P}/tape-chart?from&to` (at most 62 days); the spec only named the screen. Checked-in lines are drawn in their line's room until M12 adds room moves.
    - Deposits (`POST .../deposits`) belong to M9 with the payment service.

- **M9** (folio core and payments) is complete. One migration, 00017: `approved_by` on `folio_items` and `payments`, with CHECKs (`folio_items_approval_ck`: ADJUSTMENT and REVERSAL items carry an approver and no other item does; `payments_approval_ck`: REFUND and VOIDED payments carry one and no other payment does) and the payments guard now lets `approved_by` change with POSTED -> VOIDED. The new permission is `correction.approve`.
  - `internal/iam/approval.go`: `Service.VerifyApproval` checks the approver's email and password with the login verifier and login rate limits (same limiters, an `approval|tenant|email` key), then `correction.approve` at the property (tenant administrators hold it everywhere). It returns an `iam.Approval` whose only constructor is that function, so posting code that takes an `Approval` knows a password was checked. Errors: 422 `APPROVAL_REQUIRED`, 401 `APPROVAL_INVALID_CREDENTIALS` (one generic error), 403 `APPROVAL_NOT_PERMITTED`, 429 `TOO_MANY_ATTEMPTS`. The password is never stored, audited or echoed (a test greps the audit log for it).
  - `internal/folios`: `posting.go` is `FolioPostingService` and the only writer of `folio_items` and components (charge, adjustment, payment and refund entries, reversal). `payments.go` is `PaymentService` (post, deposit, void, refund, cashier list). `charges.go` holds the charge, adjustment and reversal use cases. Every posting bumps `folios.version`, so a stale "close" is rejected.
  - **Decisions and deviations:**
    - The approval is verified **before** the transaction opens, not inside it (06-api.md §14.1 said inside): no lock is held while argon2 runs. A replay (same `Idempotency-Key`) is answered before the approval is checked.
    - Charges, adjustments, payments, deposits and refunds need `Idempotency-Key` (400 `IDEMPOTENCY_KEY_REQUIRED`). The key is stored on the item or payment; a replay returns the stored result, a key reused on another folio is 422 `IDEMPOTENCY_KEY_REUSED`, concurrent requests with one key converge on one row.
    - Manual charges refuse ROOM codes (409 `ROOM_CHARGE_REQUIRES_ROOM_POSTING`); adjustments may use them. Room postings arrive with M11.
    - A closed folio takes no postings (409 `FOLIO_CLOSED`); `folio.post_after_checkout` and `folio.reopen` are not used yet.
    - A payment is voided (same business date, no refunds, POSTED, PAYMENT type) with its ledger entry reversed and the payment marked VOIDED; an earlier date is refunded instead. Refunds check `amount <= original - refunds` under the original payment's row lock.
    - The reversal of a room-charge item flips its `stay_charge_postings` row to REVERSED (tested with a seeded register row).
    - A deposit locks the reservation, then finds or creates its open folio without a stay; the new folio is not locked again after its number is taken (lock order).
    - Payment lists: `GET {P}/payments?business_date&method` returns net totals per method only when `business_date` is given, voided payments excluded.
  - The frontend has the folio screen (items, components, balance, charge and payment forms), the shared Approval dialog (the approver's password lives only in the dialog and is cleared once handed over), the cashier screen, a folio list, and a deposit form on the reservation.

Earlier revisions are in [archive/](archive/).
