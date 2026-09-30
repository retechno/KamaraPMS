#!/usr/bin/env bash
# Verifies the migrations against a throwaway PostgreSQL:
#   1. apply every "goose Up" section in order
#   2. run the schema integrity tests (db/tests/schema_test.sql)
#   3. apply every "goose Down" section in reverse order and check the schema is empty
#   4. apply "Up" again (migrations are re-runnable after a full rollback)
#
# Modes:
#   docker   (default when Docker is available) a disposable container: PG_IMAGE=postgres:18-alpine to change version
#   external (when PMS_TEST_DATABASE_URL is set and Docker is not available, e.g. cloud sandboxes):
#            a temporary database on that server, dropped afterwards (needs psql and the CREATEDB privilege)
# Force a mode with DB_TEST_MODE=docker|external.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Sandboxes (scripts/cloud-setup.sh) record the test server in the repository's .env.
if [ -z "${PMS_TEST_DATABASE_URL:-}" ] && [ -f "$ROOT/.env" ]; then
  PMS_TEST_DATABASE_URL="$(sed -n 's/^PMS_TEST_DATABASE_URL=//p' "$ROOT/.env" | tail -1)"
fi
MODE="${DB_TEST_MODE:-}"
if [ -z "$MODE" ]; then
  if docker info >/dev/null 2>&1; then MODE=docker
  elif [ -n "${PMS_TEST_DATABASE_URL:-}" ]; then MODE=external
  else echo "db-test: Docker is not available and PMS_TEST_DATABASE_URL is not set" >&2; exit 1
  fi
fi

if [ "$MODE" = docker ]; then
  IMAGE="${PG_IMAGE:-postgres:16-alpine}"
  NAME="kamara-pms-dbtest-$$"
  cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
  trap cleanup EXIT
  echo "==> starting $IMAGE"
  docker run -d --name "$NAME" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=pms "$IMAGE" >/dev/null
  # Probe over TCP: the image's init-time server only listens on the unix socket.
  until docker exec "$NAME" pg_isready -h 127.0.0.1 -U postgres -d pms >/dev/null 2>&1; do sleep 1; done
  psql_run() { docker exec -i "$NAME" psql -X -q -U postgres -d pms -v ON_ERROR_STOP=1 "$@"; }
  TARGET_DESC="$IMAGE (docker)"
else
  : "${PMS_TEST_DATABASE_URL:?PMS_TEST_DATABASE_URL is required in external mode}"
  DBNAME="pms_schema_test_$$"
  base="${PMS_TEST_DATABASE_URL%%\?*}"          # postgres://user:pw@host:port/db
  query="${PMS_TEST_DATABASE_URL#"$base"}"      # ?sslmode=... (may be empty)
  TARGET="${base%/*}/$DBNAME$query"
  psql -X -q -v ON_ERROR_STOP=1 "$PMS_TEST_DATABASE_URL" -c "CREATE DATABASE $DBNAME" >/dev/null
  cleanup() { psql -X -q "$PMS_TEST_DATABASE_URL" -c "DROP DATABASE IF EXISTS $DBNAME WITH (FORCE)" >/dev/null 2>&1 || true; }
  trap cleanup EXIT
  psql_run() { psql -X -q "$TARGET" -v ON_ERROR_STOP=1 "$@"; }
  TARGET_DESC="PostgreSQL $(psql -X -tA "$TARGET" -c "SHOW server_version" | cut -d' ' -f1) (external server)"
fi

section() { awk -v want="$1" '/^-- \+goose Up/{s="up";next} /^-- \+goose Down/{s="down";next} s==want' "$2"; }

apply_up() {
  for f in "$ROOT"/migrations/*.sql; do
    echo "    up   $(basename "$f")"
    section up "$f" | psql_run
  done
}

echo "==> migrate up"
apply_up

echo "==> schema tests"
psql_run < "$ROOT/db/tests/schema_test.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
psql_run -c "SET client_min_messages = warning; DROP SCHEMA pms_test CASCADE" >/dev/null

echo "==> migrate down"
for f in $(ls "$ROOT"/migrations/*.sql | sort -r); do
  echo "    down $(basename "$f")"
  section down "$f" | psql_run
done
left=$(psql_run -tA -c "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
funcs=$(psql_run -tA -c "SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
                         WHERE n.nspname = 'public' AND p.prokind = 'f'
                           AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')")
if [ "$left" != "0" ] || [ "$funcs" != "0" ]; then
  echo "FAIL: down migrations left $left tables and $funcs functions" >&2
  exit 1
fi
echo "    schema empty after down"

echo "==> migrate up again"
apply_up

echo "==> OK: migrations up/down/up and schema tests passed on $TARGET_DESC"
