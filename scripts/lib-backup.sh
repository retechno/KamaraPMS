#!/usr/bin/env bash
# Shared by backup-run.sh and backup-agent.sh (sourced, not run): the failure alert, the secondary copy (a directory or an SSH host), and the retention of the secondary copy.
# Needs GNU date (coreutils), curl, age and, for an SSH destination, ssh. The image of the `backup` service of deploy/compose.yaml has them.

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
# The alert. PMS_BACKUP_ALERT_WEBHOOK is any URL that takes a JSON POST ({"text": ..., "content": ...}: the two names that Slack-style and Discord-style incoming webhooks read; services such
# as ntfy, Mattermost or a small script of your own can read it too). The URL usually holds a token: it is read from the environment and never logged. A failing alert is logged and
# does not change the result of the backup. PMS_BACKUP_PING_URL is a "backup is alive" address (any dead-man's-switch service, or your own): it is requested after every success, so
# that a backup that stops running at all, which no script can report itself, is noticed by the receiver.

json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr '\n\r\t' '   '; }

alert() { # alert <level> <message>
  local level="$1" msg="$2" host text
  host="$(hostname 2>/dev/null || echo unknown)"
  text="KamaraPMS backup $level on $host: $msg"
  log "$text"
  [ -n "${PMS_BACKUP_ALERT_WEBHOOK:-}" ] || return 0
  local body
  body="{\"text\":\"$(json_escape "$text")\",\"content\":\"$(json_escape "$text")\",\"level\":\"$level\",\"service\":\"kamarapms-backup\",\"host\":\"$(json_escape "$host")\"}"
  if ! curl -fsS --max-time 20 --retry 2 --retry-delay 3 -o /dev/null -H 'Content-Type: application/json' -d "$body" "$PMS_BACKUP_ALERT_WEBHOOK" 2>/dev/null; then
    log "the alert could not be delivered to the webhook (the backup result is unchanged)"
  fi
  return 0
}

ping_alive() {
  [ -n "${PMS_BACKUP_PING_URL:-}" ] || return 0
  curl -fsS --max-time 20 --retry 2 --retry-delay 3 -o /dev/null "$PMS_BACKUP_PING_URL" 2>/dev/null || log "the success ping could not be delivered"
  return 0
}

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
# The secondary copy. A destination is either
#   PMS_BACKUP_COPY_DIR   a directory (a mounted second disk, a NAS or USB share, a folder that something else replicates), or
#   PMS_BACKUP_COPY_SSH   user@host:/absolute/path, reached with PMS_BACKUP_SSH_KEY (a private key file) and PMS_BACKUP_SSH_KNOWN_HOSTS (a known_hosts file: the host key is checked, never
#                         accepted blindly), optionally PMS_BACKUP_SSH_PORT. Nothing about a provider is assumed: only a directory or a host that takes ssh.

copy_kind() {
  if [ -n "${PMS_BACKUP_COPY_DIR:-}" ] && [ -n "${PMS_BACKUP_COPY_SSH:-}" ]; then echo both; return; fi
  if [ -n "${PMS_BACKUP_COPY_DIR:-}" ]; then echo dir; return; fi
  if [ -n "${PMS_BACKUP_COPY_SSH:-}" ]; then echo ssh; return; fi
  echo none
}

