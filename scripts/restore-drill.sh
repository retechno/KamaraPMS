#!/usr/bin/env bash
# A restore drill: proves that a backup of a running database can be restored into a new, empty PostgreSQL and that the API runs on it. Run it before the pilot and
# then regularly (docs/backup-restore.md). It reads the source and changes nothing there; everything it creates is named kamarapms-backup-test and is removed at the end.
#
#   scripts/restore-drill.sh <source-postgres-container>          e.g. kamarapms-deploy-db-1
#   KEEP=1 scripts/restore-drill.sh ...                             leave the test containers running to look at them
#
#   DRILL_FROM=<file>   restore this file instead of making a backup of the source: the DRILL FROM THE SECONDARY COPY. An encrypted copy (.dump.age) needs DRILL_IDENTITY=<the private key file
#                       of the owner>; the restore then runs in the image of the backup service (kamarapms-backup:local: age, PostgreSQL 16 tools) on the network of the throwaway server, so the host
#                       needs no tools. The private key is mounted read-only into that one container and nowhere else.
#   DRILL_COMPARE=0     do not compare with the source (use it when the source changed since that backup was made; the comparison is only meaningful for an unchanged source)
#
# The times it prints are measured on the data it was given. They are not an RTO for production unless the data is of production size.
#
# Steps: backup of the source (scripts/db-backup.sh) -> a new empty PostgreSQL container of the same image -> restore (scripts/db-restore.sh) -> the row counts and row
# hashes of every table are compared with the source (scripts/db-verify.sql) -> if the API image exists (kamarapms-api:local): migrate up on the copy, the API starts on it,
# /healthz and /readyz answer. Exit 0 only when every step passed.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh
export MSYS_NO_PATHCONV=1

T0="$(date +%s)"
hostpath() { if command -v cygpath >/dev/null 2>&1; then cygpath -w "$1"; else echo "$1"; fi; } # Git Bash on Windows: a path Docker Desktop understands
elapsed() { echo "$(($(date +%s) - T0))s"; }
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

if [ -n "${DRILL_FROM:-}" ]; then
  echo "1. no new backup: restoring the file $DRILL_FROM (the secondary copy)"
  [ -f "$DRILL_FROM" ] || fail "DRILL_FROM $DRILL_FROM does not exist"
  DUMP="$DRILL_FROM"
else
  echo "1. backup of $SRC"
  PMS_PG_CONTAINER="$SRC" PMS_BACKUP_DIR="$WORK" bash scripts/db-backup.sh || fail "backup"
  DUMP="$(ls "$WORK"/pms-*.dump | head -1)"
fi
STAMP="$(basename "$DUMP" | sed -n 's/^pms-\([0-9]\{8\}T[0-9]\{6\}Z\).*/\1/p')"

echo "2. a new empty PostgreSQL ($IMAGE) as $NAME"
docker network create "$NAME" >/dev/null || fail "network"
docker run -d --name "$NAME" --network "$NAME" -e POSTGRES_USER=pms -e POSTGRES_PASSWORD="$PW" "$IMAGE" >/dev/null || fail "container"
for _ in $(seq 1 40); do docker exec "$NAME" pg_isready -h 127.0.0.1 -U pms >/dev/null 2>&1 && break; sleep 1; done
sleep 2

echo "3. restore"
if [ -n "${DRILL_FROM:-}" ]; then
  docker image inspect kamarapms-backup:local >/dev/null 2>&1 || fail "image kamarapms-backup:local is not built (docker compose -f deploy/compose.yaml build backup)"
  IDMOUNT=(); IDENV=()
  if [ -n "${DRILL_IDENTITY:-}" ]; then
    [ -f "$DRILL_IDENTITY" ] || fail "DRILL_IDENTITY $DRILL_IDENTITY does not exist"
    IDMOUNT=(-v "$(hostpath "$(dirname "$(realpath "$DRILL_IDENTITY")")"):/id:ro"); IDENV=(-e "PMS_BACKUP_AGE_IDENTITY=/id/$(basename "$DRILL_IDENTITY")")
  fi
  docker run --rm --network "$NAME" -v "$(hostpath "$(dirname "$(realpath "$DUMP")")"):/in:ro" "${IDMOUNT[@]}" "${IDENV[@]}" \
    -e PGHOST="$NAME" -e PGPORT=5432 -e PGUSER=pms -e PGPASSWORD="$PW" -e PMS_RESTORE_DB=pms \
    --entrypoint /opt/pms/scripts/db-restore.sh kamarapms-backup:local "/in/$(basename "$DUMP")" || fail "restore"
else
  PMS_PG_CONTAINER="$NAME" PGPASSWORD="$PW" PMS_RESTORE_DB=pms bash scripts/db-restore.sh "$DUMP" || fail "restore"
fi
echo "   restored after $(elapsed)"

echo "4. the source and the copy hold the same data"
PMS_PG_CONTAINER="$NAME" PGPASSWORD="$PW" pg psql -X -qAt -d pms <scripts/db-verify.sql >"$WORK/copy.txt" || fail "fingerprint of the copy"
if [ "${DRILL_COMPARE:-1}" = 0 ]; then
  echo "   not compared with the source (DRILL_COMPARE=0); the copy: $(tail -3 "$WORK/copy.txt" | tr '\n' ' ')"
else
  PMS_PG_CONTAINER="$SRC" pg psql -X -qAt -d "${PGDATABASE:-pms}" <scripts/db-verify.sql >"$WORK/source.txt" || fail "fingerprint of the source"
  if diff "$WORK/source.txt" "$WORK/copy.txt" >/dev/null; then echo "   identical: $(tail -3 "$WORK/copy.txt" | tr '\n' ' ')"; else diff "$WORK/source.txt" "$WORK/copy.txt" | head; fail "the copy differs from the source (a write since that backup would also show here: DRILL_COMPARE=0)"; fi
fi

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
if [ -n "$STAMP" ]; then
  then_s="$(date -u -d "${STAMP:0:4}-${STAMP:4:2}-${STAMP:6:2} ${STAMP:9:2}:${STAMP:11:2}:${STAMP:13:2}" +%s 2>/dev/null || echo 0)"
  [ "$then_s" -gt 0 ] && echo "   recovery point: the backup is $(( ($(date -u +%s) - then_s) / 60 )) minute(s) old now (the data lost if the source were lost at this moment)"
fi
echo "   recovery time measured by this drill (backup or file to API ready, on THIS data): $(elapsed)"
echo "RESTORE DRILL PASSED"
