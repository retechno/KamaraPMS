#!/usr/bin/env bash
# Backup of the KamaraPMS database: one custom-format, compressed pg_dump file, verified before it is kept.
#
#   PMS_PG_CONTAINER=kamarapms-deploy-db-1 scripts/db-backup.sh        # tools inside the database container (nothing to install)
#   PGHOST=... PGUSER=... PGPASSWORD=... scripts/db-backup.sh          # tools on this machine
#
#   PGDATABASE          the database to back up (default pms)
#   PMS_BACKUP_DIR      where the files go (default ./backups, created 0700; the files are 0600)
#   PMS_BACKUP_KEEP     how many backups to keep, newest first (default 14; 0 keeps everything). Older ones are removed only after this one succeeded.
#
# The file is pms-<UTC timestamp>.dump, with pms-<timestamp>.dump.sha256 next to it. It is written under a temporary name and renamed only after it passed the checks, so
# a half-written or unreadable file never looks like a backup. Nothing but the database is in it: no JWT secret, no .env, no application secret (those are environment
# variables of the deployment). The database itself holds password hashes, refresh-token hashes and guest data, so treat the file as confidential (docs/backup-restore.md).
#
# Exit codes: 0 done · 1 wrong usage or configuration · 2 pg_dump failed · 3 the dump did not pass verification · 4 the server cannot be reached (down, wrong credentials, no such database).
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh

DB="${PGDATABASE:-pms}"
DIR="${PMS_BACKUP_DIR:-./backups}"
KEEP="${PMS_BACKUP_KEEP:-14}"
safe_name "$DB" || die 1 "PGDATABASE must be letters, digits and underscores"
case "$KEEP" in "" | *[!0-9]*) die 1 "PMS_BACKUP_KEEP must be a number" ;; esac
container_running || die 4 "container $PMS_PG_CONTAINER is not running (or does not exist)"
sql "$DB" "select 1" >/dev/null 2>&1 || die 4 "cannot connect to database '$DB' (server down, wrong user or password, or no such database)"

umask 077
mkdir -p "$DIR" 2>/dev/null && chmod 700 "$DIR" 2>/dev/null || true
[ -d "$DIR" ] && [ -w "$DIR" ] || die 1 "the backup directory $DIR cannot be created or written"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
final="$DIR/pms-$stamp.dump"
part="$DIR/.pms-$stamp.dump.partial"
[ -e "$final" ] && die 1 "$final exists already (two backups in one second?)"
trap 'rm -f "$part"' EXIT

echo "backup of database '$DB' ($(pg_mode) mode) to $final"
if ! pg pg_dump -Fc -Z 6 --no-owner --no-acl -d "$DB" >"$part"; then
  die 2 "pg_dump failed (see the message above); nothing was kept"
fi

# verification 1: pg_restore can read the whole table of contents; 2: it has the migration table and the tables of the schema
toc="$(pg pg_restore --list <"$part")" || die 3 "the dump cannot be read back by pg_restore; nothing was kept"
tables="$(printf '%s\n' "$toc" | grep -c ' TABLE public ' || true)"
data="$(printf '%s\n' "$toc" | grep -c ' TABLE DATA public ' || true)"
case "$toc" in *' TABLE DATA public goose_db_version '*) ;; *) die 3 "the dump has no goose_db_version data: not a KamaraPMS database? Nothing was kept" ;; esac
[ "$tables" -gt 0 ] && [ "$data" = "$tables" ] || die 3 "the dump has $tables tables but data for $data of them; nothing was kept"

sum="$(file_sha256 "$part")"
mv "$part" "$final" && printf '%s  %s\n' "$sum" "$(basename "$final")" >"$final.sha256" || die 2 "could not write $final"
chmod 600 "$final" "$final.sha256" 2>/dev/null || true
trap - EXIT
version="$(sql "$DB" "select max(version_id) from goose_db_version" 2>/dev/null || echo "?")"
echo "done: $(basename "$final"), $(wc -c <"$final" | tr -d ' ') bytes, $tables tables, migration version $version, sha256 $sum"

# retention: newest KEEP stay (a file and its checksum go together)
if [ "$KEEP" -gt 0 ]; then
  n=0
  for f in $(ls -1 "$DIR"/pms-*.dump 2>/dev/null | sort -r); do
    n=$((n + 1))
    if [ "$n" -gt "$KEEP" ]; then rm -f "$f" "$f.sha256" && echo "retention: removed $(basename "$f")"; fi
  done
fi
exit 0
