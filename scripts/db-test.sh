#!/usr/bin/env bash
# Verifies the migrations against a throwaway PostgreSQL container:
#   1. apply every "goose Up" section in order
#   2. run the schema integrity tests (db/tests/schema_test.sql)
#   3. apply every "goose Down" section in reverse order and check the schema is empty
#   4. apply "Up" again (migrations are re-runnable after a full rollback)
# Usage: scripts/db-test.sh            (PG_IMAGE=postgres:17-alpine scripts/db-test.sh to change version)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${PG_IMAGE:-postgres:16-alpine}"
NAME="kamara-pms-dbtest-$$"

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> starting $IMAGE"
docker run -d --name "$NAME" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=pms "$IMAGE" >/dev/null
# Probe over TCP: the image's init-time server only listens on the unix socket.
until docker exec "$NAME" pg_isready -h 127.0.0.1 -U postgres -d pms >/dev/null 2>&1; do sleep 1; done

psql_run() { docker exec -i "$NAME" psql -X -q -U postgres -d pms -v ON_ERROR_STOP=1 "$@"; }
section()  { awk -v want="$1" '/^-- \+goose Up/{s="up";next} /^-- \+goose Down/{s="down";next} s==want' "$2"; }

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

echo "==> OK: migrations up/down/up and schema tests passed on $IMAGE"
