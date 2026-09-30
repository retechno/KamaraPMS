# KamaraPMS: instructions for Claude Code

Multi-tenant hotel PMS: Go modular monolith + PostgreSQL + Vue 3/TypeScript. An independent, from-scratch
project: do not borrow conventions from other PMS products.

## Where things are decided

- `docs/architecture/`: the approved design. **Read it before changing behaviour.** Start with `README.md`
  (status). Key files: `02-database-schema.md`, `03-financial-engines.md`, `04-operations.md`,
  `05-transactions-locking.md`, `06-api.md`, `07-milestones.md`.
- Status: **M0, M1, M2, M3, M4, M5, M6, M7, M8, M9, M10, M11 done.** Next: **M12 (stay changes and check-out)**, then M13…M15
  in order. Build one milestone at a time; each has DB, backend, API, frontend and tests (see 07-milestones.md).
- Rejected decisions must not come back: `rooms.status`, `taxes.is_inclusive`, `payments.currency_code`,
  `properties.business_date`, microservices, reservation-header dates/room statuses.

## Setup

- **Cloud sandbox (Claude Code on the web):** the SessionStart hook runs `scripts/cloud-setup.sh`
  (PostgreSQL on 127.0.0.1:5432 pms/pms, `.env`, migrations). If `PMS_TEST_DATABASE_URL` is not in `.env`
  or the environment, run `scripts/cloud-setup.sh` yourself first. There is no Docker there.
- **Local (Windows, owner's machine):** Docker Desktop. Dev DB: `docker compose up -d db` (port 55432);
  API on 127.0.0.1:18080 (8080 is taken by a Windows service); `.env` is read automatically by `cmd/*`.

## Commands

```bash
go build ./... && go vet ./...
go test ./...                     # DB tests: testcontainers (Docker) or PMS_TEST_DATABASE_URL (own temp DB per package)
scripts/db-test.sh                # migrations up/down/up + 80 schema integrity tests
scripts/lint.sh                   # golangci-lint v2.14.0 (must report 0 issues)
scripts/sqlc.sh generate          # after editing any queries.sql; CI fails if generated code is stale
go run ./cmd/migrate up|down|status
go run ./cmd/pms-admin create-tenant|create-admin ...
cd web && npm test && npm run type-check && npm run build
cd web && npm run gen:api         # after editing api/openapi.yaml (the TS types are generated, never hand-edited)
```

Before saying a milestone or change is done, run: build, vet, lint, `go test ./...`, `scripts/db-test.sh`
(if migrations changed), `scripts/sqlc.sh diff`, and the web checks.

## Non-negotiable conventions

- **Business date ≠ server date.** Hotel dates come only from the OPEN `business_days` row
  (`tenancy.Service.CurrentBusinessDay` / `RequireOpenBusinessDay`). `time.Now` is lint-forbidden outside
  `internal/platform/clock`; use the injected `clock.Clock` for instants.
- **Dates are `civil.Date`, times of day `civil.TimeOfDay`**, never `time.Time`. Instants are UTC.
- **Money is `decimal.Decimal`** (never float; `decimal.NewFromFloat` is lint-forbidden), a string in JSON,
  `numeric(18,3)` in SQL, rounded half away from zero at `properties.currency_decimals` (0 to 3).
- **Transactions:** a use case opens `TxManager.WithinTx`; services join the ambient transaction and never
  commit. Row locks only via `db.LockRows` / `db.EnterLockLevel` (global lock order is enforced at runtime:
  business day → room types → rooms → reservations → stays → folios → payments → sequences).
- **Every business-dated write** first calls `RequireOpenBusinessDay(ctx, propertyID, db.ForShare, …)`.
- **Errors:** return `*apperr.Error` with a stable code. DB constraint names map to codes in
  `internal/platform/db/errors.go` (a test verifies every name exists). New constraint → add a mapping.
- **Tenancy isolation:** every query is scoped by `tenant_id`/`property_id`; composite FKs make cross-tenant
  references impossible. Another tenant's or unassigned property → 404 `PROPERTY_NOT_FOUND`, missing
  permission → 403 `PERMISSION_DENIED` (`auth.Authorizer`).
- **Audit:** every state change writes `audit.Writer.Write` in the same transaction.
- **Ledger:** folio items are append-only; corrections are reversals/adjustments. Only
  `ChargeCalculationEngine` computes tax/service; only `FolioPostingService` writes `folio_items`;
  only `RoomChargePostingService` posts room charges (design: 03-financial-engines.md).
- **Migrations:** never edit an applied migration; add a new numbered file (goose format, with Down).
- **Tests use real PostgreSQL** (`dbtest.Pool` + `dbtest.Reset`, `TestMain` → `dbtest.RunMain`). Constraint
  and locking behaviour must be tested, including races where relevant.
- **Module layout:** `internal/<module>/` with `model.go` (pure rules), `service.go` (use cases),
  `store.go` + `queries.sql` (+ generated `<module>db/`), `http.go` (thin handlers). Wire in `internal/app`.
- **Frontend:** access token in memory only (`api/session.ts`); refresh token is an httpOnly cookie.
  Mock the API client with a fresh `vi.fn()` per test (Vitest 5 quirk with mockReset/mockClear).
  TypeScript stays on 5.x (TS 7 lacks the JS API vue-tsc/openapi-typescript need).
- **Line endings LF** (`.gitattributes`); gofmt rejects CRLF.
- Never commit `.env`, `bin/`, or secrets.