SSH_HOST=""; SSH_PATH=""
ssh_parse() {
  SSH_HOST="${PMS_BACKUP_COPY_SSH%%:*}"
  SSH_PATH="${PMS_BACKUP_COPY_SSH#*:}"
  case "$SSH_HOST" in "" | *[!A-Za-z0-9_.@-]*) return 1 ;; esac
  case "$SSH_PATH" in /*) ;; *) return 1 ;; esac
  case "$SSH_PATH" in *[!A-Za-z0-9_./-]*) return 1 ;; esac
  [ -r "${PMS_BACKUP_SSH_KEY:-}" ] && [ -r "${PMS_BACKUP_SSH_KNOWN_HOSTS:-}" ] || return 1
  # ssh refuses a key file that other users can read, and a file mounted from some hosts looks readable to everybody: it is used from a private copy (SSH_KEY_COPY, removed by the caller's exit trap)
  if [ -z "${SSH_KEY_COPY:-}" ]; then
    SSH_KEY_COPY="$(mktemp)" && chmod 600 "$SSH_KEY_COPY" && cat "$PMS_BACKUP_SSH_KEY" >"$SSH_KEY_COPY" || return 1
  fi
}
ssh_run() { # ssh_run <remote command...>
  ssh -i "$SSH_KEY_COPY" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$PMS_BACKUP_SSH_KNOWN_HOSTS" -o ConnectTimeout=20 \
    -p "${PMS_BACKUP_SSH_PORT:-22}" "$SSH_HOST" "$@"
}

# copy_check: the destination can be used. A directory must exist, be writable and be on another file system than the local backups (a second directory on the same disk is not a second
# copy); PMS_BACKUP_COPY_ALLOW_SAME_DISK=1 lifts that last rule for a trial and is said in the log.
copy_check() { # copy_check <local backup dir>
  case "$(copy_kind)" in
    none) log "no secondary copy is configured"; return 1 ;;
    both) log "set PMS_BACKUP_COPY_DIR or PMS_BACKUP_COPY_SSH, not both"; return 1 ;;
    dir)
      [ -d "$PMS_BACKUP_COPY_DIR" ] && [ -w "$PMS_BACKUP_COPY_DIR" ] || { log "the secondary copy directory $PMS_BACKUP_COPY_DIR is missing or not writable"; return 1; }
      if [ "$(stat -c %d "$PMS_BACKUP_COPY_DIR")" = "$(stat -c %d "$1")" ]; then
        if [ "${PMS_BACKUP_COPY_ALLOW_SAME_DISK:-0}" = 1 ]; then log "WARNING: the secondary copy is on the same file system as the backups: NOT a second copy (trial only)"
        else log "the secondary copy directory is on the same file system as the local backups: that is not a second copy. Mount another disk or share (or set PMS_BACKUP_COPY_ALLOW_SAME_DISK=1 for a trial)"; return 1; fi
      fi ;;
    ssh)
      ssh_parse || { log "PMS_BACKUP_COPY_SSH must be user@host:/absolute/path, with a readable PMS_BACKUP_SSH_KEY and PMS_BACKUP_SSH_KNOWN_HOSTS"; return 1; }
      ssh_run "test -d '$SSH_PATH' && test -w '$SSH_PATH'" || { log "cannot reach $SSH_HOST or $SSH_PATH is not a writable directory there"; return 1; } ;;
  esac
}

# copy_put <file>: the file lands under its own name, through a temporary name, and its SHA-256 is checked at the destination.
copy_put() {
  local f="$1" name sum remote
  name="$(basename "$f")"; sum="$(file_sha256 "$f")"
  case "$(copy_kind)" in
    dir)
      cp "$f" "$PMS_BACKUP_COPY_DIR/.$name.partial" && mv "$PMS_BACKUP_COPY_DIR/.$name.partial" "$PMS_BACKUP_COPY_DIR/$name" || { rm -f "$PMS_BACKUP_COPY_DIR/.$name.partial"; return 1; }
      [ "$(file_sha256 "$PMS_BACKUP_COPY_DIR/$name")" = "$sum" ] || { log "the copy of $name differs from the original"; return 1; } ;;
    ssh)
      ssh_run "cat > '$SSH_PATH/.$name.partial' && mv '$SSH_PATH/.$name.partial' '$SSH_PATH/$name'" <"$f" || { ssh_run "rm -f '$SSH_PATH/.$name.partial'" || true; return 1; }
      remote="$(ssh_run "sha256sum '$SSH_PATH/$name'" | cut -d' ' -f1)"
      [ "$remote" = "$sum" ] || { log "the copy of $name at $SSH_HOST differs from the original"; return 1; } ;;
  esac
}

copy_list() { # the names in the destination, one per line
  case "$(copy_kind)" in
    dir) ls -1 "$PMS_BACKUP_COPY_DIR" ;;
    ssh) ssh_run "ls -1 '$SSH_PATH'" ;;
  esac
}
copy_rm() {
  case "$(copy_kind)" in
    dir) rm -f "$PMS_BACKUP_COPY_DIR/$1" ;;
    ssh) ssh_run "rm -f '$SSH_PATH/$1'" ;;
  esac
}

# ---------------------------------------------------------------------------------------------------------------------------------------------------------------
# Retention of the secondary copy (pilot): the newest backup of each of the WEEKS most recent ISO weeks (default 4), and the RECENT newest backups (default 2) so that the recovery point
# stays 24 hours when only the secondary survives. Everything else named pms-<stamp>.dump.age is removed, with its .sha256. Other files are never touched.

stamp_of() { local n="${1#pms-}"; echo "${n%%.*}"; } # 20261007T071236Z
week_of() { local s; s="$(stamp_of "$1")"; date -u -d "${s:0:4}-${s:4:2}-${s:6:2}" +%G-%V; }

# retention_keep <weeks> <recent>: reads the backup names (any order) on stdin, prints the names to KEEP: the newest <recent> names, and the newest name of each of the <weeks> most recent
# ISO weeks that have a backup (a week without a backup does not use up one of the <weeks>).
retention_keep() {
  local weeks="$1" recent="$2" n=0 weekcount=0 name wk
  local -A seen=()
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    n=$((n + 1))
    wk="$(week_of "$name")"
    if [ -z "${seen[$wk]+x}" ]; then
      seen[$wk]=1
      weekcount=$((weekcount + 1))
      if [ "$weekcount" -le "$weeks" ]; then echo "$name"; continue; fi
    fi
    if [ "$n" -le "$recent" ]; then echo "$name"; fi
  done < <(sort -r)
}

# retention_apply <weeks> <recent>: removes what retention_keep does not keep
retention_apply() {
  local weeks="$1" recent="$2" all keep name
  all="$(copy_list | grep -E '^pms-[0-9]{8}T[0-9]{6}Z\.dump\.age$' || true)"
  [ -n "$all" ] || return 0
  keep="$(printf '%s\n' "$all" | retention_keep "$weeks" "$recent")"
  while IFS= read -r name; do
    if ! printf '%s\n' "$keep" | grep -qx "$name"; then
      copy_rm "$name" && copy_rm "$name.sha256" && log "retention: removed $name from the secondary copy"
    fi
  done <<<"$all"
}
