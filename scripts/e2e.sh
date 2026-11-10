#!/usr/bin/env bash
# End-to-end tests in a real browser (Playwright) against a database of their own.
#
#   scripts/e2e.sh                         # every module, then the night audit
#   scripts/e2e.sh e2e/modules/04-frontdesk.spec.ts   # arguments go to `playwright test`
#   scripts/e2e.sh --headed --workers=1    # watch it
#   E2E_KEEP_DB=1 scripts/e2e.sh ...       # run again on the database of the last run (no reset)
#
# What it does:
#   1. drops and creates the database pms_e2e (never the development database `pms`)
#   2. applies the migrations, creates the tenant E2E and its administrator
#   3. builds the API and the command line tools once into bin/e2e/
#   4. runs `npx playwright test` from web/: playwright.config.ts starts the API (port 18090) and vite (port 5174);
#      the project "setup" (e2e/setup/seed.setup.ts) creates the property, rooms, rates, users and demo data
#
# Database: PMS_E2E_DATABASE_URL (default postgres://pms:pms@localhost:55432/pms_e2e, the server of `docker compose up -d db`).
# The database is created with psql when it is on PATH, otherwise with `docker compose exec db psql` (Windows without psql).
# Windows: run it from Git Bash. Report: web/playwright-report/index.html (npx playwright show-report).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
log() { echo "[e2e] $*" >&2; }

export PMS_E2E_DATABASE_URL="${PMS_E2E_DATABASE_URL:-postgres://pms:pms@localhost:55432/pms_e2e?sslmode=disable}"
export E2E_TENANT="${E2E_TENANT:-E2E}"
export E2E_ADMIN_EMAIL="${E2E_ADMIN_EMAIL:-admin@e2e.example}"
export E2E_ADMIN_PASSWORD="${E2E_ADMIN_PASSWORD:-E2e-Admin-Passw0rd!}"
export E2E_USER_PASSWORD="${E2E_USER_PASSWORD:-E2e-User-Passw0rd!}"
export E2E_BIN="$ROOT/bin/e2e"
# The API of the tests reads these (the environment wins over .env: internal/platform/config/dotenv.go).
export PMS_DATABASE_URL="$PMS_E2E_DATABASE_URL"
export PMS_ENV=development
export PMS_HTTP_ADDR="${PMS_E2E_HTTP_ADDR:-127.0.0.1:18090}"
export PMS_RATE_LIMIT_PER_MINUTE=0
export PMS_SMTP_HOST=""   # no e-mail from the tests: the confirmation and the night audit pack say "e-mail is off"
export PMS_LOG_LEVEL="${PMS_LOG_LEVEL:-warn}"
if [ -z "${PMS_JWT_SECRET:-}" ]; then
  if [ -f .env ] && grep -q '^PMS_JWT_SECRET=..' .env; then :   # .env has one: the API reads it
  else export PMS_JWT_SECRET="e2e-only-secret-$(date +%s)-0123456789abcdefghijklmnopqrstuvwxyz"; fi
fi

base="${PMS_E2E_DATABASE_URL%%\?*}"   # postgres://user:pw@host:port/db
DBNAME="${base##*/}"
case "$DBNAME" in pms | "") log "refusing to use the database '$DBNAME': the e2e database must have its own name (pms_e2e)"; exit 1 ;; esac
case "$DBNAME" in *[!A-Za-z0-9_]*) log "the database name '$DBNAME' may only hold letters, digits and _"; exit 1 ;; esac
ADMIN_URL="${base%/*}/postgres"

psql_admin() {
  if command -v psql >/dev/null 2>&1; then
    psql -X -q -v ON_ERROR_STOP=1 -c "$1" "$ADMIN_URL"
  else
    MSYS_NO_PATHCONV=1 docker compose exec -T db psql -X -q -v ON_ERROR_STOP=1 -U pms -d postgres -c "$1"
  fi
}

ext=""; case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) ext=".exe" ;; esac
export E2E_EXE="$ext"

log "building bin/e2e (api, migrate, pms-admin, pms-seed)"
mkdir -p "$E2E_BIN"
go build -o "$E2E_BIN/" ./cmd/api ./cmd/migrate ./cmd/pms-admin ./cmd/pms-seed

if [ "${E2E_KEEP_DB:-}" != 1 ]; then
  log "recreating database $DBNAME"
  psql_admin "DROP DATABASE IF EXISTS $DBNAME WITH (FORCE)"
  psql_admin "CREATE DATABASE $DBNAME"
  log "migrations"
  "$E2E_BIN/migrate$ext" up >/dev/null
  log "tenant $E2E_TENANT and its administrator"
  "$E2E_BIN/pms-admin$ext" create-tenant -code "$E2E_TENANT" -name "Hotel E2E" -timezone Asia/Makassar >/dev/null
  E2E_PW="$E2E_ADMIN_PASSWORD" "$E2E_BIN/pms-admin$ext" create-admin -tenant "$E2E_TENANT" -email "$E2E_ADMIN_EMAIL" -name "Admin E2E" -password-env E2E_PW >/dev/null
fi

cd web
log "playwright test $*"
exec npx playwright test "$@"
