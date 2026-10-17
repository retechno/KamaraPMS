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

echo "==> migration 00069 on duplicate tasks"
psql_run < "$ROOT/db/tests/migration_00069_pre.sql"
section up "$(ls "$ROOT"/migrations/00069_*.sql)" | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    /p'
psql_run < "$ROOT/db/tests/migration_00069_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'

echo "==> migration 00071 (QRIS) on properties with and without the account 1155"
M71="$(ls "$ROOT"/migrations/00071_*.sql)"
section down "$M71" | psql_run
psql_run < "$ROOT/db/tests/migration_00071_pre.sql"
section up "$M71" | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    /p'
psql_run < "$ROOT/db/tests/migration_00071_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# the data step of the Up can run again: a property that has the key is left as it is
accounts_before=$(psql_run -tA -c "SELECT count(*) FROM gl_accounts")
section up "$M71" | awk '/^DO \$\$/,/^\$\$;/' | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    again: /p'
accounts_after=$(psql_run -tA -c "SELECT count(*) FROM gl_accounts")
if [ "$accounts_before" != "$accounts_after" ]; then echo "FAIL: running the data step of 00071 again added accounts ($accounts_before -> $accounts_after)" >&2; exit 1; fi
# the Down is refused while a property allows refunds by QRIS, and changes nothing
psql_run -c "UPDATE properties SET refund_methods = ARRAY['CASH', 'QRIS'] WHERE code = 'Q1'"
if section down "$M71" | psql_run >/dev/null 2>"${TMPDIR:-/tmp}/m71-down.err"; then echo "FAIL: the Down of 00071 ran while QRIS was in use" >&2; exit 1; fi
grep -q "cannot roll back 00071" "${TMPDIR:-/tmp}/m71-down.err" || { echo "FAIL: the Down of 00071 was refused for another reason:" >&2; cat "${TMPDIR:-/tmp}/m71-down.err" >&2; exit 1; }
echo "    down refused while QRIS is in use"
psql_run -c "UPDATE properties SET refund_methods = ARRAY['CASH'] WHERE code = 'Q1'"
# an account its user changed is not deleted by the Down
psql_run -c "UPDATE gl_accounts SET name = 'Akun e-money (diubah pengguna)' WHERE id = (SELECT account_id FROM gl_account_map WHERE map_key = 'QRIS' AND property_id = (SELECT id FROM properties WHERE code = 'Q4'))"
section down "$M71" | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    /p'
psql_run < "$ROOT/db/tests/migration_00071_down.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
section up "$M71" | psql_run >/dev/null

echo "==> migration 00076 (rights of the master folio) on roles with and without the rights it follows"
psql_run < "$ROOT/db/tests/migration_00076_pre.sql"
section up "$(ls "$ROOT"/migrations/00076_*.sql)" | psql_run
psql_run < "$ROOT/db/tests/migration_00076_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# running the Up again changes nothing (ON CONFLICT DO NOTHING)
rp_before=$(psql_run -tA -c "SELECT count(*) FROM role_permissions")
section up "$(ls "$ROOT"/migrations/00076_*.sql)" | psql_run
rp_after=$(psql_run -tA -c "SELECT count(*) FROM role_permissions")
if [ "$rp_before" != "$rp_after" ]; then echo "FAIL: running 00076 again changed the rights ($rp_before -> $rp_after)" >&2; exit 1; fi

echo "==> migration 00077 (the right to cancel a whole group) on roles with and without the right it follows"
psql_run < "$ROOT/db/tests/migration_00077_pre.sql"
section up "$(ls "$ROOT"/migrations/00077_*.sql)" | psql_run
psql_run < "$ROOT/db/tests/migration_00077_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
rp_before=$(psql_run -tA -c "SELECT count(*) FROM role_permissions")
section up "$(ls "$ROOT"/migrations/00077_*.sql)" | psql_run
rp_after=$(psql_run -tA -c "SELECT count(*) FROM role_permissions")
if [ "$rp_before" != "$rp_after" ]; then echo "FAIL: running 00077 again changed the rights ($rp_before -> $rp_after)" >&2; exit 1; fi

