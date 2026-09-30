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
  - Deferred to M8, where the availability engine lives: the type-level `available ≥ 0` check (`INVENTORY_OVERSOLD`) on blocks, type changes and deactivation. Room type deactivation only checks active rooms and future CONFIRMED lines of the type, as specified.
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

Earlier revisions are in [archive/](archive/).
