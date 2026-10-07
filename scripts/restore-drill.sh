#!/usr/bin/env bash
# A restore drill: proves that a backup of a running database can be restored into a new, empty PostgreSQL and that the API runs on it. Run it before the pilot and
# then regularly (docs/backup-restore.md). It reads the source and changes nothing there; everything it creates is named kamarapms-backup-test and is removed at the end.
#
#   scripts/restore-drill.sh <source-postgres-container>          e.g. kamarapms-deploy-db-1
#   KEEP=1 scripts/restore-drill.sh ...                             leave the test containers running to look at them
#
# Steps: backup of the source (scripts/db-backup.sh) -> a new empty PostgreSQL container of the same image -> restore (scripts/db-restore.sh) -> the row counts and row
# hashes of every table are compared with the source (scripts/db-verify.sql) -> if the API image exists (kamarapms-api:local): migrate up on the copy, the API starts on it,
# /healthz and /readyz answer. Exit 0 only when every step passed.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh
export MSYS_NO_PATHCONV=1

SRC="${1:-}"
[ -n "$SRC" ] || die 1 "usage: restore-drill.sh <source postgres container>"
docker inspect "$SRC" >/dev/null 2>&1 || die 1 "container $SRC not found"
NAME=kamarapms-backup-test   # never the name of a development or deployment project
for n in "$NAME" "$NAME-api"; do docker inspect "$n" >/dev/null 2>&1 && die 1 "$n exists already: remove it first (docker rm -f $n)"; done
WORK="$(mktemp -d)"
IMAGE="$(docker inspect --format '{{.Config.Image}}' "$SRC")"
PW="$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"
cleanup() {
  if [ "${KEEP:-0}" != 1 ]; then docker rm -f "$NAME" "$NAME-api" >/dev/null 2>&1; docker network rm "$NAME" >/dev/null 2>&1; fi
  rm -rf "$WORK"
}
trap cleanup EXIT
fail() { echo "DRILL FAILED: $*" >&2; exit 1; }

echo "1. backup of $SRC"
PMS_PG_CONTAINER="$SRC" PMS_BACKUP_DIR="$WORK" bash scripts/db-backup.sh || fail "backup"
DUMP="$(ls "$WORK"/pms-*.dump | head -1)"

echo "2. a new empty PostgreSQL ($IMAGE) as $NAME"
docker network create "$NAME" >/dev/null || fail "network"
docker run -d --name "$NAME" --network "$NAME" -e POSTGRES_USER=pms -e POSTGRES_PASSWORD="$PW" "$IMAGE" >/dev/null || fail "container"
for _ in $(seq 1 40); do docker exec "$NAME" pg_isready -h 127.0.0.1 -U pms >/dev/null 2>&1 && break; sleep 1; done
sleep 2

echo "3. restore"
PMS_PG_CONTAINER="$NAME" PGPASSWORD="$PW" PMS_RESTORE_DB=pms bash scripts/db-restore.sh "$DUMP" || fail "restore"

echo "4. the source and the copy hold the same data"
PMS_PG_CONTAINER="$SRC" pg psql -X -qAt -d "${PGDATABASE:-pms}" <scripts/db-verify.sql >"$WORK/source.txt" || fail "fingerprint of the source"
PMS_PG_CONTAINER="$NAME" PGPASSWORD="$PW" pg psql -X -qAt -d pms <scripts/db-verify.sql >"$WORK/copy.txt" || fail "fingerprint of the copy"
if diff "$WORK/source.txt" "$WORK/copy.txt" >/dev/null; then echo "   identical: $(tail -3 "$WORK/copy.txt" | tr '\n' ' ')"; else diff "$WORK/source.txt" "$WORK/copy.txt" | head; fail "the copy differs from the source (a write during the drill would also show here)"; fi

echo "5. the application on the copy"
if docker image inspect kamarapms-api:local >/dev/null 2>&1; then
  URL="postgres://pms:$PW@$NAME:5432/pms?sslmode=disable"
  docker run --rm --network "$NAME" --entrypoint /usr/local/bin/migrate -e PMS_DATABASE_URL="$URL" kamarapms-api:local up | tail -1 || fail "migrate up on the copy"
  docker run -d --name "$NAME-api" --network "$NAME" -e PMS_ENV=production -e PMS_HTTP_ADDR=:8080 -e PMS_DATABASE_URL="$URL" \
    -e PMS_JWT_SECRET="$(head -c 48 /dev/urandom | base64 | tr -d '\n')" -e PMS_LOG_FORMAT=json kamarapms-api:local >/dev/null || fail "api container"
  ok=0
  for _ in $(seq 1 30); do
    if docker exec "$NAME-api" /usr/local/bin/api healthcheck -ready >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  [ "$ok" = 1 ] || { docker logs "$NAME-api" 2>&1 | tail -5; fail "the API is not ready on the restored database"; }
  echo "   the API is ready on the restored database (/readyz: schema complete, database reachable)"
else
  echo "   skipped: image kamarapms-api:local not built (docker compose -f deploy/compose.yaml build api)"
fi
echo "RESTORE DRILL PASSED"
