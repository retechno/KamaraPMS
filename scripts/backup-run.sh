#!/usr/bin/env bash
# One scheduled backup of the pilot: the verified backup (db-backup.sh), its encryption, the secondary copy, the retention of both, and the alert when something fails.
# The `backup` service of deploy/compose.yaml runs it every day (backup-agent.sh); `docker compose -f deploy/compose.yaml run --rm backup now` runs it once, which is the backup
# before an upgrade (docs/backup-restore.md, section 15).
#
#   database        PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE (the compose file sets them)
#   PMS_BACKUP_DIR              local backups (default /backups); PMS_BACKUP_KEEP: how many (default 14)
#   secondary copy              PMS_BACKUP_COPY_DIR or PMS_BACKUP_COPY_SSH (scripts/lib-backup.sh); PMS_BACKUP_COPY_KEEP_WEEKS (default 4), PMS_BACKUP_COPY_KEEP_RECENT (default 2)
#   encryption                  PMS_BACKUP_AGE_RECIPIENT: the age PUBLIC key(s), comma separated. The server never holds the private key: it can encrypt, it cannot decrypt.
#   alert                       PMS_BACKUP_ALERT_WEBHOOK, PMS_BACKUP_PING_URL (scripts/lib-backup.sh)
#   PMS_BACKUP_STATE_DIR        where the result of the last run is written (default /var/lib/pms-backup); the health check of the container reads it
#
# The secondary copy is mandatory for the pilot: without one the local backup is still made, but the run FAILS and says so (PMS_BACKUP_REQUIRE_COPY=0 turns that off for a trial).
# Exit codes: 0 done · 1 configuration · 2 the local backup failed · 3 no secondary copy configured · 4 encryption failed · 5 the secondary copy failed · 6 retention failed.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh
. scripts/lib-backup.sh

DIR="${PMS_BACKUP_DIR:-/backups}"
STATE="${PMS_BACKUP_STATE_DIR:-/var/lib/pms-backup}"
WEEKS="${PMS_BACKUP_COPY_KEEP_WEEKS:-4}"
RECENT="${PMS_BACKUP_COPY_KEEP_RECENT:-2}"
export PMS_BACKUP_DIR="$DIR"
mkdir -p "$STATE" 2>/dev/null || true

record() { # record <result> <message> : the state of this run, for the health check (written whole, then moved)
  local now; now="$(date -u +%s)"
  local last_ok=0
  [ -f "$STATE/status" ] && last_ok="$(sed -n 's/^last_success=//p' "$STATE/status")"
  [ "$1" = ok ] && last_ok="$now"
  { printf 'last_run=%s\nresult=%s\nlast_success=%s\nmessage=%s\n' "$now" "$1" "${last_ok:-0}" "$2"; } >"$STATE/status.tmp" 2>/dev/null && mv "$STATE/status.tmp" "$STATE/status" 2>/dev/null || true
}
fail() { # fail <code> <message>
  record failed "$2"
  alert FAILED "$2"
  exit "$1"
}

case "$WEEKS$RECENT" in "" | *[!0-9]*) fail 1 "PMS_BACKUP_COPY_KEEP_WEEKS and PMS_BACKUP_COPY_KEEP_RECENT must be numbers" ;; esac

log "backup run starts"
bash scripts/db-backup.sh || fail 2 "the local backup failed (see the log of the backup service)"
dump="$(ls -1 "$DIR"/pms-*.dump 2>/dev/null | sort | tail -1)"
[ -n "$dump" ] || fail 2 "the local backup left no file"
name="$(basename "$dump")"

# the secondary copy: mandatory, encrypted
if [ "$(copy_kind)" = none ]; then
  if [ "${PMS_BACKUP_REQUIRE_COPY:-1}" = 0 ]; then
    record ok "local backup $name only (no secondary copy, by choice)"; log "no secondary copy (PMS_BACKUP_REQUIRE_COPY=0)"; ping_alive; exit 0
  fi
  fail 3 "the local backup $name is made, but there is NO secondary copy configured (PMS_BACKUP_COPY_DIR or PMS_BACKUP_COPY_SSH): the pilot requires one"
fi
copy_check "$DIR" || fail 5 "the secondary copy cannot be used (see the log)"
[ -n "${PMS_BACKUP_AGE_RECIPIENT:-}" ] || fail 4 "PMS_BACKUP_AGE_RECIPIENT is not set: a copy that leaves the machine must be encrypted, so nothing was copied"
command -v age >/dev/null 2>&1 || fail 4 "the age program is not installed: nothing was copied"

umask 077
work="$(mktemp -d)" || fail 4 "no temporary directory"
trap 'rm -rf "$work"; rm -f "${SSH_KEY_COPY:-}"' EXIT
recips=()
IFS=',' read -r -a rlist <<<"$PMS_BACKUP_AGE_RECIPIENT"
for r in "${rlist[@]}"; do r="$(echo "$r" | tr -d ' ')"; [ -n "$r" ] && recips+=(-r "$r"); done
[ "${#recips[@]}" -gt 0 ] || fail 4 "PMS_BACKUP_AGE_RECIPIENT holds no key"
age "${recips[@]}" -o "$work/$name.age" "$dump" 2>"$work/age.err" || fail 4 "encryption failed ($(tr '\n' ' ' <"$work/age.err"))"
printf '%s  %s\n' "$(file_sha256 "$work/$name.age")" "$name.age" >"$work/$name.age.sha256"

copy_put "$work/$name.age" || fail 5 "copying $name.age to the secondary copy failed; the local backup is kept"
copy_put "$work/$name.age.sha256" || fail 5 "copying the checksum of $name.age failed"
log "secondary copy: $name.age ($(wc -c <"$work/$name.age" | tr -d ' ') bytes, encrypted) verified at the destination"

retention_apply "$WEEKS" "$RECENT" || fail 6 "retention of the secondary copy failed"
record ok "$name backed up, encrypted and copied"
ping_alive
log "backup run done: $name"
exit 0
