# KamaraPMS

A multi-tenant hotel property management system: Go modular monolith, PostgreSQL, Vue 3 + TypeScript.

- Architecture and decisions: [`docs/architecture/`](docs/architecture/README.md)
- REST contract: [`docs/architecture/06-api.md`](docs/architecture/06-api.md) and [`api/openapi.yaml`](api/openapi.yaml) (source of the generated TypeScript client)
- Milestones: [`docs/architecture/07-milestones.md`](docs/architecture/07-milestones.md). Current state: **M2 (identity & access) complete**.

## Prerequisites

Go 1.26+, Node 22.12+, Docker (for the dev database and for database tests).

## Run locally
sssssssssssdssssssss
```bash
docker compose up -d db                      # PostgreSQL 16 on localhost:55432
cp .env.example .env                         # read automatically by cmd/api and cmd/migrate (run from the repo root)

go run ./cmd/migrate up                      # apply migrations (also: down, down-to N, status, version)
                                             # set PMS_JWT_SECRET in .env first (openssl rand -base64 48)
go run ./cmd/pms-admin create-tenant -code DEMO -name "Demo Hotels" -timezone Asia/Jakarta
go run ./cmd/pms-admin create-admin -tenant DEMO -email you@example.com -name "Your Name"
                                             # prints a one-time password; sign in and change it under Account
go run ./cmd/api                             # API on 127.0.0.1:18080 (/healthz, /readyz, /api/v1/...)
# Variables already set in the environment override .env (for deployments, CI, IDE run configs).

cd web && npm install && npm run dev         # UI on http://localhost:5173 (proxies the API)
```

The dev API port is 18080 because 8080 is often taken by other local services. Override with
`PMS_HTTP_ADDR` (API) and `PMS_API_URL` (Vite proxy target).

**Sign-in:** tenant code + email + password.
- The access token (a 15-minute JWT) is kept in memory only.
- The refresh token is an `httpOnly`, `SameSite=Strict` cookie scoped to `/api/v1/auth`. It rotates on every
  refresh, and replaying an old one ends all of that user's sessions.
- Every request re-checks the session in the database, so logout and deactivation take effect immediately.
- Local development uses `PMS_COOKIE_SECURE=false` (plain `http://localhost`). Production refuses it.

## Cloud / sandboxes without Docker

`scripts/cloud-setup.sh` prepares a Linux sandbox: it installs and starts PostgreSQL, writes `.env`
(including `PMS_TEST_DATABASE_URL`) and applies migrations. Claude Code on the web runs it automatically
(SessionStart hook in `.claude/settings.json`). Tests, `scripts/db-test.sh`, `scripts/sqlc.sh` and
`scripts/lint.sh` all work without Docker. See `CLAUDE.md` for the working conventions.

## Test

```bash
go test ./...                 # unit + integration tests; DB tests start PostgreSQL via testcontainers
go test -short ./...          # skip database tests
scripts/db-test.sh            # migrations up/down/up + 80 schema integrity tests (PG_IMAGE=postgres:18-alpine to change version)

cd web && npm test && npm run type-check && npm run build
```

Lint (pinned golangci-lint v2.14.0; Docker, local binary or `go run`):

```bash
scripts/lint.sh
```

## Layout

```
cmd/api, cmd/migrate        binaries (wiring only)
cmd/pms-admin               operator CLI (create tenants and their first administrator)
internal/app                composition root (router, middleware chain) + end-to-end API tests
internal/iam                login, sessions, users, roles, grants, Authorizer (per-property permissions)
internal/tenancy            tenants, properties, business days (BusinessDayService), document numbers
internal/audit              audit trail writer (same transaction as the change)
internal/platform/          technical kernel, no business rules:
  apperr                    error model with stable codes
  auth                      Principal, permission catalogue, Authorizer interface
  civil                     civil.Date / civil.TimeOfDay: hotel dates that time zones cannot shift
  config, logging, clock    environment config, slog, server-time clock
  db                        pool, TxManager (ambient tx), lock-order enforcement, DB error mapping
  dbtest                    real PostgreSQL for tests (testcontainers)
  httpx, health             RFC 9457 problems, strict JSON, middleware, pagination, probes
  migrate, money            embedded goose migrations, decimal rounding
migrations/                 SQL schema (goose), embedded into the binaries
db/tests/                   SQL schema integrity tests
api/openapi.yaml            API contract
web/                        Vue 3 + TypeScript app
```

## Conventions worth knowing

- **Never use the server date as the hotel date.** Business dates come from the OPEN `business_days` row;
  `time.Now` is forbidden by the linter outside `internal/platform/clock`.
- **SQL lives in `queries.sql`** per module; `scripts/sqlc.sh generate` (Docker) regenerates the typed Go code.
- **Line endings are LF** (`.gitattributes`, `.editorconfig`); editors on Windows otherwise write CRLF, which gofmt rejects.
- **Money is decimal**, never float (`decimal.NewFromFloat` is forbidden by the linter), and is a string in JSON.
- **Transactions**: use cases run in `TxManager.WithinTx`; services join the ambient transaction and never
  commit. Row locks go through `db.LockRows`, which enforces the global lock order at runtime.
- **Errors**: return `*apperr.Error` with a stable code; database constraint violations are mapped to codes in
  `internal/platform/db/errors.go` (a test checks every mapped name exists in the schema).
- **Frontend tests** that mock the API client: create a fresh `vi.fn()` per test instead of calling
  `mockReset()`/`mockClear()` on a shared one. With Vitest 5, clearing a shared mock between tests made
  handled rejections fail later tests; the cause was not identified.
