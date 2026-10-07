#!/usr/bin/env bash
# Shared by db-backup.sh and db-restore.sh (sourced, not run). It runs the PostgreSQL client tools either
#
#   - inside a running PostgreSQL container (PMS_PG_CONTAINER=<name>): the tools then always match the server version, and nothing has to be installed on the host; or
#   - from the host (pg_dump, pg_restore and psql on PATH), with the usual libpq variables PGHOST, PGPORT, PGUSER, PGPASSWORD (or PGPASSFILE).
#
# The password is never an argument of a command (it would show in the process list and in the history): it is read from the environment of this process, and in
# container mode `docker exec -e PGPASSWORD` passes it from that environment without writing it. Without PGUSER and PGPASSWORD, container mode uses POSTGRES_USER and
# POSTGRES_PASSWORD of the container itself.

pg_mode() { if [ -n "${PMS_PG_CONTAINER:-}" ]; then echo container; else echo host; fi; }

# pg <tool> <args...>: run a client tool; stdin and stdout are passed through (binary safe).
pg() {
  if [ -n "${PMS_PG_CONTAINER:-}" ]; then
    MSYS_NO_PATHCONV=1 docker exec -i -e PGUSER -e PGPASSWORD -e PGDATABASE -e PGOPTIONS "$PMS_PG_CONTAINER" sh -c '
      export PGHOST="$(hostname -i 2>/dev/null || echo 127.0.0.1)"   # the container own address: the password is checked (127.0.0.1 is trusted by the official image)
      export PGUSER="${PGUSER:-$POSTGRES_USER}"
      export PGPASSWORD="${PGPASSWORD:-$POSTGRES_PASSWORD}"
      exec "$@"' sh "$@"
  else
    "$@"
  fi
}

# sql <database> <statement>: one statement, plain output without headers or alignment.
sql() { pg psql -X -v ON_ERROR_STOP=1 -qAt -d "$1" -c "$2"; }

die() { local code="$1"; shift; echo "error: $*" >&2; exit "$code"; }

# a name that is safe to put in a statement and a file name
safe_name() { case "$1" in "" | *[!A-Za-z0-9_]*) return 1 ;; *) return 0 ;; esac; }

# a hash of a file that works with GNU and BSD tools
file_sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# container_running: in container mode, the container must exist and be running (otherwise the tools cannot run at all)
container_running() {
  [ -z "${PMS_PG_CONTAINER:-}" ] && return 0
  [ "$(docker inspect --format '{{.State.Running}}' "$PMS_PG_CONTAINER" 2>/dev/null)" = true ]
}
