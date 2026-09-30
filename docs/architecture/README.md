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
  - Charge-code seeding on property creation moves to M5, where charge codes live.
- **M2** (identity and access) is complete. JWT access tokens (15 minutes, in memory) and rotating refresh tokens in an httpOnly `SameSite=Strict` cookie.
  - Refresh-token reuse ends all of a user's sessions. Migration 00012 adds `user_sessions.revoked_reason`, so detection only fires for rotated tokens, not for a stale tab after logout. There is a 30-second grace window for tabs that refresh at the same moment.
  - Sessions are re-checked on every request, so revocation is immediate.
  - Passwords use argon2id (OWASP parameters). Login gives one generic error, has timing equalisation and is rate-limited (in-memory per instance, Redis later).
  - Authorization is per property: 404 without access, 403 without the permission. Users, roles and property creation are for tenant administrators.
  - The M1 development header login was removed.

Earlier revisions are in [archive/](archive/).