echo "==> migration 00084 (inventory) on properties with and without the accounts it adds"
M84="$(ls "$ROOT"/migrations/00084_*.sql)"
M85="$(ls "$ROOT"/migrations/00085_*.sql)"
M86="$(ls "$ROOT"/migrations/00086_*.sql)"
M87="$(ls "$ROOT"/migrations/00087_*.sql)"
# 00087 stands on 00086, 00086 on 00085 and 00085 on 00084: they go first, and come back after 00084 does
section down "$M87" | psql_run
section down "$M86" | psql_run
section down "$M85" | psql_run
section down "$M84" | psql_run
psql_run < "$ROOT/db/tests/migration_00084_pre.sql"
section up "$M84" | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    /p'
psql_run < "$ROOT/db/tests/migration_00084_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# the data step of the Up can run again: a property that has the key is left as it is
accounts_before=$(psql_run -tA -c "SELECT count(*) FROM gl_accounts")
section up "$M84" | awk '/^DO \$\$/,/^\$\$;/' | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    again: /p'
accounts_after=$(psql_run -tA -c "SELECT count(*) FROM gl_accounts")
if [ "$accounts_before" != "$accounts_after" ]; then echo "FAIL: running the data step of 00084 again added accounts ($accounts_before -> $accounts_after)" >&2; exit 1; fi
# the Down is refused while the module holds anything, and changes nothing
psql_run -c "INSERT INTO inventory_settings (tenant_id, property_id) SELECT tenant_id, id FROM properties WHERE code = 'I1'"
if section down "$M84" | psql_run >/dev/null 2>"${TMPDIR:-/tmp}/m84-down.err"; then echo "FAIL: the Down of 00084 ran while the inventory held data" >&2; exit 1; fi
grep -q "cannot roll back 00084" "${TMPDIR:-/tmp}/m84-down.err" || { echo "FAIL: the Down of 00084 was refused for another reason:" >&2; cat "${TMPDIR:-/tmp}/m84-down.err" >&2; exit 1; }
echo "    down refused while the inventory holds data"
psql_run -c "DELETE FROM inventory_settings"
# an account its user changed is not deleted by the Down
psql_run -c "UPDATE gl_accounts SET name = 'Beban susut (diubah pengguna)' WHERE id = (SELECT account_id FROM gl_account_map WHERE map_key = 'INV_WASTE' AND property_id = (SELECT id FROM properties WHERE code = 'I2'))"
section down "$M84" | psql_run 2>&1 | sed -E -n 's/^(psql:[^ ]* )?NOTICE:  /    /p'
psql_run < "$ROOT/db/tests/migration_00084_down.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
section up "$M84" | psql_run >/dev/null

echo "==> migration 00085 (purchasing) on the properties that exist"
section up "$M85" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00085_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# the Down is refused while purchasing holds anything (a chain setting is enough), and changes nothing
psql_run -c "INSERT INTO inv_approval_settings (tenant_id, property_id, doc_type) SELECT tenant_id, id, 'PR' FROM properties WHERE code = 'I1'"
if section down "$M85" | psql_run >/dev/null 2>"${TMPDIR:-/tmp}/m85-down.err"; then echo "FAIL: the Down of 00085 ran while purchasing held data" >&2; exit 1; fi
grep -q "cannot roll back 00085" "${TMPDIR:-/tmp}/m85-down.err" || { echo "FAIL: the Down of 00085 was refused for another reason:" >&2; cat "${TMPDIR:-/tmp}/m85-down.err" >&2; exit 1; }
echo "    down refused while purchasing holds data"
psql_run -c "DELETE FROM inv_approval_settings"
section down "$M85" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00085_down.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
section up "$M85" | psql_run >/dev/null

echo "==> migration 00086 (movements) on the properties that exist"
section up "$M86" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00086_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# the Down is refused while the movements hold anything (an amenities plan is enough), and changes nothing
psql_run -c "INSERT INTO inv_amenity_plans (tenant_id, property_id, version_no, effective_from, is_enabled) SELECT p.tenant_id, p.id, 1, current_date, false FROM properties p WHERE p.code = 'I1'"
if section down "$M86" | psql_run >/dev/null 2>"${TMPDIR:-/tmp}/m86-down.err"; then echo "FAIL: the Down of 00086 ran while the movements held data" >&2; exit 1; fi
grep -q "cannot roll back 00086" "${TMPDIR:-/tmp}/m86-down.err" || { echo "FAIL: the Down of 00086 was refused for another reason:" >&2; cat "${TMPDIR:-/tmp}/m86-down.err" >&2; exit 1; }
echo "    down refused while the movements hold data"
# an amenities plan is immutable, so the test clears the table the way the schema tests do: by truncating after switching the trigger off
psql_run -c "ALTER TABLE inv_amenity_plans DISABLE TRIGGER inv_amenity_plans_append_only" -c "DELETE FROM inv_amenity_plans" -c "ALTER TABLE inv_amenity_plans ENABLE TRIGGER inv_amenity_plans_append_only"
section down "$M86" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00086_down.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
section up "$M86" | psql_run >/dev/null

echo "==> migration 00087 (recipes and the POS deduction) on the properties that exist"
section up "$M87" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00087_post.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
# the Down is refused while the POS deduction holds anything (a category with a class is enough), and changes nothing
psql_run -c "INSERT INTO pos_categories (tenant_id, property_id, code, name, fb_class) SELECT p.tenant_id, p.id, 'M87', 'Migration 87', 'FOOD' FROM properties p WHERE p.code = 'I1'"
if section down "$M87" | psql_run >/dev/null 2>"${TMPDIR:-/tmp}/m87-down.err"; then echo "FAIL: the Down of 00087 ran while the POS deduction held data" >&2; exit 1; fi
grep -q "cannot roll back 00087" "${TMPDIR:-/tmp}/m87-down.err" || { echo "FAIL: the Down of 00087 was refused for another reason:" >&2; cat "${TMPDIR:-/tmp}/m87-down.err" >&2; exit 1; }
echo "    down refused while the POS deduction holds data"
psql_run -c "DELETE FROM pos_categories WHERE code = 'M87'"
section down "$M87" | psql_run >/dev/null
psql_run < "$ROOT/db/tests/migration_00087_down.sql" 2>&1 | sed -E -n -e 's/^(psql:[^ ]* )?NOTICE:  /    /p' -e '/ERROR/p'
section up "$M87" | psql_run >/dev/null

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
