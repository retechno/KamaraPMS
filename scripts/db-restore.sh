#!/usr/bin/env bash
# Restore a backup made by db-backup.sh into an EMPTY database, and check it. It never overwrites: a target that already has tables is refused.
#
#   PMS_PG_CONTAINER=kamarapms-backup-test PMS_RESTORE_DB=pms scripts/db-restore.sh backups/pms-20261007T120000Z.dump
#
#   PMS_RESTORE_DB      the target database (default pms_restore). It is created when it does not exist. It must be empty (no table in any schema except pg_ and information_schema).
#   PMS_PG_CONTAINER    run the tools inside this PostgreSQL container, or leave it out and use pg_restore and psql from the host with PGHOST, PGPORT, PGUSER, PGPASSWORD.
#
# Point it at a NEW PostgreSQL server or container (docs/backup-restore.md, section 5), never at the database that is in use. The script cannot know which server is the live
# one; the empty-target rule is the guard it can enforce. After the restore it reads the migration version, compares the number of tables with the table of contents of the
# dump, and prints the same fingerprint as scripts/db-verify.sql.
#
#   The file may be a plain backup or an encrypted one (.age, with PMS_BACKUP_AGE_IDENTITY).
#
# Exit codes: 0 restored and checked · 1 wrong usage · 2 the backup file is unusable (missing, checksum differs, pg_restore cannot read it) · 3 the server cannot be
# reached or the credentials are refused · 4 the target database is not empty · 5 pg_restore failed · 6 the restored database failed the checks.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh

FILE="${1:-}"
TARGET="${PMS_RESTORE_DB:-pms_restore}"
[ -n "$FILE" ] && [ "$#" = 1 ] || die 1 "usage: db-restore.sh <backup.dump>   (target: PMS_RESTORE_DB, server: PMS_PG_CONTAINER or PG* variables)"
safe_name "$TARGET" || die 1 "PMS_RESTORE_DB must be letters, digits and underscores"
case "$TARGET" in postgres | template0 | template1) die 1 "refusing to restore into the system database $TARGET" ;; esac

# 1. the file
[ -f "$FILE" ] && [ -s "$FILE" ] || die 2 "$FILE is missing or empty"
# an encrypted backup (the secondary copy: pms-<stamp>.dump.age): its checksum is checked first, then it is decrypted with the PRIVATE key of the owner (PMS_BACKUP_AGE_IDENTITY, a file the
# server never has) into a private temporary directory that is removed at the end
if [ "${FILE%.age}" != "$FILE" ]; then
  if [ -f "$FILE.sha256" ]; then
    [ "$(file_sha256 "$FILE")" = "$(cut -d' ' -f1 <"$FILE.sha256")" ] || die 2 "the checksum of $FILE differs from $FILE.sha256: the copy is damaged or was changed"
    echo "checksum of the encrypted file ok"
  else
    echo "warning: no $FILE.sha256, the checksum was not checked" >&2
  fi
  [ -r "${PMS_BACKUP_AGE_IDENTITY:-}" ] || die 2 "$FILE is encrypted: set PMS_BACKUP_AGE_IDENTITY to the file with the private key"
  command -v age >/dev/null 2>&1 || die 2 "the age program is needed to decrypt $FILE"
  umask 077
  TMPD="$(mktemp -d)" || die 2 "no temporary directory"
  trap 'rm -rf "$TMPD"' EXIT
  age -d -i "$PMS_BACKUP_AGE_IDENTITY" -o "$TMPD/restore.dump" "$FILE" 2>"$TMPD/age.err" || die 2 "cannot decrypt $FILE (wrong key, or damaged): $(tr '\n' ' ' <"$TMPD/age.err")"
  FILE="$TMPD/restore.dump"
  echo "decrypted"
elif [ -f "$FILE.sha256" ]; then
  want="$(cut -d' ' -f1 <"$FILE.sha256")"
  [ "$(file_sha256 "$FILE")" = "$want" ] || die 2 "the checksum of $FILE differs from $FILE.sha256: the file is damaged or was changed"
  echo "checksum ok"
else
  echo "warning: no $FILE.sha256, the checksum was not checked" >&2
fi
container_running || die 3 "container $PMS_PG_CONTAINER is not running (or does not exist)"
toc="$(pg pg_restore --list <"$FILE" 2>/dev/null)" || die 2 "pg_restore cannot read $FILE: not a backup of this kind, or damaged"
want_tables="$(printf '%s\n' "$toc" | grep -c ' TABLE public ' || true)"
[ "$want_tables" -gt 0 ] || die 2 "$FILE has no tables"

# 2. the server and the target
sql postgres "select 1" >/dev/null 2>&1 || die 3 "cannot connect to the server (is it running, and are the user and the password right?)"
exists="$(sql postgres "select count(*) from pg_database where datname = '$TARGET'")" || die 3 "cannot query the server"
if [ "$exists" = 0 ]; then
  sql postgres "create database $TARGET" >/dev/null || die 3 "cannot create the database $TARGET"
  echo "created database $TARGET"
else
  used="$(sql "$TARGET" "select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relkind in ('r','p','v','m','S','f') and n.nspname not like 'pg\_%' and n.nspname <> 'information_schema'")" || die 3 "cannot read the database $TARGET"
  [ "$used" = 0 ] || die 4 "database $TARGET is not empty ($used objects). Restore into a new, empty database or server; nothing was changed"
fi

# 3. the restore: it stops at the first error, so a partial restore is never reported as done
echo "restoring $FILE into $TARGET"
if ! pg pg_restore --exit-on-error --no-owner --no-acl -d "$TARGET" <"$FILE"; then
  die 5 "pg_restore failed. The database $TARGET is incomplete: drop it and start again. This is NOT a restored backup"
fi

# 4. the checks
version="$(sql "$TARGET" "select max(version_id) from goose_db_version")" || die 6 "no migration table in the restored database"
got_tables="$(sql "$TARGET" "select count(*) from pg_tables where schemaname = 'public'")" || die 6 "cannot count the tables"
[ "$got_tables" = "$want_tables" ] || die 6 "the restored database has $got_tables tables, the backup has $want_tables"
[ "${version:-0}" -gt 0 ] || die 6 "the migration version is empty"
echo "restored: migration version $version, $got_tables tables"
echo "fingerprint (the same query as scripts/db-verify.sql; run it on the source to compare):"
pg psql -X -v ON_ERROR_STOP=1 -qAt -d "$TARGET" <scripts/db-verify.sql | tail -3
exit 0
