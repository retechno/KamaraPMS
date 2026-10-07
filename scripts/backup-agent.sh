#!/usr/bin/env bash
# The scheduler of the pilot backup: the entry point of the `backup` service of deploy/compose.yaml. It needs no cron and no host feature, so it works wherever the stack runs.
#
#   backup-agent.sh            wait for the time of the day, run backup-run.sh, repeat (default)
#   backup-agent.sh now        one run now (the backup before an upgrade); exit code of the run
#   backup-agent.sh healthcheck   exit 0 when the last run succeeded less than 26 hours ago
#
#   PMS_BACKUP_AT          HH:MM, 24 hour clock, in the time zone TZ of the container (default 03:30, TZ default UTC). Choose a time after the night audit is normally done.
#   TZ                     e.g. Asia/Makassar; the container has the time zone database
#   PMS_BACKUP_CATCH_UP    1 (default): when the service starts and the last successful backup is older than 24 hours (or there is none), run at once, so a machine that was off at
#                          the scheduled time does not wait another day
#
# The result of every run is in PMS_BACKUP_STATE_DIR/status (written by backup-run.sh); a failure raises the alert there. This agent only decides WHEN.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib-pg.sh
. scripts/lib-backup.sh

STATE="${PMS_BACKUP_STATE_DIR:-/var/lib/pms-backup}"
AT="${PMS_BACKUP_AT:-03:30}"
MAXAGE=$((26 * 3600))

last_success() { [ -f "$STATE/status" ] && sed -n 's/^last_success=//p' "$STATE/status" || echo 0; }
last_result()  { [ -f "$STATE/status" ] && sed -n 's/^result=//p' "$STATE/status" || echo none; }

case "${1:-loop}" in
  now) exec bash scripts/backup-run.sh ;;
  healthcheck)
    now="$(date -u +%s)"
    if [ ! -f "$STATE/status" ]; then # no run yet: healthy only for the first 30 minutes after start
      [ -f "$STATE/started" ] && [ $((now - $(cat "$STATE/started"))) -lt 1800 ]
      exit $?
    fi
    [ "$(last_result)" = ok ] && [ $((now - $(last_success))) -lt "$MAXAGE" ]
    exit $?
    ;;
  loop) ;;
  *) echo "usage: backup-agent.sh [now|healthcheck|loop]" >&2; exit 1 ;;
esac

case "$AT" in [0-2][0-9]:[0-5][0-9]) ;; *) echo "PMS_BACKUP_AT must be HH:MM, got '$AT'" >&2; exit 1 ;; esac
mkdir -p "$STATE"
date -u +%s >"$STATE/started"
log "backup agent started: daily at $AT (${TZ:-UTC}); secondary copy: $(copy_kind); alert webhook: $([ -n "${PMS_BACKUP_ALERT_WEBHOOK:-}" ] && echo set || echo NOT SET)"
[ "$(copy_kind)" = none ] && alert WARNING "no secondary copy is configured: the daily run will fail until one is (the pilot requires it)"
[ -n "${PMS_BACKUP_ALERT_WEBHOOK:-}" ] || log "WARNING: no PMS_BACKUP_ALERT_WEBHOOK: a failure will only be in this log and in the health status of the container"

if [ "${PMS_BACKUP_CATCH_UP:-1}" = 1 ]; then
  age_s=$(($(date -u +%s) - $(last_success)))
  if [ "$age_s" -gt 86400 ]; then log "the last successful backup is older than 24 hours (or there is none): running now"; bash scripts/backup-run.sh || true; fi
fi

while true; do
  now="$(date +%s)"
  next="$(date -d "today $AT" +%s)"
  [ "$next" -le "$now" ] && next="$(date -d "tomorrow $AT" +%s)"
  log "next backup at $(date -d "@$next" '+%Y-%m-%d %H:%M %Z')"
  while [ "$(date +%s)" -lt "$next" ]; do
    left=$((next - $(date +%s)))
    [ "$left" -gt 60 ] && left=60
    sleep "$left"
  done
  bash scripts/backup-run.sh || true
  sleep 5
done
